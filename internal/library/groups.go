package library

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/tiaot33/skills-tui/internal/library/internal/agentdir"
	"github.com/tiaot33/skills-tui/internal/library/internal/store"
)

func groupOf(row store.Group) Group {
	return Group{ID: GroupID(row.ID), Name: row.Name, CreatedAt: row.CreatedAt}
}

// CreateGroup 创建分组。名字先 TrimSpace;若已被其他 Group 占用,则改为 name#N
// (N 从 2 起取最小空闲数字),大小写敏感、不加空格。空名仍是 InvalidArgument。
func (l *Library) CreateGroup(ctx context.Context, name string) (group Group, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	name = strings.TrimSpace(name)
	if name == "" {
		return Group{}, argumentError("create group", "name", "name is required")
	}
	taken, err := l.store.GroupNames(ctx)
	if err != nil {
		return Group{}, err
	}
	name = uniqueGroupName(name, taken)
	createdAt := clockNow()
	id, err := l.store.CreateGroup(ctx, name, createdAt)
	if err != nil {
		return Group{}, err
	}
	return Group{ID: GroupID(id), Name: name, CreatedAt: createdAt}, nil
}

// ListGroups 按 Group ID 升序列出全部分组。
func (l *Library) ListGroups(ctx context.Context) (groups []Group, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	rows, err := l.store.ListGroups(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		groups = append(groups, groupOf(row))
	}
	return groups, nil
}

// RenameGroup 重命名分组并返回最终生效的 Group。唯一规则与 CreateGroup 相同;
// 改成自己当前的名字则不变。空名仍是 InvalidArgument。
func (l *Library) RenameGroup(ctx context.Context, id GroupID, name string) (group Group, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	name = strings.TrimSpace(name)
	if name == "" {
		return Group{}, argumentError("rename group", "name", "name is required")
	}
	row, err := l.store.GetGroup(ctx, int64(id))
	if err != nil {
		return Group{}, err
	}
	current := groupOf(row)
	if current.Name == name {
		return current, nil
	}
	taken, err := l.store.GroupNames(ctx)
	if err != nil {
		return Group{}, err
	}
	delete(taken, current.Name)
	name = uniqueGroupName(name, taken)
	if err := l.store.RenameGroup(ctx, int64(id), name); err != nil {
		return Group{}, err
	}
	current.Name = name
	return current, nil
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

// DeleteGroup 删除分组及其全部组安装。非破坏性:只由该组安装留着的技能会先转为直接安装,
// 磁盘上的技能文件夹不会因删除分组而被移除。
func (l *Library) DeleteGroup(ctx context.Context, id GroupID) (err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	return l.store.InTx(ctx, func(tx *store.Tx) error {
		return tx.DeleteGroup(ctx, int64(id))
	})
}

// AddSkillToGroup 加入成员,并安装到该组已安装到的每个 Scope,按 ListGroupInstalls 的顺序逐条返回结果。
// 该 skill 已安装在某 Scope 时只补上组安装,不写盘;目标被占用、有未记录文件夹、与组内尚未安装的
// 成员同名或作用域根目录不存在时跳过该 Scope,成员关系照常成立。不带覆盖开关:要覆盖某个 Scope 的
// 未记录文件夹,先 PlanGroupInstall 再对该 Scope 调 InstallGroup(Overwrite)。已是成员返回 AlreadyExists。
// I/O 失败或取消时整次复原,连同成员关系。
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
		target, err := l.resolveScope(ctx, "add skill to group", scopeRowOf(install.Scope).scope())
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

// RemoveSkillFromGroup 移出成员;该 skill 在本组已安装到的各 Scope 上失去本组的组安装。
// 因此没有直接安装或组安装留着的,按 opts 处理:keep 改为直接安装、磁盘不动;uninstall 连文件夹一起移除。
// 不是成员返回 NotFound。
func (l *Library) RemoveSkillFromGroup(ctx context.Context, groupID GroupID, skillID SkillID, opts RemoveFromGroupOptions) (err error) {
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if err := validateMemberRemoval("remove skill from group", opts.IfNoOtherInstall); err != nil {
		return err
	}
	member, err := l.store.IsMember(ctx, int64(groupID), int64(skillID))
	if err != nil {
		return err
	}
	if !member {
		return &Error{Code: NotFound, Message: "remove skill from group: skill is not a member"}
	}
	skillScopes, err := l.store.MemberSkillScopes(ctx, int64(groupID), int64(skillID), nil)
	if err != nil {
		return err
	}
	directories := make([]string, len(skillScopes))
	for i, skillScope := range skillScopes {
		if directories[i], err = l.Resolve(ctx, scopeRowOf(skillScope.Scope).scope()); err != nil {
			return err
		}
	}
	var change agentdir.Change
	defer func() { err = errors.Join(err, change.Rollback()) }()
	return l.applyChange(ctx, &change, func(tx *store.Tx) error {
		if err := tx.RemoveMember(ctx, int64(groupID), int64(skillID)); err != nil {
			return err
		}
		for i, skillScope := range skillScopes {
			if opts.IfNoOtherInstall != UninstallMember {
				if err := tx.KeepAsDirectInstall(ctx, skillScope.ID); err != nil {
					return err
				}
				continue
			}
			removed, err := tx.UninstallMember(ctx, skillScope.ID)
			if err != nil {
				return err
			}
			if removed {
				change.Remove(directories[i], skillScope.Name)
			}
		}
		return nil
	})
}

// ListGroupSkills 列出分组成员的精简摘要(不含文件正文)。
func (l *Library) ListGroupSkills(ctx context.Context, groupID GroupID) (skills []SkillSummary, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if err := l.requireGroup(ctx, groupID); err != nil {
		return nil, err
	}
	return summariesOf(l.store.ListGroupSummaries(ctx, int64(groupID)))
}

// ListGroupsWithMembers 一次列出全部分组及其成员 ID,按 Group ID 升序;空组也在列。
// 不属于任何组的 skill 不出现在任何 Members 里。
func (l *Library) ListGroupsWithMembers(ctx context.Context) (groups []GroupWithMembers, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	rows, err := l.store.ListGroupsWithMembers(ctx)
	if err != nil {
		return nil, err
	}
	groups = []GroupWithMembers{}
	for _, row := range rows {
		group := GroupWithMembers{Group: groupOf(row.Group), Members: []SkillID{}}
		for _, id := range row.Members {
			group.Members = append(group.Members, SkillID(id))
		}
		groups = append(groups, group)
	}
	return groups, nil
}

func (l *Library) requireGroup(ctx context.Context, id GroupID) error {
	_, err := l.store.GetGroup(ctx, int64(id))
	return err
}
