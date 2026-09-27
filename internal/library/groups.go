package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (l *sqliteLibrary) CreateGroup(ctx context.Context, name string) (group Group, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	name = strings.TrimSpace(name)
	if name == "" {
		return Group{}, argumentError("create group", "name", "name is required")
	}
	taken, err := occupiedGroupNames(ctx, l.db, 0)
	if err != nil {
		return Group{}, err
	}
	name = uniqueGroupName(name, taken)
	createdAt := clockNow()
	result, err := l.db.ExecContext(ctx, `INSERT INTO groups(name, created_at) VALUES (?, ?)`, name, formatStoredTime(createdAt))
	if err != nil {
		return Group{}, fmt.Errorf("create group: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Group{}, fmt.Errorf("read group id: %w", err)
	}
	return Group{ID: GroupID(id), Name: name, CreatedAt: createdAt}, nil
}

func (l *sqliteLibrary) ListGroups(ctx context.Context) (groups []Group, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	rows, err := l.db.QueryContext(ctx, `SELECT id, name, created_at FROM groups ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list groups: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		group, err := scanGroup(rows)
		if err != nil {
			return nil, fmt.Errorf("read group: %w", err)
		}
		groups = append(groups, group)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read groups: %w", err)
	}
	return groups, nil
}

func (l *sqliteLibrary) RenameGroup(ctx context.Context, id GroupID, name string) (group Group, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	name = strings.TrimSpace(name)
	if name == "" {
		return Group{}, argumentError("rename group", "name", "name is required")
	}
	current, err := getGroup(ctx, l.db, id)
	if err != nil {
		return Group{}, fmt.Errorf("rename group: %w", err)
	}
	if current.Name == name {
		return current, nil
	}
	taken, err := occupiedGroupNames(ctx, l.db, id)
	if err != nil {
		return Group{}, err
	}
	name = uniqueGroupName(name, taken)
	result, err := l.db.ExecContext(ctx, `UPDATE groups SET name = ? WHERE id = ?`, name, id)
	if err != nil {
		return Group{}, fmt.Errorf("rename group: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return Group{}, fmt.Errorf("rename group: %w", err)
	}
	if changed == 0 {
		return Group{}, fmt.Errorf("rename group: %w", sql.ErrNoRows)
	}
	current.Name = name
	return current, nil
}

// uniqueGroupName 在 taken 里找一个空闲名字:原名可用则原样返回,否则从 name#2 起取最小空闲。
func uniqueGroupName(name string, taken map[string]GroupID) string {
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

func occupiedGroupNames(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, except GroupID) (map[string]GroupID, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, name FROM groups`)
	if err != nil {
		return nil, fmt.Errorf("list group names: %w", err)
	}
	defer rows.Close()
	taken := map[string]GroupID{}
	for rows.Next() {
		var (
			id   GroupID
			name string
		)
		if err := rows.Scan(&id, &name); err != nil {
			return nil, fmt.Errorf("read group name: %w", err)
		}
		if id != except {
			taken[name] = id
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read group names: %w", err)
	}
	return taken, nil
}

func getGroup(ctx context.Context, q rowQuerier, id GroupID) (Group, error) {
	return scanGroup(q.QueryRowContext(ctx, `SELECT id, name, created_at FROM groups WHERE id = ?`, id))
}

// scanGroup 读出一行开头的 groups.id、name、created_at 并解析创建时间;extra 接收同一行其后的列。
func scanGroup(row interface{ Scan(...any) error }, extra ...any) (Group, error) {
	var (
		group     Group
		createdAt string
	)
	if err := row.Scan(append([]any{&group.ID, &group.Name, &createdAt}, extra...)...); err != nil {
		return Group{}, err
	}
	var err error
	if group.CreatedAt, err = parseStoredTime(createdAt); err != nil {
		return Group{}, err
	}
	return group, nil
}

func (l *sqliteLibrary) DeleteGroup(ctx context.Context, id GroupID) (err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	// 本组组安装覆盖的安装项在开事务前取齐(事务期间不能再走 l.db)。删组不写盘,持共享锁即可:会新建安装项、
	// 改成员或组安装的操作都持独占锁,不会与它交错;能交错的 UnregisterProject / RemoveSkill 只删记录,
	// 被它们删掉的安装项在下面的保留判定里只是一次空更新,而保留判定本身在事务内按当时状态重新检查。
	covered, err := l.memberInstallations(ctx, id, 0, scopeRow{})
	if err != nil {
		return err
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete group: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM group_installs WHERE group_id = ?`, id); err != nil {
		return fmt.Errorf("delete group installs: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM skill_groups WHERE group_id = ?`, id); err != nil {
		return fmt.Errorf("delete group members: %w", err)
	}
	// 本组不再覆盖它们后,没有其他安装撑着的转为直接安装,磁盘不动。
	for _, installation := range covered {
		if err := keepUnbackedInstallation(ctx, tx, installation.id); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM groups WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete group: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete group: %w", err)
	}
	if changed == 0 {
		return fmt.Errorf("delete group: %w", sql.ErrNoRows)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete group: %w", err)
	}
	return nil
}

func (l *sqliteLibrary) AddSkillToGroup(ctx context.Context, groupID GroupID, skillID SkillID) (_ []GroupMemberResult, err error) {
	// 加入成员会安装到组已安装的各 Scope,与安装一样持独占锁。
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if err := l.requireGroup(ctx, groupID); err != nil {
		return nil, err
	}
	if err := l.requireSkill(ctx, skillID); err != nil {
		return nil, err
	}
	var exists int
	err = l.db.QueryRowContext(ctx, `SELECT 1 FROM skill_groups WHERE skill_id = ? AND group_id = ?`, skillID, groupID).Scan(&exists)
	if err == nil {
		return nil, existsError("add skill to group", "skill is already a member", ErrorDetails{SkillID: skillID})
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("add skill to group: %w", err)
	}
	var name string
	var duplicate bool
	if err := l.db.QueryRowContext(ctx, `SELECT name, EXISTS (
			SELECT 1 FROM skill_groups sg JOIN skills other ON other.id = sg.skill_id
			WHERE sg.group_id = ? AND other.name = skills.name)
		FROM skills WHERE id = ?`, groupID, skillID).Scan(&name, &duplicate); err != nil {
		return nil, fmt.Errorf("add skill to group: %w", err)
	}
	installs, err := l.readGroupInstalls(ctx, groupID)
	if err != nil {
		return nil, err
	}
	// 不带覆盖开关:未记录文件夹一律跳过;要覆盖某个作用域,调用方对该作用域用 InstallGroup(Overwrite)。
	plan := newMemberPlan("add skill to group", InstallOptions{})
	for _, install := range installs {
		target, err := l.resolveScope(ctx, "add skill to group", install.scope())
		if err != nil {
			return nil, err
		}
		if err := l.planMember(ctx, plan, skillID, name, target, duplicate); err != nil {
			return nil, err
		}
	}
	return l.applyMemberPlan(ctx, plan, func(tx *sql.Tx, _ time.Time) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO skill_groups(skill_id, group_id) VALUES (?, ?)`, skillID, groupID); err != nil {
			return fmt.Errorf("add skill to group: %w", err)
		}
		return nil
	})
}

func (l *sqliteLibrary) RemoveSkillFromGroup(ctx context.Context, groupID GroupID, skillID SkillID, opts RemoveFromGroupOptions) (err error) {
	// withdraw 会移除磁盘上的安装项,与卸载一样持独占锁。
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if err := validateMemberRemoval("remove skill from group", opts.Installations); err != nil {
		return err
	}
	var exists int
	if err := l.db.QueryRowContext(ctx, `SELECT 1 FROM skill_groups WHERE group_id = ? AND skill_id = ?`, groupID, skillID).Scan(&exists); err != nil {
		return fmt.Errorf("remove skill from group: %w", err)
	}
	// 本组安装到的各 Scope 上该成员的安装项;目录在开事务前算好(事务期间不能再走 l.db)。
	installations, err := l.memberInstallations(ctx, groupID, skillID, scopeRow{})
	if err != nil {
		return err
	}
	targets := make([]string, len(installations))
	for i, installation := range installations {
		directory, err := l.Resolve(ctx, installation.scope())
		if err != nil {
			return err
		}
		targets[i] = filepath.Join(directory, installation.name)
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin remove skill from group: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM skill_groups WHERE group_id = ? AND skill_id = ?`, groupID, skillID); err != nil {
		return fmt.Errorf("remove skill from group: %w", err)
	}
	// 成员关系删掉后,本组的组安装不再覆盖这些安装项;没有其他安装撑着的按 opts 处理。
	var trash installationTrash
	for i, installation := range installations {
		if opts.Installations == WithdrawInstallation {
			removed, err := dropUnbackedInstallation(ctx, tx, installation.id)
			if err != nil {
				return err
			}
			if removed {
				trash.targets = append(trash.targets, targets[i])
			}
			continue
		}
		if err := keepUnbackedInstallation(ctx, tx, installation.id); err != nil {
			return err
		}
	}
	return trash.commit(ctx, tx, "remove skill from group")
}

func (l *sqliteLibrary) ListGroupSkills(ctx context.Context, groupID GroupID) (skills []SkillSummary, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if err := l.requireGroup(ctx, groupID); err != nil {
		return nil, err
	}
	return l.listSummaries(ctx, "list group skills", `JOIN skill_groups ON skill_groups.skill_id = skills.id WHERE skill_groups.group_id = ? ORDER BY skills.id`, groupID)
}

func (l *sqliteLibrary) ListGroupsWithMembers(ctx context.Context) (groups []GroupWithMembers, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	// 组与成员由同一条 LEFT JOIN 读出,单条语句即一份读快照,不会读到删组或移除技能的中间状态;空组带一行 NULL 成员。
	rows, err := l.db.QueryContext(ctx, `SELECT groups.id, groups.name, groups.created_at, skill_groups.skill_id
		FROM groups LEFT JOIN skill_groups ON skill_groups.group_id = groups.id
		ORDER BY groups.id, skill_groups.skill_id`)
	if err != nil {
		return nil, fmt.Errorf("list groups with members: %w", err)
	}
	defer rows.Close()
	groups = []GroupWithMembers{}
	for rows.Next() {
		var member sql.NullInt64
		group, err := scanGroup(rows, &member)
		if err != nil {
			return nil, fmt.Errorf("read group with members: %w", err)
		}
		if n := len(groups); n == 0 || groups[n-1].Group.ID != group.ID {
			groups = append(groups, GroupWithMembers{Group: group, Members: []SkillID{}})
		}
		if member.Valid {
			last := &groups[len(groups)-1]
			last.Members = append(last.Members, SkillID(member.Int64))
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read groups with members: %w", err)
	}
	return groups, nil
}

func (l *sqliteLibrary) requireGroup(ctx context.Context, id GroupID) error {
	var exists int
	if err := l.db.QueryRowContext(ctx, `SELECT 1 FROM groups WHERE id = ?`, id).Scan(&exists); err != nil {
		return fmt.Errorf("get group: %w", err)
	}
	return nil
}
