package library_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/tiaot33/skills-tui/internal/library"
)

func TestSkillFilesMatchFullSkill(t *testing.T) {
	lib := openTestLibrary(t)
	source := filepath.Join(t.TempDir(), "files")
	writeLocalSkill(t, source, "# 中文\n")
	writeFile(t, filepath.Join(source, "assets", "data.bin"), []byte{0, 255, 1}, 0o644)
	writeFile(t, filepath.Join(source, "empty.txt"), []byte{}, 0o644)
	writeFile(t, filepath.Join(source, "scripts", "run.sh"), []byte("#!/bin/sh\n"), 0o755)
	fetched, err := fetchLocalDir(t.Context(), lib, source)
	if err != nil {
		t.Fatal(err)
	}
	requireFilesMatchGetSkill(t, lib, fetched.ID)
}

func TestSkillFilesReadCurrentVersion(t *testing.T) {
	ctx := t.Context()
	lib := openTestLibrary(t)
	source := filepath.Join(t.TempDir(), "changing")
	writeLocalSkill(t, source, "old")
	writeFile(t, filepath.Join(source, "removed.txt"), []byte("removed"), 0o644)
	skill, err := fetchLocalDir(ctx, lib, source)
	if err != nil {
		t.Fatal(err)
	}
	writeLocalSkill(t, source, "新版正文")
	writeFile(t, filepath.Join(source, "new.sh"), []byte("new"), 0o755)
	if err := os.Remove(filepath.Join(source, "removed.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Update(ctx, skill.ID); err != nil {
		t.Fatal(err)
	}
	want := []library.FileMetadata{
		{Path: "SKILL.md", Size: int64(len(skillDocument("changing", "新版正文"))), Mode: library.ModeRegular},
		{Path: "new.sh", Size: 3, Mode: library.ModeExecutable},
	}
	got, err := lib.ListSkillFiles(ctx, skill.ID)
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("ListSkillFiles after Update = %#v, %v; want %#v", got, err, want)
	}
	requireFilesMatchGetSkill(t, lib, skill.ID)
	_, err = lib.GetSkillFile(ctx, skill.ID, "removed.txt")
	requireErrorDetails(t, err, library.NotFound, library.ErrorDetails{Path: "removed.txt"})
}

func TestSkillFilesReportMissingAndCanceled(t *testing.T) {
	ctx := t.Context()
	lib := openTestLibrary(t)
	skill := fetchNamedSkill(t, lib, "present")
	// 路径按原样精确匹配,不做归一化。
	for _, path := range []string{"missing.txt", "", "../SKILL.md", "./SKILL.md", "/SKILL.md"} {
		_, err := lib.GetSkillFile(ctx, skill.ID, path)
		requireErrorDetails(t, err, library.NotFound, library.ErrorDetails{Path: path})
	}
	_, err := lib.ListSkillFiles(ctx, 99999)
	requireErrorDetails(t, err, library.NotFound, library.ErrorDetails{SkillID: 99999})
	_, err = lib.GetSkillFile(ctx, 99999, "SKILL.md")
	requireErrorDetails(t, err, library.NotFound, library.ErrorDetails{SkillID: 99999})
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := lib.ListSkillFiles(canceled, skill.ID); !library.IsCode(err, library.Canceled) {
		t.Fatalf("canceled list error = %v", err)
	}
	if _, err := lib.GetSkillFile(canceled, skill.ID, "SKILL.md"); !library.IsCode(err, library.Canceled) {
		t.Fatalf("canceled read error = %v", err)
	}
}

func TestSkillFilesOfEmptySkill(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "library.db")
	lib := openLibraryAt(t, path)
	source := filepath.Join(t.TempDir(), "empty")
	writeLocalSkill(t, source, "")
	empty, err := fetchLocalDir(ctx, lib, source)
	if err != nil {
		t.Fatal(err)
	}
	// 入库的候选总带 SKILL.md;直接清空版本的文件清单,构造一个没有文件的当前版本。
	if err := lib.Close(); err != nil {
		t.Fatal(err)
	}
	editLibraryFixture(t, path, `DELETE FROM version_files`)
	lib = openLibraryAt(t, path)
	files, err := lib.ListSkillFiles(ctx, empty.ID)
	if err != nil || len(files) != 0 {
		t.Fatalf("empty skill files = %#v, %v", files, err)
	}
	_, err = lib.GetSkillFile(ctx, empty.ID, "SKILL.md")
	requireErrorDetails(t, err, library.NotFound, library.ErrorDetails{Path: "SKILL.md"})
}

// requireFilesMatchGetSkill 核对两个按需读取方法与 GetSkill 的完整文件一致:
// 元数据的路径、Mode 相同且大小等于内容字节数,单文件读取与同路径文件完全相同。
func requireFilesMatchGetSkill(t *testing.T, lib *library.Library, id library.SkillID) {
	t.Helper()
	full, err := lib.GetSkill(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	var want []library.FileMetadata
	for _, file := range full.Files {
		want = append(want, library.FileMetadata{Path: file.Path, Size: int64(len(file.Content)), Mode: file.Mode})
		got, err := lib.GetSkillFile(t.Context(), id, file.Path)
		if err != nil || !reflect.DeepEqual(got, file) {
			t.Fatalf("GetSkillFile(%q) = %#v, %v; want %#v", file.Path, got, err, file)
		}
	}
	files, err := lib.ListSkillFiles(t.Context(), id)
	if err != nil || !slices.Equal(files, want) {
		t.Fatalf("ListSkillFiles = %#v, %v; want %#v", files, err, want)
	}
}
