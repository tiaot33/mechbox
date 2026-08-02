package library_test

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tiaot33/skills-tui/library"
)

func TestBrowseSummariesOmitFilesAndDetailReadsPreserveContent(t *testing.T) {
	ctx := t.Context()
	lib, err := library.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
	root := filepath.Join(t.TempDir(), "example")
	writeFile(t, filepath.Join(root, "SKILL.md"), []byte("# Example\n"), 0o644)
	writeFile(t, filepath.Join(root, "run.sh"), []byte("#!/bin/sh\necho example\n"), 0o755)
	skill, err := lib.Install(ctx, library.LocalSource{Path: root})
	if err != nil {
		t.Fatal(err)
	}
	group, err := lib.CreateGroup(ctx, "examples")
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.AddSkillToGroup(ctx, group.ID, skill.ID); err != nil {
		t.Fatal(err)
	}
	check := func(t *testing.T, summaries []library.SkillSummary) {
		t.Helper()
		if want := []library.SkillSummary{summaryOf(skill)}; !reflect.DeepEqual(summaries, want) {
			t.Fatalf("summaries = %#v, want %#v", summaries, want)
		}
		data, err := json.Marshal(summaries[0])
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			t.Fatal(err)
		}
		if _, ok := fields["files"]; ok {
			t.Fatalf("summary exposes files: %s", data)
		}
	}
	t.Run("ListSkills", func(t *testing.T) {
		summaries, err := lib.ListSkills(ctx)
		if err != nil {
			t.Fatal(err)
		}
		check(t, summaries)
	})
	t.Run("ListGroupSkills", func(t *testing.T) {
		summaries, err := lib.ListGroupSkills(ctx, group.ID)
		if err != nil {
			t.Fatal(err)
		}
		check(t, summaries)
	})
	t.Run("ListCollections", func(t *testing.T) {
		collections, err := lib.ListCollections(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(collections) != 1 || !reflect.DeepEqual(collections[0].Source, skill.Source) {
			t.Fatalf("collections = %#v, want installed Source", collections)
		}
		check(t, collections[0].Skills)
	})
	full, err := lib.GetSkill(ctx, skill.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(full.Files, files) {
		t.Fatalf("GetSkill files = %#v, want %#v", full.Files, files)
	}
	versions, err := lib.ListVersions(ctx, skill.ID)
	if err != nil || len(versions) != 1 {
		t.Fatalf("ListVersions = %#v, %v", versions, err)
	}
	version, err := lib.GetVersion(ctx, versions[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(version.Files, files) {
		t.Fatalf("GetVersion files = %#v, want %#v", version.Files, files)
	}
}
