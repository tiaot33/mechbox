package library

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

func (l *sqliteLibrary) ListAgents(ctx context.Context) (agents []Agent, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	// 只读取稳定代码与展示名称;布局规则留在表里,由解析与投影内部使用。
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
	if err := validateProjectedCopies("unregister project", opts.ProjectedCopies); err != nil {
		return err
	}
	var path string
	if err := l.db.QueryRowContext(ctx, `SELECT path FROM projects WHERE id = ?`, id).Scan(&path); err != nil {
		return fmt.Errorf("get registered project: %w", err)
	}
	// 待删的投影路径在事务外先算好:单连接模式下事务内不能再走 l.db。
	var trash projectionTrash
	if opts.ProjectedCopies != Keep {
		projections, err := l.ListProjections(ctx)
		if err != nil {
			return err
		}
		for _, projection := range projections {
			if projection.Scope.Kind != ScopeProject || projection.Scope.ProjectID != id {
				continue
			}
			skill, err := l.GetSkill(ctx, projection.SkillID)
			if err != nil {
				return err
			}
			trash.targets = append(trash.targets, filepath.Join(projection.Directory, skill.Name))
		}
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin unregister project: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM activations WHERE project_id = ?`, id); err != nil {
		return fmt.Errorf("remove project activations: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id); err != nil {
		return fmt.Errorf("unregister project: %w", err)
	}
	return trash.commit(ctx, tx, "unregister project")
}

func (l *sqliteLibrary) Resolve(ctx context.Context, scope Scope) (directory string, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	row, err := scopeOf("resolve scope", scope)
	if err != nil {
		return "", err
	}
	// 两段规则都来自 agents 表:全局相对 $HOME,项目相对已注册项目根;不在代码里另写一套后缀。
	layout, err := l.agentLayout(ctx, row.agent)
	if err != nil {
		return "", err
	}
	if row.projectID == nil {
		return layout.globalDir(l.home), nil
	}
	var root string
	if err := l.db.QueryRowContext(ctx, `SELECT path FROM projects WHERE id = ?`, *row.projectID).Scan(&root); err != nil {
		return "", fmt.Errorf("get registered project: %w", err)
	}
	return layout.projectDir(root), nil
}

// agentLayout 是 agents 表里一个 Agent 的两段布局规则,均为斜杠分隔的相对路径。
type agentLayout struct {
	global  string // 相对用户主目录
	project string // 相对 Registered project 根
}

// globalDir 是该布局下全局 Scope 在 home 里的目录。
func (layout agentLayout) globalDir(home string) string {
	return filepath.Join(home, filepath.FromSlash(layout.global))
}

// projectDir 是该布局下项目 Scope 在项目根 root 里的目录。
func (layout agentLayout) projectDir(root string) string {
	return filepath.Join(root, filepath.FromSlash(layout.project))
}

// agentLayout 读取该 Agent 行的布局规则;代码不存在时错误经 typedError 变为 NotFound。
func (l *sqliteLibrary) agentLayout(ctx context.Context, code AgentCode) (agentLayout, error) {
	var layout agentLayout
	if err := l.db.QueryRowContext(ctx, `SELECT global_path, project_path FROM agents WHERE code = ?`, code).Scan(&layout.global, &layout.project); err != nil {
		return agentLayout{}, fmt.Errorf("get agent: %w", err)
	}
	return layout, nil
}
