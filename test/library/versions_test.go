package library_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/tiaot33/skills-tui/internal/library"
)

// 更新保留旧版本:版本列表与按 ID 读取返回同一个值,历史版本的文件按版本 ID 原样可读,
// 差异按 from → to 的方向归类,反过来比较时新增与删除互换。
func TestUpdatePreservesHistoryAndDiffsVersions(t *testing.T) {
	ctx := t.Context()
	source := filepath.Join(t.TempDir(), "example-skill")
	writeLocalSkill(t, source, "first\n")
	writeFile(t, filepath.Join(source, "unchanged.md"), []byte("same\n"), 0o644)
	writeFile(t, filepath.Join(source, "deleted.md"), []byte("gone\n"), 0o644)

	lib := openTestLibrary(t)
	fetched, err := fetchLocalDir(ctx, lib, source)
	if err != nil {
		t.Fatal(err)
	}
	writeLocalSkill(t, source, "second\n")
	writeFile(t, filepath.Join(source, "added.md"), []byte("new\n"), 0o644)
	if err := os.Remove(filepath.Join(source, "deleted.md")); err != nil {
		t.Fatal(err)
	}
	updated, err := updateSkill(t, lib, ctx, fetched.ID)
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
	if len(versions) != 2 || versions[0].ID != fetched.VersionID || versions[1].ID != updated.Skill.VersionID {
		t.Fatalf("ListVersions() = %#v, want the fetched version then the updated one", versions)
	}
	for _, version := range versions {
		got, err := lib.GetVersion(ctx, version.ID)
		if err != nil || !reflect.DeepEqual(got, version) {
			t.Fatalf("GetVersion(%d) = %#v, %v; want %#v", version.ID, got, err, version)
		}
		if version.SkillID != fetched.ID {
			t.Fatalf("version SkillID = %d, want %d", version.SkillID, fetched.ID)
		}
	}
	requireVersionFiles(t, lib, versions[0].ID, []library.File{
		{Path: "SKILL.md", Content: []byte(skillDocument("example-skill", "first\n")), Mode: library.ModeRegular},
		{Path: "deleted.md", Content: []byte("gone\n"), Mode: library.ModeRegular},
		{Path: "unchanged.md", Content: []byte("same\n"), Mode: library.ModeRegular},
	})
	requireVersionFiles(t, lib, versions[1].ID, []library.File{
		{Path: "SKILL.md", Content: []byte(skillDocument("example-skill", "second\n")), Mode: library.ModeRegular},
		{Path: "added.md", Content: []byte("new\n"), Mode: library.ModeRegular},
		{Path: "unchanged.md", Content: []byte("same\n"), Mode: library.ModeRegular},
	})

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
	reverse, err := lib.DiffVersions(ctx, versions[1].ID, versions[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	wantReverse := library.VersionDiff{
		Changed: []string{"SKILL.md"},
		Added:   []string{"deleted.md"},
		Deleted: []string{"added.md"},
	}
	if !reflect.DeepEqual(reverse, wantReverse) {
		t.Fatalf("reverse DiffVersions() = %#v, want %#v", reverse, wantReverse)
	}
}

// 同一个版本比较得到空差异,三类都是空数组,经 JSON 读取时不是 null。
func TestDiffSameVersionIsEmpty(t *testing.T) {
	lib := openTestLibrary(t)
	skill := fetchNamedSkill(t, lib, "same")
	diff, err := lib.DiffVersions(t.Context(), skill.VersionID, skill.VersionID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(diff)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"changed":[],"added":[],"deleted":[]}` {
		t.Fatalf("DiffVersions() JSON = %s, want three empty arrays", data)
	}
}

// 只改了可执行权限也产生新版本,差异把该文件列为改动,界面能看出这次为什么有新版本。
func TestExecutableOnlyChangeShowsInDiff(t *testing.T) {
	ctx := t.Context()
	source := filepath.Join(t.TempDir(), "script-skill")
	writeLocalSkill(t, source, "body\n")
	script := filepath.Join(source, "run.sh")
	writeFile(t, script, []byte("echo hi\n"), 0o644)
	lib := openTestLibrary(t)
	fetched, err := fetchLocalDir(ctx, lib, source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	updated, err := updateSkill(t, lib, ctx, fetched.ID)
	if err != nil || !updated.Changed || updated.Skill.VersionID == fetched.VersionID {
		t.Fatalf("Update = %#v, %v; want a new version", updated, err)
	}
	diff, err := lib.DiffVersions(ctx, fetched.VersionID, updated.Skill.VersionID)
	if err != nil {
		t.Fatal(err)
	}
	want := library.VersionDiff{Changed: []string{"run.sh"}, Added: []string{}, Deleted: []string{}}
	if !reflect.DeepEqual(diff, want) {
		t.Fatalf("DiffVersions() = %#v, want %#v", diff, want)
	}
	file, err := lib.GetVersionFile(ctx, updated.Skill.VersionID, "run.sh")
	if err != nil || file.Mode != library.ModeExecutable {
		t.Fatalf("GetVersionFile(run.sh) = %#v, %v; want executable", file, err)
	}
}

func TestUpdateChangesDescriptionOnNewVersionAndKeepsFetchedAt(t *testing.T) {
	ctx := t.Context()
	source := filepath.Join(t.TempDir(), "timed")
	writeFile(t, filepath.Join(source, "SKILL.md"), []byte("---\nname: timed\ndescription: first edition\n---\nfirst\n"), 0o644)
	lib := openTestLibrary(t)
	fetched, err := fetchLocalDir(ctx, lib, source)
	if err != nil {
		t.Fatal(err)
	}
	fetchedAt := fetched.FetchedAt
	writeFile(t, filepath.Join(source, "SKILL.md"), []byte("---\nname: timed\ndescription: second edition\n---\nsecond\n"), 0o644)
	time.Sleep(time.Millisecond)
	updated, err := updateSkill(t, lib, ctx, fetched.ID)
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
	if !current.FetchedAt.Equal(fetchedAt) || current.UpdatedAt.Equal(fetchedAt) || current.UpdatedAt.Before(fetchedAt) {
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

// 比较差异时技能被并发移除:每次比较要么得到完整正确的差异,要么返回 NotFound,不会把真实的改动报成空差异。
func TestDiffVersionsWhileSkillIsRemoved(t *testing.T) {
	ctx := t.Context()
	lib := openTestLibrary(t)
	want := library.VersionDiff{Changed: []string{"SKILL.md"}, Added: []string{}, Deleted: []string{}}
	for i := range 20 {
		source := filepath.Join(t.TempDir(), fmt.Sprintf("racing-%d", i))
		writeLocalSkill(t, source, "first\n")
		fetched, err := fetchLocalDir(ctx, lib, source)
		if err != nil {
			t.Fatal(err)
		}
		writeLocalSkill(t, source, "second\n")
		updated, err := updateSkill(t, lib, ctx, fetched.ID)
		if err != nil || !updated.Changed {
			t.Fatalf("Update = %#v, %v", updated, err)
		}

		var wg sync.WaitGroup
		var diffErr error
		started := make(chan struct{})
		wg.Go(func() {
			close(started)
			for {
				diff, err := lib.DiffVersions(ctx, fetched.VersionID, updated.Skill.VersionID)
				if err != nil {
					diffErr = err
					return
				}
				if !reflect.DeepEqual(diff, want) {
					diffErr = fmt.Errorf("DiffVersions() = %#v, want %#v", diff, want)
					return
				}
			}
		})
		<-started
		if err := lib.RemoveSkill(ctx, fetched.ID, library.RemoveSkillOptions{Folders: library.Keep}); err != nil {
			t.Fatal(err)
		}
		wg.Wait()
		if !library.IsCode(diffErr, library.NotFound) {
			t.Fatalf("DiffVersions while removing = %v, want the full diff until NotFound", diffErr)
		}
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

// Skill.VersionID 是当前版本的 SkillVersion.ID:还没有回滚时,入库、Update 与 GetSkill 都与 ListVersions 的最后一项一致,
// 无变化的 Update 不换版本。
func TestSkillReportsCurrentVersionID(t *testing.T) {
	ctx := t.Context()
	lib := openTestLibrary(t)
	dir := filepath.Join(t.TempDir(), "demo")
	writeLocalSkill(t, dir, "v1\n")
	fetched, err := fetchLocalDir(ctx, lib, dir)
	if err != nil {
		t.Fatal(err)
	}
	versions, err := lib.ListVersions(ctx, fetched.ID)
	if err != nil || len(versions) != 1 || fetched.VersionID != versions[0].ID {
		t.Fatalf("fetched VersionID = %d, ListVersions = %#v, %v", fetched.VersionID, versions, err)
	}

	writeLocalSkill(t, dir, "v2\n")
	updated, err := updateSkill(t, lib, ctx, fetched.ID)
	if err != nil || !updated.Changed {
		t.Fatalf("Update = %#v, %v", updated, err)
	}
	versions, err = lib.ListVersions(ctx, fetched.ID)
	if err != nil || len(versions) != 2 || updated.Skill.VersionID != versions[1].ID {
		t.Fatalf("updated VersionID = %d, ListVersions = %#v, %v", updated.Skill.VersionID, versions, err)
	}
	current, err := lib.GetSkill(ctx, fetched.ID)
	if err != nil || current.VersionID != versions[1].ID {
		t.Fatalf("GetSkill VersionID = %d, %v; want %d", current.VersionID, err, versions[1].ID)
	}
	unchanged, err := updateSkill(t, lib, ctx, fetched.ID)
	if err != nil || unchanged.Changed || unchanged.Skill.VersionID != versions[1].ID {
		t.Fatalf("unchanged Update = %#v, %v", unchanged, err)
	}
}

func TestVersionFilesReportMissingAndCanceled(t *testing.T) {
	ctx := t.Context()
	lib := openTestLibrary(t)
	skill := fetchNamedSkill(t, lib, "present")
	// 路径按原样精确匹配,不做归一化。
	for _, path := range []string{"missing.txt", "", "../SKILL.md", "./SKILL.md", "/SKILL.md"} {
		_, err := lib.GetVersionFile(ctx, skill.VersionID, path)
		requireErrorDetails(t, err, library.NotFound, library.ErrorDetails{Path: path})
	}
	_, err := lib.ListVersionFiles(ctx, 99999)
	requireErrorDetails(t, err, library.NotFound, library.ErrorDetails{})
	_, err = lib.GetVersionFile(ctx, 99999, "SKILL.md")
	requireErrorDetails(t, err, library.NotFound, library.ErrorDetails{})
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := lib.ListVersionFiles(canceled, skill.VersionID); !library.IsCode(err, library.Canceled) {
		t.Fatalf("canceled list error = %v", err)
	}
	if _, err := lib.GetVersionFile(canceled, skill.VersionID, "SKILL.md"); !library.IsCode(err, library.Canceled) {
		t.Fatalf("canceled read error = %v", err)
	}
}

// requireVersionFiles 核对版本的文件列表与逐个读取:元数据按路径排序、大小等于内容字节数,单文件读取与 want 完全相同。
func requireVersionFiles(t *testing.T, lib *library.Library, id library.VersionID, want []library.File) {
	t.Helper()
	var wantMetadata []library.FileMetadata
	for _, file := range want {
		wantMetadata = append(wantMetadata, library.FileMetadata{Path: file.Path, Size: int64(len(file.Content)), Mode: file.Mode})
		got, err := lib.GetVersionFile(t.Context(), id, file.Path)
		if err != nil || !reflect.DeepEqual(got, file) {
			t.Fatalf("GetVersionFile(%q) = %#v, %v; want %#v", file.Path, got, err, file)
		}
	}
	metadata, err := lib.ListVersionFiles(t.Context(), id)
	if err != nil || !slices.Equal(metadata, wantMetadata) {
		t.Fatalf("ListVersionFiles = %#v, %v; want %#v", metadata, err, wantMetadata)
	}
}
