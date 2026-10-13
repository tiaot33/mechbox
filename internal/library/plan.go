package library

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/tiaot33/skills-tui/internal/library/internal/skillfile"
	"github.com/tiaot33/skills-tui/internal/library/internal/source"
	"github.com/tiaot33/skills-tui/internal/library/internal/store"
)

// UpdatePlanID 是一份更新计划的不透明句柄,由 Library 签发。和入库预览句柄一样不持久化、不参与 Sync、
// 不跨 Library 实例共享;Close 或 30 分钟后失效。
type UpdatePlanID string

// UpdatePlan 是更新前的只读报告:一个集合里的技能在库里和来源现在之间有什么不同,以及有变化的技能装在哪些地方、
// 各处现在是什么状态。生成它不改库、不碰装出去的文件夹,也不改检查更新的结论。
type UpdatePlan struct {
	ID           UpdatePlanID `json:"id"`
	CollectionID CollectionID `json:"collection_id"`
	Source       Source       `json:"source"` // 集合来自哪里
	// Revision 是来源现在的来源版本:GitHub 为 commit,URL 为压缩包内容哈希,本地目录为空。
	// 各技能现在在哪个来源版本上看 Skill.Revision。
	Revision string `json:"revision,omitempty"`
	// Commits 是 GitHub 来源从集合里最旧的来源版本到来源现在之间的提交说明,最新的在前;其他来源没有,读不到时也为空。
	Commits []Commit `json:"commits,omitempty"`
	// Skills 是集合里的每个技能,按 ID 升序。
	Skills []PlannedSkill `json:"skills"`
	// Installs 是有变化的技能装出去的每一处及其现在的状态,按「技能 × 地方」逐条给出,与 InspectSkillScopes 的结果同形、同序;
	// 同一处装了几个有变化的技能时各自一条。没有时为空数组。
	Installs []SkillScopeInspection `json:"installs"`
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
	// Skipped 为 true 表示有变化,但来源里这份内容用户之前跳过了;照样列出,用户可以勾上更新。
	Skipped bool `json:"skipped,omitzero"`
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
	revision   string
	createdAt  time.Time
	skills     map[SkillID]*plannedState
	timer      *time.Timer
}

type plannedState struct {
	change    PlannedChange
	versionID int64
	latest    source.Skill
	content   string
}

// PlanUpdate 对一个集合生成更新计划:读一次来源,集合里的技能都按来源的同一份内容判断。只读,可以反复生成;
// 返回的句柄供 GetUpdatePlanFile 读来源里的新内容、SkipUpdate 跳过这次的改动。
//
// 错误:集合 ID 不是正数返回 InvalidArgument(Details.Field 为 collection_id),集合不存在返回 NotFound;
// 读不了来源按来源错误返回(SourceUnreachable / Permission / SourceInvalid 等)。
func (l *Library) PlanUpdate(ctx context.Context, id CollectionID) (plan UpdatePlan, err error) {
	defer func() { err = typedError(err) }()
	if id <= 0 {
		return UpdatePlan{}, argumentError("plan update", "collection_id", "collection ID must be positive")
	}
	skills, states, err := l.collectionCurrent(ctx, id)
	if err != nil {
		return UpdatePlan{}, err
	}
	if len(skills) == 0 {
		return UpdatePlan{}, &Error{Code: NotFound, Message: "plan update: collection not found"}
	}
	createdAt := clockNow()
	snapshot, err := l.sources.Open(ctx, states[0].source.Type, states[0].source.Locator, "", states[0].snapshot)
	if err != nil {
		return UpdatePlan{}, err
	}
	checked := checkSkills(ctx, snapshot, haves(skills, states), true)
	if err := ctx.Err(); err != nil {
		return UpdatePlan{}, err
	}
	revision := snapshot.Revision()
	plan = UpdatePlan{CollectionID: id, Source: skills[0].Source, Revision: revision, Skills: make([]PlannedSkill, len(skills)), Installs: []SkillScopeInspection{}}
	state := &planState{collection: id, sourceID: states[0].source.ID, revision: revision, createdAt: createdAt, skills: map[SkillID]*plannedState{}}
	for i, skill := range skills {
		planned := PlannedSkill{Skill: skill}
		item := &plannedState{versionID: states[i].versionID, latest: checked[i].latest}
		switch err := checked[i].err; {
		case missingSkill(err):
			planned.Change = PlannedDeleted
		case err != nil:
			return UpdatePlan{}, err
		case checked[i].changed:
			planned.Change = PlannedChanged
			diff := diffDigests(digests(states[i].files), digests(item.latest.Files))
			planned.Diff = &diff
			planned.Risks = updateRisks(states[i].files, item.latest.Files)
			item.content = skillfile.Sum(item.latest.Files)
		default:
			planned.Change = PlannedUnchanged
		}
		item.change = planned.Change
		state.skills[skill.ID] = item
		plan.Skills[i] = planned
	}
	plan.Commits = l.planCommits(ctx, snapshot, states)
	if err := l.describePlan(ctx, &plan, state); err != nil {
		return UpdatePlan{}, err
	}
	plan.ID, err = l.registerPlan(state)
	if err != nil {
		return UpdatePlan{}, err
	}
	return plan, nil
}

func (l *Library) planCommits(ctx context.Context, snapshot source.Snapshot, states []currentVersion) []Commit {
	var longest []source.Commit
	var tried []string
	for _, state := range states {
		if state.revision == "" || state.revision == snapshot.Revision() || slices.Contains(tried, state.revision) {
			continue
		}
		tried = append(tried, state.revision)
		commits, err := snapshot.Commits(ctx, state.revision)
		if err == nil && len(commits) > len(longest) {
			longest = commits
		}
	}
	var result []Commit
	for _, commit := range longest {
		result = append(result, Commit{SHA: commit.SHA, Message: commit.Message, Author: commit.Author, Time: commit.Time})
	}
	return result
}

func (l *Library) describePlan(ctx context.Context, plan *UpdatePlan, state *planState) error {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	const operation = "plan update"
	for i, planned := range plan.Skills {
		if planned.Change != PlannedChanged {
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

// GetUpdatePlanFile 按技能内的相对路径读取更新计划里来源一侧的一个文件,即这个技能更新后会是的内容,和库里的版本对照着看差异。
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
	if item == nil || item.change == PlannedDeleted {
		return File{}, &Error{Code: NotFound, Message: operation + ": skill is not in the source", Details: ErrorDetails{SkillID: skill}}
	}
	index := slices.IndexFunc(item.latest.Files, func(file skillfile.File) bool { return file.Path == path })
	if index < 0 {
		return File{}, &Error{Code: NotFound, Message: operation + ": file not found", Details: ErrorDetails{Path: path}}
	}
	found := item.latest.Files[index]
	return File{Path: found.Path, Content: slices.Clone(found.Content), Mode: FileMode(found.Mode)}, nil
}

// SkipRequest 是一次跳过:在一份更新计划里,对有变化的技能跳过这次的改动。Skills 与 WholeRevision 恰好给一个。
type SkipRequest struct {
	Plan   UpdatePlanID `json:"plan"`
	Skills []SkillID    `json:"skills,omitempty"` // 跳过这几个技能的这次改动;都必须在计划里有变化,重复的只算一次
	// WholeRevision 为 true 时跳过整个来源版本:计划里有变化的技能全部跳过,在集合的历史里记成一次。
	WholeRevision bool `json:"whole_revision,omitzero"`
}

// SkipUpdate 记下用户明确不要这次的改动:按技能在计划里的来源内容记录,之后检查更新看到来源里还是这份内容时
// 不计入角标(UpdateCheck.Skipped);来源里这个技能的内容又变了才重新计入,同一来源里别的技能变了不影响。
// 跳过不改库里的技能,也不碰装出去的文件夹;想要时在更新计划里照样能勾上更新。跳过的时间和来源版本记在集合的历史里。
// 计划生成时间晚于技能上次检查更新、且技能在库里还是计划里的版本时,技能的检查结论随之换成计划看到的,
// 跳过后马上不计入角标。计划句柄不因跳过失效。
//
// 错误:句柄未知或已失效返回 NotFound;Skills 与 WholeRevision 都给或都不给、Skills 含不在计划里或没有变化的技能
// 返回 InvalidArgument(Details.Field 为 skills,技能带 Details.SkillID);计划里没有有变化的技能时跳过整个来源版本返回 FailedPrecondition;
// 技能在计划生成后被移除返回 NotFound(Details.SkillID)。
func (l *Library) SkipUpdate(ctx context.Context, req SkipRequest) (err error) {
	defer func() { err = typedError(err) }()
	const operation = "skip update"
	state, err := l.lookupPlan(operation, req.Plan)
	if err != nil {
		return err
	}
	var ids []SkillID
	switch {
	case req.WholeRevision && len(req.Skills) > 0:
		return argumentError(operation, "skills", "skills must be empty when skipping the whole revision")
	case req.WholeRevision:
		for id, item := range state.skills {
			if item.change == PlannedChanged {
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
			if item := state.skills[id]; item == nil || item.change != PlannedChanged {
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
		skip := store.Skip{SourceID: state.sourceID, Revision: state.revision, WholeRevision: req.WholeRevision, At: clockNow()}
		for _, id := range ids {
			item := state.skills[id]
			versionID, err := tx.CurrentVersionID(ctx, int64(id))
			if errors.Is(err, store.ErrNotFound) {
				return &Error{Code: NotFound, Message: operation + ": skill was removed", Details: ErrorDetails{SkillID: id}, Err: err}
			}
			if err != nil {
				return err
			}
			skip.Changes = append(skip.Changes, store.SkippedChange{SkillID: int64(id), Content: item.content})
			checkedAt, _, err := tx.UpdateCheck(ctx, int64(id))
			if err != nil {
				return err
			}
			if versionID == item.versionID && checkedAt.Before(state.createdAt) {
				conclusion := store.UpdateCheck{At: state.createdAt, Status: string(UpdateAvailable), Revision: state.revision, Content: item.content, Found: true}
				if err := tx.SaveUpdateCheck(ctx, int64(id), conclusion); err != nil {
					return err
				}
			}
		}
		_, err := tx.RecordSkip(ctx, skip)
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
