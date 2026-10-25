package library

import (
	"context"
	"regexp"
	"strings"
	"sync/atomic"
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
// AI 设置里没填模型返回 NotConfigured;服务的回答按 [Library.TestAISettings] 的规则归类。最多等 2 分钟。
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

// TranslateDescriptions 自动翻译:把 ids 里的技能当前版本的描述翻译成 language,存进数据库,返回这次翻译了几个。
// 跳过不存在的技能、描述为空的、已有对得上当前描述的译文的,以及描述看起来已经是这种语言的(zh 开头的语言里含汉字,
// en 开头的语言里的字母都是拉丁字母),这些都不联网。最多同时发 4 个请求;一个失败就不再发新的,返回已翻译的个数与这个错误,
// 已翻译的都已存下。参数与错误同 [Library.TranslateDescription]。
func (l *Library) TranslateDescriptions(ctx context.Context, language string, ids []SkillID) (translated int, err error) {
	defer func() { err = typedError(err) }()
	if err := validateLanguage("translate descriptions", language); err != nil {
		return 0, err
	}
	wanted := make(map[int64]bool, len(ids))
	for _, id := range ids {
		wanted[int64(id)] = true
	}
	read, unlock := l.sharedOperation(ctx)
	rows, err := l.store.ListSkills(read)
	unlock()
	if err != nil {
		return 0, err
	}
	var todo []store.Skill
	for _, row := range rows {
		if wanted[row.ID] && row.Description != "" && row.Translations[language] == "" && !inLanguage(row.Description, language) {
			todo = append(todo, row)
		}
	}
	if len(todo) == 0 {
		return 0, nil
	}
	read, unlock = l.sharedOperation(ctx)
	settings, err := l.translateSettings(read)
	unlock()
	if err != nil {
		return 0, err
	}
	var done atomic.Int64
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(translateConcurrency)
	for _, row := range todo {
		if groupCtx.Err() != nil {
			break
		}
		group.Go(func() error {
			if _, err := l.translate(groupCtx, settings, row, language); err != nil {
				return err
			}
			done.Add(1)
			return nil
		})
	}
	err = group.Wait()
	if err == nil {
		err = ctx.Err()
	}
	return int(done.Load()), err
}

func (l *Library) translateSettings(ctx context.Context) (ai.Settings, error) {
	stored, err := l.store.AISettings(ctx)
	if err != nil {
		return ai.Settings{}, err
	}
	settings := normalizeAISettings(AISettings{
		Provider: AIProvider(stored.Provider), BaseURL: stored.BaseURL, APIKey: stored.APIKey, Model: stored.Model,
		Headers: stored.Headers, Body: stored.Body,
	})
	return parseAISettings("translate description", settings)
}

func (l *Library) translate(ctx context.Context, settings ai.Settings, row store.Skill, language string) (string, error) {
	translation, err := ai.Translate(ctx, settings, row.Description, languageName(language))
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

func primaryLanguage(language string) string {
	primary, _, _ := strings.Cut(strings.ToLower(language), "-")
	return primary
}

func languageName(language string) string {
	lower := strings.ToLower(language)
	switch primaryLanguage(language) {
	case "zh":
		if strings.Contains(lower, "hant") || strings.HasSuffix(lower, "-tw") || strings.HasSuffix(lower, "-hk") || strings.HasSuffix(lower, "-mo") {
			return "Traditional Chinese"
		}
		return "Simplified Chinese"
	case "en":
		return "English"
	}
	return "the language with BCP 47 tag " + language
}

func inLanguage(text, language string) bool {
	switch primaryLanguage(language) {
	case "zh":
		return strings.ContainsFunc(text, func(r rune) bool { return unicode.Is(unicode.Han, r) })
	case "en":
		letters := false
		for _, r := range text {
			if unicode.IsLetter(r) {
				if !unicode.Is(unicode.Latin, r) {
					return false
				}
				letters = true
			}
		}
		return letters
	}
	return false
}
