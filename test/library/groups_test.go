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

func TestGroupCanContainSkillsAcrossSourcesAndSkillsCanBelongToMultipleGroups(t *testing.T) {
	root := t.TempDir()
	firstSource := filepath.Join(root, "source-one")
	secondSource := filepath.Join(root, "source-two")
	writeLocalSkill(t, firstSource, "# One\n")
	writeLocalSkill(t, secondSource, "# Two\n")
	lib := openTestLibrary(t)
	first, err := fetchLocalDir(t.Context(), lib, firstSource)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fetchLocalDir(t.Context(), lib, secondSource)
	if err != nil {
		t.Fatal(err)
	}
	debugging, err := lib.CreateGroup(t.Context(), "frontend debugging")
	if err != nil {
		t.Fatal(err)
	}
	shipping, err := lib.CreateGroup(t.Context(), "shipping")
	if err != nil {
		t.Fatal(err)
	}

	for _, membership := range []struct {
		group library.Group
		skill library.Skill
	}{
		{debugging, first},
		{debugging, second},
		{shipping, first},
	} {
		if _, err := lib.AddSkillToGroup(t.Context(), membership.group.ID, membership.skill.ID); err != nil {
			t.Fatal(err)
		}
	}

	if got, want := groupMembers(t, lib, debugging.ID), []library.SkillID{first.ID, second.ID}; !reflect.DeepEqual(got, want) {
		t.Fatalf("debugging members = %v, want %v", got, want)
	}
	if got, want := groupMembers(t, lib, shipping.ID), []library.SkillID{first.ID}; !reflect.DeepEqual(got, want) {
		t.Fatalf("shipping members = %v, want %v", got, want)
	}
}

func TestGroupOperationsReturnNotFoundForUnknownIDs(t *testing.T) {
	ctx := t.Context()
	source := filepath.Join(t.TempDir(), "example")
	writeLocalSkill(t, source, "# Example\n")
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	skill, err := fetchLocalDir(ctx, lib, source)
	if err != nil {
		t.Fatal(err)
	}
	group, err := lib.CreateGroup(ctx, "known")
	if err != nil {
		t.Fatal(err)
	}
	unknownGroup := library.GroupID(9999)
	unknownSkill := library.SkillID(9999)
	scope := library.GlobalScope(library.AgentCodex)

	checks := []struct {
		name string
		err  error
	}{
		{"install group", func() error {
			_, err := lib.InstallGroup(ctx, unknownGroup, scope, library.InstallOptions{})
			return err
		}()},
		{"uninstall group", lib.UninstallGroup(ctx, unknownGroup, scope)},
		{"plan group install", func() error {
			_, err := lib.PlanGroupInstall(ctx, unknownGroup, scope)
			return err
		}()},
		{"rename group", func() error {
			_, err := lib.RenameGroup(ctx, unknownGroup, "missing")
			return err
		}()},
		{"delete group", lib.DeleteGroup(ctx, unknownGroup)},
		{"add to unknown group", func() error {
			_, err := lib.AddSkillToGroup(ctx, unknownGroup, skill.ID)
			return err
		}()},
		{"add unknown skill", func() error {
			_, err := lib.AddSkillToGroup(ctx, group.ID, unknownSkill)
			return err
		}()},
	}
	for _, check := range checks {
		if !library.IsCode(check.err, library.NotFound) {
			t.Errorf("%s error = %v, want NotFound", check.name, check.err)
		}
	}
}

func TestGroupLifecycleKeepsSkillsAndSkillScopes(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	source := filepath.Join(t.TempDir(), "group-skill")
	writeLocalSkill(t, source, "group\n")
	skill, err := fetchLocalDir(ctx, lib, source)
	if err != nil {
		t.Fatal(err)
	}
	groupOnlySource := filepath.Join(t.TempDir(), "group-only")
	writeLocalSkill(t, groupOnlySource, "group only\n")
	groupOnly, err := fetchLocalDir(ctx, lib, groupOnlySource)
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
	for _, group := range []library.Group{first, second} {
		if _, err := lib.AddSkillToGroup(ctx, group.ID, skill.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := lib.AddSkillToGroup(ctx, first.ID, groupOnly.ID); err != nil {
		t.Fatal(err)
	}
	renamed, err := lib.RenameGroup(ctx, first.ID, "renamed")
	if err != nil {
		t.Fatal(err)
	}
	second.Members = []library.SkillID{skill.ID}
	groups, err := lib.ListGroups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := groups, []library.Group{renamed, second}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ListGroups() = %#v, want %#v", got, want)
	}
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.Resolve(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.InstallGroup(ctx, first.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.InstallGroup(ctx, second.ID, scope, library.InstallOptions{OnConflict: library.Overwrite}); err != nil {
		t.Fatal(err)
	}
	if err := lib.DeleteGroup(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, skill.Name, "SKILL.md")); err != nil {
		t.Fatalf("DeleteGroup removed installed folder: %v", err)
	}
	if _, err := lib.GetSkill(ctx, skill.ID); err != nil {
		t.Fatalf("DeleteGroup removed skill: %v", err)
	}
	skillScopes, err := lib.ListSkillScopes(ctx)
	wantSkillScopes := []library.SkillScope{
		{SkillID: skill.ID, Scope: scope, Directory: directory, Groups: []library.GroupID{second.ID}},
		{SkillID: groupOnly.ID, Scope: scope, Directory: directory, Direct: true},
	}
	if err != nil || !reflect.DeepEqual(withoutCreatedAt(skillScopes), wantSkillScopes) {
		t.Fatalf("skill scopes after DeleteGroup = %#v, %v; want %#v", skillScopes, err, wantSkillScopes)
	}
	if _, err := os.Stat(filepath.Join(directory, groupOnly.Name, "SKILL.md")); err != nil {
		t.Fatalf("DeleteGroup removed group-only installed folder: %v", err)
	}
	// 删组转为直接安装后,仍可按 Remove 策略连同技能文件夹一起移除。
	if err := lib.RemoveSkill(ctx, groupOnly.ID, library.RemoveSkillOptions{Folders: library.Remove}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, groupOnly.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("RemoveSkill(Remove) left skill scope: %v", err)
	}
	skillScopes, err = lib.ListSkillScopes(ctx)
	if err != nil || !reflect.DeepEqual(withoutCreatedAt(skillScopes), wantSkillScopes[:1]) {
		t.Fatalf("skill scopes after RemoveSkill = %#v, %v; want %#v", skillScopes, err, wantSkillScopes[:1])
	}
	groups, err = lib.ListGroups(ctx)
	if err != nil || !reflect.DeepEqual(groups, []library.Group{second}) {
		t.Fatalf("groups after DeleteGroup = %#v, %v", groups, err)
	}
	if err := lib.DeleteGroup(ctx, first.ID); !library.IsCode(err, library.NotFound) {
		t.Fatalf("second DeleteGroup error = %v, want NotFound", err)
	}
	if err := lib.RemoveSkillFromGroup(ctx, second.ID, skill.ID, library.RemoveFromGroupOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := groupMembers(t, lib, second.ID); !reflect.DeepEqual(got, []library.SkillID{}) {
		t.Fatalf("members after remove = %#v, want empty list", got)
	}
}

func TestCreateGroupMakesOccupiedNamesUniqueAndRecordsCreatedAt(t *testing.T) {
	lib := openTestLibrary(t)
	ctx := t.Context()
	start := time.Now().UTC()
	first, err := lib.CreateGroup(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	second, err := lib.CreateGroup(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	third, err := lib.CreateGroup(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	end := time.Now().UTC()
	if first.Name != "a" || second.Name != "a#2" || third.Name != "a#3" {
		t.Fatalf("CreateGroup names = %q, %q, %q; want a, a#2, a#3", first.Name, second.Name, third.Name)
	}
	for _, group := range []library.Group{first, second, third} {
		if group.CreatedAt.Before(start) || group.CreatedAt.After(end) {
			t.Fatalf("CreateGroup CreatedAt = %v, want in [%v, %v]", group.CreatedAt, start, end)
		}
		if group.Members == nil || len(group.Members) != 0 {
			t.Fatalf("CreateGroup Members = %#v, want empty list", group.Members)
		}
	}
	if err := lib.DeleteGroup(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	reused, err := lib.CreateGroup(ctx, "a")
	if err != nil || reused.Name != "a#2" {
		t.Fatalf("CreateGroup after deleting a#2 = %#v, %v; want name a#2", reused, err)
	}
	spaced, err := lib.CreateGroup(ctx, "  a  ")
	if err != nil || spaced.Name != "a#4" {
		t.Fatalf("CreateGroup trimmed occupied name = %#v, %v; want a#4", spaced, err)
	}
	listed, err := lib.ListGroups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []library.Group{first, third, reused, spaced}
	if !reflect.DeepEqual(listed, want) {
		t.Fatalf("ListGroups() = %#v, want %#v", listed, want)
	}
}

func TestRenameGroupMakesOccupiedNamesUniqueAndKeepsOwnName(t *testing.T) {
	lib := openTestLibrary(t)
	ctx := t.Context()
	a, err := lib.CreateGroup(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := lib.CreateGroup(ctx, "b")
	if err != nil {
		t.Fatal(err)
	}
	renamed, err := lib.RenameGroup(ctx, b.ID, "a")
	if err != nil || renamed.ID != b.ID || renamed.Name != "a#2" || !renamed.CreatedAt.Equal(b.CreatedAt) {
		t.Fatalf("RenameGroup(b → a) = %#v, %v; want id %d name a#2 created_at %v", renamed, err, b.ID, b.CreatedAt)
	}
	same, err := lib.RenameGroup(ctx, a.ID, "a")
	if err != nil || !reflect.DeepEqual(same, a) {
		t.Fatalf("RenameGroup(a → a) = %#v, %v; want %#v", same, err, a)
	}
	trimmed, err := lib.RenameGroup(ctx, a.ID, "  a  ")
	if err != nil || !reflect.DeepEqual(trimmed, a) {
		t.Fatalf("RenameGroup(a → \"  a  \") = %#v, %v; want %#v", trimmed, err, a)
	}
}

func TestGroupValidationAndDuplicateMembershipCarryDetails(t *testing.T) {
	lib := openTestLibrary(t)
	_, err := lib.CreateGroup(t.Context(), " ")
	requireErrorDetails(t, err, library.InvalidArgument, library.ErrorDetails{Field: "name"})
	group, err := lib.CreateGroup(t.Context(), "named")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.RenameGroup(t.Context(), group.ID, ""); err == nil {
		t.Fatal("RenameGroup accepted a blank name")
	} else {
		requireErrorDetails(t, err, library.InvalidArgument, library.ErrorDetails{Field: "name"})
	}
	skill := fetchNamedSkill(t, lib, "member")
	if _, err := lib.AddSkillToGroup(t.Context(), group.ID, skill.ID); err != nil {
		t.Fatal(err)
	}
	_, err = lib.AddSkillToGroup(t.Context(), group.ID, skill.ID)
	requireErrorDetails(t, err, library.AlreadyExists, library.ErrorDetails{SkillID: skill.ID})
}

func TestListGroupsListsEveryGroupWithAscendingMembers(t *testing.T) {
	lib := openTestLibrary(t)
	f := seedGroupsWithMembers(t, lib)

	// 不属于任何组的 loner 不出现在任何 Members 里,由精确比对保证。
	requireGroups(t, lib, []library.Group{
		withMembers(f.empty),
		withMembers(f.both, f.first.ID, f.second.ID),
		withMembers(f.single, f.second.ID),
	})
}

func TestListGroupsReflectsMembershipChanges(t *testing.T) {
	ctx := t.Context()
	lib := openTestLibrary(t)
	f := seedGroupsWithMembers(t, lib)

	if _, err := lib.AddSkillToGroup(ctx, f.empty.ID, f.loner.ID); err != nil {
		t.Fatal(err)
	}
	if err := lib.RemoveSkillFromGroup(ctx, f.both.ID, f.second.ID, library.RemoveFromGroupOptions{}); err != nil {
		t.Fatal(err)
	}
	requireGroups(t, lib, []library.Group{
		withMembers(f.empty, f.loner.ID),
		withMembers(f.both, f.first.ID),
		withMembers(f.single, f.second.ID),
	})
	if err := lib.RemoveSkill(ctx, f.first.ID, library.RemoveSkillOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := lib.DeleteGroup(ctx, f.single.ID); err != nil {
		t.Fatal(err)
	}
	requireGroups(t, lib, []library.Group{
		withMembers(f.empty, f.loner.ID),
		withMembers(f.both),
	})
}

func TestListGroupsWithoutGroupsIsEmpty(t *testing.T) {
	lib := openTestLibrary(t)
	requireGroups(t, lib, []library.Group{})
	fetchNamedSkill(t, lib, "ungrouped")
	requireGroups(t, lib, []library.Group{})
}

func TestRenameGroupReturnsCurrentMembers(t *testing.T) {
	lib := openTestLibrary(t)
	f := seedGroupsWithMembers(t, lib)

	renamed, err := lib.RenameGroup(t.Context(), f.both.ID, "renamed")
	want := withMembers(f.both, f.first.ID, f.second.ID)
	want.Name = "renamed"
	if err != nil || !reflect.DeepEqual(renamed, want) {
		t.Fatalf("RenameGroup() = %#v, %v; want %#v", renamed, err, want)
	}
	same, err := lib.RenameGroup(t.Context(), f.single.ID, "single")
	if want := withMembers(f.single, f.second.ID); err != nil || !reflect.DeepEqual(same, want) {
		t.Fatalf("RenameGroup() to own name = %#v, %v; want %#v", same, err, want)
	}
}

// groupsWithMembersFixture 是成员关系读取测试的共同起点:empty 无成员,both 含 first 与 second,single 只含 second,loner 不属于任何组。
type groupsWithMembersFixture struct {
	first, second, loner library.Skill
	empty, both, single  library.Group
}

func seedGroupsWithMembers(t *testing.T, lib *library.Library) groupsWithMembersFixture {
	t.Helper()
	ctx := t.Context()
	f := groupsWithMembersFixture{
		first:  fetchNamedSkill(t, lib, "first"),
		second: fetchNamedSkill(t, lib, "second"),
		loner:  fetchNamedSkill(t, lib, "loner"),
	}
	for _, group := range []struct {
		dst  *library.Group
		name string
	}{{&f.empty, "empty"}, {&f.both, "both"}, {&f.single, "single"}} {
		created, err := lib.CreateGroup(ctx, group.name)
		if err != nil {
			t.Fatal(err)
		}
		*group.dst = created
	}
	// 故意倒序加入,验证成员按 ID 升序而非加入顺序。
	for _, membership := range []struct {
		group library.GroupID
		skill library.SkillID
	}{
		{f.both.ID, f.second.ID},
		{f.both.ID, f.first.ID},
		{f.single.ID, f.second.ID},
	} {
		if _, err := lib.AddSkillToGroup(ctx, membership.group, membership.skill); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// withMembers 返回带上给定成员的 group,用于和 ListGroups 的结果比对。
func withMembers(group library.Group, members ...library.SkillID) library.Group {
	group.Members = append([]library.SkillID{}, members...)
	return group
}

func requireGroups(t *testing.T, lib *library.Library, want []library.Group) {
	t.Helper()
	got, err := lib.ListGroups(t.Context())
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("ListGroups() = %#v, %v; want %#v", got, err, want)
	}
}

// requireSkillScopes 断言全部 SkillScope(忽略首次安装时间)。
func requireSkillScopes(t *testing.T, lib *library.Library, want []library.SkillScope) {
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

	if got, want := groupMembers(t, lib, g.ID), []library.SkillID{holder.ID, edited.ID, untracked.ID, namesake.ID}; !reflect.DeepEqual(got, want) {
		t.Fatalf("group members = %v, want %v", got, want)
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
	if got := groupMembers(t, lib, g.ID); len(got) != 0 {
		t.Fatalf("group members after failed add = %v, want none", got)
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
	if got := groupMembers(t, lib, g.ID); !reflect.DeepEqual(got, []library.SkillID{skill.ID}) {
		t.Fatalf("group members after failed uninstall = %v, want [%d]", got, skill.ID)
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
	if got := groupMembers(t, lib, g.ID); !reflect.DeepEqual(got, []library.SkillID{present.ID, skill.ID}) {
		t.Fatalf("group members = %v, want [%d %d]", got, present.ID, skill.ID)
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
