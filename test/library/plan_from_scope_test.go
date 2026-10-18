package library_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tiaot33/mechbox/internal/library"
)

func TestPlanUpdateFromScopeNeedsAnEditedValidFolder(t *testing.T) {
	ctx := t.Context()
	f := newScopeEditFixture(t)
	lib, skill := f.lib, f.skill
	codexFolder := filepath.Join(f.directories[f.codex], "pdf")
	_, err := lib.PlanUpdateFromScope(ctx, skill.ID, f.codex)
	requireErrorDetails(t, err, library.FailedPrecondition, library.ErrorDetails{Path: codexFolder})

	other := fetchSkillWithBody(t, lib, "other", "v1")
	_, err = lib.PlanUpdateFromScope(ctx, other.ID, f.claude)
	requireLibraryError(t, err, library.NotFound)

	writeFile(t, filepath.Join(codexFolder, "SKILL.md"), []byte(skillDocument("pdf-kit", "renamed by hand")), 0o644)
	_, err = lib.PlanUpdateFromScope(ctx, skill.ID, f.codex)
	requireErrorDetails(t, err, library.SourceInvalid, library.ErrorDetails{Path: codexFolder, Name: "pdf-kit"})

	writeFile(t, filepath.Join(codexFolder, "SKILL.md"), []byte("no frontmatter"), 0o644)
	_, err = lib.PlanUpdateFromScope(ctx, skill.ID, f.codex)
	requireErrorDetails(t, err, library.SourceInvalid, library.ErrorDetails{Path: codexFolder})

	if err := os.Remove(filepath.Join(codexFolder, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(codexFolder, "notes.md"), []byte("notes"), 0o644)
	_, err = lib.PlanUpdateFromScope(ctx, skill.ID, f.codex)
	requireErrorDetails(t, err, library.SourceInvalid, library.ErrorDetails{Path: codexFolder})

	large, err := os.Create(filepath.Join(codexFolder, "large.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err := large.Truncate(65 << 20); err != nil {
		t.Fatal(err)
	}
	if err := large.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = lib.PlanUpdateFromScope(ctx, skill.ID, f.codex)
	requireErrorDetails(t, err, library.SourceInvalid, library.ErrorDetails{Path: codexFolder})

	projectFolder := filepath.Join(f.directories[f.project], "pdf")
	if err := os.Symlink(filepath.Join(projectFolder, "SKILL.md"), filepath.Join(projectFolder, "link.md")); err != nil {
		t.Fatal(err)
	}
	_, err = lib.PlanUpdateFromScope(ctx, skill.ID, f.project)
	requireErrorDetails(t, err, library.FailedPrecondition, library.ErrorDetails{Path: projectFolder})
}
