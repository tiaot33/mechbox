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

// 单项安装的主路径只选定 Agent(全局 Scope):目录由 Library 按现行布局在隔离的 $HOME 下算出,
// 冲突 / Overwrite / Uninstall 的断言全部对着 Resolve 给出的目录,而不是测试自拟的路径。
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
			skill, err := fetchLocal(ctx, lib, source)
			if err != nil {
				t.Fatal(err)
			}

			scope := library.GlobalScope(agent)
			directory, err := lib.Resolve(ctx, scope)
			if err != nil {
				t.Fatal(err)
			}
			if directory != filepath.Join(home, agentDirectory, "skills") {
				t.Fatalf("Resolve(%#v) = %q; want under isolated HOME", scope, directory)
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
			requireInstallation(t, directory, skill.Name, "# Example\n")
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
			installations, err := lib.ListInstallations(ctx)
			if err != nil {
				t.Fatal(err)
			}
			want := []library.Installation{{
				SkillID:   skill.ID,
				Scope:     scope,
				Directory: directory,
				Direct:    true,
			}}
			if !reflect.DeepEqual(withoutCreatedAt(installations), want) {
				t.Fatalf("ListInstallations() = %#v, want %#v", installations, want)
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
			installations, err = lib.ListInstallations(ctx)
			if err != nil || len(installations) != 0 {
				t.Fatalf("ListInstallations() after uninstall = %#v, %v", installations, err)
			}
			writeFile(t, filepath.Join(target, "manual.txt"), []byte("manual"), 0o644)
			if err := lib.Uninstall(ctx, skill.ID, scope); !library.IsCode(err, library.NotFound) {
				t.Fatalf("Uninstall() inactive skill error = %v", err)
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
	skill, err := fetchLocal(ctx, lib, source)
	if err != nil {
		t.Fatal(err)
	}
	scope := library.GlobalScope(library.AgentCodex)
	start := time.Now().UTC()
	if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	end := time.Now().UTC()
	listed, err := lib.ListInstallations(ctx)
	if err != nil || len(listed) != 1 {
		t.Fatalf("ListInstallations = %#v, %v", listed, err)
	}
	first := listed[0].CreatedAt
	if first.IsZero() || first.Before(start) || first.After(end) {
		t.Fatalf("CreatedAt = %v, want in [%v, %v]", first, start, end)
	}
	time.Sleep(time.Millisecond)
	if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	installedAgain, err := lib.ListInstallations(ctx)
	if err != nil || len(installedAgain) != 1 || !installedAgain[0].CreatedAt.Equal(first) {
		t.Fatalf("installedAgain CreatedAt = %#v, %v; want %v", installedAgain, err, first)
	}
}

// Uninstall 先删记录、把安装项改名暂存、提交后才真正删除:改名失败时记录与文件夹都原样不动;
// 安装项已被手动删掉时仍成功并删掉记录;成功后 Scope 目录下不留暂存残留。
func TestUninstallKeepsLibraryAndDiskTogether(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	present := fetchNamedSkill(t, lib, "present")
	missing := fetchNamedSkill(t, lib, "missing")
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.Resolve(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range []library.Skill{present, missing} {
		if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	before, err := lib.ListInstallations(ctx)
	if err != nil {
		t.Fatal(err)
	}

	makeScopeReadOnly(t, directory)
	err = lib.Uninstall(ctx, present.ID, scope)
	requireLibraryError(t, err, library.Permission)
	if err := os.Chmod(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	after, err := lib.ListInstallations(ctx)
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("failed Uninstall changed installations: %#v, %v; want %#v", after, err, before)
	}
	content, err := os.ReadFile(filepath.Join(directory, present.Name, "SKILL.md"))
	if err != nil || string(content) != skillDocument(present.Name, "# present\n") {
		t.Fatalf("failed Uninstall changed installation: %q, %v", content, err)
	}
	requireNoStagedEntries(t, directory)

	if err := os.RemoveAll(filepath.Join(directory, missing.Name)); err != nil {
		t.Fatal(err)
	}
	if err := lib.Uninstall(ctx, missing.ID, scope); err != nil {
		t.Fatalf("Uninstall with manually deleted installation = %v", err)
	}
	if err := lib.Uninstall(ctx, present.ID, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, present.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Uninstall left installation folder behind: %v", err)
	}
	installations, err := lib.ListInstallations(ctx)
	if err != nil || len(installations) != 0 {
		t.Fatalf("ListInstallations() after uninstalls = %#v, %v", installations, err)
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

// 直接安装与组安装叠加;重复安装不增加安装,撤销组只移除仅由该组安装撑着的安装项。
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
			directory, err := lib.Resolve(ctx, scope)
			if err != nil {
				t.Fatal(err)
			}
			requireLibraryError(t, lib.UninstallGroup(ctx, group.ID, scope), library.NotFound)

			if err := lib.Install(ctx, first.ID, scope, library.InstallOptions{}); err != nil {
				t.Fatal(err)
			}
			want := []library.Installation{
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
				installations, err := lib.ListInstallations(ctx)
				if err != nil || !reflect.DeepEqual(withoutCreatedAt(installations), want) {
					t.Fatalf("ListInstallations() = %#v, %v; want %#v", installations, err, want)
				}
			}

			requireErrorDetails(t, lib.Uninstall(ctx, second.ID, scope), library.FailedPrecondition, library.ErrorDetails{Name: group.Name})
			if err := lib.UninstallGroup(ctx, group.ID, scope); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(directory, first.Name, "SKILL.md")); err != nil {
				t.Fatalf("directly installed installation was removed: %v", err)
			}
			if _, err := os.Stat(filepath.Join(directory, second.Name)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("via-group-only installation remains: %v", err)
			}
			installations, err := lib.ListInstallations(ctx)
			want = []library.Installation{{SkillID: first.ID, Scope: scope, Directory: directory, Direct: true}}
			if err != nil || !reflect.DeepEqual(withoutCreatedAt(installations), want) {
				t.Fatalf("ListInstallations() after UninstallGroup = %#v, %v; want %#v", installations, err, want)
			}
		})
	}
}

// 有未记录文件夹的成员在默认策略下被跳过;选 Overwrite 再次安装只补齐它,已有安装项的成员不重新安装,组安装不重复记录。
func TestInstallGroupSkipsUntrackedMemberUntilOverwrite(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	firstSource := filepath.Join(root, "first")
	secondSource := filepath.Join(root, "second")
	writeLocalSkill(t, firstSource, "# First\n")
	writeLocalSkill(t, secondSource, "# Second\n")
	lib := openTestLibrary(t)
	first, err := fetchLocal(ctx, lib, firstSource)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fetchLocal(ctx, lib, secondSource)
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
	directory, err := lib.Resolve(ctx, scope)
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
	requireInstallation(t, directory, second.Name, "# Second\n")
	if _, err := os.Stat(filepath.Join(directory, second.Name, "manual.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Overwrite kept the untracked folder: %v", err)
	}
	again, err := lib.ListGroupInstalls(ctx)
	if err != nil || !reflect.DeepEqual(again, installs) {
		t.Fatalf("ListGroupInstalls() after re-install = %#v, %v; want %#v", again, err, installs)
	}
}

// 对自己在身份 Scope 上的安装项再次安装,默认冲突策略下幂等成功,只补安装、不重新安装已被手动修改的文件夹;
// 记录仍是同一条身份。组安装与再次组安装同样不写盘。
func TestInstallingAgainNeverOverwritesAnExistingInstallation(t *testing.T) {
	ctx := t.Context()
	source := filepath.Join(t.TempDir(), "example-skill")
	writeLocalSkill(t, source, "# Example\n")
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	skill, err := fetchLocal(ctx, lib, source)
	if err != nil {
		t.Fatal(err)
	}
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.Resolve(ctx, scope)
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
	installations, err := lib.ListInstallations(ctx)
	want := []library.Installation{{SkillID: skill.ID, Scope: scope, Directory: directory, Direct: true}}
	if err != nil || !reflect.DeepEqual(withoutCreatedAt(installations), want) {
		t.Fatalf("ListInstallations() after re-Install = %#v, %v; want %#v", installations, err, want)
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
	installations, err = lib.ListInstallations(ctx)
	want[0].Groups = []library.GroupID{group.ID}
	if err != nil || !reflect.DeepEqual(withoutCreatedAt(installations), want) {
		t.Fatalf("installations after installing again = %#v, %v; want %#v", installations, err, want)
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
	first, err := fetchLocal(ctx, lib, filepath.Join(root, "one", "dup"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := fetchLocal(ctx, lib, filepath.Join(root, "two", "dup"))
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
	directory, err := lib.Resolve(ctx, scope)
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
		t.Fatalf("colliding members left a installation: %v", err)
	}
	requireInstallation(t, directory, "other", "# other\n")
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
	first, err := fetchLocal(ctx, lib, firstSource)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fetchLocal(ctx, lib, secondSource)
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
	directory, err := lib.Resolve(ctx, scope)
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
		t.Fatalf("failed group install removed directly installed installation: %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, second.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed group install left a partial member: %v", err)
	}
	installations, err := lib.ListInstallations(ctx)
	want := []library.Installation{{SkillID: first.ID, Scope: scope, Directory: directory, Direct: true}}
	if err != nil || !reflect.DeepEqual(withoutCreatedAt(installations), want) {
		t.Fatalf("ListInstallations() = %#v, %v; want %#v", installations, err, want)
	}
	if installs, err := lib.ListGroupInstalls(ctx); err != nil || len(installs) != 0 {
		t.Fatalf("failed InstallGroup left group installs: %#v, %v", installs, err)
	}
}
