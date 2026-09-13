package library

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

func (l *sqliteLibrary) ListAgents(ctx context.Context) (agents []Agent, err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	// 只读取稳定代码;布局规则留在表里,由解析与投影内部使用。
	rows, err := l.db.QueryContext(ctx, `SELECT code FROM agents ORDER BY code`)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var agent Agent
		if err := rows.Scan(&agent.Code); err != nil {
			return nil, fmt.Errorf("read agent: %w", err)
		}
		agents = append(agents, agent)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read agents: %w", err)
	}
	return agents, nil
}

func (l *sqliteLibrary) RegisterProject(ctx context.Context, path string) (project RegisteredProject, err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if path == "" {
		return RegisteredProject{}, fmt.Errorf("register project: path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return RegisteredProject{}, fmt.Errorf("resolve project path: %w", err)
	}
	project.Path, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return RegisteredProject{}, fmt.Errorf("resolve project path: %w", err)
	}
	info, err := os.Stat(project.Path)
	if err != nil {
		return RegisteredProject{}, fmt.Errorf("read project path: %w", err)
	}
	if !info.IsDir() {
		return RegisteredProject{}, fmt.Errorf("register project: %s is not a directory", project.Path)
	}
	project.Path = filepath.Clean(project.Path)
	result, err := l.db.ExecContext(ctx, `INSERT OR IGNORE INTO projects(path) VALUES (?)`, project.Path)
	if err != nil {
		return RegisteredProject{}, fmt.Errorf("register project: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return RegisteredProject{}, fmt.Errorf("register project: %w", err)
	}
	if inserted == 0 {
		return RegisteredProject{}, &Error{Code: AlreadyExists, Message: "register project: path already registered"}
	}
	id, err := result.LastInsertId()
	if err != nil {
		return RegisteredProject{}, fmt.Errorf("read project id: %w", err)
	}
	project.ID = ProjectID(id)
	return project, nil
}

func (l *sqliteLibrary) ListRegisteredProjects(ctx context.Context) (projects []RegisteredProject, err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	rows, err := l.db.QueryContext(ctx, `SELECT id, path FROM projects ORDER BY path, id`)
	if err != nil {
		return nil, fmt.Errorf("list registered projects: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var project RegisteredProject
		if err := rows.Scan(&project.ID, &project.Path); err != nil {
			return nil, fmt.Errorf("read registered project: %w", err)
		}
		projects = append(projects, project)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read registered projects: %w", err)
	}
	return projects, nil
}

func projectScopes(path string) []Scope {
	return []Scope{
		{Agent: AgentClaudeCode, Path: filepath.Join(path, ".claude", "skills")},
		{Agent: AgentCodex, Path: filepath.Join(path, ".codex", "skills")},
	}
}

func (l *sqliteLibrary) UnregisterProject(ctx context.Context, id ProjectID, opts UnregisterProjectOptions) (err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if opts.ProjectedCopies != "" && opts.ProjectedCopies != Keep && opts.ProjectedCopies != Remove {
		return fmt.Errorf("invalid projected copies option")
	}
	var path string
	if err := l.db.QueryRowContext(ctx, `SELECT path FROM projects WHERE id = ?`, id).Scan(&path); err != nil {
		return fmt.Errorf("get registered project: %w", err)
	}
	scopes := projectScopes(path)
	if opts.ProjectedCopies == Remove {
		activations, err := l.ListActivations(ctx)
		if err != nil {
			return err
		}
		for _, activation := range activations {
			for _, scope := range scopes {
				if activation.Scope == scope {
					if err := l.removeProjectedCopy(ctx, activation.SkillID, scope); err != nil {
						return err
					}
				}
			}
		}
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin unregister project: %w", err)
	}
	defer tx.Rollback()
	for _, scope := range scopes {
		if _, err := tx.ExecContext(ctx, `DELETE FROM activations WHERE agent = ? AND scope_path = ?`, scope.Agent, scope.Path); err != nil {
			return fmt.Errorf("remove project activations: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id); err != nil {
		return fmt.Errorf("unregister project: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit unregister project: %w", err)
	}
	return nil
}

func (l *sqliteLibrary) ListScopes(ctx context.Context) (scopes []Scope, err error) {
	ctx, unlock := l.readOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, typedError(fmt.Errorf("find home directory: %w", err))
	}
	scopes = []Scope{
		{Agent: AgentClaudeCode, Path: filepath.Join(home, ".claude", "skills")},
		{Agent: AgentCodex, Path: filepath.Join(home, ".codex", "skills")},
	}
	projects, err := l.ListRegisteredProjects(ctx)
	if err != nil {
		return nil, err
	}
	for _, project := range projects {
		scopes = append(scopes, projectScopes(project.Path)...)
	}
	return scopes, nil
}

func validateScope(scope Scope) error {
	if scope.Path == "" || (scope.Agent != AgentClaudeCode && scope.Agent != AgentCodex) {
		return fmt.Errorf("invalid scope")
	}
	return nil
}
