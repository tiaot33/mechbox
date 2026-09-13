package library

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

func (l *sqliteLibrary) CreateGroup(ctx context.Context, name string) (group Group, err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if strings.TrimSpace(name) == "" {
		return Group{}, fmt.Errorf("create group: name is required")
	}
	result, err := l.db.ExecContext(ctx, `INSERT INTO groups(name) VALUES (?)`, name)
	if err != nil {
		return Group{}, fmt.Errorf("create group: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Group{}, fmt.Errorf("read group id: %w", err)
	}
	return Group{ID: GroupID(id), Name: name}, nil
}

func (l *sqliteLibrary) ListGroups(ctx context.Context) (groups []Group, err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	rows, err := l.db.QueryContext(ctx, `SELECT id, name FROM groups ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list groups: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var group Group
		if err := rows.Scan(&group.ID, &group.Name); err != nil {
			return nil, fmt.Errorf("read group: %w", err)
		}
		groups = append(groups, group)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read groups: %w", err)
	}
	return groups, nil
}

func (l *sqliteLibrary) RenameGroup(ctx context.Context, id GroupID, name string) (err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("rename group: name is required")
	}
	result, err := l.db.ExecContext(ctx, `UPDATE groups SET name = ? WHERE id = ?`, name, id)
	if err != nil {
		return fmt.Errorf("rename group: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rename group: %w", err)
	}
	if changed == 0 {
		return fmt.Errorf("rename group: %w", sql.ErrNoRows)
	}
	return nil
}

func (l *sqliteLibrary) DeleteGroup(ctx context.Context, id GroupID) (err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete group: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE activations SET individual = 1
		WHERE individual = 0
			AND EXISTS (
				SELECT 1 FROM activation_groups
				WHERE activation_groups.skill_id = activations.skill_id
					AND activation_groups.agent = activations.agent
					AND activation_groups.scope_path = activations.scope_path
					AND activation_groups.group_id = ?
			)
			AND NOT EXISTS (
				SELECT 1 FROM activation_groups
				WHERE activation_groups.skill_id = activations.skill_id
					AND activation_groups.agent = activations.agent
					AND activation_groups.scope_path = activations.scope_path
					AND activation_groups.group_id <> ?
			)`, id, id); err != nil {
		return fmt.Errorf("preserve group activations: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM activation_groups WHERE group_id = ?`, id); err != nil {
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

func (l *sqliteLibrary) AddSkillToGroup(ctx context.Context, groupID GroupID, skillID SkillID) (err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if err := l.requireGroup(ctx, groupID); err != nil {
		return err
	}
	if err := l.requireSkill(ctx, skillID); err != nil {
		return err
	}
	if _, err := l.db.ExecContext(ctx, `INSERT INTO skill_groups(skill_id, group_id) VALUES (?, ?)`, skillID, groupID); err != nil {
		return fmt.Errorf("add skill to group: %w", err)
	}
	return nil
}

func (l *sqliteLibrary) RemoveSkillFromGroup(ctx context.Context, groupID GroupID, skillID SkillID) (err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	result, err := l.db.ExecContext(ctx, `DELETE FROM skill_groups WHERE group_id = ? AND skill_id = ?`, groupID, skillID)
	if err != nil {
		return fmt.Errorf("remove skill from group: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("remove skill from group: %w", err)
	}
	if changed == 0 {
		return fmt.Errorf("remove skill from group: %w", sql.ErrNoRows)
	}
	return nil
}

func (l *sqliteLibrary) ListGroupSkills(ctx context.Context, groupID GroupID) (skills []SkillSummary, err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if err := l.requireGroup(ctx, groupID); err != nil {
		return nil, err
	}
	rows, err := l.db.QueryContext(ctx, `SELECT skill_id FROM skill_groups WHERE group_id = ? ORDER BY skill_id`, groupID)
	if err != nil {
		return nil, fmt.Errorf("list group skills: %w", err)
	}
	var ids []SkillID
	for rows.Next() {
		var id SkillID
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("read group skill: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close group skills: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read group skills: %w", err)
	}

	// ponytail: one lookup per member; replace with a joined mapper if large groups make this measurable.
	skills = make([]SkillSummary, 0, len(ids))
	for _, id := range ids {
		summary, err := l.getSummary(ctx, id)
		if err != nil {
			return nil, err
		}
		skills = append(skills, summary)
	}
	return skills, nil
}

func (l *sqliteLibrary) requireGroup(ctx context.Context, id GroupID) error {
	var exists int
	if err := l.db.QueryRowContext(ctx, `SELECT 1 FROM groups WHERE id = ?`, id).Scan(&exists); err != nil {
		return fmt.Errorf("get group: %w", err)
	}
	return nil
}
