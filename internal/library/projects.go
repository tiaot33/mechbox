package library

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/tiaot33/mechbox/internal/library/internal/agentdir"
	"github.com/tiaot33/mechbox/internal/library/internal/store"
)

// ProjectID 是已登记项目的身份:登记时分配,注销前不变;同步到另一台机器、或改了项目位置后仍指同一个项目。
type ProjectID int64

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
	// DiscardEdits 是确认时告诉用户被改过、修改会丢掉的技能文件夹(SkillScope.Folder)。动磁盘之前核对这次要删的每个文件夹:
	// 有被改过的(SkillScopeModified,不算 SkillScopeInspection.SourceLatest,也不算是符号链接的那一处,删的只是链接)却不在这里,
	// 整次返回 Conflict(Details.Paths 为这几个文件夹),一个都不删;列了而现在没被改过的照常删。不删文件夹时不核对。
	DiscardEdits []string `json:"discard_edits,omitempty"`
	// DeleteLinks 是确认时告诉用户会随文件夹一并删掉的旧链接(SkillScopeInspection.StaleLinks)。动磁盘之前现找这次要一并删的旧链接:
	// 有不在这里的(确认之后才出现的),整次返回 Conflict(Details.Links 为这几个链接),一个都不删;可以和 Details.Paths 同时出现。
	// 不删文件夹时不核对。
	DeleteLinks []string `json:"delete_links,omitempty"`
}

func scopeOf(operation string, scope Scope) (store.Scope, error) {
	switch scope.Kind {
	case ScopeGlobal:
		if scope.ProjectID != 0 {
			return store.Scope{}, argumentError(operation, "project_id", "global scope must not have a project")
		}
		return store.Scope{Agent: string(scope.Agent)}, nil
	case ScopeProject:
		if scope.ProjectID <= 0 {
			return store.Scope{}, argumentError(operation, "project_id", "project scope requires a project")
		}
		return store.Scope{Agent: string(scope.Agent), ProjectID: new(int64(scope.ProjectID))}, nil
	default:
		return store.Scope{}, argumentError(operation, "kind", "unknown scope kind")
	}
}

func scopeFrom(row store.Scope) Scope {
	if row.ProjectID == nil {
		return GlobalScope(AgentCode(row.Agent))
	}
	return ProjectScope(AgentCode(row.Agent), ProjectID(*row.ProjectID))
}

type resolvedScope struct {
	store.Scope
	root      string
	paths     []string
	relative  string
	directory string
}

func (s resolvedScope) scope() Scope {
	return scopeFrom(s.Scope)
}

func (s resolvedScope) in(relative string) resolvedScope {
	s.relative = relative
	s.directory = agentdir.Join(s.root, relative)
	return s
}

func (s resolvedScope) sharesFolderWith(other resolvedScope) bool {
	if (s.ProjectID == nil) != (other.ProjectID == nil) || s.relative != other.relative {
		return false
	}
	return s.ProjectID == nil || *s.ProjectID == *other.ProjectID
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
	if s.ProjectID != nil {
		details.ProjectID = ProjectID(*s.ProjectID)
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
	return l.resolveRow(ctx, row)
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
	return registeredProjects(rows)
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
// 默认(opts.Folders 为零值或 Remove)同时删除库装进这个项目的技能文件夹,连同指向它们的旧链接(同 Uninstall);
// 显式 Keep 时文件夹留在磁盘上,但库从此不再管理它们。项目目录本身、库没装的文件夹都不动。项目不在这台机器上时也能注销:
// 要删的文件夹本来就不在,算成功。删之前核对文件夹有没有被改过,确认之后才被改过的返回 Conflict,不注销(见 UnregisterProjectOptions)。
// 未登记的 id 返回 NotFound。
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
	var removals []removal
	if opts.Folders != Keep {
		skillScopes, err := l.listSkillScopes(ctx, 0, nil)
		if err != nil {
			return err
		}
		var places []installedPlace
		for _, skillScope := range skillScopes {
			if skillScope.Scope.Kind != ScopeProject || skillScope.Scope.ProjectID != id {
				continue
			}
			target, err := l.resolveScope(ctx, "unregister project", skillScope.Scope)
			if err != nil {
				return err
			}
			place, err := l.installedAt(ctx, skillScope.SkillID, target)
			if err != nil {
				return err
			}
			places = append(places, place)
		}
		if removals, err = l.placeRemovals(ctx, places); err != nil {
			return err
		}
	}
	var change agentdir.Change
	defer func() { err = errors.Join(err, change.Rollback()) }()
	return l.applyChange(ctx, &change, func(tx *store.Tx) error {
		if err := l.removeFolders(ctx, tx, &change, "unregister project", removals, confirmed{edits: opts.DiscardEdits, links: opts.DeleteLinks}); err != nil {
			return err
		}
		return tx.UnregisterProject(ctx, int64(id))
	})
}

// ScopeDirectory 返回该 Agent 在 scope 装新技能的目录,即这一层级的第 1 个技能目录:全局作用域在用户主目录下(以 Open 时为准),
// 项目作用域在该项目目录下。作用域里还什么都没装时也能查,用于显示目录或「在访达中显示」。
// 只读:不写库、不创建目录;项目不在这台机器上时照样返回路径。未知 Agent、候选池里的 Agent 或未登记项目返回 NotFound。
func (l *Library) ScopeDirectory(ctx context.Context, scope Scope) (directory string, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	row, err := scopeOf("scope directory", scope)
	if err != nil {
		return "", err
	}
	target, err := l.resolveRow(ctx, row)
	return target.directory, err
}

func (l *Library) resolveRow(ctx context.Context, row store.Scope) (resolvedScope, error) {
	agent, err := l.enabledAgent(ctx, row.Agent)
	if err != nil {
		return resolvedScope{}, err
	}
	target := resolvedScope{Scope: row, root: l.home.Root(), paths: agent.GlobalPaths}
	if row.ProjectID != nil {
		project, err := l.store.GetProject(ctx, *row.ProjectID)
		if err != nil {
			return resolvedScope{}, err
		}
		target.root, target.paths = project.Path, agent.ProjectPaths
	}
	return target.in(target.paths[0]), nil
}

func (l *Library) enabledAgent(ctx context.Context, code string) (store.Agent, error) {
	agent, err := l.store.GetAgent(ctx, code)
	if err != nil {
		return store.Agent{}, err
	}
	if !agent.Enabled {
		return store.Agent{}, fmt.Errorf("agent %s is not added: %w", code, store.ErrNotFound)
	}
	return agent, nil
}

func registeredProjects(rows []store.Project) ([]RegisteredProject, error) {
	var projects []RegisteredProject
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

func registeredProject(row store.Project, missing bool) RegisteredProject {
	return RegisteredProject{ID: ProjectID(row.ID), Name: agentdir.ProjectName(row.Path), Path: row.Path, Missing: missing}
}

func projectPath(operation, path string) (string, error) {
	if path == "" {
		return "", argumentError(operation, "path", "path is required")
	}
	root, err := agentdir.ProjectRoot(path)
	switch {
	case errors.Is(err, agentdir.ErrMissing):
		return "", argumentError(operation, "path", "path does not exist")
	case errors.Is(err, agentdir.ErrNotDirectory):
		return "", argumentError(operation, "path", "path is not a directory")
	}
	return root, err
}
