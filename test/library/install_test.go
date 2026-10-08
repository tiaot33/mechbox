package library_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/tiaot33/skills-tui/internal/library"
)

func TestPlanInstallReadyAndInstalled(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	ctx := t.Context()
	source := filepath.Join(t.TempDir(), "example")
	writeLocalSkill(t, source, "# Example")
	skill, err := fetchLocalDir(ctx, lib, source)
	if err != nil {
		t.Fatal(err)
	}
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.ScopeDirectory(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := lib.PlanInstall(ctx, skill.ID, scope)
	if err != nil || plan.Status != library.InstallReady || plan.SkillID != skill.ID || plan.Name != skill.Name || plan.Folder != filepath.Join(directory, skill.Name) {
		t.Fatalf("plan = %#v, %v", plan, err)
	}
	if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("plan created directory: %v", err)
	}
	if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, missing := range []bool{false, true} {
		if missing {
			if err := os.RemoveAll(plan.Folder); err != nil {
				t.Fatal(err)
			}
		}
		got, err := lib.PlanInstall(ctx, skill.ID, scope)
		if err != nil || got.Status != library.InstallAlreadyInstalled {
			t.Fatalf("installed = %#v, %v", got, err)
		}
	}
}

func TestPlanInstallOccupancyAndUntracked(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	ctx := t.Context()
	fetchSame := func() library.Skill {
		source := filepath.Join(t.TempDir(), "same")
		writeLocalSkill(t, source, "# Same")
		skill, err := fetchLocalDir(ctx, lib, source)
		if err != nil {
			t.Fatal(err)
		}
		return skill
	}
	a, b := fetchSame(), fetchSame()
	scope := library.GlobalScope(library.AgentCodex)
	if err := lib.Install(ctx, a.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	plan, err := lib.PlanInstall(ctx, b.ID, scope)
	if err != nil || plan.Status != library.InstallOccupied || plan.OccupierSkillID != a.ID {
		t.Fatalf("occupied = %#v, %v", plan, err)
	}
	// 被另一个技能占用时,同意覆盖也不装。
	for _, overwrite := range []bool{false, true} {
		requireErrorDetails(t, lib.Install(ctx, b.ID, scope, library.InstallOptions{Overwrite: overwrite}), library.FailedPrecondition, library.ErrorDetails{SkillID: a.ID, Name: a.Name, Path: plan.Folder})
	}
	project, err := lib.RegisterProject(ctx, home)
	if err != nil {
		t.Fatal(err)
	}
	other := library.ProjectScope(library.AgentCodex, project.ID)
	plan, err = lib.PlanInstall(ctx, a.ID, other)
	// 本技能的另一个作用域恰好算出同一目录:占用者就是本技能自己。
	if err != nil || plan.Status != library.InstallOccupied || plan.OccupierSkillID != a.ID {
		t.Fatalf("cross scope = %#v, %v", plan, err)
	}
	requireErrorDetails(t, lib.Install(ctx, a.ID, other, library.InstallOptions{}), library.FailedPrecondition, library.ErrorDetails{Path: filepath.Dir(plan.Folder)})
	if err := lib.Uninstall(ctx, a.ID, scope); err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"# Same", "user edited"} {
		writeFile(t, filepath.Join(plan.Folder, "SKILL.md"), []byte(content), 0o644)
		got, err := lib.PlanInstall(ctx, b.ID, scope)
		if err != nil || got.Status != library.InstallUntracked || got.OccupierSkillID != 0 {
			t.Fatalf("untracked = %#v, %v", got, err)
		}
		data, err := os.ReadFile(filepath.Join(got.Folder, "SKILL.md"))
		if err != nil || string(data) != content {
			t.Fatalf("plan changed content: %q, %v", data, err)
		}
	}
	requireErrorDetails(t, lib.Install(ctx, b.ID, scope, library.InstallOptions{}), library.AlreadyExists, library.ErrorDetails{Path: plan.Folder})
	if err := lib.Install(ctx, b.ID, scope, library.InstallOptions{Overwrite: true}); err != nil {
		t.Fatal(err)
	}
}

func TestPlanGroupInstallReportsDuplicatesAndMemberStates(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	ctx := t.Context()
	scope := library.GlobalScope(library.AgentCodex)
	group, err := lib.CreateGroup(ctx, "group")
	if err != nil {
		t.Fatal(err)
	}
	empty, err := lib.PlanGroupInstall(ctx, group.ID, scope)
	if err != nil || len(empty.Members) != 0 {
		t.Fatalf("empty = %#v, %v", empty, err)
	}
	for _, name := range []string{"same", "same", "ready"} {
		source := filepath.Join(t.TempDir(), name)
		writeLocalSkill(t, source, "# Skill")
		skill, err := fetchLocalDir(ctx, lib, source)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := lib.AddSkillToGroup(ctx, group.ID, skill.ID); err != nil {
			t.Fatal(err)
		}
	}
	members := groupMembers(t, lib, group.ID)
	if err := lib.Install(ctx, members[0], scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	before, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := lib.PlanGroupInstall(ctx, group.ID, scope)
	if err != nil || len(got.Members) != 3 {
		t.Fatalf("group = %#v, %v", got, err)
	}
	// 同名且尚未安装的成员标为 duplicate(优先于它本来的 occupied),已安装的那个仍是 installed。
	for i, status := range []library.InstallPlanStatus{library.InstallAlreadyInstalled, library.InstallDuplicate, library.InstallReady} {
		if got.Members[i].SkillID != members[i] || got.Members[i].Status != status {
			t.Fatalf("member %d = %#v", i, got.Members[i])
		}
	}
	after, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{})
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("plan changed skill scopes: %#v, %v", after, err)
	}
	if afterMembers := groupMembers(t, lib, group.ID); !reflect.DeepEqual(members, afterMembers) {
		t.Fatalf("plan changed members: %v, want %v", afterMembers, members)
	}
	if err := lib.RemoveSkillFromGroup(ctx, group.ID, members[0], library.RemoveFromGroupOptions{}); err != nil {
		t.Fatal(err)
	}
	got, err = lib.PlanGroupInstall(ctx, group.ID, scope)
	if err != nil || len(got.Members) != 2 || got.Members[0].Status != library.InstallOccupied || got.Members[1].Status != library.InstallReady {
		t.Fatalf("mixed = %#v, %v", got, err)
	}
}

// 安装计划标出的 duplicate 与安装时的跳过一致:两个同名成员都没安装时,即使有未记录文件夹也显示 duplicate,
// 同意覆盖的 InstallGroup 也照样跳过它们、不覆盖该文件夹。
func TestPlanGroupInstallDuplicateMatchesGroupInstall(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	ctx := t.Context()
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.ScopeDirectory(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	group, err := lib.CreateGroup(ctx, "group")
	if err != nil {
		t.Fatal(err)
	}
	var skills []library.Skill
	for _, body := range []string{"# one\n", "# two\n"} {
		skill := fetchSkillWithBody(t, lib, "same", body)
		skills = append(skills, skill)
		if _, err := lib.AddSkillToGroup(ctx, group.ID, skill.ID); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(directory, "same", "manual.txt"), []byte("manual"), 0o644)
	got, err := lib.PlanGroupInstall(ctx, group.ID, scope)
	if err != nil || len(got.Members) != 2 || got.Members[0].Status != library.InstallDuplicate || got.Members[1].Status != library.InstallDuplicate {
		t.Fatalf("PlanGroupInstall() = %#v, %v; want both duplicate", got, err)
	}
	results, err := lib.InstallGroup(ctx, group.ID, scope, library.InstallOptions{Overwrite: true})
	details := library.ErrorDetails{Name: "same", Path: filepath.Join(directory, "same")}
	requireMemberResults(t, results, err, []library.GroupMemberResult{
		skippedMember(skills[0].ID, scope, library.FailedPrecondition, details),
		skippedMember(skills[1].ID, scope, library.FailedPrecondition, details),
	})
	if _, err := os.Stat(filepath.Join(directory, "same", "manual.txt")); err != nil {
		t.Fatalf("untracked folder was overwritten: %v", err)
	}
}

func TestInstallPlansReportMissingAndCanceled(t *testing.T) {
	lib := openTestLibrary(t)
	ctx := t.Context()
	group, err := lib.CreateGroup(ctx, "empty")
	if err != nil {
		t.Fatal(err)
	}
	scope := library.GlobalScope(library.AgentCodex)
	if _, err := lib.PlanInstall(ctx, -1, scope); !library.IsCode(err, library.NotFound) {
		t.Fatalf("unknown skill: %v", err)
	}
	if _, err := lib.PlanGroupInstall(ctx, -1, scope); !library.IsCode(err, library.NotFound) {
		t.Fatalf("unknown group: %v", err)
	}
	if _, err := lib.PlanGroupInstall(ctx, group.ID, library.GlobalScope("missing")); !library.IsCode(err, library.NotFound) {
		t.Fatalf("unknown scope: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := lib.PlanInstall(canceled, -1, scope); !library.IsCode(err, library.Canceled) {
		t.Fatalf("canceled skill: %v", err)
	}
	if _, err := lib.PlanGroupInstall(canceled, group.ID, scope); !library.IsCode(err, library.Canceled) {
		t.Fatalf("canceled group: %v", err)
	}
}

// 单项安装的主路径只选定 Agent(全局 Scope):Agent 目录由 Library 在隔离的 $HOME 下算出,
// 冲突 / Overwrite / Uninstall 的断言全部对着 ScopeDirectory 给出的目录,而不是测试自拟的路径。
func TestInstallAndUninstallGlobalSkill(t *testing.T) {
	for agent, agentDirectory := range map[library.AgentCode]string{
		library.AgentCodex:      ".agents",
		library.AgentClaudeCode: ".claude",
	} {
		t.Run(string(agent), func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "example-skill")
			writeLocalSkill(t, source, "# Example\n")
			writeFile(t, filepath.Join(source, "scripts", "run.sh"), []byte("#!/bin/sh\n"), 0o755)

			home := t.TempDir()
			t.Setenv("HOME", home)
			lib := openTestLibrary(t)
			ctx := t.Context()
			skill, err := fetchLocalDir(ctx, lib, source)
			if err != nil {
				t.Fatal(err)
			}

			scope := library.GlobalScope(agent)
			directory, err := lib.ScopeDirectory(ctx, scope)
			if err != nil {
				t.Fatal(err)
			}
			if directory != filepath.Join(home, agentDirectory, "skills") {
				t.Fatalf("ScopeDirectory(%#v) = %q; want under isolated HOME", scope, directory)
			}
			target := filepath.Join(directory, skill.Name)
			writeFile(t, filepath.Join(target, "old.txt"), []byte("old"), 0o644)
			if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); !library.IsCode(err, library.AlreadyExists) {
				t.Fatalf("Install() conflict error = %v", err)
			} else {
				requireErrorDetails(t, err, library.AlreadyExists, library.ErrorDetails{Path: target})
			}
			if _, err := os.Stat(filepath.Join(target, "old.txt")); err != nil {
				t.Fatalf("failed Install touched the foreign directory: %v", err)
			}
			if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{Overwrite: true}); err != nil {
				t.Fatal(err)
			}
			requireInstalledFolder(t, directory, skill.Name, "# Example\n")
			if _, err := os.Stat(filepath.Join(target, "old.txt")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("overwritten file still exists: %v", err)
			}
			info, err := os.Stat(filepath.Join(target, "scripts", "run.sh"))
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm()&0o111 == 0 {
				t.Fatal("installed script is not executable")
			}
			skillScopes, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{})
			if err != nil {
				t.Fatal(err)
			}
			want := []library.SkillScope{{
				SkillID: skill.ID,
				Scope:   scope,
				Folder:  target,
				Direct:  true,
			}}
			if !reflect.DeepEqual(withoutCreatedAt(skillScopes), want) {
				t.Fatalf("ListSkillScopes() = %#v, want %#v", skillScopes, want)
			}

			if err := lib.Uninstall(ctx, skill.ID, scope); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("uninstalled directory still exists: %v", err)
			}
			if _, err := lib.GetSkill(ctx, skill.ID); err != nil {
				t.Fatalf("Uninstall removed library skill: %v", err)
			}
			skillScopes, err = lib.ListSkillScopes(ctx, library.SkillScopeFilter{})
			if err != nil || len(skillScopes) != 0 {
				t.Fatalf("ListSkillScopes() after uninstall = %#v, %v", skillScopes, err)
			}
			writeFile(t, filepath.Join(target, "manual.txt"), []byte("manual"), 0o644)
			if err := lib.Uninstall(ctx, skill.ID, scope); !library.IsCode(err, library.NotFound) {
				t.Fatalf("Uninstall() not-installed skill error = %v", err)
			}
			if _, err := os.Stat(filepath.Join(target, "manual.txt")); err != nil {
				t.Fatalf("Uninstall() removed an untracked directory: %v", err)
			}

			if err := lib.Install(ctx, library.SkillID(9999), scope, library.InstallOptions{}); !library.IsCode(err, library.NotFound) {
				t.Fatalf("Install() unknown skill error = %v", err)
			}
		})
	}
}

func TestInstallRecordsFirstInstallTimeAndIgnoresInstallingAgain(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	ctx := t.Context()
	source := filepath.Join(t.TempDir(), "timed")
	writeLocalSkill(t, source, "timed\n")
	skill, err := fetchLocalDir(ctx, lib, source)
	if err != nil {
		t.Fatal(err)
	}
	scope := library.GlobalScope(library.AgentCodex)
	start := time.Now().UTC()
	if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	end := time.Now().UTC()
	listed, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{})
	if err != nil || len(listed) != 1 {
		t.Fatalf("ListSkillScopes = %#v, %v", listed, err)
	}
	first := listed[0].CreatedAt
	if first.IsZero() || first.Before(start) || first.After(end) {
		t.Fatalf("CreatedAt = %v, want in [%v, %v]", first, start, end)
	}
	time.Sleep(time.Millisecond)
	if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	installedAgain, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{})
	if err != nil || len(installedAgain) != 1 || !installedAgain[0].CreatedAt.Equal(first) {
		t.Fatalf("installedAgain CreatedAt = %#v, %v; want %v", installedAgain, err, first)
	}
}

// Uninstall 先删记录、把技能文件夹改名暂存、提交后才真正删除:改名失败时记录与文件夹都原样不动;
// 技能文件夹已被手动删掉时仍成功并删掉记录;成功后 Scope 目录下不留暂存残留。
func TestUninstallKeepsLibraryAndDiskTogether(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	present := fetchNamedSkill(t, lib, "present")
	missing := fetchNamedSkill(t, lib, "missing")
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.ScopeDirectory(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range []library.Skill{present, missing} {
		if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	before, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{})
	if err != nil {
		t.Fatal(err)
	}

	makeScopeReadOnly(t, directory)
	err = lib.Uninstall(ctx, present.ID, scope)
	requireLibraryError(t, err, library.Permission)
	if err := os.Chmod(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	after, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{})
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("failed Uninstall changed skill scopes: %#v, %v; want %#v", after, err, before)
	}
	content, err := os.ReadFile(filepath.Join(directory, present.Name, "SKILL.md"))
	if err != nil || string(content) != skillDocument(present.Name, "# present\n") {
		t.Fatalf("failed Uninstall changed skill scope: %q, %v", content, err)
	}
	requireNoStagedEntries(t, directory)

	if err := os.RemoveAll(filepath.Join(directory, missing.Name)); err != nil {
		t.Fatal(err)
	}
	if err := lib.Uninstall(ctx, missing.ID, scope); err != nil {
		t.Fatalf("Uninstall with manually deleted skill scope = %v", err)
	}
	if err := lib.Uninstall(ctx, present.ID, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, present.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Uninstall left installed folder behind: %v", err)
	}
	skillScopes, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{})
	if err != nil || len(skillScopes) != 0 {
		t.Fatalf("ListSkillScopes() after uninstalls = %#v, %v", skillScopes, err)
	}
	requireNoStagedEntries(t, directory)
}

func TestUninstallGroupBackedSkillNamesTheGroup(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	skill := fetchNamedSkill(t, lib, "grouped")
	group, err := lib.CreateGroup(t.Context(), "only")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.AddSkillToGroup(t.Context(), group.ID, skill.ID); err != nil {
		t.Fatal(err)
	}
	scope := library.GlobalScope(library.AgentCodex)
	if _, err := lib.InstallGroup(t.Context(), group.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	requireErrorDetails(t, lib.Uninstall(t.Context(), skill.ID, scope), library.FailedPrecondition, library.ErrorDetails{Name: "only"})
}

// 直接安装与组安装叠加;重复安装不增加安装,撤销组只卸载只由该组安装留着的技能。
func TestInstallAndUninstallGroupPreservesDirectInstall(t *testing.T) {
	for _, overwrite := range []bool{false, true} {
		t.Run(fmt.Sprintf("overwrite=%t", overwrite), func(t *testing.T) {
			ctx := t.Context()
			t.Setenv("HOME", t.TempDir())
			lib := openTestLibrary(t)
			first := fetchExampleSkill(t, lib, "first")
			second := fetchExampleSkill(t, lib, "second")
			group, err := lib.CreateGroup(ctx, "shipping")
			if err != nil {
				t.Fatal(err)
			}
			for _, skill := range []library.Skill{first, second} {
				if _, err := lib.AddSkillToGroup(ctx, group.ID, skill.ID); err != nil {
					t.Fatal(err)
				}
			}
			scope := library.GlobalScope(library.AgentCodex)
			directory, err := lib.ScopeDirectory(ctx, scope)
			if err != nil {
				t.Fatal(err)
			}
			requireLibraryError(t, lib.UninstallGroup(ctx, group.ID, scope), library.NotFound)

			if err := lib.Install(ctx, first.ID, scope, library.InstallOptions{}); err != nil {
				t.Fatal(err)
			}
			want := []library.SkillScope{
				{SkillID: first.ID, Scope: scope, Folder: filepath.Join(directory, first.Name), Direct: true, Groups: []library.GroupID{group.ID}},
				{SkillID: second.ID, Scope: scope, Folder: filepath.Join(directory, second.Name), Groups: []library.GroupID{group.ID}},
			}
			for range 2 {
				if _, err := lib.InstallGroup(ctx, group.ID, scope, library.InstallOptions{Overwrite: overwrite}); err != nil {
					t.Fatalf("InstallGroup(overwrite=%t) = %v, want success", overwrite, err)
				}
				for _, skill := range []library.Skill{first, second} {
					if _, err := os.Stat(filepath.Join(directory, skill.Name, "SKILL.md")); err != nil {
						t.Fatalf("member %q was not installed: %v", skill.Name, err)
					}
				}
				skillScopes, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{})
				if err != nil || !reflect.DeepEqual(withoutCreatedAt(skillScopes), want) {
					t.Fatalf("ListSkillScopes() = %#v, %v; want %#v", skillScopes, err, want)
				}
			}

			requireErrorDetails(t, lib.Uninstall(ctx, second.ID, scope), library.FailedPrecondition, library.ErrorDetails{Name: group.Name})
			if err := lib.UninstallGroup(ctx, group.ID, scope); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(directory, first.Name, "SKILL.md")); err != nil {
				t.Fatalf("directly installed skill scope was removed: %v", err)
			}
			if _, err := os.Stat(filepath.Join(directory, second.Name)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("via-group-only skill scope remains: %v", err)
			}
			skillScopes, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{})
			want = []library.SkillScope{{SkillID: first.ID, Scope: scope, Folder: filepath.Join(directory, first.Name), Direct: true}}
			if err != nil || !reflect.DeepEqual(withoutCreatedAt(skillScopes), want) {
				t.Fatalf("ListSkillScopes() after UninstallGroup = %#v, %v; want %#v", skillScopes, err, want)
			}
		})
	}
}

// 有未记录文件夹的成员默认被跳过;同意覆盖再次安装只补齐它,已安装的成员不重新安装,组安装不重复记录。
func TestInstallGroupSkipsUntrackedMemberUntilOverwrite(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	firstSource := filepath.Join(root, "first")
	secondSource := filepath.Join(root, "second")
	writeLocalSkill(t, firstSource, "# First\n")
	writeLocalSkill(t, secondSource, "# Second\n")
	lib := openTestLibrary(t)
	first, err := fetchLocalDir(ctx, lib, firstSource)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fetchLocalDir(ctx, lib, secondSource)
	if err != nil {
		t.Fatal(err)
	}
	group, err := lib.CreateGroup(ctx, "shipping")
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range []library.Skill{first, second} {
		if _, err := lib.AddSkillToGroup(ctx, group.ID, skill.ID); err != nil {
			t.Fatal(err)
		}
	}
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.ScopeDirectory(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(directory, second.Name, "manual.txt"), []byte("manual"), 0o644)

	results, err := lib.InstallGroup(ctx, group.ID, scope, library.InstallOptions{})
	requireMemberResults(t, results, err, []library.GroupMemberResult{
		{SkillID: first.ID, Scope: scope, Outcome: library.MemberInstalled},
		skippedMember(second.ID, scope, library.AlreadyExists, library.ErrorDetails{Path: filepath.Join(directory, second.Name)}),
	})
	if _, err := os.Stat(filepath.Join(directory, second.Name, "manual.txt")); err != nil {
		t.Fatalf("conflicting directory changed: %v", err)
	}
	installs, err := lib.ListGroupInstalls(ctx)
	if err != nil || len(installs) != 1 {
		t.Fatalf("ListGroupInstalls() = %#v, %v; want one", installs, err)
	}
	writeFile(t, filepath.Join(directory, first.Name, "SKILL.md"), []byte("edited\n"), 0o644)

	results, err = lib.InstallGroup(ctx, group.ID, scope, library.InstallOptions{Overwrite: true})
	requireMemberResults(t, results, err, []library.GroupMemberResult{
		{SkillID: first.ID, Scope: scope, Outcome: library.MemberAlreadyInstalled},
		{SkillID: second.ID, Scope: scope, Outcome: library.MemberInstalled},
	})
	requireSkillFile(t, directory, first.Name, "edited\n")
	requireInstalledFolder(t, directory, second.Name, "# Second\n")
	if _, err := os.Stat(filepath.Join(directory, second.Name, "manual.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Overwrite kept the untracked folder: %v", err)
	}
	again, err := lib.ListGroupInstalls(ctx)
	if err != nil || !reflect.DeepEqual(again, installs) {
		t.Fatalf("ListGroupInstalls() after re-install = %#v, %v; want %#v", again, err, installs)
	}
}

// 对自己已安装的身份 Scope 再次安装,不同意覆盖时也幂等成功,只补安装、不重新安装已被手动修改的文件夹;
// 记录仍是同一条身份。组安装与再次组安装同样不写盘。
func TestInstallingAgainNeverOverwritesAnExistingFolder(t *testing.T) {
	ctx := t.Context()
	source := filepath.Join(t.TempDir(), "example-skill")
	writeLocalSkill(t, source, "# Example\n")
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	skill, err := fetchLocalDir(ctx, lib, source)
	if err != nil {
		t.Fatal(err)
	}
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.ScopeDirectory(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(directory, skill.Name, "SKILL.md"), []byte("edited\n"), 0o644)
	if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatalf("re-Install under Fail policy = %v, want success", err)
	}
	requireSkillFile(t, directory, skill.Name, "edited\n")
	skillScopes, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{})
	want := []library.SkillScope{{SkillID: skill.ID, Scope: scope, Folder: filepath.Join(directory, skill.Name), Direct: true}}
	if err != nil || !reflect.DeepEqual(withoutCreatedAt(skillScopes), want) {
		t.Fatalf("ListSkillScopes() after re-Install = %#v, %v; want %#v", skillScopes, err, want)
	}

	group, err := lib.CreateGroup(ctx, "g")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.AddSkillToGroup(ctx, group.ID, skill.ID); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		results, err := lib.InstallGroup(ctx, group.ID, scope, library.InstallOptions{})
		requireMemberResults(t, results, err, []library.GroupMemberResult{{SkillID: skill.ID, Scope: scope, Outcome: library.MemberAlreadyInstalled}})
		requireSkillFile(t, directory, skill.Name, "edited\n")
	}
	skillScopes, err = lib.ListSkillScopes(ctx, library.SkillScopeFilter{})
	want[0].Groups = []library.GroupID{group.ID}
	if err != nil || !reflect.DeepEqual(withoutCreatedAt(skillScopes), want) {
		t.Fatalf("skill scopes after installing again = %#v, %v; want %#v", skillScopes, err, want)
	}
}

// 组内同名的成员都被跳过(不按顺序先到先得),其他成员照常安装。
func TestInstallGroupSkipsAllMembersSharingAName(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	writeLocalSkill(t, filepath.Join(root, "one", "dup"), "# One\n")
	writeLocalSkill(t, filepath.Join(root, "two", "dup"), "# Two\n")
	lib, err := library.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
	first, err := fetchLocalDir(ctx, lib, filepath.Join(root, "one", "dup"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := fetchLocalDir(ctx, lib, filepath.Join(root, "two", "dup"))
	if err != nil {
		t.Fatal(err)
	}
	group, err := lib.CreateGroup(ctx, "duplicates")
	if err != nil {
		t.Fatal(err)
	}
	other := fetchNamedSkill(t, lib, "other")
	for _, skill := range []library.Skill{first, second, other} {
		if _, err := lib.AddSkillToGroup(ctx, group.ID, skill.ID); err != nil {
			t.Fatal(err)
		}
	}
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.ScopeDirectory(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	results, err := lib.InstallGroup(ctx, group.ID, scope, library.InstallOptions{Overwrite: true})
	requireMemberResults(t, results, err, []library.GroupMemberResult{
		skippedMember(first.ID, scope, library.FailedPrecondition, library.ErrorDetails{Name: "dup", Path: filepath.Join(directory, "dup")}),
		skippedMember(second.ID, scope, library.FailedPrecondition, library.ErrorDetails{Name: "dup", Path: filepath.Join(directory, "dup")}),
		{SkillID: other.ID, Scope: scope, Outcome: library.MemberInstalled},
	})
	if _, err := os.Stat(filepath.Join(directory, "dup")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("colliding members left an installed folder: %v", err)
	}
	requireInstalledFolder(t, directory, "other", "# other\n")
}

func TestInstallGroupFailurePreservesDirectInstall(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	firstSource := filepath.Join(root, "first")
	secondSource := filepath.Join(root, "second")
	writeLocalSkill(t, firstSource, "# First\n")
	writeLocalSkill(t, secondSource, "# Second\n")
	lib, err := library.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
	first, err := fetchLocalDir(ctx, lib, firstSource)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fetchLocalDir(ctx, lib, secondSource)
	if err != nil {
		t.Fatal(err)
	}
	group, err := lib.CreateGroup(ctx, "shipping")
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range []library.Skill{first, second} {
		if _, err := lib.AddSkillToGroup(ctx, group.ID, skill.ID); err != nil {
			t.Fatal(err)
		}
	}
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.ScopeDirectory(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.Install(ctx, first.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	makeScopeReadOnly(t, directory)
	if _, err := lib.InstallGroup(ctx, group.ID, scope, library.InstallOptions{Overwrite: true}); err == nil {
		t.Fatal("InstallGroup succeeded despite a read-only scope")
	}
	if err := os.Chmod(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, first.Name, "SKILL.md")); err != nil {
		t.Fatalf("failed group install removed directly installed skill scope: %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, second.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed group install left a partial member: %v", err)
	}
	skillScopes, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{})
	want := []library.SkillScope{{SkillID: first.ID, Scope: scope, Folder: filepath.Join(directory, first.Name), Direct: true}}
	if err != nil || !reflect.DeepEqual(withoutCreatedAt(skillScopes), want) {
		t.Fatalf("ListSkillScopes() = %#v, %v; want %#v", skillScopes, err, want)
	}
	if installs, err := lib.ListGroupInstalls(ctx); err != nil || len(installs) != 0 {
		t.Fatalf("failed InstallGroup left group installs: %#v, %v", installs, err)
	}
}

// 用项目 Scope 安装:文件夹落在该 Registered project 根下 ScopeDirectory 给出的目录;Uninstall 后
// 目录与记录一起消失,Library 里的 skill 仍在;再次 Uninstall 得到 NotFound。
func TestInstallAndUninstallProjectScope(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	skill := fetchExampleSkill(t, lib, "example-skill")
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	scope := library.ProjectScope(library.AgentClaudeCode, project.ID)
	if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	directory, err := lib.ScopeDirectory(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if directory != filepath.Join(project.Path, ".claude", "skills") {
		t.Fatalf("ScopeDirectory(%#v) = %q; want under registered project root", scope, directory)
	}
	target := filepath.Join(directory, skill.Name)
	if _, err := os.Stat(filepath.Join(target, "SKILL.md")); err != nil {
		t.Fatalf("project skill scope missing: %v", err)
	}
	skillScopes, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{})
	if err != nil {
		t.Fatal(err)
	}
	want := []library.SkillScope{{
		SkillID: skill.ID,
		Scope:   scope,
		Folder:  target,
		Direct:  true,
	}}
	if !reflect.DeepEqual(withoutCreatedAt(skillScopes), want) {
		t.Fatalf("ListSkillScopes() = %#v, want %#v", skillScopes, want)
	}

	if err := lib.Uninstall(ctx, skill.ID, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("uninstalled directory still exists: %v", err)
	}
	if _, err := lib.GetSkill(ctx, skill.ID); err != nil {
		t.Fatalf("Uninstall removed library skill: %v", err)
	}
	if skillScopes, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{}); err != nil || len(skillScopes) != 0 {
		t.Fatalf("ListSkillScopes() after uninstall = %#v, %v", skillScopes, err)
	}
	if err := lib.Uninstall(ctx, skill.ID, scope); !library.IsCode(err, library.NotFound) {
		t.Fatalf("Uninstall() not-installed skill error = %v, want NotFound", err)
	}
}

// 整组安装在身份路径上仍是原子的:某成员目标被外来文件夹占用时整组拒绝,不留装了一半的组。
// 项目 Scope 上有未记录文件夹的成员被跳过,其余成员照常安装,组安装照常成立。
func TestInstallGroupOnIdentityScopeSkipsUntrackedMember(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	first := fetchExampleSkill(t, lib, "first")
	second := fetchExampleSkill(t, lib, "second")
	group, err := lib.CreateGroup(ctx, "shipping")
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range []library.Skill{first, second} {
		if _, err := lib.AddSkillToGroup(ctx, group.ID, skill.ID); err != nil {
			t.Fatal(err)
		}
	}
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	scope := library.ProjectScope(library.AgentCodex, project.ID)
	directory, err := lib.ScopeDirectory(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(directory, second.Name, "manual.txt"), []byte("manual"), 0o644)

	results, err := lib.InstallGroup(ctx, group.ID, scope, library.InstallOptions{})
	requireMemberResults(t, results, err, []library.GroupMemberResult{
		{SkillID: first.ID, Scope: scope, Outcome: library.MemberInstalled},
		skippedMember(second.ID, scope, library.AlreadyExists, library.ErrorDetails{Path: filepath.Join(directory, second.Name)}),
	})
	requireInstalledFolder(t, directory, first.Name, "# first\n")
	if _, err := os.Stat(filepath.Join(directory, second.Name, "manual.txt")); err != nil {
		t.Fatalf("conflicting directory changed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, second.Name, "SKILL.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("skipped member was installed: %v", err)
	}
	skillScopes, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{})
	want := []library.SkillScope{{SkillID: first.ID, Scope: scope, Folder: filepath.Join(directory, first.Name), Groups: []library.GroupID{group.ID}}}
	if err != nil || !reflect.DeepEqual(withoutCreatedAt(skillScopes), want) {
		t.Fatalf("ListSkillScopes() = %#v, %v; want %#v", skillScopes, err, want)
	}
	installs, err := lib.ListGroupInstalls(ctx)
	if want := []library.GroupInstall{{GroupID: group.ID, Scope: scope}}; err != nil || !reflect.DeepEqual(withoutGroupCreatedAt(installs), want) {
		t.Fatalf("ListGroupInstalls() = %#v, %v; want %#v", installs, err, want)
	}
}

// 同一 skill 可同时占用两个 Agent 的全局 Scope,以及同一 Agent 的全局与项目 Scope:各是一个 SkillScope。
func TestIdentityScopesOfOneSkillAreIndependentSkillScopes(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	skill := fetchExampleSkill(t, lib, "example-skill")
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	scopes := []library.Scope{
		library.GlobalScope(library.AgentClaudeCode),
		library.GlobalScope(library.AgentCodex),
		library.ProjectScope(library.AgentClaudeCode, project.ID),
	}
	for _, scope := range scopes {
		if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
			t.Fatalf("Install(%#v) = %v", scope, err)
		}
	}
	skillScopes, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{})
	if err != nil || len(skillScopes) != len(scopes) {
		t.Fatalf("ListSkillScopes() = %#v, %v; want %d skill scopes", skillScopes, err, len(scopes))
	}
	for _, scope := range scopes {
		directory, err := lib.ScopeDirectory(ctx, scope)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(directory, skill.Name, "SKILL.md")); err != nil {
			t.Fatalf("skill scope for %#v missing: %v", scope, err)
		}
	}
	// 撤销其中一条不影响其余两条。
	if err := lib.Uninstall(ctx, skill.ID, scopes[0]); err != nil {
		t.Fatal(err)
	}
	skillScopes, err = lib.ListSkillScopes(ctx, library.SkillScopeFilter{})
	if err != nil || len(skillScopes) != 2 {
		t.Fatalf("ListSkillScopes() after one Uninstall = %#v, %v; want 2", skillScopes, err)
	}
	for _, skillScope := range skillScopes {
		if skillScope.Scope == scopes[0] {
			t.Fatalf("uninstalled scope still listed: %#v", skillScope)
		}
	}
}

// 两个不同身份的 Scope 解析到同一目录(把 $HOME 本身登记为项目)时,第二次安装不能悄悄改写
// 第一个 SkillScope 的身份:得到 AlreadyExists,原记录与磁盘保持原样。
func TestInstallDoesNotOverwriteAnotherScopeSharingTheDirectory(t *testing.T) {
	ctx := t.Context()
	// RegisterProject 会解析符号链接(macOS 的 /var → /private/var),HOME 先取规范路径以便两边一致。
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	skill := fetchExampleSkill(t, lib, "example-skill")
	project, err := lib.RegisterProject(ctx, home)
	if err != nil {
		t.Fatal(err)
	}
	global := library.GlobalScope(library.AgentCodex)
	projectScope := library.ProjectScope(library.AgentCodex, project.ID)
	directory, err := lib.ScopeDirectory(ctx, global)
	if err != nil {
		t.Fatal(err)
	}
	if projectDirectory, err := lib.ScopeDirectory(ctx, projectScope); err != nil || projectDirectory != directory {
		t.Fatalf("precondition: scopes should share a directory, got %q vs %q (%v)", projectDirectory, directory, err)
	}

	if err := lib.Install(ctx, skill.ID, global, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, opts := range []library.InstallOptions{{}, {Overwrite: true}} {
		err := lib.Install(ctx, skill.ID, projectScope, opts)
		requireErrorDetails(t, err, library.FailedPrecondition, library.ErrorDetails{Path: directory})
	}
	skillScopes, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{})
	want := []library.SkillScope{{SkillID: skill.ID, Scope: global, Folder: filepath.Join(directory, skill.Name), Direct: true}}
	if err != nil || !reflect.DeepEqual(withoutCreatedAt(skillScopes), want) {
		t.Fatalf("ListSkillScopes() = %#v, %v; want %#v", skillScopes, err, want)
	}
	if _, err := os.Stat(filepath.Join(directory, skill.Name, "SKILL.md")); err != nil {
		t.Fatalf("global skill scope lost: %v", err)
	}
}

// 两个身份争同一实际目录时,并发 Install 也不能绕过占用保护:最多一条记录,磁盘上的技能文件夹仍在。
func TestInstallSerializesSharedDirectoryClaims(t *testing.T) {
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
	skill := fetchExampleSkill(t, lib, "example-skill")
	scopes := []library.Scope{
		library.GlobalScope(library.AgentCodex),
		library.ProjectScope(library.AgentCodex, project.ID),
	}
	start := make(chan struct{})
	results := make([]error, len(scopes))
	var calls sync.WaitGroup
	for i, scope := range scopes {
		calls.Go(func() {
			<-start
			results[i] = lib.Install(ctx, skill.ID, scope, library.InstallOptions{})
		})
	}
	close(start)
	calls.Wait()
	skillScopes, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(skillScopes) != 1 {
		t.Fatalf("ListSkillScopes() = %#v, install errors=%v; want exactly one claim", skillScopes, results)
	}
	directory, err := lib.ScopeDirectory(ctx, skillScopes[0].Scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, skill.Name, "SKILL.md")); err != nil {
		t.Fatalf("surviving skill scope missing: %v", err)
	}
	var accepted, rejected int
	for _, err := range results {
		switch {
		case err == nil:
			accepted++
		case library.IsCode(err, library.FailedPrecondition):
			rejected++
		default:
			t.Fatalf("Install = %v, want nil or FailedPrecondition", err)
		}
	}
	if accepted != 1 || rejected != 1 {
		t.Fatalf("Install results = %v, want one success and one FailedPrecondition", results)
	}
}

// 同名不同 Source 的 A 已安装到某 Scope 时,安装 B 不能接管那个文件夹:同不同意覆盖都得到
// FailedPrecondition 并指认占用者;A 的记录与内容原样保留,不会出现两条记录指向同一文件夹。
func TestInstallRejectsNameClaimedByAnotherSkill(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	first := fetchSkillWithBody(t, lib, "commit", "# A\n")
	second := fetchSkillWithBody(t, lib, "commit", "# B\n")
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.ScopeDirectory(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.Install(ctx, first.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(directory, "commit")
	for _, opts := range []library.InstallOptions{{}, {Overwrite: true}} {
		err := lib.Install(ctx, second.ID, scope, opts)
		requireErrorDetails(t, err, library.FailedPrecondition, library.ErrorDetails{Path: target, Name: "commit", SkillID: first.ID})
	}
	skillScopes, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{})
	want := []library.SkillScope{{SkillID: first.ID, Scope: scope, Folder: filepath.Join(directory, first.Name), Direct: true}}
	if err != nil || !reflect.DeepEqual(withoutCreatedAt(skillScopes), want) {
		t.Fatalf("ListSkillScopes() = %#v, %v; want %#v", skillScopes, err, want)
	}
	requireInstalledFolder(t, directory, "commit", "# A\n")

	// 换用 B 的正确路径:先撤 A,再安装 B。
	if err := lib.Uninstall(ctx, first.ID, scope); err != nil {
		t.Fatal(err)
	}
	if err := lib.Install(ctx, second.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	requireInstalledFolder(t, directory, "commit", "# B\n")
	skillScopes, err = lib.ListSkillScopes(ctx, library.SkillScopeFilter{})
	want = []library.SkillScope{{SkillID: second.ID, Scope: scope, Folder: filepath.Join(directory, second.Name), Direct: true}}
	if err != nil || !reflect.DeepEqual(withoutCreatedAt(skillScopes), want) {
		t.Fatalf("ListSkillScopes() after switch = %#v, %v; want %#v", skillScopes, err, want)
	}
}

// 占用者只由组安装留着(无直接安装)时同样受保护;另一组向该 Scope 安装时只跳过被占名字的成员,
// 其他成员照常安装。占用者撤走后再次安装该组即可补齐。
func TestInstallGroupSkipsMemberWhoseNameIsClaimedByAnotherSkill(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	first := fetchSkillWithBody(t, lib, "commit", "# A\n")
	second := fetchSkillWithBody(t, lib, "commit", "# B\n")
	bystander := fetchSkillWithBody(t, lib, "bystander", "# bystander\n")
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.ScopeDirectory(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	firstGroup, err := lib.CreateGroup(ctx, "holders")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.AddSkillToGroup(ctx, firstGroup.ID, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.InstallGroup(ctx, firstGroup.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(directory, "commit")
	err = lib.Install(ctx, second.ID, scope, library.InstallOptions{Overwrite: true})
	requireErrorDetails(t, err, library.FailedPrecondition, library.ErrorDetails{Path: target, Name: "commit", SkillID: first.ID})

	secondGroup, err := lib.CreateGroup(ctx, "challengers")
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range []library.Skill{bystander, second} {
		if _, err := lib.AddSkillToGroup(ctx, secondGroup.ID, skill.ID); err != nil {
			t.Fatal(err)
		}
	}
	results, err := lib.InstallGroup(ctx, secondGroup.ID, scope, library.InstallOptions{Overwrite: true})
	requireMemberResults(t, results, err, []library.GroupMemberResult{
		skippedMember(second.ID, scope, library.FailedPrecondition, library.ErrorDetails{Path: target, Name: "commit", SkillID: first.ID}),
		{SkillID: bystander.ID, Scope: scope, Outcome: library.MemberInstalled},
	})
	requireInstalledFolder(t, directory, "bystander", "# bystander\n")
	requireInstalledFolder(t, directory, "commit", "# A\n")
	skillScopes, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{})
	want := []library.SkillScope{
		{SkillID: first.ID, Scope: scope, Folder: filepath.Join(directory, first.Name), Groups: []library.GroupID{firstGroup.ID}},
		{SkillID: bystander.ID, Scope: scope, Folder: filepath.Join(directory, bystander.Name), Groups: []library.GroupID{secondGroup.ID}},
	}
	if err != nil || !reflect.DeepEqual(withoutCreatedAt(skillScopes), want) {
		t.Fatalf("ListSkillScopes() = %#v, %v; want %#v", skillScopes, err, want)
	}

	if err := lib.UninstallGroup(ctx, firstGroup.ID, scope); err != nil {
		t.Fatal(err)
	}
	results, err = lib.InstallGroup(ctx, secondGroup.ID, scope, library.InstallOptions{})
	requireMemberResults(t, results, err, []library.GroupMemberResult{
		{SkillID: second.ID, Scope: scope, Outcome: library.MemberInstalled},
		{SkillID: bystander.ID, Scope: scope, Outcome: library.MemberAlreadyInstalled},
	})
	requireInstalledFolder(t, directory, "commit", "# B\n")
}

// 占用以实际目录为准:A 在全局 Scope,把 $HOME 登记为项目后向项目 Scope 安装同名的 B 同样被拒;
// 而 A、B 安装到不同目录的 Scope 互不干扰。即使 A 的文件夹已被手删,只要记录在,B 仍被拒。
func TestSameNameSkillsCollideOnlyInTheSameDirectory(t *testing.T) {
	ctx := t.Context()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	first := fetchSkillWithBody(t, lib, "commit", "# A\n")
	second := fetchSkillWithBody(t, lib, "commit", "# B\n")
	project, err := lib.RegisterProject(ctx, home)
	if err != nil {
		t.Fatal(err)
	}
	global := library.GlobalScope(library.AgentCodex)
	sameDirectory := library.ProjectScope(library.AgentCodex, project.ID)
	otherAgent := library.GlobalScope(library.AgentClaudeCode)
	directory, err := lib.ScopeDirectory(ctx, global)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.Install(ctx, first.ID, global, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}

	err = lib.Install(ctx, second.ID, sameDirectory, library.InstallOptions{Overwrite: true})
	requireErrorDetails(t, err, library.FailedPrecondition, library.ErrorDetails{Path: filepath.Join(directory, "commit"), Name: "commit", SkillID: first.ID})

	if err := lib.Install(ctx, second.ID, otherAgent, library.InstallOptions{}); err != nil {
		t.Fatalf("Install(B) into another agent's directory = %v, want success", err)
	}
	otherDirectory, err := lib.ScopeDirectory(ctx, otherAgent)
	if err != nil {
		t.Fatal(err)
	}
	requireInstalledFolder(t, directory, "commit", "# A\n")
	requireInstalledFolder(t, otherDirectory, "commit", "# B\n")
	skillScopes, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{})
	want := []library.SkillScope{
		{SkillID: first.ID, Scope: global, Folder: filepath.Join(directory, first.Name), Direct: true},
		{SkillID: second.ID, Scope: otherAgent, Folder: filepath.Join(otherDirectory, second.Name), Direct: true},
	}
	if err != nil || !reflect.DeepEqual(withoutCreatedAt(skillScopes), want) {
		t.Fatalf("ListSkillScopes() = %#v, %v; want %#v", skillScopes, err, want)
	}

	if err := os.RemoveAll(filepath.Join(directory, "commit")); err != nil {
		t.Fatal(err)
	}
	err = lib.Install(ctx, second.ID, global, library.InstallOptions{})
	requireErrorDetails(t, err, library.FailedPrecondition, library.ErrorDetails{Path: filepath.Join(directory, "commit"), Name: "commit", SkillID: first.ID})
}

// 身份 Scope 里的错误指认在安装时同样得到 NotFound,不会写到任何目录。
func TestInstallUnknownAgentOrProjectIsNotFound(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	skill := fetchExampleSkill(t, lib, "example-skill")

	for name, scope := range map[string]library.Scope{
		"unknown agent":        library.GlobalScope("cursor"),
		"unregistered project": library.ProjectScope(library.AgentClaudeCode, 42),
	} {
		t.Run(name, func(t *testing.T) {
			if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); !library.IsCode(err, library.NotFound) {
				t.Fatalf("Install(%#v) = %v, want NotFound", scope, err)
			}
		})
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed Install wrote into HOME: %v, %v", entries, err)
	}
}
