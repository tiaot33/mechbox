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
	writeLocalSkill(t, source, "# v1\n")
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
	if err := lib.Activate(ctx, skill.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	requireInspectionStatuses(t, lib, library.ProjectionCurrent)

	writeLocalSkill(t, source, "# v2\n")
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

	writeFile(t, filepath.Join(directory, skill.Name, "SKILL.md"), []byte(skillDocument(skill.Name, "# v1\n")), 0o644)
	requireInspectionStatuses(t, lib, library.ProjectionStale)

	writeFile(t, filepath.Join(directory, skill.Name, "SKILL.md"), []byte("# neither\n"), 0o644)
	requireInspectionStatuses(t, lib, library.ProjectionModified)
}

func TestInspectProjectionsReportsModifiedForExtraFileAndExecutableBit(t *testing.T) {
	ctx := t.Context()
	source := filepath.Join(t.TempDir(), "example-skill")
	writeLocalSkill(t, source, "# Example\n")
	writeFile(t, filepath.Join(source, "scripts", "run.sh"), []byte("#!/bin/sh\n"), 0o755)
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
		writeLocalSkill(t, source, "# Example\n")
		skill, err := fetchLocal(ctx, lib, source)
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
		writeLocalSkill(t, source, "# Example\n")
		skill, err := fetchLocal(ctx, lib, source)
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
	writeLocalSkill(t, source, "# Group\n")
	skill, err := fetchLocal(ctx, lib, source)
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
	writeLocalSkill(t, firstSource, "# First\n")
	writeLocalSkill(t, secondSource, "# Second\n")
	first, err := fetchLocal(ctx, lib, firstSource)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fetchLocal(ctx, lib, secondSource)
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
	got, err := lib.InspectProjections(t.Context(), library.ProjectionFilter{})
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

func TestInspectProjectionsByFilterIsSubsetOfFullInspection(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, firstSource := installLocalSkill(t, lib, "first", "# first v1\n")
	second := installNamedSkill(t, lib, "second")
	idle := installNamedSkill(t, lib, "idle")
	codex := library.GlobalScope(library.AgentCodex)
	claude := library.GlobalScope(library.AgentClaudeCode)
	projectScope := library.ProjectScope(library.AgentCodex, project.ID)
	for _, scope := range []library.Scope{codex, claude, projectScope} {
		if err := lib.Activate(ctx, first.ID, scope, library.ActivateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	for _, scope := range []library.Scope{codex, projectScope} {
		if err := lib.Activate(ctx, second.ID, scope, library.ActivateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	updateLocalSkill(t, lib, first.ID, firstSource, "# first v2\n")
	full := inspectProjections(t, lib)
	if len(full) != 5 {
		t.Fatalf("InspectProjections() = %#v, want 5", full)
	}

	subset := func(match func(library.ProjectionInspection) bool) []library.ProjectionInspection {
		var want []library.ProjectionInspection
		for _, inspection := range full {
			if match(inspection) {
				want = append(want, inspection)
			}
		}
		return want
	}
	tests := []struct {
		name   string
		filter library.ProjectionFilter
		want   []library.ProjectionInspection
	}{
		{name: "skill", filter: library.ProjectionFilter{SkillID: first.ID},
			want: subset(func(i library.ProjectionInspection) bool { return i.SkillID == first.ID })},
		{name: "scope", filter: scopeFilter(projectScope),
			want: subset(func(i library.ProjectionInspection) bool { return i.Scope == projectScope })},
		{name: "both", filter: library.ProjectionFilter{SkillID: second.ID, Scope: &codex},
			want: subset(func(i library.ProjectionInspection) bool { return i.SkillID == second.ID && i.Scope == codex })},
		{name: "no projections", filter: library.ProjectionFilter{SkillID: idle.ID}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := lib.InspectProjections(ctx, test.filter)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(test.want) || (len(got) > 0 && !reflect.DeepEqual(got, test.want)) {
				t.Fatalf("InspectProjections(%s) = %#v, want %#v", test.name, got, test.want)
			}
		})
	}

	unknownAgent := library.GlobalScope("unknown-agent")
	for name, filter := range map[string]library.ProjectionFilter{
		"unknown skill": {SkillID: 999},
		"unknown agent": {Scope: &unknownAgent},
	} {
		_, err := lib.InspectProjections(ctx, filter)
		if !library.IsCode(err, library.NotFound) {
			t.Fatalf("InspectProjections(%s) error = %v, want not_found", name, err)
		}
	}
	invalid := library.Scope{Kind: "nowhere", Agent: library.AgentCodex}
	_, err = lib.InspectProjections(ctx, library.ProjectionFilter{Scope: &invalid})
	requireLibraryError(t, err, library.InvalidArgument)
}
