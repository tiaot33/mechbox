package library

import (
	"cmp"
	"context"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/tiaot33/mechbox/internal/library/internal/ai"
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

// Mentions 是一张关系图要画的线,以及还没交给 AI 判断的几处。
type Mentions struct {
	Mentions []Mention `json:"mentions"` // 按 From、To 的 ID 升序
	// Unjudged 是还要交给 AI 判断的几处:这组技能里的一个技能(当前版本)的正文里出现了库里单个词的技能名,
	// 又没用点名写法,也还没有判断结论。一个技能里同一个名字算一处。用 [Library.JudgeMentions] 判断;没配 AI 时界面据此提示。
	Unjudged int `json:"unjudged"`
}

// ListMentions 给出集合或组里的技能提到了库里哪些技能,用来画关系图。
// 现场读技能当前版本里的所有 Markdown 文件(.md、.markdown,包括 frontmatter),不联网,不写数据库;
// 技能更新出新版本、入库、从库里移除后再调用,结果跟着变。
//
// 一行里出现库里另一个技能的名字(区分大小写,按完整的词,技能提到自己不算),并且:
//   - 名字用了 Agent 点名技能的写法:斜杠命令 /name、Codex 的 $name、带插件前缀的 plugin:name、
//     让 Agent 用 Skill tool 加载(Skill tool 后面引号里的名字),或指向对方技能文件夹的路径(../name/…、skills/name/…、name/SKILL.md,用反斜杠分隔的也算);
//   - 或者名字由几个词用连字符连成(如 writing-plans),单独出现,有没有反引号都算;
//
// 就算提到。以下出现都不算:路径中间的一段(斜杠或反斜杠分隔)、网址的一部分、文件名(如 name.md),以及公共段落里的:
// 同一个集合里原样出现在超过一半(至少 3 个)技能里的一行,是作者给整套技能加的公共说明;没有来源的技能不按公共段落排除。
//
// 单个词的名字(如 review)没用点名写法时,规则分不清是不是在指那个技能,交给 AI 判断([Library.JudgeMentions]):
// 一个技能里出现这个名字的地方算一处,已经靠规则连到这个名字的不算;判断为提到的也画线,Lines 是交给 AI 的那几行
// (出现这个名字的前 3 行)。还没判断的计入 Unjudged,不画线。
//
// 库里有几个同名技能时,先连到这次集合或组里的那个,再是和提到它的技能同一个集合的,再是 ID 最小的。
// 两个 ID 都没给或都给了返回 InvalidArgument(Details.Field 为 collection_id);集合或组不存在返回 NotFound。
// 集合或组里没有技能、或谁都没提到别人时 Mentions 为空列表。
func (l *Library) ListMentions(ctx context.Context, filter MentionsFilter) (result Mentions, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	scan, err := l.scanMentions(ctx, "list mentions", filter)
	if err != nil {
		return Mentions{}, err
	}
	judgments, err := l.store.MentionJudgments(ctx)
	if err != nil {
		return Mentions{}, err
	}
	found := scan.found
	for _, from := range scan.rows {
		for _, candidate := range scan.candidates[from.ID] {
			mentioned, judged := judgments[from.VersionID][candidate.name]
			if !judged {
				result.Unjudged++
				continue
			}
			if !mentioned {
				continue
			}
			key := [2]int64{from.ID, candidate.target.ID}
			if found[key] == nil {
				found[key] = &Mention{From: SkillID(from.ID), To: SkillID(candidate.target.ID), Lines: []MentionLine{}}
			}
			found[key].Lines = append(found[key].Lines, candidate.lines...)
		}
	}
	result.Mentions = []Mention{}
	for _, mention := range found {
		result.Mentions = append(result.Mentions, *mention)
	}
	slices.SortFunc(result.Mentions, func(a, b Mention) int {
		return cmp.Or(cmp.Compare(a.From, b.From), cmp.Compare(a.To, b.To))
	})
	return result, nil
}

// JudgeMentionsResult 是一次判断的结果。
type JudgeMentionsResult struct {
	// Judged 是这次判断并存下了几处(一个技能里的一个名字算一处),判断为提到的和不是的都算。
	Judged int `json:"judged"`
	// Failed 是因为这个技能本身没判断成的:服务拒绝了这一条、回答被 max_tokens 截断、回答不是要的格式。
	// 其余技能照常判断;它的几处还算没判断,下次再判断。
	Failed []MentionJudgmentFailure `json:"failed,omitempty"`
}

// MentionJudgmentFailure 是判断时没判断成的一个技能。
type MentionJudgmentFailure struct {
	SkillID SkillID `json:"skill_id"`
	Error   Error   `json:"error"`
}

// JudgeMentions 用数据库里的 AI 设置判断集合或组里还没判断的几处([Mentions].Unjudged),结论随技能的版本存进数据库:
// 同一个版本只判断一次,之后 [Library.ListMentions] 直接用存下的结论;技能出了新版本只重判它自己,
// 新入库了单个词名字的技能只判断出现这个名字的技能。只判断这组技能,不判断全库。
// 别的调用(如另一张包含同一技能的关系图)正在判断的那几处不再发请求,等它判断完再返回,不计入这次的结果。
//
// 每个技能发一个请求,带上它要判断的每一处:被提到的技能的名字和描述,以及出现这个名字的前 3 行(不同的行);
// 不发整份文件,也不发这个技能自己的名字和描述。最多同时发 4 个请求。
// 只和一个技能有关的失败(InvalidArgument:服务拒绝了这一条、回答被截断、回答不是编号的 JSON 数组)记进 Failed,其余照常判断;
// 整体性的失败(没填模型、连不上、超时、限流、Key 被拒、模型不存在)不再发新的请求,返回已判断的结果与这个错误,已判断的都已存下。
// 判断途中数据库被同步拉回替换了时,回答不写进新的库,返回 Conflict。
// 没有要判断的时不联网,也不看 AI 设置。参数错误同 [Library.ListMentions];AI 设置里没填模型返回 NotConfigured,
// 服务的回答按 [Library.TestAISettings] 的规则归类。每个请求最多等 2 分钟。
func (l *Library) JudgeMentions(ctx context.Context, filter MentionsFilter) (result JudgeMentionsResult, err error) {
	defer func() { err = typedError(err) }()
	read, unlock := l.sharedOperation(ctx)
	scan, err := l.scanMentions(read, "judge mentions", filter)
	var judgments map[int64]map[string]bool
	var replica string
	if err == nil {
		judgments, err = l.store.MentionJudgments(read)
	}
	if err == nil {
		replica, err = l.store.Replica(read)
	}
	unlock()
	if err != nil {
		return result, err
	}
	var waiting []mentionJudgmentKey
	for _, from := range scan.rows {
		for _, candidate := range scan.candidates[from.ID] {
			if _, judged := judgments[from.VersionID][candidate.name]; !judged {
				waiting = append(waiting, mentionJudgmentKey{from.VersionID, candidate.name})
			}
		}
	}
	claimed, others := l.claimJudgments(waiting)
	defer func() {
		if err == nil {
			err = waitJudgments(ctx, others)
		}
	}()
	defer l.releaseJudgments(claimed)
	if len(claimed) == 0 {
		return result, nil
	}
	read, unlock = l.sharedOperation(ctx)
	judgments, err = l.store.MentionJudgments(read)
	unlock()
	if err != nil {
		return result, err
	}
	type task struct {
		from       store.Skill
		candidates []mentionCandidate
	}
	var todo []task
	for _, from := range scan.rows {
		var mine []mentionCandidate
		for _, candidate := range scan.candidates[from.ID] {
			key := mentionJudgmentKey{from.VersionID, candidate.name}
			if _, judged := judgments[from.VersionID][candidate.name]; claimed[key] != nil && !judged {
				mine = append(mine, candidate)
			}
		}
		if len(mine) > 0 {
			todo = append(todo, task{from: from, candidates: mine})
		}
	}
	if len(todo) == 0 {
		return result, nil
	}
	read, unlock = l.sharedOperation(ctx)
	settings, err := l.aiSettings(read, "judge mentions")
	unlock()
	if err != nil {
		return result, err
	}
	judged, failed, err := runPerSkill(ctx, todo, func(job task) int64 { return job.from.ID }, func(ctx context.Context, job task) error {
		return l.judge(ctx, settings, replica, job.from, job.candidates)
	})
	for _, job := range judged {
		result.Judged += len(job.candidates)
	}
	for _, f := range failed {
		result.Failed = append(result.Failed, MentionJudgmentFailure{SkillID: f.skill, Error: f.err})
	}
	return result, err
}

type mentionJudgmentKey struct {
	version int64
	name    string
}

func (l *Library) claimJudgments(keys []mentionJudgmentKey) (claimed map[mentionJudgmentKey]chan struct{}, others []chan struct{}) {
	l.judgeMu.Lock()
	defer l.judgeMu.Unlock()
	if l.judging == nil {
		l.judging = map[mentionJudgmentKey]chan struct{}{}
	}
	claimed = map[mentionJudgmentKey]chan struct{}{}
	for _, key := range keys {
		if done, ok := l.judging[key]; ok {
			others = append(others, done)
			continue
		}
		done := make(chan struct{})
		l.judging[key] = done
		claimed[key] = done
	}
	return claimed, others
}

func (l *Library) releaseJudgments(claimed map[mentionJudgmentKey]chan struct{}) {
	l.judgeMu.Lock()
	defer l.judgeMu.Unlock()
	for key, done := range claimed {
		delete(l.judging, key)
		close(done)
	}
}

func waitJudgments(ctx context.Context, others []chan struct{}) error {
	for _, done := range others {
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (l *Library) judge(ctx context.Context, settings ai.Settings, replica string, from store.Skill, candidates []mentionCandidate) error {
	asked := make([]ai.MentionCandidate, len(candidates))
	for i, candidate := range candidates {
		lines := make([]string, len(candidate.lines))
		for j, line := range candidate.lines {
			lines[j] = line.Text
		}
		asked[i] = ai.MentionCandidate{Name: candidate.name, Description: candidate.target.Description, Lines: lines}
	}
	answers, err := ai.JudgeMentions(ctx, settings, asked)
	if err != nil {
		return err
	}
	judgments := make(map[string]bool, len(candidates))
	for i, candidate := range candidates {
		judgments[candidate.name] = answers[i]
	}
	write, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	return l.store.InTx(write, func(tx *store.Tx) error {
		if err := tx.RequireReplica(write, replica); err != nil {
			return err
		}
		return tx.SetMentionJudgments(write, from.VersionID, judgments, clockNow())
	})
}

const candidateLines = 3

type mentionCandidate struct {
	name   string
	target store.Skill
	lines  []MentionLine
}

type mentionScan struct {
	rows       []store.Skill
	found      map[[2]int64]*Mention
	candidates map[int64][]mentionCandidate
}

func (l *Library) scanMentions(ctx context.Context, operation string, filter MentionsFilter) (mentionScan, error) {
	if (filter.CollectionID > 0) == (filter.GroupID > 0) || filter.CollectionID < 0 || filter.GroupID < 0 {
		return mentionScan{}, argumentError(operation, "collection_id", "exactly one of collection ID and group ID is required")
	}
	rows, err := l.store.ListSkills(ctx)
	if err != nil {
		return mentionScan{}, err
	}
	inSet := map[int64]bool{}
	if filter.CollectionID > 0 {
		if _, err := l.store.GetCollectionSource(ctx, int64(filter.CollectionID)); err != nil {
			return mentionScan{}, err
		}
		for _, row := range rows {
			if row.Source.ID == int64(filter.CollectionID) {
				inSet[row.ID] = true
			}
		}
	} else {
		if _, err := l.store.GetGroup(ctx, int64(filter.GroupID)); err != nil {
			return mentionScan{}, err
		}
		members, err := l.store.GroupMembers(ctx, int64(filter.GroupID))
		if err != nil {
			return mentionScan{}, err
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
			return mentionScan{}, err
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
	scan := mentionScan{found: map[[2]int64]*Mention{}, candidates: map[int64][]mentionCandidate{}}
	for _, from := range rows {
		if !inSet[from.ID] {
			continue
		}
		scan.rows = append(scan.rows, from)
		seen := map[[2]int64]map[MentionLine]bool{}
		ruled := map[string]bool{}
		asked := map[string][]MentionLine{}
		var names []string
		for _, line := range lines[from.ID] {
			if common[from.Source.ID][line.text] {
				continue
			}
			for start, end := range nameRuns(line.text) {
				name := line.text[start:end]
				if name == from.Name || byName[name] == nil {
					continue
				}
				entry := MentionLine{Path: line.path, Text: line.text}
				switch mentionAt(line.text, start, end, name) {
				case mentionRule:
					ruled[name] = true
					to := mentionTarget(byName[name], from, inSet)
					key := [2]int64{from.ID, to.ID}
					if scan.found[key] == nil {
						scan.found[key] = &Mention{From: SkillID(from.ID), To: SkillID(to.ID), Lines: []MentionLine{}}
						seen[key] = map[MentionLine]bool{}
					}
					if !seen[key][entry] {
						seen[key][entry] = true
						scan.found[key].Lines = append(scan.found[key].Lines, entry)
					}
				case mentionAsk:
					if asked[name] == nil {
						names = append(names, name)
					}
					if len(asked[name]) < candidateLines && !slices.Contains(asked[name], entry) {
						asked[name] = append(asked[name], entry)
					}
				}
			}
		}
		for _, name := range names {
			if !ruled[name] {
				scan.candidates[from.ID] = append(scan.candidates[from.ID], mentionCandidate{
					name: name, target: mentionTarget(byName[name], from, inSet), lines: asked[name],
				})
			}
		}
	}
	return scan, nil
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

type mentionKind int

const (
	mentionNone mentionKind = iota
	mentionRule
	mentionAsk
)

func mentionAt(line string, start, end int, name string) mentionKind {
	command := line[:start]
	line = strings.ReplaceAll(line, "\\", "/")
	before, after := line[:start], line[end:]
	if inURL(before) || isFileName(after) {
		return mentionNone
	}
	if strings.HasSuffix(command, "/") || strings.HasSuffix(command, "$") {
		if opensName(before[:len(before)-1]) {
			return mentionRule
		}
	}
	if prefix, ok := strings.CutSuffix(before, ":"); ok {
		trimmed := strings.TrimRightFunc(prefix, func(r rune) bool { return r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' })
		if len(trimmed) < len(prefix) && (opensName(trimmed) || strings.HasSuffix(trimmed, "/")) {
			return mentionRule
		}
	}
	if skillToolQuote.MatchString(before) && strings.ContainsAny(firstRune(after), "\"`'”") {
		return mentionRule
	}
	if (strings.HasSuffix(before, "../") || skillsSegment(before)) && strings.HasPrefix(after, "/") {
		return mentionRule
	}
	if strings.HasPrefix(after, "/SKILL.md") {
		return mentionRule
	}
	if strings.HasSuffix(before, "/") || strings.HasSuffix(before, ".") || strings.HasPrefix(after, "/") {
		return mentionNone
	}
	if strings.Contains(name, "-") {
		return mentionRule
	}
	return mentionAsk
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
