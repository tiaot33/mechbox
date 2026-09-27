package library_test

import (
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

	fetched, err := fetchLocal(t.Context(), lib, source)
	if err != nil {
		t.Fatal(err)
	}
	got, err := lib.GetSkill(t.Context(), fetched.ID)
	if err != nil {
		t.Fatal(err)
	}

	want := library.Skill{
		ID:        fetched.ID,
		Name:      "example-skill",
		FetchedAt: fetched.FetchedAt,
		UpdatedAt: fetched.UpdatedAt,
		Source: library.Source{
			ID:          fetched.Source.ID,
			Type:        library.SourceLocal,
			Locator:     library.SourceLocator{Path: source},
			LastFetched: fetched.Source.LastFetched,
		},
		Files: []library.File{
			{Path: "SKILL.md", Content: []byte(skillDocument("example-skill", "# Example\n")), Mode: library.ModeRegular},
			{Path: "assets/data.bin", Content: []byte{0x00, 0xff, 0x01}, Mode: library.ModeRegular},
			{Path: "references/guide.md", Content: []byte("nested\n"), Mode: library.ModeRegular},
			{Path: "scripts/run.sh", Content: []byte("#!/bin/sh\nexit 0\n"), Mode: library.ModeExecutable},
		},
	}
	if !reflect.DeepEqual(got, want) || got.Source.LastFetched.IsZero() {
		t.Fatalf("GetSkill() = %#v, want %#v", got, want)
	}
	if !reflect.DeepEqual(fetched, got) {
		t.Fatalf("fetchLocal() = %#v, GetSkill() = %#v", fetched, got)
	}
}

func TestFetchRecordsDescriptionAndVersionTimes(t *testing.T) {
	ctx := t.Context()
	lib := openTestLibrary(t)
	withDesc := filepath.Join(t.TempDir(), "described")
	writeFile(t, filepath.Join(withDesc, "SKILL.md"), []byte("---\nname: described\ndescription: a useful helper\n---\n# Described\n"), 0o644)
	start := time.Now().UTC()
	fetched, err := fetchLocal(ctx, lib, withDesc)
	if err != nil {
		t.Fatal(err)
	}
	end := time.Now().UTC()
	summaries, err := lib.ListSkills(ctx)
	if err != nil || len(summaries) != 1 || summaries[0].Description != "a useful helper" {
		t.Fatalf("ListSkills description = %#v, %v", summaries, err)
	}
	if fetched.Description != "a useful helper" {
		t.Fatalf("Fetch Description = %q, want a useful helper", fetched.Description)
	}
	versions, err := lib.ListVersions(ctx, fetched.ID)
	if err != nil || len(versions) != 1 {
		t.Fatalf("ListVersions = %#v, %v", versions, err)
	}
	if !fetched.FetchedAt.Equal(fetched.UpdatedAt) || !fetched.FetchedAt.Equal(versions[0].CreatedAt) {
		t.Fatalf("times = fetched %v updated %v version %v, want equal", fetched.FetchedAt, fetched.UpdatedAt, versions[0].CreatedAt)
	}
	if fetched.FetchedAt.Before(start) || fetched.FetchedAt.After(end) {
		t.Fatalf("InstalledAt = %v, want in [%v, %v]", fetched.FetchedAt, start, end)
	}

	plain := filepath.Join(t.TempDir(), "plain")
	writeLocalSkill(t, plain, "# Plain\n")
	undocumented, err := fetchLocal(ctx, lib, plain)
	if err != nil || undocumented.Description != "" {
		t.Fatalf("Fetch without description = %#v, %v; want empty", undocumented, err)
	}
}

func TestListCollectionsDerivesLocalSkillsFromTheirSources(t *testing.T) {
	root := t.TempDir()
	firstSource := filepath.Join(root, "z-first-skill")
	secondSource := filepath.Join(root, "a-second-skill")
	writeLocalSkill(t, firstSource, "# First\n")
	writeLocalSkill(t, secondSource, "# Second\n")
	lib := openTestLibrary(t)
	first, err := fetchLocal(t.Context(), lib, firstSource)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fetchLocal(t.Context(), lib, secondSource)
	if err != nil {
		t.Fatal(err)
	}
	got, err := lib.ListCollections(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := []library.Collection{
		{Source: first.Source, Skills: []library.SkillSummary{summaryOf(first)}},
		{Source: second.Source, Skills: []library.SkillSummary{summaryOf(second)}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListCollections() = %#v, want %#v", got, want)
	}
}

func TestRemoveSkillKeepsOrRemovesInstallations(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)

	fetchSkill := func(name string) library.Skill {
		t.Helper()
		source := filepath.Join(t.TempDir(), name)
		writeLocalSkill(t, source, "# "+name+"\n")
		skill, err := fetchLocal(ctx, lib, source)
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
	keepDirectory, err := lib.Resolve(ctx, keepScope)
	if err != nil {
		t.Fatal(err)
	}
	removeDirectory, err := lib.Resolve(ctx, removeScope)
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

	if err := lib.RemoveSkill(ctx, keepSkill.ID, library.RemoveSkillOptions{Installations: library.Keep}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(keepDirectory, keepSkill.Name, "SKILL.md")); err != nil {
		t.Fatalf("explicit Keep removed installation folder: %v", err)
	}
	writeFile(t, filepath.Join(removeDirectory, removeSkill.Name, "SKILL.md"), []byte("edited"), 0o644)
	if err := lib.RemoveSkill(ctx, removeSkill.ID, library.RemoveSkillOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(removeDirectory, removeSkill.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Remove left installation folder behind: %v", err)
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
	installations, err := lib.ListInstallations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(installations) != 0 {
		t.Fatalf("ListInstallations() after removals = %#v", installations)
	}
	if err := lib.RemoveSkill(ctx, library.SkillID(9999), library.RemoveSkillOptions{}); !library.IsCode(err, library.NotFound) {
		t.Fatalf("RemoveSkill() unknown skill error = %v", err)
	}
	if _, err := os.Stat(untracked); err != nil {
		t.Fatalf("unknown removal touched disk: %v", err)
	}
}

// RemoveSkill(Remove) 先删记录、把安装项改名暂存、提交后才真正删除:改名失败时库与磁盘都原样不动;
// 安装项已被手动删掉时仍成功;成功后 Scope 目录下不留暂存残留。
func TestRemoveSkillRemoveKeepsLibraryAndDiskTogether(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	present := fetchNamedSkill(t, lib, "present")
	missing := fetchNamedSkill(t, lib, "missing")
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.Resolve(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range []library.Skill{present, missing} {
		if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	before, err := lib.ListInstallations(ctx)
	if err != nil {
		t.Fatal(err)
	}

	makeScopeReadOnly(t, directory)
	err = lib.RemoveSkill(ctx, present.ID, library.RemoveSkillOptions{Installations: library.Remove})
	requireLibraryError(t, err, library.Permission)
	if err := os.Chmod(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	skills, err := lib.ListSkills(ctx)
	if err != nil || !reflect.DeepEqual(summaryIDs(skills), []library.SkillID{missing.ID, present.ID}) {
		t.Fatalf("failed Remove changed skills: %#v, %v", skills, err)
	}
	after, err := lib.ListInstallations(ctx)
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("failed Remove changed installations: %#v, %v; want %#v", after, err, before)
	}
	content, err := os.ReadFile(filepath.Join(directory, present.Name, "SKILL.md"))
	if err != nil || string(content) != skillDocument("present", "# present\n") {
		t.Fatalf("failed Remove changed installation: %q, %v", content, err)
	}
	requireNoStagedEntries(t, directory)

	if err := os.RemoveAll(filepath.Join(directory, missing.Name)); err != nil {
		t.Fatal(err)
	}
	if err := lib.RemoveSkill(ctx, missing.ID, library.RemoveSkillOptions{Installations: library.Remove}); err != nil {
		t.Fatalf("Remove with manually deleted installation = %v", err)
	}
	if err := lib.RemoveSkill(ctx, present.ID, library.RemoveSkillOptions{Installations: library.Remove}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, present.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Remove left installation folder behind: %v", err)
	}
	skills, err = lib.ListSkills(ctx)
	if err != nil || len(skills) != 0 {
		t.Fatalf("ListSkills() after removals = %#v, %v", skills, err)
	}
	installations, err := lib.ListInstallations(ctx)
	if err != nil || len(installations) != 0 {
		t.Fatalf("ListInstallations() after removals = %#v, %v", installations, err)
	}
	requireNoStagedEntries(t, directory)
}

func TestFetchDuplicateLocalSkillCarriesIdentity(t *testing.T) {
	lib := openTestLibrary(t)
	source := filepath.Join(t.TempDir(), "dup")
	writeLocalSkill(t, source, "# Dup\n")
	skill, err := fetchLocal(t.Context(), lib, source)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fetchLocal(t.Context(), lib, source)
	requireErrorDetails(t, err, library.AlreadyExists, library.ErrorDetails{SkillID: skill.ID, Name: skill.Name})
	skills, err := lib.ListSkills(t.Context())
	if err != nil || len(skills) != 1 || skills[0].ID != skill.ID {
		t.Fatalf("duplicate fetch changed library: %#v, %v", skills, err)
	}
}
