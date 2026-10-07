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
}

type Project struct {
	ID   int64
	Path string
}

func (q queries) ListAgents(ctx context.Context) ([]Agent, error) {
	rows, err := q.q.QueryContext(ctx, `SELECT code, name, global_path, project_path FROM agents ORDER BY code`)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", wrap(err))
	}
	defer rows.Close()
	var agents []Agent
	for rows.Next() {
		var agent Agent
		if err := rows.Scan(&agent.Code, &agent.Name, &agent.GlobalPath, &agent.ProjectPath); err != nil {
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
	agent := Agent{Code: code}
	if err := q.q.QueryRowContext(ctx, `SELECT name, global_path, project_path FROM agents WHERE code = ?`, code).Scan(&agent.Name, &agent.GlobalPath, &agent.ProjectPath); err != nil {
		return Agent{}, fmt.Errorf("get agent: %w", wrap(err))
	}
	return agent, nil
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
		project := Project{Path: path}
		if err := q.q.QueryRowContext(ctx, `SELECT id FROM projects WHERE path = ?`, path).Scan(&project.ID); err != nil {
			return Project{}, false, fmt.Errorf("read registered project: %w", wrap(err))
		}
		return project, false, nil
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
