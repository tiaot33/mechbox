package library_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/tiaot33/skills-tui/internal/library"
)

func TestUpdatePreservesHistoryAndDiffsVersions(t *testing.T) {
	ctx := t.Context()
	source := filepath.Join(t.TempDir(), "example-skill")
	writeLocalSkill(t, source, "first\n")
	writeFile(t, filepath.Join(source, "unchanged.md"), []byte("same\n"), 0o644)
	writeFile(t, filepath.Join(source, "deleted.md"), []byte("gone\n"), 0o644)

	lib := openTestLibrary(t)
	fetched, err := fetchLocal(ctx, lib, source)
	if err != nil {
		t.Fatal(err)
	}
	writeLocalSkill(t, source, "second\n")
	writeFile(t, filepath.Join(source, "added.md"), []byte("new\n"), 0o644)
	if err := os.Remove(filepath.Join(source, "deleted.md")); err != nil {
		t.Fatal(err)
	}
	updated, err := lib.Update(ctx, fetched.ID)
	if err != nil {
		t.Fatal(err)
	}

	current, err := lib.GetSkill(ctx, fetched.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(current, updated.Skill) {
		t.Fatalf("GetSkill() = %#v, Update() = %#v", current, updated)
	}
	versions, err := lib.ListVersions(ctx, fetched.ID)
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
	if !reflect.DeepEqual(first.Files, fetched.Files) {
		t.Fatalf("first version files = %#v, want %#v", first.Files, fetched.Files)
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

func TestUpdateChangesDescriptionOnNewVersionAndKeepsFetchedAt(t *testing.T) {
	ctx := t.Context()
	source := filepath.Join(t.TempDir(), "timed")
	writeFile(t, filepath.Join(source, "SKILL.md"), []byte("---\nname: timed\ndescription: first edition\n---\nfirst\n"), 0o644)
	lib := openTestLibrary(t)
	fetched, err := fetchLocal(ctx, lib, source)
	if err != nil {
		t.Fatal(err)
	}
	installedAt := fetched.FetchedAt
	writeFile(t, filepath.Join(source, "SKILL.md"), []byte("---\nname: timed\ndescription: second edition\n---\nsecond\n"), 0o644)
	time.Sleep(time.Millisecond)
	updated, err := lib.Update(ctx, fetched.ID)
	if err != nil || !updated.Changed {
		t.Fatalf("Update = %#v, %v", updated, err)
	}
	current, err := lib.GetSkill(ctx, fetched.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Description != "second edition" || updated.Skill.Description != "second edition" {
		t.Fatalf("updated Description = %q / %q, want second edition", current.Description, updated.Skill.Description)
	}
	if !current.FetchedAt.Equal(installedAt) || current.UpdatedAt.Equal(installedAt) || current.UpdatedAt.Before(installedAt) {
		t.Fatalf("FetchedAt %v UpdatedAt %v, want FetchedAt unchanged and UpdatedAt later", current.FetchedAt, current.UpdatedAt)
	}
	versions, err := lib.ListVersions(ctx, fetched.ID)
	if err != nil || len(versions) != 2 {
		t.Fatalf("ListVersions = %#v, %v", versions, err)
	}
	if versions[0].Description != "first edition" || versions[1].Description != "second edition" {
		t.Fatalf("version descriptions = %q, %q", versions[0].Description, versions[1].Description)
	}
	if versions[0].CreatedAt.Equal(versions[1].CreatedAt) {
		t.Fatal("new version CreatedAt did not advance")
	}
}

func TestDiffVersionsAcrossSkillsIsInvalidArgument(t *testing.T) {
	lib := openTestLibrary(t)
	first := fetchNamedSkill(t, lib, "one")
	second := fetchNamedSkill(t, lib, "two")
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
