package library

import (
	"cmp"
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"sync"
	"unicode"

	"golang.org/x/sync/errgroup"

	"github.com/tiaot33/mechbox/internal/library/internal/ai"
	"github.com/tiaot33/mechbox/internal/library/internal/store"
)

const translateConcurrency = 4

var languageTag = regexp.MustCompile(`^[A-Za-z]{2,8}(-[A-Za-z0-9]{1,8})*$`)

// TranslateDescription 用数据库里的 AI 设置把技能当前版本的描述翻译成 language(BCP 47 语言标签,如界面语言 zh、en),
// 存进数据库并返回译文;之后 [Skill].DescriptionTranslations 里就有它。已有译文时也重新翻译、覆盖(界面上点「翻译」)。
// language 不是语言标签时返回 InvalidArgument(Details.Field 为 language);技能不存在返回 NotFound;描述为空返回 FailedPrecondition;
// AI 设置里没填模型返回 NotConfigured;服务的回答按 [Library.TestAISettings] 的规则归类,回答被 max_tokens 截断时同样不存。最多等 2 分钟。
func (l *Library) TranslateDescription(ctx context.Context, id SkillID, language string) (translation string, err error) {
	defer func() { err = typedError(err) }()
	if err := validateLanguage("translate description", language); err != nil {
		return "", err
	}
	read, unlock := l.sharedOperation(ctx)
	row, err := l.store.GetSkill(read, int64(id))
	if err != nil {
		unlock()
		return "", err
	}
	settings, err := l.translateSettings(read)
	unlock()
	if err != nil {
		return "", err
	}
	if row.Description == "" {
		return "", preconditionError("translate description", "skill has no description", ErrorDetails{SkillID: id})
	}
	return l.translate(ctx, settings, row, language)
}

// TranslateDescriptionsResult 是一次自动翻译的结果。
type TranslateDescriptionsResult struct {
	// Translated 是这次翻译并存下了几个。
	Translated int `json:"translated"`
	// Failed 是因为描述本身没翻译成的技能:服务拒绝了这一条(如内容过滤)、回答被 max_tokens 截断。
	// 其余技能照常翻译;界面这次不再交它们,说明是哪几个。
	Failed []TranslationFailure `json:"failed,omitempty"`
}

// TranslationFailure 是自动翻译里没翻译成的一个技能。
type TranslationFailure struct {
	SkillID SkillID `json:"skill_id"`
	Error   Error   `json:"error"`
}

// TranslateDescriptions 自动翻译:把 ids 里的技能当前版本的描述翻译成 language,存进数据库。
// 跳过不存在的技能、描述为空的、已有对得上当前描述的译文的,以及界面语言是中文(zh 开头)而描述已经是中文的
// (汉字在汉字与其他文字的词里占一半以上,且没有假名);这些都不联网。其他语言都交给模型,模型看到已是该语言时原样返回。
// 最多同时发 4 个请求。只和一条描述有关的失败(InvalidArgument:服务拒绝了这一条、回答被截断)记进 Failed,其余照常翻译;
// 整体性的失败(没填模型、连不上、超时、限流、Key 被拒、模型不存在)不再发新的请求,返回已翻译的结果与这个错误,已翻译的都已存下。
// 参数与错误同 [Library.TranslateDescription]。
func (l *Library) TranslateDescriptions(ctx context.Context, language string, ids []SkillID) (result TranslateDescriptionsResult, err error) {
	defer func() { err = typedError(err) }()
	if err := validateLanguage("translate descriptions", language); err != nil {
		return result, err
	}
	wanted := make(map[int64]bool, len(ids))
	for _, id := range ids {
		wanted[int64(id)] = true
	}
	read, unlock := l.sharedOperation(ctx)
	rows, err := l.store.ListSkills(read)
	unlock()
	if err != nil {
		return result, err
	}
	var todo []store.Skill
	for _, row := range rows {
		if wanted[row.ID] && row.Description != "" && row.Translations[language] == "" && !inLanguage(row.Description, language) {
			todo = append(todo, row)
		}
	}
	if len(todo) == 0 {
		return result, nil
	}
	read, unlock = l.sharedOperation(ctx)
	settings, err := l.translateSettings(read)
	unlock()
	if err != nil {
		return result, err
	}
	var mu sync.Mutex
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(translateConcurrency)
	for _, row := range todo {
		if groupCtx.Err() != nil {
			break
		}
		group.Go(func() error {
			_, err := l.translate(groupCtx, settings, row, language)
			mu.Lock()
			defer mu.Unlock()
			switch e := ErrorOf(typedError(err)); {
			case err == nil:
				result.Translated++
			case errors.Is(err, store.ErrNotFound):
			case e.Code == InvalidArgument && groupCtx.Err() == nil:
				result.Failed = append(result.Failed, TranslationFailure{SkillID: SkillID(row.ID), Error: *e})
			default:
				return err
			}
			return nil
		})
	}
	err = group.Wait()
	if err == nil {
		err = ctx.Err()
	}
	slices.SortFunc(result.Failed, func(a, b TranslationFailure) int { return cmp.Compare(a.SkillID, b.SkillID) })
	return result, err
}

func (l *Library) translateSettings(ctx context.Context) (ai.Settings, error) {
	stored, err := l.store.AISettings(ctx)
	if err != nil {
		return ai.Settings{}, err
	}
	return parseAISettings("translate description", aiSettingsOf(stored))
}

func (l *Library) translate(ctx context.Context, settings ai.Settings, row store.Skill, language string) (string, error) {
	translation, err := ai.Translate(ctx, settings, row.Description, language)
	if err != nil {
		return "", err
	}
	write, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	if err := l.store.SetTranslation(write, row.ID, language, row.Description, translation, clockNow()); err != nil {
		return "", err
	}
	return translation, nil
}

func validateLanguage(operation, language string) error {
	if !languageTag.MatchString(language) {
		return argumentError(operation, "language", "language must be a BCP 47 tag such as zh or en")
	}
	return nil
}

func inLanguage(text, language string) bool {
	if primary, _, _ := strings.Cut(strings.ToLower(language), "-"); primary != "zh" {
		return false
	}
	han, words := 0, 0
	inWord := false
	for _, r := range text {
		switch {
		case unicode.In(r, unicode.Hiragana, unicode.Katakana):
			return false
		case unicode.Is(unicode.Han, r):
			han++
			inWord = false
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if !inWord {
				words++
			}
			inWord = true
		default:
			inWord = false
		}
	}
	return han > 0 && han >= words
}
