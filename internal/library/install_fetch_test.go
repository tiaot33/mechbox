package library_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/tiaot33/skills-tui/internal/library"
)

func TestInstallFetchKeepsCandidateDescription(t *testing.T) {
	for _, source := range []struct {
		name string
		spec func(*testing.T) library.SourceSpec
	}{
		{"github", func(t *testing.T) library.SourceSpec {
			repository := t.TempDir()
			runGit(t, repository, "init", "-b", "main")
			writeFile(t, filepath.Join(repository, "useful", "SKILL.md"), []byte("---\nname: useful\ndescription: helps a lot\n---\nbody\n"), 0o644)
			commitGit(t, repository, "initial")
			return library.SourceSpec{Type: library.SourceGitHub, Locator: library.SourceLocator{Repository: repository, Branch: "main"}}
		}},
		{"url", func(t *testing.T) library.SourceSpec {
			return serveArchive(t, zipArchive(t, map[string]string{"useful/SKILL.md": "---\nname: useful\ndescription: helps a lot\n---\nbody\n"}))
		}},
	} {
		t.Run(source.name, func(t *testing.T) {
			lib := openTestLibrary(t)
			ctx := t.Context()
			fetch, err := lib.Fetch(ctx, source.spec(t))
			if err != nil {
				t.Fatal(err)
			}
			if len(fetch.Skills) != 1 || fetch.Skills[0].Description != "helps a lot" {
				t.Fatalf("Fetch = %#v", fetch)
			}
			description := fetch.Skills[0].Description
			fetch.Skills[0].Description = "changed by caller"
			installed, err := lib.InstallFetch(ctx, fetch.ID, nil)
			if err != nil || len(installed) != 1 || installed[0].Description != "helps a lot" {
				t.Fatalf("InstallFetch = %#v, %v", installed, err)
			}
			summaries, err := lib.ListSkills(ctx)
			if err != nil || len(summaries) != 1 || summaries[0].Description != description {
				t.Fatalf("ListSkills description = %#v, %v; want %q", summaries, err, description)
			}
		})
	}
}

func TestInstallFetchFailureKeepsHandleAndLibraryUnchanged(t *testing.T) {
	repository := installFetchRepository(t)
	lib, err := library.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
	ctx := t.Context()
	fetch, err := lib.Fetch(ctx, library.SourceSpec{Type: library.SourceGitHub, Locator: library.SourceLocator{Repository: repository, Branch: "main"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(repository); err != nil {
		t.Fatal(err)
	}
	if installed, err := lib.InstallFetch(ctx, fetch.ID, []string{"first", "missing"}); err == nil || len(installed) != 0 {
		t.Fatalf("unknown selection = %v, %v; want no results and error", installed, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if installed, err := lib.InstallFetch(canceled, fetch.ID, nil); !library.IsCode(err, library.Canceled) || !errors.Is(err, context.Canceled) || len(installed) != 0 {
		t.Fatalf("canceled install = %v, %v; want no results and Canceled", installed, err)
	}
	collections, err := lib.ListCollections(ctx)
	if err != nil || len(collections) != 0 {
		t.Fatalf("failed attempts left collections: %v, %v", collections, err)
	}
	installed, err := lib.InstallFetch(ctx, fetch.ID, nil)
	if err != nil || len(installed) != 2 || installed[0].Name != "first" || installed[1].Name != "second" {
		t.Fatalf("offline retry with empty filter = %v, %v; want both Skills", installed, err)
	}
	if _, err := lib.InstallFetch(ctx, fetch.ID, []string{"second"}); !library.IsCode(err, library.NotFound) {
		t.Fatalf("reused handle after success: %v; want NotFound", err)
	}
}

func TestInstallFetchConcurrentCallsConsumeHandleOnce(t *testing.T) {
	repository := installFetchRepository(t)
	// 每轮使用独立的库,两个调用选择不同候选,避免唯一约束掩盖重复消费。
	for range 10 {
		lib, err := library.Open(filepath.Join(t.TempDir(), "library.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = lib.Close() })
		ctx := t.Context()
		fetch, err := lib.Fetch(ctx, library.SourceSpec{Type: library.SourceGitHub, Locator: library.SourceLocator{Repository: repository, Branch: "main"}})
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		results := make(chan error, 2)
		for _, name := range fetch.Skills {
			go func() {
				<-start
				_, err := lib.InstallFetch(ctx, fetch.ID, []string{name.Name})
				results <- err
			}()
		}
		close(start)
		// 输家要么撞上"正在安装"(FailedPrecondition),要么在赢家提交后才到、句柄已丢弃(NotFound)。
		succeeded := 0
		for range 2 {
			if err := <-results; err == nil {
				succeeded++
			} else if !library.IsCode(err, library.FailedPrecondition) && !library.IsCode(err, library.NotFound) {
				t.Errorf("concurrent install error = %v, want FailedPrecondition or NotFound", err)
			}
		}
		if succeeded != 1 {
			t.Fatalf("successful consumers = %d, want 1", succeeded)
		}
		skills, err := lib.ListSkills(ctx)
		if err != nil || len(skills) != 1 {
			t.Fatalf("concurrent installs left %v, %v; want one Skill", skills, err)
		}
	}
}

func installFetchRepository(t *testing.T) string {
	t.Helper()
	repository := t.TempDir()
	runGit(t, repository, "init", "-b", "main")
	writeFile(t, filepath.Join(repository, "first", "SKILL.md"), []byte(skillDocument("first", "first")), 0o644)
	writeFile(t, filepath.Join(repository, "second", "SKILL.md"), []byte(skillDocument("second", "second")), 0o644)
	commitGit(t, repository, "initial")
	return repository
}

func TestInstallFetchIsAtomicAndDiscardsSuccessfulHandle(t *testing.T) {
	repository := installFetchRepository(t)
	lib, err := library.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
	ctx := t.Context()
	spec := library.SourceSpec{Type: library.SourceGitHub, Locator: library.SourceLocator{Repository: repository, Branch: "main"}}

	pre, err := lib.Fetch(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.InstallFetch(ctx, pre.ID, []string{"second"}); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.InstallFetch(ctx, pre.ID, []string{"first"}); !library.IsCode(err, library.NotFound) {
		t.Fatalf("reused fetch handle error = %v, want NotFound", err)
	}
	before, err := lib.ListSkills(ctx)
	if err != nil {
		t.Fatal(err)
	}
	again, err := lib.Fetch(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.InstallFetch(ctx, again.ID, nil); !library.IsCode(err, library.AlreadyExists) {
		t.Fatalf("reinstall existing candidate = %v, want AlreadyExists", err)
	} else {
		requireErrorDetails(t, err, library.AlreadyExists, library.ErrorDetails{SkillID: before[0].ID, Name: "second"})
	}
	if after, err := lib.ListSkills(ctx); err != nil || len(after) != 1 || after[0].ID != before[0].ID {
		t.Fatalf("failed batch install changed library: %#v, %v", after, err)
	}
	versions, err := lib.ListVersions(ctx, before[0].ID)
	if err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(repository, "second", "SKILL.md"), []byte(skillDocument("second", "changed second")), 0o644)
	commitGit(t, repository, "changed")
	batch, err := lib.Fetch(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	// first 会先写入事务,随后 second 与已有条目冲突;整批必须回滚。
	if installed, err := lib.InstallFetch(ctx, batch.ID, []string{"first", "second"}); err == nil || len(installed) != 0 {
		t.Fatalf("failed batch = %v, %v; want no results and error", installed, err)
	}
	skills, err := lib.ListSkills(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(skills, before) {
		t.Fatalf("failed batch changed skills or Source: got %#v, want %#v", skills, before)
	}
	afterVersions, err := lib.ListVersions(ctx, before[0].ID)
	if err != nil || !reflect.DeepEqual(afterVersions, versions) {
		t.Fatalf("failed batch changed versions: %v, %v", afterVersions, err)
	}
	if err := os.RemoveAll(repository); err != nil {
		t.Fatal(err)
	}
	if installed, err := lib.InstallFetch(ctx, batch.ID, []string{"first"}); err != nil || len(installed) != 1 {
		t.Fatalf("retry from staged Fetch = %v, %v", installed, err)
	}
}

func TestInstallFetchDefaultsToAllSkillsWithoutRefetching(t *testing.T) {
	archive := zipArchive(t, map[string]string{
		"first/SKILL.md":  skillDocument("first", "# First\n"),
		"first/guide.md":  "guide\n",
		"second/SKILL.md": skillDocument("second", "# Second\n"),
	})
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write(archive)
	}))
	t.Cleanup(server.Close)

	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)

	ctx := t.Context()
	fetch, err := lib.Fetch(ctx, library.SourceSpec{
		Type: library.SourceURL, Locator: library.SourceLocator{URL: server.URL + "/skills.zip"},
	})
	if err != nil {
		t.Fatal(err)
	}
	installed, err := lib.InstallFetch(ctx, fetch.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatalf("remote requests = %d, want Fetch only", requests.Load())
	}
	if got, want := skillNames(installed), []string{"first", "second"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("installed skills = %v, want %v", got, want)
	}
	if installed[0].Source.ID == 0 || installed[0].Source.ID != installed[1].Source.ID {
		t.Fatalf("source IDs = %d and %d, want one persisted Source", installed[0].Source.ID, installed[1].Source.ID)
	}
	collections, err := lib.ListCollections(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(collections) != 1 || !reflect.DeepEqual(summaryNames(collections[0].Skills), []string{"first", "second"}) {
		t.Fatalf("collections = %#v, want both remote skills grouped by Source", collections)
	}
	group, err := lib.CreateGroup(t.Context(), "remote group")
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.AddSkillToGroup(t.Context(), group.ID, installed[0].ID); err != nil {
		t.Fatal(err)
	}
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.Resolve(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.ActivateGroup(t.Context(), group.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := lib.RemoveSkill(t.Context(), installed[0].ID, library.RemoveSkillOptions{ProjectedCopies: library.Keep}); err != nil {
		t.Fatal(err)
	}
	projections, err := lib.ListProjections(t.Context())
	if err != nil || len(projections) != 0 {
		t.Fatalf("activations after remote removal = %#v, %v; want none", projections, err)
	}
	if _, err := os.Stat(filepath.Join(directory, installed[0].Name, "SKILL.md")); err != nil {
		t.Fatalf("explicit Keep removed remote projection: %v", err)
	}
	remaining, err := lib.ListSkills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := summaryNames(remaining), []string{"second"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("skills after remote removal = %v, want %v", got, want)
	}
}

func TestInstallFetchGitHubSelectionAndUpdateOnlyWhenContentChanges(t *testing.T) {
	repository := t.TempDir()
	runGit(t, repository, "init", "-b", "main")
	writeFile(t, filepath.Join(repository, "first", "SKILL.md"), []byte(skillDocument("first", "first\n")), 0o644)
	writeFile(t, filepath.Join(repository, "second", "SKILL.md"), []byte(skillDocument("second", "second\n")), 0o644)
	commitGit(t, repository, "initial")

	lib := openTestLibrary(t)
	ctx := t.Context()
	fetch, err := lib.Fetch(ctx, library.SourceSpec{
		Type: library.SourceGitHub, Locator: library.SourceLocator{Repository: repository, Branch: "main"},
	})
	if err != nil {
		t.Fatal(err)
	}
	installed, err := lib.InstallFetch(ctx, fetch.ID, []string{"first"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := skillNames(installed), []string{"first"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("installed skills = %v, want %v", got, want)
	}

	writeFile(t, filepath.Join(repository, "second", "SKILL.md"), []byte(skillDocument("second", "unrelated\n")), 0o644)
	commitGit(t, repository, "unrelated skill")
	if available, err := checkSingleUpdate(t, lib, ctx, installed[0].ID); err != nil || available {
		t.Fatalf("unrelated commit CheckUpdates = %v, %v", available, err)
	}
	writeFile(t, filepath.Join(repository, "first", "SKILL.md"), []byte(skillDocument("first", "updated\n")), 0o644)
	commitGit(t, repository, "update")
	if available, err := checkSingleUpdate(t, lib, ctx, installed[0].ID); err != nil || !available {
		t.Fatalf("changed skill CheckUpdates = %v, %v", available, err)
	}
	updated, err := lib.Update(ctx, installed[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(updated.Skill.Files[0].Content); got != skillDocument("first", "updated\n") {
		t.Fatalf("updated content = %q, want updated", got)
	}
	if updated.Skill.Source.LastSeenRef == "" || updated.Skill.Source.LastSeenRef == installed[0].Source.LastSeenRef {
		t.Fatalf("updated source ref = %q, want new git commit", updated.Skill.Source.LastSeenRef)
	}
	persisted, err := lib.GetSkill(ctx, installed[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Source.LastSeenRef != updated.Skill.Source.LastSeenRef {
		t.Fatalf("persisted source ref = %q, want %q", persisted.Source.LastSeenRef, updated.Skill.Source.LastSeenRef)
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

func TestInstallFetchStoreUsesPackageVersionAndSelection(t *testing.T) {
	archive := zipArchive(t, map[string]string{
		"first/SKILL.md":  skillDocument("first", "# First\n"),
		"second/SKILL.md": skillDocument("second", "# Second\n"),
	})
	var requestedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requestedPath = request.URL.EscapedPath()
		_, _ = w.Write(archive)
	}))
	t.Cleanup(server.Close)
	t.Setenv("SKILLS_STORE_URL", server.URL)

	lib := openTestLibrary(t)
	fetch, err := lib.Fetch(t.Context(), library.SourceSpec{
		Type:    library.SourceStore,
		Locator: library.SourceLocator{PackageID: "owner/package", Version: "1.2.3 beta"},
	})
	if err != nil {
		t.Fatal(err)
	}
	installed, err := lib.InstallFetch(t.Context(), fetch.ID, []string{"second"})
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

func TestInstallFetchStoreDoesNotRefetchAfterStaging(t *testing.T) {
	archive := zipArchive(t, map[string]string{
		"first/SKILL.md":  skillDocument("first", "# First\n"),
		"second/SKILL.md": skillDocument("second", "# Second\n"),
	})
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write(archive)
	}))
	t.Cleanup(server.Close)
	t.Setenv("SKILLS_STORE_URL", server.URL)

	lib := openTestLibrary(t)
	ctx := t.Context()
	fetch, err := lib.Fetch(ctx, library.SourceSpec{
		Type:    library.SourceStore,
		Locator: library.SourceLocator{PackageID: "owner/package", Version: "1.0.0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	server.Close()
	installed, err := lib.InstallFetch(ctx, fetch.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatalf("remote requests = %d, want Fetch only", requests.Load())
	}
	if got, want := skillNames(installed), []string{"first", "second"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("installed skills = %v, want %v", got, want)
	}
}

func TestInstallFetchPersistsTypedSourceAndSharesIt(t *testing.T) {
	archive := zipArchive(t, map[string]string{
		"first/SKILL.md":  skillDocument("first", "# First\n"),
		"second/SKILL.md": skillDocument("second", "# Second\n"),
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	t.Cleanup(server.Close)
	lib := openTestLibrary(t)
	ctx := t.Context()

	fetch, err := lib.Fetch(ctx, library.SourceSpec{Type: library.SourceURL, Locator: library.SourceLocator{URL: server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	installed, err := lib.InstallFetch(ctx, fetch.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(installed) != 2 {
		t.Fatalf("installed = %#v, want two skills", installed)
	}
	first, second := installed[0], installed[1]
	if first.Source.ID != second.Source.ID {
		t.Fatalf("source IDs = %d and %d, want one shared Source", first.Source.ID, second.Source.ID)
	}
	got, err := lib.GetSkill(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, first) {
		t.Fatalf("GetSkill() = %#v, want %#v", got, first)
	}
	if got.Source.ID == 0 || got.Source.Type != library.SourceURL || got.Source.Locator.URL != server.URL {
		t.Fatalf("persisted Source = %#v, want typed url source", got.Source)
	}
	if got.Source.LastSeenRef == "" || got.Source.LastSeenRef != fetch.Ref {
		t.Fatalf("persisted ref = %q, want fetch ref %q", got.Source.LastSeenRef, fetch.Ref)
	}
	if got.Source.LastFetched.IsZero() {
		t.Fatal("persisted source has zero LastFetched")
	}
	collections, err := lib.ListCollections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(collections) != 1 || !reflect.DeepEqual(summaryIDs(collections[0].Skills), []library.SkillID{first.ID, second.ID}) {
		t.Fatalf("ListCollections() = %#v, want both skills in one collection", collections)
	}
}
