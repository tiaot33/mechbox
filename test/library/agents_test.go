package library_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/tiaot33/skills-tui/internal/library"
)

// addAgent 添加一个自定义 Agent,全局目录在 home 下的 global,项目目录为 project。
func addAgent(t *testing.T, lib *library.Library, home, name, global, project string) library.Agent {
	t.Helper()
	agent, err := lib.AddAgent(t.Context(), library.CustomAgent{Name: name, GlobalDirectory: filepath.Join(home, global), ProjectDirectory: project})
	if err != nil {
		t.Fatal(err)
	}
	return agent
}

func listAgents(t *testing.T, lib *library.Library) []library.Agent {
	t.Helper()
	agents, err := lib.ListAgents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return agents
}

// 内置 Agent 按主目录下它的配置目录是否存在报出是否检测到,列出时现查。
func TestListAgentsReportsWhetherBuiltinAgentsAreDetected(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	if got := listAgents(t, lib); !reflect.DeepEqual(got, seededAgents(home)) {
		t.Fatalf("ListAgents() = %#v, want %#v", got, seededAgents(home))
	}
	if err := os.Mkdir(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	want := seededAgents(home)
	want[0].Detected = true
	if got := listAgents(t, lib); !reflect.DeepEqual(got, want) {
		t.Fatalf("ListAgents() with ~/.claude = %#v, want %#v", got, want)
	}
	if err := os.Mkdir(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	want[1].Detected = true
	if got := listAgents(t, lib); !reflect.DeepEqual(got, want) {
		t.Fatalf("ListAgents() with ~/.codex = %#v, want %#v", got, want)
	}
}

// 添加的自定义 Agent 和内置 Agent 一样能装到全局和项目;重开库时写入内置 Agent 不动它。
func TestCustomAgentInstallsLikeBuiltinAgents(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(t.TempDir(), "library.db")
	lib := openTestLibraryAt(t, path)
	agent, err := lib.AddAgent(ctx, library.CustomAgent{Name: "  My Agent ", GlobalDirectory: "~/.my-agent/skills", ProjectDirectory: "./.my-agent/skills/"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(agent.Code), "custom-") {
		t.Fatalf("AddAgent code = %q, want custom- prefix", agent.Code)
	}
	want := library.Agent{
		Code: agent.Code, Name: "My Agent", Custom: true,
		GlobalDirectory: filepath.Join(home, ".my-agent", "skills"), ProjectDirectory: ".my-agent/skills",
	}
	if agent != want {
		t.Fatalf("AddAgent() = %#v, want %#v", agent, want)
	}
	if got := listAgents(t, lib); !slices.Contains(got, want) || len(got) != 3 {
		t.Fatalf("ListAgents() = %#v, want built-in agents and %#v", got, want)
	}
	skill := fetchNamedSkill(t, lib, "example")
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	scopes := []library.Scope{library.GlobalScope(agent.Code), library.ProjectScope(agent.Code, project.ID)}
	for _, scope := range scopes {
		if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	globalDir := filepath.Join(home, ".my-agent", "skills")
	projectDir := filepath.Join(project.Path, ".my-agent", "skills")
	requireInstalledFolder(t, globalDir, "example", "# example\n")
	requireInstalledFolder(t, projectDir, "example", "# example\n")
	wantScopes := []library.SkillScope{
		{SkillID: skill.ID, Scope: scopes[0], Folder: filepath.Join(globalDir, "example"), Direct: true},
		{SkillID: skill.ID, Scope: scopes[1], Folder: filepath.Join(projectDir, "example"), Direct: true},
	}
	requireSkillScopes(t, lib, wantScopes)

	if err := lib.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openTestLibraryAt(t, path)
	if got := listAgents(t, reopened); !slices.Contains(got, want) {
		t.Fatalf("ListAgents() after reopen = %#v, want %#v kept", got, want)
	}
	requireSkillScopes(t, reopened, wantScopes)
}

// 填写不合法时返回 InvalidArgument 并指向字段,什么都不添加;全局目录必须在主目录里面。
func TestAddAgentRejectsInvalidSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	valid := library.CustomAgent{Name: "Agent", GlobalDirectory: filepath.Join(home, ".agent", "skills"), ProjectDirectory: ".agent/skills"}
	cases := map[string]struct {
		edit  func(*library.CustomAgent)
		field string
	}{
		"empty name":              {func(a *library.CustomAgent) { a.Name = "  " }, "name"},
		"global outside home":     {func(a *library.CustomAgent) { a.GlobalDirectory = filepath.Join(t.TempDir(), "skills") }, "global_directory"},
		"global escapes home":     {func(a *library.CustomAgent) { a.GlobalDirectory = filepath.Join(home, "..", "skills") }, "global_directory"},
		"global is home":          {func(a *library.CustomAgent) { a.GlobalDirectory = home }, "global_directory"},
		"global is relative":      {func(a *library.CustomAgent) { a.GlobalDirectory = ".agent/skills" }, "global_directory"},
		"global missing":          {func(a *library.CustomAgent) { a.GlobalDirectory = "" }, "global_directory"},
		"project missing":         {func(a *library.CustomAgent) { a.ProjectDirectory = "" }, "project_directory"},
		"project is absolute":     {func(a *library.CustomAgent) { a.ProjectDirectory = filepath.Join(home, "skills") }, "project_directory"},
		"project escapes project": {func(a *library.CustomAgent) { a.ProjectDirectory = "../skills" }, "project_directory"},
		"project is project root": {func(a *library.CustomAgent) { a.ProjectDirectory = "." }, "project_directory"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			agent := valid
			tc.edit(&agent)
			_, err := lib.AddAgent(t.Context(), agent)
			requireErrorDetails(t, err, library.InvalidArgument, library.ErrorDetails{Field: tc.field})
		})
	}
	if got := listAgents(t, lib); !reflect.DeepEqual(got, seededAgents(home)) {
		t.Fatalf("ListAgents() = %#v, want only built-in agents", got)
	}
}

// 内置 Agent 不能改也不能删;未知代码返回 NotFound。
func TestBuiltinAgentsCannotBeEditedOrDeleted(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	settings := library.CustomAgent{Name: "Codex", GlobalDirectory: filepath.Join(home, ".other", "skills"), ProjectDirectory: ".other/skills"}
	_, err := lib.EditAgent(ctx, library.AgentCodex, settings, library.EditAgentOptions{})
	requireLibraryError(t, err, library.FailedPrecondition)
	_, err = lib.DeleteAgent(ctx, library.AgentCodex, library.DeleteAgentOptions{})
	requireLibraryError(t, err, library.FailedPrecondition)
	_, err = lib.EditAgent(ctx, "custom-missing", settings, library.EditAgentOptions{})
	requireLibraryError(t, err, library.NotFound)
	_, err = lib.DeleteAgent(ctx, "custom-missing", library.DeleteAgentOptions{})
	requireLibraryError(t, err, library.NotFound)
	if got := listAgents(t, lib); !reflect.DeepEqual(got, seededAgents(home)) {
		t.Fatalf("ListAgents() = %#v, want built-in agents unchanged", got)
	}
}

// 只改名称时代码不变,安装记录和文件夹都不动。
func TestEditAgentRenameKeepsInstalls(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	agent := addAgent(t, lib, home, "Old", ".agent/skills", ".agent/skills")
	skill := fetchNamedSkill(t, lib, "example")
	scope := library.GlobalScope(agent.Code)
	if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	result, err := lib.EditAgent(ctx, agent.Code, library.CustomAgent{Name: "New", GlobalDirectory: agent.GlobalDirectory, ProjectDirectory: agent.ProjectDirectory}, library.EditAgentOptions{Migrate: true})
	agent.Name = "New"
	if err != nil || result.Agent != agent || len(result.Migrations) != 0 {
		t.Fatalf("EditAgent() = %#v, %v; want renamed %#v and no migrations", result, err, agent)
	}
	requireSkillScopes(t, lib, []library.SkillScope{{SkillID: skill.ID, Scope: scope, Folder: filepath.Join(agent.GlobalDirectory, "example"), Direct: true}})
	requireInstalledFolder(t, agent.GlobalDirectory, "example", "# example\n")
}

// 改目录并迁移:已装的文件夹原样搬到新目录(保留用户改过的内容),记录保留;新目录里已有同名文件夹的不覆盖、
// 不搬,旧文件夹留在原处,去掉这一处的安装记录并在结果里报出。没变的层级不受影响。
func TestEditAgentMigratesFoldersAndReportsConflicts(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	agent := addAgent(t, lib, home, "Agent", ".old/skills", ".agent/skills")
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	moved := fetchNamedSkill(t, lib, "moved")
	blocked := fetchNamedSkill(t, lib, "blocked")
	global := library.GlobalScope(agent.Code)
	projectScope := library.ProjectScope(agent.Code, project.ID)
	for _, install := range []struct {
		skill library.Skill
		scope library.Scope
	}{{moved, global}, {blocked, global}, {moved, projectScope}} {
		if err := lib.Install(ctx, install.skill.ID, install.scope, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	oldDir := filepath.Join(home, ".old", "skills")
	newDir := filepath.Join(home, ".new", "skills")
	edited := []byte("edited by the user\n")
	writeFile(t, filepath.Join(oldDir, "moved", "notes.md"), edited, 0o644)
	writeFile(t, filepath.Join(newDir, "blocked", "SKILL.md"), []byte("not from the library\n"), 0o644)

	result, err := lib.EditAgent(ctx, agent.Code, library.CustomAgent{Name: "Agent", GlobalDirectory: newDir, ProjectDirectory: ".agent/skills"}, library.EditAgentOptions{Migrate: true})
	if err != nil {
		t.Fatal(err)
	}
	wantMigrations := []library.AgentMigration{
		{SkillID: moved.ID, Scope: global, Outcome: library.MigrationMoved, From: filepath.Join(oldDir, "moved"), To: filepath.Join(newDir, "moved")},
		{SkillID: blocked.ID, Scope: global, Outcome: library.MigrationSkipped, From: filepath.Join(oldDir, "blocked"), To: filepath.Join(newDir, "blocked")},
	}
	if result.Agent.GlobalDirectory != newDir || !reflect.DeepEqual(result.Migrations, wantMigrations) {
		t.Fatalf("EditAgent() = %#v; want global %q and migrations %#v", result, newDir, wantMigrations)
	}
	requireNoInstalledFolder(t, oldDir, "moved")
	requireInstalledFolder(t, newDir, "moved", "# moved\n")
	if content, err := os.ReadFile(filepath.Join(newDir, "moved", "notes.md")); err != nil || string(content) != string(edited) {
		t.Fatalf("moved folder lost user edits: %q, %v", content, err)
	}
	requireInstalledFolder(t, oldDir, "blocked", "# blocked\n")
	requireSkillFile(t, newDir, "blocked", "not from the library\n")
	projectDir := filepath.Join(project.Path, ".agent", "skills")
	requireSkillScopes(t, lib, []library.SkillScope{
		{SkillID: moved.ID, Scope: global, Folder: filepath.Join(newDir, "moved"), Direct: true},
		{SkillID: moved.ID, Scope: projectScope, Folder: filepath.Join(projectDir, "moved"), Direct: true},
	})
	requireInstalledFolder(t, projectDir, "moved", "# moved\n")
	inspections, err := lib.InspectSkillScopes(ctx, library.SkillScopeFilter{Scope: &global})
	if err != nil || len(inspections) != 1 || inspections[0].Status != library.SkillScopeModified {
		t.Fatalf("InspectSkillScopes(global) = %#v, %v; want the moved folder with its user edits", inspections, err)
	}
}

// 改目录不迁移:只改目录,旧目录里的文件夹不动,去掉这个 Agent 在该层级的直接安装与组安装;没变的层级不受影响。
func TestEditAgentWithoutMigrationDropsInstallsOfChangedLevel(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	agent := addAgent(t, lib, home, "Agent", ".old/skills", ".agent/skills")
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	skill := fetchNamedSkill(t, lib, "example")
	member := fetchNamedSkill(t, lib, "member")
	group, err := lib.CreateGroup(ctx, "g")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.AddSkillToGroup(ctx, group.ID, member.ID); err != nil {
		t.Fatal(err)
	}
	global := library.GlobalScope(agent.Code)
	projectScope := library.ProjectScope(agent.Code, project.ID)
	for _, scope := range []library.Scope{global, projectScope} {
		if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := lib.InstallGroup(ctx, group.ID, global, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	oldDir := filepath.Join(home, ".old", "skills")
	newDir := filepath.Join(home, ".new", "skills")
	result, err := lib.EditAgent(ctx, agent.Code, library.CustomAgent{Name: "Agent", GlobalDirectory: newDir, ProjectDirectory: ".agent/skills"}, library.EditAgentOptions{})
	if err != nil || len(result.Migrations) != 0 {
		t.Fatalf("EditAgent() = %#v, %v; want no migrations", result, err)
	}
	requireInstalledFolder(t, oldDir, "example", "# example\n")
	requireInstalledFolder(t, oldDir, "member", "# member\n")
	requireNoInstalledFolder(t, newDir, "example")
	projectDir := filepath.Join(project.Path, ".agent", "skills")
	requireSkillScopes(t, lib, []library.SkillScope{{SkillID: skill.ID, Scope: projectScope, Folder: filepath.Join(projectDir, "example"), Direct: true}})
	installs, err := lib.ListGroupInstalls(ctx)
	if err != nil || len(installs) != 0 {
		t.Fatalf("ListGroupInstalls() = %#v, %v; want the global group install dropped", installs, err)
	}
	plan, err := lib.PlanInstall(ctx, skill.ID, global)
	if err != nil || plan.Status != library.InstallReady || plan.Folder != filepath.Join(newDir, "example") {
		t.Fatalf("PlanInstall(global) = %#v, %v; want ready in the new directory", plan, err)
	}
}

// 删除自定义 Agent 和注销项目一样:默认连同文件夹删除,Keep 时文件夹留着,库不再管;记录都撤掉。
func TestDeleteAgentRemovesOrKeepsFolders(t *testing.T) {
	for _, policy := range []library.FolderPolicy{"", library.Remove, library.Keep} {
		t.Run(string(policy), func(t *testing.T) {
			ctx := t.Context()
			home := t.TempDir()
			t.Setenv("HOME", home)
			lib := openTestLibrary(t)
			agent := addAgent(t, lib, home, "Agent", ".agent/skills", ".agent/skills")
			skill := fetchNamedSkill(t, lib, "example")
			group, err := lib.CreateGroup(ctx, "g")
			if err != nil {
				t.Fatal(err)
			}
			global := library.GlobalScope(agent.Code)
			if err := lib.Install(ctx, skill.ID, global, library.InstallOptions{}); err != nil {
				t.Fatal(err)
			}
			if _, err := lib.InstallGroup(ctx, group.ID, global, library.InstallOptions{}); err != nil {
				t.Fatal(err)
			}
			result, err := lib.DeleteAgent(ctx, agent.Code, library.DeleteAgentOptions{Folders: policy})
			if err != nil || len(result.Kept) != 0 {
				t.Fatalf("DeleteAgent() = %#v, %v; want nothing kept for other agents", result, err)
			}
			if got := listAgents(t, lib); !reflect.DeepEqual(got, seededAgents(home)) {
				t.Fatalf("ListAgents() = %#v, want the custom agent gone", got)
			}
			requireSkillScopes(t, lib, nil)
			if installs, err := lib.ListGroupInstalls(ctx); err != nil || len(installs) != 0 {
				t.Fatalf("ListGroupInstalls() = %#v, %v; want none", installs, err)
			}
			if policy == library.Keep {
				requireInstalledFolder(t, agent.GlobalDirectory, "example", "# example\n")
			} else {
				requireNoInstalledFolder(t, agent.GlobalDirectory, "example")
			}
			_, err = lib.ScopeDirectory(ctx, global)
			requireLibraryError(t, err, library.NotFound)
		})
	}
}

// 两个 Agent 共用目录:装到其中一个,另一个也算已装(列表、巡检、安装计划一致),并能查到共用的 Agent;
// 对另一个再安装只补记选择,不报占用;从任何一个卸载都删除文件夹,两个都不再算已装。
func TestSharedDirectoryCountsAsInstalledForEveryAgent(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	skill := fetchNamedSkill(t, lib, "example")
	codex := library.GlobalScope(library.AgentCodex)
	if err := lib.Install(ctx, skill.ID, codex, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	agent := addAgent(t, lib, home, "Agent", ".agents/skills", ".agent/skills")
	shared := library.GlobalScope(agent.Code)
	directory := filepath.Join(home, ".agents", "skills")
	folder := filepath.Join(directory, "example")
	want := []library.SkillScope{
		{SkillID: skill.ID, Scope: codex, Folder: folder, Direct: true, SharedWith: []library.AgentCode{agent.Code}},
		{SkillID: skill.ID, Scope: shared, Folder: folder, Direct: true, SharedWith: []library.AgentCode{library.AgentCodex}},
	}
	if agent.Code < library.AgentCodex {
		want[0], want[1] = want[1], want[0]
	}
	requireSkillScopes(t, lib, want)
	got, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{Scope: &shared})
	if err != nil || len(got) != 1 || !reflect.DeepEqual(got[0].SharedWith, []library.AgentCode{library.AgentCodex}) {
		t.Fatalf("ListSkillScopes(shared) = %#v, %v; want the folder shared with codex", got, err)
	}
	inspections, err := lib.InspectSkillScopes(ctx, library.SkillScopeFilter{Scope: &shared})
	if err != nil || len(inspections) != 1 || inspections[0].Status != library.SkillScopeCurrent {
		t.Fatalf("InspectSkillScopes(shared) = %#v, %v; want current", inspections, err)
	}
	plan, err := lib.PlanInstall(ctx, skill.ID, shared)
	if err != nil || plan.Status != library.InstallAlreadyInstalled || plan.Folder != folder {
		t.Fatalf("PlanInstall(shared) = %#v, %v; want already installed", plan, err)
	}
	writeFile(t, filepath.Join(folder, "notes.md"), []byte("edited\n"), 0o644)
	if err := lib.Install(ctx, skill.ID, shared, library.InstallOptions{}); err != nil {
		t.Fatalf("Install(shared) = %v, want recorded without occupying", err)
	}
	if _, err := os.Stat(filepath.Join(folder, "notes.md")); err != nil {
		t.Fatalf("Install(shared) touched the folder: %v", err)
	}
	requireSkillScopes(t, lib, want)

	if err := lib.Uninstall(ctx, skill.ID, shared); err != nil {
		t.Fatal(err)
	}
	requireNoInstalledFolder(t, directory, "example")
	requireSkillScopes(t, lib, nil)
	plan, err = lib.PlanInstall(ctx, skill.ID, codex)
	if err != nil || plan.Status != library.InstallReady {
		t.Fatalf("PlanInstall(codex) after uninstall = %#v, %v; want ready", plan, err)
	}

	if err := lib.Install(ctx, skill.ID, shared, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := lib.Uninstall(ctx, skill.ID, codex); err != nil {
		t.Fatal(err)
	}
	requireNoInstalledFolder(t, directory, "example")
	requireSkillScopes(t, lib, nil)
}

// 共用目录里装着另一个同名技能时,仍按占用处理;项目层级按项目里的技能目录判断共用。
func TestSharedDirectoryStillReportsAnotherSkillAsOccupier(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	agent := addAgent(t, lib, home, "Agent", ".agent/skills", ".agents/skills")
	first := fetchSkillWithBody(t, lib, "commit", "# A\n")
	second := fetchSkillWithBody(t, lib, "commit", "# B\n")
	if err := lib.Install(ctx, first.ID, library.ProjectScope(library.AgentCodex, project.ID), library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	shared := library.ProjectScope(agent.Code, project.ID)
	plan, err := lib.PlanInstall(ctx, second.ID, shared)
	if err != nil || plan.Status != library.InstallOccupied || plan.OccupierSkillID != first.ID {
		t.Fatalf("PlanInstall(second, shared) = %#v, %v; want occupied by first", plan, err)
	}
	plan, err = lib.PlanInstall(ctx, first.ID, shared)
	if err != nil || plan.Status != library.InstallAlreadyInstalled {
		t.Fatalf("PlanInstall(first, shared) = %#v, %v; want already installed", plan, err)
	}
	plan, err = lib.PlanInstall(ctx, first.ID, library.GlobalScope(agent.Code))
	if err != nil || plan.Status != library.InstallReady {
		t.Fatalf("PlanInstall(first, global) = %#v, %v; want ready: global directories differ", plan, err)
	}
}

// 组装到共用目录的两个 Agent:加入成员时只放一次文件夹,两处都算装上;撤掉一处组安装,另一处的组安装还留着文件夹。
func TestGroupInstallsAcrossAgentsSharingADirectory(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	agent := addAgent(t, lib, home, "Agent", ".agents/skills", ".agent/skills")
	codex := library.GlobalScope(library.AgentCodex)
	shared := library.GlobalScope(agent.Code)
	group, err := lib.CreateGroup(ctx, "g")
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []library.Scope{codex, shared} {
		if _, err := lib.InstallGroup(ctx, group.ID, scope, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	skill := fetchNamedSkill(t, lib, "member")
	results, err := lib.AddSkillToGroup(ctx, group.ID, skill.ID)
	installed := []library.GroupMemberResult{
		{SkillID: skill.ID, Scope: codex, Outcome: library.MemberInstalled},
		{SkillID: skill.ID, Scope: shared, Outcome: library.MemberInstalled},
	}
	if agent.Code < library.AgentCodex {
		installed[0], installed[1] = installed[1], installed[0]
	}
	requireMemberResults(t, results, err, installed)
	directory := filepath.Join(home, ".agents", "skills")
	requireInstalledFolder(t, directory, "member", "# member\n")

	results, err = lib.InstallGroup(ctx, group.ID, shared, library.InstallOptions{})
	requireMemberResults(t, results, err, []library.GroupMemberResult{{SkillID: skill.ID, Scope: shared, Outcome: library.MemberAlreadyInstalled}})
	if err := lib.UninstallGroup(ctx, group.ID, codex); err != nil {
		t.Fatal(err)
	}
	requireInstalledFolder(t, directory, "member", "# member\n")
	got, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{Scope: &codex})
	if err != nil || len(got) != 1 || !reflect.DeepEqual(got[0].Groups, []library.GroupID{group.ID}) {
		t.Fatalf("ListSkillScopes(codex) = %#v, %v; want still installed through the shared group install", got, err)
	}
	if err := lib.UninstallGroup(ctx, group.ID, shared); err != nil {
		t.Fatal(err)
	}
	requireNoInstalledFolder(t, directory, "member")
	requireSkillScopes(t, lib, nil)
}

// 共用目录里一个 Agent 的组安装把技能装上后,在另一个 Agent 上组安装只补记,不报占用。
func TestGroupInstallCountsSkillInstalledByASharingAgent(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	agent := addAgent(t, lib, home, "Agent", ".agents/skills", ".agent/skills")
	skill := fetchNamedSkill(t, lib, "member")
	codex := library.GlobalScope(library.AgentCodex)
	shared := library.GlobalScope(agent.Code)
	if err := lib.Install(ctx, skill.ID, codex, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	group, err := lib.CreateGroup(ctx, "g")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.AddSkillToGroup(ctx, group.ID, skill.ID); err != nil {
		t.Fatal(err)
	}
	plan, err := lib.PlanGroupInstall(ctx, group.ID, shared)
	if err != nil || len(plan.Members) != 1 || plan.Members[0].Status != library.InstallAlreadyInstalled {
		t.Fatalf("PlanGroupInstall(shared) = %#v, %v; want already installed", plan, err)
	}
	results, err := lib.InstallGroup(ctx, group.ID, shared, library.InstallOptions{})
	requireMemberResults(t, results, err, []library.GroupMemberResult{{SkillID: skill.ID, Scope: shared, Outcome: library.MemberAlreadyInstalled}})
	if err := lib.Uninstall(ctx, skill.ID, codex); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(home, ".agents", "skills")
	requireInstalledFolder(t, directory, "member", "# member\n")
	got, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{Scope: &codex})
	if err != nil || len(got) != 1 || got[0].Direct || !reflect.DeepEqual(got[0].Groups, []library.GroupID{group.ID}) {
		t.Fatalf("ListSkillScopes(codex) = %#v, %v; want kept by the group install on the sharing agent", got, err)
	}
	err = lib.Uninstall(ctx, skill.ID, codex)
	requireErrorDetails(t, err, library.FailedPrecondition, library.ErrorDetails{Name: "g"})
}

// 共用目录里文件夹不在了,从共用的另一个 Agent 也能重新安装回去。
func TestReinstallThroughASharingAgent(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	agent := addAgent(t, lib, home, "Agent", ".agents/skills", ".agent/skills")
	skill := fetchNamedSkill(t, lib, "example")
	if err := lib.Install(ctx, skill.ID, library.GlobalScope(library.AgentCodex), library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(home, ".agents", "skills")
	if err := os.RemoveAll(filepath.Join(directory, "example")); err != nil {
		t.Fatal(err)
	}
	shared := library.GlobalScope(agent.Code)
	requireReinstallResults(t, reinstall(t, lib, reinstallAt(skill.ID, shared)), []library.ReinstallResult{reinstalledResult(skill.ID, shared)})
	requireInstalledFolder(t, directory, "example", "# example\n")
}

// 自定义 Agent 离开一个 Codex 还在读的共用目录:迁移时文件夹原样复制过去,旧文件夹和安装留给 Codex;
// 不迁移时安装记录转给 Codex,文件夹不动。
func TestEditAgentLeavingASharedDirectoryKeepsItForTheOthers(t *testing.T) {
	for _, migrate := range []bool{true, false} {
		t.Run(map[bool]string{true: "migrate", false: "keep"}[migrate], func(t *testing.T) {
			ctx := t.Context()
			home := t.TempDir()
			t.Setenv("HOME", home)
			lib := openTestLibrary(t)
			agent := addAgent(t, lib, home, "Agent", ".agents/skills", ".agent/skills")
			skill := fetchNamedSkill(t, lib, "example")
			shared := library.GlobalScope(agent.Code)
			codex := library.GlobalScope(library.AgentCodex)
			if err := lib.Install(ctx, skill.ID, shared, library.InstallOptions{}); err != nil {
				t.Fatal(err)
			}
			oldDir := filepath.Join(home, ".agents", "skills")
			newDir := filepath.Join(home, ".agent", "skills")
			writeFile(t, filepath.Join(oldDir, "example", "notes.md"), []byte("edited\n"), 0o644)
			result, err := lib.EditAgent(ctx, agent.Code, library.CustomAgent{Name: "Agent", GlobalDirectory: newDir, ProjectDirectory: ".agent/skills"}, library.EditAgentOptions{Migrate: migrate})
			if err != nil {
				t.Fatal(err)
			}
			want := []library.SkillScope{{SkillID: skill.ID, Scope: codex, Folder: filepath.Join(oldDir, "example"), Direct: true}}
			wantMigrations := []library.AgentMigration{}
			if migrate {
				wantMigrations = append(wantMigrations, library.AgentMigration{
					SkillID: skill.ID, Scope: shared, Outcome: library.MigrationCopied, From: filepath.Join(oldDir, "example"), To: filepath.Join(newDir, "example"),
				})
				want = append(want, library.SkillScope{SkillID: skill.ID, Scope: shared, Folder: filepath.Join(newDir, "example"), Direct: true})
				if agent.Code < library.AgentCodex {
					want[0], want[1] = want[1], want[0]
				}
				requireInstalledFolder(t, newDir, "example", "# example\n")
				if content, err := os.ReadFile(filepath.Join(newDir, "example", "notes.md")); err != nil || string(content) != "edited\n" {
					t.Fatalf("copied folder lost user edits: %q, %v", content, err)
				}
			} else {
				requireNoInstalledFolder(t, newDir, "example")
			}
			if !reflect.DeepEqual(result.Migrations, wantMigrations) {
				t.Fatalf("EditAgent() migrations = %#v, want %#v", result.Migrations, wantMigrations)
			}
			requireInstalledFolder(t, oldDir, "example", "# example\n")
			requireSkillScopes(t, lib, want)
		})
	}
}

// 删除共用目录的自定义 Agent:即使选了一并删除,Codex 还在读的文件夹也保留,安装记录转给 Codex,结果里列出保留了哪些、
// 还有哪些 Agent 在读;只因共用而算已装的也列出。没有别的 Agent 在读的照常删除。
func TestDeleteAgentKeepsFoldersStillReadByAnotherAgent(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	agent := addAgent(t, lib, home, "Agent", ".agents/skills", ".agent/skills")
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	explicit := fetchNamedSkill(t, lib, "explicit")
	inferred := fetchNamedSkill(t, lib, "inferred")
	alone := fetchNamedSkill(t, lib, "alone")
	shared := library.GlobalScope(agent.Code)
	codex := library.GlobalScope(library.AgentCodex)
	for _, install := range []struct {
		skill library.Skill
		scope library.Scope
	}{{explicit, shared}, {inferred, codex}, {alone, library.ProjectScope(agent.Code, project.ID)}} {
		if err := lib.Install(ctx, install.skill.ID, install.scope, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	directory := filepath.Join(home, ".agents", "skills")
	before, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{Scope: &shared})
	if err != nil {
		t.Fatal(err)
	}
	result, err := lib.DeleteAgent(ctx, agent.Code, library.DeleteAgentOptions{Folders: library.Remove})
	if err != nil || !reflect.DeepEqual(withoutCreatedAt(result.Kept), withoutCreatedAt(before)) {
		t.Fatalf("DeleteAgent() = %#v, %v; want kept %#v", result, err, before)
	}
	for _, kept := range result.Kept {
		if !reflect.DeepEqual(kept.SharedWith, []library.AgentCode{library.AgentCodex}) {
			t.Fatalf("kept folder %#v, want it still read by codex", kept)
		}
	}
	requireInstalledFolder(t, directory, "explicit", "# explicit\n")
	requireInstalledFolder(t, directory, "inferred", "# inferred\n")
	requireNoInstalledFolder(t, filepath.Join(project.Path, ".agent", "skills"), "alone")
	requireSkillScopes(t, lib, []library.SkillScope{
		{SkillID: explicit.ID, Scope: codex, Folder: filepath.Join(directory, "explicit"), Direct: true},
		{SkillID: inferred.ID, Scope: codex, Folder: filepath.Join(directory, "inferred"), Direct: true},
	})
}

// 只因共用目录而算已装的技能,改目录迁移时也原样复制到新目录,之后这个 Agent 在新目录照常算已装;旧文件夹留给 Codex。
func TestEditAgentMigratesSkillsCountedThroughASharedDirectory(t *testing.T) {
	for _, level := range []string{"global", "project"} {
		t.Run(level, func(t *testing.T) {
			ctx := t.Context()
			home := t.TempDir()
			t.Setenv("HOME", home)
			lib := openTestLibrary(t)
			agent := addAgent(t, lib, home, "Agent", ".agents/skills", ".agents/skills")
			project, err := lib.RegisterProject(ctx, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			skill := fetchNamedSkill(t, lib, "example")
			codex, scope := library.GlobalScope(library.AgentCodex), library.GlobalScope(agent.Code)
			root, settings := home, library.CustomAgent{Name: "Agent", GlobalDirectory: filepath.Join(home, ".new", "skills"), ProjectDirectory: agent.ProjectDirectory}
			if level == "project" {
				codex, scope = library.ProjectScope(library.AgentCodex, project.ID), library.ProjectScope(agent.Code, project.ID)
				root, settings = project.Path, library.CustomAgent{Name: "Agent", GlobalDirectory: agent.GlobalDirectory, ProjectDirectory: ".new/skills"}
			}
			if err := lib.Install(ctx, skill.ID, codex, library.InstallOptions{}); err != nil {
				t.Fatal(err)
			}
			oldDir, newDir := filepath.Join(root, ".agents", "skills"), filepath.Join(root, ".new", "skills")
			writeFile(t, filepath.Join(oldDir, "example", "notes.md"), []byte("user edits\n"), 0o644)
			result, err := lib.EditAgent(ctx, agent.Code, settings, library.EditAgentOptions{Migrate: true})
			want := []library.AgentMigration{{
				SkillID: skill.ID, Scope: scope, Outcome: library.MigrationCopied, From: filepath.Join(oldDir, "example"), To: filepath.Join(newDir, "example"),
			}}
			if err != nil || !reflect.DeepEqual(result.Migrations, want) {
				t.Fatalf("EditAgent() = %#v, %v; want migrations %#v", result, err, want)
			}
			for _, directory := range []string{oldDir, newDir} {
				if content, err := os.ReadFile(filepath.Join(directory, "example", "notes.md")); err != nil || string(content) != "user edits\n" {
					t.Fatalf("%s/example/notes.md = %q, %v; want the user edits", directory, content, err)
				}
			}
			wantScopes := []library.SkillScope{
				{SkillID: skill.ID, Scope: codex, Folder: filepath.Join(oldDir, "example"), Direct: true},
				{SkillID: skill.ID, Scope: scope, Folder: filepath.Join(newDir, "example"), Direct: true},
			}
			if agent.Code < library.AgentCodex {
				wantScopes[0], wantScopes[1] = wantScopes[1], wantScopes[0]
			}
			requireSkillScopes(t, lib, wantScopes)
		})
	}
}

// 迁移不光看磁盘,也看库里的安装:新目录里另一个同名技能的文件夹不在了、但它的安装还在时,不搬过去,
// 之后卸载那个技能也不会删掉这个 Agent 的文件夹。
func TestEditAgentMigrationRespectsAnotherSkillsInstall(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	agent := addAgent(t, lib, home, "Agent", ".old/skills", ".agent/skills")
	first := fetchSkillWithBody(t, lib, "example", "# first\n")
	second := fetchSkillWithBody(t, lib, "example", "# second\n")
	codex, scope := library.GlobalScope(library.AgentCodex), library.GlobalScope(agent.Code)
	if err := lib.Install(ctx, first.ID, codex, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := lib.Install(ctx, second.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	newDir, oldDir := filepath.Join(home, ".agents", "skills"), filepath.Join(home, ".old", "skills")
	if err := os.RemoveAll(filepath.Join(newDir, "example")); err != nil {
		t.Fatal(err)
	}
	result, err := lib.EditAgent(ctx, agent.Code, library.CustomAgent{Name: "Agent", GlobalDirectory: newDir, ProjectDirectory: agent.ProjectDirectory}, library.EditAgentOptions{Migrate: true})
	want := []library.AgentMigration{{
		SkillID: second.ID, Scope: scope, Outcome: library.MigrationSkipped, From: filepath.Join(oldDir, "example"), To: filepath.Join(newDir, "example"),
	}}
	if err != nil || !reflect.DeepEqual(result.Migrations, want) {
		t.Fatalf("EditAgent() = %#v, %v; want migrations %#v", result, err, want)
	}
	requireNoInstalledFolder(t, newDir, "example")
	requireInstalledFolder(t, oldDir, "example", "# second\n")
	if scopes, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{SkillID: second.ID}); err != nil || len(scopes) != 0 {
		t.Fatalf("ListSkillScopes(second) = %#v, %v; want none", scopes, err)
	}
	if err := lib.Uninstall(ctx, first.ID, codex); err != nil {
		t.Fatal(err)
	}
	requireInstalledFolder(t, oldDir, "example", "# second\n")
}

// 复制期间新目录里冒出同名文件夹(用户或别的程序建的)时不覆盖它:整次返回 AlreadyExists 并指出路径,什么都不改。
func TestEditAgentMigrationNeverOverwritesAFolderThatAppearsDuringCopy(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	agent := addAgent(t, lib, home, "Agent", ".old/skills", ".agent/skills")
	skill := fetchNamedSkill(t, lib, "example")
	scope := library.GlobalScope(agent.Code)
	if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	oldDir, newDir := filepath.Join(home, ".old", "skills"), filepath.Join(home, ".new", "skills")
	appeared := false
	injectDuringCopy := cancelWhen{Context: ctx, cond: func() bool {
		entries, _ := os.ReadDir(newDir)
		for _, entry := range entries {
			if !appeared && strings.HasPrefix(entry.Name(), ".example-") {
				writeFile(t, filepath.Join(newDir, "example", "SKILL.md"), []byte("created during the copy\n"), 0o644)
				appeared = true
			}
		}
		return false
	}}
	_, err := lib.EditAgent(injectDuringCopy, agent.Code, library.CustomAgent{Name: "Agent", GlobalDirectory: newDir, ProjectDirectory: agent.ProjectDirectory}, library.EditAgentOptions{Migrate: true})
	if !appeared {
		t.Fatal("the folder was never injected")
	}
	requireErrorDetails(t, err, library.AlreadyExists, library.ErrorDetails{Path: filepath.Join(newDir, "example")})
	requireSkillFile(t, newDir, "example", "created during the copy\n")
	requireInstalledFolder(t, oldDir, "example", "# example\n")
	requireNoStagedEntries(t, newDir)
	requireSkillScopes(t, lib, []library.SkillScope{{SkillID: skill.ID, Scope: scope, Folder: filepath.Join(oldDir, "example"), Direct: true}})
	if got := listAgents(t, lib); !slices.Contains(got, agent) {
		t.Fatalf("ListAgents() = %#v, want %#v unchanged", got, agent)
	}
}

// 新目录填在要迁移的技能文件夹里面时拒绝,什么都不改,也不在源文件夹里建出目录。
func TestEditAgentRejectsDirectoryInsideAnInstalledSkill(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	agent := addAgent(t, lib, home, "Agent", ".old/skills", ".agent/skills")
	for _, name := range []string{"aaa", "example"} {
		skill := fetchNamedSkill(t, lib, name)
		if err := lib.Install(ctx, skill.ID, library.GlobalScope(agent.Code), library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	source := filepath.Join(home, ".old", "skills", "example")
	before := snapshotTree(t, filepath.Join(home, ".old"))
	_, err := lib.EditAgent(ctx, agent.Code, library.CustomAgent{Name: "Agent", GlobalDirectory: filepath.Join(source, "nested"), ProjectDirectory: agent.ProjectDirectory}, library.EditAgentOptions{Migrate: true})
	requireErrorDetails(t, err, library.InvalidArgument, library.ErrorDetails{Field: "global_directory"})
	if after := snapshotTree(t, filepath.Join(home, ".old")); !reflect.DeepEqual(after, before) {
		t.Fatalf("source tree changed: %#v, want %#v", after, before)
	}
	requireNoInstalledFolder(t, source, "nested")
	if got := listAgents(t, lib); !slices.Contains(got, agent) {
		t.Fatalf("ListAgents() = %#v, want %#v unchanged", got, agent)
	}
}

// 迁移在提交阶段失败(旧项目目录不让删)时整次复原:新目录里的副本(哪怕复制成只读)撤掉,已挪走的旧全局文件夹放回,
// 安装记录和 Agent 的目录都不变。
func TestEditAgentMigrationFailureWhileCommittingRestoresEverything(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	agent := addAgent(t, lib, home, "Agent", ".old/skills", ".old/skills")
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	skill := fetchNamedSkill(t, lib, "example")
	global, projectScope := library.GlobalScope(agent.Code), library.ProjectScope(agent.Code, project.ID)
	for _, scope := range []library.Scope{global, projectScope} {
		if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	oldGlobal, oldProject := filepath.Join(home, ".old", "skills"), filepath.Join(project.Path, ".old", "skills")
	newGlobal, newProject := filepath.Join(home, ".new", "skills"), filepath.Join(project.Path, ".new", "skills")
	writeFile(t, filepath.Join(oldGlobal, "example", "notes.md"), []byte("user edits\n"), 0o644)
	readOnlyFolder := filepath.Join(oldProject, "example")
	if err := os.Chmod(readOnlyFolder, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(readOnlyFolder, 0o755) })
	makeScopeReadOnly(t, oldProject)
	before := snapshotTree(t, oldGlobal, oldProject)
	_, err = lib.EditAgent(ctx, agent.Code, library.CustomAgent{Name: "Agent", GlobalDirectory: newGlobal, ProjectDirectory: ".new/skills"}, library.EditAgentOptions{Migrate: true})
	requireLibraryError(t, err, library.Permission)
	if after := snapshotTree(t, oldGlobal, oldProject); !reflect.DeepEqual(after, before) {
		t.Fatalf("old folders = %#v, want %#v", after, before)
	}
	for _, directory := range []string{newGlobal, newProject} {
		requireNoInstalledFolder(t, directory, "example")
		requireNoStagedEntries(t, directory)
	}
	requireNoStagedEntries(t, oldGlobal)
	requireSkillScopes(t, lib, []library.SkillScope{
		{SkillID: skill.ID, Scope: global, Folder: filepath.Join(oldGlobal, "example"), Direct: true},
		{SkillID: skill.ID, Scope: projectScope, Folder: filepath.Join(oldProject, "example"), Direct: true},
	})
	if got := listAgents(t, lib); !slices.Contains(got, agent) {
		t.Fatalf("ListAgents() = %#v, want %#v unchanged", got, agent)
	}
}

// 改目录迁移中途失败时整次复原:磁盘、安装记录和 Agent 的目录都保持原样。
func TestEditAgentMigrationFailureLeavesEverythingAsItWas(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	agent := addAgent(t, lib, home, "Agent", ".old/skills", ".agent/skills")
	skill := fetchNamedSkill(t, lib, "example")
	global := library.GlobalScope(agent.Code)
	if err := lib.Install(ctx, skill.ID, global, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	oldDir := filepath.Join(home, ".old", "skills")
	unreadable := filepath.Join(oldDir, "example", "private")
	writeFile(t, filepath.Join(unreadable, "secret.md"), []byte("secret\n"), 0o644)
	if err := os.Chmod(unreadable, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0o755) })
	if _, err := os.ReadDir(unreadable); !errors.Is(err, fs.ErrPermission) {
		t.Skip("directory permissions are not enforced for this user")
	}
	newDir := filepath.Join(home, ".new", "skills")
	_, err := lib.EditAgent(ctx, agent.Code, library.CustomAgent{Name: "Agent", GlobalDirectory: newDir, ProjectDirectory: ".agent/skills"}, library.EditAgentOptions{Migrate: true})
	if err == nil {
		t.Fatal("EditAgent() = nil, want the copy to fail")
	}
	requireInstalledFolder(t, oldDir, "example", "# example\n")
	requireNoInstalledFolder(t, newDir, "example")
	requireNoStagedEntries(t, newDir)
	requireSkillScopes(t, lib, []library.SkillScope{{SkillID: skill.ID, Scope: global, Folder: filepath.Join(oldDir, "example"), Direct: true}})
	if got := listAgents(t, lib); !slices.Contains(got, agent) {
		t.Fatalf("ListAgents() = %#v, want %#v unchanged", got, agent)
	}
}
