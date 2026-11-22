package library

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/tiaot33/mechbox/internal/library/internal/agentdir"
	"github.com/tiaot33/mechbox/internal/library/internal/store"
)

// AgentCode 是 Agent 的稳定代码:跨应用版本、跨同步都不变。界面上区分 Agent(例如按代码取色)应该用它,不用名称。
// 应用收录的 Agent 的代码随应用固定(如 claude-code、cursor),用户手填添加的在添加时生成;改名称和目录都不变。
type AgentCode string

// 新数据库里先添加的两个 Agent。调用方不应假定只有这两个,已添加的 Agent 以 [Library.ListAgents] 为准。
const (
	AgentClaudeCode AgentCode = "claude-code"
	AgentCodex      AgentCode = "codex"
)

// Agent 是一个 Agent:应用收录的,或用户手填添加的,两者地位相同,名称、技能目录、检测目录都能改。名称可能变化,身份以代码为准。
// 已添加的 Agent 组成全局与各已登记项目的作用域,可以安装;候选池里的([Library.ListAgentPool])不参与任何作用域。
// 一个层级可以有几个技能目录:第 1 个装新技能,其余是附加目录,里面已有的技能可以原地入库。
// 几个已添加的 Agent 的目录列表里有同一个目录时,装在那里的技能它们都读得到(见 [SkillScope].SharedWith)。
type Agent struct {
	Code AgentCode `json:"code"`
	Name string    `json:"name"`
	// Detected 表示在这台机器上检测到了这个 Agent:DetectDirectories 里任一目录存在。列出时现查;DetectDirectories 为空时始终为 false。
	Detected bool `json:"detected"`
	// GlobalDirectories 是全局作用域读取技能的目录,在用户主目录下(以 Open 时为准),绝对路径,至少 1 个;
	// 第 1 个装新技能,与 ScopeDirectory(GlobalScope(Code)) 相同。
	GlobalDirectories []string `json:"global_directories"`
	// ProjectDirectories 是项目作用域读取技能的目录相对已登记项目根的路径,斜杠分隔,如 .agents/skills,至少 1 个;第 1 个装新技能。
	ProjectDirectories []string `json:"project_directories"`
	// DetectDirectories 是检测这台机器上有没有装这个 Agent 时看的目录,在用户主目录下,绝对路径;任一存在即算检测到。可以为空。
	DetectDirectories []string `json:"detect_directories"`
	// SameName 是全局和项目里有同名技能时这个 Agent 会怎样(见 [Library.ListSameNameSkills]):存在数据库里,
	// Claude Code 为 SameNameGlobalWins,Codex 为 SameNameBothShown,其余为 SameNameUnknown。
	SameName SameNameRule `json:"same_name,omitempty"`
}

// SameNameRule 是一个 Agent 在全局和项目里都有同名技能时会怎样,按生态里已知的规则;只看全局与项目这两层,
// 不推算插件是否启用、frontmatter 里关闭自动调用等其他规则。
type SameNameRule string

const (
	SameNameUnknown    SameNameRule = ""            // 没有已知规则:只标出同名
	SameNameGlobalWins SameNameRule = "global_wins" // 全局的那个生效,项目里的不会被用到(Claude Code:个人 > 项目)
	SameNameBothShown  SameNameRule = "both_shown"  // 不合并,两份都会出现(Codex)
)

// AgentInput 是手填添加或编辑 Agent 时用户填写的内容。不合法时返回 InvalidArgument,Details.Field 为对应的 JSON 字段名。
type AgentInput struct {
	// Name 是展示名称,去掉首尾空白后不能为空。
	Name string `json:"name"`
	// GlobalDirectories 是全局技能目录,至少 1 个,第 1 个装新技能:每项是绝对路径,或以 ~/ 开头表示用户主目录下。
	// 必须在用户主目录里面(不能是主目录本身),按相对主目录的路径记录,同步到别的机器后对应到那台机器的主目录。
	// 规范化后不能重复,也不能一项在另一项里面。目录不必已经存在,安装时创建。
	GlobalDirectories []string `json:"global_directories"`
	// ProjectDirectories 是项目里的技能目录,至少 1 个,第 1 个装新技能:每项是相对已登记项目根的路径,不能是绝对路径,
	// 也不能是项目根本身或在项目外面。规范化后不能重复,也不能一项在另一项里面。
	ProjectDirectories []string `json:"project_directories"`
	// DetectDirectories 是检测这个 Agent 有没有装时看的目录,写法与 GlobalDirectories 相同;可以为空,为空时不检测。重复的只算一次。
	DetectDirectories []string `json:"detect_directories"`
}

// EditAgentOptions 是 EditAgent 的选项。
type EditAgentOptions struct {
	// Migrate 选择把实际目录不在新目录列表里的安装原样迁移到新的第 1 个目录;默认不迁移。
	Migrate bool `json:"migrate"`
}

// MigrationOutcome 是改目录并迁移时,一处安装的结果。
type MigrationOutcome string

const (
	MigrationMoved   MigrationOutcome = "moved"   // 文件夹原样搬到了新目录
	MigrationCopied  MigrationOutcome = "copied"  // 旧目录还有别的 Agent 在读:文件夹原样复制到新目录,旧文件夹留给它们,认这一处的照常算已装
	MigrationSkipped MigrationOutcome = "skipped" // 新目录里已有不是这个技能的同名文件夹,或库里已有别的技能装在那里:不覆盖、不搬,旧文件夹留在原处,这个 Agent 在这一处不再算已装
	// 新目录里已经装着库里的这个技能(别的 Agent 装的,文件夹名可以不同):不复制、不搬,这个 Agent 改认那一处,照常算已装;
	// 旧文件夹留在原处,还有别的 Agent 在读的连同安装记录留给它们,没有的库不再管理它。
	MigrationAlreadyInstalled MigrationOutcome = "already_installed"
)

// AgentMigration 是改目录并迁移时一处安装的结果:这个 Agent 的某个作用域里的一个技能,含只因共用目录而算已装的。
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
	// Migrations 逐处列出迁移结果,按技能 ID、作用域排序;不迁移、或没有安装的实际目录被去掉时为空。旧文件夹本来就不在的
	// (含项目不在这台机器上)没有可搬的东西,不出现在这里:这个 Agent 自己的安装记录照常改到新的第 1 个目录,巡检会按那里报出它的状态;
	// 只因共用目录而算已装的,改目录后不再算已装。
	Migrations []AgentMigration `json:"migrations"`
}

// DeleteAgentOptions 是 DeleteAgent 的选项。
type DeleteAgentOptions struct {
	// Folders 默认删除技能文件夹,只有显式 Keep 保留。还有别的已添加 Agent 在读的文件夹不受它影响,总是保留(见 DeleteAgent)。
	Folders FolderPolicy `json:"folders"`
	// DiscardEdits 是确认时告诉用户被改过、修改会丢掉的技能文件夹(SkillScope.Folder)。动磁盘之前核对这次要删的每个文件夹:
	// 有被改过的(SkillScopeModified,不算 SkillScopeInspection.SourceLatest,也不算是符号链接的那一处,删的只是链接)却不在这里,
	// 整次返回 Conflict(Details.Paths 为这几个文件夹),一个都不删;列了而现在没被改过的照常删。不删文件夹时不核对。
	DiscardEdits []string `json:"discard_edits,omitempty"`
}

// DeleteAgentResult 是 DeleteAgent 的结果。
type DeleteAgentResult struct {
	// Kept 列出因为还有别的已添加 Agent 在读而保留的技能文件夹,按技能 ID、作用域排序:Scope 是被移除的 Agent 的作用域,
	// SharedWith 是目录列表里还有这个目录的 Agent:认这一处的照常算已装,自己在别的目录装着这个技能的仍认那一处。没有这样的文件夹时为空。
	Kept []SkillScope `json:"kept"`
}

// ListAgents 列出已添加的 Agent,按代码排序,包括库从装了更新版本应用的机器同步来、本版应用没有收录的。
// Detected 在列出时现查;其余字段只在添加、编辑、移除 Agent 或拉回库时改变。
func (l *Library) ListAgents(ctx context.Context) (agents []Agent, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	return l.listAgents(ctx, true)
}

// ListAgentPool 列出候选池里的 Agent:应用收录了、还没添加的,以及移除后回到候选池的(保持用户改过的样子),按代码排序。
// Detected 在列出时现查,界面据此把检测到的排在前面。候选池里的 Agent 不组成作用域,装不了技能。
func (l *Library) ListAgentPool(ctx context.Context) (agents []Agent, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	return l.listAgents(ctx, false)
}

func (l *Library) listAgents(ctx context.Context, enabled bool) ([]Agent, error) {
	rows, err := l.store.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	rows = slices.DeleteFunc(rows, func(row store.Agent) bool { return row.Enabled != enabled })
	return l.agentsOf(ctx, rows)
}

// AddAgentsFromPool 一次添加几个候选池里的 Agent,按代码排序返回它们;之后它们组成全局与各已登记项目的作用域,可以安装。
// 它和已添加的 Agent 读同一个目录时,那里已经装着的技能它立刻也算已装。已经添加了的照旧,不报错;不认识的代码返回 NotFound,
// 什么都不添加。
func (l *Library) AddAgentsFromPool(ctx context.Context, codes []AgentCode) (added []Agent, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	var rows []store.Agent
	err = l.store.InTx(ctx, func(tx *store.Tx) error {
		rows = nil
		var enable []string
		for _, code := range codes {
			row, err := tx.GetAgent(ctx, string(code))
			if err != nil {
				return err
			}
			if !slices.ContainsFunc(rows, func(other store.Agent) bool { return other.Code == row.Code }) {
				row.Enabled = true
				rows = append(rows, row)
				enable = append(enable, row.Code)
			}
		}
		return tx.SetAgentsEnabled(ctx, enable, true)
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(rows, func(a, b store.Agent) int { return strings.Compare(a.Code, b.Code) })
	return l.agentsOf(ctx, rows)
}

// ApplyDetectedAgents 按这台机器上检测到的 Agent 设定已添加的 Agent,返回之后已添加的 Agent(同 ListAgents):
// 检测到的都添加;Claude Code、Codex 没检测到、又没有任何安装记录与组安装的移回候选池;一个 Agent 都没检测到时什么都不改,
// 保留新数据库里先添加的 Claude Code 和 Codex。桌面端在第一次打开的流程里、全局目录选好之后调用它,
// 这样在 Windows 上选了 WSL 发行版时检测的是发行版里的目录。用户添加的其他 Agent 不受影响。
func (l *Library) ApplyDetectedAgents(ctx context.Context) (agents []Agent, err error) {
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	rows, err := l.store.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	all, err := l.agentsOf(ctx, rows)
	if err != nil {
		return nil, err
	}
	var detected, missing []string
	for _, agent := range all {
		switch {
		case agent.Detected:
			detected = append(detected, string(agent.Code))
		case slices.Contains(store.EnabledInNewLibrary, string(agent.Code)):
			missing = append(missing, string(agent.Code))
		}
	}
	if len(detected) > 0 {
		err = l.store.InTx(ctx, func(tx *store.Tx) error {
			if err := tx.SetAgentsEnabled(ctx, detected, true); err != nil {
				return err
			}
			for _, code := range missing {
				installed, err := tx.AgentHasInstalls(ctx, code)
				if err != nil {
					return err
				}
				if !installed {
					if err := tx.SetAgentsEnabled(ctx, []string{code}, false); err != nil {
						return err
					}
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return l.listAgents(ctx, true)
}

// AddAgent 手填添加一个 Agent,生成它的代码;它直接是已添加的,和其他已添加的 Agent 一样组成全局与各已登记项目的作用域,可以安装。
// 目录与已添加的 Agent 有相同的时,那里已经装着的技能这个 Agent 立刻也算已装。填写的内容不合法返回 InvalidArgument(规则见 AgentInput)。
func (l *Library) AddAgent(ctx context.Context, agent AgentInput) (added Agent, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	row, err := l.agentRow("add agent", agent)
	if err != nil {
		return Agent{}, err
	}
	row.Code = customAgentPrefix + hex.EncodeToString(randomBytes(6))
	row.Enabled = true
	if err := l.store.AddAgent(ctx, row); err != nil {
		return Agent{}, err
	}
	return l.agentOf(ctx, row)
}

// EditAgent 修改一个已添加的 Agent 的名称、技能目录和检测目录,代码不变;填写规则同 AgentInput。未知代码或候选池里的 Agent
// 返回 NotFound。这个 Agent 的每处安装(含只因共用目录而算已装的)按编辑前的实际目录处理:
//   - 这个目录还在新列表里:磁盘不动,安装记录跟着新列表换算。
//   - 不在了、选了迁移:文件夹原样搬到新的第 1 个目录,不从库里重新安装,这个 Agent 在那里照常算已装;旧目录还有别的已添加 Agent
//     在读时复制过去,旧文件夹和它的安装记录留给它们。新目录里已经装着库里的这个技能(别的 Agent 装的)时不复制、不搬,
//     这个 Agent 改认那一处,照常算已装。新目录里已有别的同名文件夹、或库里已有别的技能装在那里的不覆盖、不搬,
//     旧文件夹留在原处,这个 Agent 在这一处不再算已装。逐处结果见 EditAgentResult.Migrations。新的第 1 个目录落在某个要迁移的
//     技能文件夹里面时返回 InvalidArgument(Details.Field 为对应目录字段),什么都不改;复制期间新目录里出现了同名文件夹时
//     返回 AlreadyExists(Details.Path 为该文件夹),什么都不改。
//   - 不在了、没选迁移:文件夹不动,去掉这个 Agent 在那里的安装记录;还有别的已添加 Agent 在读的,记录转给它们,它们照常算已装。
//
// 旧的第 1 个目录不在新列表里、又没选迁移时,还去掉这个 Agent 在这一层级的组安装(以后加入组的技能不再装到新目录),
// 只靠这些组留着的安装改为直接安装。只改名称或检测目录时安装记录和文件夹都不动。失败时磁盘和库都保持原样。
func (l *Library) EditAgent(ctx context.Context, code AgentCode, agent AgentInput, opts EditAgentOptions) (result EditAgentResult, err error) {
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	const operation = "edit agent"
	row, err := l.agentRow(operation, agent)
	if err != nil {
		return EditAgentResult{}, err
	}
	return l.editAgent(ctx, operation, code, row, opts)
}

func (l *Library) editAgent(ctx context.Context, operation string, code AgentCode, row store.Agent, opts EditAgentOptions) (result EditAgentResult, err error) {
	current, err := l.enabledAgent(ctx, string(code))
	if err != nil {
		return EditAgentResult{}, err
	}
	row.Code, row.SameName, row.Enabled = current.Code, current.SameName, true
	installs, err := l.agentInstalls(ctx, code)
	if err != nil {
		return EditAgentResult{}, err
	}
	removed := func(item agentInstall) bool {
		return !slices.Contains(row.Paths(item.project()), item.place.target.relative)
	}
	if opts.Migrate {
		for _, item := range installs {
			if !removed(item) {
				continue
			}
			destination := migrationDestination(item, row)
			if agentdir.Canonical(destination.directory) == agentdir.Canonical(item.Folder) || agentdir.Inside(item.Folder, destination.directory) {
				field := "global_directories"
				if item.project() {
					field = "project_directories"
				}
				return EditAgentResult{}, argumentError(operation, field, "directory is inside an installed skill folder")
			}
		}
	}
	var change agentdir.Change
	defer func() { err = errors.Join(err, change.Rollback()) }()
	type rebase struct {
		id        int64
		directory string
	}
	var handOver []agentInstall
	var dropped []int64
	var rebased []rebase
	var claimed []agentInstall
	planned := map[string]bool{}
	result.Migrations = []AgentMigration{}
	for _, item := range installs {
		if !removed(item) {
			if item.own != nil {
				rebased = append(rebased, rebase{item.own.ID, store.StoredDirectory(row, item.project(), item.place.target.relative)})
			}
			continue
		}
		if item.own != nil && item.receiver != "" {
			handOver = append(handOver, item)
		}
		if !opts.Migrate {
			if item.own != nil {
				dropped = append(dropped, item.own.ID)
			}
			continue
		}
		destination := migrationDestination(item, row)
		name := agentdir.FolderName(item.Folder)
		migration := AgentMigration{SkillID: item.SkillID, Scope: item.Scope, From: item.Folder, To: agentdir.FolderPath(destination.directory, name)}
		already, err := l.installedInDestination(ctx, item.SkillID, destination)
		if err != nil {
			return EditAgentResult{}, err
		}
		if already {
			if item.own != nil {
				dropped = append(dropped, item.own.ID)
			}
			migration.Outcome = MigrationAlreadyInstalled
			result.Migrations = append(result.Migrations, migration)
			continue
		}
		exists, err := agentdir.Exists(item.Folder)
		if err != nil {
			return EditAgentResult{}, fmt.Errorf("inspect installed folder: %w", err)
		}
		if !exists {
			if item.own != nil {
				rebased = append(rebased, rebase{item.own.ID, ""})
			}
			continue
		}
		occupied, err := l.migrationBlocked(ctx, code, installs, removed, item.SkillID, name, destination, planned)
		if err != nil {
			return EditAgentResult{}, err
		}
		if occupied {
			migration.Outcome = MigrationSkipped
			if item.own != nil {
				dropped = append(dropped, item.own.ID)
			}
			result.Migrations = append(result.Migrations, migration)
			continue
		}
		if err := change.Copy(ctx, item.Folder, destination.directory, name); err != nil {
			return EditAgentResult{}, err
		}
		planned[agentdir.Canonical(migration.To)] = true
		migration.Outcome = MigrationCopied
		if len(item.readers) == 0 {
			change.Remove(item.Folder)
			migration.Outcome = MigrationMoved
		}
		switch {
		case item.own == nil:
			claimed = append(claimed, item)
		case item.receiver == "":
			rebased = append(rebased, rebase{item.own.ID, ""})
		default:
			dropped = append(dropped, item.own.ID)
			claimed = append(claimed, item)
		}
		result.Migrations = append(result.Migrations, migration)
	}
	err = l.applyChange(ctx, &change, func(tx *store.Tx) error {
		for _, item := range handOver {
			if err := tx.HandOverSkillScope(ctx, item.own.ID, string(item.receiver)); err != nil {
				return err
			}
		}
		for _, id := range dropped {
			if err := tx.RemoveSkillScope(ctx, id); err != nil {
				return err
			}
		}
		for _, item := range rebased {
			if err := tx.SetSkillScopeDirectory(ctx, item.id, item.directory); err != nil {
				return err
			}
		}
		if err := tx.EditAgent(ctx, row); err != nil {
			return err
		}
		for _, item := range claimed {
			direct, created := true, item.CreatedAt
			if item.own != nil {
				direct, created = item.own.Direct, item.own.CreatedAt
			}
			if err := tx.RecordSkillScope(ctx, store.NewSkillScope{
				SkillID: int64(item.SkillID), Scope: item.place.target.Scope, Directory: row.Paths(item.project())[0],
				Folder: item.place.rows[0].Folder, Direct: direct, CreatedAt: created,
			}); err != nil {
				return err
			}
		}
		for _, project := range []bool{false, true} {
			if !opts.Migrate && !slices.Contains(row.Paths(project), current.Paths(project)[0]) {
				if err := tx.RemoveAgentGroupInstalls(ctx, row.Code, project); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return EditAgentResult{}, err
	}
	result.Agent, err = l.agentOf(ctx, row)
	return result, err
}

func migrationDestination(item agentInstall, row store.Agent) resolvedScope {
	return item.place.target.in(row.Paths(item.project())[0])
}

func (l *Library) installedInDestination(ctx context.Context, id SkillID, destination resolvedScope) (bool, error) {
	want := agentdir.Canonical(destination.directory)
	installed, err := l.listSkillScopes(ctx, id, nil)
	if err != nil {
		return false, err
	}
	for _, other := range installed {
		if agentdir.Canonical(agentdir.FolderDirectory(other.Folder)) == want {
			return true, nil
		}
	}
	return false, nil
}

func (l *Library) migrationBlocked(ctx context.Context, code AgentCode, installs []agentInstall, removed func(agentInstall) bool, id SkillID, name string, destination resolvedScope, planned map[string]bool) (bool, error) {
	folder := agentdir.FolderPath(destination.directory, name)
	if planned[agentdir.Canonical(folder)] {
		return true, nil
	}
	exists, err := agentdir.Exists(folder)
	if err != nil || exists {
		return exists, err
	}
	moving := func(other store.Claimant) bool {
		return other.Scope.Agent == string(code) && slices.ContainsFunc(installs, func(item agentInstall) bool {
			return item.own != nil && item.SkillID == SkillID(other.SkillID) && sameProject(item.place.target.ProjectID, other.Scope.ProjectID) && removed(item)
		})
	}
	want := agentdir.Canonical(destination.directory)
	claimants, err := l.store.Claimants(ctx, name, int64(id))
	if err != nil {
		return false, err
	}
	for _, other := range claimants {
		if moving(other) {
			continue
		}
		claimed, err := l.resolveRow(ctx, other.Scope)
		if err != nil {
			return false, err
		}
		if agentdir.Canonical(claimed.in(other.Directory).directory) == want {
			return true, nil
		}
	}
	return false, nil
}

// DeleteAgent 移除一个已添加的 Agent:它回到候选池(ListAgentPool),表里保持用户改过的样子,之后可以再添加;
// 它的直接安装与组安装都撤掉。技能文件夹:还有别的已添加 Agent 在读的总是保留,这个 Agent 在那里的安装记录转给还在读的 Agent,
// 它们照常算已装(保留了哪些、还有哪些 Agent 在读,见 DeleteAgentResult.Kept);其余按 opts.Folders:默认(零值或 Remove)删除
// 库为它装的技能文件夹,连同认这一处的 Agent 在别的目录里指向它的旧链接,显式 Keep 时留在磁盘上,库从此不再管理它们。
// 只因共用目录才算已装、不是为它装的文件夹都不动。它在某一层级的组安装转给同一层级目录列表完全相同的另一个已添加 Agent,
// 没有就撤掉。删除前的确认框用 ListSkillScopes 按这个 Agent 的作用域筛选,SharedWith 非空的就是会保留的。
// 删之前核对要删的文件夹有没有被改过,确认之后才被改过的返回 Conflict,不删除(见 DeleteAgentOptions)。
// 未知代码或候选池里的 Agent 返回 NotFound。失败时磁盘和库都保持原样。
func (l *Library) DeleteAgent(ctx context.Context, code AgentCode, opts DeleteAgentOptions) (result DeleteAgentResult, err error) {
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	const operation = "delete agent"
	if err := validateFolderPolicy(operation, opts.Folders); err != nil {
		return DeleteAgentResult{}, err
	}
	return l.deleteAgent(ctx, operation, code, opts, false)
}

func (l *Library) deleteAgent(ctx context.Context, operation string, code AgentCode, opts DeleteAgentOptions, forget bool) (result DeleteAgentResult, err error) {
	agent, err := l.enabledAgent(ctx, string(code))
	if err != nil {
		return DeleteAgentResult{}, err
	}
	heirs, err := l.groupHeirs(ctx, agent)
	if err != nil {
		return DeleteAgentResult{}, err
	}
	installs, err := l.agentInstalls(ctx, code)
	if err != nil {
		return DeleteAgentResult{}, err
	}
	var change agentdir.Change
	defer func() { err = errors.Join(err, change.Rollback()) }()
	var handOver []agentInstall
	var removed []installedPlace
	result.Kept = []SkillScope{}
	for i, item := range installs {
		if heir := heirs[item.project()]; heir != "" && slices.Contains(item.SharedWith, heir) {
			installs[i].receiver, item.receiver = heir, heir
		}
		switch {
		case item.receiver != "":
			result.Kept = append(result.Kept, item.SkillScope)
			if item.own != nil {
				handOver = append(handOver, item)
			}
		case len(item.readers) > 0:
			kept := item.SkillScope
			kept.SharedWith = item.readers
			result.Kept = append(result.Kept, kept)
		case opts.Folders != Keep && item.own != nil:
			removed = append(removed, item.place)
		}
	}
	removals, err := l.placeRemovals(ctx, removed)
	if err != nil {
		return DeleteAgentResult{}, err
	}
	groupInstalls, err := l.store.ListGroupInstalls(ctx, 0)
	if err != nil {
		return DeleteAgentResult{}, err
	}
	err = l.applyChange(ctx, &change, func(tx *store.Tx) error {
		if err := l.removeFolders(ctx, tx, &change, operation, removals, opts.DiscardEdits); err != nil {
			return err
		}
		for _, install := range groupInstalls {
			heir := heirs[install.Scope.ProjectID != nil]
			if install.Scope.Agent != string(code) || heir == "" {
				continue
			}
			if err := tx.RecordGroupInstall(ctx, install.GroupID, store.Scope{Agent: string(heir), ProjectID: install.Scope.ProjectID}, install.CreatedAt); err != nil {
				return err
			}
		}
		for _, item := range handOver {
			if err := tx.HandOverSkillScope(ctx, item.own.ID, string(item.receiver)); err != nil {
				return err
			}
		}
		if forget {
			return tx.ForgetAgent(ctx, string(code))
		}
		return tx.DisableAgent(ctx, string(code))
	})
	if err != nil {
		return DeleteAgentResult{}, err
	}
	return result, nil
}

// ListAgentDefaults 列出应用内置收录的 Agent 的默认设置,按代码排序:名称、技能目录、检测目录与同名规则都是这一版应用内置的值,
// 不是数据库里那一行,用户改过的照样按内置值列出;用户手填添加的、本版应用没有收录的不在里面。编辑 Agent 时给目录输入列候选、
// 「恢复默认」前看哪些 Agent 会改,都用它。全局目录与检测目录按当前用户主目录(选了 WSL 发行版时是发行版里的)写成绝对路径。
// 不检测,Detected 始终为 false。
func (l *Library) ListAgentDefaults(ctx context.Context) (agents []Agent, err error) {
	_, unlock := l.sharedOperation(ctx)
	defer unlock()
	rows := store.Catalog()
	slices.SortFunc(rows, func(a, b store.Agent) int { return strings.Compare(a.Code, b.Code) })
	for _, row := range rows {
		agent := Agent{
			Code: AgentCode(row.Code), Name: row.Name, SameName: SameNameRule(row.SameName),
			GlobalDirectories: []string{}, ProjectDirectories: slices.Clone(row.ProjectPaths), DetectDirectories: []string{},
		}
		for _, path := range row.GlobalPaths {
			agent.GlobalDirectories = append(agent.GlobalDirectories, l.home.Join(path))
		}
		for _, path := range row.DetectPaths {
			agent.DetectDirectories = append(agent.DetectDirectories, l.home.Join(path))
		}
		agents = append(agents, agent)
	}
	return agents, nil
}

// ResetAgentsOptions 是 ResetAgents 的选项,只管移除用户手填添加的 Agent 时只有它在读的技能文件夹,含义同 DeleteAgentOptions:
// Folders 默认(零值或 Remove)删除,显式 Keep 保留;DiscardEdits 是确认时告诉用户被改过、修改会丢掉的文件夹。
type ResetAgentsOptions struct {
	Folders      FolderPolicy `json:"folders"`
	DiscardEdits []string     `json:"discard_edits,omitempty"`
}

// ResetAgentsResult 是 ResetAgents 的结果,各项按代码(技能 ID、作用域)排序。
type ResetAgentsResult struct {
	// Removed 是移除了的用户手填添加的 Agent,含候选池里的。
	Removed []AgentCode `json:"removed"`
	// Reset 是换回了内置值的收录 Agent(已添加的和候选池里的),只列原来和内置值不一样的。
	Reset []AgentCode `json:"reset"`
	// Migrations 是已添加的收录 Agent 去掉了的目录里那几处安装的迁移结果,同 EditAgentResult.Migrations。
	Migrations []AgentMigration `json:"migrations"`
	// Kept 是移除手填添加的 Agent 时因为还有别的已添加 Agent 在读而保留的技能文件夹,同 DeleteAgentResult.Kept。
	Kept []SkillScope `json:"kept"`
}

// ResetAgents 是设置页的「恢复默认」:把 Agent 设置和这一版应用内置的默认同步,免得同一个 Agent 有两个(用户手填的和收录的)。
// 用户在确认框里确认过才调用:应用升级、拉回库时照旧不改写已有的 Agent,也不自动合并手填的 Agent,只有这里一次改写。
//   - 用户手填添加的 Agent(代码以 custom- 开头)一律移除,不判断它和哪个收录的 Agent 重复,也不回到候选池。已添加的按 DeleteAgent 的规则
//     处理安装:还有别的已添加 Agent 在读的文件夹总是保留并转交记录,只有它读的按 opts 删除或保留。先移除它们,再改收录的 Agent,
//     这样手填 Agent 和收录 Agent 共用的文件夹先交给收录的那个,再随它迁移。
//   - 收录的 Agent(已添加的和候选池里的)名称、技能目录、检测目录都换回内置值。已添加的按 EditAgent 选了迁移的规则处理:
//     实际目录不在内置目录里的安装迁移到内置的第 1 个目录。不改哪些 Agent 已添加。
//   - 本版应用没有收录、又不是手填的(从装了更新版本应用的机器同步来的)原样不动。
//
// 逐个 Agent 提交:中途失败时已经处理完的 Agent 保持处理后的样子,其余不动,再调用一次会接着做完;
// 要删的文件夹确认之后才被改过时返回 Conflict(见 DeleteAgentOptions),那个 Agent 和之后的都没动。
func (l *Library) ResetAgents(ctx context.Context, opts ResetAgentsOptions) (result ResetAgentsResult, err error) {
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	const operation = "reset agents"
	if err := validateFolderPolicy(operation, opts.Folders); err != nil {
		return ResetAgentsResult{}, err
	}
	result = ResetAgentsResult{Removed: []AgentCode{}, Reset: []AgentCode{}, Migrations: []AgentMigration{}, Kept: []SkillScope{}}
	rows, err := l.store.ListAgents(ctx)
	if err != nil {
		return ResetAgentsResult{}, err
	}
	for _, row := range rows {
		if !strings.HasPrefix(row.Code, customAgentPrefix) {
			continue
		}
		if row.Enabled {
			deleted, err := l.deleteAgent(ctx, operation, AgentCode(row.Code), DeleteAgentOptions{Folders: opts.Folders, DiscardEdits: opts.DiscardEdits}, true)
			if err != nil {
				return ResetAgentsResult{}, err
			}
			result.Kept = append(result.Kept, deleted.Kept...)
		} else if err := l.store.InTx(ctx, func(tx *store.Tx) error { return tx.ForgetAgent(ctx, row.Code) }); err != nil {
			return ResetAgentsResult{}, err
		}
		result.Removed = append(result.Removed, AgentCode(row.Code))
	}
	rows, err = l.store.ListAgents(ctx)
	if err != nil {
		return ResetAgentsResult{}, err
	}
	for _, defaults := range store.Catalog() {
		i := slices.IndexFunc(rows, func(row store.Agent) bool { return row.Code == defaults.Code })
		if i < 0 || sameAgentSettings(rows[i], defaults) {
			continue
		}
		if rows[i].Enabled {
			edited, err := l.editAgent(ctx, operation, AgentCode(defaults.Code), defaults, EditAgentOptions{Migrate: true})
			if err != nil {
				return ResetAgentsResult{}, err
			}
			result.Migrations = append(result.Migrations, edited.Migrations...)
		} else if err := l.store.InTx(ctx, func(tx *store.Tx) error { return tx.EditAgent(ctx, defaults) }); err != nil {
			return ResetAgentsResult{}, err
		}
		result.Reset = append(result.Reset, AgentCode(defaults.Code))
	}
	slices.Sort(result.Reset)
	slices.SortFunc(result.Migrations, func(a, b AgentMigration) int { return compareSkillInScope(a.SkillID, a.Scope, b.SkillID, b.Scope) })
	slices.SortFunc(result.Kept, func(a, b SkillScope) int { return compareSkillInScope(a.SkillID, a.Scope, b.SkillID, b.Scope) })
	return result, nil
}

const customAgentPrefix = "custom-"

func compareSkillInScope(aID SkillID, a Scope, bID SkillID, b Scope) int {
	return cmp.Or(cmp.Compare(aID, bID), cmp.Compare(a.Agent, b.Agent), cmp.Compare(a.ProjectID, b.ProjectID))
}

func sameAgentSettings(a, b store.Agent) bool {
	return a.Name == b.Name && slices.Equal(a.GlobalPaths, b.GlobalPaths) && slices.Equal(a.ProjectPaths, b.ProjectPaths) &&
		slices.Equal(slices.Sorted(slices.Values(a.DetectPaths)), slices.Sorted(slices.Values(b.DetectPaths)))
}

func (l *Library) groupHeirs(ctx context.Context, agent store.Agent) (map[bool]AgentCode, error) {
	agents, err := l.store.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	heirs := map[bool]AgentCode{}
	for _, project := range []bool{false, true} {
		for _, other := range agents {
			if other.Enabled && other.Code != agent.Code && slices.Equal(other.Paths(project), agent.Paths(project)) {
				heirs[project] = AgentCode(other.Code)
				break
			}
		}
	}
	return heirs, nil
}

type agentInstall struct {
	SkillScope
	place    installedPlace
	own      *store.PlaceSkillScope
	receiver AgentCode
	readers  []AgentCode
}

func (i agentInstall) project() bool {
	return i.place.target.ProjectID != nil
}

func (l *Library) agentInstalls(ctx context.Context, code AgentCode) ([]agentInstall, error) {
	skillScopes, err := l.listSkillScopes(ctx, 0, nil)
	if err != nil {
		return nil, err
	}
	agents, err := l.store.ListAgents(ctx)
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
		place, err := l.installedAt(ctx, skillScope.SkillID, target)
		if err != nil {
			return nil, err
		}
		item := agentInstall{SkillScope: skillScope, place: place}
		for _, row := range place.rows {
			if row.Agent == string(code) {
				item.own = &row
			} else if item.receiver == "" && slices.Contains(skillScope.SharedWith, AgentCode(row.Agent)) {
				item.receiver = AgentCode(row.Agent)
			}
		}
		if item.receiver == "" && len(skillScope.SharedWith) > 0 {
			item.receiver = skillScope.SharedWith[0]
		}
		for _, agent := range agents {
			if agent.Enabled && agent.Code != string(code) && slices.Contains(agent.Paths(item.project()), place.target.relative) {
				item.readers = append(item.readers, AgentCode(agent.Code))
			}
		}
		installs = append(installs, item)
	}
	return installs, nil
}

func (l *Library) agentOf(ctx context.Context, row store.Agent) (Agent, error) {
	agents, err := l.agentsOf(ctx, []store.Agent{row})
	if err != nil {
		return Agent{}, err
	}
	return agents[0], nil
}

func (l *Library) agentsOf(ctx context.Context, rows []store.Agent) ([]Agent, error) {
	agents := make([]Agent, len(rows))
	var detect []string
	var owners []int
	for i, row := range rows {
		agents[i] = Agent{
			Code: AgentCode(row.Code), Name: row.Name, SameName: SameNameRule(row.SameName),
			GlobalDirectories: []string{}, ProjectDirectories: slices.Clone(row.ProjectPaths), DetectDirectories: []string{},
		}
		for _, path := range row.GlobalPaths {
			agents[i].GlobalDirectories = append(agents[i].GlobalDirectories, l.home.Join(path))
		}
		for _, path := range row.DetectPaths {
			agents[i].DetectDirectories = append(agents[i].DetectDirectories, l.home.Join(path))
			detect = append(detect, l.home.Join(path))
			owners = append(owners, i)
		}
	}
	states, err := agentdir.StatFolders(ctx, detect)
	if err != nil {
		return nil, fmt.Errorf("detect agent: %w", err)
	}
	for i, state := range states {
		agents[owners[i]].Detected = agents[owners[i]].Detected || state.Directory
	}
	return agents, nil
}

func (l *Library) agentRow(operation string, agent AgentInput) (store.Agent, error) {
	name := strings.TrimSpace(agent.Name)
	if name == "" {
		return store.Agent{}, argumentError(operation, "name", "name is required")
	}
	globalPaths, err := l.homeDirectories(operation, "global_directories", agent.GlobalDirectories, true)
	if err != nil {
		return store.Agent{}, err
	}
	if len(agent.ProjectDirectories) == 0 {
		return store.Agent{}, argumentError(operation, "project_directories", "at least one project directory is required")
	}
	var projectPaths []string
	for _, directory := range agent.ProjectDirectories {
		if directory == "" {
			return store.Agent{}, argumentError(operation, "project_directories", "project directory is required")
		}
		path, err := agentdir.ProjectRelative(directory)
		switch {
		case errors.Is(err, agentdir.ErrNotRelative):
			return store.Agent{}, argumentError(operation, "project_directories", "project directory must be relative to the project")
		case err != nil:
			return store.Agent{}, argumentError(operation, "project_directories", "project directory must be inside the project")
		}
		projectPaths = append(projectPaths, path)
	}
	if err := distinctDirectories(operation, "project_directories", projectPaths, nil); err != nil {
		return store.Agent{}, err
	}
	detectPaths, err := l.homeDirectories(operation, "detect_directories", agent.DetectDirectories, false)
	if err != nil {
		return store.Agent{}, err
	}
	return store.Agent{Name: name, GlobalPaths: globalPaths, ProjectPaths: projectPaths, DetectPaths: slices.Compact(slices.Sorted(slices.Values(detectPaths)))}, nil
}

func (l *Library) homeDirectories(operation, field string, directories []string, skills bool) ([]string, error) {
	if skills && len(directories) == 0 {
		return nil, argumentError(operation, field, "at least one directory is required")
	}
	paths := []string{}
	for _, directory := range directories {
		if directory == "" {
			return nil, argumentError(operation, field, "directory is required")
		}
		path, err := l.home.Relative(directory)
		switch {
		case errors.Is(err, agentdir.ErrNotAbsolute):
			return nil, argumentError(operation, field, "directory must be an absolute path")
		case err != nil:
			return nil, argumentError(operation, field, "directory must be inside the home directory")
		}
		paths = append(paths, path)
	}
	if !skills {
		return paths, nil
	}
	return paths, distinctDirectories(operation, field, paths, l.home.Join)
}

func distinctDirectories(operation, field string, paths []string, absolute func(string) string) error {
	overlap := func(a, b string) bool {
		if a == b || strings.HasPrefix(b, a+"/") || strings.HasPrefix(a, b+"/") {
			return true
		}
		if absolute == nil {
			return false
		}
		a, b = absolute(a), absolute(b)
		return agentdir.SameFolder(a, b) || agentdir.Inside(a, b) || agentdir.Inside(b, a)
	}
	for i, path := range paths {
		for _, other := range paths[i+1:] {
			if overlap(path, other) {
				return argumentError(operation, field, "directories must not repeat or contain one another")
			}
		}
	}
	return nil
}

func randomBytes(n int) []byte {
	buffer := make([]byte, n)
	_, _ = rand.Read(buffer)
	return buffer
}
