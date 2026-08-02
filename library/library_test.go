package library_test

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/tiaot33/skills-tui/library"
)

func TestInstallRemoteURLDefaultsToAllSkills(t *testing.T) {
	archive := zipArchive(t, map[string]string{
		"first/SKILL.md":  "# First\n",
		"first/guide.md":  "guide\n",
		"second/SKILL.md": "# Second\n",
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	t.Cleanup(server.Close)

	lib := openTestLibrary(t)

	installed, err := lib.InstallRemote(context.Background(), library.Source{
		Type: library.SourceURL, Locator: library.SourceLocator{URL: server.URL + "/skills.zip"},
	}, library.InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := skillNames(installed), []string{"first", "second"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("installed skills = %v, want %v", got, want)
	}
	if installed[0].Source.ID == 0 || installed[0].Source.ID != installed[1].Source.ID {
		t.Fatalf("source IDs = %d and %d, want one persisted Source", installed[0].Source.ID, installed[1].Source.ID)
	}
	collections, err := lib.ListCollections(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(collections) != 1 || !reflect.DeepEqual(summaryNames(collections[0].Skills), []string{"first", "second"}) {
		t.Fatalf("collections = %#v, want both remote skills grouped by Source", collections)
	}
	group, err := lib.CreateGroup(context.Background(), "remote group")
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.AddSkillToGroup(context.Background(), group.ID, installed[0].ID); err != nil {
		t.Fatal(err)
	}
	scope := library.Scope{Agent: library.AgentCodex, Path: filepath.Join(t.TempDir(), "skills")}
	if err := lib.ActivateGroup(context.Background(), group.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := lib.RemoveSkill(context.Background(), installed[0].ID, library.RemoveSkillOptions{}); err != nil {
		t.Fatal(err)
	}
	activations, err := lib.ListActivations(context.Background())
	if err != nil || len(activations) != 0 {
		t.Fatalf("activations after remote removal = %#v, %v; want none", activations, err)
	}
	if _, err := os.Stat(filepath.Join(scope.Path, installed[0].Name, "SKILL.md")); err != nil {
		t.Fatalf("default Keep removed remote projection: %v", err)
	}
	remaining, err := lib.ListSkills(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := summaryNames(remaining), []string{"second"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("skills after remote removal = %v, want %v", got, want)
	}
}

func TestInstallRemoteGitHubSelectionAndUpdateOnlyWhenContentChanges(t *testing.T) {
	repository := t.TempDir()
	runGit(t, repository, "init", "-b", "main")
	writeFile(t, filepath.Join(repository, "first", "SKILL.md"), []byte("first\n"), 0o644)
	writeFile(t, filepath.Join(repository, "second", "SKILL.md"), []byte("second\n"), 0o644)
	commitGit(t, repository, "initial")

	lib := openTestLibrary(t)
	ctx := context.Background()
	installed, err := lib.InstallRemote(ctx, library.Source{
		Type: library.SourceGitHub, Locator: library.SourceLocator{Repository: repository, Branch: "main"},
	}, library.InstallOptions{Skills: []string{"first"}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := skillNames(installed), []string{"first"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("installed skills = %v, want %v", got, want)
	}

	writeFile(t, filepath.Join(repository, "first", "SKILL.md"), []byte("updated\n"), 0o644)
	commitGit(t, repository, "update")
	updated, err := lib.Update(ctx, installed[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(updated.Files[0].Content); got != "updated\n" {
		t.Fatalf("updated content = %q, want updated", got)
	}
	if updated.Source.LastSeenRef == "" || updated.Source.LastSeenRef == installed[0].Source.LastSeenRef {
		t.Fatalf("updated source ref = %q, want new git commit", updated.Source.LastSeenRef)
	}
	persisted, err := lib.GetSkill(ctx, installed[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Source.LastSeenRef != updated.Source.LastSeenRef {
		t.Fatalf("persisted source ref = %q, want %q", persisted.Source.LastSeenRef, updated.Source.LastSeenRef)
	}
	if _, err := lib.Update(ctx, installed[0].ID); err != nil {
		t.Fatal(err)
	}
	versions, err := lib.ListVersions(ctx, installed[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 {
		t.Fatalf("versions = %d, want 2 after changed and unchanged updates", len(versions))
	}
}

func TestInstallRemoteStoreFetchesPackageVersionAndHonoursSelection(t *testing.T) {
	archive := zipArchive(t, map[string]string{
		"first/SKILL.md":  "# First\n",
		"second/SKILL.md": "# Second\n",
	})
	var requestedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requestedPath = request.URL.EscapedPath()
		_, _ = w.Write(archive)
	}))
	t.Cleanup(server.Close)
	t.Setenv("SKILLS_STORE_URL", server.URL)

	lib := openTestLibrary(t)
	installed, err := lib.InstallRemote(context.Background(), library.Source{
		Type:    library.SourceStore,
		Locator: library.SourceLocator{PackageID: "owner/package", Version: "1.2.3 beta"},
	}, library.InstallOptions{Skills: []string{"second"}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := skillNames(installed), []string{"second"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("installed skills = %v, want %v", got, want)
	}
	if got, want := requestedPath, "/owner%2Fpackage/1.2.3%20beta"; got != want {
		t.Fatalf("store path = %q, want %q", got, want)
	}
}

func TestInstallRemoteMapsFetchErrorsAndPreservesCancellation(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		code   library.ErrorCode
	}{
		{name: "permission", status: http.StatusForbidden, code: library.Permission},
		{name: "unreachable", status: http.StatusServiceUnavailable, code: library.SourceUnreachable},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
			}))
			defer server.Close()
			lib, err := library.Open(filepath.Join(t.TempDir(), "library.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer lib.Close()
			_, err = lib.InstallRemote(context.Background(), library.Source{
				Type: library.SourceURL, Locator: library.SourceLocator{URL: server.URL},
			}, library.InstallOptions{})
			if !library.IsCode(err, test.code) {
				t.Fatalf("error = %v, want code %s", err, test.code)
			}
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	lib, err := library.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	_, err = lib.InstallRemote(ctx, library.Source{
		Type: library.SourceURL, Locator: library.SourceLocator{URL: "http://127.0.0.1/skills.zip"},
	}, library.InstallOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestInstallRemoteRejectsUnsafeArchivePaths(t *testing.T) {
	archive := zipArchive(t, map[string]string{"../SKILL.md": "unsafe\n"})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer server.Close()
	lib, err := library.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	_, err = lib.InstallRemote(context.Background(), library.Source{
		Type: library.SourceURL, Locator: library.SourceLocator{URL: server.URL},
	}, library.InstallOptions{})
	if !library.IsCode(err, library.SourceUnreachable) {
		t.Fatalf("error = %v, want unsafe archive rejected as SourceUnreachable", err)
	}
}

func TestInstallLocalSkillAndReadItBackIntact(t *testing.T) {
	source := filepath.Join(t.TempDir(), "example-skill")
	writeFile(t, filepath.Join(source, "SKILL.md"), []byte("# Example\n"), 0o644)
	writeFile(t, filepath.Join(source, "references", "guide.md"), []byte("nested\n"), 0o644)
	writeFile(t, filepath.Join(source, "scripts", "run.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755)
	writeFile(t, filepath.Join(source, "assets", "data.bin"), []byte{0x00, 0xff, 0x01}, 0o644)

	lib := openTestLibrary(t)

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

	lib := openTestLibrary(t)
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
	lib := openTestLibrary(t)
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
	if got, want := summaryIDs(debuggingSkills), []library.SkillID{first.ID, second.ID}; !reflect.DeepEqual(got, want) {
		t.Fatalf("debugging members = %v, want %v", got, want)
	}
	if got, want := summaryIDs(shippingSkills), []library.SkillID{first.ID}; !reflect.DeepEqual(got, want) {
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

func skillNames(skills []library.Skill) []string {
	names := make([]string, len(skills))
	for i, skill := range skills {
		names[i] = skill.Name
	}
	return names
}

func summaryNames(skills []library.SkillSummary) []string {
	names := make([]string, len(skills))
	for i, skill := range skills {
		names[i] = skill.Name
	}
	return names
}

func summaryIDs(skills []library.SkillSummary) []library.SkillID {
	ids := make([]library.SkillID, len(skills))
	for i, skill := range skills {
		ids[i] = skill.ID
	}
	return ids
}

func TestFacadeFailuresAlwaysReturnTypedErrors(t *testing.T) {
	tests := []struct {
		name       string
		closeFirst bool
		want       library.ErrorCode
		call       func(library.Library) error
	}{
		{name: "create group", closeFirst: true, want: library.Internal, call: func(lib library.Library) error {
			_, err := lib.CreateGroup(context.Background(), "group")
			return err
		}},
		{name: "install fetched", want: library.Internal, call: func(lib library.Library) error {
			_, err := lib.InstallFetched(context.Background(), library.Source{Type: library.SourceLocal}, "skill", nil)
			return err
		}},
		{name: "list versions", closeFirst: true, want: library.Internal, call: func(lib library.Library) error {
			_, err := lib.ListVersions(context.Background(), 1)
			return err
		}},
		{name: "get version", want: library.NotFound, call: func(lib library.Library) error {
			_, err := lib.GetVersion(context.Background(), 9999)
			return err
		}},
		{name: "diff versions", want: library.NotFound, call: func(lib library.Library) error {
			_, err := lib.DiffVersions(context.Background(), 9998, 9999)
			return err
		}},
		{name: "source update", want: library.NotFound, call: func(lib library.Library) error {
			_, err := lib.SourceHasUpdate(context.Background(), 9999, "new")
			return err
		}},
		{name: "list scopes", closeFirst: true, want: library.Internal, call: func(lib library.Library) error {
			_, err := lib.ListScopes(context.Background())
			return err
		}},
		{name: "list collections", closeFirst: true, want: library.Internal, call: func(lib library.Library) error {
			_, err := lib.ListCollections(context.Background())
			return err
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lib, err := library.Open(filepath.Join(t.TempDir(), "library.db"))
			if err != nil {
				t.Fatal(err)
			}
			if test.closeFirst {
				if err := lib.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				t.Cleanup(func() { _ = lib.Close() })
			}
			err = test.call(lib)
			typed, ok := err.(*library.Error)
			if !ok || typed.Code != test.want {
				t.Fatalf("error = %T %v, want *library.Error code %s", err, err, test.want)
			}
		})
	}
}

func TestInstallFetchedSkillsPersistsTypedSources(t *testing.T) {
	lib := openTestLibrary(t)

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
	lib := openTestLibrary(t)

	source := library.Source{
		Type:        library.SourceGitHub,
		Locator:     library.SourceLocator{Repository: "github.com/example/skills", Branch: "main"},
		LastFetched: time.Now().UTC().Truncate(time.Second),
		LastSeenRef: "abc123",
	}
	first, err := lib.InstallFetched(context.Background(), source, "z-first", []library.File{{Path: "SKILL.md", Content: []byte("# first\n"), Mode: library.ModeRegular}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := lib.InstallFetched(context.Background(), source, "a-second", []library.File{{Path: "SKILL.md", Content: []byte("# second\n"), Mode: library.ModeRegular}})
	if err != nil {
		t.Fatal(err)
	}
	if first.Source.ID != second.Source.ID {
		t.Fatalf("source IDs = %d and %d, want one shared Source", first.Source.ID, second.Source.ID)
	}
	collections, err := lib.ListCollections(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(collections) != 1 || !reflect.DeepEqual(summaryIDs(collections[0].Skills), []library.SkillID{first.ID, second.ID}) {
		t.Fatalf("ListCollections() = %#v, want both skills in one collection", collections)
	}
}

func TestActivateAndDeactivateGlobalSkill(t *testing.T) {
	source := filepath.Join(t.TempDir(), "example-skill")
	writeFile(t, filepath.Join(source, "SKILL.md"), []byte("# Example\n"), 0o644)
	writeFile(t, filepath.Join(source, "scripts", "run.sh"), []byte("#!/bin/sh\n"), 0o755)

	lib := openTestLibrary(t)
	ctx := context.Background()
	skill, err := lib.Install(ctx, library.LocalSource{Path: source})
	if err != nil {
		t.Fatal(err)
	}

	scopes, err := lib.ListScopes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(scopes) != 2 || scopes[0].Agent != library.AgentClaudeCode || scopes[1].Agent != library.AgentCodex {
		t.Fatalf("ListScopes() = %#v", scopes)
	}

	scope := library.Scope{Agent: library.AgentCodex, Path: filepath.Join(t.TempDir(), "skills")}
	target := filepath.Join(scope.Path, skill.Name)
	writeFile(t, filepath.Join(target, "old.txt"), []byte("old"), 0o644)
	if err := lib.Activate(ctx, skill.ID, scope, library.ActivateOptions{}); !library.IsCode(err, library.AlreadyExists) {
		t.Fatalf("Activate() conflict error = %v", err)
	}
	if err := lib.Activate(ctx, skill.ID, scope, library.ActivateOptions{OnConflict: library.Overwrite}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, "old.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("overwritten file still exists: %v", err)
	}
	info, err := os.Stat(filepath.Join(target, "scripts", "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatal("activated script is not executable")
	}
	activations, err := lib.ListActivations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(activations) != 1 || activations[0].SkillID != skill.ID || activations[0].Scope != scope {
		t.Fatalf("ListActivations() = %#v", activations)
	}

	if err := lib.Deactivate(ctx, skill.ID, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deactivated directory still exists: %v", err)
	}
	if _, err := lib.GetSkill(ctx, skill.ID); err != nil {
		t.Fatalf("Deactivate removed library skill: %v", err)
	}
	activations, err = lib.ListActivations(ctx)
	if err != nil || len(activations) != 0 {
		t.Fatalf("ListActivations() after deactivate = %#v, %v", activations, err)
	}
	writeFile(t, filepath.Join(target, "manual.txt"), []byte("manual"), 0o644)
	if err := lib.Deactivate(ctx, skill.ID, scope); !library.IsCode(err, library.NotFound) {
		t.Fatalf("Deactivate() inactive skill error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "manual.txt")); err != nil {
		t.Fatalf("Deactivate() removed an untracked directory: %v", err)
	}

	if err := lib.Activate(ctx, library.SkillID(9999), scope, library.ActivateOptions{}); !library.IsCode(err, library.NotFound) {
		t.Fatalf("Activate() unknown skill error = %v", err)
	}
}

func TestListCollectionsDerivesLocalSkillsFromTheirSources(t *testing.T) {
	root := t.TempDir()
	firstSource := filepath.Join(root, "z-first-skill")
	secondSource := filepath.Join(root, "a-second-skill")
	writeFile(t, filepath.Join(firstSource, "SKILL.md"), []byte("# First\n"), 0o644)
	writeFile(t, filepath.Join(secondSource, "SKILL.md"), []byte("# Second\n"), 0o644)
	lib := openTestLibrary(t)
	first, err := lib.Install(context.Background(), library.LocalSource{Path: firstSource})
	if err != nil {
		t.Fatal(err)
	}
	second, err := lib.Install(context.Background(), library.LocalSource{Path: secondSource})
	if err != nil {
		t.Fatal(err)
	}
	got, err := lib.ListCollections(context.Background())
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

func summaryOf(skill library.Skill) library.SkillSummary {
	return library.SkillSummary{ID: skill.ID, Name: skill.Name, Source: skill.Source}
}

func TestRemoveSkillKeepsOrRemovesProjectedCopies(t *testing.T) {
	ctx := context.Background()
	lib := openTestLibrary(t)

	install := func(name string) library.Skill {
		t.Helper()
		source := filepath.Join(t.TempDir(), name)
		writeFile(t, filepath.Join(source, "SKILL.md"), []byte("# "+name+"\n"), 0o644)
		skill, err := lib.Install(ctx, library.LocalSource{Path: source})
		if err != nil {
			t.Fatal(err)
		}
		return skill
	}
	keepSkill := install("keep-skill")
	removeSkill := install("remove-skill")
	group, err := lib.CreateGroup(ctx, "temporary")
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.AddSkillToGroup(ctx, group.ID, removeSkill.ID); err != nil {
		t.Fatal(err)
	}
	keepScope := library.Scope{Agent: library.AgentCodex, Path: filepath.Join(t.TempDir(), "keep")}
	removeScope := library.Scope{Agent: library.AgentClaudeCode, Path: filepath.Join(t.TempDir(), "remove")}
	if err := lib.Activate(ctx, keepSkill.ID, keepScope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := lib.ActivateGroup(ctx, group.ID, removeScope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	untracked := filepath.Join(t.TempDir(), "untracked", removeSkill.Name, "mine.txt")
	writeFile(t, untracked, []byte("mine"), 0o644)
	skills, err := lib.ListSkills(ctx)
	if err != nil || len(skills) != 2 {
		t.Fatalf("ListSkills() before removals = %#v, %v", skills, err)
	}

	if err := lib.RemoveSkill(ctx, keepSkill.ID, library.RemoveSkillOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(keepScope.Path, keepSkill.Name, "SKILL.md")); err != nil {
		t.Fatalf("default Keep removed projected copy: %v", err)
	}
	if err := lib.RemoveSkill(ctx, removeSkill.ID, library.RemoveSkillOptions{ProjectedCopies: library.Remove}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(removeScope.Path, removeSkill.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Remove left projected copy behind: %v", err)
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
	activations, err := lib.ListActivations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(activations) != 0 {
		t.Fatalf("ListActivations() after removals = %#v", activations)
	}
	if err := lib.RemoveSkill(ctx, library.SkillID(9999), library.RemoveSkillOptions{}); !library.IsCode(err, library.NotFound) {
		t.Fatalf("RemoveSkill() unknown skill error = %v", err)
	}
	if _, err := os.Stat(untracked); err != nil {
		t.Fatalf("unknown removal touched disk: %v", err)
	}
}

func TestRegisteredProjectScopesAndUnregisterOptions(t *testing.T) {
	ctx := context.Background()
	lib := openTestLibrary(t)
	if _, err := lib.RegisterProject(ctx, ""); err == nil {
		t.Fatal("RegisterProject() accepted an empty path")
	}

	projectRoot := t.TempDir()
	project, err := lib.RegisterProject(ctx, projectRoot+string(os.PathSeparator)+".")
	if err != nil {
		t.Fatal(err)
	}
	wantRoot, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	if project.ID == 0 || project.Path != wantRoot {
		t.Fatalf("RegisterProject() = %#v, want canonical path %q", project, wantRoot)
	}
	if _, err := lib.RegisterProject(ctx, projectRoot); !library.IsCode(err, library.AlreadyExists) {
		t.Fatalf("duplicate RegisterProject() error = %v", err)
	}
	projects, err := lib.ListRegisteredProjects(ctx)
	if err != nil || !reflect.DeepEqual(projects, []library.RegisteredProject{project}) {
		t.Fatalf("ListRegisteredProjects() = %#v, %v", projects, err)
	}
	projectScopes := []library.Scope{
		{Agent: library.AgentClaudeCode, Path: filepath.Join(project.Path, ".claude", "skills")},
		{Agent: library.AgentCodex, Path: filepath.Join(project.Path, ".codex", "skills")},
	}
	scopes, err := lib.ListScopes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(scopes) != 4 || !reflect.DeepEqual(scopes[2:], projectScopes) {
		t.Fatalf("ListScopes() project scopes = %#v, want %#v", scopes, projectScopes)
	}

	source := filepath.Join(t.TempDir(), "example-skill")
	writeFile(t, filepath.Join(source, "SKILL.md"), []byte("# Example\n"), 0o644)
	skill, err := lib.Install(ctx, library.LocalSource{Path: source})
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.Activate(ctx, skill.ID, projectScopes[1], library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	keptProjection := filepath.Join(projectScopes[1].Path, skill.Name, "SKILL.md")
	if _, err := os.Stat(keptProjection); err != nil {
		t.Fatal(err)
	}
	if err := lib.UnregisterProject(ctx, project.ID, library.UnregisterProjectOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(keptProjection); err != nil {
		t.Fatalf("default Keep removed project projection: %v", err)
	}

	removeProject, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	removeScope := library.Scope{Agent: library.AgentClaudeCode, Path: filepath.Join(removeProject.Path, ".claude", "skills")}
	group, err := lib.CreateGroup(ctx, "project group")
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.AddSkillToGroup(ctx, group.ID, skill.ID); err != nil {
		t.Fatal(err)
	}
	if err := lib.ActivateGroup(ctx, group.ID, removeScope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	manualFile := filepath.Join(removeScope.Path, "manual-skill", "mine.txt")
	writeFile(t, manualFile, []byte("mine"), 0o644)
	if err := lib.UnregisterProject(ctx, removeProject.ID, library.UnregisterProjectOptions{ProjectedCopies: library.Remove}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(removeScope.Path, skill.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Remove left project projection behind: %v", err)
	}
	if _, err := os.Stat(manualFile); err != nil {
		t.Fatalf("Remove touched an untracked directory: %v", err)
	}
	projects, err = lib.ListRegisteredProjects(ctx)
	if err != nil || len(projects) != 0 {
		t.Fatalf("ListRegisteredProjects() after unregister = %#v, %v", projects, err)
	}
	scopes, err = lib.ListScopes(ctx)
	if err != nil || len(scopes) != 2 {
		t.Fatalf("ListScopes() after unregister = %#v, %v", scopes, err)
	}
	activations, err := lib.ListActivations(ctx)
	if err != nil || len(activations) != 0 {
		t.Fatalf("ListActivations() after unregister = %#v, %v", activations, err)
	}
	if err := lib.UnregisterProject(ctx, library.ProjectID(9999), library.UnregisterProjectOptions{}); !library.IsCode(err, library.NotFound) {
		t.Fatalf("UnregisterProject() unknown project error = %v", err)
	}
	if _, err := os.Stat(manualFile); err != nil {
		t.Fatalf("unknown unregister touched disk: %v", err)
	}
}

func TestActivateAndDeactivateGroupPreservesIndependentActivation(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	firstSource := filepath.Join(root, "first")
	secondSource := filepath.Join(root, "second")
	writeFile(t, filepath.Join(firstSource, "SKILL.md"), []byte("# First\n"), 0o644)
	writeFile(t, filepath.Join(secondSource, "SKILL.md"), []byte("# Second\n"), 0o644)

	lib := openTestLibrary(t)
	first, err := lib.Install(ctx, library.LocalSource{Path: firstSource})
	if err != nil {
		t.Fatal(err)
	}
	second, err := lib.Install(ctx, library.LocalSource{Path: secondSource})
	if err != nil {
		t.Fatal(err)
	}
	group, err := lib.CreateGroup(ctx, "shipping")
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range []library.Skill{first, second} {
		if err := lib.AddSkillToGroup(ctx, group.ID, skill.ID); err != nil {
			t.Fatal(err)
		}
	}
	scope := library.Scope{Agent: library.AgentCodex, Path: filepath.Join(t.TempDir(), "skills")}
	if err := lib.Activate(ctx, first.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := lib.ActivateGroup(ctx, group.ID, scope, library.ActivateOptions{OnConflict: library.Overwrite}); err != nil {
		t.Fatal(err)
	}
	for _, skill := range []library.Skill{first, second} {
		if _, err := os.Stat(filepath.Join(scope.Path, skill.Name, "SKILL.md")); err != nil {
			t.Fatalf("member %q was not projected: %v", skill.Name, err)
		}
	}
	activations, err := lib.ListActivations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantActivations := []library.Activation{
		{SkillID: first.ID, Scope: scope, Individual: true, ViaGroups: []library.GroupID{group.ID}},
		{SkillID: second.ID, Scope: scope, ViaGroups: []library.GroupID{group.ID}},
	}
	if !reflect.DeepEqual(activations, wantActivations) {
		t.Fatalf("ListActivations() = %#v, want %#v", activations, wantActivations)
	}

	if err := lib.DeactivateGroup(ctx, group.ID, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(scope.Path, first.Name, "SKILL.md")); err != nil {
		t.Fatalf("independent activation was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(scope.Path, second.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("via-group-only activation remains: %v", err)
	}
	activations, err = lib.ListActivations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantActivations = []library.Activation{{SkillID: first.ID, Scope: scope, Individual: true}}
	if !reflect.DeepEqual(activations, wantActivations) {
		t.Fatalf("ListActivations() after DeactivateGroup = %#v, want %#v", activations, wantActivations)
	}
}

func TestActivateGroupChecksAllConflictsBeforeProjectingMembers(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	firstSource := filepath.Join(root, "first")
	secondSource := filepath.Join(root, "second")
	writeFile(t, filepath.Join(firstSource, "SKILL.md"), []byte("# First\n"), 0o644)
	writeFile(t, filepath.Join(secondSource, "SKILL.md"), []byte("# Second\n"), 0o644)
	lib := openTestLibrary(t)
	first, err := lib.Install(ctx, library.LocalSource{Path: firstSource})
	if err != nil {
		t.Fatal(err)
	}
	second, err := lib.Install(ctx, library.LocalSource{Path: secondSource})
	if err != nil {
		t.Fatal(err)
	}
	group, err := lib.CreateGroup(ctx, "shipping")
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range []library.Skill{first, second} {
		if err := lib.AddSkillToGroup(ctx, group.ID, skill.ID); err != nil {
			t.Fatal(err)
		}
	}
	scope := library.Scope{Agent: library.AgentCodex, Path: filepath.Join(t.TempDir(), "skills")}
	writeFile(t, filepath.Join(scope.Path, second.Name, "manual.txt"), []byte("manual"), 0o644)

	if err := lib.ActivateGroup(ctx, group.ID, scope, library.ActivateOptions{}); !library.IsCode(err, library.AlreadyExists) {
		t.Fatalf("ActivateGroup() conflict error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(scope.Path, first.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("first member was partially activated: %v", err)
	}
	if _, err := os.Stat(filepath.Join(scope.Path, second.Name, "manual.txt")); err != nil {
		t.Fatalf("conflicting directory changed: %v", err)
	}
	activations, err := lib.ListActivations(ctx)
	if err != nil || len(activations) != 0 {
		t.Fatalf("ListActivations() = %#v, %v; want none", activations, err)
	}
}

func TestGroupOperationsReturnNotFoundForUnknownIDs(t *testing.T) {
	ctx := context.Background()
	source := filepath.Join(t.TempDir(), "example")
	writeFile(t, filepath.Join(source, "SKILL.md"), []byte("# Example\n"), 0o644)
	lib := openTestLibrary(t)
	skill, err := lib.Install(ctx, library.LocalSource{Path: source})
	if err != nil {
		t.Fatal(err)
	}
	group, err := lib.CreateGroup(ctx, "known")
	if err != nil {
		t.Fatal(err)
	}
	unknownGroup := library.GroupID(9999)
	unknownSkill := library.SkillID(9999)
	scope := library.Scope{Agent: library.AgentCodex, Path: filepath.Join(t.TempDir(), "skills")}

	checks := []struct {
		name string
		err  error
	}{
		{"activate group", lib.ActivateGroup(ctx, unknownGroup, scope, library.ActivateOptions{})},
		{"deactivate group", lib.DeactivateGroup(ctx, unknownGroup, scope)},
		{"list group", func() error { _, err := lib.ListGroupSkills(ctx, unknownGroup); return err }()},
		{"add to unknown group", lib.AddSkillToGroup(ctx, unknownGroup, skill.ID)},
		{"add unknown skill", lib.AddSkillToGroup(ctx, group.ID, unknownSkill)},
	}
	for _, check := range checks {
		if !library.IsCode(check.err, library.NotFound) {
			t.Errorf("%s error = %v, want NotFound", check.name, check.err)
		}
	}
}

func TestGroupLifecycleKeepsSkillsAndProjectedCopies(t *testing.T) {
	ctx := context.Background()
	lib := openTestLibrary(t)
	source := filepath.Join(t.TempDir(), "group-skill")
	writeFile(t, filepath.Join(source, "SKILL.md"), []byte("group\n"), 0o644)
	skill, err := lib.Install(ctx, library.LocalSource{Path: source})
	if err != nil {
		t.Fatal(err)
	}
	groupOnlySource := filepath.Join(t.TempDir(), "group-only")
	writeFile(t, filepath.Join(groupOnlySource, "SKILL.md"), []byte("group only\n"), 0o644)
	groupOnly, err := lib.Install(ctx, library.LocalSource{Path: groupOnlySource})
	if err != nil {
		t.Fatal(err)
	}
	first, err := lib.CreateGroup(ctx, "first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := lib.CreateGroup(ctx, "second")
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range []library.Group{first, second} {
		if err := lib.AddSkillToGroup(ctx, group.ID, skill.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := lib.AddSkillToGroup(ctx, first.ID, groupOnly.ID); err != nil {
		t.Fatal(err)
	}
	if err := lib.RenameGroup(ctx, first.ID, "renamed"); err != nil {
		t.Fatal(err)
	}
	groups, err := lib.ListGroups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := groups, []library.Group{{ID: first.ID, Name: "renamed"}, second}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ListGroups() = %#v, want %#v", got, want)
	}
	scope := library.Scope{Agent: library.AgentCodex, Path: filepath.Join(t.TempDir(), "skills")}
	if err := lib.ActivateGroup(ctx, first.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := lib.ActivateGroup(ctx, second.ID, scope, library.ActivateOptions{OnConflict: library.Overwrite}); err != nil {
		t.Fatal(err)
	}
	if err := lib.DeleteGroup(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(scope.Path, skill.Name, "SKILL.md")); err != nil {
		t.Fatalf("DeleteGroup removed projected copy: %v", err)
	}
	if _, err := lib.GetSkill(ctx, skill.ID); err != nil {
		t.Fatalf("DeleteGroup removed skill: %v", err)
	}
	activations, err := lib.ListActivations(ctx)
	wantActivations := []library.Activation{
		{SkillID: skill.ID, Scope: scope, ViaGroups: []library.GroupID{second.ID}},
		{SkillID: groupOnly.ID, Scope: scope, Individual: true},
	}
	if err != nil || !reflect.DeepEqual(activations, wantActivations) {
		t.Fatalf("activations after DeleteGroup = %#v, %v; want %#v", activations, err, wantActivations)
	}
	if _, err := os.Stat(filepath.Join(scope.Path, groupOnly.Name, "SKILL.md")); err != nil {
		t.Fatalf("DeleteGroup removed group-only projected copy: %v", err)
	}
	groups, err = lib.ListGroups(ctx)
	if err != nil || !reflect.DeepEqual(groups, []library.Group{second}) {
		t.Fatalf("groups after DeleteGroup = %#v, %v", groups, err)
	}
	if err := lib.DeleteGroup(ctx, first.ID); !library.IsCode(err, library.NotFound) {
		t.Fatalf("second DeleteGroup error = %v, want NotFound", err)
	}
	if err := lib.RemoveSkillFromGroup(ctx, second.ID, skill.ID); err != nil {
		t.Fatal(err)
	}
	members, err := lib.ListGroupSkills(ctx, second.ID)
	if err != nil || len(members) != 0 {
		t.Fatalf("members after remove = %#v, %v", members, err)
	}
	if err := lib.RenameGroup(ctx, library.GroupID(9999), "missing"); !library.IsCode(err, library.NotFound) {
		t.Fatalf("RenameGroup unknown error = %v", err)
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

func zipArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, content := range files {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func runGit(t *testing.T, directory string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func commitGit(t *testing.T, repository, message string) {
	t.Helper()
	runGit(t, repository, "add", ".")
	runGit(t, repository, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", message)
}

func openTestLibrary(t *testing.T) library.Library {
	t.Helper()
	lib, err := library.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
	return lib
}
