package library

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/tiaot33/mechbox/internal/library/internal/agentdir"
	"github.com/tiaot33/mechbox/internal/library/internal/skillfile"
	"github.com/tiaot33/mechbox/internal/library/internal/source"
	"github.com/tiaot33/mechbox/internal/library/internal/source/discovery"
	"github.com/tiaot33/mechbox/internal/library/internal/store"
)

// UpdatePlanID 是一份更新计划的不透明句柄,由 Library 签发。和入库预览句柄一样不持久化、不参与 Sync、
// 不跨 Library 实例共享;Close 或 30 分钟后失效。
type UpdatePlanID string

// UpdatePlan 是更新前的只读报告:一个集合里的技能在库里和来源现在之间有什么不同,以及有变化的技能装在哪些地方、
// 各处现在是什么状态。
type UpdatePlan struct {
	ID           UpdatePlanID `json:"id"`
	CollectionID CollectionID `json:"collection_id"` // 没有来源的技能用目录里的修改更新时为 0
	Source       Source       `json:"source"`        // 集合来自哪里;没有来源的技能为零值
	// Revision 是来源现在的来源版本:Git 类来源为 commit,直接下载为文件内容哈希,well-known 为索引内容哈希,本地目录为空。
	// 各技能现在在哪个来源版本上看 Skill.Revision。
	Revision string `json:"revision,omitempty"`
	// Commits 是 Git 类来源(GitHub 与 Git 仓库)从集合里最旧的来源版本到来源现在之间的提交说明,最新的在前;其他来源没有,读不到时也为空。
	Commits []Commit `json:"commits,omitempty"`
	// Skills 是集合里的每个技能,按 ID 升序。
	Skills []PlannedSkill `json:"skills"`
	// Installs 是有变化的技能装出去的每一处及其现在的状态,按「技能 × 地方」逐条给出,与 InspectSkillScopes 的结果同形、同序;
	// 同一处装了几个有变化的技能时各自一条。没有时为空数组。用目录里的修改更新时不含取内容的那个文件夹(共用它的 Agent、
	// 以及解析符号链接后是同一个真实文件夹的地方也不列)。
	Installs []SkillScopeInspection `json:"installs"`
	// FromScope 是用目录里的修改更新(PlanUpdateFromScope)时取内容的那一处;从来源更新时为 nil。
	FromScope *Scope `json:"from_scope,omitempty"`
}

// Commit 是来源里的一次提交,只供展示。
type Commit struct {
	SHA     string    `json:"sha"`
	Message string    `json:"message"`          // 完整的提交说明
	Author  string    `json:"author,omitempty"` // 作者名
	Time    time.Time `json:"time,omitzero"`    // 作者提交的时间
}

// PlannedChange 是更新计划里一个技能在库里和来源现在之间的差别。
type PlannedChange string

const (
	PlannedChanged   PlannedChange = "changed"   // 有变化:来源里这个技能的文件和库里的当前版本不同
	PlannedUnchanged PlannedChange = "unchanged" // 没变化
	// PlannedDeleted 是来源里已删除:来源读到了,但里面已经没有这个名字的技能。库里的技能和装出去的文件夹都不动,
	// 要不要移除(RemoveSkill)或继续保留(KeepDeletedSkill)由用户决定。
	PlannedDeleted PlannedChange = "deleted"
)

// PlannedSkill 是更新计划里的一个技能。
type PlannedSkill struct {
	Skill  Skill         `json:"skill"` // 库里现在的技能
	Change PlannedChange `json:"change"`
	// Skipped 为 true 表示有变化(或推测出了改名),但来源里这份内容用户之前跳过了;照样列出,用户可以勾上更新。
	Skipped bool `json:"skipped,omitzero"`
	// RenamedTo 是来源里已删除时,推测这个技能改名成的新名字,含义同 UpdateCheck.RenamedTo。勾上更新即确认「是」:技能改用新名字,
	// 内容换成那个新名字的;RejectRename 回答「不是」。有推测时 Risks、Diff 按那个新名字的内容给出,装出去的各处也列在 Installs 里。
	RenamedTo string `json:"renamed_to,omitempty"`
	// Edited 为 true 表示库里的当前版本是用目录里的修改更新进来的(SkillVersion.BasedOn 不为空)。这时有没有变化只看来源:
	// 来源里这个技能和这份修改所基于的来源版本相比有没有改过,没改过就是没变化;改过且和这份修改不同才是有变化,勾上更新会换掉这份修改
	// (之后能回滚回来),所以桌面端默认不勾。本地目录来源照旧拿目录里的内容和这份修改比较,「重新读取」就是换成目录里的内容。
	// 当前版本是入库时说不清有没有用户修改的那一版(SkillVersion.MaybeEdited)时也为 true:它没有所基于的来源版本,
	// 来源里的和它不一样就是有变化,同样默认不勾。
	// 用目录里的修改更新的计划(PlanUpdateFromScope)里为 false。
	Edited bool `json:"edited,omitzero"`
	// Risks 是有变化的技能里需要先看一眼的文件:新增或改动的脚本、新增的可执行文件,按路径排序。没有时为空。
	Risks []UpdateRisk `json:"risks,omitempty"`
	// Diff 是有变化时从库里的当前版本到来源里的内容改了哪些文件;其他情况为 nil。库里一侧用 GetVersionFile(Skill.VersionID, path) 读,
	// 来源一侧用 GetUpdatePlanFile 读。
	Diff *VersionDiff `json:"diff,omitempty"`
}

// UpdateRiskKind 是更新计划标出的风险种类。脚本指可执行文件、带 #! 开头的文件,以及常见脚本扩展名
// (.sh、.py、.js、.ts、.rb、.pl、.ps1 等)的文件。种类以后可能增加,调用方遇到不认识的值按「需要用户查看」处理。
type UpdateRiskKind string

const (
	RiskExecutableAdded UpdateRiskKind = "executable_added" // 新增的可执行文件,包括原来不可执行、这次变成可执行的文件
	RiskScriptAdded     UpdateRiskKind = "script_added"     // 新增的脚本(不可执行)
	RiskScriptChanged   UpdateRiskKind = "script_changed"   // 改动的脚本
)

// UpdateRisk 是一个有变化的技能里的一个风险文件。
type UpdateRisk struct {
	Kind UpdateRiskKind `json:"kind"`
	Path string         `json:"path"` // 技能内斜杠分隔的相对路径
}

const planTTL = 30 * time.Minute

type planState struct {
	collection CollectionID
	sourceID   int64
	replica    string
	revision   string
	skills     map[SkillID]*plannedState
	from       *Scope
	timer      *time.Timer
}

type plannedState struct {
	name      string
	renameTo  string
	change    PlannedChange
	versionID int64
	latest    source.Skill
	content   string
}

// PlanUpdate 对一个集合生成更新计划:读一次来源,集合里的技能都按来源的同一份内容判断,返回的句柄供 GetUpdatePlanFile
// 读来源里的新内容、SkipUpdate 跳过这次的改动。和 CheckUpdates 一样只读:不改技能的内容和版本,也不碰装出去的文件夹;
// 读到的结论同样写进各技能的 LastUpdateCheck 与集合的新技能,所以计划里看到来源里已删除的技能可以直接 KeepDeletedSkill。
// 可以反复生成。
//
// 错误:集合 ID 不是正数返回 InvalidArgument(Details.Field 为 collection_id),集合不存在返回 NotFound;
// 读不了来源按来源错误返回(SourceUnreachable / Permission / SourceInvalid 等);读来源期间整个库被同步拉回替换返回 Conflict。
func (l *Library) PlanUpdate(ctx context.Context, id CollectionID) (plan UpdatePlan, err error) {
	defer func() { err = typedError(err) }()
	if id <= 0 {
		return UpdatePlan{}, argumentError("plan update", "collection_id", "collection ID must be positive")
	}
	read, err := l.readCollection(ctx, id)
	if err != nil {
		return UpdatePlan{}, err
	}
	if read == nil {
		return UpdatePlan{}, &Error{Code: NotFound, Message: "plan update: collection not found"}
	}
	if read.openErr != nil {
		return UpdatePlan{}, read.openErr
	}
	for _, checked := range read.checked {
		if checked.err != nil && !missingSkill(checked.err) {
			return UpdatePlan{}, checked.err
		}
	}
	commits, err := l.planCommits(ctx, read.snapshot, read.states)
	if err != nil {
		return UpdatePlan{}, err
	}
	conclusions, _ := read.conclusions()
	if _, err := l.saveCollectionRead(ctx, read, conclusions); err != nil {
		return UpdatePlan{}, err
	}
	plan = UpdatePlan{CollectionID: id, Source: read.skills[0].Source, Revision: read.revision, Commits: commits, Skills: make([]PlannedSkill, len(read.skills)), Installs: []SkillScopeInspection{}}
	state := &planState{collection: id, sourceID: read.states[0].source.ID, replica: read.replica, revision: read.revision, skills: map[SkillID]*plannedState{}}
	for i, skill := range read.skills {
		planned := PlannedSkill{Skill: skill, Edited: read.states[i].base != 0 || read.states[i].maybeEdited}
		checked := read.checked[i]
		item := &plannedState{name: skill.Name, versionID: read.states[i].versionID, latest: checked.latest}
		guess, renamed := read.renames[i]
		switch {
		case checked.err != nil && renamed:
			planned.Change, planned.RenamedTo = PlannedDeleted, guess.latest.Name
			item.latest, item.content, item.renameTo = guess.latest, guess.content, guess.latest.Name
			diff := diffDigests(digests(read.states[i].files), digests(item.latest.Files))
			planned.Diff = &diff
			planned.Risks = updateRisks(read.states[i].files, item.latest.Files)
		case checked.err != nil:
			planned.Change = PlannedDeleted
		case checked.changed:
			planned.Change = PlannedChanged
			diff := diffDigests(digests(read.states[i].files), digests(item.latest.Files))
			planned.Diff = &diff
			planned.Risks = updateRisks(read.states[i].files, item.latest.Files)
			item.content = skillfile.Sum(item.latest.Files)
		default:
			planned.Change = PlannedUnchanged
		}
		item.change = planned.Change
		state.skills[skill.ID] = item
		plan.Skills[i] = planned
	}
	if err := l.describePlan(ctx, &plan, state); err != nil {
		return UpdatePlan{}, err
	}
	if err := ctx.Err(); err != nil {
		return UpdatePlan{}, err
	}
	plan.ID, err = l.registerPlan(state)
	if err != nil {
		return UpdatePlan{}, err
	}
	return plan, nil
}

// PlanUpdateFromScope 对巡检为用户改过(modified)的那一处生成更新计划:用那里文件夹的内容更新库里的这个技能,成为它的新版本。
// 这是一次普通的更新,计划同样交给 Update 执行(确认时一并把勾选的其他地方换成这份修改)、用 GetUpdatePlanFile 读那里的文件,
// 留下的更新记录(UpdateRecord.FromScope 为这一处)同样能回滚、单独重试没换上的地方;不能跳过(SkipUpdate)。
// 计划里只有这一个技能:Change 为 changed,Risks 与 Diff 从库里的当前版本到那里的内容;Revision 与 Commits 为空。
// Installs 列出这个技能装出去的其他各处及其状态,默认勾选的规则和从来源更新相同:用户改过的地方、文件夹不在了的地方不勾,勾上才覆盖、才装回去。
// 更新之后,取内容的那一处和新的当前版本一致,巡检为 current。新版本的 SkillVersion.BasedOn 是当时库里的当前版本,
// 来源版本沿用它的;之后检查更新只看来源从这份修改所基于的来源版本之后有没有改过(见 PlannedSkill.Edited)。
// 只读:不改库,也不碰装出去的文件夹;可以反复生成。
//
// 错误:作用域不合法返回 InvalidArgument;技能、Agent、项目不存在,或技能不装在这里返回 NotFound;作用域根目录不存在返回
// FailedPrecondition(Details.Path 为根目录,项目带 ProjectID);这一处没有被改过(和库里某个版本一致、文件夹不在了),或里面有
// 不是普通文件的东西,返回 FailedPrecondition(Details.Path 为文件夹);内容不是有效技能(没有 SKILL.md,或缺 name / description)、
// SKILL.md 的 name 和技能名不同,或超出读取上限,返回 SourceInvalid(Details.Path 为文件夹,名字不同时 Details.Name 为那里写的名字)。
func (l *Library) PlanUpdateFromScope(ctx context.Context, skill SkillID, scope Scope) (plan UpdatePlan, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	const operation = "plan update from scope"
	edit, err := l.readScopeEdit(ctx, operation, skill, scope)
	if err != nil {
		return UpdatePlan{}, err
	}
	replica, err := l.store.Replica(ctx)
	if err != nil {
		return UpdatePlan{}, err
	}
	from := edit.target.scope()
	diff := diffDigests(digests(edit.current.files), digests(edit.latest.Files))
	plan = UpdatePlan{
		CollectionID: edit.skill.CollectionID,
		Source:       edit.skill.Source,
		Skills:       []PlannedSkill{{Skill: edit.skill, Change: PlannedChanged, Risks: updateRisks(edit.current.files, edit.latest.Files), Diff: &diff}},
		Installs:     []SkillScopeInspection{},
		FromScope:    &from,
	}
	skillScopes, err := l.listSkillScopes(ctx, skill, nil)
	if err != nil {
		return UpdatePlan{}, err
	}
	inspections, err := l.inspectSkillScopes(ctx, operation, skillScopes)
	if err != nil {
		return UpdatePlan{}, err
	}
	for _, inspection := range inspections {
		if !agentdir.SameFolder(inspection.Folder, edit.folder) {
			plan.Installs = append(plan.Installs, inspection)
		}
	}
	item := &plannedState{name: edit.skill.Name, change: PlannedChanged, versionID: edit.current.versionID, latest: edit.latest, content: skillfile.Sum(edit.latest.Files)}
	state := &planState{collection: edit.skill.CollectionID, sourceID: edit.current.source.ID, replica: replica, skills: map[SkillID]*plannedState{skill: item}, from: &from}
	plan.ID, err = l.registerPlan(state)
	if err != nil {
		return UpdatePlan{}, err
	}
	return plan, nil
}

type scopeEdit struct {
	skill   Skill
	current currentVersion
	target  resolvedScope
	folder  string
	latest  source.Skill
}

func (l *Library) readScopeEdit(ctx context.Context, operation string, id SkillID, scope Scope) (scopeEdit, error) {
	target, err := l.resolveScope(ctx, operation, scope)
	if err != nil {
		return scopeEdit{}, err
	}
	skill, current, err := l.getCurrent(ctx, id)
	if err != nil {
		return scopeEdit{}, err
	}
	place, err := l.store.PlaceSkillScopes(ctx, int64(id), target.record())
	if err != nil {
		return scopeEdit{}, err
	}
	if len(place) == 0 {
		return scopeEdit{}, &Error{Code: NotFound, Message: operation + ": skill is not installed in scope", Details: ErrorDetails{SkillID: id}}
	}
	if err := target.requireRoot(operation); err != nil {
		return scopeEdit{}, err
	}
	folder := filepath.Join(target.directory, place[0].Folder)
	unmodified := preconditionError(operation, "the installed folder was not modified", ErrorDetails{Path: folder})
	if exists, err := agentdir.Exists(folder); err != nil {
		return scopeEdit{}, fmt.Errorf("inspect target folder: %w", err)
	} else if !exists {
		return scopeEdit{}, unmodified
	}
	files, err := agentdir.ReadFiles(ctx, folder, maxFetchFiles, maxFetchSize)
	switch {
	case errors.Is(err, agentdir.ErrIrregularFile):
		return scopeEdit{}, preconditionError(operation, "the installed folder has entries that are not regular files", ErrorDetails{Path: folder})
	case errors.Is(err, agentdir.ErrTooLarge):
		return scopeEdit{}, &Error{Code: SourceInvalid, Message: operation + ": " + err.Error(), Details: ErrorDetails{Path: folder}, Err: err}
	case err != nil:
		return scopeEdit{}, err
	}
	if status, _, err := l.matchVersion(ctx, id, current.versionID, digests(files)); err != nil {
		return scopeEdit{}, err
	} else if status != SkillScopeModified {
		return scopeEdit{}, unmodified
	}
	index := slices.IndexFunc(files, func(file skillfile.File) bool { return file.Path == "SKILL.md" })
	if index < 0 {
		return scopeEdit{}, &Error{Code: SourceInvalid, Message: operation + ": the installed folder has no SKILL.md", Details: ErrorDetails{Path: folder}}
	}
	parsed, err := discovery.ParseSkillMD(files[index].Content, ".")
	if err != nil {
		return scopeEdit{}, &Error{Code: SourceInvalid, Message: operation + ": SKILL.md: " + err.Error(), Details: ErrorDetails{Path: folder}, Err: err}
	}
	if parsed.Name != skill.Name {
		return scopeEdit{}, &Error{Code: SourceInvalid, Message: operation + ": SKILL.md names another skill", Details: ErrorDetails{Path: folder, Name: parsed.Name}}
	}
	return scopeEdit{skill: skill, current: current, target: target, folder: folder, latest: source.Skill{Name: skill.Name, Description: parsed.Description, Files: files}}, nil
}

func (l *Library) planCommits(ctx context.Context, snapshot source.Snapshot, states []currentVersion) ([]Commit, error) {
	var longest []source.Commit
	var tried []string
	for _, state := range states {
		if state.revision == "" || state.revision == snapshot.Revision() || slices.Contains(tried, state.revision) {
			continue
		}
		tried = append(tried, state.revision)
		commits, err := snapshot.Commits(ctx, state.revision)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if err == nil && len(commits) > len(longest) {
			longest = commits
		}
	}
	var result []Commit
	for _, commit := range longest {
		result = append(result, Commit{SHA: commit.SHA, Message: commit.Message, Author: commit.Author, Time: commit.Time})
	}
	return result, nil
}

func (l *Library) describePlan(ctx context.Context, plan *UpdatePlan, state *planState) error {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	const operation = "plan update"
	for i, planned := range plan.Skills {
		if planned.Change != PlannedChanged && planned.RenamedTo == "" {
			continue
		}
		skipped, err := l.store.Skipped(ctx, int64(planned.Skill.ID), state.skills[planned.Skill.ID].content)
		if err != nil {
			return err
		}
		plan.Skills[i].Skipped = skipped
		skillScopes, err := l.listSkillScopes(ctx, planned.Skill.ID, nil)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		inspections, err := l.inspectSkillScopes(ctx, operation, skillScopes)
		if err != nil {
			return err
		}
		plan.Installs = append(plan.Installs, inspections...)
	}
	return nil
}

func (l *Library) registerPlan(state *planState) (UpdatePlanID, error) {
	raw, err := newPreviewID()
	if err != nil {
		return "", err
	}
	id := UpdatePlanID(raw)
	l.previewMu.Lock()
	defer l.previewMu.Unlock()
	if l.closed {
		return "", fmt.Errorf("plan update: library is closed")
	}
	l.plans[id] = state
	state.timer = time.AfterFunc(planTTL, func() {
		l.previewMu.Lock()
		defer l.previewMu.Unlock()
		if l.plans[id] == state {
			delete(l.plans, id)
		}
	})
	return id, nil
}

func (l *Library) lookupPlan(operation string, id UpdatePlanID) (*planState, error) {
	l.previewMu.Lock()
	defer l.previewMu.Unlock()
	state := l.plans[id]
	if state == nil {
		return nil, &Error{Code: NotFound, Message: operation + ": unknown or expired update plan"}
	}
	return state, nil
}

// GetUpdatePlanFile 按技能内的相对路径读取更新计划里来源一侧的一个文件,即这个技能更新后会是的内容,和库里的版本对照着看差异;
// 推测改了名的技能读那个新名字的内容。
// 句柄未知或已失效返回 NotFound;技能不在这份计划里、或在来源里已删除返回 NotFound(Details.SkillID);路径不存在返回 NotFound(Details.Path)。
func (l *Library) GetUpdatePlanFile(ctx context.Context, plan UpdatePlanID, skill SkillID, path string) (file File, err error) {
	defer func() { err = typedError(err) }()
	if err := ctx.Err(); err != nil {
		return File{}, err
	}
	const operation = "get update plan file"
	state, err := l.lookupPlan(operation, plan)
	if err != nil {
		return File{}, err
	}
	item := state.skills[skill]
	if item == nil || item.change == PlannedDeleted && item.renameTo == "" {
		return File{}, &Error{Code: NotFound, Message: operation + ": skill is not in the source", Details: ErrorDetails{SkillID: skill}}
	}
	index := slices.IndexFunc(item.latest.Files, func(file skillfile.File) bool { return file.Path == path })
	if index < 0 {
		return File{}, &Error{Code: NotFound, Message: operation + ": file not found", Details: ErrorDetails{Path: path}}
	}
	found := item.latest.Files[index]
	return File{Path: found.Path, Content: slices.Clone(found.Content), Mode: FileMode(found.Mode)}, nil
}

// SkipRequest 是一次跳过:在一份更新计划里,对有变化或推测出改名的技能跳过这次的改动。Skills 与 WholeRevision 恰好给一个。
type SkipRequest struct {
	Plan   UpdatePlanID `json:"plan"`
	Skills []SkillID    `json:"skills,omitempty"` // 跳过这几个技能的这次改动;都必须在计划里有变化或推测出了改名,重复的只算一次
	// WholeRevision 为 true 时跳过整个来源版本:计划里有变化和推测出改名的技能全部跳过,在集合的历史里记成一次。
	WholeRevision bool `json:"whole_revision,omitzero"`
}

// SkipUpdate 记下用户明确不要这次的改动:按技能在计划里的来源内容记录,之后检查更新看到来源里还是这份内容时
// 不计入角标(UpdateCheck.Skipped);来源里这个技能的内容又变了才重新计入,同一来源里别的技能变了不影响。
// 跳过不改库里的技能,也不碰装出去的文件夹;想要时在更新计划里照样能勾上更新。跳过的时间和来源版本记在集合的历史里。
// 生成计划时已写下了检查结论,所以跳过后马上不计入角标。计划句柄不因跳过失效。
//
// 错误:句柄未知或已失效返回 NotFound;计划是用目录里的修改更新的(PlanUpdateFromScope)返回 InvalidArgument(Details.Field 为 plan);
// Skills 与 WholeRevision 都给或都不给、Skills 含不在计划里或没有变化的技能
// 返回 InvalidArgument(Details.Field 为 skills,技能带 Details.SkillID);计划里没有有变化的技能时跳过整个来源版本返回 FailedPrecondition;
// 技能在计划生成后被移除返回 NotFound(Details.SkillID);计划生成后整个库被同步拉回替换返回 Conflict。
func (l *Library) SkipUpdate(ctx context.Context, req SkipRequest) (err error) {
	defer func() { err = typedError(err) }()
	const operation = "skip update"
	state, err := l.lookupPlan(operation, req.Plan)
	if err != nil {
		return err
	}
	if state.from != nil {
		return argumentError(operation, "plan", "an update from a scope has no source change to skip")
	}
	var ids []SkillID
	switch {
	case req.WholeRevision && len(req.Skills) > 0:
		return argumentError(operation, "skills", "skills must be empty when skipping the whole revision")
	case req.WholeRevision:
		for id, item := range state.skills {
			if item.change == PlannedChanged || item.renameTo != "" {
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			return preconditionError(operation, "no skill changed in this update plan", ErrorDetails{})
		}
		slices.Sort(ids)
	case len(req.Skills) == 0:
		return argumentError(operation, "skills", "at least one skill is required")
	default:
		for _, id := range req.Skills {
			if item := state.skills[id]; item == nil || item.change != PlannedChanged && item.renameTo == "" {
				return &Error{Code: InvalidArgument, Message: operation + ": skill has no change in this update plan", Details: ErrorDetails{Field: "skills", SkillID: id}}
			}
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	return l.store.InTx(ctx, func(tx *store.Tx) error {
		if err := tx.RequireReplica(ctx, state.replica); err != nil {
			return err
		}
		skip := store.Record{SourceID: state.sourceID, Kind: recordSkip, Revision: state.revision, WholeRevision: req.WholeRevision, At: clockNow()}
		for _, id := range ids {
			item := state.skills[id]
			row, err := tx.GetSkill(ctx, int64(id))
			if errors.Is(err, store.ErrNotFound) || err == nil && (row.Source.ID != state.sourceID || row.Name != item.name) {
				return &Error{Code: NotFound, Message: operation + ": skill was removed", Details: ErrorDetails{SkillID: id}, Err: err}
			}
			if err != nil {
				return err
			}
			skip.Skipped = append(skip.Skipped, store.SkippedChange{SkillID: int64(id), Content: item.content})
			skip.Skills = append(skip.Skills, store.RecordSkill{SkillID: int64(id), Name: row.Name, From: row.VersionID, To: row.VersionID})
		}
		_, err := tx.AddRecord(ctx, skip)
		return err
	})
}

func digests(files []skillfile.File) map[string]skillfile.Digest {
	result := make(map[string]skillfile.Digest, len(files))
	for _, file := range files {
		result[file.Path] = skillfile.Digest{Fingerprint: skillfile.Fingerprint(file.Content), Mode: file.Mode}
	}
	return result
}

func diffDigests(from, to map[string]skillfile.Digest) VersionDiff {
	diff := VersionDiff{Changed: []string{}, Added: []string{}, Deleted: []string{}}
	for path, fromDigest := range from {
		toDigest, ok := to[path]
		if !ok {
			diff.Deleted = append(diff.Deleted, path)
		} else if toDigest != fromDigest {
			diff.Changed = append(diff.Changed, path)
		}
	}
	for path := range to {
		if _, ok := from[path]; !ok {
			diff.Added = append(diff.Added, path)
		}
	}
	for _, paths := range [][]string{diff.Changed, diff.Added, diff.Deleted} {
		slices.Sort(paths)
	}
	return diff
}

var scriptExtensions = []string{".sh", ".bash", ".zsh", ".fish", ".py", ".js", ".mjs", ".cjs", ".ts", ".mts", ".cts", ".rb", ".pl", ".php", ".lua", ".ps1", ".psm1", ".bat", ".cmd"}

func isScript(file skillfile.File) bool {
	return file.Mode == skillfile.ModeExecutable || strings.HasPrefix(string(file.Content), "#!") || slices.Contains(scriptExtensions, strings.ToLower(path.Ext(file.Path)))
}

func updateRisks(from, to []skillfile.File) []UpdateRisk {
	previous := make(map[string]skillfile.File, len(from))
	for _, file := range from {
		previous[file.Path] = file
	}
	var risks []UpdateRisk
	for _, file := range to {
		old, existed := previous[file.Path]
		switch {
		case file.Mode == skillfile.ModeExecutable && (!existed || old.Mode != skillfile.ModeExecutable):
			risks = append(risks, UpdateRisk{Kind: RiskExecutableAdded, Path: file.Path})
		case !existed && isScript(file):
			risks = append(risks, UpdateRisk{Kind: RiskScriptAdded, Path: file.Path})
		case existed && (isScript(file) || isScript(old)) && !skillfile.Equal([]skillfile.File{old}, []skillfile.File{file}):
			risks = append(risks, UpdateRisk{Kind: RiskScriptChanged, Path: file.Path})
		}
	}
	slices.SortFunc(risks, func(a, b UpdateRisk) int { return cmp.Compare(a.Path, b.Path) })
	return risks
}

// RejectRename 对更新计划里的改名推测回答「不是」:这个技能不是改名成了那个新名字。按一删一增处理:这个技能照旧算来源里已删除
// (继续保留或移除由用户决定),那个新名字算来源里的新技能(Collection.NewSkills)。记下来,来源里那个新名字的内容又变了才会再推测这一条。
// 句柄未知或已失效返回 NotFound;技能在计划里没有改名推测返回 InvalidArgument(Details.Field 为 skill,带 Details.SkillID);
// 技能在计划生成后被移除返回 NotFound(Details.SkillID);计划生成后整个库被同步拉回替换返回 Conflict。
func (l *Library) RejectRename(ctx context.Context, plan UpdatePlanID, skill SkillID) (err error) {
	defer func() { err = typedError(err) }()
	const operation = "reject rename"
	state, err := l.lookupPlan(operation, plan)
	if err != nil {
		return err
	}
	item := state.skills[skill]
	if item == nil || item.renameTo == "" {
		return &Error{Code: InvalidArgument, Message: operation + ": no rename was guessed for the skill", Details: ErrorDetails{Field: "skill", SkillID: skill}}
	}
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	return l.store.InTx(ctx, func(tx *store.Tx) error {
		if err := tx.RequireReplica(ctx, state.replica); err != nil {
			return err
		}
		row, err := tx.GetSkill(ctx, int64(skill))
		if errors.Is(err, store.ErrNotFound) || err == nil && row.Name != item.name {
			return &Error{Code: NotFound, Message: operation + ": skill was removed", Details: ErrorDetails{SkillID: skill}, Err: err}
		}
		if err != nil {
			return err
		}
		if err := tx.RejectRename(ctx, int64(skill), item.renameTo, item.content); err != nil {
			return err
		}
		return tx.AddNewSkill(ctx, state.sourceID, item.renameTo)
	})
}
