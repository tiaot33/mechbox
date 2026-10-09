package library

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"syscall"

	"github.com/tiaot33/skills-tui/internal/library/internal/agentdir"
	"github.com/tiaot33/skills-tui/internal/library/internal/store"
)

// ProjectID 是已登记项目的身份:登记时分配,注销前不变;同步到另一台机器、或改了项目位置后仍指同一个项目。
type ProjectID int64

// AgentCode 是 Agent 的稳定代码:跨应用版本、跨同步都不变。界面上区分 Agent(例如按代码取色)应该用它,不用名称。
type AgentCode string

// v1 内置的两个 Agent。调用方不应假定只有这两个,可选的 Agent 以 [Library.ListAgents] 为准。
const (
	AgentClaudeCode AgentCode = "claude-code"
	AgentCodex      AgentCode = "codex"
)

// Agent 是应用支持的一个 Agent:稳定代码和展示名称。名称可能随应用版本变化,身份以代码为准。
// 它在各作用域读取技能的目录不在这里,用 [Library.ScopeDirectory] 查。
type Agent struct {
	Code AgentCode `json:"code"`
	Name string    `json:"name"`
}

// RegisteredProject 是一个已登记项目:登记后它与每个 Agent 组成一个项目作用域。
// 界面上「Codex · ai-workflow-engine」这样的显示用 Agent.Name 和 Name 拼出。
type RegisteredProject struct {
	ID ProjectID `json:"id"`
	// Name 是项目目录的名字,即 Path 的最后一段。两个项目同名时由界面用 Path 区分,库不改名、不加后缀。
	Name string `json:"name"`
	// Path 是登记或改项目位置时规范化后的绝对路径(已解析符号链接)。
	Path string `json:"path"`
	// Missing 表示项目目录在这台机器上不存在:从别的机器同步过来、被移动过,或在未挂载的磁盘上。
	// 列出时现查;界面据此提示「不在这台机器上」并提供改项目位置([Library.RelocateProject])。
	Missing bool `json:"missing,omitzero"`
}

// ScopeKind 是作用域的种类:某个 Agent 的全局,或某个 Agent 加一个已登记项目。
type ScopeKind string

const (
	ScopeGlobal  ScopeKind = "global"  // 全局:本机当前用户的所有项目都能用
	ScopeProject ScopeKind = "project" // 项目:只在这个已登记项目里能用
)

// Scope 是技能安装所在的作用域,只是身份,不含路径(见 ADR 0005):调用方不能指定目录,
// 该 Agent 在此作用域读取技能的目录用 [Library.ScopeDirectory] 查。展示时按 Agent 代码和 ProjectID
// 从 [Library.ListAgents]、[Library.ListRegisteredProjects] 的结果里查出名字。
// 用 GlobalScope / ProjectScope 构造;JSON 形状 {"kind","agent","project_id"?} 双向一致,
// 读出的 Scope 可原样传回。每个接收 Scope 的方法在入口校验组合:
// 未知 Kind、全局带 ProjectID、项目缺 ProjectID 均为 InvalidArgument,Details.Field 指向对应字段。
type Scope struct {
	Kind      ScopeKind `json:"kind"`
	Agent     AgentCode `json:"agent"`
	ProjectID ProjectID `json:"project_id,omitzero"` // 仅 ScopeProject;全局为零值
}

// GlobalScope 是某个 Agent 的全局作用域。
func GlobalScope(agent AgentCode) Scope {
	return Scope{Kind: ScopeGlobal, Agent: agent}
}

// ProjectScope 是某个 Agent 在某个已登记项目里的作用域。
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

// ListAgents 列出全部 Agent,按代码排序。Agent 随应用版本内置,用户和调用方都不能增删改;
// 库从装了更新版本应用的机器同步来、本版应用不认识的 Agent 也在其中。结果只在应用版本变化时改变,
// 调用方载入一次即可。
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

// RegisterProject 登记一个项目目录,使它与每个 Agent 组成可安装的项目作用域。path 必须是本机上存在的目录,
// 保存规范化后的绝对路径;为空、不存在或不是目录返回 InvalidArgument(Details.Field 为 "path")。
// 已登记过返回 AlreadyExists,Details 带已登记项目的 ProjectID 和 Path,界面可直接跳到那个项目。
func (l *Library) RegisterProject(ctx context.Context, path string) (project RegisteredProject, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	path, err = projectPath("register project", path)
	if err != nil {
		return RegisteredProject{}, err
	}
	row, created, err := l.store.RegisterProject(ctx, path)
	if err != nil {
		return RegisteredProject{}, err
	}
	if !created {
		return RegisteredProject{}, existsError("register project", "path already registered", ErrorDetails{ProjectID: ProjectID(row.ID), Path: path})
	}
	return registeredProject(row, false), nil
}

// ListRegisteredProjects 列出全部已登记项目,按 Name 排序,同名再按 Path。每个项目的 Missing 在列出时现查。
// 结果只在登记、改位置、注销项目时改变(Missing 除外),调用方载入一次即可。
func (l *Library) ListRegisteredProjects(ctx context.Context) (projects []RegisteredProject, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	rows, err := l.store.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		exists, err := agentdir.DirectoryExists(row.Path)
		if err != nil {
			return nil, fmt.Errorf("inspect registered project: %w", err)
		}
		projects = append(projects, registeredProject(row, !exists))
	}
	slices.SortFunc(projects, func(a, b RegisteredProject) int {
		return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.Path, b.Path))
	})
	return projects, nil
}

// RelocateProject 把已登记项目 id 指到本机上的另一个目录:从别的机器同步过来后指到它在这台机器上的位置,
// 或者项目目录在本机被移动、改名之后。path 的规则与 RegisterProject 相同;已被另一个已登记项目占用返回
// AlreadyExists,Details 带那个项目的 ProjectID 和 Path;与原路径相同时什么都不改。未登记的 id 返回 NotFound。
// 项目 ID、在这个项目里的直接安装与组安装都不变;不搬动、不删除任何文件夹,旧位置的技能文件夹库从此不再管。
// 新位置里各安装的状态照常由巡检给出:整个项目目录搬过来时是正常的,新位置没有这些文件夹时显示缺失,可以重新安装。
func (l *Library) RelocateProject(ctx context.Context, id ProjectID, path string) (project RegisteredProject, err error) {
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	path, err = projectPath("relocate project", path)
	if err != nil {
		return RegisteredProject{}, err
	}
	err = l.store.InTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.GetProject(ctx, int64(id)); err != nil {
			return err
		}
		other, err := tx.ProjectByPath(ctx, path)
		switch {
		case err == nil && other.ID != int64(id):
			return existsError("relocate project", "path already registered", ErrorDetails{ProjectID: ProjectID(other.ID), Path: path})
		case err == nil:
			return nil
		case !errors.Is(err, store.ErrNotFound):
			return err
		}
		return tx.RelocateProject(ctx, int64(id), path)
	})
	if err != nil {
		return RegisteredProject{}, err
	}
	return registeredProject(store.Project{ID: int64(id), Path: path}, false), nil
}

// UnregisterProject 注销一个已登记项目:它从项目列表里消失,在各 Agent 下的直接安装与组安装都撤掉。
// 默认(opts.Folders 为零值或 Remove)同时删除库装进这个项目的技能文件夹;显式 Keep 时文件夹留在磁盘上,
// 但库从此不再管理它们。项目目录本身、库没装的文件夹都不动。项目不在这台机器上时也能注销:
// 要删的文件夹本来就不在,算成功。未登记的 id 返回 NotFound。
func (l *Library) UnregisterProject(ctx context.Context, id ProjectID, opts UnregisterProjectOptions) (err error) {
	ctx, unlock := l.exclusiveOperation(ctx)
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
		skillScopes, err := l.listSkillScopes(ctx, 0, nil)
		if err != nil {
			return err
		}
		for _, skillScope := range skillScopes {
			if skillScope.Scope.Kind == ScopeProject && skillScope.Scope.ProjectID == id {
				change.Remove(skillScope.Folder)
			}
		}
	}
	return l.applyChange(ctx, &change, func(tx *store.Tx) error {
		return tx.UnregisterProject(ctx, int64(id))
	})
}

// ScopeDirectory 返回该 Agent 在 scope 读取技能的目录:全局作用域在用户主目录下(以 Open 时为准),
// 项目作用域在该项目目录下。作用域里还什么都没装时也能查,用于显示目录或「在访达中显示」。
// 只读:不写库、不创建目录;项目不在这台机器上时照样返回路径。未知 Agent 或未登记项目返回 NotFound。
func (l *Library) ScopeDirectory(ctx context.Context, scope Scope) (directory string, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	row, err := scopeOf("scope directory", scope)
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

func registeredProject(row store.Project, missing bool) RegisteredProject {
	return RegisteredProject{ID: ProjectID(row.ID), Name: filepath.Base(row.Path), Path: row.Path, Missing: missing}
}

func projectPath(operation, path string) (string, error) {
	if path == "" {
		return "", argumentError(operation, "path", "path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve project path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return "", argumentError(operation, "path", "path does not exist")
	}
	if err != nil {
		return "", fmt.Errorf("resolve project path: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("read project path: %w", err)
	}
	if !info.IsDir() {
		return "", argumentError(operation, "path", "path is not a directory")
	}
	return filepath.Clean(resolved), nil
}
