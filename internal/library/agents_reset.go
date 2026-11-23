package library

import (
	"cmp"
	"context"
	"slices"
	"strings"

	"github.com/tiaot33/mechbox/internal/library/internal/agentdir"
	"github.com/tiaot33/mechbox/internal/library/internal/store"
)

// ListAgentDefaults 列出应用内置收录的 Agent 的默认设置,按代码排序:名称、技能目录、检测目录与同名规则都是这一版应用内置的值,
// 不是数据库里那一行,用户改过的照样按内置值列出;用户手填添加的、本版应用没有收录的不在里面。编辑 Agent 时给目录输入列候选用它。
// 全局目录与检测目录按当前用户主目录(选了 WSL 发行版时是发行版里的)写成绝对路径。不检测,Detected 始终为 false。
func (l *Library) ListAgentDefaults(ctx context.Context) (agents []Agent, err error) {
	_, unlock := l.sharedOperation(ctx)
	defer unlock()
	rows := store.Catalog()
	slices.SortFunc(rows, func(a, b store.Agent) int { return strings.Compare(a.Code, b.Code) })
	for _, row := range rows {
		agents = append(agents, l.agentFromRow(row))
	}
	return agents, nil
}

// AgentReset 是「恢复默认」里的一个 Agent:Agent 是它现在的样子,Added 表示已添加(否则在候选池里);
// Defaults 是要换回的内置值,要移除的手填 Agent 没有。
type AgentReset struct {
	Agent    Agent  `json:"agent"`
	Defaults *Agent `json:"defaults,omitempty"`
	Added    bool   `json:"added"`
}

// PlannedMigration 是「恢复默认」会迁移的一处安装:已添加的收录 Agent 的这一处所在的目录不在内置目录里,迁移到 To(新的技能文件夹)。
// Leaves 表示恢复默认之后旧文件夹不再有已添加的 Agent 在读,会被搬走;否则复制一份过去,旧文件夹留给还在读的 Agent。
type PlannedMigration struct {
	Place  SkillScope `json:"place"`
	To     string     `json:"to"`
	Leaves bool       `json:"leaves"`
}

// ResetAgentsPlan 是 [Library.PlanResetAgents] 给出的「恢复默认」会做的事,和 [Library.ResetAgents] 按同一套规则算。
// 要移除的手填 Agent 当一批算:还在读的 Agent 只算这一批以外的已添加 Agent。
type ResetAgentsPlan struct {
	// Reset 是会换回内置值的收录 Agent(已添加的和候选池里的),只列和内置值不一样的,按代码排序。
	Reset []AgentReset `json:"reset"`
	// Removed 是会移除的手填 Agent(已添加的和候选池里的),按代码排序;它们不回候选池。
	Removed []AgentReset `json:"removed"`
	// Migrations 是已添加的收录 Agent 去掉的目录里会迁移的各处,按技能 ID、作用域排序。
	Migrations []PlannedMigration `json:"migrations"`
	// Kept 是移除手填 Agent 时还有这一批以外的已添加 Agent 在读、不管怎么选都保留的技能文件夹,SharedWith 是在读的 Agent。
	Kept []SkillScope `json:"kept"`
	// Removable 是只有这一批手填 Agent 在读、为它们装的技能文件夹:ResetAgentsOptions.Folders 选删除时删掉,一个文件夹只列一处。
	Removable []SkillScope `json:"removable"`
}

// ResetAgentsOptions 是 ResetAgents 的选项,只管 ResetAgentsPlan.Removable 里的文件夹,含义同 DeleteAgentOptions:
// Folders 默认(零值或 Remove)删除,显式 Keep 保留;DiscardEdits 是确认时告诉用户被改过、修改会丢掉的文件夹,
// DeleteLinks 是确认时告诉用户会一并删掉的旧链接。
type ResetAgentsOptions struct {
	Folders      FolderPolicy `json:"folders"`
	DiscardEdits []string     `json:"discard_edits,omitempty"`
	DeleteLinks  []string     `json:"delete_links,omitempty"`
}

// ResetAgentsResult 是 ResetAgents 的结果,各项按代码(技能 ID、作用域)排序。
type ResetAgentsResult struct {
	// Removed 是移除了的用户手填添加的 Agent,含候选池里的。
	Removed []AgentCode `json:"removed"`
	// Reset 是换回了内置值的收录 Agent(已添加的和候选池里的),只列原来和内置值不一样的。
	Reset []AgentCode `json:"reset"`
	// Migrations 是已添加的收录 Agent 去掉了的目录里那几处安装的迁移结果,同 EditAgentResult.Migrations。
	Migrations []AgentMigration `json:"migrations"`
	// Kept 同 ResetAgentsPlan.Kept。
	Kept []SkillScope `json:"kept"`
}

// PlanResetAgents 给出「恢复默认」会做的事,只读:确认框据此写出会改哪些 Agent、移除哪些手填 Agent、哪些技能要迁移、
// 哪些文件夹保留、哪些在选了删除时删掉。规则见 [Library.ResetAgents]。
func (l *Library) PlanResetAgents(ctx context.Context) (plan ResetAgentsPlan, err error) {
	ctx, unlock := l.sharedOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	rows, err := l.store.ListAgents(ctx)
	if err != nil {
		return ResetAgentsPlan{}, err
	}
	batch := l.resetBatch(rows)
	plan = ResetAgentsPlan{Reset: []AgentReset{}, Removed: []AgentReset{}, Migrations: []PlannedMigration{}, Removable: []SkillScope{}}
	agents, err := l.agentsOf(ctx, rows)
	if err != nil {
		return ResetAgentsPlan{}, err
	}
	for i, row := range rows {
		if handAdded(row) {
			plan.Removed = append(plan.Removed, AgentReset{Agent: agents[i], Added: row.Enabled})
		} else if defaults, ok := batch.defaults[row.Code]; ok && !sameAgentSettings(row, defaults) {
			shown := l.agentFromRow(defaults)
			plan.Reset = append(plan.Reset, AgentReset{Agent: agents[i], Defaults: &shown, Added: row.Enabled})
		}
	}
	removal, err := l.planAgentRemoval(ctx, batch.removed)
	if err != nil {
		return ResetAgentsPlan{}, err
	}
	plan.Kept = removal.kept
	for _, item := range removal.owned {
		plan.Removable = append(plan.Removable, item.SkillScope)
	}
	final := batch.final(rows)
	for _, code := range batch.edited {
		defaults := batch.defaults[string(code)]
		others := slices.DeleteFunc(slices.Clone(final), func(row store.Agent) bool { return row.Code == string(code) })
		installs, err := l.agentInstalls(ctx, code, others)
		if err != nil {
			return ResetAgentsPlan{}, err
		}
		for _, item := range installs {
			if _, ok := keptDirectory(item.place.target.sameDirectory, defaults.Paths(item.project()), item.place.target.relative); ok {
				continue
			}
			to := agentdir.FolderPath(migrationDestination(item, defaults).directory, agentdir.FolderName(item.Folder))
			plan.Migrations = append(plan.Migrations, PlannedMigration{Place: item.SkillScope, To: to, Leaves: len(item.readers) == 0})
		}
	}
	slices.SortFunc(plan.Migrations, func(a, b PlannedMigration) int {
		return compareSkillInScope(a.Place.SkillID, a.Place.Scope, b.Place.SkillID, b.Place.Scope)
	})
	return plan, nil
}

// ResetAgents 是设置页的「恢复默认」:把 Agent 设置和这一版应用内置的默认同步,免得同一个 Agent 有两个(用户手填的和收录的)。
// 用户在确认框里看过 [Library.PlanResetAgents] 的计划、确认过才调用:应用升级、拉回库时照旧不改写已有的 Agent,
// 也不自动合并手填的 Agent,只有这里一次改写。
//   - 用户手填添加的 Agent(代码以 custom- 开头)一律移除,不判断它和哪个收录的 Agent 重复,也不回到候选池。已添加的当一批移除:
//     还有这一批以外的已添加 Agent 在读的文件夹总是保留并转交记录,只有这一批在读的按 opts 删除或保留;
//     要删的文件夹确认之后才被改过时返回 Conflict(见 DeleteAgentOptions),什么都不改。
//     先移除它们,再改收录的 Agent,这样手填 Agent 和收录 Agent 共用的文件夹先交给收录的那个,再随它迁移。
//   - 收录的 Agent(已添加的和候选池里的)名称、技能目录、检测目录都换回内置值。已添加的按 EditAgent 选了迁移的规则处理:
//     实际目录不在内置目录里的安装迁移到内置的第 1 个目录。不改哪些 Agent 已添加。
//   - 本版应用没有收录、又不是手填的(从装了更新版本应用的机器同步来的)原样不动。
//
// 移除手填 Agent 是一次提交,之后逐个收录 Agent 提交:改某个收录 Agent 失败时,手填 Agent 已经移除、前面的收录 Agent 已经换回,
// 其余不动,再调用一次会接着做完。
func (l *Library) ResetAgents(ctx context.Context, opts ResetAgentsOptions) (result ResetAgentsResult, err error) {
	ctx, unlock := l.exclusiveOperation(ctx)
	defer unlock()
	defer func() { err = typedError(err) }()
	const operation = "reset agents"
	if err := validateFolderPolicy(operation, opts.Folders); err != nil {
		return ResetAgentsResult{}, err
	}
	rows, err := l.store.ListAgents(ctx)
	if err != nil {
		return ResetAgentsResult{}, err
	}
	batch := l.resetBatch(rows)
	result = ResetAgentsResult{Removed: []AgentCode{}, Reset: []AgentCode{}, Migrations: []AgentMigration{}, Kept: []SkillScope{}}
	pooled := []string{}
	for _, row := range rows {
		if handAdded(row) {
			result.Removed = append(result.Removed, AgentCode(row.Code))
			if !row.Enabled {
				pooled = append(pooled, row.Code)
			}
		}
	}
	deleted, err := l.deleteAgents(ctx, operation, batch.removed, DeleteAgentOptions{Folders: opts.Folders, DiscardEdits: opts.DiscardEdits, DeleteLinks: opts.DeleteLinks}, pooled)
	if err != nil {
		return ResetAgentsResult{}, err
	}
	result.Kept = deleted.Kept
	err = l.store.InTx(ctx, func(tx *store.Tx) error {
		for _, code := range batch.pooled {
			if err := tx.EditAgent(ctx, batch.defaults[string(code)]); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return ResetAgentsResult{}, err
	}
	for _, code := range batch.edited {
		edited, err := l.editAgent(ctx, operation, code, batch.defaults[string(code)], EditAgentOptions{Migrate: true}, false)
		if err != nil {
			return ResetAgentsResult{}, err
		}
		result.Migrations = append(result.Migrations, edited.Migrations...)
	}
	result.Reset = append(append(result.Reset, batch.edited...), batch.pooled...)
	slices.Sort(result.Reset)
	slices.SortFunc(result.Migrations, func(a, b AgentMigration) int { return compareSkillInScope(a.SkillID, a.Scope, b.SkillID, b.Scope) })
	return result, nil
}

// resetBatch 是恢复默认要动的 Agent:removed 是已添加的手填 Agent(当一批移除),edited 是和内置值不一样的已添加收录 Agent,
// pooled 是和内置值不一样的候选池里的收录 Agent,defaults 是各收录 Agent 的内置值。
type resetBatch struct {
	removed  []AgentCode
	edited   []AgentCode
	pooled   []AgentCode
	defaults map[string]store.Agent
}

func (l *Library) resetBatch(rows []store.Agent) resetBatch {
	batch := resetBatch{defaults: map[string]store.Agent{}}
	for _, row := range store.Catalog() {
		batch.defaults[row.Code] = row
	}
	for _, row := range rows {
		defaults, ok := batch.defaults[row.Code]
		switch {
		case handAdded(row):
			if row.Enabled {
				batch.removed = append(batch.removed, AgentCode(row.Code))
			}
		case !ok || sameAgentSettings(row, defaults):
		case row.Enabled:
			batch.edited = append(batch.edited, AgentCode(row.Code))
		default:
			batch.pooled = append(batch.pooled, AgentCode(row.Code))
		}
	}
	return batch
}

// final 是恢复默认之后已添加的 Agent:手填的移除了,收录的换成内置值。
func (b resetBatch) final(rows []store.Agent) []store.Agent {
	var final []store.Agent
	for _, row := range rows {
		if !row.Enabled || handAdded(row) {
			continue
		}
		if defaults, ok := b.defaults[row.Code]; ok {
			defaults.Enabled = true
			row = defaults
		}
		final = append(final, row)
	}
	return final
}

const customAgentPrefix = "custom-"

func handAdded(row store.Agent) bool {
	return strings.HasPrefix(row.Code, customAgentPrefix)
}

func compareSkillInScope(aID SkillID, a Scope, bID SkillID, b Scope) int {
	return cmp.Or(cmp.Compare(aID, bID), cmp.Compare(a.Agent, b.Agent), cmp.Compare(a.ProjectID, b.ProjectID))
}

func sameAgentSettings(a, b store.Agent) bool {
	return a.Name == b.Name && slices.Equal(a.GlobalPaths, b.GlobalPaths) && slices.Equal(a.ProjectPaths, b.ProjectPaths) &&
		slices.Equal(slices.Sorted(slices.Values(a.DetectPaths)), slices.Sorted(slices.Values(b.DetectPaths)))
}
