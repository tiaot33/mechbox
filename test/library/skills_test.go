package library_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/tiaot33/skills-tui/internal/library"
)

func TestFetchLocalSkillAndReadItBackIntact(t *testing.T) {
	source := filepath.Join(t.TempDir(), "example-skill")
	writeLocalSkill(t, source, "# Example\n")
	writeFile(t, filepath.Join(source, "references", "guide.md"), []byte("nested\n"), 0o644)
	writeFile(t, filepath.Join(source, "scripts", "run.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755)
	writeFile(t, filepath.Join(source, "assets", "data.bin"), []byte{0x00, 0xff, 0x01}, 0o644)

	lib := openTestLibrary(t)

	fetched, err := fetchLocalDir(t.Context(), lib, source)
	if err != nil {
		t.Fatal(err)
	}
	got, err := lib.GetSkill(t.Context(), fetched.ID)
	if err != nil {
		t.Fatal(err)
	}

	want := library.Skill{
		ID:           fetched.ID,
		Name:         "example-skill",
		CollectionID: fetched.CollectionID,
		VersionID:    fetched.VersionID,
		FetchedAt:    fetched.FetchedAt,
		Source: library.Source{
			Type:  library.SourceLocal,
			Label: source,
		},
	}
	if !reflect.DeepEqual(got, want) || got.FetchedAt.IsZero() {
		t.Fatalf("GetSkill() = %#v, want %#v", got, want)
	}
	if !reflect.DeepEqual(fetched, got) {
		t.Fatalf("fetchLocalDir() = %#v, GetSkill() = %#v", fetched, got)
	}
	requireVersionFiles(t, lib, got.VersionID, []library.File{
		{Path: "SKILL.md", Content: []byte(skillDocument("example-skill", "# Example\n")), Mode: library.ModeRegular},
		{Path: "assets/data.bin", Content: []byte{0x00, 0xff, 0x01}, Mode: library.ModeRegular},
		{Path: "references/guide.md", Content: []byte("nested\n"), Mode: library.ModeRegular},
		{Path: "scripts/run.sh", Content: []byte("#!/bin/sh\nexit 0\n"), Mode: library.ModeExecutable},
	})
}

func TestFetchRecordsDescriptionAndVersionTimes(t *testing.T) {
	ctx := t.Context()
	lib := openTestLibrary(t)
	withDesc := filepath.Join(t.TempDir(), "described")
	writeFile(t, filepath.Join(withDesc, "SKILL.md"), []byte("---\nname: described\ndescription: a useful helper\n---\n# Described\n"), 0o644)
	start := time.Now().UTC()
	fetched, err := fetchLocalDir(ctx, lib, withDesc)
	if err != nil {
		t.Fatal(err)
	}
	end := time.Now().UTC()
	listed, err := lib.ListSkills(ctx)
	if err != nil || len(listed) != 1 || listed[0].Description != "a useful helper" {
		t.Fatalf("ListSkills description = %#v, %v", listed, err)
	}
	if fetched.Description != "a useful helper" {
		t.Fatalf("Fetch Description = %q, want a useful helper", fetched.Description)
	}
	versions, err := lib.ListVersions(ctx, fetched.ID)
	if err != nil || len(versions) != 1 {
		t.Fatalf("ListVersions = %#v, %v", versions, err)
	}
	if !fetched.FetchedAt.Equal(versions[0].CreatedAt) || !fetched.UpdatedAt.IsZero() {
		t.Fatalf("times = fetched %v updated %v version %v, want fetch time of the first version and no update time", fetched.FetchedAt, fetched.UpdatedAt, versions[0].CreatedAt)
	}
	if fetched.FetchedAt.Before(start) || fetched.FetchedAt.After(end) {
		t.Fatalf("FetchedAt = %v, want in [%v, %v]", fetched.FetchedAt, start, end)
	}

	plain := filepath.Join(t.TempDir(), "plain")
	writeLocalSkill(t, plain, "# Plain\n")
	undocumented, err := fetchLocalDir(ctx, lib, plain)
	if err != nil || undocumented.Description != "" {
		t.Fatalf("Fetch without description = %#v, %v; want empty", undocumented, err)
	}
}

// 列表与详情给出的是同一个技能,不带文件;详情也带最近一次更新检查。
func TestListsAndDetailReturnTheSameSkill(t *testing.T) {
	ctx := t.Context()
	lib := openTestLibrary(t)
	skill := fetchExampleSkill(t, lib, "example")
	if _, err := lib.CheckUpdates(ctx, []library.CollectionID{skill.CollectionID}); err != nil {
		t.Fatal(err)
	}
	detail, err := lib.GetSkill(ctx, skill.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.LastUpdateCheck == nil || detail.LastUpdateCheck.Status != library.UpdateCurrent {
		t.Fatalf("GetSkill LastUpdateCheck = %#v, want current", detail.LastUpdateCheck)
	}
	data, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["files"]; ok {
		t.Fatalf("skill exposes files: %s", data)
	}
	listed, err := lib.ListSkills(ctx)
	if err != nil || !reflect.DeepEqual(listed, []library.Skill{detail}) {
		t.Fatalf("ListSkills = %#v, %v; want %#v", listed, err, detail)
	}
}

// 更新时间只在更新带来新版本后才有;读文件按版本 ID,技能更新后旧版本的文件仍按原样可读。
func TestUpdateTimeAndVersionFilesAcrossUpdate(t *testing.T) {
	ctx := t.Context()
	lib := openTestLibrary(t)
	source := filepath.Join(t.TempDir(), "changing")
	writeLocalSkill(t, source, "old")
	writeFile(t, filepath.Join(source, "removed.txt"), []byte("removed"), 0o644)
	fetched, err := fetchLocalDir(ctx, lib, source)
	if err != nil {
		t.Fatal(err)
	}
	unchanged, err := updateSkill(t, lib, ctx, fetched.ID)
	if err != nil || unchanged.Changed || !unchanged.Skill.UpdatedAt.IsZero() || unchanged.Skill.VersionID != fetched.VersionID {
		t.Fatalf("Update without changes = %#v, %v; want no update time and the same version", unchanged, err)
	}

	writeLocalSkill(t, source, "新版正文")
	writeFile(t, filepath.Join(source, "new.sh"), []byte("new"), 0o755)
	if err := os.Remove(filepath.Join(source, "removed.txt")); err != nil {
		t.Fatal(err)
	}
	updated, err := updateSkill(t, lib, ctx, fetched.ID)
	if err != nil || !updated.Changed {
		t.Fatalf("Update = %#v, %v", updated, err)
	}
	versions, err := lib.ListVersions(ctx, fetched.ID)
	if err != nil || len(versions) != 2 {
		t.Fatalf("ListVersions = %#v, %v", versions, err)
	}
	detail, err := lib.GetSkill(ctx, fetched.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(detail, updated.Skill) || detail.VersionID != versions[1].ID || !detail.UpdatedAt.Equal(versions[1].CreatedAt) || !detail.FetchedAt.Equal(fetched.FetchedAt) {
		t.Fatalf("after Update GetSkill = %#v, Update = %#v; want the new version, its time as UpdatedAt and the original FetchedAt", detail, updated.Skill)
	}

	requireVersionFiles(t, lib, detail.VersionID, []library.File{
		{Path: "SKILL.md", Content: []byte(skillDocument("changing", "新版正文")), Mode: library.ModeRegular},
		{Path: "new.sh", Content: []byte("new"), Mode: library.ModeExecutable},
	})
	_, err = lib.GetVersionFile(ctx, detail.VersionID, "removed.txt")
	requireErrorDetails(t, err, library.NotFound, library.ErrorDetails{Path: "removed.txt"})
	requireVersionFiles(t, lib, fetched.VersionID, []library.File{
		{Path: "SKILL.md", Content: []byte(skillDocument("changing", "old")), Mode: library.ModeRegular},
		{Path: "removed.txt", Content: []byte("removed"), Mode: library.ModeRegular},
	})
}

func TestRemoveSkillKeepsOrRemovesFolders(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)

	fetchSkill := func(name string) library.Skill {
		t.Helper()
		source := filepath.Join(t.TempDir(), name)
		writeLocalSkill(t, source, "# "+name+"\n")
		skill, err := fetchLocalDir(ctx, lib, source)
		if err != nil {
			t.Fatal(err)
		}
		return skill
	}
	keepSkill := fetchSkill("keep-skill")
	removeSkill := fetchSkill("remove-skill")
	group, err := lib.CreateGroup(ctx, "temporary")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.AddSkillToGroup(ctx, group.ID, removeSkill.ID); err != nil {
		t.Fatal(err)
	}
	keepScope := library.GlobalScope(library.AgentCodex)
	removeScope := library.GlobalScope(library.AgentClaudeCode)
	keepDirectory, err := lib.ScopeDirectory(ctx, keepScope)
	if err != nil {
		t.Fatal(err)
	}
	removeDirectory, err := lib.ScopeDirectory(ctx, removeScope)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.Install(ctx, keepSkill.ID, keepScope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.InstallGroup(ctx, group.ID, removeScope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	untracked := filepath.Join(t.TempDir(), "untracked", removeSkill.Name, "mine.txt")
	writeFile(t, untracked, []byte("mine"), 0o644)
	skills, err := lib.ListSkills(ctx)
	if err != nil || len(skills) != 2 {
		t.Fatalf("ListSkills() before removals = %#v, %v", skills, err)
	}

	if err := lib.RemoveSkill(ctx, keepSkill.ID, library.RemoveSkillOptions{Folders: library.Keep}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(keepDirectory, keepSkill.Name, "SKILL.md")); err != nil {
		t.Fatalf("explicit Keep removed installed folder: %v", err)
	}
	writeFile(t, filepath.Join(removeDirectory, removeSkill.Name, "SKILL.md"), []byte("edited"), 0o644)
	if err := lib.RemoveSkill(ctx, removeSkill.ID, library.RemoveSkillOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(removeDirectory, removeSkill.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Remove left installed folder behind: %v", err)
	}
	if _, err := os.Stat(untracked); err != nil {
		t.Fatalf("Remove touched an untracked directory: %v", err)
	}

	skills, err = lib.ListSkills(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 0 {
		t.Fatalf("ListSkills() after removals = %#v", skills)
	}
	skillScopes, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(skillScopes) != 0 {
		t.Fatalf("ListSkillScopes() after removals = %#v", skillScopes)
	}
	if err := lib.RemoveSkill(ctx, library.SkillID(9999), library.RemoveSkillOptions{}); !library.IsCode(err, library.NotFound) {
		t.Fatalf("RemoveSkill() unknown skill error = %v", err)
	}
	if _, err := os.Stat(untracked); err != nil {
		t.Fatalf("unknown removal touched disk: %v", err)
	}
}

// RemoveSkill(Remove) 先删记录、把技能文件夹改名暂存、提交后才真正删除:改名失败时库与磁盘都原样不动;
// 技能文件夹已被手动删掉时仍成功;成功后 Scope 目录下不留暂存残留。
func TestRemoveSkillRemoveKeepsLibraryAndDiskTogether(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	present := fetchNamedSkill(t, lib, "present")
	missing := fetchNamedSkill(t, lib, "missing")
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.ScopeDirectory(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range []library.Skill{present, missing} {
		if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	before, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{})
	if err != nil {
		t.Fatal(err)
	}

	makeScopeReadOnly(t, directory)
	err = lib.RemoveSkill(ctx, present.ID, library.RemoveSkillOptions{Folders: library.Remove})
	requireLibraryError(t, err, library.Permission)
	if err := os.Chmod(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	skills, err := lib.ListSkills(ctx)
	if err != nil || !reflect.DeepEqual(skillIDs(skills), []library.SkillID{missing.ID, present.ID}) {
		t.Fatalf("failed Remove changed skills: %#v, %v", skills, err)
	}
	after, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{})
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("failed Remove changed skill scopes: %#v, %v; want %#v", after, err, before)
	}
	content, err := os.ReadFile(filepath.Join(directory, present.Name, "SKILL.md"))
	if err != nil || string(content) != skillDocument("present", "# present\n") {
		t.Fatalf("failed Remove changed skill scope: %q, %v", content, err)
	}
	requireNoStagedEntries(t, directory)

	if err := os.RemoveAll(filepath.Join(directory, missing.Name)); err != nil {
		t.Fatal(err)
	}
	if err := lib.RemoveSkill(ctx, missing.ID, library.RemoveSkillOptions{Folders: library.Remove}); err != nil {
		t.Fatalf("Remove with manually deleted skill scope = %v", err)
	}
	if err := lib.RemoveSkill(ctx, present.ID, library.RemoveSkillOptions{Folders: library.Remove}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, present.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Remove left installed folder behind: %v", err)
	}
	skills, err = lib.ListSkills(ctx)
	if err != nil || len(skills) != 0 {
		t.Fatalf("ListSkills() after removals = %#v, %v", skills, err)
	}
	skillScopes, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{})
	if err != nil || len(skillScopes) != 0 {
		t.Fatalf("ListSkillScopes() after removals = %#v, %v", skillScopes, err)
	}
	requireNoStagedEntries(t, directory)
}

func TestFetchDuplicateLocalSkillCarriesIdentity(t *testing.T) {
	lib := openTestLibrary(t)
	source := filepath.Join(t.TempDir(), "dup")
	writeLocalSkill(t, source, "# Dup\n")
	skill, err := fetchLocalDir(t.Context(), lib, source)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fetchLocalDir(t.Context(), lib, source)
	requireErrorDetails(t, err, library.AlreadyExists, library.ErrorDetails{SkillID: skill.ID, Name: skill.Name})
	skills, err := lib.ListSkills(t.Context())
	if err != nil || len(skills) != 1 || skills[0].ID != skill.ID {
		t.Fatalf("duplicate fetch changed library: %#v, %v", skills, err)
	}
}
