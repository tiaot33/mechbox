package library

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/tiaot33/skills-tui/internal/library/internal/agentdir"
	"github.com/tiaot33/skills-tui/internal/library/internal/store"
)

// AgentCode 是 Agent 的稳定代码:跨应用版本、跨同步都不变。界面上区分 Agent(例如按代码取色)应该用它,不用名称。
// 自定义 Agent 的代码在添加时生成,修改名称和目录都不变。
type AgentCode string

// 应用内置的两个 Agent。调用方不应假定只有这两个,可选的 Agent 以 [Library.ListAgents] 为准。
const (
	AgentClaudeCode AgentCode = "claude-code"
	AgentCodex      AgentCode = "codex"
)

// Agent 是一个 Agent:内置的,或用户添加的自定义 Agent。名称可能变化,身份以代码为准。
// 几个 Agent 的 GlobalDirectory 相同时共用全局的技能文件夹,ProjectDirectory 相同时共用各项目里的技能文件夹:
// 装在其中一个,共用的 Agent 也算已装(见 [SkillScope].SharedWith)。
type Agent struct {
	Code AgentCode `json:"code"`
	Name string    `json:"name"`
	// Custom 表示用户添加的自定义 Agent,可以用 [Library.EditAgent] 修改、[Library.DeleteAgent] 删除。
	// 内置 Agent 的名称和目录随应用版本维护,用户不能改。
	Custom bool `json:"custom"`
	// Detected 表示在这台机器上检测到了这个 Agent,列出时现查:内置 Agent 看用户主目录下它的配置目录
	// (Claude Code 是 ~/.claude,Codex 是 ~/.codex)是否存在。自定义 Agent 不检测,始终为 false。
	Detected bool `json:"detected"`
	// GlobalDirectory 是全局作用域读取技能的目录,在用户主目录下(以 Open 时为准),与 ScopeDirectory(GlobalScope(Code)) 相同。
	GlobalDirectory string `json:"global_directory"`
	// ProjectDirectory 是项目作用域读取技能的目录相对已登记项目根的路径,斜杠分隔,如 .agents/skills。
	ProjectDirectory string `json:"project_directory"`
}

// CustomAgent 是添加或修改自定义 Agent 时用户填写的内容。不合法时返回 InvalidArgument,Details.Field 为对应的 JSON 字段名。
type CustomAgent struct {
	// Name 是展示名称,去掉首尾空白后不能为空。
	Name string `json:"name"`
	// GlobalDirectory 是全局技能目录:绝对路径,或以 ~/ 开头表示用户主目录下。必须在用户主目录里面(不能是主目录本身),
	// 按相对主目录的路径记录,同步到别的机器后对应到那台机器的主目录。目录不必已经存在,安装时创建。
	GlobalDirectory string `json:"global_directory"`
	// ProjectDirectory 是项目里的技能目录:相对已登记项目根的路径,不能是绝对路径,也不能是项目根本身或在项目外面。
	ProjectDirectory string `json:"project_directory"`
}

// EditAgentOptions 是 EditAgent 的选项。
type EditAgentOptions struct {
	// Migrate 选择把已装的技能文件夹原样迁移到新目录;默认不迁移。只对目录有变化的层级(全局或项目)起作用。
	Migrate bool `json:"migrate"`
}

// MigrationOutcome 是改目录并迁移时,一处安装的结果。
type MigrationOutcome string

const (
	MigrationMoved   MigrationOutcome = "moved"   // 文件夹原样搬到了新目录
	MigrationCopied  MigrationOutcome = "copied"  // 旧目录还有别的 Agent 在读:文件夹原样复制到新目录,旧文件夹留给它们,它们照常算已装
	MigrationSkipped MigrationOutcome = "skipped" // 新目录里已有同名文件夹,或库里已有别的技能装在那里:不覆盖、不搬,旧文件夹留在原处,这个 Agent 在这一处不再算已装
)

// AgentMigration 是改目录并迁移时一处安装的结果:这个 Agent 的某个作用域里的一个技能,含因共用目录而算已装的。
type AgentMigration struct {
	SkillID SkillID          `json:"skill_id"`
	Scope   Scope            `json:"scope"`
	Outcome MigrationOutcome `json:"outcome"`
	From    string           `json:"from"` // 旧目录里的技能文件夹
	To      string           `json:"to"`   // 新目录里的技能文件夹
}

// EditAgentResult 是 EditAgent 的结果。
type EditAgentResult struct {
	Agent Agent `json:"agent"`
	// Migrations 逐处列出迁移结果,按技能 ID、作用域排序;不迁移、或目录没变时为空。旧文件夹本来就不在的
	// (含项目不在这台机器上)没有可搬的东西,不出现在这里:这个 Agent 自己的安装记录照常保留,巡检会按新目录报出它的状态;
	// 只因共用目录而算已装的,改目录后不再算已装。
	Migrations []AgentMigration `json:"migrations"`
}

// DeleteAgentOptions 是 DeleteAgent 的选项。
type DeleteAgentOptions struct {
	// Folders 默认删除技能文件夹,只有显式 Keep 保留。还有别的 Agent 在读的文件夹不受它影响,总是保留(见 DeleteAgent)。
	Folders FolderPolicy `json:"folders"`
}

// DeleteAgentResult 是 DeleteAgent 的结果。
type DeleteAgentResult struct {
	// Kept 列出因为还有别的 Agent 在读而保留的技能文件夹,按技能 ID、作用域排序:Scope 是被删除的 Agent 的作用域,
	// SharedWith 是还在读这个文件夹的 Agent,它们照常算已装。没有这样的文件夹时为空。
	Kept []SkillScope `json:"kept"`
}

// ListAgents 列出全部 Agent,按代码排序:应用内置的、用户添加的自定义 Agent,以及库从装了更新版本应用的机器
// 同步来、本版应用不认识的 Agent。Detected 在列出时现查;其余字段只在应用版本变化、增删改自定义 Agent 或拉回库时改变。
func (l *Library) ListAgents(ctx context.Context) (agents []Agent, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	rows, err := l.store.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		agent, err := l.agentOf(row)
		if err != nil {
			return nil, err
		}
		agents = append(agents, agent)
	}
	return agents, nil
}

// AddAgent 添加一个自定义 Agent,生成它的代码;之后它和内置 Agent 一样组成全局与各已登记项目的作用域,可以安装。
// 目录与已有的 Agent 相同时与它们共用技能文件夹,那里已经装着的技能,这个 Agent 立刻也算已装。
// 填写的内容不合法返回 InvalidArgument(规则见 CustomAgent)。
func (l *Library) AddAgent(ctx context.Context, agent CustomAgent) (added Agent, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	row, err := l.customAgentRow("add agent", agent)
	if err != nil {
		return Agent{}, err
	}
	row.Code = "custom-" + hex.EncodeToString(randomBytes(6))
	if err := l.store.AddAgent(ctx, row); err != nil {
		return Agent{}, err
	}
	return l.agentOf(row)
}

// EditAgent 修改自定义 Agent 的名称和目录,代码不变;填写规则同 AddAgent。未知代码返回 NotFound,内置 Agent 返回
// FailedPrecondition。只改名称时安装记录和文件夹都不动。某一层级(全局或项目)的目录改了时,这个 Agent 在该层级的安装
// (含因共用目录而算已装的)按 opts 处理:
//   - 迁移:已装的文件夹原样搬到新目录,不从库里重新安装,这个 Agent 在那里照常算已装;新目录里已有同名文件夹、
//     或库里已有别的技能装在那里的不覆盖、不搬,旧文件夹留在原处,这个 Agent 在这一处不再算已装。逐处结果见
//     EditAgentResult.Migrations。新目录落在某个要迁移的技能文件夹里面时返回 InvalidArgument(Details.Field 为对应目录字段),
//     什么都不改;复制期间新目录里出现了同名文件夹时返回 AlreadyExists(Details.Path 为该文件夹),什么都不改。
//   - 不迁移:只改目录,旧目录里的文件夹不动,去掉这个 Agent 在该层级的直接安装与组安装。
//
// 旧目录里还有别的 Agent 在读的文件夹不动:迁移时原样复制到新目录,不迁移就什么都不做;这个 Agent 在那里的安装记录
// 转给还在读的 Agent,它们照常算已装。失败时磁盘和库都保持原样。
func (l *Library) EditAgent(ctx context.Context, code AgentCode, agent CustomAgent, opts EditAgentOptions) (result EditAgentResult, err error) {
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	const operation = "edit agent"
	row, err := l.customAgentRow(operation, agent)
	if err != nil {
		return EditAgentResult{}, err
	}
	row.Code = string(code)
	current, err := l.customAgent(ctx, operation, code)
	if err != nil {
		return EditAgentResult{}, err
	}
	changed := map[bool]bool{false: current.GlobalPath != row.GlobalPath, true: current.ProjectPath != row.ProjectPath}
	installs, err := l.agentInstalls(ctx, code)
	if err != nil {
		return EditAgentResult{}, err
	}
	if opts.Migrate {
		for _, item := range installs {
			project := item.target.projectID != nil
			if !changed[project] {
				continue
			}
			destination := migrationDestination(item, row)
			if agentdir.Canonical(destination.directory) == agentdir.Canonical(item.Folder) || isInside(item.Folder, destination.directory) {
				field := "global_directory"
				if project {
					field = "project_directory"
				}
				return EditAgentResult{}, argumentError(operation, field, "directory is inside an installed skill folder")
			}
		}
	}
	var change agentdir.Change
	defer func() { err = errors.Join(err, change.Rollback()) }()
	var handOver, claimed []agentInstall
	var dropped []int64
	planned := map[string]bool{}
	result.Migrations = []AgentMigration{}
	for _, item := range installs {
		project := item.target.projectID != nil
		if !changed[project] {
			continue
		}
		if item.own != nil && item.receiver != "" {
			handOver = append(handOver, item)
		}
		if !opts.Migrate {
			continue
		}
		exists, err := agentdir.Exists(item.Folder)
		if err != nil {
			return EditAgentResult{}, fmt.Errorf("inspect installed folder: %w", err)
		}
		if !exists {
			continue
		}
		destination := migrationDestination(item, row)
		name := filepath.Base(item.Folder)
		migration := AgentMigration{SkillID: item.SkillID, Scope: item.Scope, From: item.Folder, To: filepath.Join(destination.directory, name)}
		occupied, err := l.migrationBlocked(ctx, code, changed, item.SkillID, name, destination, planned)
		if err != nil {
			return EditAgentResult{}, err
		}
		switch {
		case occupied:
			migration.Outcome = MigrationSkipped
			if item.own != nil {
				dropped = append(dropped, item.own.ID)
			}
		default:
			if err := change.Copy(ctx, item.Folder, destination.directory, name); err != nil {
				return EditAgentResult{}, err
			}
			planned[agentdir.Canonical(migration.To)] = true
			migration.Outcome = MigrationCopied
			if item.receiver == "" {
				change.Remove(item.Folder)
				migration.Outcome = MigrationMoved
			}
			if item.own == nil {
				claimed = append(claimed, item)
			}
		}
		result.Migrations = append(result.Migrations, migration)
	}
	err = l.applyChange(ctx, &change, func(tx *store.Tx) error {
		for _, item := range handOver {
			if err := tx.HandOverSkillScope(ctx, item.own.ID, string(item.receiver)); err != nil {
				return err
			}
		}
		if !opts.Migrate {
			if err := tx.RemoveAgentInstalls(ctx, string(code), changed[false], changed[true]); err != nil {
				return err
			}
		}
		for _, id := range dropped {
			if err := tx.RemoveSkillScope(ctx, id); err != nil {
				return err
			}
		}
		for _, item := range claimed {
			if err := tx.ClaimSkillScope(ctx, int64(item.SkillID), item.target.record(), item.CreatedAt); err != nil {
				return err
			}
		}
		return tx.EditAgent(ctx, row)
	})
	if err != nil {
		return EditAgentResult{}, err
	}
	result.Agent, err = l.agentOf(row)
	return result, err
}

func migrationDestination(item agentInstall, row store.Agent) resolvedScope {
	destination := item.target
	destination.relative = row.GlobalPath
	if destination.projectID != nil {
		destination.relative = row.ProjectPath
	}
	destination.directory = filepath.Join(destination.root, filepath.FromSlash(destination.relative))
	return destination
}

func isInside(root, path string) bool {
	_, inside := relativeInside(agentdir.Canonical(root), agentdir.Canonical(path))
	return inside
}

func (l *Library) migrationBlocked(ctx context.Context, code AgentCode, changed map[bool]bool, id SkillID, name string, destination resolvedScope, planned map[string]bool) (bool, error) {
	folder := filepath.Join(destination.directory, name)
	if planned[agentdir.Canonical(folder)] {
		return true, nil
	}
	exists, err := agentdir.Exists(folder)
	if err != nil || exists {
		return exists, err
	}
	claimants, err := l.store.Claimants(ctx, name, int64(id))
	if err != nil {
		return false, err
	}
	want := agentdir.Canonical(destination.directory)
	for _, other := range claimants {
		if other.Scope.Agent == string(code) && changed[other.Scope.ProjectID != nil] {
			continue
		}
		claimed, err := l.resolveRow(ctx, scopeRowOf(other.Scope))
		if err != nil {
			return false, err
		}
		if agentdir.Canonical(claimed.directory) != want {
			continue
		}
		if SkillID(other.SkillID) != id || !claimed.sharesFolderWith(destination) {
			return true, nil
		}
	}
	return false, nil
}

// DeleteAgent 删除一个自定义 Agent:它从 Agent 列表里消失,直接安装与组安装都撤掉。默认(opts.Folders 为零值或 Remove)
// 同时删除库为它装的技能文件夹;显式 Keep 时文件夹留在磁盘上,库从此不再管理它们。还有别的 Agent 在读的文件夹,
// 即使选了 Remove 也保留,这个 Agent 在那里的安装记录转给还在读的 Agent,它们照常算已装;保留了哪些、还有哪些 Agent 在读,
// 见 DeleteAgentResult.Kept。删除前的确认框用 ListSkillScopes 按这个 Agent 的作用域筛选,SharedWith 非空的就是会保留的。
// 未知代码返回 NotFound,内置 Agent 返回 FailedPrecondition。失败时磁盘和库都保持原样。
func (l *Library) DeleteAgent(ctx context.Context, code AgentCode, opts DeleteAgentOptions) (result DeleteAgentResult, err error) {
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	const operation = "delete agent"
	if err := validateFolderPolicy(operation, opts.Folders); err != nil {
		return DeleteAgentResult{}, err
	}
	if _, err := l.customAgent(ctx, operation, code); err != nil {
		return DeleteAgentResult{}, err
	}
	installs, err := l.agentInstalls(ctx, code)
	if err != nil {
		return DeleteAgentResult{}, err
	}
	var change agentdir.Change
	defer func() { err = errors.Join(err, change.Rollback()) }()
	var handOver []agentInstall
	result.Kept = []SkillScope{}
	for _, item := range installs {
		switch {
		case item.receiver != "":
			result.Kept = append(result.Kept, item.SkillScope)
			if item.own != nil {
				handOver = append(handOver, item)
			}
		case opts.Folders != Keep:
			change.Remove(item.Folder)
		}
	}
	err = l.applyChange(ctx, &change, func(tx *store.Tx) error {
		for _, item := range handOver {
			if err := tx.HandOverSkillScope(ctx, item.own.ID, string(item.receiver)); err != nil {
				return err
			}
		}
		return tx.DeleteAgent(ctx, string(code))
	})
	if err != nil {
		return DeleteAgentResult{}, err
	}
	return result, nil
}

type agentInstall struct {
	SkillScope
	target   resolvedScope
	own      *store.PlaceSkillScope
	receiver AgentCode
}

func (l *Library) agentInstalls(ctx context.Context, code AgentCode) ([]agentInstall, error) {
	skillScopes, err := l.listSkillScopes(ctx, 0, nil)
	if err != nil {
		return nil, err
	}
	var installs []agentInstall
	for _, skillScope := range skillScopes {
		if skillScope.Scope.Agent != code {
			continue
		}
		target, err := l.resolveScope(ctx, "list agent installs", skillScope.Scope)
		if err != nil {
			return nil, err
		}
		item := agentInstall{SkillScope: skillScope, target: target}
		place, err := l.store.PlaceSkillScopes(ctx, int64(skillScope.SkillID), target.record())
		if err != nil {
			return nil, err
		}
		for _, row := range place {
			if row.Agent == string(code) {
				item.own = &row
			} else if item.receiver == "" {
				item.receiver = AgentCode(row.Agent)
			}
		}
		if item.receiver == "" && len(skillScope.SharedWith) > 0 {
			item.receiver = skillScope.SharedWith[0]
		}
		installs = append(installs, item)
	}
	return installs, nil
}

func (l *Library) customAgent(ctx context.Context, operation string, code AgentCode) (store.Agent, error) {
	agent, err := l.store.GetAgent(ctx, string(code))
	if err != nil {
		return store.Agent{}, err
	}
	if !agent.Custom {
		return store.Agent{}, preconditionError(operation, "built-in agent cannot be changed", ErrorDetails{})
	}
	return agent, nil
}

func (l *Library) agentOf(row store.Agent) (Agent, error) {
	agent := Agent{
		Code: AgentCode(row.Code), Name: row.Name, Custom: row.Custom,
		GlobalDirectory: filepath.Join(l.home, filepath.FromSlash(row.GlobalPath)), ProjectDirectory: row.ProjectPath,
	}
	if row.DetectPath != "" {
		detected, err := agentdir.DirectoryExists(filepath.Join(l.home, filepath.FromSlash(row.DetectPath)))
		if err != nil {
			return Agent{}, fmt.Errorf("detect agent: %w", err)
		}
		agent.Detected = detected
	}
	return agent, nil
}

func (l *Library) customAgentRow(operation string, agent CustomAgent) (store.Agent, error) {
	name := strings.TrimSpace(agent.Name)
	if name == "" {
		return store.Agent{}, argumentError(operation, "name", "name is required")
	}
	global := agent.GlobalDirectory
	if global == "" {
		return store.Agent{}, argumentError(operation, "global_directory", "global directory is required")
	}
	if global == "~" || strings.HasPrefix(global, "~/") {
		global = filepath.Join(l.home, global[1:])
	}
	if !filepath.IsAbs(global) {
		return store.Agent{}, argumentError(operation, "global_directory", "global directory must be an absolute path")
	}
	globalPath, inside := relativeInside(l.home, global)
	if !inside {
		globalPath, inside = relativeInside(agentdir.Canonical(l.home), agentdir.Canonical(global))
	}
	if !inside {
		return store.Agent{}, argumentError(operation, "global_directory", "global directory must be inside the home directory")
	}
	project := agent.ProjectDirectory
	if project == "" {
		return store.Agent{}, argumentError(operation, "project_directory", "project directory is required")
	}
	if filepath.IsAbs(project) {
		return store.Agent{}, argumentError(operation, "project_directory", "project directory must be relative to the project")
	}
	project = filepath.Clean(project)
	if project == "." || !filepath.IsLocal(project) {
		return store.Agent{}, argumentError(operation, "project_directory", "project directory must be inside the project")
	}
	return store.Agent{Name: name, GlobalPath: filepath.ToSlash(globalPath), ProjectPath: filepath.ToSlash(project), Custom: true}, nil
}

func relativeInside(root, path string) (string, bool) {
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == "." || !filepath.IsLocal(relative) {
		return "", false
	}
	return relative, true
}

func randomBytes(n int) []byte {
	buffer := make([]byte, n)
	_, _ = rand.Read(buffer)
	return buffer
}
