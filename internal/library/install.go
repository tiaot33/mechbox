package library

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/tiaot33/skills-tui/internal/library/internal/agentdir"
	"github.com/tiaot33/skills-tui/internal/library/internal/skillfile"
	"github.com/tiaot33/skills-tui/internal/library/internal/store"
)

// InstallOptions 是 Install 与 InstallGroup 的选项。
type InstallOptions struct {
	// Overwrite 同意覆盖目标处不是从库里装的同名文件夹;目标被另一个技能占用时仍然不装。默认不覆盖。
	Overwrite bool `json:"overwrite"`
}

// InstallPlanStatus 是安装前,一个技能在所选作用域的情况。和执行时的判定一致,与是否同意覆盖无关。
type InstallPlanStatus string

const (
	InstallReady            InstallPlanStatus = "ready"             // 可以安装
	InstallAlreadyInstalled InstallPlanStatus = "already_installed" // 已经装在这里(含装在共用这个目录的另一个 Agent),安装只会补记这次选择
	InstallOccupied         InstallPlanStatus = "occupied"          // 另一个技能已经装在这里(见 OccupierSkillID),先卸载它
	InstallUntracked        InstallPlanStatus = "untracked"         // 这里有同名文件夹但不是从库里装的,可以选择覆盖
	InstallDuplicate        InstallPlanStatus = "duplicate"         // 仅组安装计划:组里还有同名技能,这几个都不会装;优先于 occupied / untracked / ready
)

// InstallPlan 是安装前看到的一个技能在所选作用域的情况。只读,不校验同名文件夹的内容或来历,也不是执行令牌。
type InstallPlan struct {
	SkillID SkillID           `json:"skill_id"`
	Name    string            `json:"name"`
	Folder  string            `json:"folder"` // 这个技能在该作用域的文件夹路径
	Status  InstallPlanStatus `json:"status"`
	// OccupierSkillID 是已经装在 Folder 的技能,仅 occupied 时非零;界面用它查占用者来自哪个来源。
	// 同一目录里同名才算占用,所以占用者与本技能同名。本技能的另一个作用域恰好算出同一目录时,它等于 SkillID。
	OccupierSkillID SkillID `json:"occupier_skill_id,omitzero"`
}

// GroupInstallPlan 按组成员顺序列出各自的安装计划。组里同名的成员中尚未安装的标为 duplicate,
// 组安装时都会被跳过;已经装在这里的那个仍是 already_installed,照常只补记组安装。
type GroupInstallPlan struct {
	Members []InstallPlan `json:"members"`
}

// SkillScope 是某个技能装在某个 Scope:只要还有直接安装或组安装留着它就存在,最后一个撤掉时移除。
// 对照磁盘的状态由 InspectSkillScopes 给出。几个 Agent 共用一个目录时,装在其中一个,共用的每个 Agent 各列一条,
// 它们的 Folder、Direct、Groups、CreatedAt 相同,按共用的这几处合起来算。
type SkillScope struct {
	SkillID   SkillID   `json:"skill_id"`
	Scope     Scope     `json:"scope"`
	Folder    string    `json:"folder"`              // 这个技能在该作用域的文件夹路径,与 InstallPlan.Folder 同义;按当前的 Agent 目录与项目路径算出。确认改名后没跟着重新安装的地方仍是旧名字的文件夹
	Direct    bool      `json:"direct"`              // 有直接安装留着它;为 false 时只靠组安装留在这里,不能单独卸载
	Groups    []GroupID `json:"groups,omitempty"`    // 组安装留着它的 Group:该技能是成员且组已安装到此 Scope,按 ID 升序
	CreatedAt time.Time `json:"created_at,omitzero"` // 首次安装到此 Scope 的时间。再次安装不更新
	// SharedWith 是同一层级与这个 Agent 共用这个文件夹的其他 Agent,按代码排序;没有共用时为空。
	// 从其中任何一个卸载都撤销各处的直接安装;没有组安装还留着其中某一处时文件夹随之删除,它们都不再算已装。
	// 卸载前界面据此提示。
	SharedWith []AgentCode `json:"shared_with,omitempty"`
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
	MemberInstalled        GroupMemberOutcome = "installed"         // 装到了这里
	MemberAlreadyInstalled GroupMemberOutcome = "already_installed" // 已经装在这里(含装在共用这个目录的另一个 Agent),只补记组安装,文件夹不动
	MemberSkipped          GroupMemberOutcome = "skipped"           // 没装,原因见 Error;成员关系与组安装照常成立
)

// GroupMemberResult 是一个成员在一个 Scope 上的结果。Error 仅在 MemberSkipped 时非空,按跳过原因:
//   - 被另一个技能占用:FailedPrecondition,Details.SkillID / Name / Path 为占用者与其文件夹;
//   - 组里同名:FailedPrecondition,Details.Name 为同名的名字,Path 为共同的文件夹;
//   - 作用域根目录不存在(仅加入成员时):FailedPrecondition,Details.Path 为根目录,项目带 ProjectID;
//   - 有不是从库里装的同名文件夹且没同意覆盖:AlreadyExists,Details.Path 为该文件夹。
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
		claimed, err := l.resolveRow(ctx, scopeRowOf(other.Scope))
		if err != nil {
			return nil, err
		}
		if agentdir.Canonical(claimed.directory) != want {
			continue
		}
		return &claimant{skillID: SkillID(other.SkillID), name: other.Name}, nil
	}
	return nil, nil
}

func (l *Library) placementConflict(ctx context.Context, operation string, id SkillID, name string, target resolvedScope, overwrite bool) (*Error, error) {
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
	if exists && !overwrite {
		return existsError(operation, "target already exists", ErrorDetails{Path: path}), nil
	}
	return nil, nil
}

// PlanInstall 给出技能装到 scope 会怎样:装到哪个文件夹、那里现在是什么情况。只读,不创建目录。
// 未知技能、未知 Agent 或未登记项目返回 NotFound;作用域根目录不存在(项目不在这台机器上)时与 Install 一样
// 返回 FailedPrecondition(Details.Path 为根目录,项目带 ProjectID)。
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

// PlanGroupInstall 逐个成员给出组装到 scope 会怎样;空组返回空成员列表。组安装之后再看一次,
// 状态不是 already_installed 的就是还没装上的成员。作用域根目录不存在时与 InstallGroup 一样返回 FailedPrecondition。
func (l *Library) PlanGroupInstall(ctx context.Context, groupID GroupID, scope Scope) (plan GroupInstallPlan, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	target, err := l.resolveScope(ctx, "plan group install", scope)
	if err != nil {
		return plan, err
	}
	if err := l.requireGroup(ctx, groupID); err != nil {
		return plan, err
	}
	members, err := l.skillsOf(l.store.ListGroupSkills(ctx, int64(groupID)))
	if err != nil {
		return plan, err
	}
	if err := target.requireRoot("plan group install"); err != nil {
		return plan, err
	}
	counts := memberNameCounts(members)
	plan.Members = make([]InstallPlan, 0, len(members))
	for _, member := range members {
		item, err := l.planInstall(ctx, member.ID, member.Name, target)
		if err != nil {
			return GroupInstallPlan{}, err
		}
		if counts[member.Name] > 1 && item.Status != InstallAlreadyInstalled {
			item.Status = InstallDuplicate
			item.OccupierSkillID = 0
		}
		plan.Members = append(plan.Members, item)
	}
	return plan, nil
}

func memberNameCounts(members []Skill) map[string]int {
	counts := make(map[string]int, len(members))
	for _, member := range members {
		counts[member.Name]++
	}
	return counts
}

func (l *Library) planInstall(ctx context.Context, id SkillID, name string, scope resolvedScope) (InstallPlan, error) {
	plan := InstallPlan{SkillID: id, Name: name, Folder: filepath.Join(scope.directory, name), Status: InstallReady}
	place, err := l.store.PlaceSkillScopes(ctx, int64(id), scope.record())
	if err != nil {
		return plan, err
	}
	if len(place) > 0 {
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
		return plan, nil
	}
	exists, err := agentdir.Exists(plan.Folder)
	if err != nil {
		return plan, fmt.Errorf("inspect target folder: %w", err)
	}
	if exists {
		plan.Status = InstallUntracked
	}
	return plan, nil
}

// Install 把技能的当前版本装到 scope:记一个直接安装,并放入技能文件夹;成功时文件夹完整就位,失败时磁盘和库都保持原样。
// 本技能已经装在这里、或装在共用这个目录的另一个 Agent 时只补记这次选择,文件夹不动(文件夹丢了也不补回,放回去用 Reinstall)。
// 目标处有不是从库里装的同名文件夹时返回 AlreadyExists(Details.Path),opts.Overwrite 同意时覆盖它;
// 被另一个技能占用时返回 FailedPrecondition(Details.SkillID / Name / Path),需先卸载占用者。
// 作用域根目录(全局为用户主目录,项目为已登记路径)不存在时返回 FailedPrecondition;Library 从不创建根目录,
// 只创建其下的 Agent 目录。
func (l *Library) Install(ctx context.Context, id SkillID, scope Scope, opts InstallOptions) (err error) {
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	target, err := l.resolveScope(ctx, "install", scope)
	if err != nil {
		return err
	}
	skill, current, err := l.getCurrent(ctx, id)
	if err != nil {
		return err
	}
	if err := target.requireRoot("install skill"); err != nil {
		return err
	}
	place, err := l.store.PlaceSkillScopes(ctx, int64(id), target.record())
	if err != nil {
		return err
	}
	if len(place) > 0 && place[0].Agent == string(target.agent) {
		return l.store.MarkDirect(ctx, place[0].ID)
	}
	if len(place) > 0 {
		return l.store.InTx(ctx, func(tx *store.Tx) error {
			return tx.RecordSkillScope(ctx, int64(skill.ID), target.record(), true, clockNow())
		})
	}
	conflict, err := l.placementConflict(ctx, "install skill", skill.ID, skill.Name, target, opts.Overwrite)
	if err != nil {
		return err
	}
	if conflict != nil {
		return conflict
	}
	placements := []*placement{{skill: skill, files: current.files, scope: target}}
	return l.place(ctx, placements, func(tx *store.Tx) error {
		return tx.RecordSkillScope(ctx, int64(skill.ID), target.record(), true, clockNow())
	})
}

type placement struct {
	skill    Skill
	files    []skillfile.File
	scope    resolvedScope
	replaces string
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
		if err := change.Stage(ctx, item.scope.directory, item.skill.Name, item.files); err != nil {
			return err
		}
		if item.replaces != "" && item.replaces != item.skill.Name {
			change.Remove(filepath.Join(item.scope.directory, item.replaces))
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

// Uninstall 撤销直接安装;没有组安装留着它时,技能文件夹随之删除。技能只靠组安装留在这里时返回
// FailedPrecondition(Details.Name 为组名;界面可按 SkillScope.Direct 提前得知),没装在这里返回 NotFound。
// 几个 Agent 共用这个文件夹时(见 SkillScope.SharedWith),从其中任何一个卸载都撤销它们各自的直接安装,
// 文件夹随之删除,它们都不再算已装;只有组安装还留着其中某一处时文件夹才留下。
// 不检查文件夹是否被改过:确认前用 InspectSkillScopes 查出 modified 的地方,告诉用户这些修改会丢掉。
func (l *Library) Uninstall(ctx context.Context, id SkillID, scope Scope) (err error) {
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	target, err := l.resolveScope(ctx, "uninstall", scope)
	if err != nil {
		return err
	}
	place, err := l.store.PlaceSkillScopes(ctx, int64(id), target.record())
	if err != nil {
		return err
	}
	if len(place) == 0 {
		return &Error{Code: NotFound, Message: "uninstall skill: skill is not installed in scope"}
	}
	if !slices.ContainsFunc(place, func(row store.PlaceSkillScope) bool { return row.Direct }) {
		covered := target.record()
		covered.Agent = place[0].Agent
		name, err := l.store.CoveringGroupName(ctx, int64(id), covered)
		if err != nil {
			return err
		}
		return preconditionError("uninstall skill", "skill is only installed here by a group install", ErrorDetails{Name: name})
	}
	var change agentdir.Change
	defer func() { err = errors.Join(err, change.Rollback()) }()
	return l.applyChange(ctx, &change, func(tx *store.Tx) error {
		kept := false
		for _, row := range place {
			removed, err := tx.Uninstall(ctx, row.ID)
			if err != nil {
				return err
			}
			kept = kept || !removed
		}
		if !kept {
			change.Remove(filepath.Join(target.directory, place[0].Folder))
		}
		return nil
	})
}

type memberPlan struct {
	operation  string
	opts       InstallOptions
	results    []GroupMemberResult
	placements []*placement
	records    []memberRecord
	planned    map[string]*placement
}

type memberRecord struct {
	skillID SkillID
	scope   resolvedScope
}

func newMemberPlan(operation string, opts InstallOptions) *memberPlan {
	return &memberPlan{operation: operation, opts: opts, results: []GroupMemberResult{}, planned: map[string]*placement{}}
}

func (l *Library) planMember(ctx context.Context, plan *memberPlan, id SkillID, name string, target resolvedScope, duplicate bool) error {
	result := GroupMemberResult{SkillID: id, Scope: target.scope(), Outcome: MemberSkipped}
	place, err := l.store.PlaceSkillScopes(ctx, int64(id), target.record())
	if err != nil {
		return err
	}
	key := filepath.Join(agentdir.Canonical(target.directory), name)
	planned := plan.planned[key]
	switch {
	case len(place) > 0:
		result.Outcome = MemberAlreadyInstalled
		if place[0].Agent != string(target.agent) {
			plan.records = append(plan.records, memberRecord{skillID: id, scope: target})
		}
	case planned != nil && planned.skill.ID == id && planned.scope.sharesFolderWith(target):
		result.Outcome = MemberInstalled
		plan.records = append(plan.records, memberRecord{skillID: id, scope: target})
	default:
		if result.Error, err = l.memberBlocker(ctx, plan, id, name, target, duplicate); err != nil {
			return err
		}
		if result.Error != nil {
			break
		}
		skill, current, err := l.getCurrent(ctx, id)
		if err != nil {
			return err
		}
		item := &placement{skill: skill, files: current.files, scope: target}
		plan.planned[key] = item
		plan.placements = append(plan.placements, item)
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
	if plan.planned[filepath.Join(agentdir.Canonical(target.directory), name)] != nil {
		return preconditionError(plan.operation, "another scope already installs this skill into the same directory", ErrorDetails{Path: target.directory}), nil
	}
	return l.placementConflict(ctx, plan.operation, id, name, target, plan.opts.Overwrite)
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
		for _, item := range plan.records {
			if err := tx.RecordSkillScope(ctx, int64(item.skillID), item.scope.record(), false, now); err != nil {
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

// InstallGroup 把组装到 scope:记一个组安装(空组也记;已有则不变),这是持续的选择,以后加入的成员也会装到这里(见 ADR 0006)。
// 按成员顺序逐个返回结果:能装的都装上;已经装在这里的只补记组安装,文件夹不动;被另一个技能占用、组里同名、
// 有不是从库里装的同名文件夹且没同意覆盖的成员跳过,原因见 GroupMemberResult,处理后再次 InstallGroup 可补齐。
// 作用域根目录不存在时同 Install 返回 FailedPrecondition,不记组安装。其余失败(I/O、取消)时磁盘和库都保持原样,
// 包括组安装记录。
func (l *Library) InstallGroup(ctx context.Context, groupID GroupID, scope Scope, opts InstallOptions) (_ []GroupMemberResult, err error) {
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
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
	members, err := l.skillsOf(l.store.ListGroupSkills(ctx, int64(groupID)))
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

// UninstallGroup 撤销该组在 scope 的组安装;不再有直接安装或别的组安装留着的成员随之卸载,失败时磁盘和库都保持原样。
// 共用目录的另一个 Agent 还装着某个成员时,它的文件夹留着。
// 该组没有组安装到这里返回 NotFound。和 Uninstall 一样不检查文件夹是否被改过。
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
			if !removed {
				continue
			}
			place, err := tx.PlaceSkillScopes(ctx, member.SkillID, member.Scope)
			if err != nil {
				return err
			}
			if len(place) == 0 {
				change.Remove(filepath.Join(target.directory, member.Name))
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

// ListSkillScopes 列出 filter 范围内技能装在哪些地方,以及留着它们的直接安装与组安装;零值 filter 列出全库,
// 筛选规则见 SkillScopeFilter。不读磁盘:只要列出位置(技能列表上的「装在 3 处」、组页)用它;
// 要知道每一处现在什么样(技能详情页、移除技能或卸载前的确认框)用 InspectSkillScopes。
func (l *Library) ListSkillScopes(ctx context.Context, filter SkillScopeFilter) (skillScopes []SkillScope, err error) {
	defer func() { err = typedError(err) }()
	return l.filteredSkillScopes(ctx, "list skill scopes", filter)
}

func (l *Library) listSkillScopes(ctx context.Context, skillID SkillID, scope *scopeRow) ([]SkillScope, error) {
	rows, err := l.store.ListSkillScopes(ctx, int64(skillID))
	if err != nil {
		return nil, err
	}
	type folder struct {
		skillID   int64
		projectID ProjectID
		relative  string
	}
	merged := map[folder]*SkillScope{}
	sharers := map[folder][]AgentCode{}
	var folders []folder
	for _, row := range rows {
		target, err := l.resolveRow(ctx, scopeRowOf(row.Scope))
		if err != nil {
			return nil, err
		}
		key := folder{skillID: row.SkillID, relative: target.relative}
		if target.projectID != nil {
			key.projectID = *target.projectID
		}
		entry := merged[key]
		if entry == nil {
			entry = &SkillScope{SkillID: SkillID(row.SkillID), Folder: filepath.Join(target.directory, row.Name), CreatedAt: row.CreatedAt}
			merged[key] = entry
			folders = append(folders, key)
			codes, err := l.store.FolderSharers(ctx, row.Scope)
			if err != nil {
				return nil, err
			}
			for _, code := range codes {
				sharers[key] = append(sharers[key], AgentCode(code))
			}
		}
		entry.Direct = entry.Direct || row.Direct
		if row.CreatedAt.Before(entry.CreatedAt) {
			entry.CreatedAt = row.CreatedAt
		}
		for _, id := range row.Groups {
			entry.Groups = append(entry.Groups, GroupID(id))
		}
	}
	var skillScopes []SkillScope
	for _, key := range folders {
		entry := *merged[key]
		slices.Sort(entry.Groups)
		entry.Groups = slices.Compact(entry.Groups)
		for _, agent := range sharers[key] {
			row := scopeRow{agent: agent}
			if key.projectID != 0 {
				row.projectID = new(key.projectID)
			}
			if scope != nil && row.scope() != scope.scope() {
				continue
			}
			skillScope := entry
			skillScope.Scope = row.scope()
			for _, other := range sharers[key] {
				if other != agent {
					skillScope.SharedWith = append(skillScope.SharedWith, other)
				}
			}
			skillScopes = append(skillScopes, skillScope)
		}
	}
	slices.SortFunc(skillScopes, func(a, b SkillScope) int {
		return cmp.Or(cmp.Compare(a.SkillID, b.SkillID), cmp.Compare(a.Scope.Agent, b.Scope.Agent), cmp.Compare(a.Scope.ProjectID, b.Scope.ProjectID))
	})
	return skillScopes, nil
}
