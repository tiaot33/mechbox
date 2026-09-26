package library_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tiaot33/skills-tui/internal/library"
)

func TestCheckUpdatesAndUpdateAreSkillSpecificAndLibraryOnly(t *testing.T) {
	ctx := t.Context()
	repository := t.TempDir()
	runGit(t, repository, "init", "-b", "main")
	writeSource := func(first, second string) {
		t.Helper()
		writeFile(t, filepath.Join(repository, "first", "SKILL.md"), []byte(skillDocument("first", first)), 0o644)
		writeFile(t, filepath.Join(repository, "second", "SKILL.md"), []byte(skillDocument("second", second)), 0o644)
		commitGit(t, repository, "source state")
	}
	writeSource("old", "old")
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	fetch, err := lib.Fetch(ctx, library.SourceSpec{Type: library.SourceGitHub, Locator: library.SourceLocator{Repository: repository, Branch: "main"}})
	if err != nil {
		t.Fatal(err)
	}
	skills, err := lib.InstallFetch(ctx, fetch.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, second := skills[0], skills[1]
	scopes := []library.Scope{
		library.GlobalScope(library.AgentCodex),
		library.GlobalScope(library.AgentClaudeCode),
	}
	directories := make([]string, len(scopes))
	for i, scope := range scopes {
		if directories[i], err = lib.Resolve(ctx, scope); err != nil {
			t.Fatal(err)
		}
		if err := lib.Activate(ctx, first.ID, scope, library.ActivateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	projections, err := lib.ListProjections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	check := func(id library.SkillID, want bool) {
		t.Helper()
		got, err := checkSingleUpdate(t, lib, ctx, id)
		if err != nil || got != want {
			t.Fatalf("CheckUpdates(%d) = %v, %v; want %v", id, got, err, want)
		}
	}
	check(first.ID, false)
	writeSource("new", "old")
	check(first.ID, true)
	check(first.ID, true)
	check(second.ID, false)
	current, err := lib.GetSkill(ctx, first.ID)
	if err != nil || !reflect.DeepEqual(current, first) {
		t.Fatalf("check mutated skill: %#v, %v", current, err)
	}
	versions, err := lib.ListVersions(ctx, first.ID)
	if err != nil || len(versions) != 1 {
		t.Fatalf("check created version: %v, %v", versions, err)
	}
	// 更新一个 skill 后,共享 Source 的抓取状态不能掩盖另一个 skill 的变化。
	writeSource("new", "new")
	result, err := lib.Update(ctx, first.ID)
	if err != nil || !result.Changed || string(result.Skill.Files[0].Content) != skillDocument("first", "new") {
		t.Fatalf("Update = %#v, %v", result, err)
	}
	assertUpdateResultFields(t, result)
	check(first.ID, false)
	check(second.ID, true)
	result, err = lib.Update(ctx, first.ID)
	if err != nil || result.Changed {
		t.Fatalf("unchanged Update = %#v, %v", result, err)
	}
	assertUpdateResultFields(t, result)
	versions, err = lib.ListVersions(ctx, first.ID)
	if err != nil || len(versions) != 2 {
		t.Fatalf("versions = %v, %v", versions, err)
	}
	for _, directory := range directories {
		content, err := os.ReadFile(filepath.Join(directory, first.Name, "SKILL.md"))
		if err != nil || string(content) != skillDocument("first", "old") {
			t.Fatalf("projection changed: %q, %v", content, err)
		}
	}
	after, err := lib.ListProjections(ctx)
	if err != nil || !reflect.DeepEqual(after, projections) {
		t.Fatalf("projection records changed: %v, %v", after, err)
	}
	if err := os.RemoveAll(filepath.Join(repository, "first")); err != nil {
		t.Fatal(err)
	}
	commitGit(t, repository, "remove first")
	if _, err := checkSingleUpdate(t, lib, ctx, first.ID); !library.IsCode(err, library.NotFound) {
		t.Fatalf("missing source skill check: %v", err)
	}
	if _, err := lib.Update(ctx, first.ID); !library.IsCode(err, library.NotFound) {
		t.Fatalf("missing source skill update: %v", err)
	}
	if err := os.RemoveAll(repository); err != nil {
		t.Fatal(err)
	}
	if _, err := checkSingleUpdate(t, lib, ctx, first.ID); !library.IsCode(err, library.SourceUnreachable) {
		t.Fatalf("unreachable check: %#v", err)
	}
	if _, err := lib.Update(ctx, first.ID); !library.IsCode(err, library.SourceUnreachable) {
		t.Fatalf("unreachable update: %v", err)
	}
	versions, err = lib.ListVersions(ctx, first.ID)
	if err != nil || len(versions) != 2 {
		t.Fatalf("failed operations changed history: %v, %v", versions, err)
	}
}

func TestLocalCheckUpdatesDetectsContentAndMode(t *testing.T) {
	ctx := t.Context()
	lib := openTestLibrary(t)
	source := t.TempDir()
	path := filepath.Join(source, "SKILL.md")
	writeFile(t, path, []byte("old"), 0o644)
	skill, err := lib.Install(ctx, library.LocalSource{Path: source})
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"unchanged", "content", "mode"} {
		t.Run(change, func(t *testing.T) {
			switch change {
			case "content":
				writeFile(t, path, []byte("new"), 0o644)
			case "mode":
				if err := os.Chmod(path, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			want := change != "unchanged"
			available, err := checkSingleUpdate(t, lib, ctx, skill.ID)
			if err != nil || available != want {
				t.Fatalf("CheckUpdates = %v, %v", available, err)
			}
			result, err := lib.Update(ctx, skill.ID)
			if err != nil || result.Changed != want {
				t.Fatalf("Update = %#v, %v", result, err)
			}
			available, err = checkSingleUpdate(t, lib, ctx, skill.ID)
			if err != nil || available {
				t.Fatalf("CheckUpdates after update = %v, %v", available, err)
			}
		})
	}
	versions, err := lib.ListVersions(ctx, skill.ID)
	if err != nil || len(versions) != 3 {
		t.Fatalf("versions = %v, %v", versions, err)
	}
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	if _, err := checkSingleUpdate(t, lib, ctx, skill.ID); !library.IsCode(err, library.SourceUnreachable) {
		t.Fatalf("missing local source: %v", err)
	}
}

func TestUpdateOperationsRejectInvalidAndMissingSkillIDs(t *testing.T) {
	lib := openTestLibrary(t)
	for _, id := range []library.SkillID{-1, 0, 9999} {
		want := library.InvalidArgument
		if id > 0 {
			want = library.NotFound
		}
		if _, err := checkSingleUpdate(t, lib, t.Context(), id); !library.IsCode(err, want) {
			t.Fatalf("CheckUpdates(%d): %v, want %s", id, err, want)
		}
		if _, err := lib.Update(t.Context(), id); !library.IsCode(err, want) {
			t.Fatalf("Update(%d): %v, want %s", id, err, want)
		}
	}
}

func assertUpdateResultFields(t *testing.T, result library.UpdateResult) {
	t.Helper()
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 2 || fields["skill"] == nil || fields["changed"] == nil {
		t.Fatalf("UpdateResult fields = %s", data)
	}
}
