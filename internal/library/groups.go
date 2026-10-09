package library

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tiaot33/skills-tui/internal/library/internal/agentdir"
	"github.com/tiaot33/skills-tui/internal/library/internal/store"
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

// MemberRemovalPolicy 决定把技能移出组时,失去该组组安装的技能如何处理。与 FolderPolicy
// 含义与默认值都不同:只影响因移出而没有直接安装或组安装留着的技能,默认保留。
type MemberRemovalPolicy string

const (
	KeepAsDirectInstall MemberRemovalPolicy = "keep"      // 改为直接安装,磁盘不动(默认,空字符串同义)
	UninstallMember     MemberRemovalPolicy = "uninstall" // 不补直接安装,连同磁盘上的文件夹一起移除
)

// RemoveFromGroupOptions 是 RemoveSkillFromGroup 的选项。
type RemoveFromGroupOptions struct {
	IfNoOtherInstall MemberRemovalPolicy `json:"if_no_other_install"` // 只管移出后没有直接安装或组安装留着的技能;默认 KeepAsDirectInstall
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
	return Group{ID: GroupID(id), Name: name, CreatedAt: createdAt, Members: []SkillID{}}, nil
}

// ListGroups 按组 ID 升序列出全部组及各自的成员;空组也在列。没有组时返回空列表。
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
	row, err := l.store.GetGroup(ctx, int64(id))
	if err != nil {
		return Group{}, err
	}
	if row.Members, err = l.store.GroupMembers(ctx, int64(id)); err != nil {
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

// DeleteGroup 删除组,连同它在各作用域的组安装。只因为这个组才装上的技能改为直接安装,
// 磁盘上的技能文件夹不动。组不存在返回 NotFound。
func (l *Library) DeleteGroup(ctx context.Context, id GroupID) (err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	return l.store.InTx(ctx, func(tx *store.Tx) error {
		return tx.DeleteGroup(ctx, int64(id))
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

// RemoveSkillFromGroup 把技能移出组;它在这个组装到的各作用域里失去这个组的组安装。
// 没有直接安装或别的组安装留着的,按 opts 处理:保留(改为直接安装、磁盘不动,默认)或一并卸载。
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
		if _, directories[i], err = l.scopeDirectory(ctx, scopeRowOf(skillScope.Scope)); err != nil {
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
				change.Remove(filepath.Join(directories[i], skillScope.Name))
			}
		}
		return nil
	})
}

func (l *Library) requireGroup(ctx context.Context, id GroupID) error {
	_, err := l.store.GetGroup(ctx, int64(id))
	return err
}
