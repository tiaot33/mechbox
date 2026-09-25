package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
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
		var (
			group     Group
			createdAt string
		)
		if err := rows.Scan(&group.ID, &group.Name, &createdAt); err != nil {
			return nil, fmt.Errorf("read group: %w", err)
		}
		if group.CreatedAt, err = parseStoredTime(createdAt); err != nil {
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
	var (
		group     Group
		createdAt string
	)
	if err := q.QueryRowContext(ctx, `SELECT id, name, created_at FROM groups WHERE id = ?`, id).Scan(&group.ID, &group.Name, &createdAt); err != nil {
		return Group{}, err
	}
	parsed, err := parseStoredTime(createdAt)
	if err != nil {
		return Group{}, err
	}
	group.CreatedAt = parsed
	return group, nil
}

func (l *sqliteLibrary) DeleteGroup(ctx context.Context, id GroupID) (err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete group: %w", err)
	}
	defer tx.Rollback()
	// 本组组激活覆盖、且没有其他激活撑着的投影先转为直接激活,再删组激活。
	if _, err := tx.ExecContext(ctx, `UPDATE projections SET direct = 1
		WHERE direct = 0
			AND skill_id IN (SELECT skill_id FROM skill_groups WHERE group_id = ?)
			AND EXISTS (SELECT 1 FROM group_activations WHERE group_id = ? AND agent = projections.agent AND project_id IS projections.project_id)
			AND NOT `+groupCoverageSQL, id, id, id); err != nil {
		return fmt.Errorf("preserve group projections: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM group_activations WHERE group_id = ?`, id); err != nil {
		return fmt.Errorf("delete group activations: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM skill_groups WHERE group_id = ?`, id); err != nil {
		return fmt.Errorf("delete group members: %w", err)
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

func (l *sqliteLibrary) AddSkillToGroup(ctx context.Context, groupID GroupID, skillID SkillID, opts ActivateOptions) (results []GroupMemberResult, err error) {
	// 加入成员会投影到组已激活的各 Scope,与激活一样持独占锁。
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if err := validateConflictPolicy("add skill to group", opts.OnConflict); err != nil {
		return nil, err
	}
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
	activations, err := l.readGroupActivations(ctx, groupID)
	if err != nil {
		return nil, err
	}
	results = make([]GroupMemberResult, 0, len(activations))
	var placements []*placement
	planned := map[string]bool{}
	for _, activation := range activations {
		target, err := l.resolveScope(ctx, "add skill to group", activation.scope())
		if err != nil {
			return nil, err
		}
		result, item, err := l.planMember(ctx, "add skill to group", skillID, name, target, opts, duplicate, planned)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
		if item != nil {
			placements = append(placements, item)
		}
	}
	now := clockNow()
	err = l.place(ctx, placements, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO skill_groups(skill_id, group_id) VALUES (?, ?)`, skillID, groupID); err != nil {
			return fmt.Errorf("add skill to group: %w", err)
		}
		for _, item := range placements {
			if err := recordProjectionTx(ctx, tx, item.skill.ID, item.scope.scopeRow, false, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

func (l *sqliteLibrary) RemoveSkillFromGroup(ctx context.Context, groupID GroupID, skillID SkillID, opts RemoveFromGroupOptions) (err error) {
	// withdraw 会移除磁盘上的投影,与停用一样持独占锁。
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if err := validateMemberRemoval("remove skill from group", opts.Projections); err != nil {
		return err
	}
	var exists int
	if err := l.db.QueryRowContext(ctx, `SELECT 1 FROM skill_groups WHERE group_id = ? AND skill_id = ?`, groupID, skillID).Scan(&exists); err != nil {
		return fmt.Errorf("remove skill from group: %w", err)
	}
	// 本组激活到的各 Scope 上该成员的投影;目录在开事务前算好(事务期间不能再走 l.db)。
	projections, err := l.memberProjections(ctx, groupID, skillID, scopeRow{})
	if err != nil {
		return err
	}
	targets := make([]string, len(projections))
	for i, projection := range projections {
		directory, err := l.Resolve(ctx, projection.scope())
		if err != nil {
			return err
		}
		targets[i] = filepath.Join(directory, projection.name)
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin remove skill from group: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM skill_groups WHERE group_id = ? AND skill_id = ?`, groupID, skillID); err != nil {
		return fmt.Errorf("remove skill from group: %w", err)
	}
	// 成员关系删掉后,本组的组激活不再覆盖这些投影;没有其他激活撑着的按 opts 处理。
	var trash projectionTrash
	for i, projection := range projections {
		if opts.Projections == WithdrawProjection {
			removed, err := dropUnbackedProjection(ctx, tx, projection.id)
			if err != nil {
				return err
			}
			if removed {
				trash.targets = append(trash.targets, targets[i])
			}
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE projections SET direct = 1 WHERE id = ? AND direct = 0 AND NOT `+groupCoverageSQL, projection.id, 0); err != nil {
			return fmt.Errorf("preserve member projection: %w", err)
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

func (l *sqliteLibrary) requireGroup(ctx context.Context, id GroupID) error {
	var exists int
	if err := l.db.QueryRowContext(ctx, `SELECT 1 FROM groups WHERE id = ?`, id).Scan(&exists); err != nil {
		return fmt.Errorf("get group: %w", err)
	}
	return nil
}
