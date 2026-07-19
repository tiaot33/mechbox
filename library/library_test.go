package library_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

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
			ID:      installed.Source.ID,
			Type:    library.SourceLocal,
			Locator: library.SourceLocator{Path: source},
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

func TestInstallFetchedSkillsPersistsTypedSources(t *testing.T) {
	lib, err := library.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })

	fetchedAt := time.Date(2026, time.July, 19, 10, 30, 0, 0, time.UTC)
	tests := []struct {
		name    string
		source  library.Source
		skill   string
		wantRef string
	}{
		{
			name: "github",
			source: library.Source{Type: library.SourceGitHub, Locator: library.SourceLocator{
				Repository: "github.com/example/skills", Branch: "main",
			}, LastFetched: fetchedAt, LastSeenRef: "abc123"},
			skill: "github-skill", wantRef: "def456",
		},
		{
			name: "url",
			source: library.Source{Type: library.SourceURL, Locator: library.SourceLocator{
				URL: "https://example.com/url-skill.zip",
			}, LastFetched: fetchedAt, LastSeenRef: "sha256:old"},
			skill: "url-skill", wantRef: "sha256:new",
		},
		{
			name: "store",
			source: library.Source{Type: library.SourceStore, Locator: library.SourceLocator{
				PackageID: "example/store-skill", Version: "1.0.0",
			}, LastFetched: fetchedAt, LastSeenRef: "1.0.0"},
			skill: "store-skill", wantRef: "1.1.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			installed, err := lib.InstallFetched(context.Background(), tt.source, tt.skill, []library.File{{
				Path: "SKILL.md", Content: []byte("# " + tt.skill + "\n"), Mode: library.ModeRegular,
			}})
			if err != nil {
				t.Fatal(err)
			}
			got, err := lib.GetSkill(context.Background(), installed.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, installed) {
				t.Fatalf("GetSkill() = %#v, want %#v", got, installed)
			}
			wantSource := tt.source
			wantSource.ID = got.Source.ID
			if got.Source.ID == 0 || !reflect.DeepEqual(got.Source, wantSource) {
				t.Fatalf("Source = %#v, want persisted source %#v", got.Source, tt.source)
			}

			available, err := lib.SourceHasUpdate(context.Background(), got.Source.ID, got.Source.LastSeenRef)
			if err != nil {
				t.Fatal(err)
			}
			if available {
				t.Fatal("SourceHasUpdate() = true for the installed ref")
			}
			available, err = lib.SourceHasUpdate(context.Background(), got.Source.ID, tt.wantRef)
			if err != nil {
				t.Fatal(err)
			}
			if !available {
				t.Fatal("SourceHasUpdate() = false for a newer ref")
			}
		})
	}
}

func TestSkillsFromSameSourceShareSource(t *testing.T) {
	lib, err := library.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })

	source := library.Source{
		Type:        library.SourceGitHub,
		Locator:     library.SourceLocator{Repository: "github.com/example/skills", Branch: "main"},
		LastFetched: time.Now().UTC().Truncate(time.Second),
		LastSeenRef: "abc123",
	}
	first, err := lib.InstallFetched(context.Background(), source, "first", []library.File{{Path: "SKILL.md", Content: []byte("# first\n"), Mode: library.ModeRegular}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := lib.InstallFetched(context.Background(), source, "second", []library.File{{Path: "SKILL.md", Content: []byte("# second\n"), Mode: library.ModeRegular}})
	if err != nil {
		t.Fatal(err)
	}
	if first.Source.ID != second.Source.ID {
		t.Fatalf("source IDs = %d and %d, want one shared Source", first.Source.ID, second.Source.ID)
	}
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
