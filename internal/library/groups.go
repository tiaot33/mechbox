package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/tiaot33/skills-tui/internal/library/internal/agentdir"
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
	covered, err := l.memberSkillScopes(ctx, id, 0, scopeRow{})
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
	for _, skillScope := range covered {
		if err := keepAsDirectInstall(ctx, tx, skillScope.id); err != nil {
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
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if err := l.requireGroup(ctx, groupID); err != nil {
		return nil, err
	}
	if err := l.store.RequireSkill(ctx, int64(skillID)); err != nil {
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
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if err := validateMemberRemoval("remove skill from group", opts.IfNoOtherInstall); err != nil {
		return err
	}
	var exists int
	if err := l.db.QueryRowContext(ctx, `SELECT 1 FROM skill_groups WHERE group_id = ? AND skill_id = ?`, groupID, skillID).Scan(&exists); err != nil {
		return fmt.Errorf("remove skill from group: %w", err)
	}
	skillScopes, err := l.memberSkillScopes(ctx, groupID, skillID, scopeRow{})
	if err != nil {
		return err
	}
	targets := make([]struct{ directory, name string }, len(skillScopes))
	for i, skillScope := range skillScopes {
		directory, err := l.Resolve(ctx, skillScope.scope())
		if err != nil {
			return err
		}
		targets[i].directory, targets[i].name = directory, skillScope.name
	}
	var change agentdir.Change
	defer func() { err = errors.Join(err, change.Rollback()) }()
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin remove skill from group: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM skill_groups WHERE group_id = ? AND skill_id = ?`, groupID, skillID); err != nil {
		return fmt.Errorf("remove skill from group: %w", err)
	}
	for i, skillScope := range skillScopes {
		if opts.IfNoOtherInstall == UninstallMember {
			removed, err := uninstallMember(ctx, tx, skillScope.id)
			if err != nil {
				return err
			}
			if removed {
				change.Remove(targets[i].directory, targets[i].name)
			}
			continue
		}
		if err := keepAsDirectInstall(ctx, tx, skillScope.id); err != nil {
			return err
		}
	}
	return commitChange(ctx, tx, &change, "remove skill from group")
}

func (l *sqliteLibrary) ListGroupSkills(ctx context.Context, groupID GroupID) (skills []SkillSummary, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if err := l.requireGroup(ctx, groupID); err != nil {
		return nil, err
	}
	return summariesOf(l.store.ListGroupSummaries(ctx, int64(groupID)))
}

func (l *sqliteLibrary) ListGroupsWithMembers(ctx context.Context) (groups []GroupWithMembers, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
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
