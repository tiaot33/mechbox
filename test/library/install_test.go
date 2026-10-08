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
			if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{OnConflict: library.Overwrite}); err != nil {
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
			skillScopes, err := lib.ListSkillScopes(ctx)
			if err != nil {
				t.Fatal(err)
			}
			want := []library.SkillScope{{
				SkillID:   skill.ID,
				Scope:     scope,
				Directory: directory,
				Direct:    true,
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
			skillScopes, err = lib.ListSkillScopes(ctx)
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
	listed, err := lib.ListSkillScopes(ctx)
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
	installedAgain, err := lib.ListSkillScopes(ctx)
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
	before, err := lib.ListSkillScopes(ctx)
	if err != nil {
		t.Fatal(err)
	}

	makeScopeReadOnly(t, directory)
	err = lib.Uninstall(ctx, present.ID, scope)
	requireLibraryError(t, err, library.Permission)
	if err := os.Chmod(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	after, err := lib.ListSkillScopes(ctx)
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
	skillScopes, err := lib.ListSkillScopes(ctx)
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
	for _, policy := range []library.ConflictPolicy{library.Fail, library.Overwrite} {
		t.Run(string(policy), func(t *testing.T) {
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
				{SkillID: first.ID, Scope: scope, Directory: directory, Direct: true, Groups: []library.GroupID{group.ID}},
				{SkillID: second.ID, Scope: scope, Directory: directory, Groups: []library.GroupID{group.ID}},
			}
			for range 2 {
				if _, err := lib.InstallGroup(ctx, group.ID, scope, library.InstallOptions{OnConflict: policy}); err != nil {
					t.Fatalf("InstallGroup under %s policy = %v, want success", policy, err)
				}
				for _, skill := range []library.Skill{first, second} {
					if _, err := os.Stat(filepath.Join(directory, skill.Name, "SKILL.md")); err != nil {
						t.Fatalf("member %q was not installed: %v", skill.Name, err)
					}
				}
				skillScopes, err := lib.ListSkillScopes(ctx)
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
			skillScopes, err := lib.ListSkillScopes(ctx)
			want = []library.SkillScope{{SkillID: first.ID, Scope: scope, Directory: directory, Direct: true}}
			if err != nil || !reflect.DeepEqual(withoutCreatedAt(skillScopes), want) {
				t.Fatalf("ListSkillScopes() after UninstallGroup = %#v, %v; want %#v", skillScopes, err, want)
			}
		})
	}
}

// 有未记录文件夹的成员在默认策略下被跳过;选 Overwrite 再次安装只补齐它,已安装的成员不重新安装,组安装不重复记录。
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

	results, err = lib.InstallGroup(ctx, group.ID, scope, library.InstallOptions{OnConflict: library.Overwrite})
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

// 对自己已安装的身份 Scope 再次安装,默认冲突策略下幂等成功,只补安装、不重新安装已被手动修改的文件夹;
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
	skillScopes, err := lib.ListSkillScopes(ctx)
	want := []library.SkillScope{{SkillID: skill.ID, Scope: scope, Directory: directory, Direct: true}}
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
	skillScopes, err = lib.ListSkillScopes(ctx)
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
	results, err := lib.InstallGroup(ctx, group.ID, scope, library.InstallOptions{OnConflict: library.Overwrite})
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
	if _, err := lib.InstallGroup(ctx, group.ID, scope, library.InstallOptions{OnConflict: library.Overwrite}); err == nil {
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
	skillScopes, err := lib.ListSkillScopes(ctx)
	want := []library.SkillScope{{SkillID: first.ID, Scope: scope, Directory: directory, Direct: true}}
	if err != nil || !reflect.DeepEqual(withoutCreatedAt(skillScopes), want) {
		t.Fatalf("ListSkillScopes() = %#v, %v; want %#v", skillScopes, err, want)
	}
	if installs, err := lib.ListGroupInstalls(ctx); err != nil || len(installs) != 0 {
		t.Fatalf("failed InstallGroup left group installs: %#v, %v", installs, err)
	}
}
