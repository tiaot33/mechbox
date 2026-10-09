package store

import (
	"context"
	"fmt"
)

type Agent struct {
	Code        string
	Name        string
	GlobalPath  string
	ProjectPath string
	Custom      bool
	DetectPath  string
}

type Project struct {
	ID   int64
	Path string
}

const agentColumns = `code, name, global_path, project_path, custom, detect_path`

func scanAgent(row interface{ Scan(...any) error }) (Agent, error) {
	var agent Agent
	err := row.Scan(&agent.Code, &agent.Name, &agent.GlobalPath, &agent.ProjectPath, &agent.Custom, &agent.DetectPath)
	return agent, err
}

func (q queries) ListAgents(ctx context.Context) ([]Agent, error) {
	rows, err := q.q.QueryContext(ctx, `SELECT `+agentColumns+` FROM agents ORDER BY code`)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", wrap(err))
	}
	defer rows.Close()
	var agents []Agent
	for rows.Next() {
		agent, err := scanAgent(rows)
		if err != nil {
			return nil, fmt.Errorf("read agent: %w", wrap(err))
		}
		agents = append(agents, agent)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read agents: %w", wrap(err))
	}
	return agents, nil
}

func (q queries) GetAgent(ctx context.Context, code string) (Agent, error) {
	agent, err := scanAgent(q.q.QueryRowContext(ctx, `SELECT `+agentColumns+` FROM agents WHERE code = ?`, code))
	if err != nil {
		return Agent{}, fmt.Errorf("get agent: %w", wrap(err))
	}
	return agent, nil
}

func (q queries) FolderSharers(ctx context.Context, scope Scope) ([]string, error) {
	rows, err := q.q.QueryContext(ctx, `SELECT a.code FROM agents a JOIN agents t ON t.code = ?
		WHERE CASE WHEN ? IS NULL THEN a.global_path = t.global_path ELSE a.project_path = t.project_path END
		ORDER BY a.code`, scope.Agent, scope.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("list folder sharers: %w", wrap(err))
	}
	defer rows.Close()
	var codes []string
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return nil, fmt.Errorf("read folder sharer: %w", wrap(err))
		}
		codes = append(codes, code)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read folder sharers: %w", wrap(err))
	}
	return codes, nil
}

func (q queries) AddAgent(ctx context.Context, agent Agent) error {
	if _, err := q.q.ExecContext(ctx, `INSERT INTO agents(code, name, global_path, project_path, custom) VALUES (?, ?, ?, ?, 1)`,
		agent.Code, agent.Name, agent.GlobalPath, agent.ProjectPath); err != nil {
		return fmt.Errorf("add agent: %w", wrap(err))
	}
	return nil
}

func (t *Tx) EditAgent(ctx context.Context, agent Agent) error {
	result, err := t.q.ExecContext(ctx, `UPDATE agents SET name = ?, global_path = ?, project_path = ? WHERE code = ? AND custom = 1`,
		agent.Name, agent.GlobalPath, agent.ProjectPath, agent.Code)
	if err != nil {
		return fmt.Errorf("edit agent: %w", wrap(err))
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("edit agent: %w", wrap(err))
	}
	if updated == 0 {
		return fmt.Errorf("edit agent: %w", ErrNotFound)
	}
	return nil
}

func (t *Tx) RemoveAgentInstalls(ctx context.Context, code string, global, project bool) error {
	const levels = `agent = ? AND ((? AND project_id IS NULL) OR (? AND project_id IS NOT NULL))`
	if _, err := t.q.ExecContext(ctx, `DELETE FROM skill_scopes WHERE `+levels, code, global, project); err != nil {
		return fmt.Errorf("remove agent skill scopes: %w", wrap(err))
	}
	if _, err := t.q.ExecContext(ctx, `DELETE FROM group_installs WHERE `+levels, code, global, project); err != nil {
		return fmt.Errorf("remove agent group installs: %w", wrap(err))
	}
	return nil
}

func (t *Tx) DeleteAgent(ctx context.Context, code string) error {
	if err := t.RemoveAgentInstalls(ctx, code, true, true); err != nil {
		return err
	}
	if _, err := t.q.ExecContext(ctx, `DELETE FROM agents WHERE code = ? AND custom = 1`, code); err != nil {
		return fmt.Errorf("delete agent: %w", wrap(err))
	}
	return nil
}

func (q queries) RegisterProject(ctx context.Context, path string) (Project, bool, error) {
	result, err := q.q.ExecContext(ctx, `INSERT OR IGNORE INTO projects(path) VALUES (?)`, path)
	if err != nil {
		return Project{}, false, fmt.Errorf("register project: %w", wrap(err))
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return Project{}, false, fmt.Errorf("register project: %w", wrap(err))
	}
	if inserted == 0 {
		project, err := q.ProjectByPath(ctx, path)
		return project, false, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Project{}, false, fmt.Errorf("read project id: %w", wrap(err))
	}
	return Project{ID: id, Path: path}, true, nil
}

func (q queries) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := q.q.QueryContext(ctx, `SELECT id, path FROM projects ORDER BY path, id`)
	if err != nil {
		return nil, fmt.Errorf("list registered projects: %w", wrap(err))
	}
	defer rows.Close()
	var projects []Project
	for rows.Next() {
		var project Project
		if err := rows.Scan(&project.ID, &project.Path); err != nil {
			return nil, fmt.Errorf("read registered project: %w", wrap(err))
		}
		projects = append(projects, project)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read registered projects: %w", wrap(err))
	}
	return projects, nil
}

func (q queries) GetProject(ctx context.Context, id int64) (Project, error) {
	project := Project{ID: id}
	if err := q.q.QueryRowContext(ctx, `SELECT path FROM projects WHERE id = ?`, id).Scan(&project.Path); err != nil {
		return Project{}, fmt.Errorf("get registered project: %w", wrap(err))
	}
	return project, nil
}

func (q queries) ProjectByPath(ctx context.Context, path string) (Project, error) {
	project := Project{Path: path}
	if err := q.q.QueryRowContext(ctx, `SELECT id FROM projects WHERE path = ?`, path).Scan(&project.ID); err != nil {
		return Project{}, fmt.Errorf("get registered project by path: %w", wrap(err))
	}
	return project, nil
}

func (t *Tx) RelocateProject(ctx context.Context, id int64, path string) error {
	result, err := t.q.ExecContext(ctx, `UPDATE projects SET path = ? WHERE id = ?`, path, id)
	if err != nil {
		return fmt.Errorf("relocate project: %w", wrap(err))
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("relocate project: %w", wrap(err))
	}
	if updated == 0 {
		return fmt.Errorf("relocate project: %w", ErrNotFound)
	}
	return nil
}

func (t *Tx) UnregisterProject(ctx context.Context, id int64) error {
	if _, err := t.q.ExecContext(ctx, `DELETE FROM skill_scopes WHERE project_id = ?`, id); err != nil {
		return fmt.Errorf("remove project skill scopes: %w", wrap(err))
	}
	if _, err := t.q.ExecContext(ctx, `DELETE FROM group_installs WHERE project_id = ?`, id); err != nil {
		return fmt.Errorf("remove project group installs: %w", wrap(err))
	}
	if _, err := t.q.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id); err != nil {
		return fmt.Errorf("unregister project: %w", wrap(err))
	}
	return nil
}
