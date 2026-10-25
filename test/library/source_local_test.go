package library_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tiaot33/mechbox/internal/library"
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
	fetched, err := lib.Fetch(ctx, preview.ID, candidateNames(preview))
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
	if !reflect.DeepEqual(skillFiles(t, lib, fetched[0]), wantFiles) {
		t.Fatalf("alpha files = %#v", skillFiles(t, lib, fetched[0]))
	}
	collections, err := lib.ListCollections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(collections) != 1 || !reflect.DeepEqual(collections[0].Skills, skillIDs(fetched)) {
		t.Fatalf("collections = %#v; want one collection for the previewed directory", collections)
	}
	source := collections[0].Source
	if source.Type != library.SourceLocal || source.Label != root || source.Ref != "" || source.ViewURL != "" {
		t.Fatalf("source = %#v; want the previewed directory", source)
	}
}

// 选的是一个技能文件夹时,来源是它的上一级目录:入库预览只列这个技能,先后从同一个目录入库的技能进同一个集合。
func TestLocalSkillFolderJoinsTheCollectionOfItsParentDirectory(t *testing.T) {
	lib := openTestLibrary(t)
	ctx := t.Context()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "alpha-dir", "SKILL.md"), []byte(skillDocument("alpha", "alpha body")), 0o644)
	writeFile(t, filepath.Join(root, "beta", "SKILL.md"), []byte(skillDocument("beta", "beta body")), 0o644)
	writeFile(t, filepath.Join(root, "broken", "SKILL.md"), []byte("# no frontmatter\n"), 0o644)

	preview, err := lib.PreviewFetch(ctx, localRequest(filepath.Join(root, "alpha-dir")))
	if err != nil {
		t.Fatal(err)
	}
	if want := []library.Candidate{{Name: "alpha", Path: "alpha-dir"}}; !reflect.DeepEqual(preview.Candidates, want) || len(preview.Diagnostics) != 0 {
		t.Fatalf("preview = %#v; want only %#v without the siblings' diagnostics", preview, want)
	}
	if preview.Source.Label != root {
		t.Fatalf("source label = %q; want the parent directory %q", preview.Source.Label, root)
	}
	alpha := fetchSkills(t, lib, preview, []string{"alpha"})[0]
	beta, err := fetchLocalDir(ctx, lib, filepath.Join(root, "beta"))
	if err != nil {
		t.Fatal(err)
	}
	if beta.CollectionID != alpha.CollectionID {
		t.Fatalf("collections = %d and %d; want one collection for the directory", alpha.CollectionID, beta.CollectionID)
	}
	again, err := lib.PreviewFetch(ctx, library.PreviewFetchRequest{Collection: alpha.CollectionID})
	if err != nil {
		t.Fatal(err)
	}
	if want := []library.Candidate{{Fetched: alpha.ID, Name: "alpha", Path: "alpha-dir"}, {Fetched: beta.ID, Name: "beta", Path: "beta"}}; !reflect.DeepEqual(again.Candidates, want) {
		t.Fatalf("collection preview = %#v; want %#v", again.Candidates, want)
	}
}

// 上一级目录列不出这个技能文件夹时,来源就是这个文件夹本身。
func TestLocalSkillFolderIsItsOwnSourceWhenTheParentCannotListIt(t *testing.T) {
	skillInside := t.TempDir()
	writeFile(t, filepath.Join(skillInside, "SKILL.md"), []byte(skillDocument("outer", "")), 0o644)
	writeFile(t, filepath.Join(skillInside, "inner", "SKILL.md"), []byte(skillDocument("inner", "")), 0o644)
	sameName := t.TempDir()
	writeFile(t, filepath.Join(sameName, "a", "SKILL.md"), []byte(skillDocument("twin", "first")), 0o644)
	writeFile(t, filepath.Join(sameName, "b", "SKILL.md"), []byte(skillDocument("twin", "second")), 0o644)
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeFile(t, filepath.Join(home, "mine", "SKILL.md"), []byte(skillDocument("mine", "")), 0o644)
	for name, folder := range map[string]string{
		"parent is a skill":      filepath.Join(skillInside, "inner"),
		"same name listed first": filepath.Join(sameName, "b"),
		"parent is home":         filepath.Join(home, "mine"),
	} {
		t.Run(name, func(t *testing.T) {
			preview, err := openTestLibrary(t).PreviewFetch(t.Context(), localRequest(folder))
			if err != nil {
				t.Fatal(err)
			}
			if len(preview.Candidates) != 1 || preview.Candidates[0].Path != "." || preview.Source.Label != folder {
				t.Fatalf("preview = %#v; want the folder itself as the source", preview)
			}
		})
	}
}

func TestLocalSkillLeavesOutSystemAndCacheFiles(t *testing.T) {
	lib := openTestLibrary(t)
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "alpha", "SKILL.md"), []byte(skillDocument("alpha", "alpha body")), 0o644)
	writeFile(t, filepath.Join(root, "alpha", "scripts", "run.py"), []byte("print(1)\n"), 0o644)
	for _, junk := range []string{".DS_Store", "._SKILL.md", "Thumbs.db", "desktop.ini", "scripts/.DS_Store", "scripts/__pycache__/run.cpython-312.pyc", "__pypackages__/3.12/lib/dep.py", ".git/HEAD"} {
		writeFile(t, filepath.Join(root, "alpha", filepath.FromSlash(junk)), []byte("junk"), 0o644)
	}

	preview, err := lib.PreviewFetch(t.Context(), localRequest(root))
	if err != nil {
		t.Fatal(err)
	}
	fetched := fetchSkills(t, lib, preview, []string{"alpha"})
	want := []library.File{
		{Path: "SKILL.md", Content: []byte(skillDocument("alpha", "alpha body")), Mode: library.ModeRegular},
		{Path: "scripts/run.py", Content: []byte("print(1)\n"), Mode: library.ModeRegular},
	}
	if got := skillFiles(t, lib, fetched[0]); !reflect.DeepEqual(got, want) {
		t.Fatalf("files = %#v; want %#v", got, want)
	}
}

func TestLocalPreviewResolvesRelativePathAndListsAllCandidates(t *testing.T) {
	lib := openTestLibrary(t)
	ctx := t.Context()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "one", "SKILL.md"), []byte(skillDocument("one", "")), 0o644)
	writeFile(t, filepath.Join(root, "two", "SKILL.md"), []byte(skillDocument("two", "")), 0o644)
	t.Chdir(filepath.Dir(root))

	preview, err := lib.PreviewFetch(ctx, localRequest("./"+filepath.Base(root)))
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
	if len(fetched) != 1 || fetched[0].Name != "two" || fetched[0].Source.Label != root {
		t.Fatalf("source label = %q; want absolute %q", fetched[0].Source.Label, root)
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
	// 入库预览之后改动或删除目录,入库的仍是入库预览时的内容。
	writeFile(t, skillFile, []byte(skillDocument("pinned", "edited later")), 0o644)
	writeFile(t, filepath.Join(root, "extra.md"), []byte("added later"), 0o644)
	fetched, err := lib.Fetch(ctx, preview.ID, candidateNames(preview))
	if err != nil {
		t.Fatal(err)
	}
	want := []library.File{{Path: "SKILL.md", Content: []byte(skillDocument("pinned", "previewed")), Mode: library.ModeRegular}}
	if !reflect.DeepEqual(skillFiles(t, lib, fetched[0]), want) {
		t.Fatalf("fetched files = %#v; want preview-time snapshot", skillFiles(t, lib, fetched[0]))
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
	checks, err := lib.CheckUpdates(ctx, []library.CollectionID{first.CollectionID})
	if err != nil || len(checks) != 1 || checks[0].Error != nil || len(checks[0].Skills) != 2 {
		t.Fatalf("CheckUpdates = %#v, %v", checks, err)
	}
	if got := []library.UpdateCheckStatus{checks[0].Skills[0].LastUpdateCheck.Status, checks[0].Skills[1].LastUpdateCheck.Status}; !reflect.DeepEqual(got, []library.UpdateCheckStatus{library.UpdateAvailable, library.UpdateCurrent}) {
		t.Fatalf("CheckUpdates statuses = %v; want available, current", got)
	}
	updated, err := updateSkill(t, lib, ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.Changed || string(skillFiles(t, lib, updated.Skill)[0].Content) != skillDocument("first", "v2") {
		t.Fatalf("Update = %#v; want v2 from relocated directory", updated)
	}
	if updated.Skill.UpdatedAt.IsZero() || updated.Skill.Revision != "" {
		t.Fatalf("updated skill = %#v; want an update time and no revision for a local source", updated.Skill)
	}

	// 名字从目录里消失时,检查记为来源里已删除,更新报 NotFound。
	if err := os.RemoveAll(filepath.Join(root, "b")); err != nil {
		t.Fatal(err)
	}
	if status, err := checkSkillUpdate(t, lib, ctx, second.ID); err != nil || status != library.UpdateDeleted {
		t.Fatalf("check of vanished skill = %v, %v; want deleted", status, err)
	}
	_, err = lib.Update(ctx, mustPlanUpdate(t, lib, second.ID))
	requireErrorDetails(t, err, library.NotFound, library.ErrorDetails{SkillID: second.ID, Name: second.Name})
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
	// 稀疏文件:只声明大小不占磁盘,入库预览按文件大小在读取前拒绝。
	large, err := os.Create(filepath.Join(tooLarge, "large.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(large.Truncate(64<<20), large.Close()); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		spec  library.PreviewFetchRequest
		want  library.ErrorCode
		field string
	}{
		{name: "empty path", spec: localRequest(""), want: library.InvalidArgument},
		{name: "file path", spec: localRequest(file), want: library.InvalidArgument, field: "input"},
		{name: "missing path", spec: localRequest(filepath.Join(t.TempDir(), "missing")), want: library.SourceUnreachable},
		{name: "no valid candidates", spec: localRequest(withoutCandidates), want: library.SourceInvalid},
		{name: "too many files", spec: localRequest(tooMany), want: library.SourceInvalid},
		{name: "too many bytes", spec: localRequest(tooLarge), want: library.SourceInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lib := openTestLibrary(t)
			_, err := lib.PreviewFetch(t.Context(), test.spec)
			if !library.IsCode(err, test.want) {
				t.Fatalf("PreviewFetch = %v; want %s", err, test.want)
			}
			if got := library.ErrorOf(err).Details.Field; got != test.field {
				t.Fatalf("PreviewFetch field = %q; want %q", got, test.field)
			}
		})
	}
}
