package library_test

import (
	"cmp"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/tiaot33/mechbox/internal/library"
)

// 编辑 Agent 去掉目录并选迁移时,新目录里已经是同一个技能(另一个读那个目录的 Agent 装的):不复制、不搬,算已装在新目录,
// 这个 Agent 照样认得到它;旧文件夹留在原处。以前这种情况记成「没有迁移,不再算已装」,和实际不符。
func TestEditAgentMigrationCountsTheSameSkillAlreadyInTheNewDirectory(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	home := t.TempDir()
	setTestHome(t, home)
	lib := openTestLibrary(t)
	agent := addAgent(t, lib, home, "Agent", ".agent/skills", ".agent/skills")
	skill := fetchNamedSkill(t, lib, "only-here")
	global := library.GlobalScope(agent.Code)
	codex := library.GlobalScope(library.AgentCodex)
	for _, scope := range []library.Scope{codex, global} {
		if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	oldDir := filepath.Join(home, ".agent", "skills")
	newDir := filepath.Join(home, ".agents", "skills")
	result, err := lib.EditAgent(ctx, agent.Code, library.AgentInput{Name: "Agent", GlobalDirectories: []string{newDir}, ProjectDirectories: []string{".agent/skills"}}, library.EditAgentOptions{Migrate: true})
	if err != nil {
		t.Fatal(err)
	}
	want := []library.AgentMigration{{
		SkillID: skill.ID, Scope: global, Outcome: library.MigrationAlreadyInstalled,
		From: filepath.Join(oldDir, "only-here"), To: filepath.Join(newDir, "only-here"),
	}}
	if !reflect.DeepEqual(result.Migrations, want) {
		t.Fatalf("EditAgent() migrations = %#v, want %#v", result.Migrations, want)
	}
	requireInstalledFolder(t, oldDir, "only-here", "# only-here\n")
	requireInstalledFolder(t, newDir, "only-here", "# only-here\n")
	scopes, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{Scope: &global})
	if err != nil || len(scopes) != 1 || scopes[0].Folder != filepath.Join(newDir, "only-here") || !scopes[0].SharedOnly {
		t.Fatalf("ListSkillScopes(agent) = %#v, %v; want the skill in the new directory, shared with codex", scopes, err)
	}
}

// 列出的默认设置是应用内置的值,不随用户改过的那一行变;全局目录按用户主目录写成绝对路径,手填添加的不在里面。
func TestListAgentDefaultsGivesTheBuiltInSettings(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	home := t.TempDir()
	setTestHome(t, home)
	lib := openTestLibrary(t)
	custom := addAgent(t, lib, home, "Mine", ".mine/skills", ".mine/skills")
	if _, err := lib.EditAgent(ctx, library.AgentClaudeCode, library.AgentInput{
		Name: "Claude", GlobalDirectories: []string{filepath.Join(home, ".claude-work", "skills")}, ProjectDirectories: []string{".claude/skills"},
	}, library.EditAgentOptions{}); err != nil {
		t.Fatal(err)
	}
	defaults, err := lib.ListAgentDefaults(ctx)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(defaults, func(a library.Agent) bool { return a.Code == library.AgentClaudeCode })
	want := library.Agent{
		Code: library.AgentClaudeCode, Name: "Claude Code", SameName: library.SameNameGlobalWins,
		GlobalDirectories: []string{filepath.Join(home, ".claude", "skills")}, ProjectDirectories: []string{".claude/skills"},
		DetectDirectories: []string{filepath.Join(home, ".claude")},
	}
	if i < 0 || !reflect.DeepEqual(defaults[i], want) {
		t.Fatalf("ListAgentDefaults() claude code = %#v, want %#v", defaults, want)
	}
	if slices.ContainsFunc(defaults, func(a library.Agent) bool { return a.Code == custom.Code }) {
		t.Fatalf("ListAgentDefaults() lists the agent added by hand: %#v", defaults)
	}
	if !slices.IsSortedFunc(defaults, func(a, b library.Agent) int { return cmp.Compare(a.Code, b.Code) }) {
		t.Fatal("ListAgentDefaults() is not sorted by code")
	}
}

// 恢复默认:手填添加的 Agent 都移除(候选池里的也删掉,不回候选池),只有它读的文件夹按选项保留或删除,
// 还有收录的 Agent 在读的总是保留并转给它;收录的 Agent 换回内置值,已添加的把去掉的目录里的安装迁移到内置的第 1 个目录;
// 候选池里改过的也换回内置值;不改哪些 Agent 已添加。
func TestResetAgentsRemovesHandAddedAgentsAndRestoresTheCatalog(t *testing.T) {
	t.Parallel()
	for _, folders := range []library.FolderPolicy{library.Keep, library.Remove} {
		t.Run(string(folders), func(t *testing.T) {
			ctx := t.Context()
			home := t.TempDir()
			setTestHome(t, home)
			lib := openTestLibrary(t)
			// 手填的 antigravity-cli 读一个只有它读的目录;另一个手填的和 Codex 共用 ~/.agents/skills。
			lonely := addAgent(t, lib, home, "antigravity-cli", ".gemini/antigravity-cli/skills", ".agents/skills")
			sharing := addAgent(t, lib, home, "agents", ".agents/skills", ".agents/skills")
			pooled := addAgent(t, lib, home, "pooled", ".pooled/skills", ".pooled/skills")
			if _, err := lib.DeleteAgent(ctx, pooled.Code, library.DeleteAgentOptions{Folders: library.Keep}); err != nil {
				t.Fatal(err)
			}
			// Cursor 在候选池里,但改过名字。
			if _, err := lib.AddAgentsFromPool(ctx, []library.AgentCode{"cursor"}); err != nil {
				t.Fatal(err)
			}
			if _, err := lib.EditAgent(ctx, "cursor", library.AgentInput{Name: "My Cursor", GlobalDirectories: []string{filepath.Join(home, ".cursor", "skills")}, ProjectDirectories: []string{".cursor/skills"}}, library.EditAgentOptions{}); err != nil {
				t.Fatal(err)
			}
			if _, err := lib.DeleteAgent(ctx, "cursor", library.DeleteAgentOptions{Folders: library.Keep}); err != nil {
				t.Fatal(err)
			}
			// Claude Code 的全局目录被改到别处,那里装着技能。
			workDir := filepath.Join(home, ".claude-work", "skills")
			if _, err := lib.EditAgent(ctx, library.AgentClaudeCode, library.AgentInput{Name: "Claude Code", GlobalDirectories: []string{workDir}, ProjectDirectories: []string{".claude/skills"}, DetectDirectories: []string{filepath.Join(home, ".claude")}}, library.EditAgentOptions{}); err != nil {
				t.Fatal(err)
			}
			alone := fetchNamedSkill(t, lib, "alone")
			shared := fetchNamedSkill(t, lib, "shared")
			work := fetchNamedSkill(t, lib, "work")
			for _, install := range []struct {
				skill library.Skill
				scope library.Scope
			}{{alone, library.GlobalScope(lonely.Code)}, {shared, library.GlobalScope(sharing.Code)}, {work, library.GlobalScope(library.AgentClaudeCode)}} {
				if err := lib.Install(ctx, install.skill.ID, install.scope, library.InstallOptions{}); err != nil {
					t.Fatal(err)
				}
			}

			plan, err := lib.PlanResetAgents(ctx)
			if err != nil {
				t.Fatal(err)
			}
			codesOf := func(items []library.AgentReset) []library.AgentCode {
				var codes []library.AgentCode
				for _, item := range items {
					codes = append(codes, item.Agent.Code)
				}
				return codes
			}
			if !sameCodes(codesOf(plan.Removed), []library.AgentCode{lonely.Code, sharing.Code, pooled.Code}) || !slices.Equal(codesOf(plan.Reset), []library.AgentCode{library.AgentClaudeCode, "cursor"}) {
				t.Fatalf("PlanResetAgents() removed %v, reset %v", codesOf(plan.Removed), codesOf(plan.Reset))
			}
			if len(plan.Migrations) != 1 || plan.Migrations[0].Place.SkillID != work.ID || plan.Migrations[0].To != filepath.Join(home, ".claude", "skills", "work") || !plan.Migrations[0].Leaves {
				t.Fatalf("PlanResetAgents() migrations = %#v", plan.Migrations)
			}
			if len(plan.Kept) != 1 || plan.Kept[0].SkillID != shared.ID || len(plan.Removable) != 1 || plan.Removable[0].SkillID != alone.ID {
				t.Fatalf("PlanResetAgents() kept %#v, removable %#v", plan.Kept, plan.Removable)
			}
			result, err := lib.ResetAgents(ctx, library.ResetAgentsOptions{Folders: folders})
			if err != nil {
				t.Fatal(err)
			}
			claudeDir := filepath.Join(home, ".claude", "skills")
			agentsDir := filepath.Join(home, ".agents", "skills")
			if !sameCodes(result.Removed, []library.AgentCode{lonely.Code, sharing.Code, pooled.Code}) {
				t.Fatalf("ResetAgents() removed %v", result.Removed)
			}
			if !slices.Equal(result.Reset, []library.AgentCode{library.AgentClaudeCode, "cursor"}) {
				t.Fatalf("ResetAgents() reset %v, want claude-code and cursor", result.Reset)
			}
			wantMigrations := []library.AgentMigration{{
				SkillID: work.ID, Scope: library.GlobalScope(library.AgentClaudeCode), Outcome: library.MigrationMoved,
				From: filepath.Join(workDir, "work"), To: filepath.Join(claudeDir, "work"),
			}}
			if !reflect.DeepEqual(result.Migrations, wantMigrations) {
				t.Fatalf("ResetAgents() migrations = %#v, want %#v", result.Migrations, wantMigrations)
			}
			if len(result.Kept) != 1 || result.Kept[0].SkillID != shared.ID || !slices.Equal(result.Kept[0].SharedWith, []library.AgentCode{library.AgentCodex}) {
				t.Fatalf("ResetAgents() kept = %#v, want the shared folder still read by codex", result.Kept)
			}

			added := listAgents(t, lib)
			if codes := agentCodes(added); !slices.Equal(codes, []library.AgentCode{library.AgentClaudeCode, library.AgentCodex}) {
				t.Fatalf("ListAgents() after reset = %v, want claude-code and codex", codes)
			}
			if added[0].Name != "Claude Code" || !slices.Equal(added[0].GlobalDirectories, []string{claudeDir}) {
				t.Fatalf("claude code after reset = %#v", added[0])
			}
			pool, err := lib.ListAgentPool(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, agent := range pool {
				if agent.Code == lonely.Code || agent.Code == sharing.Code || agent.Code == pooled.Code {
					t.Fatalf("hand-added agent %s went back to the pool", agent.Code)
				}
				if agent.Code == "cursor" && (agent.Name != "Cursor" || !slices.Equal(agent.GlobalDirectories, []string{agentsDir, filepath.Join(home, ".cursor", "skills")})) {
					t.Fatalf("cursor in the pool after reset = %#v", agent)
				}
			}
			lonelyDir := filepath.Join(home, ".gemini", "antigravity-cli", "skills")
			if folders == library.Keep {
				requireInstalledFolder(t, lonelyDir, "alone", "# alone\n")
			} else {
				requireNoInstalledFolder(t, lonelyDir, "alone")
			}
			requireNoInstalledFolder(t, workDir, "work")
			requireInstalledFolder(t, claudeDir, "work", "# work\n")
			requireSkillScopes(t, lib, []library.SkillScope{
				{SkillID: shared.ID, Scope: library.GlobalScope(library.AgentCodex), Folder: filepath.Join(agentsDir, "shared"), Direct: true},
				{SkillID: work.ID, Scope: library.GlobalScope(library.AgentClaudeCode), Folder: filepath.Join(claudeDir, "work"), Direct: true},
			})

			again, err := lib.ResetAgents(ctx, library.ResetAgentsOptions{Folders: folders})
			if err != nil || len(again.Removed)+len(again.Reset)+len(again.Migrations)+len(again.Kept) != 0 {
				t.Fatalf("second ResetAgents() = %#v, %v; want nothing left to do", again, err)
			}
		})
	}
}

func sameCodes(a, b []library.AgentCode) bool {
	return slices.Equal(slices.Sorted(slices.Values(a)), slices.Sorted(slices.Values(b)))
}

// 恢复默认把要移除的手填 Agent 当一批算:两个手填 Agent 共用、没有别的 Agent 在读的文件夹,计划里归入选了删除时会删的,
// 不报成保留;执行按同一套规则,选保留时留着,选删除时删掉。以前逐个移除,前一个把文件夹转给后一个,确认框写着保留,实际却删了。
func TestResetAgentsTreatsHandAddedAgentsAsOneBatch(t *testing.T) {
	t.Parallel()
	for _, folders := range []library.FolderPolicy{library.Keep, library.Remove} {
		t.Run(string(folders), func(t *testing.T) {
			ctx := t.Context()
			home := t.TempDir()
			setTestHome(t, home)
			lib := openTestLibrary(t)
			first := addAgent(t, lib, home, "first", ".shared/skills", ".first/skills")
			addAgent(t, lib, home, "second", ".shared/skills", ".second/skills")
			skill := fetchNamedSkill(t, lib, "both")
			if err := lib.Install(ctx, skill.ID, library.GlobalScope(first.Code), library.InstallOptions{}); err != nil {
				t.Fatal(err)
			}
			plan, err := lib.PlanResetAgents(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Kept) != 0 || len(plan.Removable) != 1 || plan.Removable[0].SkillID != skill.ID {
				t.Fatalf("PlanResetAgents() kept %#v, removable %#v; want the shared folder removable", plan.Kept, plan.Removable)
			}
			result, err := lib.ResetAgents(ctx, library.ResetAgentsOptions{Folders: folders})
			if err != nil || len(result.Kept) != 0 {
				t.Fatalf("ResetAgents() = %#v, %v; want nothing reported as kept", result, err)
			}
			shared := filepath.Join(home, ".shared", "skills")
			if folders == library.Keep {
				requireInstalledFolder(t, shared, "both", "# both\n")
			} else {
				requireNoInstalledFolder(t, shared, "both")
			}
		})
	}
}

// 恢复默认时有一个要删的文件夹确认之后才被改过:整次返回 Conflict,哪个手填 Agent 都不移除,文件夹都不删。
// 以前逐个移除,前面的 Agent 已经移除、文件夹已经删了,界面却像什么都没做。
func TestResetAgentsConflictLeavesEveryAgentInPlace(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	home := t.TempDir()
	setTestHome(t, home)
	lib := openTestLibrary(t)
	first := addAgent(t, lib, home, "first", ".first/skills", ".first/skills")
	second := addAgent(t, lib, home, "second", ".second/skills", ".second/skills")
	one := fetchNamedSkill(t, lib, "one")
	two := fetchNamedSkill(t, lib, "two")
	for _, install := range []struct {
		skill library.Skill
		agent library.Agent
	}{{one, first}, {two, second}} {
		if err := lib.Install(ctx, install.skill.ID, library.GlobalScope(install.agent.Code), library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	edited := filepath.Join(home, ".second", "skills", "two")
	writeFile(t, filepath.Join(edited, "notes.md"), []byte("edited\n"), 0o644)
	_, err := lib.ResetAgents(ctx, library.ResetAgentsOptions{Folders: library.Remove})
	requireErrorDetails(t, err, library.Conflict, library.ErrorDetails{Paths: []string{edited}})
	if codes := agentCodes(listAgents(t, lib)); !slices.Contains(codes, first.Code) || !slices.Contains(codes, second.Code) {
		t.Fatalf("ListAgents() after conflict = %v; want both hand-added agents still added", codes)
	}
	requireInstalledFolder(t, filepath.Join(home, ".first", "skills"), "one", "# one\n")
	if _, err := lib.ResetAgents(ctx, library.ResetAgentsOptions{Folders: library.Remove, DiscardEdits: []string{edited}}); err != nil {
		t.Fatal(err)
	}
	requireNoInstalledFolder(t, filepath.Join(home, ".first", "skills"), "one")
	requireNoInstalledFolder(t, filepath.Join(home, ".second", "skills"), "two")
}
