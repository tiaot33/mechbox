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

// 单项激活的主路径只选定 Agent(全局 Scope):目录由 Library 按现行布局在隔离的 $HOME 下算出,
// 冲突 / Overwrite / Deactivate 的断言全部对着 Resolve 给出的目录,而不是测试自拟的路径。
func TestActivateAndDeactivateGlobalSkill(t *testing.T) {
	for agent, agentDirectory := range map[library.AgentCode]string{
		library.AgentCodex:      ".agents",
		library.AgentClaudeCode: ".claude",
	} {
		t.Run(string(agent), func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "example-skill")
			writeFile(t, filepath.Join(source, "SKILL.md"), []byte("# Example\n"), 0o644)
			writeFile(t, filepath.Join(source, "scripts", "run.sh"), []byte("#!/bin/sh\n"), 0o755)

			home := t.TempDir()
			t.Setenv("HOME", home)
			lib := openTestLibrary(t)
			ctx := t.Context()
			skill, err := lib.Install(ctx, library.LocalSource{Path: source})
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
			if err := lib.Activate(ctx, skill.ID, scope, library.ActivateOptions{}); !library.IsCode(err, library.AlreadyExists) {
				t.Fatalf("Activate() conflict error = %v", err)
			} else {
				requireErrorDetails(t, err, library.AlreadyExists, library.ErrorDetails{Path: target})
			}
			if _, err := os.Stat(filepath.Join(target, "old.txt")); err != nil {
				t.Fatalf("failed Activate touched the foreign directory: %v", err)
			}
			if err := lib.Activate(ctx, skill.ID, scope, library.ActivateOptions{OnConflict: library.Overwrite}); err != nil {
				t.Fatal(err)
			}
			requireProjection(t, directory, skill.Name, "# Example\n")
			if _, err := os.Stat(filepath.Join(target, "old.txt")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("overwritten file still exists: %v", err)
			}
			info, err := os.Stat(filepath.Join(target, "scripts", "run.sh"))
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm()&0o111 == 0 {
				t.Fatal("activated script is not executable")
			}
			projections, err := lib.ListProjections(ctx)
			if err != nil {
				t.Fatal(err)
			}
			want := []library.Projection{{
				SkillID:   skill.ID,
				Scope:     scope,
				Directory: directory,
				Direct:    true,
			}}
			if !reflect.DeepEqual(withoutActivatedAt(projections), want) {
				t.Fatalf("ListProjections() = %#v, want %#v", projections, want)
			}

			if err := lib.Deactivate(ctx, skill.ID, scope); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("deactivated directory still exists: %v", err)
			}
			if _, err := lib.GetSkill(ctx, skill.ID); err != nil {
				t.Fatalf("Deactivate removed library skill: %v", err)
			}
			projections, err = lib.ListProjections(ctx)
			if err != nil || len(projections) != 0 {
				t.Fatalf("ListProjections() after deactivate = %#v, %v", projections, err)
			}
			writeFile(t, filepath.Join(target, "manual.txt"), []byte("manual"), 0o644)
			if err := lib.Deactivate(ctx, skill.ID, scope); !library.IsCode(err, library.NotFound) {
				t.Fatalf("Deactivate() inactive skill error = %v", err)
			}
			if _, err := os.Stat(filepath.Join(target, "manual.txt")); err != nil {
				t.Fatalf("Deactivate() removed an untracked directory: %v", err)
			}

			if err := lib.Activate(ctx, library.SkillID(9999), scope, library.ActivateOptions{}); !library.IsCode(err, library.NotFound) {
				t.Fatalf("Activate() unknown skill error = %v", err)
			}
		})
	}
}

func TestActivateRecordsFirstActivationTimeAndIgnoresReactivation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	ctx := t.Context()
	source := filepath.Join(t.TempDir(), "timed")
	writeFile(t, filepath.Join(source, "SKILL.md"), []byte("timed\n"), 0o644)
	skill, err := lib.Install(ctx, library.LocalSource{Path: source})
	if err != nil {
		t.Fatal(err)
	}
	scope := library.GlobalScope(library.AgentCodex)
	start := time.Now().UTC()
	if err := lib.Activate(ctx, skill.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	end := time.Now().UTC()
	listed, err := lib.ListProjections(ctx)
	if err != nil || len(listed) != 1 {
		t.Fatalf("ListProjections = %#v, %v", listed, err)
	}
	first := listed[0].ActivatedAt
	if first.IsZero() || first.Before(start) || first.After(end) {
		t.Fatalf("ActivatedAt = %v, want in [%v, %v]", first, start, end)
	}
	time.Sleep(time.Millisecond)
	if err := lib.Activate(ctx, skill.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	reactivated, err := lib.ListProjections(ctx)
	if err != nil || len(reactivated) != 1 || !reactivated[0].ActivatedAt.Equal(first) {
		t.Fatalf("reactivated ActivatedAt = %#v, %v; want %v", reactivated, err, first)
	}
}

// Deactivate 先删记录、把投影改名暂存、提交后才真正删除:改名失败时记录与文件夹都原样不动;
// 投影已被手动删掉时仍成功并删掉记录;成功后 Scope 目录下不留暂存残留。
func TestDeactivateKeepsLibraryAndDiskTogether(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	present := installNamedSkill(t, lib, "present")
	missing := installNamedSkill(t, lib, "missing")
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.Resolve(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range []library.Skill{present, missing} {
		if err := lib.Activate(ctx, skill.ID, scope, library.ActivateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	before, err := lib.ListProjections(ctx)
	if err != nil {
		t.Fatal(err)
	}

	makeScopeReadOnly(t, directory)
	err = lib.Deactivate(ctx, present.ID, scope)
	requireLibraryError(t, err, library.Permission)
	if err := os.Chmod(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	after, err := lib.ListProjections(ctx)
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("failed Deactivate changed projections: %#v, %v; want %#v", after, err, before)
	}
	content, err := os.ReadFile(filepath.Join(directory, present.Name, "SKILL.md"))
	if err != nil || string(content) != "# present\n" {
		t.Fatalf("failed Deactivate changed projection: %q, %v", content, err)
	}
	requireNoStagedEntries(t, directory)

	if err := os.RemoveAll(filepath.Join(directory, missing.Name)); err != nil {
		t.Fatal(err)
	}
	if err := lib.Deactivate(ctx, missing.ID, scope); err != nil {
		t.Fatalf("Deactivate with manually deleted projection = %v", err)
	}
	if err := lib.Deactivate(ctx, present.ID, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, present.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Deactivate left projected copy behind: %v", err)
	}
	projections, err := lib.ListProjections(ctx)
	if err != nil || len(projections) != 0 {
		t.Fatalf("ListProjections() after deactivations = %#v, %v", projections, err)
	}
	requireNoStagedEntries(t, directory)
}

func TestDeactivateGroupBackedSkillNamesTheGroup(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	skill := installNamedSkill(t, lib, "grouped")
	group, err := lib.CreateGroup(t.Context(), "only")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.AddSkillToGroup(t.Context(), group.ID, skill.ID, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	scope := library.GlobalScope(library.AgentCodex)
	if _, err := lib.ActivateGroup(t.Context(), group.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	requireErrorDetails(t, lib.Deactivate(t.Context(), skill.ID, scope), library.FailedPrecondition, library.ErrorDetails{Name: "only"})
}

// 直接激活与组激活叠加;重复激活不增加激活,撤销组只移除仅由该组激活撑着的投影。
func TestActivateAndDeactivateGroupPreservesDirectActivation(t *testing.T) {
	for _, policy := range []library.ConflictPolicy{library.Fail, library.Overwrite} {
		t.Run(string(policy), func(t *testing.T) {
			ctx := t.Context()
			t.Setenv("HOME", t.TempDir())
			lib := openTestLibrary(t)
			first := installExampleSkill(t, lib, "first")
			second := installExampleSkill(t, lib, "second")
			group, err := lib.CreateGroup(ctx, "shipping")
			if err != nil {
				t.Fatal(err)
			}
			for _, skill := range []library.Skill{first, second} {
				if _, err := lib.AddSkillToGroup(ctx, group.ID, skill.ID, library.ActivateOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			scope := library.GlobalScope(library.AgentCodex)
			directory, err := lib.Resolve(ctx, scope)
			if err != nil {
				t.Fatal(err)
			}
			requireLibraryError(t, lib.DeactivateGroup(ctx, group.ID, scope), library.NotFound)

			if err := lib.Activate(ctx, first.ID, scope, library.ActivateOptions{}); err != nil {
				t.Fatal(err)
			}
			want := []library.Projection{
				{SkillID: first.ID, Scope: scope, Directory: directory, Direct: true, Groups: []library.GroupID{group.ID}},
				{SkillID: second.ID, Scope: scope, Directory: directory, Groups: []library.GroupID{group.ID}},
			}
			for range 2 {
				if _, err := lib.ActivateGroup(ctx, group.ID, scope, library.ActivateOptions{OnConflict: policy}); err != nil {
					t.Fatalf("ActivateGroup under %s policy = %v, want success", policy, err)
				}
				for _, skill := range []library.Skill{first, second} {
					if _, err := os.Stat(filepath.Join(directory, skill.Name, "SKILL.md")); err != nil {
						t.Fatalf("member %q was not projected: %v", skill.Name, err)
					}
				}
				projections, err := lib.ListProjections(ctx)
				if err != nil || !reflect.DeepEqual(withoutActivatedAt(projections), want) {
					t.Fatalf("ListProjections() = %#v, %v; want %#v", projections, err, want)
				}
			}

			requireErrorDetails(t, lib.Deactivate(ctx, second.ID, scope), library.FailedPrecondition, library.ErrorDetails{Name: group.Name})
			if err := lib.DeactivateGroup(ctx, group.ID, scope); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(directory, first.Name, "SKILL.md")); err != nil {
				t.Fatalf("directly activated projection was removed: %v", err)
			}
			if _, err := os.Stat(filepath.Join(directory, second.Name)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("via-group-only projection remains: %v", err)
			}
			projections, err := lib.ListProjections(ctx)
			want = []library.Projection{{SkillID: first.ID, Scope: scope, Directory: directory, Direct: true}}
			if err != nil || !reflect.DeepEqual(withoutActivatedAt(projections), want) {
				t.Fatalf("ListProjections() after DeactivateGroup = %#v, %v; want %#v", projections, err, want)
			}
		})
	}
}

// 有未记录文件夹的成员在默认策略下被跳过;选 Overwrite 再次激活只补齐它,已投影的成员不重写,组激活不重复记录。
func TestActivateGroupSkipsUntrackedMemberUntilOverwrite(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	firstSource := filepath.Join(root, "first")
	secondSource := filepath.Join(root, "second")
	writeFile(t, filepath.Join(firstSource, "SKILL.md"), []byte("# First\n"), 0o644)
	writeFile(t, filepath.Join(secondSource, "SKILL.md"), []byte("# Second\n"), 0o644)
	lib := openTestLibrary(t)
	first, err := lib.Install(ctx, library.LocalSource{Path: firstSource})
	if err != nil {
		t.Fatal(err)
	}
	second, err := lib.Install(ctx, library.LocalSource{Path: secondSource})
	if err != nil {
		t.Fatal(err)
	}
	group, err := lib.CreateGroup(ctx, "shipping")
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range []library.Skill{first, second} {
		if _, err := lib.AddSkillToGroup(ctx, group.ID, skill.ID, library.ActivateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.Resolve(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(directory, second.Name, "manual.txt"), []byte("manual"), 0o644)

	results, err := lib.ActivateGroup(ctx, group.ID, scope, library.ActivateOptions{})
	requireMemberResults(t, results, err, []library.GroupMemberResult{
		{SkillID: first.ID, Scope: scope, Outcome: library.MemberProjected},
		skippedMember(second.ID, scope, library.AlreadyExists, library.ErrorDetails{Path: filepath.Join(directory, second.Name)}),
	})
	if _, err := os.Stat(filepath.Join(directory, second.Name, "manual.txt")); err != nil {
		t.Fatalf("conflicting directory changed: %v", err)
	}
	activations, err := lib.ListGroupActivations(ctx)
	if err != nil || len(activations) != 1 {
		t.Fatalf("ListGroupActivations() = %#v, %v; want one", activations, err)
	}
	writeFile(t, filepath.Join(directory, first.Name, "SKILL.md"), []byte("edited\n"), 0o644)

	results, err = lib.ActivateGroup(ctx, group.ID, scope, library.ActivateOptions{OnConflict: library.Overwrite})
	requireMemberResults(t, results, err, []library.GroupMemberResult{
		{SkillID: first.ID, Scope: scope, Outcome: library.MemberAlreadyProjected},
		{SkillID: second.ID, Scope: scope, Outcome: library.MemberProjected},
	})
	requireProjection(t, directory, first.Name, "edited\n")
	requireProjection(t, directory, second.Name, "# Second\n")
	if _, err := os.Stat(filepath.Join(directory, second.Name, "manual.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Overwrite kept the untracked folder: %v", err)
	}
	again, err := lib.ListGroupActivations(ctx)
	if err != nil || !reflect.DeepEqual(again, activations) {
		t.Fatalf("ListGroupActivations() after re-activation = %#v, %v; want %#v", again, err, activations)
	}
}

// 对自己在身份 Scope 上的投影再次激活,默认冲突策略下幂等成功,只补激活、不重写已被手动修改的文件夹;
// 记录仍是同一条身份。组激活与再次组激活同样不写盘。
func TestReactivatingNeverRewritesAnExistingProjection(t *testing.T) {
	ctx := t.Context()
	source := filepath.Join(t.TempDir(), "example-skill")
	writeFile(t, filepath.Join(source, "SKILL.md"), []byte("# Example\n"), 0o644)
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	skill, err := lib.Install(ctx, library.LocalSource{Path: source})
	if err != nil {
		t.Fatal(err)
	}
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.Resolve(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.Activate(ctx, skill.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(directory, skill.Name, "SKILL.md"), []byte("edited\n"), 0o644)
	if err := lib.Activate(ctx, skill.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatalf("re-Activate under Fail policy = %v, want success", err)
	}
	requireProjection(t, directory, skill.Name, "edited\n")
	projections, err := lib.ListProjections(ctx)
	want := []library.Projection{{SkillID: skill.ID, Scope: scope, Directory: directory, Direct: true}}
	if err != nil || !reflect.DeepEqual(withoutActivatedAt(projections), want) {
		t.Fatalf("ListProjections() after re-Activate = %#v, %v; want %#v", projections, err, want)
	}

	group, err := lib.CreateGroup(ctx, "g")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.AddSkillToGroup(ctx, group.ID, skill.ID, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		results, err := lib.ActivateGroup(ctx, group.ID, scope, library.ActivateOptions{})
		requireMemberResults(t, results, err, []library.GroupMemberResult{{SkillID: skill.ID, Scope: scope, Outcome: library.MemberAlreadyProjected}})
		requireProjection(t, directory, skill.Name, "edited\n")
	}
	projections, err = lib.ListProjections(ctx)
	want[0].Groups = []library.GroupID{group.ID}
	if err != nil || !reflect.DeepEqual(withoutActivatedAt(projections), want) {
		t.Fatalf("projections after reactivation = %#v, %v; want %#v", projections, err, want)
	}
}

// 组内同名的成员都被跳过(不按顺序先到先得),其他成员照常投影。
func TestActivateGroupSkipsAllMembersSharingAName(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "one", "dup", "SKILL.md"), []byte("# One\n"), 0o644)
	writeFile(t, filepath.Join(root, "two", "dup", "SKILL.md"), []byte("# Two\n"), 0o644)
	lib, err := library.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
	first, err := lib.Install(ctx, library.LocalSource{Path: filepath.Join(root, "one", "dup")})
	if err != nil {
		t.Fatal(err)
	}
	second, err := lib.Install(ctx, library.LocalSource{Path: filepath.Join(root, "two", "dup")})
	if err != nil {
		t.Fatal(err)
	}
	group, err := lib.CreateGroup(ctx, "duplicates")
	if err != nil {
		t.Fatal(err)
	}
	other := installNamedSkill(t, lib, "other")
	for _, skill := range []library.Skill{first, second, other} {
		if _, err := lib.AddSkillToGroup(ctx, group.ID, skill.ID, library.ActivateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.Resolve(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	results, err := lib.ActivateGroup(ctx, group.ID, scope, library.ActivateOptions{OnConflict: library.Overwrite})
	requireMemberResults(t, results, err, []library.GroupMemberResult{
		skippedMember(first.ID, scope, library.FailedPrecondition, library.ErrorDetails{Name: "dup"}),
		skippedMember(second.ID, scope, library.FailedPrecondition, library.ErrorDetails{Name: "dup"}),
		{SkillID: other.ID, Scope: scope, Outcome: library.MemberProjected},
	})
	if _, err := os.Stat(filepath.Join(directory, "dup")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("colliding members left a projection: %v", err)
	}
	requireProjection(t, directory, "other", "# other\n")
}

func TestActivateGroupFailurePreservesDirectActivation(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	firstSource := filepath.Join(root, "first")
	secondSource := filepath.Join(root, "second")
	writeFile(t, filepath.Join(firstSource, "SKILL.md"), []byte("# First\n"), 0o644)
	writeFile(t, filepath.Join(secondSource, "SKILL.md"), []byte("# Second\n"), 0o644)
	lib, err := library.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
	first, err := lib.Install(ctx, library.LocalSource{Path: firstSource})
	if err != nil {
		t.Fatal(err)
	}
	second, err := lib.Install(ctx, library.LocalSource{Path: secondSource})
	if err != nil {
		t.Fatal(err)
	}
	group, err := lib.CreateGroup(ctx, "shipping")
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range []library.Skill{first, second} {
		if _, err := lib.AddSkillToGroup(ctx, group.ID, skill.ID, library.ActivateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.Resolve(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.Activate(ctx, first.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	makeScopeReadOnly(t, directory)
	if _, err := lib.ActivateGroup(ctx, group.ID, scope, library.ActivateOptions{OnConflict: library.Overwrite}); err == nil {
		t.Fatal("ActivateGroup succeeded despite a read-only scope")
	}
	if err := os.Chmod(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, first.Name, "SKILL.md")); err != nil {
		t.Fatalf("failed group activation removed directly activated projection: %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, second.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed group activation left a partial member: %v", err)
	}
	projections, err := lib.ListProjections(ctx)
	want := []library.Projection{{SkillID: first.ID, Scope: scope, Directory: directory, Direct: true}}
	if err != nil || !reflect.DeepEqual(withoutActivatedAt(projections), want) {
		t.Fatalf("ListProjections() = %#v, %v; want %#v", projections, err, want)
	}
	if activations, err := lib.ListGroupActivations(ctx); err != nil || len(activations) != 0 {
		t.Fatalf("failed ActivateGroup left group activations: %#v, %v", activations, err)
	}
}
