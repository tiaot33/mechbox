package library_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tiaot33/skills-tui/library"
)

// 单项激活的主路径只选定 Agent(全局 Scope):目录由 Library 按现行布局在隔离的 $HOME 下算出,
// 冲突 / Overwrite / Deactivate 的断言全部对着 Resolve 给出的目录,而不是测试自拟的路径。
func TestActivateAndDeactivateGlobalSkill(t *testing.T) {
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

	scope := library.GlobalScope{Agent: library.AgentCodex}
	directory, err := lib.Resolve(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if directory != filepath.Join(home, ".agents", "skills") {
		t.Fatalf("Resolve(%#v) = %q; want under isolated HOME", scope, directory)
	}
	target := filepath.Join(directory, skill.Name)
	writeFile(t, filepath.Join(target, "old.txt"), []byte("old"), 0o644)
	if err := lib.Activate(ctx, skill.ID, scope, library.ActivateOptions{}); !library.IsCode(err, library.AlreadyExists) {
		t.Fatalf("Activate() conflict error = %v", err)
	} else {
		requireErrorDetails(t, err, library.AlreadyExists, library.ErrorDetails{Path: target})
	}
	if err := lib.Activate(ctx, skill.ID, scope, library.ActivateOptions{OnConflict: library.Overwrite}); err != nil {
		t.Fatal(err)
	}
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
	activations, err := lib.ListActivations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []library.Activation{{
		SkillID:    skill.ID,
		Scope:      scope,
		Directory:  directory,
		Individual: true,
	}}
	if !reflect.DeepEqual(activations, want) {
		t.Fatalf("ListActivations() = %#v, want %#v", activations, want)
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
	activations, err = lib.ListActivations(ctx)
	if err != nil || len(activations) != 0 {
		t.Fatalf("ListActivations() after deactivate = %#v, %v", activations, err)
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
}

func TestDeactivateGroupBackedSkillNamesTheGroup(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	skill := installNamedSkill(t, lib, "grouped")
	group, err := lib.CreateGroup(t.Context(), "only")
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.AddSkillToGroup(t.Context(), group.ID, skill.ID); err != nil {
		t.Fatal(err)
	}
	scope := library.GlobalScope{Agent: library.AgentCodex}
	if err := lib.ActivateGroup(t.Context(), group.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	requireErrorDetails(t, lib.Deactivate(t.Context(), skill.ID, scope), library.FailedPrecondition, library.ErrorDetails{Name: "only"})
}

func TestActivateAndDeactivateGroupPreservesIndependentActivation(t *testing.T) {
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
		if err := lib.AddSkillToGroup(ctx, group.ID, skill.ID); err != nil {
			t.Fatal(err)
		}
	}
	scope := library.GlobalScope{Agent: library.AgentCodex}
	directory, err := lib.Resolve(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.Activate(ctx, first.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := lib.ActivateGroup(ctx, group.ID, scope, library.ActivateOptions{OnConflict: library.Overwrite}); err != nil {
		t.Fatal(err)
	}
	for _, skill := range []library.Skill{first, second} {
		if _, err := os.Stat(filepath.Join(directory, skill.Name, "SKILL.md")); err != nil {
			t.Fatalf("member %q was not projected: %v", skill.Name, err)
		}
	}
	activations, err := lib.ListActivations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantActivations := []library.Activation{
		{SkillID: first.ID, Scope: scope, Directory: directory, Individual: true, ViaGroups: []library.GroupID{group.ID}},
		{SkillID: second.ID, Scope: scope, Directory: directory, ViaGroups: []library.GroupID{group.ID}},
	}
	if !reflect.DeepEqual(activations, wantActivations) {
		t.Fatalf("ListActivations() = %#v, want %#v", activations, wantActivations)
	}

	if err := lib.DeactivateGroup(ctx, group.ID, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, first.Name, "SKILL.md")); err != nil {
		t.Fatalf("independent activation was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, second.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("via-group-only activation remains: %v", err)
	}
	activations, err = lib.ListActivations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantActivations = []library.Activation{{SkillID: first.ID, Scope: scope, Directory: directory, Individual: true}}
	if !reflect.DeepEqual(activations, wantActivations) {
		t.Fatalf("ListActivations() after DeactivateGroup = %#v, want %#v", activations, wantActivations)
	}
}

func TestActivateGroupChecksAllConflictsBeforeProjectingMembers(t *testing.T) {
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
		if err := lib.AddSkillToGroup(ctx, group.ID, skill.ID); err != nil {
			t.Fatal(err)
		}
	}
	scope := library.GlobalScope{Agent: library.AgentCodex}
	directory, err := lib.Resolve(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(directory, second.Name, "manual.txt"), []byte("manual"), 0o644)

	err = lib.ActivateGroup(ctx, group.ID, scope, library.ActivateOptions{})
	requireErrorDetails(t, err, library.AlreadyExists, library.ErrorDetails{Path: filepath.Join(directory, second.Name)})
	if _, err := os.Stat(filepath.Join(directory, first.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("first member was partially activated: %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, second.Name, "manual.txt")); err != nil {
		t.Fatalf("conflicting directory changed: %v", err)
	}
	activations, err := lib.ListActivations(ctx)
	if err != nil || len(activations) != 0 {
		t.Fatalf("ListActivations() = %#v, %v; want none", activations, err)
	}
}

// 对自己在身份 Scope 上的投影再次激活,默认冲突策略下幂等成功并刷新文件;记录仍是同一条身份。
func TestReactivatingAnActiveSkillSucceedsUnderFailPolicy(t *testing.T) {
	ctx := t.Context()
	source := filepath.Join(t.TempDir(), "example-skill")
	writeFile(t, filepath.Join(source, "SKILL.md"), []byte("# Example\n"), 0o644)
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	skill, err := lib.Install(ctx, library.LocalSource{Path: source})
	if err != nil {
		t.Fatal(err)
	}
	scope := library.GlobalScope{Agent: library.AgentCodex}
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
	content, err := os.ReadFile(filepath.Join(directory, skill.Name, "SKILL.md"))
	if err != nil || string(content) != "# Example\n" {
		t.Fatalf("re-Activate did not refresh SKILL.md: %q, %v", content, err)
	}
	activations, err := lib.ListActivations(ctx)
	want := []library.Activation{{SkillID: skill.ID, Scope: scope, Directory: directory, Individual: true}}
	if err != nil || !reflect.DeepEqual(activations, want) {
		t.Fatalf("ListActivations() after re-Activate = %#v, %v; want %#v", activations, err, want)
	}

	group, err := lib.CreateGroup(ctx, "g")
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.AddSkillToGroup(ctx, group.ID, skill.ID); err != nil {
		t.Fatal(err)
	}
	if err := lib.ActivateGroup(ctx, group.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatalf("ActivateGroup onto active skill = %v, want success", err)
	}
	if err := lib.ActivateGroup(ctx, group.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatalf("re-ActivateGroup under Fail policy = %v, want success", err)
	}
	activations, err = lib.ListActivations(ctx)
	want[0].ViaGroups = []library.GroupID{group.ID}
	if err != nil || !reflect.DeepEqual(activations, want) {
		t.Fatalf("origins after reactivation = %#v, %v; want %#v", activations, err, want)
	}
}

func TestActivateGroupRejectsCollidingMemberNames(t *testing.T) {
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
	for _, skill := range []library.Skill{first, second} {
		if err := lib.AddSkillToGroup(ctx, group.ID, skill.ID); err != nil {
			t.Fatal(err)
		}
	}
	scope := library.GlobalScope{Agent: library.AgentCodex}
	directory, err := lib.Resolve(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	err = lib.ActivateGroup(ctx, group.ID, scope, library.ActivateOptions{OnConflict: library.Overwrite})
	requireErrorDetails(t, err, library.FailedPrecondition, library.ErrorDetails{Name: "dup"})
	if _, err := os.Stat(filepath.Join(directory, "dup")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("colliding group left a projection: %v", err)
	}
}

func TestActivateGroupFailurePreservesIndependentActivation(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root bypasses the injected permission fault")
	}
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
		if err := lib.AddSkillToGroup(ctx, group.ID, skill.ID); err != nil {
			t.Fatal(err)
		}
	}
	scope := library.GlobalScope{Agent: library.AgentCodex}
	directory, err := lib.Resolve(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.Activate(ctx, first.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(directory, 0o755) })
	if err := lib.ActivateGroup(ctx, group.ID, scope, library.ActivateOptions{OnConflict: library.Overwrite}); err == nil {
		t.Fatal("ActivateGroup succeeded despite a read-only scope")
	}
	if err := os.Chmod(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, first.Name, "SKILL.md")); err != nil {
		t.Fatalf("failed group activation removed independent projection: %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, second.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed group activation left a partial member: %v", err)
	}
	activations, err := lib.ListActivations(ctx)
	want := []library.Activation{{SkillID: first.ID, Scope: scope, Directory: directory, Individual: true}}
	if err != nil || !reflect.DeepEqual(activations, want) {
		t.Fatalf("ListActivations() = %#v, %v; want %#v", activations, err, want)
	}
}
