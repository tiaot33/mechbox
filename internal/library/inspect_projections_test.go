package library_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/tiaot33/skills-tui/internal/library"
)

func TestInspectProjectionsCurrentStaleModifiedAndReactivate(t *testing.T) {
	ctx := t.Context()
	source := filepath.Join(t.TempDir(), "example-skill")
	writeFile(t, filepath.Join(source, "SKILL.md"), []byte("# v1\n"), 0o644)
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
	requireInspectionStatuses(t, lib, library.ProjectionCurrent)

	writeFile(t, filepath.Join(source, "SKILL.md"), []byte("# v2\n"), 0o644)
	if result, err := lib.Update(ctx, skill.ID); err != nil || !result.Changed {
		t.Fatalf("Update = %#v, %v", result, err)
	}
	requireInspectionStatuses(t, lib, library.ProjectionStale)

	// 再次激活不写盘,投影仍是 stale;停用后重新激活才按当前版本投影。
	if err := lib.Activate(ctx, skill.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	requireInspectionStatuses(t, lib, library.ProjectionStale)
	if err := lib.Deactivate(ctx, skill.ID, scope); err != nil {
		t.Fatal(err)
	}
	if err := lib.Activate(ctx, skill.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	requireInspectionStatuses(t, lib, library.ProjectionCurrent)

	writeFile(t, filepath.Join(directory, skill.Name, "SKILL.md"), []byte("# v1\n"), 0o644)
	requireInspectionStatuses(t, lib, library.ProjectionStale)

	writeFile(t, filepath.Join(directory, skill.Name, "SKILL.md"), []byte("# neither\n"), 0o644)
	requireInspectionStatuses(t, lib, library.ProjectionModified)
}

func TestInspectProjectionsReportsModifiedForExtraFileAndExecutableBit(t *testing.T) {
	ctx := t.Context()
	source := filepath.Join(t.TempDir(), "example-skill")
	writeFile(t, filepath.Join(source, "SKILL.md"), []byte("# Example\n"), 0o644)
	writeFile(t, filepath.Join(source, "scripts", "run.sh"), []byte("#!/bin/sh\n"), 0o755)
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
	target := filepath.Join(directory, skill.Name)

	writeFile(t, filepath.Join(target, "extra.txt"), []byte("extra"), 0o644)
	requireInspectionStatuses(t, lib, library.ProjectionModified)
	if err := os.Remove(filepath.Join(target, "extra.txt")); err != nil {
		t.Fatal(err)
	}
	requireInspectionStatuses(t, lib, library.ProjectionCurrent)

	script := filepath.Join(target, "scripts", "run.sh")
	if err := os.Chmod(script, 0o644); err != nil {
		t.Fatal(err)
	}
	requireInspectionStatuses(t, lib, library.ProjectionModified)
}

func TestInspectProjectionsReportsMissingWhenFolderRemoved(t *testing.T) {
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
	if err := os.RemoveAll(filepath.Join(directory, skill.Name)); err != nil {
		t.Fatal(err)
	}
	requireInspectionStatuses(t, lib, library.ProjectionMissing)
}

func TestInspectProjectionsReportsDirectoryMissing(t *testing.T) {
	t.Run("deleted project root", func(t *testing.T) {
		ctx := t.Context()
		t.Setenv("HOME", t.TempDir())
		lib := openTestLibrary(t)
		projectRoot := t.TempDir()
		project, err := lib.RegisterProject(ctx, projectRoot)
		if err != nil {
			t.Fatal(err)
		}
		source := filepath.Join(t.TempDir(), "example-skill")
		writeFile(t, filepath.Join(source, "SKILL.md"), []byte("# Example\n"), 0o644)
		skill, err := lib.Install(ctx, library.LocalSource{Path: source})
		if err != nil {
			t.Fatal(err)
		}
		scope := library.ProjectScope(library.AgentCodex, project.ID)
		if err := lib.Activate(ctx, skill.ID, scope, library.ActivateOptions{}); err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(projectRoot); err != nil {
			t.Fatal(err)
		}
		requireInspectionStatuses(t, lib, library.ProjectionDirectoryMissing)
	})

	t.Run("foreign project path", func(t *testing.T) {
		ctx := t.Context()
		t.Setenv("HOME", t.TempDir())
		path := filepath.Join(t.TempDir(), "library.db")
		lib, err := library.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		project, err := lib.RegisterProject(ctx, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		source := filepath.Join(t.TempDir(), "example-skill")
		writeFile(t, filepath.Join(source, "SKILL.md"), []byte("# Example\n"), 0o644)
		skill, err := lib.Install(ctx, library.LocalSource{Path: source})
		if err != nil {
			t.Fatal(err)
		}
		scope := library.ProjectScope(library.AgentCodex, project.ID)
		if err := lib.Activate(ctx, skill.ID, scope, library.ActivateOptions{}); err != nil {
			t.Fatal(err)
		}
		if err := lib.Close(); err != nil {
			t.Fatal(err)
		}
		missing := filepath.Join(t.TempDir(), "other-machine")
		editLibraryFixture(t, path, `UPDATE projects SET path = ?`, missing)
		lib, err = library.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = lib.Close() })
		requireInspectionStatuses(t, lib, library.ProjectionDirectoryMissing)
	})
}

func TestInspectProjectionsInspectsGroupProjections(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	source := filepath.Join(t.TempDir(), "group-skill")
	writeFile(t, filepath.Join(source, "SKILL.md"), []byte("# Group\n"), 0o644)
	skill, err := lib.Install(ctx, library.LocalSource{Path: source})
	if err != nil {
		t.Fatal(err)
	}
	group, err := lib.CreateGroup(ctx, "shipping")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.AddSkillToGroup(ctx, group.ID, skill.ID); err != nil {
		t.Fatal(err)
	}
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.Resolve(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.ActivateGroup(ctx, group.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	got := inspectProjections(t, lib)
	want := []library.ProjectionInspection{{
		Projection: library.Projection{
			SkillID:   skill.ID,
			Scope:     scope,
			Directory: directory,
			Groups:    []library.GroupID{group.ID},
		},
		Status: library.ProjectionCurrent,
	}}
	if len(got) == 1 {
		got[0].Projection.CreatedAt = time.Time{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("InspectProjections() = %#v, want %#v", got, want)
	}
}

func TestInspectProjectionsIsReadOnlyAndMatchesListProjections(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	firstSource := filepath.Join(t.TempDir(), "first")
	secondSource := filepath.Join(t.TempDir(), "second")
	writeFile(t, filepath.Join(firstSource, "SKILL.md"), []byte("# First\n"), 0o644)
	writeFile(t, filepath.Join(secondSource, "SKILL.md"), []byte("# Second\n"), 0o644)
	first, err := lib.Install(ctx, library.LocalSource{Path: firstSource})
	if err != nil {
		t.Fatal(err)
	}
	second, err := lib.Install(ctx, library.LocalSource{Path: secondSource})
	if err != nil {
		t.Fatal(err)
	}
	scopes := []library.Scope{
		library.GlobalScope(library.AgentClaudeCode),
		library.GlobalScope(library.AgentCodex),
	}
	var directories []string
	for _, skill := range []library.Skill{first, second} {
		for _, scope := range scopes {
			directory, err := lib.Resolve(ctx, scope)
			if err != nil {
				t.Fatal(err)
			}
			directories = append(directories, directory)
			if err := lib.Activate(ctx, skill.ID, scope, library.ActivateOptions{}); err != nil {
				t.Fatal(err)
			}
		}
	}
	before, err := lib.ListProjections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	skillsBefore, err := lib.ListSkills(ctx)
	if err != nil {
		t.Fatal(err)
	}
	diskBefore := snapshotTree(t, directories...)

	got := inspectProjections(t, lib)

	after, err := lib.ListProjections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	skillsAfter, err := lib.ListSkills(ctx)
	if err != nil {
		t.Fatal(err)
	}
	diskAfter := snapshotTree(t, directories...)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("ListProjections changed: %#v, want %#v", after, before)
	}
	if !reflect.DeepEqual(skillsAfter, skillsBefore) {
		t.Fatalf("ListSkills changed: %#v, want %#v", skillsAfter, skillsBefore)
	}
	if !reflect.DeepEqual(diskAfter, diskBefore) {
		t.Fatalf("disk changed: %#v, want %#v", diskAfter, diskBefore)
	}
	if len(got) != len(before) {
		t.Fatalf("InspectProjections len=%d, ListProjections len=%d: %#v", len(got), len(before), got)
	}
	for i := range before {
		if !reflect.DeepEqual(got[i].Projection, before[i]) {
			t.Fatalf("InspectProjections()[%d].Projection = %#v, want %#v", i, got[i].Projection, before[i])
		}
		if got[i].Status != library.ProjectionCurrent {
			t.Fatalf("InspectProjections()[%d].Status = %q, want current", i, got[i].Status)
		}
	}
}

func inspectProjections(t *testing.T, lib library.Library) []library.ProjectionInspection {
	t.Helper()
	got, err := lib.InspectProjections(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func requireInspectionStatuses(t *testing.T, lib library.Library, want ...library.ProjectionStatus) {
	t.Helper()
	got := inspectProjections(t, lib)
	if len(got) != len(want) {
		t.Fatalf("InspectProjections() = %#v, want %d statuses %v", got, len(want), want)
	}
	for i := range want {
		if got[i].Status != want[i] {
			t.Fatalf("InspectProjections()[%d].Status = %q, want %q (full %#v)", i, got[i].Status, want[i], got)
		}
	}
}

func snapshotTree(t *testing.T, roots ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			out[path] = fmt.Sprintf("%s %q", info.Mode(), content)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}
