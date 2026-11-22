package library

import (
	"cmp"
	"context"
	"slices"

	"github.com/tiaot33/mechbox/internal/library/internal/agentdir"
	"github.com/tiaot33/mechbox/internal/library/internal/source/discovery"
	"github.com/tiaot33/mechbox/internal/library/internal/store"
)

// SkillMentions 是技能详情里一个技能和其他技能之间的提到关系。
type SkillMentions struct {
	// Mentions 是它提到的技能,按名字排。
	Mentions []SkillMention `json:"mentions"`
	// MentionedBy 是库里提到它的技能,按名字、ID 排。
	MentionedBy []SkillMention `json:"mentioned_by"`
	// Unjudged 是两栏里还要交给 AI 判断的几处:它的正文里单个词的技能名,以及别的技能正文里它的名字(单个词时),
	// 没用点名写法、也还没有判断结论。一个技能里同一个名字算一处。用 [Library.JudgeSkillMentions] 判断。
	Unjudged int `json:"unjudged"`
}

// SkillMention 是提到关系的另一头。
type SkillMention struct {
	Name string `json:"name"`
	// SkillID 是库里的这个技能。在 Mentions 里为 0 表示库里没有这个名字,是 Agent 目录里的未纳管技能;
	// 库里有几个同名技能时,先取和提到它的技能同一个集合的,再取 ID 最小的。
	SkillID SkillID `json:"skill_id,omitzero"`
	// Lines 是提到的那几行原文:在 Mentions 里出自这个技能,在 MentionedBy 里出自对方。
	// 规则认出的按文件路径、行的先后,同一个文件里相同的一行只列一次;AI 判断为提到的是交给 AI 的那几行(最多 3 行)。
	Lines []MentionLine `json:"lines"`
}

// GetSkillMentions 给出技能详情里的提到关系:这个技能提到了哪些技能,库里又有哪些技能提到了它。
// 判断规则同 [Library.ListMentions],只是范围是整个库,被提到的一方除了库里的技能,也包括各作用域里的未纳管技能
// (按文件夹名;Agent 目录、项目不在这台机器上的那几个作用域不算)。未纳管技能的正文不读,只作为被提到的一方。
// 公共段落按每个技能自己的集合算。只读,不联网;AI 的判断结论用已经存下的,没判断的计入 Unjudged。
// 技能不存在返回 NotFound。
func (l *Library) GetSkillMentions(ctx context.Context, id SkillID) (result SkillMentions, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if _, err := l.store.GetSkill(ctx, int64(id)); err != nil {
		return SkillMentions{}, err
	}
	scan, err := l.scanLibrary(ctx)
	if err != nil {
		return SkillMentions{}, err
	}
	self := scan.byID[int64(id)]
	names, unjudged := scan.mentioned(self)
	result.Unjudged = len(unjudged)
	result.Mentions = []SkillMention{}
	for _, name := range names {
		mention := SkillMention{Name: name, Lines: scan.docs[self.ID].lines[name]}
		if to, ok := scan.target(self, name); ok {
			mention.SkillID = SkillID(to.ID)
		}
		result.Mentions = append(result.Mentions, mention)
	}
	result.MentionedBy = []SkillMention{}
	for _, from := range scan.rows {
		if !scan.points(from, self) {
			continue
		}
		names, unjudged := scan.mentioned(from)
		switch {
		case slices.Contains(names, self.Name):
			result.MentionedBy = append(result.MentionedBy, SkillMention{Name: from.Name, SkillID: SkillID(from.ID), Lines: scan.docs[from.ID].lines[self.Name]})
		case slices.Contains(unjudged, self.Name):
			result.Unjudged++
		}
	}
	byName := func(a, b SkillMention) int {
		return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.SkillID, b.SkillID))
	}
	slices.SortFunc(result.Mentions, byName)
	slices.SortFunc(result.MentionedBy, byName)
	return result, nil
}

// JudgeSkillMentions 用 AI 设置判断 [SkillMentions].Unjudged 里的几处,结论随提到对方的那个技能的版本存进数据库,
// 之后 [Library.GetSkillMentions]、[Library.ListMentions]、[Library.ListMissingMentions] 都直接用。
// 请求、失败与并发的规则同 [Library.JudgeMentions]。技能不存在返回 NotFound;没有要判断的时不联网,也不看 AI 设置。
func (l *Library) JudgeSkillMentions(ctx context.Context, id SkillID) (JudgeMentionsResult, error) {
	return l.runJudgments(ctx, func(ctx context.Context) ([]judgeTask, error) {
		if _, err := l.store.GetSkill(ctx, int64(id)); err != nil {
			return nil, err
		}
		scan, err := l.scanLibrary(ctx)
		if err != nil {
			return nil, err
		}
		self := scan.byID[int64(id)]
		own := judgeTask{from: self}
		for _, name := range scan.docs[self.ID].asked {
			own.candidates = append(own.candidates, scan.candidate(self, name))
		}
		tasks := []judgeTask{own}
		for _, from := range scan.rows {
			if scan.points(from, self) && slices.Contains(scan.docs[from.ID].asked, self.Name) {
				tasks = append(tasks, judgeTask{from: from, candidates: []mentionCandidate{scan.candidate(from, self.Name)}})
			}
		}
		return tasks, nil
	})
}

// SkillInScope 是装在某个作用域的一个技能,[Library.ListLostMentions] 用它指出要卸掉的那几处。
type SkillInScope struct {
	SkillID SkillID `json:"skill_id"`
	Scope   Scope   `json:"scope"`
}

// MissingMention 是装在某个作用域的技能提到了一个技能,而 Agent 在这个作用域里找不到它。
type MissingMention struct {
	SkillID SkillID `json:"skill_id"` // 装着的、提到对方的技能
	Scope   Scope   `json:"scope"`
	Name    string  `json:"name"` // 找不到的技能名
	// MentionedSkillID 是库里叫这个名字的技能,可以装到 Scope;有几个同名时先取和 SkillID 同一个集合的,再取 ID 最小的。
	// 为 0 表示库里没有,只是别处 Agent 目录里的未纳管技能。
	MentionedSkillID SkillID `json:"mentioned_skill_id,omitzero"`
}

// ListMissingMentions 列出装在各作用域的库里技能提到了、Agent 在那个作用域里却找不到的技能:作用域页的提醒、
// 技能详情与装好后的提示都用它。提到按 [Library.GetSkillMentions] 的规则,AI 只用已经存下的结论,没判断的不算。
//
// 找不找得到看磁盘上的技能文件夹(按文件夹名),库装的和未纳管技能都算:项目作用域里看这个 Agent 在项目里的全部技能目录
// 和它的全部全局目录,全局作用域只看全局目录。Agent 目录、项目不在这台机器上的作用域不列;装着的技能自己的文件夹已经不在了的也不列。
// 只读,不联网。结果按技能 ID、Agent、作用域(全局在前,项目按 ID)、名字排序;没有时为空列表。
func (l *Library) ListMissingMentions(ctx context.Context) (missing []MissingMention, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	scan, installed, err := l.scanInstalled(ctx)
	if err != nil {
		return nil, err
	}
	return scan.missing(installed, nil), nil
}

// ListLostMentions 在卸载、从库里删除之前给出:gone 这几处的技能文件夹不在了以后,哪些装着的技能会因此找不到它们提到的技能
// (现在找得到、之后找不到的,规则同 [Library.ListMissingMentions])。按要删的实际文件夹判断:认同一处的其他 Agent 一并算不在,
// 同一个作用域另一个技能目录里的同名文件夹照样算找得到;gone 里的技能
// 自己不再算;不是装在那里的忽略。只读,一次扫描算出,排序同 ListMissingMentions;没有时为空列表。
// gone 里的作用域组合不合法时返回 InvalidArgument。
func (l *Library) ListLostMentions(ctx context.Context, gone []SkillInScope) (lost []MissingMention, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	for _, place := range gone {
		if _, err := scopeOf("list lost mentions", place.Scope); err != nil {
			return nil, err
		}
	}
	scan, installed, err := l.scanInstalled(ctx)
	if err != nil {
		return nil, err
	}
	removed := map[string]bool{}
	for _, place := range gone {
		for _, in := range installed {
			if in.SkillID == place.SkillID && in.Scope == place.Scope {
				removed[in.Folder] = true
			}
		}
	}
	before := map[MissingMention]bool{}
	for _, m := range scan.missing(installed, nil) {
		before[m] = true
	}
	lost = []MissingMention{}
	for _, m := range scan.missing(installed, removed) {
		if !before[m] {
			lost = append(lost, m)
		}
	}
	return lost, nil
}

type folderAt struct {
	scope Scope
	name  string
}

func (l *Library) scanInstalled(ctx context.Context) (*libraryScan, []SkillScope, error) {
	scan, err := l.scanLibrary(ctx)
	if err != nil {
		return nil, nil, err
	}
	installed, err := l.listSkillScopes(ctx, 0, nil)
	if err != nil {
		return nil, nil, err
	}
	return scan, installed, nil
}

func (s *libraryScan) missing(installed []SkillScope, removed map[string]bool) []MissingMention {
	present := map[folderAt]bool{}
	for _, folder := range s.folders {
		if !removed[folder.Path] || slices.ContainsFunc(folder.twins, func(path string) bool { return !removed[path] }) {
			present[folderAt{folder.target.scope(), folder.Name}] = true
		}
	}
	visible := func(scope Scope, name string) bool {
		return present[folderAt{scope, name}] || scope.Kind == ScopeProject && present[folderAt{GlobalScope(scope.Agent), name}]
	}
	missing := []MissingMention{}
	for _, in := range installed {
		if removed[in.Folder] || !present[folderAt{in.Scope, agentdir.FolderName(in.Folder)}] {
			continue
		}
		from := s.byID[int64(in.SkillID)]
		names, _ := s.mentioned(from)
		for _, name := range names {
			if visible(in.Scope, name) {
				continue
			}
			mention := MissingMention{SkillID: in.SkillID, Scope: in.Scope, Name: name}
			if to, ok := s.target(from, name); ok {
				mention.MentionedSkillID = SkillID(to.ID)
			}
			missing = append(missing, mention)
		}
	}
	slices.SortStableFunc(missing, func(a, b MissingMention) int {
		return cmp.Or(cmp.Compare(a.SkillID, b.SkillID), cmp.Compare(a.Scope.Agent, b.Scope.Agent),
			cmp.Compare(a.Scope.ProjectID, b.Scope.ProjectID), cmp.Compare(a.Name, b.Name))
	})
	return missing
}

type libraryScan struct {
	rows      []store.Skill
	byID      map[int64]store.Skill
	byName    map[string][]store.Skill
	folders   []scopeFolder
	unmanaged map[string]string
	docs      map[int64]docMentions
	judgments map[int64]map[string]bool
}

func (l *Library) scanLibrary(ctx context.Context) (*libraryScan, error) {
	rows, err := l.store.ListSkills(ctx)
	if err != nil {
		return nil, err
	}
	folders, err := l.readableScopeFolders(ctx)
	if err != nil {
		return nil, err
	}
	judgments, err := l.store.MentionJudgments(ctx)
	if err != nil {
		return nil, err
	}
	scan := &libraryScan{
		rows: rows, byID: map[int64]store.Skill{}, byName: map[string][]store.Skill{}, folders: folders,
		unmanaged: map[string]string{}, docs: map[int64]docMentions{}, judgments: judgments,
	}
	collections := map[int64]bool{}
	for _, row := range rows {
		scan.byID[row.ID] = row
		scan.byName[row.Name] = append(scan.byName[row.Name], row)
		if row.Source.ID != 0 {
			collections[row.Source.ID] = true
		}
	}
	for _, folder := range folders {
		if _, seen := scan.unmanaged[folder.Name]; seen || scan.byName[folder.Name] != nil {
			continue
		}
		var description string
		if candidate, err := discovery.ParseSkillMD(folder.SkillMD, folder.Name); err == nil {
			description = candidate.Description
		}
		scan.unmanaged[folder.Name] = description
	}
	lines, err := l.skillLines(ctx, rows, func(store.Skill) bool { return true })
	if err != nil {
		return nil, err
	}
	common := commonLines(rows, collections, lines)
	known := func(name string) bool {
		_, unmanaged := scan.unmanaged[name]
		return unmanaged || scan.byName[name] != nil
	}
	for _, row := range rows {
		scan.docs[row.ID] = scanDoc(row.Name, lines[row.ID], common[row.Source.ID], known)
	}
	return scan, nil
}

func (s *libraryScan) target(from store.Skill, name string) (store.Skill, bool) {
	if s.byName[name] == nil {
		return store.Skill{}, false
	}
	return mentionTarget(s.byName[name], from, nil), true
}

func (s *libraryScan) points(from, to store.Skill) bool {
	target, ok := s.target(from, to.Name)
	return from.ID != to.ID && ok && target.ID == to.ID
}

func (s *libraryScan) mentioned(from store.Skill) (names, unjudged []string) {
	doc := s.docs[from.ID]
	names = slices.Clone(doc.ruled)
	for _, name := range doc.asked {
		switch mentioned, judged := s.judgments[from.VersionID][name]; {
		case !judged:
			unjudged = append(unjudged, name)
		case mentioned:
			names = append(names, name)
		}
	}
	return names, unjudged
}

func (s *libraryScan) candidate(from store.Skill, name string) mentionCandidate {
	candidate := mentionCandidate{name: name, description: s.unmanaged[name], lines: s.docs[from.ID].lines[name]}
	if to, ok := s.target(from, name); ok {
		candidate.to, candidate.description = to.ID, to.Description
	}
	return candidate
}
