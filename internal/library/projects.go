package library

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tiaot33/skills-tui/internal/library/internal/agentdir"
)

func (l *sqliteLibrary) ListAgents(ctx context.Context) (agents []Agent, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	rows, err := l.db.QueryContext(ctx, `SELECT code, name FROM agents ORDER BY code`)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var agent Agent
		if err := rows.Scan(&agent.Code, &agent.Name); err != nil {
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
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if path == "" {
		return RegisteredProject{}, argumentError("register project", "path", "path is required")
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
		return RegisteredProject{}, argumentError("register project", "path", "path is not a directory")
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
		var id ProjectID
		if err := l.db.QueryRowContext(ctx, `SELECT id FROM projects WHERE path = ?`, project.Path).Scan(&id); err != nil {
			return RegisteredProject{}, fmt.Errorf("read registered project: %w", err)
		}
		return RegisteredProject{}, existsError("register project", "path already registered", ErrorDetails{ProjectID: id, Path: project.Path})
	}
	id, err := result.LastInsertId()
	if err != nil {
		return RegisteredProject{}, fmt.Errorf("read project id: %w", err)
	}
	project.ID = ProjectID(id)
	return project, nil
}

func (l *sqliteLibrary) ListRegisteredProjects(ctx context.Context) (projects []RegisteredProject, err error) {
	ctx, unlock := l.sharedOperation(ctx)
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

func (l *sqliteLibrary) UnregisterProject(ctx context.Context, id ProjectID, opts UnregisterProjectOptions) (err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if err := validateFolderPolicy("unregister project", opts.Folders); err != nil {
		return err
	}
	var path string
	if err := l.db.QueryRowContext(ctx, `SELECT path FROM projects WHERE id = ?`, id).Scan(&path); err != nil {
		return fmt.Errorf("get registered project: %w", err)
	}
	var change agentdir.Change
	defer func() { err = errors.Join(err, change.Rollback()) }()
	if opts.Folders != Keep {
		skillScopes, err := l.ListSkillScopes(ctx)
		if err != nil {
			return err
		}
		for _, skillScope := range skillScopes {
			if skillScope.Scope.Kind != ScopeProject || skillScope.Scope.ProjectID != id {
				continue
			}
			skill, err := l.GetSkill(ctx, skillScope.SkillID)
			if err != nil {
				return err
			}
			change.Remove(skillScope.Directory, skill.Name)
		}
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin unregister project: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM skill_scopes WHERE project_id = ?`, id); err != nil {
		return fmt.Errorf("remove project skill scopes: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM group_installs WHERE project_id = ?`, id); err != nil {
		return fmt.Errorf("remove project group installs: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id); err != nil {
		return fmt.Errorf("unregister project: %w", err)
	}
	return commitChange(ctx, tx, &change, "unregister project")
}

func (l *sqliteLibrary) Resolve(ctx context.Context, scope Scope) (directory string, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	row, err := scopeOf("resolve scope", scope)
	if err != nil {
		return "", err
	}
	_, directory, err = l.scopeDirectory(ctx, row)
	return directory, err
}

func (l *sqliteLibrary) scopeDirectory(ctx context.Context, row scopeRow) (root, directory string, err error) {
	dirs, err := l.agentDirectories(ctx, row.agent)
	if err != nil {
		return "", "", err
	}
	if row.projectID == nil {
		return l.home, dirs.globalDir(l.home), nil
	}
	if err := l.db.QueryRowContext(ctx, `SELECT path FROM projects WHERE id = ?`, *row.projectID).Scan(&root); err != nil {
		return "", "", fmt.Errorf("get registered project: %w", err)
	}
	return root, dirs.projectDir(root), nil
}

type agentDirectories struct {
	global  string
	project string
}

func (dirs agentDirectories) globalDir(home string) string {
	return filepath.Join(home, filepath.FromSlash(dirs.global))
}

func (dirs agentDirectories) projectDir(root string) string {
	return filepath.Join(root, filepath.FromSlash(dirs.project))
}

func (l *sqliteLibrary) agentDirectories(ctx context.Context, code AgentCode) (agentDirectories, error) {
	var dirs agentDirectories
	if err := l.db.QueryRowContext(ctx, `SELECT global_path, project_path FROM agents WHERE code = ?`, code).Scan(&dirs.global, &dirs.project); err != nil {
		return agentDirectories{}, fmt.Errorf("get agent: %w", err)
	}
	return dirs, nil
}
