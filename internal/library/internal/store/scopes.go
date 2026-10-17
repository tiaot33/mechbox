package store

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type Scope struct {
	Agent     string
	ProjectID *int64
}

type SkillScope struct {
	ID        int64
	SkillID   int64
	Name      string
	Scope     Scope
	Direct    bool
	CreatedAt time.Time
	Groups    []int64
}

type Claimant struct {
	SkillID int64
	Name    string
	Scope   Scope
}

type MemberScope struct {
	ID      int64
	SkillID int64
	Name    string
	Scope   Scope
}

type PlaceSkillScope struct {
	ID     int64
	Agent  string
	Direct bool
	Folder string
}

type GroupInstall struct {
	GroupID   int64
	Scope     Scope
	CreatedAt time.Time
}

const folderSQL = `CASE WHEN p.folder = '' THEN s.name ELSE p.folder END`

const groupCoverageSQL = `EXISTS (
	SELECT 1 FROM group_installs ga JOIN skill_groups sg ON sg.group_id = ga.group_id
	WHERE sg.skill_id = skill_scopes.skill_id AND ga.agent = skill_scopes.agent
		AND ga.project_id IS skill_scopes.project_id)`

func (q queries) PlaceSkillScopes(ctx context.Context, skillID int64, scope Scope) ([]PlaceSkillScope, error) {
	rows, err := q.q.QueryContext(ctx, `
		SELECT p.id, p.agent, p.direct, `+folderSQL+` FROM skill_scopes p
		JOIN skills s ON s.id = p.skill_id
		JOIN agents a ON a.code = p.agent JOIN agents t ON t.code = ?
		WHERE p.skill_id = ? AND p.project_id IS ?
			AND CASE WHEN p.project_id IS NULL THEN a.global_path = t.global_path ELSE a.project_path = t.project_path END
		ORDER BY p.agent <> t.code, p.agent`, scope.Agent, skillID, scope.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("list skill scopes sharing the folder: %w", wrap(err))
	}
	defer rows.Close()
	var place []PlaceSkillScope
	for rows.Next() {
		var row PlaceSkillScope
		if err := rows.Scan(&row.ID, &row.Agent, &row.Direct, &row.Folder); err != nil {
			return nil, fmt.Errorf("read skill scope sharing the folder: %w", wrap(err))
		}
		place = append(place, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read skill scopes sharing the folder: %w", wrap(err))
	}
	return place, nil
}

func (q queries) MarkDirect(ctx context.Context, skillScopeID int64) error {
	if _, err := q.q.ExecContext(ctx, `UPDATE skill_scopes SET direct = 1 WHERE id = ?`, skillScopeID); err != nil {
		return fmt.Errorf("record direct install: %w", wrap(err))
	}
	return nil
}

func (q queries) ListSkillScopes(ctx context.Context, skillID int64) ([]SkillScope, error) {
	rows, err := q.q.QueryContext(ctx, `SELECT p.id, p.skill_id, `+folderSQL+`, p.agent, p.project_id, p.direct, p.created_at
		FROM skill_scopes p JOIN skills s ON s.id = p.skill_id
		WHERE ? = 0 OR p.skill_id = ?
		ORDER BY p.skill_id, p.agent, p.project_id`,
		skillID, skillID)
	if err != nil {
		return nil, fmt.Errorf("list skill scopes: %w", wrap(err))
	}
	var skillScopes []SkillScope
	for rows.Next() {
		var skillScope SkillScope
		var createdAt string
		if err := rows.Scan(&skillScope.ID, &skillScope.SkillID, &skillScope.Name, &skillScope.Scope.Agent, &skillScope.Scope.ProjectID, &skillScope.Direct, &createdAt); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("read skill scope: %w", wrap(err))
		}
		if skillScope.CreatedAt, err = parseTime(createdAt); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("read skill scope: %w", err)
		}
		skillScopes = append(skillScopes, skillScope)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, fmt.Errorf("read skill scopes: %w", wrap(err))
	}
	for i := range skillScopes {
		if skillScopes[i].Groups, err = q.coveringGroups(ctx, skillScopes[i].SkillID, skillScopes[i].Scope); err != nil {
			return nil, err
		}
	}
	return skillScopes, nil
}

func (q queries) coveringGroups(ctx context.Context, skillID int64, scope Scope) ([]int64, error) {
	rows, err := q.q.QueryContext(ctx, `
		SELECT ga.group_id FROM group_installs ga JOIN skill_groups sg ON sg.group_id = ga.group_id
		WHERE sg.skill_id = ? AND ga.agent = ? AND ga.project_id IS ? ORDER BY ga.group_id`,
		skillID, scope.Agent, scope.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("list covering groups: %w", wrap(err))
	}
	defer rows.Close()
	var groups []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("read covering group: %w", wrap(err))
		}
		groups = append(groups, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read covering groups: %w", wrap(err))
	}
	return groups, nil
}

func (q queries) CoveringGroupName(ctx context.Context, skillID int64, scope Scope) (string, error) {
	var name string
	err := q.q.QueryRowContext(ctx, `
		SELECT groups.name FROM group_installs ga
		JOIN skill_groups sg ON sg.group_id = ga.group_id
		JOIN groups ON groups.id = ga.group_id
		WHERE sg.skill_id = ? AND ga.agent = ? AND ga.project_id IS ?
		ORDER BY groups.id LIMIT 1`, skillID, scope.Agent, scope.ProjectID).Scan(&name)
	if err != nil {
		return "", fmt.Errorf("read covering group: %w", wrap(err))
	}
	return name, nil
}

func (q queries) Claimants(ctx context.Context, name string, skillID int64) ([]Claimant, error) {
	rows, err := q.q.QueryContext(ctx, `
		SELECT p.skill_id, `+folderSQL+`, p.agent, p.project_id FROM skill_scopes p
		JOIN skills s ON s.id = p.skill_id
		WHERE `+folderSQL+` = ? ORDER BY p.skill_id <> ?`, name, skillID)
	if err != nil {
		return nil, fmt.Errorf("list claimants: %w", wrap(err))
	}
	defer rows.Close()
	var claimants []Claimant
	for rows.Next() {
		var claimant Claimant
		if err := rows.Scan(&claimant.SkillID, &claimant.Name, &claimant.Scope.Agent, &claimant.Scope.ProjectID); err != nil {
			return nil, fmt.Errorf("read claimant: %w", wrap(err))
		}
		claimants = append(claimants, claimant)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read claimants: %w", wrap(err))
	}
	return claimants, nil
}

func (q queries) MemberSkillScopes(ctx context.Context, groupID, skillID int64, filter *Scope) ([]MemberScope, error) {
	var agent string
	var projectID *int64
	if filter != nil {
		agent, projectID = filter.Agent, filter.ProjectID
	}
	rows, err := q.q.QueryContext(ctx, `
		SELECT p.id, p.skill_id, `+folderSQL+`, p.agent, p.project_id FROM skill_scopes p
		JOIN skills s ON s.id = p.skill_id
		JOIN skill_groups sg ON sg.skill_id = p.skill_id AND sg.group_id = ?
		JOIN group_installs ga ON ga.group_id = sg.group_id AND ga.agent = p.agent AND ga.project_id IS p.project_id
		WHERE (? = 0 OR p.skill_id = ?) AND (? = '' OR (p.agent = ? AND p.project_id IS ?))
		ORDER BY p.skill_id, p.agent, p.project_id`,
		groupID, skillID, skillID, agent, agent, projectID)
	if err != nil {
		return nil, fmt.Errorf("list member skill scopes: %w", wrap(err))
	}
	defer rows.Close()
	var members []MemberScope
	for rows.Next() {
		var member MemberScope
		if err := rows.Scan(&member.ID, &member.SkillID, &member.Name, &member.Scope.Agent, &member.Scope.ProjectID); err != nil {
			return nil, fmt.Errorf("read member skill scope: %w", wrap(err))
		}
		members = append(members, member)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read member skill scopes: %w", wrap(err))
	}
	return members, nil
}

func (q queries) ListGroupInstalls(ctx context.Context, groupID int64) ([]GroupInstall, error) {
	rows, err := q.q.QueryContext(ctx, `SELECT group_id, agent, project_id, created_at FROM group_installs
		WHERE ? = 0 OR group_id = ? ORDER BY group_id, agent, project_id`, groupID, groupID)
	if err != nil {
		return nil, fmt.Errorf("list group installs: %w", wrap(err))
	}
	defer rows.Close()
	var installs []GroupInstall
	for rows.Next() {
		var install GroupInstall
		var createdAt string
		if err := rows.Scan(&install.GroupID, &install.Scope.Agent, &install.Scope.ProjectID, &createdAt); err != nil {
			return nil, fmt.Errorf("read group install: %w", wrap(err))
		}
		if install.CreatedAt, err = parseTime(createdAt); err != nil {
			return nil, fmt.Errorf("read group install: %w", err)
		}
		installs = append(installs, install)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read group install: %w", wrap(err))
	}
	return installs, nil
}

func (q queries) FindGroupInstall(ctx context.Context, groupID int64, scope Scope) (int64, error) {
	var id int64
	if err := q.q.QueryRowContext(ctx, `SELECT id FROM group_installs WHERE group_id = ? AND agent = ? AND project_id IS ?`,
		groupID, scope.Agent, scope.ProjectID).Scan(&id); err != nil {
		return 0, fmt.Errorf("find group install: %w", wrap(err))
	}
	return id, nil
}

func (q queries) SkillHead(ctx context.Context, skillID int64) (string, int64, error) {
	var name string
	var currentVersionID int64
	if err := q.q.QueryRowContext(ctx, `SELECT name, current_version_id FROM skills WHERE id = ?`, skillID).Scan(&name, &currentVersionID); err != nil {
		return "", 0, fmt.Errorf("inspect skill scope: %w", wrap(err))
	}
	return name, currentVersionID, nil
}

func (q queries) OlderVersionIDs(ctx context.Context, skillID, currentVersionID int64) ([]int64, error) {
	rows, err := q.q.QueryContext(ctx, `SELECT id FROM skill_versions WHERE skill_id = ? AND id != ? ORDER BY id DESC`, skillID, currentVersionID)
	if err != nil {
		return nil, fmt.Errorf("list skill versions: %w", wrap(err))
	}
	defer rows.Close()
	var versionIDs []int64
	for rows.Next() {
		var versionID int64
		if err := rows.Scan(&versionID); err != nil {
			return nil, fmt.Errorf("read skill version: %w", wrap(err))
		}
		versionIDs = append(versionIDs, versionID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read skill versions: %w", wrap(err))
	}
	return versionIDs, nil
}

const sharedFolderSQL = `coalesce((
	SELECT p.folder FROM skill_scopes p JOIN agents a ON a.code = p.agent JOIN agents t ON t.code = ?
	WHERE p.skill_id = ? AND p.project_id IS ?
		AND CASE WHEN p.project_id IS NULL THEN a.global_path = t.global_path ELSE a.project_path = t.project_path END
	LIMIT 1), '')`

func (t *Tx) RecordSkillScope(ctx context.Context, skillID int64, scope Scope, direct bool, createdAt time.Time) error {
	if _, err := t.q.ExecContext(ctx, `INSERT INTO skill_scopes(skill_id, agent, project_id, direct, created_at, folder) VALUES (?, ?, ?, ?, ?, `+sharedFolderSQL+`)`,
		skillID, scope.Agent, scope.ProjectID, direct, formatTime(createdAt), scope.Agent, skillID, scope.ProjectID); err != nil {
		return fmt.Errorf("record skill scope: %w", wrap(err))
	}
	return nil
}

func (t *Tx) RecordFetchedSkillScope(ctx context.Context, skillID int64, scope Scope, folder string, createdAt time.Time) error {
	if _, err := t.q.ExecContext(ctx, `INSERT INTO skill_scopes(skill_id, agent, project_id, direct, created_at, folder)
		VALUES (?, ?, ?, 1, ?, CASE WHEN ? = (SELECT name FROM skills WHERE id = ?) THEN '' ELSE ? END)`,
		skillID, scope.Agent, scope.ProjectID, formatTime(createdAt), folder, skillID, folder); err != nil {
		return fmt.Errorf("record skill scope: %w", wrap(err))
	}
	return nil
}

func (t *Tx) RecordGroupInstall(ctx context.Context, groupID int64, scope Scope, createdAt time.Time) error {
	if _, err := t.q.ExecContext(ctx, `INSERT INTO group_installs(group_id, agent, project_id, created_at) VALUES (?, ?, ?, ?) ON CONFLICT DO NOTHING`,
		groupID, scope.Agent, scope.ProjectID, formatTime(createdAt)); err != nil {
		return fmt.Errorf("record group install: %w", wrap(err))
	}
	return nil
}

func (t *Tx) Uninstall(ctx context.Context, skillScopeID int64) (bool, error) {
	if _, err := t.q.ExecContext(ctx, `UPDATE skill_scopes SET direct = 0 WHERE id = ?`, skillScopeID); err != nil {
		return false, fmt.Errorf("remove direct install: %w", wrap(err))
	}
	return t.UninstallMember(ctx, skillScopeID)
}

func (t *Tx) UninstallMember(ctx context.Context, skillScopeID int64) (bool, error) {
	result, err := t.q.ExecContext(ctx, `DELETE FROM skill_scopes WHERE id = ? AND direct = 0 AND NOT `+groupCoverageSQL, skillScopeID)
	if err != nil {
		return false, fmt.Errorf("remove skill scope: %w", wrap(err))
	}
	removed, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("remove skill scope: %w", wrap(err))
	}
	return removed > 0, nil
}

func (t *Tx) KeepAsDirectInstall(ctx context.Context, skillScopeID int64) error {
	if _, err := t.q.ExecContext(ctx, `UPDATE skill_scopes SET direct = 1 WHERE id = ? AND direct = 0 AND NOT `+groupCoverageSQL, skillScopeID); err != nil {
		return fmt.Errorf("keep skill scope as direct install: %w", wrap(err))
	}
	return nil
}

func (t *Tx) RemoveGroupInstall(ctx context.Context, groupInstallID int64) error {
	if _, err := t.q.ExecContext(ctx, `DELETE FROM group_installs WHERE id = ?`, groupInstallID); err != nil {
		return fmt.Errorf("remove group install: %w", wrap(err))
	}
	return nil
}

func (t *Tx) RemoveSkillScope(ctx context.Context, skillScopeID int64) error {
	if _, err := t.q.ExecContext(ctx, `DELETE FROM skill_scopes WHERE id = ?`, skillScopeID); err != nil {
		return fmt.Errorf("remove skill scope: %w", wrap(err))
	}
	return nil
}

func (t *Tx) HandOverSkillScope(ctx context.Context, skillScopeID int64, agent string) error {
	var skillID int64
	var projectID *int64
	var direct bool
	var createdAt, folder string
	if err := t.q.QueryRowContext(ctx, `SELECT skill_id, project_id, direct, created_at, folder FROM skill_scopes WHERE id = ?`, skillScopeID).
		Scan(&skillID, &projectID, &direct, &createdAt, &folder); err != nil {
		return fmt.Errorf("read skill scope to hand over: %w", wrap(err))
	}
	result, err := t.q.ExecContext(ctx, `UPDATE skill_scopes SET direct = max(direct, ?) WHERE skill_id = ? AND agent = ? AND project_id IS ?`,
		direct, skillID, agent, projectID)
	if err != nil {
		return fmt.Errorf("hand over skill scope: %w", wrap(err))
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("hand over skill scope: %w", wrap(err))
	}
	if updated == 0 {
		if _, err := t.q.ExecContext(ctx, `INSERT INTO skill_scopes(skill_id, agent, project_id, direct, created_at, folder) VALUES (?, ?, ?, ?, ?, ?)`,
			skillID, agent, projectID, direct, createdAt, folder); err != nil {
			return fmt.Errorf("hand over skill scope: %w", wrap(err))
		}
	}
	if _, err := t.q.ExecContext(ctx, `UPDATE skill_scopes SET direct = 1 WHERE skill_id = ? AND agent = ? AND project_id IS ? AND direct = 0 AND NOT `+groupCoverageSQL,
		skillID, agent, projectID); err != nil {
		return fmt.Errorf("hand over skill scope: %w", wrap(err))
	}
	return nil
}

func (t *Tx) ClaimSkillScope(ctx context.Context, skillID int64, scope Scope, createdAt time.Time) error {
	if err := t.RecordSkillScope(ctx, skillID, scope, false, createdAt); err != nil {
		return err
	}
	if _, err := t.q.ExecContext(ctx, `UPDATE skill_scopes SET direct = 1 WHERE skill_id = ? AND agent = ? AND project_id IS ? AND direct = 0 AND NOT `+groupCoverageSQL,
		skillID, scope.Agent, scope.ProjectID); err != nil {
		return fmt.Errorf("claim skill scope: %w", wrap(err))
	}
	return nil
}

func (t *Tx) ResetFolders(ctx context.Context, skillScopeIDs []int64) error {
	for _, id := range skillScopeIDs {
		if _, err := t.q.ExecContext(ctx, `UPDATE skill_scopes SET folder = '' WHERE id = ?`, id); err != nil {
			return fmt.Errorf("record skill folder: %w", wrap(err))
		}
	}
	return nil
}

func (t *Tx) RenameSkill(ctx context.Context, skillID int64, name string) error {
	if _, err := t.q.ExecContext(ctx, `UPDATE skill_scopes SET folder = (SELECT name FROM skills WHERE id = ?) WHERE skill_id = ? AND folder = ''`, skillID, skillID); err != nil {
		return fmt.Errorf("keep installed folder names: %w", wrap(err))
	}
	if _, err := t.q.ExecContext(ctx, `UPDATE skill_scopes SET folder = '' WHERE skill_id = ? AND folder = ?`, skillID, name); err != nil {
		return fmt.Errorf("keep installed folder names: %w", wrap(err))
	}
	if _, err := t.q.ExecContext(ctx, `UPDATE skills SET name = ? WHERE id = ?`, name, skillID); err != nil {
		return fmt.Errorf("rename skill: %w", wrap(err))
	}
	return nil
}
