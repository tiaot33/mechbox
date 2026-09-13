package library_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tiaot33/skills-tui/library"
)

func TestActivateAndDeactivateGlobalSkill(t *testing.T) {
	source := filepath.Join(t.TempDir(), "example-skill")
	writeFile(t, filepath.Join(source, "SKILL.md"), []byte("# Example\n"), 0o644)
	writeFile(t, filepath.Join(source, "scripts", "run.sh"), []byte("#!/bin/sh\n"), 0o755)

	lib := openTestLibrary(t)
	ctx := t.Context()
	skill, err := lib.Install(ctx, library.LocalSource{Path: source})
	if err != nil {
		t.Fatal(err)
	}

	scopes, err := lib.ListScopes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(scopes) != 2 || scopes[0].Agent != library.AgentClaudeCode || scopes[1].Agent != library.AgentCodex {
		t.Fatalf("ListScopes() = %#v", scopes)
	}

	scope := library.Scope{Agent: library.AgentCodex, Path: filepath.Join(t.TempDir(), "skills")}
	target := filepath.Join(scope.Path, skill.Name)
	writeFile(t, filepath.Join(target, "old.txt"), []byte("old"), 0o644)
	if err := lib.Activate(ctx, skill.ID, scope, library.ActivateOptions{}); !library.IsCode(err, library.AlreadyExists) {
		t.Fatalf("Activate() conflict error = %v", err)
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
	if len(activations) != 1 || activations[0].SkillID != skill.ID || activations[0].Scope != scope {
		t.Fatalf("ListActivations() = %#v", activations)
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

func TestActivateAndDeactivateGroupPreservesIndependentActivation(t *testing.T) {
	ctx := t.Context()
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
	scope := library.Scope{Agent: library.AgentCodex, Path: filepath.Join(t.TempDir(), "skills")}
	if err := lib.Activate(ctx, first.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := lib.ActivateGroup(ctx, group.ID, scope, library.ActivateOptions{OnConflict: library.Overwrite}); err != nil {
		t.Fatal(err)
	}
	for _, skill := range []library.Skill{first, second} {
		if _, err := os.Stat(filepath.Join(scope.Path, skill.Name, "SKILL.md")); err != nil {
			t.Fatalf("member %q was not projected: %v", skill.Name, err)
		}
	}
	activations, err := lib.ListActivations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantActivations := []library.Activation{
		{SkillID: first.ID, Scope: scope, Individual: true, ViaGroups: []library.GroupID{group.ID}},
		{SkillID: second.ID, Scope: scope, ViaGroups: []library.GroupID{group.ID}},
	}
	if !reflect.DeepEqual(activations, wantActivations) {
		t.Fatalf("ListActivations() = %#v, want %#v", activations, wantActivations)
	}

	if err := lib.DeactivateGroup(ctx, group.ID, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(scope.Path, first.Name, "SKILL.md")); err != nil {
		t.Fatalf("independent activation was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(scope.Path, second.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("via-group-only activation remains: %v", err)
	}
	activations, err = lib.ListActivations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantActivations = []library.Activation{{SkillID: first.ID, Scope: scope, Individual: true}}
	if !reflect.DeepEqual(activations, wantActivations) {
		t.Fatalf("ListActivations() after DeactivateGroup = %#v, want %#v", activations, wantActivations)
	}
}

func TestActivateGroupChecksAllConflictsBeforeProjectingMembers(t *testing.T) {
	ctx := t.Context()
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
	scope := library.Scope{Agent: library.AgentCodex, Path: filepath.Join(t.TempDir(), "skills")}
	writeFile(t, filepath.Join(scope.Path, second.Name, "manual.txt"), []byte("manual"), 0o644)

	if err := lib.ActivateGroup(ctx, group.ID, scope, library.ActivateOptions{}); !library.IsCode(err, library.AlreadyExists) {
		t.Fatalf("ActivateGroup() conflict error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(scope.Path, first.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("first member was partially activated: %v", err)
	}
	if _, err := os.Stat(filepath.Join(scope.Path, second.Name, "manual.txt")); err != nil {
		t.Fatalf("conflicting directory changed: %v", err)
	}
	activations, err := lib.ListActivations(ctx)
	if err != nil || len(activations) != 0 {
		t.Fatalf("ListActivations() = %#v, %v; want none", activations, err)
	}
}

func TestReactivatingAnActiveSkillSucceedsUnderFailPolicy(t *testing.T) {
	ctx := t.Context()
	source := filepath.Join(t.TempDir(), "example-skill")
	writeFile(t, filepath.Join(source, "SKILL.md"), []byte("# Example\n"), 0o644)
	lib, err := library.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
	skill, err := lib.Install(ctx, library.LocalSource{Path: source})
	if err != nil {
		t.Fatal(err)
	}
	scope := library.Scope{Agent: library.AgentCodex, Path: filepath.Join(t.TempDir(), "skills")}
	if err := lib.Activate(ctx, skill.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := lib.Activate(ctx, skill.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatalf("re-Activate under Fail policy = %v, want success", err)
	}
	activations, err := lib.ListActivations(ctx)
	want := []library.Activation{{SkillID: skill.ID, Scope: scope, Individual: true}}
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
	scope := library.Scope{Agent: library.AgentCodex, Path: filepath.Join(t.TempDir(), "skills")}
	if err := lib.ActivateGroup(ctx, group.ID, scope, library.ActivateOptions{OnConflict: library.Overwrite}); !library.IsCode(err, library.AlreadyExists) {
		t.Fatalf("ActivateGroup with duplicate names = %v, want AlreadyExists", err)
	}
	if _, err := os.Stat(filepath.Join(scope.Path, "dup")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("colliding group left a projection: %v", err)
	}
}

func TestActivateGroupFailurePreservesIndependentActivation(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root bypasses the injected permission fault")
	}
	ctx := t.Context()
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
	scope := library.Scope{Agent: library.AgentCodex, Path: filepath.Join(t.TempDir(), "skills")}
	if err := lib.Activate(ctx, first.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(scope.Path, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(scope.Path, 0o755) })
	if err := lib.ActivateGroup(ctx, group.ID, scope, library.ActivateOptions{OnConflict: library.Overwrite}); err == nil {
		t.Fatal("ActivateGroup succeeded despite a read-only scope")
	}
	if err := os.Chmod(scope.Path, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(scope.Path, first.Name, "SKILL.md")); err != nil {
		t.Fatalf("failed group activation removed independent projection: %v", err)
	}
	if _, err := os.Stat(filepath.Join(scope.Path, second.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed group activation left a partial member: %v", err)
	}
	activations, err := lib.ListActivations(ctx)
	want := []library.Activation{{SkillID: first.ID, Scope: scope, Individual: true}}
	if err != nil || !reflect.DeepEqual(activations, want) {
		t.Fatalf("ListActivations() = %#v, %v; want %#v", activations, err, want)
	}
}
