package library

import (
	"cmp"
	"context"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/tiaot33/mechbox/internal/library/internal/store"
)

// MentionsFilter 选出要画关系图的那组技能:画集合的传 CollectionID,画组的传 GroupID,两者只给一个。
type MentionsFilter struct {
	CollectionID CollectionID `json:"collection_id,omitempty"`
	GroupID      GroupID      `json:"group_id,omitempty"`
}

// Mention 是一个技能的正文提到了库里另一个技能:关系图上从 From 指向 To 的一条线。
// From 总在要画的那组技能里;To 可以是库里其他集合的技能(关系图上用虚线框画出)。
type Mention struct {
	From  SkillID       `json:"from"`
	To    SkillID       `json:"to"`
	Lines []MentionLine `json:"lines"` // 提到对方的那几行原文,按文件路径、行的先后;同一个文件里相同的一行只列一次
}

// MentionLine 是 Mention.From 当前版本的一个 Markdown 文件里提到对方的一行。
type MentionLine struct {
	Path string `json:"path"` // 技能内斜杠分隔的相对路径,如 SKILL.md
	Text string `json:"text"` // 这一行原文,去掉首尾空白
}

// ListMentions 给出集合或组里的技能提到了库里哪些技能,用来画关系图;按 From、To 的 ID 升序。
// 现场读技能当前版本里的所有 Markdown 文件(.md、.markdown,包括 frontmatter),不联网,不写数据库;
// 技能更新出新版本、入库、从库里移除后再调用,结果跟着变。
//
// 一行里出现库里另一个技能的名字(区分大小写,按完整的词,技能提到自己不算),并且:
//   - 名字用了 Agent 点名技能的写法:斜杠命令 /name、Codex 的 $name、带插件前缀的 plugin:name、
//     让 Agent 用 Skill tool 加载(Skill tool 后面引号里的名字),或指向对方技能文件夹的路径(../name/…、skills/name/…、name/SKILL.md);
//   - 或者名字由几个词用连字符连成(如 writing-plans),单独出现,有没有反引号都算;
//
// 就算提到。单个词的名字(如 review)没用点名写法时不算。以下出现都不算:路径中间的一段、网址的一部分、
// 文件名(如 name.md),以及公共段落里的:同一个集合里原样出现在超过一半(至少 3 个)技能里的一行,
// 是作者给整套技能加的公共说明;没有来源的技能不按公共段落排除。
//
// 库里有几个同名技能时,先连到这次集合或组里的那个,再是和提到它的技能同一个集合的,再是 ID 最小的。
// 两个 ID 都没给或都给了返回 InvalidArgument(Details.Field 为 collection_id);集合或组不存在返回 NotFound。
// 集合或组里没有技能、或谁都没提到别人时返回空列表。
func (l *Library) ListMentions(ctx context.Context, filter MentionsFilter) (mentions []Mention, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if (filter.CollectionID > 0) == (filter.GroupID > 0) || filter.CollectionID < 0 || filter.GroupID < 0 {
		return nil, argumentError("list mentions", "collection_id", "exactly one of collection ID and group ID is required")
	}
	rows, err := l.store.ListSkills(ctx)
	if err != nil {
		return nil, err
	}
	inSet := map[int64]bool{}
	if filter.CollectionID > 0 {
		if _, err := l.store.GetCollectionSource(ctx, int64(filter.CollectionID)); err != nil {
			return nil, err
		}
		for _, row := range rows {
			if row.Source.ID == int64(filter.CollectionID) {
				inSet[row.ID] = true
			}
		}
	} else {
		if _, err := l.store.GetGroup(ctx, int64(filter.GroupID)); err != nil {
			return nil, err
		}
		members, err := l.store.GroupMembers(ctx, int64(filter.GroupID))
		if err != nil {
			return nil, err
		}
		for _, id := range members {
			inSet[id] = true
		}
	}

	collections := map[int64]bool{}
	for _, row := range rows {
		if inSet[row.ID] && row.Source.ID != 0 {
			collections[row.Source.ID] = true
		}
	}
	lines := map[int64][]mentionSourceLine{}
	for _, row := range rows {
		if !inSet[row.ID] && !collections[row.Source.ID] {
			continue
		}
		files, err := l.store.MarkdownFiles(ctx, row.VersionID)
		if err != nil {
			return nil, err
		}
		for _, file := range files {
			for line := range strings.SplitSeq(string(file.Content), "\n") {
				if text := strings.TrimSpace(line); text != "" {
					lines[row.ID] = append(lines[row.ID], mentionSourceLine{path: file.Path, text: text})
				}
			}
		}
	}
	common := commonLines(rows, collections, lines)

	byName := map[string][]store.Skill{}
	for _, row := range rows {
		byName[row.Name] = append(byName[row.Name], row)
	}
	found := map[[2]int64]*Mention{}
	for _, from := range rows {
		if !inSet[from.ID] {
			continue
		}
		seen := map[[2]int64]map[MentionLine]bool{}
		for _, line := range lines[from.ID] {
			if common[from.Source.ID][line.text] {
				continue
			}
			for start, end := range nameRuns(line.text) {
				name := line.text[start:end]
				if name == from.Name || byName[name] == nil || !mentionedAt(line.text, start, end, name) {
					continue
				}
				to := mentionTarget(byName[name], from, inSet)
				key := [2]int64{from.ID, to.ID}
				if found[key] == nil {
					found[key] = &Mention{From: SkillID(from.ID), To: SkillID(to.ID), Lines: []MentionLine{}}
					seen[key] = map[MentionLine]bool{}
				}
				entry := MentionLine{Path: line.path, Text: line.text}
				if !seen[key][entry] {
					seen[key][entry] = true
					found[key].Lines = append(found[key].Lines, entry)
				}
			}
		}
	}
	mentions = []Mention{}
	for _, mention := range found {
		mentions = append(mentions, *mention)
	}
	slices.SortFunc(mentions, func(a, b Mention) int {
		return cmp.Or(cmp.Compare(a.From, b.From), cmp.Compare(a.To, b.To))
	})
	return mentions, nil
}

type mentionSourceLine struct {
	path, text string
}

func commonLines(rows []store.Skill, collections map[int64]bool, lines map[int64][]mentionSourceLine) map[int64]map[string]bool {
	counts := map[int64]map[string]int{}
	sizes := map[int64]int{}
	for _, row := range rows {
		if !collections[row.Source.ID] {
			continue
		}
		sizes[row.Source.ID]++
		if counts[row.Source.ID] == nil {
			counts[row.Source.ID] = map[string]int{}
		}
		distinct := map[string]bool{}
		for _, line := range lines[row.ID] {
			if !distinct[line.text] {
				distinct[line.text] = true
				counts[row.Source.ID][line.text]++
			}
		}
	}
	common := map[int64]map[string]bool{}
	for collection, texts := range counts {
		common[collection] = map[string]bool{}
		for text, n := range texts {
			if n >= 3 && n*2 > sizes[collection] {
				common[collection][text] = true
			}
		}
	}
	return common
}

func nameRuns(line string) func(yield func(int, int) bool) {
	return func(yield func(int, int) bool) {
		start := -1
		for i := 0; i <= len(line); i++ {
			if i < len(line) && isNameByte(line[i]) {
				if start < 0 {
					start = i
				}
				continue
			}
			if start >= 0 && !yield(start, i) {
				return
			}
			start = -1
		}
	}
}

func isNameByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-' || b == '_'
}

var skillToolQuote = regexp.MustCompile("Skill tool.{0,40}[\"`'“]$")

func mentionedAt(line string, start, end int, name string) bool {
	before, after := line[:start], line[end:]
	if inURL(before) || isFileName(after) {
		return false
	}
	if strings.HasSuffix(before, "/") || strings.HasSuffix(before, "$") {
		if opensName(before[:len(before)-1]) {
			return true
		}
	}
	if prefix, ok := strings.CutSuffix(before, ":"); ok {
		trimmed := strings.TrimRightFunc(prefix, func(r rune) bool { return r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' })
		if len(trimmed) < len(prefix) && (opensName(trimmed) || strings.HasSuffix(trimmed, "/")) {
			return true
		}
	}
	if skillToolQuote.MatchString(before) && strings.ContainsAny(firstRune(after), "\"`'”") {
		return true
	}
	if (strings.HasSuffix(before, "../") || skillsSegment(before)) && strings.HasPrefix(after, "/") {
		return true
	}
	if strings.HasPrefix(after, "/SKILL.md") {
		return true
	}
	if strings.HasSuffix(before, "/") || strings.HasSuffix(before, ".") || strings.HasPrefix(after, "/") {
		return false
	}
	return strings.Contains(name, "-")
}

func isFileName(after string) bool {
	return len(after) >= 2 && after[0] == '.' && (after[1] >= 'a' && after[1] <= 'z' || after[1] >= 'A' && after[1] <= 'Z')
}

func skillsSegment(before string) bool {
	prefix, ok := strings.CutSuffix(before, "skills/")
	return ok && (strings.HasSuffix(prefix, "/") || opensName(prefix))
}

func opensName(before string) bool {
	if before == "" {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(before)
	return unicode.IsSpace(r) || strings.ContainsRune("`*\"'([（「“", r)
}

func inURL(before string) bool {
	word := before[strings.LastIndexFunc(before, unicode.IsSpace)+1:]
	return strings.Contains(word, "://")
}

func firstRune(s string) string {
	_, size := utf8.DecodeRuneInString(s)
	return s[:size]
}

func mentionTarget(candidates []store.Skill, from store.Skill, inSet map[int64]bool) store.Skill {
	rank := func(row store.Skill) int {
		switch {
		case inSet[row.ID]:
			return 0
		case from.Source.ID != 0 && row.Source.ID == from.Source.ID:
			return 1
		}
		return 2
	}
	return slices.MinFunc(candidates, func(a, b store.Skill) int {
		return cmp.Or(cmp.Compare(rank(a), rank(b)), cmp.Compare(a.ID, b.ID))
	})
}
