package library_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tiaot33/skills-tui/library"
)

func TestUpdatePreservesHistoryAndDiffsVersions(t *testing.T) {
	ctx := t.Context()
	source := filepath.Join(t.TempDir(), "example-skill")
	writeFile(t, filepath.Join(source, "SKILL.md"), []byte("first\n"), 0o644)
	writeFile(t, filepath.Join(source, "unchanged.md"), []byte("same\n"), 0o644)
	writeFile(t, filepath.Join(source, "deleted.md"), []byte("gone\n"), 0o644)

	lib := openTestLibrary(t)
	installed, err := lib.Install(ctx, library.LocalSource{Path: source})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(source, "SKILL.md"), []byte("second\n"), 0o644)
	writeFile(t, filepath.Join(source, "added.md"), []byte("new\n"), 0o644)
	if err := os.Remove(filepath.Join(source, "deleted.md")); err != nil {
		t.Fatal(err)
	}
	updated, err := lib.Update(ctx, installed.ID)
	if err != nil {
		t.Fatal(err)
	}

	current, err := lib.GetSkill(ctx, installed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(current, updated.Skill) {
		t.Fatalf("GetSkill() = %#v, Update() = %#v", current, updated)
	}
	versions, err := lib.ListVersions(ctx, installed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 {
		t.Fatalf("len(ListVersions()) = %d, want 2", len(versions))
	}
	first, err := lib.GetVersion(ctx, versions[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.Files, installed.Files) {
		t.Fatalf("first version files = %#v, want %#v", first.Files, installed.Files)
	}
	second, err := lib.GetVersion(ctx, versions[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(second.Files, updated.Skill.Files) {
		t.Fatalf("second version files = %#v, want %#v", second.Files, updated.Skill.Files)
	}

	diff, err := lib.DiffVersions(ctx, versions[0].ID, versions[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	wantDiff := library.VersionDiff{
		Changed: []string{"SKILL.md"},
		Added:   []string{"added.md"},
		Deleted: []string{"deleted.md"},
	}
	if !reflect.DeepEqual(diff, wantDiff) {
		t.Fatalf("DiffVersions() = %#v, want %#v", diff, wantDiff)
	}
}

func TestDiffVersionsAcrossSkillsIsInvalidArgument(t *testing.T) {
	lib := openTestLibrary(t)
	first := installNamedSkill(t, lib, "one")
	second := installNamedSkill(t, lib, "two")
	oldVersions, err := lib.ListVersions(t.Context(), first.ID)
	if err != nil {
		t.Fatal(err)
	}
	newVersions, err := lib.ListVersions(t.Context(), second.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = lib.DiffVersions(t.Context(), oldVersions[0].ID, newVersions[0].ID)
	requireErrorDetails(t, err, library.InvalidArgument, library.ErrorDetails{})
}
