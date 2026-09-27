package library_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/tiaot33/skills-tui/internal/library"
)

// requireSkillScopes 断言全部 SkillScope(忽略首次安装时间)。
func requireSkillScopes(t *testing.T, lib library.Library, want []library.SkillScope) {
	t.Helper()
	skillScopes, err := lib.ListSkillScopes(t.Context())
	if err != nil || !reflect.DeepEqual(withoutCreatedAt(skillScopes), want) {
		t.Fatalf("ListSkillScopes() = %#v, %v; want %#v", skillScopes, err, want)
	}
}

// requireNoInstalledFolder 断言 directory 下没有 name 文件夹。
func requireNoInstalledFolder(t *testing.T, directory, name string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(directory, name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s/%s exists: %v", directory, name, err)
	}
}

// issue #85 的复现:组已安装到全局与项目两个 Scope 后,加入的成员安装到两处;移出的成员(默认 keep)
// 改为直接安装,可以单独卸载,不再卡在"只经由组安装"。
func TestGroupMembershipFollowsGroupInstallInEveryScope(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	a := fetchNamedSkill(t, lib, "a")
	b := fetchNamedSkill(t, lib, "b")
	c := fetchNamedSkill(t, lib, "c")
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	global := library.GlobalScope(library.AgentCodex)
	projectScope := library.ProjectScope(library.AgentClaudeCode, project.ID)
	globalDir, err := lib.Resolve(ctx, global)
	if err != nil {
		t.Fatal(err)
	}
	projectDir, err := lib.Resolve(ctx, projectScope)
	if err != nil {
		t.Fatal(err)
	}
	g, err := lib.CreateGroup(ctx, "g")
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range []library.Skill{a, b} {
		if _, err := lib.AddSkillToGroup(ctx, g.ID, skill.ID); err != nil {
			t.Fatal(err)
		}
	}
	for _, scope := range []library.Scope{projectScope, global} {
		if _, err := lib.InstallGroup(ctx, g.ID, scope, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}

	results, err := lib.AddSkillToGroup(ctx, g.ID, c.ID)
	// 按 ListGroupInstalls 的顺序:claude-code 在 codex 之前。
	requireMemberResults(t, results, err, []library.GroupMemberResult{
		{SkillID: c.ID, Scope: projectScope, Outcome: library.MemberInstalled},
		{SkillID: c.ID, Scope: global, Outcome: library.MemberInstalled},
	})
	requireInstalledFolder(t, globalDir, "c", "# c\n")
	requireInstalledFolder(t, projectDir, "c", "# c\n")

	if err := lib.RemoveSkillFromGroup(ctx, g.ID, a.ID, library.RemoveFromGroupOptions{}); err != nil {
		t.Fatal(err)
	}
	via := []library.GroupID{g.ID}
	requireSkillScopes(t, lib, []library.SkillScope{
		{SkillID: a.ID, Scope: projectScope, Directory: projectDir, Direct: true},
		{SkillID: a.ID, Scope: global, Directory: globalDir, Direct: true},
		{SkillID: b.ID, Scope: projectScope, Directory: projectDir, Groups: via},
		{SkillID: b.ID, Scope: global, Directory: globalDir, Groups: via},
		{SkillID: c.ID, Scope: projectScope, Directory: projectDir, Groups: via},
		{SkillID: c.ID, Scope: global, Directory: globalDir, Groups: via},
	})
	requireInstalledFolder(t, globalDir, "a", "# a\n")
	if err := lib.Uninstall(ctx, a.ID, global); err != nil {
		t.Fatalf("Uninstall removed member = %v, want success", err)
	}
	requireNoInstalledFolder(t, globalDir, "a")

	// 卸载整组只撤走只由本组留着的技能;移出后转为直接安装的 a 仍在项目 Scope。
	for _, scope := range []library.Scope{projectScope, global} {
		if err := lib.UninstallGroup(ctx, g.ID, scope); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"b", "c"} {
		requireNoInstalledFolder(t, globalDir, name)
		requireNoInstalledFolder(t, projectDir, name)
	}
	requireInstalledFolder(t, projectDir, "a", "# a\n")
	requireSkillScopes(t, lib, []library.SkillScope{{SkillID: a.ID, Scope: projectScope, Directory: projectDir, Direct: true}})
	if installs, err := lib.ListGroupInstalls(ctx); err != nil || len(installs) != 0 {
		t.Fatalf("ListGroupInstalls() after UninstallGroup = %#v, %v; want none", installs, err)
	}
}

// 空组也能安装:组安装带时间记录下来;之后加入的成员随之安装。成员全部移出(uninstall)后组安装仍在,
// 再次加入又会安装。再次 InstallGroup 不改变组安装的时间。
func TestEmptyGroupInstallIsRecordedAndCoversLaterMembers(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.Resolve(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	g, err := lib.CreateGroup(ctx, "g")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC()
	results, err := lib.InstallGroup(ctx, g.ID, scope, library.InstallOptions{})
	requireMemberResults(t, results, err, nil)
	end := time.Now().UTC()
	installs, err := lib.ListGroupInstalls(ctx)
	if err != nil || len(installs) != 1 || installs[0].GroupID != g.ID || installs[0].Scope != scope ||
		installs[0].CreatedAt.Before(start) || installs[0].CreatedAt.After(end) {
		t.Fatalf("ListGroupInstalls() = %#v, %v; want %d at %v within [%v, %v]", installs, err, g.ID, scope, start, end)
	}

	skill := fetchNamedSkill(t, lib, "late")
	for range 2 {
		results, err = lib.AddSkillToGroup(ctx, g.ID, skill.ID)
		requireMemberResults(t, results, err, []library.GroupMemberResult{{SkillID: skill.ID, Scope: scope, Outcome: library.MemberInstalled}})
		requireInstalledFolder(t, directory, "late", "# late\n")
		requireSkillScopes(t, lib, []library.SkillScope{{SkillID: skill.ID, Scope: scope, Directory: directory, Groups: []library.GroupID{g.ID}}})

		if err := lib.RemoveSkillFromGroup(ctx, g.ID, skill.ID, library.RemoveFromGroupOptions{IfNoOtherInstall: library.UninstallMember}); err != nil {
			t.Fatal(err)
		}
		requireNoInstalledFolder(t, directory, "late")
		requireSkillScopes(t, lib, nil)
	}

	if _, err := lib.InstallGroup(ctx, g.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	again, err := lib.ListGroupInstalls(ctx)
	if err != nil || !reflect.DeepEqual(again, installs) {
		t.Fatalf("ListGroupInstalls() after re-install = %#v, %v; want %#v", again, err, installs)
	}
}

// 技能同属两个已安装的组时,移出其中一个(无论 keep 还是 uninstall)技能仍由另一组留着,不补直接安装;
// uninstall 只卸载因此没有直接安装或组安装留着的技能,有直接安装的保留。
func TestRemovingMemberFromOneOfTwoInstalledGroups(t *testing.T) {
	for _, policy := range []library.MemberRemovalPolicy{library.KeepAsDirectInstall, library.UninstallMember} {
		t.Run(string(policy), func(t *testing.T) {
			ctx := t.Context()
			t.Setenv("HOME", t.TempDir())
			lib := openTestLibrary(t)
			shared := fetchNamedSkill(t, lib, "shared")
			direct := fetchNamedSkill(t, lib, "direct")
			scope := library.GlobalScope(library.AgentCodex)
			directory, err := lib.Resolve(ctx, scope)
			if err != nil {
				t.Fatal(err)
			}
			if err := lib.Install(ctx, direct.ID, scope, library.InstallOptions{}); err != nil {
				t.Fatal(err)
			}
			var groups []library.Group
			for _, name := range []string{"first", "second"} {
				group, err := lib.CreateGroup(ctx, name)
				if err != nil {
					t.Fatal(err)
				}
				groups = append(groups, group)
				if _, err := lib.AddSkillToGroup(ctx, group.ID, shared.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := lib.InstallGroup(ctx, group.ID, scope, library.InstallOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			results, err := lib.AddSkillToGroup(ctx, groups[0].ID, direct.ID)
			requireMemberResults(t, results, err, []library.GroupMemberResult{{SkillID: direct.ID, Scope: scope, Outcome: library.MemberAlreadyInstalled}})

			opts := library.RemoveFromGroupOptions{IfNoOtherInstall: policy}
			for _, skill := range []library.Skill{shared, direct} {
				if err := lib.RemoveSkillFromGroup(ctx, groups[0].ID, skill.ID, opts); err != nil {
					t.Fatal(err)
				}
			}
			requireSkillScopes(t, lib, []library.SkillScope{
				{SkillID: shared.ID, Scope: scope, Directory: directory, Groups: []library.GroupID{groups[1].ID}},
				{SkillID: direct.ID, Scope: scope, Directory: directory, Direct: true},
			})
			requireInstalledFolder(t, directory, "shared", "# shared\n")
			requireInstalledFolder(t, directory, "direct", "# direct\n")

			// 另一组也移出后:keep 转为直接安装,uninstall 连文件夹移除。
			if err := lib.RemoveSkillFromGroup(ctx, groups[1].ID, shared.ID, opts); err != nil {
				t.Fatal(err)
			}
			if policy == library.KeepAsDirectInstall {
				requireInstalledFolder(t, directory, "shared", "# shared\n")
				requireSkillScopes(t, lib, []library.SkillScope{
					{SkillID: shared.ID, Scope: scope, Directory: directory, Direct: true},
					{SkillID: direct.ID, Scope: scope, Directory: directory, Direct: true},
				})
			} else {
				requireNoInstalledFolder(t, directory, "shared")
				requireSkillScopes(t, lib, []library.SkillScope{{SkillID: direct.ID, Scope: scope, Directory: directory, Direct: true}})
			}
			requireNoStagedEntries(t, directory)
		})
	}
}

// 加入已安装的组时:已安装的(即使文件夹被手动改过)只补组安装、不写盘;有未记录文件夹或与组内成员同名的跳过,
// 成员关系照常成立,之后再次 InstallGroup(Overwrite)补齐未记录文件夹那一项。
func TestAddingMemberToInstalledGroupReportsEachScope(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.Resolve(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	g, err := lib.CreateGroup(ctx, "g")
	if err != nil {
		t.Fatal(err)
	}
	holder := fetchSkillWithBody(t, lib, "dup", "# holder\n")
	if _, err := lib.AddSkillToGroup(ctx, g.ID, holder.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.InstallGroup(ctx, g.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}

	edited := fetchNamedSkill(t, lib, "edited")
	if err := lib.Install(ctx, edited.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(directory, "edited", "SKILL.md"), []byte("by hand\n"), 0o644)
	results, err := lib.AddSkillToGroup(ctx, g.ID, edited.ID)
	requireMemberResults(t, results, err, []library.GroupMemberResult{{SkillID: edited.ID, Scope: scope, Outcome: library.MemberAlreadyInstalled}})
	requireSkillFile(t, directory, "edited", "by hand\n")

	untracked := fetchNamedSkill(t, lib, "untracked")
	writeFile(t, filepath.Join(directory, "untracked", "manual.txt"), []byte("manual"), 0o644)
	results, err = lib.AddSkillToGroup(ctx, g.ID, untracked.ID)
	requireMemberResults(t, results, err, []library.GroupMemberResult{
		skippedMember(untracked.ID, scope, library.AlreadyExists, library.ErrorDetails{Path: filepath.Join(directory, "untracked")}),
	})

	namesake := fetchSkillWithBody(t, lib, "dup", "# namesake\n")
	results, err = lib.AddSkillToGroup(ctx, g.ID, namesake.ID)
	requireMemberResults(t, results, err, []library.GroupMemberResult{
		skippedMember(namesake.ID, scope, library.FailedPrecondition, library.ErrorDetails{Name: "dup", Path: filepath.Join(directory, "dup")}),
	})
	requireInstalledFolder(t, directory, "dup", "# holder\n")

	members, err := lib.ListGroupSkills(ctx, g.ID)
	if want := []library.SkillID{holder.ID, edited.ID, untracked.ID, namesake.ID}; err != nil || !reflect.DeepEqual(summaryIDs(members), want) {
		t.Fatalf("ListGroupSkills() = %v, %v; want %v", summaryIDs(members), err, want)
	}
	via := []library.GroupID{g.ID}
	requireSkillScopes(t, lib, []library.SkillScope{
		{SkillID: holder.ID, Scope: scope, Directory: directory, Groups: via},
		{SkillID: edited.ID, Scope: scope, Directory: directory, Direct: true, Groups: via},
	})

	// 组内同名的成员此后在组安装时都被跳过,包括已安装的 holder 以外的那一个。
	results, err = lib.InstallGroup(ctx, g.ID, scope, library.InstallOptions{OnConflict: library.Overwrite})
	requireMemberResults(t, results, err, []library.GroupMemberResult{
		{SkillID: holder.ID, Scope: scope, Outcome: library.MemberAlreadyInstalled},
		{SkillID: edited.ID, Scope: scope, Outcome: library.MemberAlreadyInstalled},
		{SkillID: untracked.ID, Scope: scope, Outcome: library.MemberInstalled},
		skippedMember(namesake.ID, scope, library.FailedPrecondition, library.ErrorDetails{Name: "dup", Path: filepath.Join(directory, "dup")}),
	})
	requireInstalledFolder(t, directory, "untracked", "# untracked\n")
	requireSkillFile(t, directory, "edited", "by hand\n")
}

// 加入成员时落盘失败,整个操作复原:不留成员关系、安装记录与半成品目录。
func TestAddingMemberRollsBackMembershipWhenInstallFails(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.Resolve(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	g, err := lib.CreateGroup(ctx, "g")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.InstallGroup(ctx, g.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	skill := fetchNamedSkill(t, lib, "blocked")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	makeScopeReadOnly(t, directory)
	if _, err := lib.AddSkillToGroup(ctx, g.ID, skill.ID); err == nil {
		t.Fatal("AddSkillToGroup succeeded despite a read-only scope")
	}
	if err := os.Chmod(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	members, err := lib.ListGroupSkills(ctx, g.ID)
	if err != nil || len(members) != 0 {
		t.Fatalf("ListGroupSkills() after failed add = %#v, %v; want none", members, err)
	}
	requireNoInstalledFolder(t, directory, "blocked")
	requireSkillScopes(t, lib, nil)
	requireNoStagedEntries(t, directory)
}

// 注销项目清掉该项目各 Scope 上的组安装;删除分组清掉它的全部组安装,只由它留着的技能转为直接安装。
func TestUnregisterProjectAndDeleteGroupClearGroupInstalls(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	skill := fetchNamedSkill(t, lib, "member")
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	global := library.GlobalScope(library.AgentCodex)
	projectScope := library.ProjectScope(library.AgentCodex, project.ID)
	globalDir, err := lib.Resolve(ctx, global)
	if err != nil {
		t.Fatal(err)
	}
	first, err := lib.CreateGroup(ctx, "first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := lib.CreateGroup(ctx, "second")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.AddSkillToGroup(ctx, first.ID, skill.ID); err != nil {
		t.Fatal(err)
	}
	for _, install := range []library.GroupInstall{{GroupID: first.ID, Scope: global}, {GroupID: first.ID, Scope: projectScope}, {GroupID: second.ID, Scope: projectScope}} {
		if _, err := lib.InstallGroup(ctx, install.GroupID, install.Scope, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := lib.UnregisterProject(ctx, project.ID, library.UnregisterProjectOptions{}); err != nil {
		t.Fatal(err)
	}
	installs, err := lib.ListGroupInstalls(ctx)
	if want := []library.GroupInstall{{GroupID: first.ID, Scope: global}}; err != nil || !reflect.DeepEqual(withoutGroupCreatedAt(installs), want) {
		t.Fatalf("ListGroupInstalls() after UnregisterProject = %#v, %v; want %#v", installs, err, want)
	}

	if err := lib.DeleteGroup(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if installs, err := lib.ListGroupInstalls(ctx); err != nil || len(installs) != 0 {
		t.Fatalf("ListGroupInstalls() after DeleteGroup = %#v, %v; want none", installs, err)
	}
	requireSkillScopes(t, lib, []library.SkillScope{{SkillID: skill.ID, Scope: global, Directory: globalDir, Direct: true}})
	requireInstalledFolder(t, globalDir, "member", "# member\n")
}

// uninstall 先记录后删盘(#75):某个 Scope 的文件夹移不走时整体复原——已移走的其他 Scope 文件夹放回原处,
// 成员关系与安装记录不变。
func TestWithdrawRestoresEverythingWhenAFolderCannotBeRemoved(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	skill := fetchNamedSkill(t, lib, "member")
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	global := library.GlobalScope(library.AgentCodex)
	projectScope := library.ProjectScope(library.AgentCodex, project.ID)
	globalDir, err := lib.Resolve(ctx, global)
	if err != nil {
		t.Fatal(err)
	}
	projectDir, err := lib.Resolve(ctx, projectScope)
	if err != nil {
		t.Fatal(err)
	}
	g, err := lib.CreateGroup(ctx, "g")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.AddSkillToGroup(ctx, g.ID, skill.ID); err != nil {
		t.Fatal(err)
	}
	// 全局先于项目 Scope 处理:全局的文件夹先被移走,再在只读的项目 Scope 上失败。
	for _, scope := range []library.Scope{global, projectScope} {
		if _, err := lib.InstallGroup(ctx, g.ID, scope, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	makeScopeReadOnly(t, projectDir)
	err = lib.RemoveSkillFromGroup(ctx, g.ID, skill.ID, library.RemoveFromGroupOptions{IfNoOtherInstall: library.UninstallMember})
	if err == nil {
		t.Fatal("RemoveSkillFromGroup(uninstall) succeeded despite a read-only scope")
	}
	if err := os.Chmod(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	requireInstalledFolder(t, globalDir, "member", "# member\n")
	requireInstalledFolder(t, projectDir, "member", "# member\n")
	requireNoStagedEntries(t, globalDir)
	members, err := lib.ListGroupSkills(ctx, g.ID)
	if err != nil || !reflect.DeepEqual(summaryIDs(members), []library.SkillID{skill.ID}) {
		t.Fatalf("ListGroupSkills() after failed uninstall = %v, %v; want [%d]", summaryIDs(members), err, skill.ID)
	}
	via := []library.GroupID{g.ID}
	requireSkillScopes(t, lib, []library.SkillScope{
		{SkillID: skill.ID, Scope: global, Directory: globalDir, Groups: via},
		{SkillID: skill.ID, Scope: projectScope, Directory: projectDir, Groups: via},
	})
}

// 作用域根目录(项目为已登记路径)不在本机时 Library 不创建它:Install / InstallGroup 报 FailedPrecondition,
// 不写盘、不留记录、不记组安装;加入成员跳过该 Scope,成员关系照加。根目录存在而 Agent 目录不存在时照常创建。
func TestMissingScopeRootIsNeverCreated(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	scope := library.ProjectScope(library.AgentClaudeCode, project.ID)
	directory, err := lib.Resolve(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	global := library.GlobalScope(library.AgentCodex)
	g, err := lib.CreateGroup(ctx, "g")
	if err != nil {
		t.Fatal(err)
	}
	// 根目录存在、.claude/skills 还不存在:组安装照常创建 Agent 目录。
	for _, target := range []library.Scope{global, scope} {
		if _, err := lib.InstallGroup(ctx, g.ID, target, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	present := fetchNamedSkill(t, lib, "present")
	if _, err := lib.AddSkillToGroup(ctx, g.ID, present.ID); err != nil {
		t.Fatal(err)
	}
	requireInstalledFolder(t, directory, "present", "# present\n")

	// 项目被移走:根目录不存在。
	if err := os.RemoveAll(project.Path); err != nil {
		t.Fatal(err)
	}
	missing := library.ErrorDetails{Path: project.Path, ProjectID: project.ID}
	skill := fetchNamedSkill(t, lib, "late")
	// 安装计划与执行一致:根目录缺失时同样报 FailedPrecondition。
	_, err = lib.PlanInstall(ctx, skill.ID, scope)
	requireErrorDetails(t, err, library.FailedPrecondition, missing)
	_, err = lib.PlanGroupInstall(ctx, g.ID, scope)
	requireErrorDetails(t, err, library.FailedPrecondition, missing)
	requireErrorDetails(t, lib.Install(ctx, skill.ID, scope, library.InstallOptions{}), library.FailedPrecondition, missing)
	// 先确认技能存在,再查根目录。
	if err := lib.Install(ctx, library.SkillID(9999), scope, library.InstallOptions{}); !library.IsCode(err, library.NotFound) {
		t.Fatalf("Install(unknown skill) with missing root = %v, want NotFound", err)
	}
	other, err := lib.CreateGroup(ctx, "other")
	if err != nil {
		t.Fatal(err)
	}
	_, err = lib.InstallGroup(ctx, other.ID, scope, library.InstallOptions{})
	requireErrorDetails(t, err, library.FailedPrecondition, missing)

	results, err := lib.AddSkillToGroup(ctx, g.ID, skill.ID)
	requireMemberResults(t, results, err, []library.GroupMemberResult{
		skippedMember(skill.ID, scope, library.FailedPrecondition, missing),
		{SkillID: skill.ID, Scope: global, Outcome: library.MemberInstalled},
	})
	members, err := lib.ListGroupSkills(ctx, g.ID)
	if err != nil || !reflect.DeepEqual(summaryIDs(members), []library.SkillID{present.ID, skill.ID}) {
		t.Fatalf("ListGroupSkills() = %v, %v; want [%d %d]", summaryIDs(members), err, present.ID, skill.ID)
	}
	if _, err := os.Lstat(project.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing project root was created: %v", err)
	}
	installs, err := lib.ListGroupInstalls(ctx)
	want := []library.GroupInstall{{GroupID: g.ID, Scope: scope}, {GroupID: g.ID, Scope: global}}
	if err != nil || !reflect.DeepEqual(withoutGroupCreatedAt(installs), want) {
		t.Fatalf("ListGroupInstalls() = %#v, %v; want %#v", installs, err, want)
	}
	globalDir, err := lib.Resolve(ctx, global)
	if err != nil {
		t.Fatal(err)
	}
	via := []library.GroupID{g.ID}
	requireSkillScopes(t, lib, []library.SkillScope{
		{SkillID: present.ID, Scope: scope, Directory: directory, Groups: via},
		{SkillID: present.ID, Scope: global, Directory: globalDir, Groups: via},
		{SkillID: skill.ID, Scope: global, Directory: globalDir, Groups: via},
	})
}

// 两个 Scope 实际落在同一目录时(已登记项目就是主目录,或路径经符号链接指到同一处),同一次加入成员里
// 先处理的那个照常安装、后一个按占用跳过;之后再对后一个 Scope 安装组也不会让两个 Scope 共用一个文件夹。
func TestAddingMemberSkipsSecondScopeResolvingToTheSameDirectory(t *testing.T) {
	cases := map[string]func(t *testing.T, real string) (home, project string){
		"project is home": func(t *testing.T, real string) (string, string) {
			return real, real
		},
		// 主目录经符号链接指向项目根。
		"home is a symlink": func(t *testing.T, real string) (string, string) {
			link := filepath.Join(t.TempDir(), "home-link")
			if err := os.Symlink(real, link); err != nil {
				t.Fatal(err)
			}
			return link, real
		},
		// 主目录里的 Agent 目录经符号链接指向项目里的 Agent 目录(常见于 dotfiles 仓库)。
		"agent directory is a symlink": func(t *testing.T, real string) (string, string) {
			home := t.TempDir()
			if err := os.MkdirAll(filepath.Join(real, ".agents"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(real, ".agents"), filepath.Join(home, ".agents")); err != nil {
				t.Fatal(err)
			}
			return home, real
		},
	}
	for name, arrange := range cases {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			real, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			home, projectPath := arrange(t, real)
			t.Setenv("HOME", home)
			lib := openTestLibrary(t)
			project, err := lib.RegisterProject(ctx, projectPath)
			if err != nil {
				t.Fatal(err)
			}
			global := library.GlobalScope(library.AgentCodex)
			sameDirectory := library.ProjectScope(library.AgentCodex, project.ID)
			globalDir, err := lib.Resolve(ctx, global)
			if err != nil {
				t.Fatal(err)
			}
			projectDir, err := lib.Resolve(ctx, sameDirectory)
			if err != nil {
				t.Fatal(err)
			}
			g, err := lib.CreateGroup(ctx, "g")
			if err != nil {
				t.Fatal(err)
			}
			for _, scope := range []library.Scope{global, sameDirectory} {
				if _, err := lib.InstallGroup(ctx, g.ID, scope, library.InstallOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			skill := fetchNamedSkill(t, lib, "shared")
			results, err := lib.AddSkillToGroup(ctx, g.ID, skill.ID)
			requireMemberResults(t, results, err, []library.GroupMemberResult{
				{SkillID: skill.ID, Scope: global, Outcome: library.MemberInstalled},
				skippedMember(skill.ID, sameDirectory, library.FailedPrecondition, library.ErrorDetails{Path: projectDir}),
			})
			requireInstalledFolder(t, globalDir, "shared", "# shared\n")
			results, err = lib.InstallGroup(ctx, g.ID, sameDirectory, library.InstallOptions{OnConflict: library.Overwrite})
			requireMemberResults(t, results, err, []library.GroupMemberResult{
				skippedMember(skill.ID, sameDirectory, library.FailedPrecondition, library.ErrorDetails{Path: projectDir}),
			})
			requireSkillScopes(t, lib, []library.SkillScope{{SkillID: skill.ID, Scope: global, Directory: globalDir, Groups: []library.GroupID{g.ID}}})
		})
	}
}
