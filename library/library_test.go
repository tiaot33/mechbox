package library_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tiaot33/skills-tui/library"
)

func TestInstallLocalSkillAndReadItBackIntact(t *testing.T) {
	source := filepath.Join(t.TempDir(), "example-skill")
	writeFile(t, filepath.Join(source, "SKILL.md"), []byte("# Example\n"), 0o644)
	writeFile(t, filepath.Join(source, "references", "guide.md"), []byte("nested\n"), 0o644)
	writeFile(t, filepath.Join(source, "scripts", "run.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755)
	writeFile(t, filepath.Join(source, "assets", "data.bin"), []byte{0x00, 0xff, 0x01}, 0o644)

	lib, err := library.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })

	installed, err := lib.Install(context.Background(), library.LocalSource{Path: source})
	if err != nil {
		t.Fatal(err)
	}
	got, err := lib.GetSkill(context.Background(), installed.ID)
	if err != nil {
		t.Fatal(err)
	}

	want := library.Skill{
		ID:   installed.ID,
		Name: "example-skill",
		Source: library.Source{
			Type: library.SourceLocal,
			Path: source,
		},
		Files: []library.File{
			{Path: "SKILL.md", Content: []byte("# Example\n"), Mode: library.ModeRegular},
			{Path: "assets/data.bin", Content: []byte{0x00, 0xff, 0x01}, Mode: library.ModeRegular},
			{Path: "references/guide.md", Content: []byte("nested\n"), Mode: library.ModeRegular},
			{Path: "scripts/run.sh", Content: []byte("#!/bin/sh\nexit 0\n"), Mode: library.ModeExecutable},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GetSkill() = %#v, want %#v", got, want)
	}
	if !reflect.DeepEqual(installed, got) {
		t.Fatalf("Install() = %#v, GetSkill() = %#v", installed, got)
	}
}

func TestUpdatePreservesHistoryAndDiffsVersions(t *testing.T) {
	ctx := context.Background()
	source := filepath.Join(t.TempDir(), "example-skill")
	writeFile(t, filepath.Join(source, "SKILL.md"), []byte("first\n"), 0o644)
	writeFile(t, filepath.Join(source, "unchanged.md"), []byte("same\n"), 0o644)
	writeFile(t, filepath.Join(source, "deleted.md"), []byte("gone\n"), 0o644)

	lib, err := library.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
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
	if !reflect.DeepEqual(current, updated) {
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
	if !reflect.DeepEqual(second.Files, updated.Files) {
		t.Fatalf("second version files = %#v, want %#v", second.Files, updated.Files)
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

func TestGroupCanContainSkillsAcrossSourcesAndSkillsCanBelongToMultipleGroups(t *testing.T) {
	root := t.TempDir()
	firstSource := filepath.Join(root, "source-one")
	secondSource := filepath.Join(root, "source-two")
	writeFile(t, filepath.Join(firstSource, "SKILL.md"), []byte("# One\n"), 0o644)
	writeFile(t, filepath.Join(secondSource, "SKILL.md"), []byte("# Two\n"), 0o644)

	lib, err := library.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })

	first, err := lib.Install(context.Background(), library.LocalSource{Path: firstSource})
	if err != nil {
		t.Fatal(err)
	}
	second, err := lib.Install(context.Background(), library.LocalSource{Path: secondSource})
	if err != nil {
		t.Fatal(err)
	}
	debugging, err := lib.CreateGroup(context.Background(), "frontend debugging")
	if err != nil {
		t.Fatal(err)
	}
	shipping, err := lib.CreateGroup(context.Background(), "shipping")
	if err != nil {
		t.Fatal(err)
	}

	for _, membership := range []struct {
		group library.Group
		skill library.Skill
	}{
		{debugging, first},
		{debugging, second},
		{shipping, first},
	} {
		if err := lib.AddSkillToGroup(context.Background(), membership.group.ID, membership.skill.ID); err != nil {
			t.Fatal(err)
		}
	}

	debuggingSkills, err := lib.ListGroupSkills(context.Background(), debugging.ID)
	if err != nil {
		t.Fatal(err)
	}
	shippingSkills, err := lib.ListGroupSkills(context.Background(), shipping.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := skillIDs(debuggingSkills), []library.SkillID{first.ID, second.ID}; !reflect.DeepEqual(got, want) {
		t.Fatalf("debugging members = %v, want %v", got, want)
	}
	if got, want := skillIDs(shippingSkills), []library.SkillID{first.ID}; !reflect.DeepEqual(got, want) {
		t.Fatalf("shipping members = %v, want %v", got, want)
	}
}

func skillIDs(skills []library.Skill) []library.SkillID {
	ids := make([]library.SkillID, len(skills))
	for i, skill := range skills {
		ids[i] = skill.ID
	}
	return ids
}
func writeFile(t *testing.T, path string, content []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, mode); err != nil {
		t.Fatal(err)
	}
}
