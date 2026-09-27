package library_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tiaot33/skills-tui/internal/library"
)

func TestLocalDirectoryPreviewsCandidatesAndFetchesThemIntoOneCollection(t *testing.T) {
	lib := openTestLibrary(t)
	ctx := t.Context()
	root := t.TempDir()
	// 名字取自 SKILL.md 声明,与目录名无关。
	writeFile(t, filepath.Join(root, "skills", "alpha-dir", "SKILL.md"), []byte(skillDocument("alpha", "alpha body")), 0o644)
	writeFile(t, filepath.Join(root, "skills", "alpha-dir", "run.sh"), []byte("#!/bin/sh\n"), 0o755)
	writeFile(t, filepath.Join(root, "skills", "beta", "SKILL.md"), []byte(skillDocument("beta", "beta body")), 0o644)

	preview, err := lib.PreviewFetch(ctx, localRequest(root))
	if err != nil {
		t.Fatal(err)
	}
	want := []library.Candidate{{Name: "alpha", Path: "skills/alpha-dir"}, {Name: "beta", Path: "skills/beta"}}
	if preview.ID == "" || !reflect.DeepEqual(preview.Candidates, want) {
		t.Fatalf("local preview = %#v; want candidates %#v", preview, want)
	}
	if skills, err := lib.ListSkills(ctx); err != nil || len(skills) != 0 {
		t.Fatalf("preview wrote to library: %v, %v", skills, err)
	}
	fetched, err := getFetchedSkills(ctx, lib, preview, candidateNames(preview))
	if err != nil {
		t.Fatal(err)
	}
	if got := skillNames(fetched); !reflect.DeepEqual(got, []string{"alpha", "beta"}) {
		t.Fatalf("fetched names = %v", got)
	}
	wantFiles := []library.File{
		{Path: "SKILL.md", Content: []byte(skillDocument("alpha", "alpha body")), Mode: library.ModeRegular},
		{Path: "run.sh", Content: []byte("#!/bin/sh\n"), Mode: library.ModeExecutable},
	}
	if !reflect.DeepEqual(fetched[0].Files, wantFiles) {
		t.Fatalf("alpha files = %#v", fetched[0].Files)
	}
	collections, err := lib.ListCollections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(collections) != 1 || len(collections[0].Skills) != 2 {
		t.Fatalf("collections = %#v; want one collection for the previewed directory", collections)
	}
	source := collections[0].Source
	if source.Type != library.SourceLocal || source.Locator != (library.SourceLocator{Path: root}) || source.LastFetched.IsZero() {
		t.Fatalf("source = %#v; want the previewed directory with a read time", source)
	}
}

func TestLocalPreviewResolvesRelativePathAndListsAllCandidates(t *testing.T) {
	lib := openTestLibrary(t)
	ctx := t.Context()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "one", "SKILL.md"), []byte(skillDocument("one", "")), 0o644)
	writeFile(t, filepath.Join(root, "two", "SKILL.md"), []byte(skillDocument("two", "")), 0o644)
	t.Chdir(filepath.Dir(root))

	preview, err := lib.PreviewFetch(ctx, localRequest(filepath.Base(root)))
	if err != nil {
		t.Fatal(err)
	}
	// 本地目录总是列出全部候选,由入库时挑选。
	if want := []library.Candidate{{Name: "one", Path: "one"}, {Name: "two", Path: "two"}}; !reflect.DeepEqual(preview.Candidates, want) {
		t.Fatalf("candidates = %#v; want %#v", preview.Candidates, want)
	}
	fetched, err := lib.Fetch(ctx, preview.ID, []string{"two"})
	if err != nil {
		t.Fatal(err)
	}
	if len(fetched) != 1 || fetched[0].Name != "two" || fetched[0].Source.Locator.Path != root {
		t.Fatalf("source path = %q; want absolute %q", fetched[0].Source.Locator.Path, root)
	}
}

func TestLocalPreviewPinsContentUntilFetch(t *testing.T) {
	lib := openTestLibrary(t)
	ctx := t.Context()
	root := t.TempDir()
	skillFile := filepath.Join(root, "SKILL.md")
	writeFile(t, skillFile, []byte(skillDocument("pinned", "previewed")), 0o644)
	preview, err := lib.PreviewFetch(ctx, localRequest(root))
	if err != nil {
		t.Fatal(err)
	}
	// 预览之后改动或删除目录,入库的仍是预览时的内容。
	writeFile(t, skillFile, []byte(skillDocument("pinned", "edited later")), 0o644)
	writeFile(t, filepath.Join(root, "extra.md"), []byte("added later"), 0o644)
	fetched, err := getFetchedSkills(ctx, lib, preview, candidateNames(preview))
	if err != nil {
		t.Fatal(err)
	}
	want := []library.File{{Path: "SKILL.md", Content: []byte(skillDocument("pinned", "previewed")), Mode: library.ModeRegular}}
	if !reflect.DeepEqual(fetched[0].Files, want) {
		t.Fatalf("fetched files = %#v; want preview-time snapshot", fetched[0].Files)
	}
}

func TestLocalUpdateRelocatesSkillByNameInPreviewedDirectory(t *testing.T) {
	lib := openTestLibrary(t)
	ctx := t.Context()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a", "SKILL.md"), []byte(skillDocument("first", "v1")), 0o644)
	writeFile(t, filepath.Join(root, "b", "SKILL.md"), []byte(skillDocument("second", "v1")), 0o644)
	preview, err := lib.PreviewFetch(ctx, localRequest(root))
	if err != nil {
		t.Fatal(err)
	}
	fetched, err := lib.Fetch(ctx, preview.ID, candidateNames(preview))
	if err != nil {
		t.Fatal(err)
	}
	first, second := fetched[0], fetched[1]

	// 技能搬到另一个目录也按名字找回;另一个技能不变。
	if err := os.RemoveAll(filepath.Join(root, "a")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "moved", "SKILL.md"), []byte(skillDocument("first", "v2")), 0o644)
	checks, err := lib.CheckUpdates(ctx, []library.SkillID{first.ID, second.ID})
	if err != nil {
		t.Fatal(err)
	}
	want := []library.UpdateCheck{{SkillID: first.ID, Available: true}, {SkillID: second.ID}}
	if !reflect.DeepEqual(checks, want) {
		t.Fatalf("CheckUpdates = %#v; want %#v", checks, want)
	}
	updated, err := lib.Update(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.Changed || string(updated.Skill.Files[0].Content) != skillDocument("first", "v2") {
		t.Fatalf("Update = %#v; want v2 from relocated directory", updated)
	}
	if updated.Skill.Source.LastFetched.IsZero() {
		t.Fatalf("Update did not record read time on local source: %#v", updated.Skill.Source)
	}

	// 名字从目录里消失时,更新报 NotFound。
	if err := os.RemoveAll(filepath.Join(root, "b")); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Update(ctx, second.ID); !library.IsCode(err, library.NotFound) {
		t.Fatalf("Update of vanished skill = %v; want NotFound", err)
	}
}

func TestLocalPreviewRejectsInvalidDirectories(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-dir")
	writeFile(t, file, []byte("x"), 0o644)
	withoutCandidates := t.TempDir()
	writeFile(t, filepath.Join(withoutCandidates, "SKILL.md"), []byte("# no frontmatter\n"), 0o644)
	tooMany := t.TempDir()
	writeFile(t, filepath.Join(tooMany, "SKILL.md"), []byte(skillDocument("big", "")), 0o644)
	for i := range 1000 {
		writeFile(t, filepath.Join(tooMany, "files", fmt.Sprintf("%04d.txt", i)), []byte("x"), 0o644)
	}
	tooLarge := t.TempDir()
	writeFile(t, filepath.Join(tooLarge, "SKILL.md"), []byte(skillDocument("large", "")), 0o644)
	// 稀疏文件:只声明大小不占磁盘,预览按文件大小在读取前拒绝。
	large, err := os.Create(filepath.Join(tooLarge, "large.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(large.Truncate(64<<20), large.Close()); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		spec library.PreviewFetchRequest
		want library.ErrorCode
	}{
		{name: "empty path", spec: localRequest(""), want: library.InvalidArgument},
		{name: "file path", spec: localRequest(file), want: library.InvalidArgument},
		{name: "missing path", spec: localRequest(filepath.Join(t.TempDir(), "missing")), want: library.SourceUnreachable},
		{name: "no valid candidates", spec: localRequest(withoutCandidates), want: library.SourceInvalid},
		{name: "too many files", spec: localRequest(tooMany), want: library.SourceInvalid},
		{name: "too many bytes", spec: localRequest(tooLarge), want: library.SourceInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lib := openTestLibrary(t)
			if _, err := lib.PreviewFetch(t.Context(), test.spec); !library.IsCode(err, test.want) {
				t.Fatalf("PreviewFetch = %v; want %s", err, test.want)
			}
		})
	}
}
