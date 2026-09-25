package library_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tiaot33/skills-tui/internal/library"
)

func TestPreviewActivateReadyAndProjected(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	ctx := t.Context()
	source := filepath.Join(t.TempDir(), "example")
	writeFile(t, filepath.Join(source, "SKILL.md"), []byte("# Example"), 0o644)
	skill, err := lib.Install(ctx, library.LocalSource{Path: source})
	if err != nil {
		t.Fatal(err)
	}
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.Resolve(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := lib.PreviewActivate(ctx, skill.ID, scope)
	if err != nil || preview.Status != library.ActivateReady || preview.SkillID != skill.ID || preview.Name != skill.Name || preview.Directory != directory || preview.Target != filepath.Join(directory, skill.Name) {
		t.Fatalf("preview = %#v, %v", preview, err)
	}
	if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("preview created directory: %v", err)
	}
	if err := lib.Activate(ctx, skill.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, missing := range []bool{false, true} {
		if missing {
			if err := os.RemoveAll(preview.Target); err != nil {
				t.Fatal(err)
			}
		}
		got, err := lib.PreviewActivate(ctx, skill.ID, scope)
		if err != nil || got.Status != library.ActivateProjected {
			t.Fatalf("projected = %#v, %v", got, err)
		}
	}
}

func TestPreviewActivateOccupancyAndUntracked(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	ctx := t.Context()
	install := func() library.Skill {
		source := filepath.Join(t.TempDir(), "same")
		writeFile(t, filepath.Join(source, "SKILL.md"), []byte("# Same"), 0o644)
		skill, err := lib.Install(ctx, library.LocalSource{Path: source})
		if err != nil {
			t.Fatal(err)
		}
		return skill
	}
	a, b := install(), install()
	scope := library.GlobalScope(library.AgentCodex)
	if err := lib.Activate(ctx, a.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	preview, err := lib.PreviewActivate(ctx, b.ID, scope)
	if err != nil || preview.Status != library.ActivateOccupied || preview.OccupierSkillID != a.ID || preview.OccupierName != a.Name {
		t.Fatalf("occupied = %#v, %v", preview, err)
	}
	for _, policy := range []library.ConflictPolicy{"", library.Overwrite} {
		requireErrorDetails(t, lib.Activate(ctx, b.ID, scope, library.ActivateOptions{OnConflict: policy}), library.FailedPrecondition, library.ErrorDetails{SkillID: a.ID, Name: a.Name, Path: preview.Target})
	}
	project, err := lib.RegisterProject(ctx, home)
	if err != nil {
		t.Fatal(err)
	}
	other := library.ProjectScope(library.AgentCodex, project.ID)
	preview, err = lib.PreviewActivate(ctx, a.ID, other)
	if err != nil || preview.Status != library.ActivateOccupied || preview.OccupierSkillID != a.ID || preview.OccupierName != a.Name {
		t.Fatalf("cross scope = %#v, %v", preview, err)
	}
	requireErrorDetails(t, lib.Activate(ctx, a.ID, other, library.ActivateOptions{}), library.FailedPrecondition, library.ErrorDetails{Path: preview.Directory})
	if err := lib.Deactivate(ctx, a.ID, scope); err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"# Same", "user edited"} {
		writeFile(t, filepath.Join(preview.Target, "SKILL.md"), []byte(content), 0o644)
		got, err := lib.PreviewActivate(ctx, b.ID, scope)
		if err != nil || got.Status != library.ActivateUntracked || got.OccupierSkillID != 0 || got.OccupierName != "" {
			t.Fatalf("untracked = %#v, %v", got, err)
		}
		data, err := os.ReadFile(filepath.Join(got.Target, "SKILL.md"))
		if err != nil || string(data) != content {
			t.Fatalf("preview changed content: %q, %v", data, err)
		}
	}
	requireErrorDetails(t, lib.Activate(ctx, b.ID, scope, library.ActivateOptions{}), library.AlreadyExists, library.ErrorDetails{Path: preview.Target})
	if err := lib.Activate(ctx, b.ID, scope, library.ActivateOptions{OnConflict: library.Overwrite}); err != nil {
		t.Fatal(err)
	}
}

func TestPreviewActivateGroupReportsDuplicatesAndMemberStates(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	ctx := t.Context()
	scope := library.GlobalScope(library.AgentCodex)
	group, err := lib.CreateGroup(ctx, "group")
	if err != nil {
		t.Fatal(err)
	}
	empty, err := lib.PreviewActivateGroup(ctx, group.ID, scope)
	if err != nil || len(empty.Members) != 0 || len(empty.DuplicateNames) != 0 {
		t.Fatalf("empty = %#v, %v", empty, err)
	}
	for _, name := range []string{"same", "same", "ready"} {
		source := filepath.Join(t.TempDir(), name)
		writeFile(t, filepath.Join(source, "SKILL.md"), []byte("# Skill"), 0o644)
		skill, err := lib.Install(ctx, library.LocalSource{Path: source})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := lib.AddSkillToGroup(ctx, group.ID, skill.ID, library.ActivateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	members, err := lib.ListGroupSkills(ctx, group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.Activate(ctx, members[0].ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	before, err := lib.ListProjections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got, err := lib.PreviewActivateGroup(ctx, group.ID, scope)
	if err != nil || len(got.Members) != 3 || !reflect.DeepEqual(got.DuplicateNames, []string{"same"}) {
		t.Fatalf("group = %#v, %v", got, err)
	}
	for i, status := range []library.ActivatePreviewStatus{library.ActivateProjected, library.ActivateOccupied, library.ActivateReady} {
		if got.Members[i].SkillID != members[i].ID || got.Members[i].Status != status {
			t.Fatalf("member %d = %#v", i, got.Members[i])
		}
	}
	after, err := lib.ListProjections(ctx)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("preview changed projections: %#v, %v", after, err)
	}
	afterSkills, err := lib.ListGroupSkills(ctx, group.ID)
	if err != nil || !reflect.DeepEqual(members, afterSkills) {
		t.Fatalf("preview changed skills: %#v, %v", afterSkills, err)
	}
	if err := lib.RemoveSkillFromGroup(ctx, group.ID, members[0].ID, library.RemoveFromGroupOptions{}); err != nil {
		t.Fatal(err)
	}
	got, err = lib.PreviewActivateGroup(ctx, group.ID, scope)
	if err != nil || len(got.DuplicateNames) != 0 || len(got.Members) != 2 || got.Members[0].Status != library.ActivateOccupied || got.Members[1].Status != library.ActivateReady {
		t.Fatalf("mixed = %#v, %v", got, err)
	}
}

func TestActivationPreviewsReportMissingAndCanceled(t *testing.T) {
	lib := openTestLibrary(t)
	ctx := t.Context()
	group, err := lib.CreateGroup(ctx, "empty")
	if err != nil {
		t.Fatal(err)
	}
	scope := library.GlobalScope(library.AgentCodex)
	if _, err := lib.PreviewActivate(ctx, -1, scope); !library.IsCode(err, library.NotFound) {
		t.Fatalf("unknown skill: %v", err)
	}
	if _, err := lib.PreviewActivateGroup(ctx, -1, scope); !library.IsCode(err, library.NotFound) {
		t.Fatalf("unknown group: %v", err)
	}
	if _, err := lib.PreviewActivateGroup(ctx, group.ID, library.GlobalScope("missing")); !library.IsCode(err, library.NotFound) {
		t.Fatalf("unknown scope: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := lib.PreviewActivate(canceled, -1, scope); !library.IsCode(err, library.Canceled) {
		t.Fatalf("canceled skill: %v", err)
	}
	if _, err := lib.PreviewActivateGroup(canceled, group.ID, scope); !library.IsCode(err, library.Canceled) {
		t.Fatalf("canceled group: %v", err)
	}
}
