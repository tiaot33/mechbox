package library

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/tiaot33/skills-tui/internal/library/internal/agentdir"
	"github.com/tiaot33/skills-tui/internal/library/internal/store"
)

// ConflictPolicy 决定目标里有不是 Library 写入的内容时的处理方式:安装时是无记录的外来文件夹,
// 重新安装时是被手动改过(modified)的文件夹。目标文件夹已被另一个 SkillScope 占用(同一 skill 的另一 Scope,
// 或同名的另一 skill)时两种策略都返回 FailedPrecondition:同一目录下的一个文件夹名至多归一个 SkillScope,
// 需先 Uninstall 占用者。
type ConflictPolicy string

const (
	Fail      ConflictPolicy = "fail"      // 安装返回 AlreadyExists;重新安装该条 FailedPrecondition(默认)
	Overwrite ConflictPolicy = "overwrite" // 原子替换外来文件夹或被手动改过的文件夹
)

// InstallOptions 是 Install 与 InstallGroup 的选项。
type InstallOptions struct {
	OnConflict ConflictPolicy `json:"on_conflict"`
}

// InstallPlanStatus 是安装前的目标状态,不受执行时冲突策略影响;判定顺序与执行时一致。
type InstallPlanStatus string

const (
	InstallReady            InstallPlanStatus = "ready"             // 目标不存在
	InstallAlreadyInstalled InstallPlanStatus = "already_installed" // 本技能已安装在本作用域
	InstallOccupied         InstallPlanStatus = "occupied"          // 另一个 SkillScope 占用目标文件夹
	InstallUntracked        InstallPlanStatus = "untracked"         // 目标文件夹存在但 Library 没有记录
	InstallDuplicate        InstallPlanStatus = "duplicate"         // 仅组安装计划:与组内其他成员同名且尚未安装,组安装时会被跳过;优先于 occupied / untracked / ready
)

// InstallPlan 只描述目标事实;不校验无记录文件夹的内容或来历,也不是执行令牌。
type InstallPlan struct {
	SkillID         SkillID           `json:"skill_id"`
	Name            string            `json:"name"`
	Directory       string            `json:"directory"`
	Target          string            `json:"target"`
	Status          InstallPlanStatus `json:"status"`
	OccupierSkillID SkillID           `json:"occupier_skill_id,omitzero"`
	OccupierName    string            `json:"occupier_name,omitempty"`
}

// GroupInstallPlan 按组成员顺序报告各自状态。同名成员里尚未安装的标为 duplicate,组安装时会被跳过;
// 已安装的那个仍是 installed,照常只补组安装。DuplicateNames 列出组内重复的名字。
type GroupInstallPlan struct {
	Members        []InstallPlan `json:"members"`
	DuplicateNames []string      `json:"duplicate_names,omitempty"`
}

// SkillScope 是某个技能安装在某个 Scope:该 Scope 目录里的技能文件夹,以及 Library 对它的记录。
// 只要还有直接安装或组安装留着它就存在,最后一个撤掉时移除。
// 记录只存 Scope 身份;Directory 是列表时 Resolve 出的 Agent 目录,
// 不是 Install 的输入(Install 也不接受目录)。对照磁盘的状态由 InspectSkillScopes 给出。
type SkillScope struct {
	SkillID   SkillID   `json:"skill_id"`
	Scope     Scope     `json:"scope"`
	Directory string    `json:"directory"`
	Direct    bool      `json:"direct"`              // 有直接安装留着它
	Groups    []GroupID `json:"groups,omitempty"`    // 覆盖它的组安装所属的 Group:该 skill 是成员且组已安装到此 Scope,按 ID 升序
	CreatedAt time.Time `json:"created_at,omitzero"` // 首次安装到此 Scope 的时间。再次安装不更新
}

// GroupInstall 是一个组安装:某 Group 安装到某 Scope,覆盖该组的当前和未来成员(见 ADR 0006)。
// 它是独立的记录,空组或成员全被跳过时也存在;App 据此显示整组开关。
type GroupInstall struct {
	GroupID   GroupID   `json:"group_id"`
	Scope     Scope     `json:"scope"`
	CreatedAt time.Time `json:"created_at,omitzero"` // 组安装记录建立(首次组安装)的时间。再次组安装不更新
}

// GroupMemberOutcome 是组安装或加入成员时,一个成员在一个 Scope 上的结果。
type GroupMemberOutcome string

const (
	MemberInstalled        GroupMemberOutcome = "installed"         // 新装到此 Scope
	MemberAlreadyInstalled GroupMemberOutcome = "already_installed" // 已安装在此 Scope,只补上组安装,不写盘
	MemberSkipped          GroupMemberOutcome = "skipped"           // 冲突跳过,原因见 Error;成员关系与组安装照常成立
)

// GroupMemberResult 是一个成员在一个 Scope 上的逐条结果,形状参考 UpdateCheck。
// Error 仅在 MemberSkipped 时非空:FailedPrecondition 为目标被另一个 SkillScope 占用(Details.SkillID / Name / Path)
// 、组内同名(Details.Name 与共同的目标 Path)或作用域根目录不存在(仅加入成员;Details.Path 为根目录,项目带 ProjectID),
// AlreadyExists 为目标有未记录文件夹且未选 Overwrite(Details.Path)。
type GroupMemberResult struct {
	SkillID SkillID            `json:"skill_id"`
	Scope   Scope              `json:"scope"`
	Outcome GroupMemberOutcome `json:"outcome"`
	Error   *Error             `json:"error,omitempty"`
}

type claimant struct {
	skillID SkillID
	name    string
}

func (l *Library) targetClaimant(ctx context.Context, id SkillID, name string, target resolvedScope) (*claimant, error) {
	claimants, err := l.store.Claimants(ctx, name, int64(id))
	if err != nil {
		return nil, err
	}
	want := agentdir.Canonical(target.directory)
	for _, other := range claimants {
		directory, err := l.Resolve(ctx, scopeRowOf(other.Scope).scope())
		if err != nil {
			return nil, err
		}
		if agentdir.Canonical(directory) != want {
			continue
		}
		return &claimant{skillID: SkillID(other.SkillID), name: other.Name}, nil
	}
	return nil, nil
}

func (l *Library) placementConflict(ctx context.Context, operation string, id SkillID, name string, target resolvedScope, policy ConflictPolicy) (*Error, error) {
	other, err := l.targetClaimant(ctx, id, name, target)
	if err != nil {
		return nil, err
	}
	if other != nil && other.skillID == id {
		return preconditionError(operation, "another scope already installs this skill into the same directory", ErrorDetails{Path: target.directory}), nil
	}
	if other != nil {
		return preconditionError(operation, "another skill already installs this name into the same directory", ErrorDetails{
			Path: filepath.Join(target.directory, other.name), Name: other.name, SkillID: other.skillID,
		}), nil
	}
	path := filepath.Join(target.directory, name)
	exists, err := agentdir.Exists(path)
	if err != nil {
		return nil, fmt.Errorf("inspect target folder: %w", err)
	}
	if exists && policy != Overwrite {
		return existsError(operation, "target already exists", ErrorDetails{Path: path}), nil
	}
	return nil, nil
}

// PlanInstall 只读报告解析目录与目标状态,不创建目录;未知技能或作用域返回 NotFound,
// 作用域根目录不存在时与 Install 一样返回 FailedPrecondition。
func (l *Library) PlanInstall(ctx context.Context, id SkillID, scope Scope) (plan InstallPlan, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	target, err := l.resolveScope(ctx, "plan install", scope)
	if err != nil {
		return plan, err
	}
	skill, err := l.GetSkill(ctx, id)
	if err != nil {
		return plan, err
	}
	if err := target.requireRoot("plan install"); err != nil {
		return plan, err
	}
	return l.planInstall(ctx, skill.ID, skill.Name, target)
}

// PlanGroupInstall 只读报告组内同名和各成员目标状态;空组成功返回空成员列表。
// 作用域根目录不存在时与 InstallGroup 一样返回 FailedPrecondition。
func (l *Library) PlanGroupInstall(ctx context.Context, groupID GroupID, scope Scope) (plan GroupInstallPlan, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	target, err := l.resolveScope(ctx, "plan group install", scope)
	if err != nil {
		return plan, err
	}
	members, err := l.ListGroupSkills(ctx, groupID)
	if err != nil {
		return plan, err
	}
	if err := target.requireRoot("plan group install"); err != nil {
		return plan, err
	}
	counts := memberNameCounts(members)
	for _, member := range members {
		if counts[member.Name] > 1 && !slices.Contains(plan.DuplicateNames, member.Name) {
			plan.DuplicateNames = append(plan.DuplicateNames, member.Name)
		}
	}
	plan.Members = make([]InstallPlan, 0, len(members))
	for _, member := range members {
		item, err := l.planInstall(ctx, member.ID, member.Name, target)
		if err != nil {
			return GroupInstallPlan{}, err
		}
		if counts[member.Name] > 1 && item.Status != InstallAlreadyInstalled {
			item.Status = InstallDuplicate
			item.OccupierSkillID, item.OccupierName = 0, ""
		}
		plan.Members = append(plan.Members, item)
	}
	return plan, nil
}

func memberNameCounts(members []SkillSummary) map[string]int {
	counts := make(map[string]int, len(members))
	for _, member := range members {
		counts[member.Name]++
	}
	return counts
}

func (l *Library) planInstall(ctx context.Context, id SkillID, name string, scope resolvedScope) (InstallPlan, error) {
	plan := InstallPlan{SkillID: id, Name: name, Directory: scope.directory, Target: filepath.Join(scope.directory, name), Status: InstallReady}
	existing, err := l.store.FindSkillScope(ctx, int64(id), scope.record())
	if err != nil {
		return plan, err
	}
	if existing != nil {
		plan.Status = InstallAlreadyInstalled
		return plan, nil
	}
	other, err := l.targetClaimant(ctx, id, name, scope)
	if err != nil {
		return plan, err
	}
	if other != nil {
		plan.Status = InstallOccupied
		plan.OccupierSkillID = other.skillID
		plan.OccupierName = other.name
		return plan, nil
	}
	exists, err := agentdir.Exists(plan.Target)
	if err != nil {
		return plan, fmt.Errorf("inspect target folder: %w", err)
	}
	if exists {
		plan.Status = InstallUntracked
	}
	return plan, nil
}

// Install 为 skill 在 Scope 记一个直接安装,并放入技能文件夹;本技能已安装在本作用域时只补上直接安装,
// 不写盘(文件夹丢失也不补回;把文件夹带到当前版本是单独的重新安装)。作用域根目录(全局为用户主目录,
// 项目为已登记路径)不存在时返回 FailedPrecondition,Library 从不创建根目录,只创建其下的 Agent 目录。
func (l *Library) Install(ctx context.Context, id SkillID, scope Scope, opts InstallOptions) (err error) {
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if err := validateConflictPolicy("install", opts.OnConflict); err != nil {
		return err
	}
	target, err := l.resolveScope(ctx, "install", scope)
	if err != nil {
		return err
	}
	skill, err := l.GetSkill(ctx, id)
	if err != nil {
		return err
	}
	if err := target.requireRoot("install skill"); err != nil {
		return err
	}
	existing, err := l.store.FindSkillScope(ctx, int64(id), target.record())
	if err != nil {
		return err
	}
	if existing != nil {
		return l.store.MarkDirect(ctx, existing.ID)
	}
	conflict, err := l.placementConflict(ctx, "install skill", skill.ID, skill.Name, target, opts.OnConflict)
	if err != nil {
		return err
	}
	if conflict != nil {
		return conflict
	}
	placements := []*placement{{skill: skill, scope: target}}
	return l.place(ctx, placements, func(tx *store.Tx) error {
		return tx.RecordSkillScope(ctx, int64(skill.ID), target.record(), true, clockNow())
	})
}

type placement struct {
	skill Skill
	scope resolvedScope
}

func (l *Library) place(ctx context.Context, placements []*placement, record func(*store.Tx) error) (err error) {
	for _, item := range placements {
		if err := item.scope.requireRoot("place skill folder"); err != nil {
			return err
		}
	}
	var change agentdir.Change
	defer func() { err = errors.Join(err, change.Rollback()) }()
	for _, item := range placements {
		if err := change.Stage(ctx, item.scope.directory, item.skill.Name, internalFiles(item.skill.Files)); err != nil {
			return err
		}
	}
	if record != nil {
		return l.applyChange(ctx, &change, record)
	}
	if err := change.Commit(ctx); err != nil {
		return err
	}
	change.Done()
	return nil
}

// Uninstall 撤销直接安装;若仍有组安装留着它,技能仍安装在此 Scope。
func (l *Library) Uninstall(ctx context.Context, id SkillID, scope Scope) (err error) {
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	target, err := l.resolveScope(ctx, "uninstall", scope)
	if err != nil {
		return err
	}
	existing, err := l.store.FindSkillScope(ctx, int64(id), target.record())
	if err != nil {
		return err
	}
	if existing == nil {
		return &Error{Code: NotFound, Message: "uninstall skill: skill is not installed in scope"}
	}
	if !existing.Direct {
		name, err := l.store.CoveringGroupName(ctx, int64(id), target.record())
		if err != nil {
			return err
		}
		return preconditionError("uninstall skill", "skill is only installed here by a group install", ErrorDetails{Name: name})
	}
	skill, err := l.GetSkill(ctx, id)
	if err != nil {
		return err
	}
	var change agentdir.Change
	defer func() { err = errors.Join(err, change.Rollback()) }()
	return l.applyChange(ctx, &change, func(tx *store.Tx) error {
		removed, err := tx.Uninstall(ctx, existing.ID)
		if err != nil {
			return err
		}
		if removed {
			change.Remove(target.directory, skill.Name)
		}
		return nil
	})
}

type memberPlan struct {
	operation  string
	opts       InstallOptions
	results    []GroupMemberResult
	placements []*placement
	planned    map[string]bool
}

func newMemberPlan(operation string, opts InstallOptions) *memberPlan {
	return &memberPlan{operation: operation, opts: opts, results: []GroupMemberResult{}, planned: map[string]bool{}}
}

func (l *Library) planMember(ctx context.Context, plan *memberPlan, id SkillID, name string, target resolvedScope, duplicate bool) error {
	result := GroupMemberResult{SkillID: id, Scope: target.scope(), Outcome: MemberSkipped}
	existing, err := l.store.FindSkillScope(ctx, int64(id), target.record())
	if err != nil {
		return err
	}
	if existing != nil {
		result.Outcome = MemberAlreadyInstalled
	} else if result.Error, err = l.memberBlocker(ctx, plan, id, name, target, duplicate); err != nil {
		return err
	} else if result.Error == nil {
		skill, err := l.GetSkill(ctx, id)
		if err != nil {
			return err
		}
		plan.planned[filepath.Join(agentdir.Canonical(target.directory), name)] = true
		plan.placements = append(plan.placements, &placement{skill: skill, scope: target})
		result.Outcome = MemberInstalled
	}
	plan.results = append(plan.results, result)
	return nil
}

func (l *Library) memberBlocker(ctx context.Context, plan *memberPlan, id SkillID, name string, target resolvedScope, duplicate bool) (*Error, error) {
	if missing, err := target.rootMissing(plan.operation); err != nil || missing != nil {
		return missing, err
	}
	path := filepath.Join(target.directory, name)
	if duplicate {
		return preconditionError(plan.operation, "multiple members install to the same target", ErrorDetails{Name: name, Path: path}), nil
	}
	if plan.planned[filepath.Join(agentdir.Canonical(target.directory), name)] {
		return preconditionError(plan.operation, "another scope already installs this skill into the same directory", ErrorDetails{Path: target.directory}), nil
	}
	return l.placementConflict(ctx, plan.operation, id, name, target, plan.opts.OnConflict)
}

func (l *Library) applyMemberPlan(ctx context.Context, plan *memberPlan, record func(tx *store.Tx, now time.Time) error) ([]GroupMemberResult, error) {
	now := clockNow()
	err := l.place(ctx, plan.placements, func(tx *store.Tx) error {
		if err := record(tx, now); err != nil {
			return err
		}
		for _, item := range plan.placements {
			if err := tx.RecordSkillScope(ctx, int64(item.skill.ID), item.scope.record(), false, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return plan.results, nil
}

// InstallGroup 在 Scope 记一个组安装(空组也记;已有则不变),并按成员顺序逐条返回结果:能安装的都安装,
// 已安装在此 Scope 的只补上组安装、不写盘;目标被占用、有未记录文件夹(且未选 Overwrite)、组内同名且尚未安装(同名的都跳过)
// 的成员跳过,再次 InstallGroup 可补齐。作用域根目录不存在时同 Install 返回 FailedPrecondition,
// 不记组安装。其余失败(I/O、取消)时撤销本次全部改动,含组安装记录。
func (l *Library) InstallGroup(ctx context.Context, groupID GroupID, scope Scope, opts InstallOptions) (_ []GroupMemberResult, err error) {
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if err := validateConflictPolicy("install group", opts.OnConflict); err != nil {
		return nil, err
	}
	target, err := l.resolveScope(ctx, "install group", scope)
	if err != nil {
		return nil, err
	}
	if err := l.requireGroup(ctx, groupID); err != nil {
		return nil, err
	}
	if err := target.requireRoot("install group"); err != nil {
		return nil, err
	}
	members, err := l.ListGroupSkills(ctx, groupID)
	if err != nil {
		return nil, err
	}
	counts := memberNameCounts(members)
	plan := newMemberPlan("install group", opts)
	for _, member := range members {
		if err := l.planMember(ctx, plan, member.ID, member.Name, target, counts[member.Name] > 1); err != nil {
			return nil, err
		}
	}
	return l.applyMemberPlan(ctx, plan, func(tx *store.Tx, now time.Time) error {
		return tx.RecordGroupInstall(ctx, int64(groupID), target.record(), now)
	})
}

// UninstallGroup 删除该组在此 Scope 的组安装;不再有直接安装或组安装留着的成员会被卸载,失败时恢复整组状态。
// 该组未组安装到此 Scope 返回 NotFound。
func (l *Library) UninstallGroup(ctx context.Context, groupID GroupID, scope Scope) (err error) {
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	target, err := l.resolveScope(ctx, "uninstall group", scope)
	if err != nil {
		return err
	}
	if err := l.requireGroup(ctx, groupID); err != nil {
		return err
	}
	groupInstallID, err := l.store.FindGroupInstall(ctx, int64(groupID), target.record())
	if errors.Is(err, store.ErrNotFound) {
		return &Error{Code: NotFound, Message: "uninstall group: group is not installed in scope"}
	}
	if err != nil {
		return err
	}
	members, err := l.store.MemberSkillScopes(ctx, int64(groupID), 0, new(target.record()))
	if err != nil {
		return err
	}
	var change agentdir.Change
	defer func() { err = errors.Join(err, change.Rollback()) }()
	return l.applyChange(ctx, &change, func(tx *store.Tx) error {
		if err := tx.RemoveGroupInstall(ctx, groupInstallID); err != nil {
			return err
		}
		for _, member := range members {
			removed, err := tx.UninstallMember(ctx, member.ID)
			if err != nil {
				return err
			}
			if removed {
				change.Remove(target.directory, member.Name)
			}
		}
		return nil
	})
}

// ListGroupInstalls 列出全部组安装,按 Group、Agent 排序,全局排在同一 Agent 的项目 Scope 之前。
func (l *Library) ListGroupInstalls(ctx context.Context) (installs []GroupInstall, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	rows, err := l.store.ListGroupInstalls(ctx, 0)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		installs = append(installs, GroupInstall{GroupID: GroupID(row.GroupID), Scope: scopeRowOf(row.Scope).scope(), CreatedAt: row.CreatedAt})
	}
	return installs, nil
}

// ListSkillScopes 列出全部 SkillScope:Scope 身份、Resolve 出的 Agent 目录及留着它的直接安装与组安装。
func (l *Library) ListSkillScopes(ctx context.Context) (skillScopes []SkillScope, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	return l.listSkillScopes(ctx, 0, nil)
}

func (l *Library) listSkillScopes(ctx context.Context, skillID SkillID, scope *scopeRow) ([]SkillScope, error) {
	var filter *store.Scope
	if scope != nil {
		filter = new(scope.record())
	}
	rows, err := l.store.ListSkillScopes(ctx, int64(skillID), filter)
	if err != nil {
		return nil, err
	}
	var skillScopes []SkillScope
	for _, row := range rows {
		skillScope := SkillScope{SkillID: SkillID(row.SkillID), Scope: scopeRowOf(row.Scope).scope(), Direct: row.Direct, CreatedAt: row.CreatedAt}
		if skillScope.Directory, err = l.Resolve(ctx, skillScope.Scope); err != nil {
			return nil, err
		}
		for _, id := range row.Groups {
			skillScope.Groups = append(skillScope.Groups, GroupID(id))
		}
		skillScopes = append(skillScopes, skillScope)
	}
	return skillScopes, nil
}
