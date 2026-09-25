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

// requireProjections 断言全部投影(忽略首次激活时间)。
func requireProjections(t *testing.T, lib library.Library, want []library.Projection) {
	t.Helper()
	projections, err := lib.ListProjections(t.Context())
	if err != nil || !reflect.DeepEqual(withoutCreatedAt(projections), want) {
		t.Fatalf("ListProjections() = %#v, %v; want %#v", projections, err, want)
	}
}

// requireNoProjectionFolder 断言 directory 下没有 name 文件夹。
func requireNoProjectionFolder(t *testing.T, directory, name string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(directory, name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s/%s exists: %v", directory, name, err)
	}
}

// issue #85 的复现:组已激活到全局与项目两个 Scope 后,加入的成员投影到两处;移出的成员(默认 keep)
// 改为直接激活,可以单独停用,不再卡在"只经由组激活"。
func TestGroupMembershipFollowsGroupActivationInEveryScope(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	a := installNamedSkill(t, lib, "a")
	b := installNamedSkill(t, lib, "b")
	c := installNamedSkill(t, lib, "c")
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
		if _, err := lib.ActivateGroup(ctx, g.ID, scope, library.ActivateOptions{}); err != nil {
			t.Fatal(err)
		}
	}

	results, err := lib.AddSkillToGroup(ctx, g.ID, c.ID)
	// 按 ListGroupActivations 的顺序:claude-code 在 codex 之前。
	requireMemberResults(t, results, err, []library.GroupMemberResult{
		{SkillID: c.ID, Scope: projectScope, Outcome: library.MemberProjected},
		{SkillID: c.ID, Scope: global, Outcome: library.MemberProjected},
	})
	requireProjection(t, globalDir, "c", "# c\n")
	requireProjection(t, projectDir, "c", "# c\n")

	if err := lib.RemoveSkillFromGroup(ctx, g.ID, a.ID, library.RemoveFromGroupOptions{}); err != nil {
		t.Fatal(err)
	}
	via := []library.GroupID{g.ID}
	requireProjections(t, lib, []library.Projection{
		{SkillID: a.ID, Scope: projectScope, Directory: projectDir, Direct: true},
		{SkillID: a.ID, Scope: global, Directory: globalDir, Direct: true},
		{SkillID: b.ID, Scope: projectScope, Directory: projectDir, Groups: via},
		{SkillID: b.ID, Scope: global, Directory: globalDir, Groups: via},
		{SkillID: c.ID, Scope: projectScope, Directory: projectDir, Groups: via},
		{SkillID: c.ID, Scope: global, Directory: globalDir, Groups: via},
	})
	requireProjection(t, globalDir, "a", "# a\n")
	if err := lib.Deactivate(ctx, a.ID, global); err != nil {
		t.Fatalf("Deactivate removed member = %v, want success", err)
	}
	requireNoProjectionFolder(t, globalDir, "a")

	// 停用整组只撤走本组撑着的投影;移出后转为直接激活的 a 仍在项目 Scope。
	for _, scope := range []library.Scope{projectScope, global} {
		if err := lib.DeactivateGroup(ctx, g.ID, scope); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"b", "c"} {
		requireNoProjectionFolder(t, globalDir, name)
		requireNoProjectionFolder(t, projectDir, name)
	}
	requireProjection(t, projectDir, "a", "# a\n")
	requireProjections(t, lib, []library.Projection{{SkillID: a.ID, Scope: projectScope, Directory: projectDir, Direct: true}})
	if activations, err := lib.ListGroupActivations(ctx); err != nil || len(activations) != 0 {
		t.Fatalf("ListGroupActivations() after DeactivateGroup = %#v, %v; want none", activations, err)
	}
}

// 空组也能激活:组激活带时间记录下来;之后加入的成员随之投影。成员全部移出(withdraw)后组激活仍在,
// 再次加入又会投影。再次 ActivateGroup 不改变组激活的时间。
func TestEmptyGroupActivationIsRecordedAndCoversLaterMembers(t *testing.T) {
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
	results, err := lib.ActivateGroup(ctx, g.ID, scope, library.ActivateOptions{})
	requireMemberResults(t, results, err, nil)
	end := time.Now().UTC()
	activations, err := lib.ListGroupActivations(ctx)
	if err != nil || len(activations) != 1 || activations[0].GroupID != g.ID || activations[0].Scope != scope ||
		activations[0].ActivatedAt.Before(start) || activations[0].ActivatedAt.After(end) {
		t.Fatalf("ListGroupActivations() = %#v, %v; want %d at %v within [%v, %v]", activations, err, g.ID, scope, start, end)
	}

	skill := installNamedSkill(t, lib, "late")
	for range 2 {
		results, err = lib.AddSkillToGroup(ctx, g.ID, skill.ID)
		requireMemberResults(t, results, err, []library.GroupMemberResult{{SkillID: skill.ID, Scope: scope, Outcome: library.MemberProjected}})
		requireProjection(t, directory, "late", "# late\n")
		requireProjections(t, lib, []library.Projection{{SkillID: skill.ID, Scope: scope, Directory: directory, Groups: []library.GroupID{g.ID}}})

		if err := lib.RemoveSkillFromGroup(ctx, g.ID, skill.ID, library.RemoveFromGroupOptions{Projections: library.WithdrawProjection}); err != nil {
			t.Fatal(err)
		}
		requireNoProjectionFolder(t, directory, "late")
		requireProjections(t, lib, nil)
	}

	if _, err := lib.ActivateGroup(ctx, g.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	again, err := lib.ListGroupActivations(ctx)
	if err != nil || !reflect.DeepEqual(again, activations) {
		t.Fatalf("ListGroupActivations() after re-activation = %#v, %v; want %#v", again, err, activations)
	}
}

// 技能同属两个已激活的组时,移出其中一个(无论 keep 还是 withdraw)投影仍由另一组撑着,不补直接激活;
// withdraw 只移除因此没有任何激活的投影,有直接激活的保留。
func TestRemovingMemberFromOneOfTwoActiveGroups(t *testing.T) {
	for _, policy := range []library.MemberRemovalPolicy{library.KeepProjection, library.WithdrawProjection} {
		t.Run(string(policy), func(t *testing.T) {
			ctx := t.Context()
			t.Setenv("HOME", t.TempDir())
			lib := openTestLibrary(t)
			shared := installNamedSkill(t, lib, "shared")
			direct := installNamedSkill(t, lib, "direct")
			scope := library.GlobalScope(library.AgentCodex)
			directory, err := lib.Resolve(ctx, scope)
			if err != nil {
				t.Fatal(err)
			}
			if err := lib.Activate(ctx, direct.ID, scope, library.ActivateOptions{}); err != nil {
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
				if _, err := lib.ActivateGroup(ctx, group.ID, scope, library.ActivateOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			results, err := lib.AddSkillToGroup(ctx, groups[0].ID, direct.ID)
			requireMemberResults(t, results, err, []library.GroupMemberResult{{SkillID: direct.ID, Scope: scope, Outcome: library.MemberAlreadyProjected}})

			opts := library.RemoveFromGroupOptions{Projections: policy}
			for _, skill := range []library.Skill{shared, direct} {
				if err := lib.RemoveSkillFromGroup(ctx, groups[0].ID, skill.ID, opts); err != nil {
					t.Fatal(err)
				}
			}
			requireProjections(t, lib, []library.Projection{
				{SkillID: shared.ID, Scope: scope, Directory: directory, Groups: []library.GroupID{groups[1].ID}},
				{SkillID: direct.ID, Scope: scope, Directory: directory, Direct: true},
			})
			requireProjection(t, directory, "shared", "# shared\n")
			requireProjection(t, directory, "direct", "# direct\n")

			// 另一组也移出后:keep 转为直接激活,withdraw 连文件夹移除。
			if err := lib.RemoveSkillFromGroup(ctx, groups[1].ID, shared.ID, opts); err != nil {
				t.Fatal(err)
			}
			if policy == library.KeepProjection {
				requireProjection(t, directory, "shared", "# shared\n")
				requireProjections(t, lib, []library.Projection{
					{SkillID: shared.ID, Scope: scope, Directory: directory, Direct: true},
					{SkillID: direct.ID, Scope: scope, Directory: directory, Direct: true},
				})
			} else {
				requireNoProjectionFolder(t, directory, "shared")
				requireProjections(t, lib, []library.Projection{{SkillID: direct.ID, Scope: scope, Directory: directory, Direct: true}})
			}
			requireNoStagedEntries(t, directory)
		})
	}
}

// 加入已激活的组时:已有投影(即使被手动改过)只补组激活、不写盘;有未记录文件夹或与组内成员同名的跳过,
// 成员关系照常成立,之后再次 ActivateGroup(Overwrite)补齐未记录文件夹那一项。
func TestAddingMemberToActiveGroupReportsEachScope(t *testing.T) {
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
	holder := installSkillWithBody(t, lib, "dup", "# holder\n")
	if _, err := lib.AddSkillToGroup(ctx, g.ID, holder.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.ActivateGroup(ctx, g.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}

	edited := installNamedSkill(t, lib, "edited")
	if err := lib.Activate(ctx, edited.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(directory, "edited", "SKILL.md"), []byte("by hand\n"), 0o644)
	results, err := lib.AddSkillToGroup(ctx, g.ID, edited.ID)
	requireMemberResults(t, results, err, []library.GroupMemberResult{{SkillID: edited.ID, Scope: scope, Outcome: library.MemberAlreadyProjected}})
	requireProjection(t, directory, "edited", "by hand\n")

	untracked := installNamedSkill(t, lib, "untracked")
	writeFile(t, filepath.Join(directory, "untracked", "manual.txt"), []byte("manual"), 0o644)
	results, err = lib.AddSkillToGroup(ctx, g.ID, untracked.ID)
	requireMemberResults(t, results, err, []library.GroupMemberResult{
		skippedMember(untracked.ID, scope, library.AlreadyExists, library.ErrorDetails{Path: filepath.Join(directory, "untracked")}),
	})

	namesake := installSkillWithBody(t, lib, "dup", "# namesake\n")
	results, err = lib.AddSkillToGroup(ctx, g.ID, namesake.ID)
	requireMemberResults(t, results, err, []library.GroupMemberResult{
		skippedMember(namesake.ID, scope, library.FailedPrecondition, library.ErrorDetails{Name: "dup", Path: filepath.Join(directory, "dup")}),
	})
	requireProjection(t, directory, "dup", "# holder\n")

	members, err := lib.ListGroupSkills(ctx, g.ID)
	if want := []library.SkillID{holder.ID, edited.ID, untracked.ID, namesake.ID}; err != nil || !reflect.DeepEqual(summaryIDs(members), want) {
		t.Fatalf("ListGroupSkills() = %v, %v; want %v", summaryIDs(members), err, want)
	}
	via := []library.GroupID{g.ID}
	requireProjections(t, lib, []library.Projection{
		{SkillID: holder.ID, Scope: scope, Directory: directory, Groups: via},
		{SkillID: edited.ID, Scope: scope, Directory: directory, Direct: true, Groups: via},
	})

	// 组内同名的成员此后在组激活时都被跳过,包括已投影的 holder 以外的那一个。
	results, err = lib.ActivateGroup(ctx, g.ID, scope, library.ActivateOptions{OnConflict: library.Overwrite})
	requireMemberResults(t, results, err, []library.GroupMemberResult{
		{SkillID: holder.ID, Scope: scope, Outcome: library.MemberAlreadyProjected},
		{SkillID: edited.ID, Scope: scope, Outcome: library.MemberAlreadyProjected},
		{SkillID: untracked.ID, Scope: scope, Outcome: library.MemberProjected},
		skippedMember(namesake.ID, scope, library.FailedPrecondition, library.ErrorDetails{Name: "dup", Path: filepath.Join(directory, "dup")}),
	})
	requireProjection(t, directory, "untracked", "# untracked\n")
	requireProjection(t, directory, "edited", "by hand\n")
}

// 加入成员时落盘失败,整个操作复原:不留成员关系、投影记录与半成品目录。
func TestAddingMemberRollsBackMembershipWhenProjectionFails(t *testing.T) {
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
	if _, err := lib.ActivateGroup(ctx, g.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	skill := installNamedSkill(t, lib, "blocked")
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
	requireNoProjectionFolder(t, directory, "blocked")
	requireProjections(t, lib, nil)
	requireNoStagedEntries(t, directory)
}

// 注销项目清掉该项目各 Scope 上的组激活;删除分组清掉它的全部组激活,仅由它撑着的投影转为直接激活。
func TestUnregisterProjectAndDeleteGroupClearGroupActivations(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	skill := installNamedSkill(t, lib, "member")
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
	for _, activation := range []library.GroupActivation{{GroupID: first.ID, Scope: global}, {GroupID: first.ID, Scope: projectScope}, {GroupID: second.ID, Scope: projectScope}} {
		if _, err := lib.ActivateGroup(ctx, activation.GroupID, activation.Scope, library.ActivateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := lib.UnregisterProject(ctx, project.ID, library.UnregisterProjectOptions{}); err != nil {
		t.Fatal(err)
	}
	activations, err := lib.ListGroupActivations(ctx)
	if want := []library.GroupActivation{{GroupID: first.ID, Scope: global}}; err != nil || !reflect.DeepEqual(withoutGroupActivatedAt(activations), want) {
		t.Fatalf("ListGroupActivations() after UnregisterProject = %#v, %v; want %#v", activations, err, want)
	}

	if err := lib.DeleteGroup(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if activations, err := lib.ListGroupActivations(ctx); err != nil || len(activations) != 0 {
		t.Fatalf("ListGroupActivations() after DeleteGroup = %#v, %v; want none", activations, err)
	}
	requireProjections(t, lib, []library.Projection{{SkillID: skill.ID, Scope: global, Directory: globalDir, Direct: true}})
	requireProjection(t, globalDir, "member", "# member\n")
}

// withdraw 先记录后删盘(#75):某个 Scope 的文件夹移不走时整体复原——已移走的其他 Scope 文件夹放回原处,
// 成员关系与投影记录不变。
func TestWithdrawRestoresEverythingWhenAFolderCannotBeRemoved(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	skill := installNamedSkill(t, lib, "member")
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
		if _, err := lib.ActivateGroup(ctx, g.ID, scope, library.ActivateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	makeScopeReadOnly(t, projectDir)
	err = lib.RemoveSkillFromGroup(ctx, g.ID, skill.ID, library.RemoveFromGroupOptions{Projections: library.WithdrawProjection})
	if err == nil {
		t.Fatal("RemoveSkillFromGroup(withdraw) succeeded despite a read-only scope")
	}
	if err := os.Chmod(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	requireProjection(t, globalDir, "member", "# member\n")
	requireProjection(t, projectDir, "member", "# member\n")
	requireNoStagedEntries(t, globalDir)
	members, err := lib.ListGroupSkills(ctx, g.ID)
	if err != nil || !reflect.DeepEqual(summaryIDs(members), []library.SkillID{skill.ID}) {
		t.Fatalf("ListGroupSkills() after failed withdraw = %v, %v; want [%d]", summaryIDs(members), err, skill.ID)
	}
	via := []library.GroupID{g.ID}
	requireProjections(t, lib, []library.Projection{
		{SkillID: skill.ID, Scope: global, Directory: globalDir, Groups: via},
		{SkillID: skill.ID, Scope: projectScope, Directory: projectDir, Groups: via},
	})
}

// 作用域根目录(项目为已登记路径)不在本机时 Library 不创建它:Activate / ActivateGroup 报 FailedPrecondition,
// 不写盘、不留记录、不记组激活;加入成员跳过该 Scope,成员关系照加。根目录存在而 Agent 布局目录不存在时照常创建。
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
	// 根目录存在、.claude/skills 还不存在:组激活照常创建布局目录。
	for _, target := range []library.Scope{global, scope} {
		if _, err := lib.ActivateGroup(ctx, g.ID, target, library.ActivateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	present := installNamedSkill(t, lib, "present")
	if _, err := lib.AddSkillToGroup(ctx, g.ID, present.ID); err != nil {
		t.Fatal(err)
	}
	requireProjection(t, directory, "present", "# present\n")

	// 项目被移走:根目录不存在。
	if err := os.RemoveAll(project.Path); err != nil {
		t.Fatal(err)
	}
	missing := library.ErrorDetails{Path: project.Path, ProjectID: project.ID}
	skill := installNamedSkill(t, lib, "late")
	requireErrorDetails(t, lib.Activate(ctx, skill.ID, scope, library.ActivateOptions{}), library.FailedPrecondition, missing)
	other, err := lib.CreateGroup(ctx, "other")
	if err != nil {
		t.Fatal(err)
	}
	_, err = lib.ActivateGroup(ctx, other.ID, scope, library.ActivateOptions{})
	requireErrorDetails(t, err, library.FailedPrecondition, missing)

	results, err := lib.AddSkillToGroup(ctx, g.ID, skill.ID)
	requireMemberResults(t, results, err, []library.GroupMemberResult{
		skippedMember(skill.ID, scope, library.FailedPrecondition, missing),
		{SkillID: skill.ID, Scope: global, Outcome: library.MemberProjected},
	})
	members, err := lib.ListGroupSkills(ctx, g.ID)
	if err != nil || !reflect.DeepEqual(summaryIDs(members), []library.SkillID{present.ID, skill.ID}) {
		t.Fatalf("ListGroupSkills() = %v, %v; want [%d %d]", summaryIDs(members), err, present.ID, skill.ID)
	}
	if _, err := os.Lstat(project.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing project root was created: %v", err)
	}
	activations, err := lib.ListGroupActivations(ctx)
	want := []library.GroupActivation{{GroupID: g.ID, Scope: scope}, {GroupID: g.ID, Scope: global}}
	if err != nil || !reflect.DeepEqual(withoutGroupActivatedAt(activations), want) {
		t.Fatalf("ListGroupActivations() = %#v, %v; want %#v", activations, err, want)
	}
	globalDir, err := lib.Resolve(ctx, global)
	if err != nil {
		t.Fatal(err)
	}
	via := []library.GroupID{g.ID}
	requireProjections(t, lib, []library.Projection{
		{SkillID: present.ID, Scope: scope, Directory: directory, Groups: via},
		{SkillID: present.ID, Scope: global, Directory: globalDir, Groups: via},
		{SkillID: skill.ID, Scope: global, Directory: globalDir, Groups: via},
	})
}

// 同一次加入成员里两个 Scope 解析到同一目录(已登记项目就是主目录)时,先处理的那个投影,后一个按占用跳过;
// 再次对后一个 Scope 激活组也不会让两个 Scope 共用一个文件夹。
func TestAddingMemberSkipsSecondScopeResolvingToTheSameDirectory(t *testing.T) {
	ctx := t.Context()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	project, err := lib.RegisterProject(ctx, home)
	if err != nil {
		t.Fatal(err)
	}
	global := library.GlobalScope(library.AgentCodex)
	sameDirectory := library.ProjectScope(library.AgentCodex, project.ID)
	directory, err := lib.Resolve(ctx, global)
	if err != nil {
		t.Fatal(err)
	}
	g, err := lib.CreateGroup(ctx, "g")
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []library.Scope{global, sameDirectory} {
		if _, err := lib.ActivateGroup(ctx, g.ID, scope, library.ActivateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	skill := installNamedSkill(t, lib, "shared")
	results, err := lib.AddSkillToGroup(ctx, g.ID, skill.ID)
	requireMemberResults(t, results, err, []library.GroupMemberResult{
		{SkillID: skill.ID, Scope: global, Outcome: library.MemberProjected},
		skippedMember(skill.ID, sameDirectory, library.FailedPrecondition, library.ErrorDetails{Path: directory}),
	})
	requireProjection(t, directory, "shared", "# shared\n")
	results, err = lib.ActivateGroup(ctx, g.ID, sameDirectory, library.ActivateOptions{OnConflict: library.Overwrite})
	requireMemberResults(t, results, err, []library.GroupMemberResult{
		skippedMember(skill.ID, sameDirectory, library.FailedPrecondition, library.ErrorDetails{Path: directory}),
	})
	requireProjections(t, lib, []library.Projection{{SkillID: skill.ID, Scope: global, Directory: directory, Groups: []library.GroupID{g.ID}}})
}
