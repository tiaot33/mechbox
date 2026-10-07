package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type Group struct {
	ID        int64
	Name      string
	CreatedAt time.Time
}

type GroupWithMembers struct {
	Group   Group
	Members []int64
}

func (q queries) GroupNames(ctx context.Context) (map[string]int64, error) {
	rows, err := q.q.QueryContext(ctx, `SELECT id, name FROM groups`)
	if err != nil {
		return nil, fmt.Errorf("list group names: %w", wrap(err))
	}
	defer rows.Close()
	names := map[string]int64{}
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, fmt.Errorf("read group name: %w", wrap(err))
		}
		names[name] = id
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read group names: %w", wrap(err))
	}
	return names, nil
}

func (q queries) CreateGroup(ctx context.Context, name string, createdAt time.Time) (int64, error) {
	result, err := q.q.ExecContext(ctx, `INSERT INTO groups(name, created_at) VALUES (?, ?)`, name, formatTime(createdAt))
	if err != nil {
		return 0, fmt.Errorf("create group: %w", wrap(err))
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read group id: %w", wrap(err))
	}
	return id, nil
}

func (q queries) ListGroups(ctx context.Context) ([]Group, error) {
	rows, err := q.q.QueryContext(ctx, `SELECT id, name, created_at FROM groups ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list groups: %w", wrap(err))
	}
	defer rows.Close()
	var groups []Group
	for rows.Next() {
		group, err := scanGroup(rows)
		if err != nil {
			return nil, fmt.Errorf("read group: %w", wrap(err))
		}
		groups = append(groups, group)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read groups: %w", wrap(err))
	}
	return groups, nil
}

func (q queries) GetGroup(ctx context.Context, id int64) (Group, error) {
	group, err := scanGroup(q.q.QueryRowContext(ctx, `SELECT id, name, created_at FROM groups WHERE id = ?`, id))
	if err != nil {
		return Group{}, fmt.Errorf("get group: %w", wrap(err))
	}
	return group, nil
}

func scanGroup(row interface{ Scan(...any) error }, extra ...any) (Group, error) {
	var group Group
	var createdAt string
	if err := row.Scan(append([]any{&group.ID, &group.Name, &createdAt}, extra...)...); err != nil {
		return Group{}, err
	}
	var err error
	if group.CreatedAt, err = parseTime(createdAt); err != nil {
		return Group{}, err
	}
	return group, nil
}

func (q queries) RenameGroup(ctx context.Context, id int64, name string) error {
	result, err := q.q.ExecContext(ctx, `UPDATE groups SET name = ? WHERE id = ?`, name, id)
	if err != nil {
		return fmt.Errorf("rename group: %w", wrap(err))
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rename group: %w", wrap(err))
	}
	if changed == 0 {
		return fmt.Errorf("rename group: %w", ErrNotFound)
	}
	return nil
}

func (q queries) ListGroupsWithMembers(ctx context.Context) ([]GroupWithMembers, error) {
	rows, err := q.q.QueryContext(ctx, `SELECT groups.id, groups.name, groups.created_at, skill_groups.skill_id
		FROM groups LEFT JOIN skill_groups ON skill_groups.group_id = groups.id
		ORDER BY groups.id, skill_groups.skill_id`)
	if err != nil {
		return nil, fmt.Errorf("list groups with members: %w", wrap(err))
	}
	defer rows.Close()
	var groups []GroupWithMembers
	for rows.Next() {
		var member sql.NullInt64
		group, err := scanGroup(rows, &member)
		if err != nil {
			return nil, fmt.Errorf("read group with members: %w", wrap(err))
		}
		if n := len(groups); n == 0 || groups[n-1].Group.ID != group.ID {
			groups = append(groups, GroupWithMembers{Group: group})
		}
		if member.Valid {
			last := &groups[len(groups)-1]
			last.Members = append(last.Members, member.Int64)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read groups with members: %w", wrap(err))
	}
	return groups, nil
}

func (q queries) IsMember(ctx context.Context, groupID, skillID int64) (bool, error) {
	var exists int
	err := q.q.QueryRowContext(ctx, `SELECT 1 FROM skill_groups WHERE skill_id = ? AND group_id = ?`, skillID, groupID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read group member: %w", wrap(err))
	}
	return true, nil
}

func (q queries) MemberCandidate(ctx context.Context, groupID, skillID int64) (string, bool, error) {
	var name string
	var duplicate bool
	if err := q.q.QueryRowContext(ctx, `SELECT name, EXISTS (
			SELECT 1 FROM skill_groups sg JOIN skills other ON other.id = sg.skill_id
			WHERE sg.group_id = ? AND other.name = skills.name)
		FROM skills WHERE id = ?`, groupID, skillID).Scan(&name, &duplicate); err != nil {
		return "", false, fmt.Errorf("add skill to group: %w", wrap(err))
	}
	return name, duplicate, nil
}

func (t *Tx) AddMember(ctx context.Context, groupID, skillID int64) error {
	if _, err := t.q.ExecContext(ctx, `INSERT INTO skill_groups(skill_id, group_id) VALUES (?, ?)`, skillID, groupID); err != nil {
		return fmt.Errorf("add skill to group: %w", wrap(err))
	}
	return nil
}

func (t *Tx) RemoveMember(ctx context.Context, groupID, skillID int64) error {
	if _, err := t.q.ExecContext(ctx, `DELETE FROM skill_groups WHERE group_id = ? AND skill_id = ?`, groupID, skillID); err != nil {
		return fmt.Errorf("remove skill from group: %w", wrap(err))
	}
	return nil
}

func (t *Tx) DeleteGroup(ctx context.Context, id int64) error {
	covered, err := t.MemberSkillScopes(ctx, id, 0, nil)
	if err != nil {
		return err
	}
	if _, err := t.q.ExecContext(ctx, `DELETE FROM group_installs WHERE group_id = ?`, id); err != nil {
		return fmt.Errorf("delete group installs: %w", wrap(err))
	}
	if _, err := t.q.ExecContext(ctx, `DELETE FROM skill_groups WHERE group_id = ?`, id); err != nil {
		return fmt.Errorf("delete group members: %w", wrap(err))
	}
	for _, member := range covered {
		if err := t.KeepAsDirectInstall(ctx, member.ID); err != nil {
			return err
		}
	}
	result, err := t.q.ExecContext(ctx, `DELETE FROM groups WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete group: %w", wrap(err))
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete group: %w", wrap(err))
	}
	if changed == 0 {
		return fmt.Errorf("delete group: %w", ErrNotFound)
	}
	return nil
}
