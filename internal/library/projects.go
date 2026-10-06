package library

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tiaot33/skills-tui/internal/library/internal/agentdir"
	"github.com/tiaot33/skills-tui/internal/library/internal/store"
)

// ProjectID 是已登记项目的数据库主键。
type ProjectID int64

// AgentCode 是 Agent 的稳定代码:跨库、跨 Sync 仍指同一个 Agent。
type AgentCode string

const (
	AgentClaudeCode AgentCode = "claude-code"
	AgentCodex      AgentCode = "codex"
)

// Agent 是 Library 持有的一个安装目标 agent。暴露稳定代码与展示名称;Agent 目录
// 不出现在这里,由 Library 算出,调用方不自行拼路径。
type Agent struct {
	Code AgentCode `json:"code"`
	Name string    `json:"name"`
}

// RegisteredProject 是已注册的项目目录;注册后它与每个 Agent 组成一个 ProjectScope,
// 各 Agent 目录在项目根下算出。
type RegisteredProject struct {
	ID   ProjectID `json:"id"`
	Path string    `json:"path"`
}

// ScopeKind 是 Scope 的层级:某 Agent 的全局,或某 Agent 在某 Registered project 下。
type ScopeKind string

const (
	ScopeGlobal  ScopeKind = "global"  // 目录相对用户主目录
	ScopeProject ScopeKind = "project" // 目录相对 Registered project 根
)

// Scope 是技能安装所在的层级身份,不含路径(见 ADR 0005)。该 Agent 在此作用域的目录
// 由 Library 算出(见 Resolve),调用方不自行拼路径,也无法传入一条目录来 Install。
// 用 GlobalScope / ProjectScope 构造;JSON 形状 {"kind","agent","project_id"?} 双向一致,
// ListSkillScopes 读出的 Scope 可原样传回。每个接收 Scope 的方法在入口校验组合:
// 未知 Kind、全局带 ProjectID、项目缺 ProjectID 均为 InvalidArgument,Details.Field 指向对应字段。
type Scope struct {
	Kind      ScopeKind `json:"kind"`
	Agent     AgentCode `json:"agent"`
	ProjectID ProjectID `json:"project_id,omitzero"` // 仅 ScopeProject;全局为零值
}

// GlobalScope 是某个 Agent 的全局 Scope。
func GlobalScope(agent AgentCode) Scope {
	return Scope{Kind: ScopeGlobal, Agent: agent}
}

// ProjectScope 是某个 Agent 在某个 Registered project 下的 Scope。
func ProjectScope(agent AgentCode, projectID ProjectID) Scope {
	return Scope{Kind: ScopeProject, Agent: agent, ProjectID: projectID}
}

// UnregisterProjectOptions 是 UnregisterProject 的选项。
type UnregisterProjectOptions struct {
	Folders FolderPolicy `json:"folders"` // 默认删除技能文件夹,只有显式 Keep 保留
}

type scopeRow struct {
	agent     AgentCode
	projectID *ProjectID
}

func scopeOf(operation string, scope Scope) (scopeRow, error) {
	switch scope.Kind {
	case ScopeGlobal:
		if scope.ProjectID != 0 {
			return scopeRow{}, argumentError(operation, "project_id", "global scope must not have a project")
		}
		return scopeRow{agent: scope.Agent}, nil
	case ScopeProject:
		if scope.ProjectID <= 0 {
			return scopeRow{}, argumentError(operation, "project_id", "project scope requires a project")
		}
		return scopeRow{agent: scope.Agent, projectID: new(scope.ProjectID)}, nil
	default:
		return scopeRow{}, argumentError(operation, "kind", "unknown scope kind")
	}
}

func (r scopeRow) scope() Scope {
	if r.projectID == nil {
		return GlobalScope(r.agent)
	}
	return ProjectScope(r.agent, *r.projectID)
}

type resolvedScope struct {
	scopeRow
	root      string
	directory string
}

func (s resolvedScope) rootMissing(operation string) (*Error, error) {
	exists, err := agentdir.DirectoryExists(s.root)
	if err != nil {
		return nil, fmt.Errorf("inspect scope root: %w", err)
	}
	if exists {
		return nil, nil
	}
	details := ErrorDetails{Path: s.root}
	if s.projectID != nil {
		details.ProjectID = *s.projectID
	}
	return preconditionError(operation, "scope root does not exist", details), nil
}

func (s resolvedScope) requireRoot(operation string) error {
	missing, err := s.rootMissing(operation)
	if err != nil {
		return err
	}
	if missing != nil {
		return missing
	}
	return nil
}

func (l *Library) resolveScope(ctx context.Context, operation string, scope Scope) (resolvedScope, error) {
	row, err := scopeOf(operation, scope)
	if err != nil {
		return resolvedScope{}, err
	}
	root, directory, err := l.scopeDirectory(ctx, row)
	if err != nil {
		return resolvedScope{}, err
	}
	return resolvedScope{scopeRow: row, root: root, directory: directory}, nil
}

func (r scopeRow) record() store.Scope {
	scope := store.Scope{Agent: string(r.agent)}
	if r.projectID != nil {
		scope.ProjectID = new(int64(*r.projectID))
	}
	return scope
}

func scopeRowOf(scope store.Scope) scopeRow {
	row := scopeRow{agent: AgentCode(scope.Agent)}
	if scope.ProjectID != nil {
		row.projectID = new(ProjectID(*scope.ProjectID))
	}
	return row
}

// ListAgents 列出 Library 持有的全部 Agent(按代码排序)。只读;结果含稳定代码与展示名称,
// 不含 Agent 目录。Agent 由建库种子写入,技能库没有增删改 Agent 的方法。
func (l *Library) ListAgents(ctx context.Context) (agents []Agent, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	rows, err := l.store.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		agents = append(agents, Agent{Code: AgentCode(row.Code), Name: row.Name})
	}
	return agents, nil
}

// RegisterProject 注册一个项目目录,使它与各 Agent 组成可安装的 ProjectScope。
func (l *Library) RegisterProject(ctx context.Context, path string) (project RegisteredProject, err error) {
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
	row, created, err := l.store.RegisterProject(ctx, project.Path)
	if err != nil {
		return RegisteredProject{}, err
	}
	if !created {
		return RegisteredProject{}, existsError("register project", "path already registered", ErrorDetails{ProjectID: ProjectID(row.ID), Path: project.Path})
	}
	project.ID = ProjectID(row.ID)
	return project, nil
}

// ListRegisteredProjects 按路径列出全部已登记项目。
func (l *Library) ListRegisteredProjects(ctx context.Context) (projects []RegisteredProject, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	rows, err := l.store.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		projects = append(projects, RegisteredProject{ID: ProjectID(row.ID), Path: row.Path})
	}
	return projects, nil
}

// UnregisterProject 注销项目并清掉其各 Agent 项目 Scope 下的全部安装记录与组安装;
// opts 决定磁盘上技能文件夹的去留。
func (l *Library) UnregisterProject(ctx context.Context, id ProjectID, opts UnregisterProjectOptions) (err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	if err := validateFolderPolicy("unregister project", opts.Folders); err != nil {
		return err
	}
	if _, err := l.store.GetProject(ctx, int64(id)); err != nil {
		return err
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
	return l.applyChange(ctx, &change, func(tx *store.Tx) error {
		return tx.UnregisterProject(ctx, int64(id))
	})
}

// Resolve 按 agents 表算出 Scope 对应的 Agent 目录:全局相对 Open 时的用户
// 主目录,项目相对该 Registered project 根。只读:不写 Library、不创建目录、不产生安装。
// 未知 Agent 代码或未登记项目返回 NotFound。可选目标没有单独的列举方法:调用方用
// ListAgents ×(全局 | ListRegisteredProjects)自行组合。
func (l *Library) Resolve(ctx context.Context, scope Scope) (directory string, err error) {
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

func (l *Library) scopeDirectory(ctx context.Context, row scopeRow) (root, directory string, err error) {
	agent, err := l.store.GetAgent(ctx, string(row.agent))
	if err != nil {
		return "", "", err
	}
	if row.projectID == nil {
		return l.home, filepath.Join(l.home, filepath.FromSlash(agent.GlobalPath)), nil
	}
	project, err := l.store.GetProject(ctx, int64(*row.projectID))
	if err != nil {
		return "", "", err
	}
	return project.Path, filepath.Join(project.Path, filepath.FromSlash(agent.ProjectPath)), nil
}
