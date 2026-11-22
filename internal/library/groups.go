package library

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tiaot33/mechbox/internal/library/internal/agentdir"
	"github.com/tiaot33/mechbox/internal/library/internal/store"
)

// GroupID 是分组的数据库主键。
type GroupID int64

// Group 是手动创建的场景分组;与按来源自动形成的 Collection 相对(见 ADR 0006)。
// 只给出成员的技能 ID,技能信息从 ListSkills / GetSkill 取。
type Group struct {
	ID        GroupID   `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at,omitzero"` // 创建时间
	Members   []SkillID `json:"members"`             // 成员的技能 ID,按 ID 升序;空组为空列表
}

// MemberRemovalPolicy 决定把技能移出组或删除组时,失去该组组安装的技能如何处理。与 FolderPolicy
// 含义与默认值都不同:只影响因此没有直接安装或组安装留着的技能,默认保留。
type MemberRemovalPolicy string

const (
	KeepAsDirectInstall MemberRemovalPolicy = "keep"      // 改为直接安装,磁盘不动(默认,空字符串同义)
	UninstallMember     MemberRemovalPolicy = "uninstall" // 不补直接安装,连同磁盘上的文件夹一起移除
)

// RemoveFromGroupOptions 是 RemoveSkillFromGroup 的选项。
type RemoveFromGroupOptions struct {
	IfNoOtherInstall MemberRemovalPolicy `json:"if_no_other_install"` // 只管移出后没有直接安装或组安装留着的技能;默认 KeepAsDirectInstall
	// DiscardEdits 是确认时告诉用户被改过、修改会丢掉的技能文件夹(SkillScope.Folder)。动磁盘之前核对这次要删的每个文件夹:
	// 有被改过的(SkillScopeModified,不算 SkillScopeInspection.SourceLatest,也不算是符号链接的那一处,删的只是链接)却不在这里,
	// 整次返回 Conflict(Details.Paths 为这几个文件夹),一个都不删;列了而现在没被改过的照常删。不删文件夹时不核对。
	DiscardEdits []string `json:"discard_edits,omitempty"`
}

// DeleteGroupOptions 是 DeleteGroup 的选项。
type DeleteGroupOptions struct {
	IfNoOtherInstall MemberRemovalPolicy `json:"if_no_other_install"` // 只管删组后没有直接安装或组安装留着的技能;默认 KeepAsDirectInstall
	// DiscardEdits 是确认时告诉用户被改过、修改会丢掉的技能文件夹(SkillScope.Folder)。动磁盘之前核对这次要删的每个文件夹:
	// 有被改过的(SkillScopeModified,不算 SkillScopeInspection.SourceLatest,也不算是符号链接的那一处,删的只是链接)却不在这里,
	// 整次返回 Conflict(Details.Paths 为这几个文件夹),一个都不删;列了而现在没被改过的照常删。不删文件夹时不核对。
	DiscardEdits []string `json:"discard_edits,omitempty"`
}

func groupOf(row store.Group) Group {
	members := make([]SkillID, len(row.Members))
	for i, id := range row.Members {
		members[i] = SkillID(id)
	}
	return Group{ID: GroupID(row.ID), Name: row.Name, CreatedAt: row.CreatedAt, Members: members}
}

// CreateGroup 新建一个空组。名字去掉首尾空白;和已有组重名时改成 name#2、name#3…
// (取最小的空闲数字,大小写敏感、不加空格)。空名返回 InvalidArgument。
func (l *Library) CreateGroup(ctx context.Context, name string) (group Group, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	name = strings.TrimSpace(name)
	if name == "" {
		return Group{}, argumentError("create group", "name", "name is required")
	}
	createdAt := clockNow()
	var id int64
	err = l.store.InTx(ctx, func(tx *store.Tx) error {
		taken, err := tx.GroupNames(ctx)
		if err != nil {
			return err
		}
		name = uniqueGroupName(name, taken)
		id, err = tx.CreateGroup(ctx, name, createdAt)
		return err
	})
	if err != nil {
		return Group{}, err
	}
	return Group{ID: GroupID(id), Name: name, CreatedAt: createdAt, Members: []SkillID{}}, nil
}

// ListGroups 按用户排的先后(ReorderGroups)列出全部组及各自的成员,没排过的(从没排过、恢复了默认顺序,或排过之后才新建的)
// 按组 ID 升序接在后面;空组也在列。没有组时返回空列表。
func (l *Library) ListGroups(ctx context.Context) (groups []Group, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	rows, err := l.store.ListGroups(ctx)
	if err != nil {
		return nil, err
	}
	groups = []Group{}
	for _, row := range rows {
		groups = append(groups, groupOf(row))
	}
	return groups, nil
}

// RenameGroup 给组改名,返回改名后的组及其当前成员。名字规则与 CreateGroup 相同;
// 改成自己现在的名字时不变。空名返回 InvalidArgument,组不存在返回 NotFound。
func (l *Library) RenameGroup(ctx context.Context, id GroupID, name string) (group Group, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	name = strings.TrimSpace(name)
	if name == "" {
		return Group{}, argumentError("rename group", "name", "name is required")
	}
	err = l.store.InTx(ctx, func(tx *store.Tx) error {
		row, err := tx.GetGroup(ctx, int64(id))
		if err != nil {
			return err
		}
		if row.Members, err = tx.GroupMembers(ctx, int64(id)); err != nil {
			return err
		}
		group = groupOf(row)
		if group.Name == name {
			return nil
		}
		taken, err := tx.GroupNames(ctx)
		if err != nil {
			return err
		}
		delete(taken, group.Name)
		group.Name = uniqueGroupName(name, taken)
		return tx.RenameGroup(ctx, int64(id), group.Name)
	})
	if err != nil {
		return Group{}, err
	}
	return group, nil
}

// ReorderGroups 按 ids 的先后排列组,左栏把一个组拖到另一个组前后时调用:ids 里的排在前面,
// 没列出的按原来的先后接在后面;之后新建的组排在最后。ids 为空时恢复默认的先后,即组 ID 升序。
// 先后存在数据库里,随推送、拉回走。ids 有重复或不是正数返回 InvalidArgument(Details.Field 为 ids),
// 有组不存在返回 NotFound。
func (l *Library) ReorderGroups(ctx context.Context, ids []GroupID) (err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if err := validateOrder("reorder groups", ids); err != nil {
		return err
	}
	return l.store.InTx(ctx, func(tx *store.Tx) error {
		for _, id := range ids {
			if _, err := tx.GetGroup(ctx, int64(id)); err != nil {
				return err
			}
		}
		if len(ids) == 0 {
			return tx.SetGroupPositions(ctx, nil)
		}
		current, err := tx.GroupOrder(ctx)
		if err != nil {
			return err
		}
		return tx.SetGroupPositions(ctx, reordered(ids, current))
	})
}

func uniqueGroupName(name string, taken map[string]int64) string {
	if _, exists := taken[name]; !exists {
		return name
	}
	for n := 2; ; n++ {
		candidate := name + "#" + strconv.Itoa(n)
		if _, exists := taken[candidate]; !exists {
			return candidate
		}
	}
}

// DeleteGroup 删除组,连同它在各作用域的组安装。只因为这个组才装上的技能按 opts 处理:保留(改为直接安装、
// 磁盘不动,默认)或一并卸载;在同一作用域里有直接安装或别的组安装的照常保留。组不存在返回 NotFound。
// 一并卸载时某个文件夹移不走则整次复原;删之前核对文件夹有没有被改过,确认之后才被改过的返回 Conflict,不删组(见 DeleteGroupOptions)。
func (l *Library) DeleteGroup(ctx context.Context, id GroupID, opts DeleteGroupOptions) (err error) {
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if err := validateMemberRemoval("delete group", opts.IfNoOtherInstall); err != nil {
		return err
	}
	if err := l.requireGroup(ctx, id); err != nil {
		return err
	}
	released, err := l.releasedMemberScopes(ctx, int64(id), 0)
	if err != nil {
		return err
	}
	var change agentdir.Change
	defer func() { err = errors.Join(err, change.Rollback()) }()
	return l.applyChange(ctx, &change, func(tx *store.Tx) error {
		if err := tx.DeleteGroup(ctx, int64(id)); err != nil {
			return err
		}
		removals, err := releaseMemberScopes(ctx, tx, released, opts.IfNoOtherInstall)
		if err != nil {
			return err
		}
		return l.removeFolders(ctx, tx, &change, "delete group", removals, opts.DiscardEdits)
	})
}

// AddSkillToGroup 把技能加入组:组已经装到哪些作用域,就把它装到哪些作用域,按 ListGroupInstalls 的顺序
// 每个作用域返回一条结果。技能已经装在那里时只补记组安装,不写盘;目标被占用、有未记录文件夹、与组内
// 尚未安装的成员同名或作用域根目录不存在时跳过该作用域,成员关系照常成立。不提供覆盖开关(见 ADR 0006):
// 要覆盖某个作用域里不是从库里装的同名文件夹,先 PlanGroupInstall 再对该作用域调 InstallGroup 并同意覆盖。
// 已是成员返回 AlreadyExists。中途失败或取消时整次复原,连同成员关系。
func (l *Library) AddSkillToGroup(ctx context.Context, groupID GroupID, skillID SkillID) (_ []GroupMemberResult, err error) {
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if err := l.requireGroup(ctx, groupID); err != nil {
		return nil, err
	}
	if err := l.store.RequireSkill(ctx, int64(skillID)); err != nil {
		return nil, err
	}
	member, err := l.store.IsMember(ctx, int64(groupID), int64(skillID))
	if err != nil {
		return nil, err
	}
	if member {
		return nil, existsError("add skill to group", "skill is already a member", ErrorDetails{SkillID: skillID})
	}
	name, duplicate, err := l.store.MemberCandidate(ctx, int64(groupID), int64(skillID))
	if err != nil {
		return nil, err
	}
	installs, err := l.store.ListGroupInstalls(ctx, int64(groupID))
	if err != nil {
		return nil, err
	}
	plan := newMemberPlan("add skill to group", InstallOptions{})
	for _, install := range installs {
		target, err := l.resolveScope(ctx, "add skill to group", scopeFrom(install.Scope))
		if err != nil {
			return nil, err
		}
		if err := l.planMember(ctx, plan, skillID, name, target, duplicate); err != nil {
			return nil, err
		}
	}
	return l.applyMemberPlan(ctx, plan, func(tx *store.Tx, _ time.Time) error {
		return tx.AddMember(ctx, int64(groupID), int64(skillID))
	})
}

// RemoveSkillFromGroup 把技能移出组;它在这个组装到的各作用域里失去这个组的组安装。
// 没有直接安装或别的组安装留着的,按 opts 处理:保留(改为直接安装、磁盘不动,默认)或一并卸载;
// 一并卸载时删之前核对文件夹有没有被改过,确认之后才被改过的返回 Conflict,不移出(见 RemoveFromGroupOptions)。
// 不是成员返回 NotFound。
func (l *Library) RemoveSkillFromGroup(ctx context.Context, groupID GroupID, skillID SkillID, opts RemoveFromGroupOptions) (err error) {
	defer func() { err = typedError(err) }()
	return l.removeSkillsFromGroup(ctx, "remove skill from group", groupID, []SkillID{skillID}, opts)
}

// RemoveSkillsFromGroup 是一次把几个技能移出组(勾选几个后操作栏上的「移出组」):每个技能的规则与 RemoveSkillFromGroup 相同,
// 整批一次提交,全成或全不成。有一个不是成员,或要删的文件夹里有确认之后才被改过的,整次返回与 RemoveSkillFromGroup 相同的错误,
// 一个都不移出。同一个技能列了几次只移出一次。空列表什么都不做。
func (l *Library) RemoveSkillsFromGroup(ctx context.Context, groupID GroupID, skillIDs []SkillID, opts RemoveFromGroupOptions) (err error) {
	defer func() { err = typedError(err) }()
	return l.removeSkillsFromGroup(ctx, "remove skills from group", groupID, skillIDs, opts)
}

func (l *Library) removeSkillsFromGroup(ctx context.Context, operation string, groupID GroupID, skillIDs []SkillID, opts RemoveFromGroupOptions) (err error) {
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	if err := validateMemberRemoval(operation, opts.IfNoOtherInstall); err != nil {
		return err
	}
	skillIDs = slices.Compact(slices.Sorted(slices.Values(skillIDs)))
	var released []releasedMemberScope
	for _, skillID := range skillIDs {
		member, err := l.store.IsMember(ctx, int64(groupID), int64(skillID))
		if err != nil {
			return err
		}
		if !member {
			return &Error{Code: NotFound, Message: operation + ": skill is not a member"}
		}
		scopes, err := l.releasedMemberScopes(ctx, int64(groupID), int64(skillID))
		if err != nil {
			return err
		}
		released = append(released, scopes...)
	}
	var change agentdir.Change
	defer func() { err = errors.Join(err, change.Rollback()) }()
	return l.applyChange(ctx, &change, func(tx *store.Tx) error {
		for _, skillID := range skillIDs {
			if err := tx.RemoveMember(ctx, int64(groupID), int64(skillID)); err != nil {
				return err
			}
		}
		removals, err := releaseMemberScopes(ctx, tx, released, opts.IfNoOtherInstall)
		if err != nil {
			return err
		}
		return l.removeFolders(ctx, tx, &change, operation, removals, opts.DiscardEdits)
	})
}

type releasedMemberScope struct {
	store.MemberScope
	directory string
}

func (l *Library) releasedMemberScopes(ctx context.Context, groupID, skillID int64) ([]releasedMemberScope, error) {
	skillScopes, err := l.store.MemberSkillScopes(ctx, groupID, skillID, nil)
	if err != nil {
		return nil, err
	}
	released := make([]releasedMemberScope, len(skillScopes))
	for i, skillScope := range skillScopes {
		target, err := l.resolveRow(ctx, skillScope.Scope)
		if err != nil {
			return nil, err
		}
		released[i] = releasedMemberScope{MemberScope: skillScope, directory: target.directory}
	}
	return released, nil
}

func releaseMemberScopes(ctx context.Context, tx *store.Tx, released []releasedMemberScope, policy MemberRemovalPolicy) ([]removal, error) {
	var removals []removal
	for _, skillScope := range released {
		if policy != UninstallMember {
			if err := tx.KeepAsDirectInstall(ctx, skillScope.ID); err != nil {
				return nil, err
			}
			continue
		}
		removed, err := tx.UninstallMember(ctx, skillScope.ID)
		if err != nil {
			return nil, err
		}
		if !removed {
			continue
		}
		place, err := tx.PlaceSkillScopes(ctx, skillScope.SkillID, skillScope.Scope)
		if err != nil {
			return nil, err
		}
		if len(place) == 0 {
			removals = append(removals, removal{skillID: SkillID(skillScope.SkillID), folder: agentdir.FolderPath(skillScope.directory, skillScope.Name)})
		}
	}
	return removals, nil
}

func (l *Library) requireGroup(ctx context.Context, id GroupID) error {
	_, err := l.store.GetGroup(ctx, int64(id))
	return err
}
