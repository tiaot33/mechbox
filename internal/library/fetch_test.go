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

func TestFetchKeepsCandidateDescription(t *testing.T) {
	for _, source := range []struct {
		name string
		spec func(*testing.T) library.PreviewFetchRequest
	}{
		{"github", func(t *testing.T) library.PreviewFetchRequest {
			repository := t.TempDir()
			runGit(t, repository, "init", "-b", "main")
			writeFile(t, filepath.Join(repository, "useful", "SKILL.md"), []byte("---\nname: useful\ndescription: helps a lot\n---\nbody\n"), 0o644)
			commitGit(t, repository, "initial")
			return gitHubCloneRequest(t, repository)
		}},
		{"url", func(t *testing.T) library.PreviewFetchRequest {
			return serveArchive(t, zipArchive(t, map[string]string{"useful/SKILL.md": "---\nname: useful\ndescription: helps a lot\n---\nbody\n"}))
		}},
	} {
		t.Run(source.name, func(t *testing.T) {
			lib := openTestLibrary(t)
			ctx := t.Context()
			preview, err := lib.PreviewFetch(ctx, source.spec(t))
			if err != nil {
				t.Fatal(err)
			}
			if len(preview.Candidates) != 1 || preview.Candidates[0].Description != "helps a lot" {
				t.Fatalf("PreviewFetch = %#v", preview)
			}
			description := preview.Candidates[0].Description
			preview.Candidates[0].Description = "changed by caller"
			fetched, err := lib.Fetch(ctx, preview.ID, candidateNames(preview))
			if err != nil || len(fetched) != 1 || fetched[0].Description != "helps a lot" {
				t.Fatalf("Fetch = %#v, %v", fetched, err)
			}
			summaries, err := lib.ListSkills(ctx)
			if err != nil || len(summaries) != 1 || summaries[0].Description != description {
				t.Fatalf("ListSkills description = %#v, %v; want %q", summaries, err, description)
			}
		})
	}
}

func TestFetchFailureKeepsHandleAndLibraryUnchanged(t *testing.T) {
	repository := previewRepository(t)
	lib, err := library.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
	ctx := t.Context()
	preview, err := lib.PreviewFetch(ctx, gitHubCloneRequest(t, repository))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(repository); err != nil {
		t.Fatal(err)
	}
	if fetched, err := lib.Fetch(ctx, preview.ID, []string{"first", "missing"}); err == nil || len(fetched) != 0 {
		t.Fatalf("unknown selection = %v, %v; want no results and error", fetched, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if fetched, err := lib.Fetch(canceled, preview.ID, candidateNames(preview)); !library.IsCode(err, library.Canceled) || !errors.Is(err, context.Canceled) || len(fetched) != 0 {
		t.Fatalf("canceled fetch = %v, %v; want no results and Canceled", fetched, err)
	}
	collections, err := lib.ListCollections(ctx)
	if err != nil || len(collections) != 0 {
		t.Fatalf("failed attempts left collections: %v, %v", collections, err)
	}
	// 不指定名字不再表示全部入库;句柄保留,可按候选名重试。
	_, err = lib.Fetch(ctx, preview.ID, nil)
	requireErrorDetails(t, err, library.InvalidArgument, library.ErrorDetails{Field: "names"})
	fetched, err := lib.Fetch(ctx, preview.ID, candidateNames(preview))
	if err != nil || len(fetched) != 2 || fetched[0].Name != "first" || fetched[1].Name != "second" {
		t.Fatalf("offline retry with all candidates = %v, %v; want both Skills", fetched, err)
	}
	if _, err := lib.Fetch(ctx, preview.ID, []string{"second"}); !library.IsCode(err, library.NotFound) {
		t.Fatalf("reused handle after success: %v; want NotFound", err)
	}
}

func TestFetchConcurrentCallsConsumeHandleOnce(t *testing.T) {
	repository := previewRepository(t)
	// 每轮使用独立的库,两个调用选择不同候选,避免唯一约束掩盖重复消费。
	for range 10 {
		lib, err := library.Open(filepath.Join(t.TempDir(), "library.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = lib.Close() })
		ctx := t.Context()
		preview, err := lib.PreviewFetch(ctx, gitHubCloneRequest(t, repository))
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		results := make(chan error, 2)
		for _, name := range preview.Candidates {
			go func() {
				<-start
				_, err := lib.Fetch(ctx, preview.ID, []string{name.Name})
				results <- err
			}()
		}
		close(start)
		// 输家要么撞上"正在入库"(FailedPrecondition),要么在赢家提交后才到、句柄已丢弃(NotFound)。
		succeeded := 0
		for range 2 {
			if err := <-results; err == nil {
				succeeded++
			} else if !library.IsCode(err, library.FailedPrecondition) && !library.IsCode(err, library.NotFound) {
				t.Errorf("concurrent fetch error = %v, want FailedPrecondition or NotFound", err)
			}
		}
		if succeeded != 1 {
			t.Fatalf("successful consumers = %d, want 1", succeeded)
		}
		skills, err := lib.ListSkills(ctx)
		if err != nil || len(skills) != 1 {
			t.Fatalf("concurrent fetches left %v, %v; want one Skill", skills, err)
		}
	}
}

func previewRepository(t *testing.T) string {
	t.Helper()
	repository := t.TempDir()
	runGit(t, repository, "init", "-b", "main")
	writeFile(t, filepath.Join(repository, "first", "SKILL.md"), []byte(skillDocument("first", "first")), 0o644)
	writeFile(t, filepath.Join(repository, "second", "SKILL.md"), []byte(skillDocument("second", "second")), 0o644)
	commitGit(t, repository, "initial")
	return repository
}

func TestFetchIsAtomicAndDiscardsSuccessfulHandle(t *testing.T) {
	repository := previewRepository(t)
	lib, err := library.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
	ctx := t.Context()
	spec := gitHubCloneRequest(t, repository)

	pre, err := lib.PreviewFetch(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Fetch(ctx, pre.ID, []string{"second"}); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Fetch(ctx, pre.ID, []string{"first"}); !library.IsCode(err, library.NotFound) {
		t.Fatalf("reused preview handle error = %v, want NotFound", err)
	}
	before, err := lib.ListSkills(ctx)
	if err != nil {
		t.Fatal(err)
	}
	again, err := lib.PreviewFetch(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Fetch(ctx, again.ID, candidateNames(again)); !library.IsCode(err, library.AlreadyExists) {
		t.Fatalf("fetch existing candidate again = %v, want AlreadyExists", err)
	} else {
		requireErrorDetails(t, err, library.AlreadyExists, library.ErrorDetails{SkillID: before[0].ID, Name: "second"})
	}
	if after, err := lib.ListSkills(ctx); err != nil || len(after) != 1 || after[0].ID != before[0].ID {
		t.Fatalf("failed batch fetch changed library: %#v, %v", after, err)
	}
	versions, err := lib.ListVersions(ctx, before[0].ID)
	if err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(repository, "second", "SKILL.md"), []byte(skillDocument("second", "changed second")), 0o644)
	commitGit(t, repository, "changed")
	batch, err := lib.PreviewFetch(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	// first 会先写入事务,随后 second 与已有条目冲突;整批必须回滚。
	if fetched, err := lib.Fetch(ctx, batch.ID, []string{"first", "second"}); err == nil || len(fetched) != 0 {
		t.Fatalf("failed batch = %v, %v; want no results and error", fetched, err)
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
	if fetched, err := lib.Fetch(ctx, batch.ID, []string{"first"}); err != nil || len(fetched) != 1 {
		t.Fatalf("retry from the same preview = %v, %v", fetched, err)
	}
}

func TestFetchDefaultsToAllSkillsWithoutRereading(t *testing.T) {
	archive := zipArchive(t, map[string]string{
		"first/SKILL.md":  skillDocument("first", "# First\n"),
		"first/guide.md":  "guide\n",
		"second/SKILL.md": skillDocument("second", "# Second\n"),
	})
	var served atomic.Pointer[[]byte]
	served.Store(&archive)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(*served.Load())
	}))
	t.Cleanup(server.Close)

	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)

	ctx := t.Context()
	preview, err := lib.PreviewFetch(ctx, library.PreviewFetchRequest{URL: server.URL + "/skills.zip"})
	if err != nil {
		t.Fatal(err)
	}
	changed := zipArchive(t, map[string]string{"first/SKILL.md": skillDocument("first", "# Changed\n")})
	served.Store(&changed)
	fetched, err := getFetchedSkills(ctx, lib, preview, candidateNames(preview))
	if err != nil {
		t.Fatal(err)
	}
	if string(fetched[0].Files[0].Content) != skillDocument("first", "# First\n") {
		t.Fatalf("Fetch reread the remote: %q", fetched[0].Files[0].Content)
	}
	if got, want := skillNames(fetched), []string{"first", "second"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fetched skills = %v, want %v", got, want)
	}
	if fetched[0].Source.ID == 0 || fetched[0].Source.ID != fetched[1].Source.ID {
		t.Fatalf("source IDs = %d and %d, want one persisted Source", fetched[0].Source.ID, fetched[1].Source.ID)
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
	if _, err := lib.AddSkillToGroup(t.Context(), group.ID, fetched[0].ID); err != nil {
		t.Fatal(err)
	}
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.Resolve(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.InstallGroup(t.Context(), group.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := lib.RemoveSkill(t.Context(), fetched[0].ID, library.RemoveSkillOptions{Folders: library.Keep}); err != nil {
		t.Fatal(err)
	}
	skillScopes, err := lib.ListSkillScopes(t.Context())
	if err != nil || len(skillScopes) != 0 {
		t.Fatalf("skill scopes after remote removal = %#v, %v; want none", skillScopes, err)
	}
	if _, err := os.Stat(filepath.Join(directory, fetched[0].Name, "SKILL.md")); err != nil {
		t.Fatalf("explicit Keep removed remote skill scope: %v", err)
	}
	remaining, err := lib.ListSkills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := summaryNames(remaining), []string{"second"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("skills after remote removal = %v, want %v", got, want)
	}
}

func TestFetchPersistsTypedSourceAndSharesIt(t *testing.T) {
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

	preview, err := lib.PreviewFetch(ctx, library.PreviewFetchRequest{URL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	fetched, err := lib.Fetch(ctx, preview.ID, candidateNames(preview))
	if err != nil {
		t.Fatal(err)
	}
	if len(fetched) != 2 {
		t.Fatalf("fetched = %#v, want two skills", fetched)
	}
	first, second := fetched[0], fetched[1]
	if first.Source.ID != second.Source.ID {
		t.Fatalf("source IDs = %d and %d, want one shared Source", first.Source.ID, second.Source.ID)
	}
	// 入库结果与技能列表的条目形状一致,不带文件。
	if summaries, err := lib.ListSkills(ctx); err != nil || !reflect.DeepEqual(summaries, fetched) {
		t.Fatalf("ListSkills() = %#v, %v; want fetch result %#v", summaries, err, fetched)
	}
	got, err := lib.GetSkill(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Source.ID == 0 || got.Source.Type != library.SourceURL || got.Source.Locator.URL != server.URL {
		t.Fatalf("persisted Source = %#v, want typed url source", got.Source)
	}
	if got.Source.LastSeenRef == "" || got.Source.LastSeenRef != preview.Revision {
		t.Fatalf("persisted ref = %q, want preview ref %q", got.Source.LastSeenRef, preview.Revision)
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
