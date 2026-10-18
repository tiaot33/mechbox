package library_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tiaot33/mechbox/internal/library"
)

// 检查更新

func TestCheckUpdatesIsReadOnlyAndUpdateIsLibraryOnly(t *testing.T) {
	ctx := t.Context()
	repository := t.TempDir()
	runGit(t, repository, "init", "-b", "main")
	writeSource := func(first, second string) {
		t.Helper()
		writeFile(t, filepath.Join(repository, "first", "SKILL.md"), []byte(skillDocument("first", first)), 0o644)
		writeFile(t, filepath.Join(repository, "second", "SKILL.md"), []byte(skillDocument("second", second)), 0o644)
		commitGit(t, repository, "source state")
	}
	writeSource("old", "old")
	t.Setenv("HOME", t.TempDir())
	serveGitHubCloneOnly(t, repository)
	lib := openTestLibrary(t)
	preview, err := lib.PreviewFetch(ctx, gitHubRequest())
	if err != nil {
		t.Fatal(err)
	}
	skills, err := lib.Fetch(ctx, preview.ID, candidateNames(preview))
	if err != nil {
		t.Fatal(err)
	}
	first, second := skills[0], skills[1]
	scopes := []library.Scope{
		library.GlobalScope(library.AgentCodex),
		library.GlobalScope(library.AgentClaudeCode),
	}
	directories := make([]string, len(scopes))
	for i, scope := range scopes {
		if directories[i], err = lib.ScopeDirectory(ctx, scope); err != nil {
			t.Fatal(err)
		}
		if err := lib.Install(ctx, first.ID, scope, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	skillScopes, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{})
	if err != nil {
		t.Fatal(err)
	}
	check := func(id library.SkillID, want library.UpdateCheckStatus) {
		t.Helper()
		got, err := checkSkillUpdate(t, lib, ctx, id)
		if err != nil || got != want {
			t.Fatalf("check %d = %v, %v; want %v", id, got, err, want)
		}
	}
	check(first.ID, library.UpdateCurrent)
	writeSource("new", "old")
	check(first.ID, library.UpdateAvailable)
	check(first.ID, library.UpdateAvailable)
	check(second.ID, library.UpdateCurrent)
	current, err := lib.GetSkill(ctx, first.ID)
	if err != nil || current.LastUpdateCheck == nil || current.LastUpdateCheck.Status != library.UpdateAvailable {
		t.Fatalf("check result on skill: %#v, %v", current, err)
	}
	current.LastUpdateCheck = nil
	if !reflect.DeepEqual(current, first) {
		t.Fatalf("check mutated skill: %#v, %v", current, err)
	}
	versions, err := lib.ListVersions(ctx, first.ID)
	if err != nil || len(versions) != 1 {
		t.Fatalf("check created version: %v, %v", versions, err)
	}
	// 只勾一个技能更新后,同一集合里没勾的技能照样报告有新版本。
	writeSource("new", "new")
	result, err := updateSkill(t, lib, ctx, first.ID)
	if err != nil || !result.Changed || string(skillFiles(t, lib, result.Skill)[0].Content) != skillDocument("first", "new") {
		t.Fatalf("Update = %#v, %v", result, err)
	}
	check(first.ID, library.UpdateCurrent)
	check(second.ID, library.UpdateAvailable)
	result, err = updateSkill(t, lib, ctx, first.ID)
	if err != nil || result.Changed || result.PreviousVersionID != result.Skill.VersionID {
		t.Fatalf("unchanged Update = %#v, %v", result, err)
	}
	versions, err = lib.ListVersions(ctx, first.ID)
	if err != nil || len(versions) != 2 {
		t.Fatalf("versions = %v, %v", versions, err)
	}
	for _, directory := range directories {
		content, err := os.ReadFile(filepath.Join(directory, first.Name, "SKILL.md"))
		if err != nil || string(content) != skillDocument("first", "old") {
			t.Fatalf("skill scope changed: %q, %v", content, err)
		}
	}
	after, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{})
	if err != nil || !reflect.DeepEqual(after, skillScopes) {
		t.Fatalf("skill scope records changed: %v, %v", after, err)
	}
	// 来源里已经没有这个技能:检查记为来源里已删除,更新报 NotFound。
	if err := os.RemoveAll(filepath.Join(repository, "first")); err != nil {
		t.Fatal(err)
	}
	commitGit(t, repository, "remove first")
	check(first.ID, library.UpdateDeleted)
	_, err = lib.Update(ctx, mustPlanUpdate(t, lib, first.ID))
	requireErrorDetails(t, err, library.NotFound, library.ErrorDetails{SkillID: first.ID, Name: first.Name})
	// 来源整个读不到:检查失败,不算来源里已删除。
	if err := os.RemoveAll(repository); err != nil {
		t.Fatal(err)
	}
	if status, err := checkSkillUpdate(t, lib, ctx, first.ID); !library.IsCode(err, library.SourceUnreachable) || status != library.UpdateFailed {
		t.Fatalf("unreachable check: %v, %#v", status, err)
	}
	if _, err := updateSkill(t, lib, ctx, first.ID); !library.IsCode(err, library.SourceUnreachable) {
		t.Fatalf("unreachable update: %v", err)
	}
	versions, err = lib.ListVersions(ctx, first.ID)
	if err != nil || len(versions) != 2 {
		t.Fatalf("failed operations changed history: %v, %v", versions, err)
	}
}

func TestLocalCheckUpdatesDetectsContentAndMode(t *testing.T) {
	ctx := t.Context()
	lib := openTestLibrary(t)
	source := filepath.Join(t.TempDir(), "local")
	path := filepath.Join(source, "SKILL.md")
	writeLocalSkill(t, source, "old")
	skill, err := fetchLocalDir(ctx, lib, source)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"unchanged", "content", "mode"} {
		t.Run(change, func(t *testing.T) {
			switch change {
			case "content":
				writeLocalSkill(t, source, "new")
			case "mode":
				if err := os.Chmod(path, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			want := change != "unchanged"
			status, err := checkSkillUpdate(t, lib, ctx, skill.ID)
			if err != nil || (status == library.UpdateAvailable) != want {
				t.Fatalf("CheckUpdates = %v, %v", status, err)
			}
			result, err := updateSkill(t, lib, ctx, skill.ID)
			if err != nil || result.Changed != want {
				t.Fatalf("Update = %#v, %v", result, err)
			}
			if status, err = checkSkillUpdate(t, lib, ctx, skill.ID); err != nil || status != library.UpdateCurrent {
				t.Fatalf("CheckUpdates after update = %v, %v", status, err)
			}
		})
	}
	versions, err := lib.ListVersions(ctx, skill.ID)
	if err != nil || len(versions) != 3 {
		t.Fatalf("versions = %v, %v", versions, err)
	}
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	if status, err := checkSkillUpdate(t, lib, ctx, skill.ID); !library.IsCode(err, library.SourceUnreachable) || status != library.UpdateFailed {
		t.Fatalf("missing local source: %v, %v", status, err)
	}
}

func TestCheckUpdatesReadsEachCollectionOnceAndKeepsCollectionsIndependent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	archive := zipArchive(t, map[string]string{"first/SKILL.md": skillDocument("first", "old"), "second/SKILL.md": skillDocument("second", "old")})
	failed := false
	requests := map[string]int{}
	client := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: fetchTransport(func(r *http.Request) (*http.Response, error) {
		requests[r.URL.Path]++
		if failed && r.URL.Path == "/broken" {
			return nil, errors.New("offline")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(archive))}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = client })
	var skills []library.Skill
	for _, suffix := range []string{"/good", "/broken"} {
		preview, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: "https://source.example" + suffix})
		if err != nil {
			t.Fatal(err)
		}
		fetched, err := lib.Fetch(t.Context(), preview.ID, candidateNames(preview))
		if err != nil {
			t.Fatal(err)
		}
		skills = append(skills, fetched...)
	}
	good, broken := skills[0].CollectionID, skills[2].CollectionID
	scope := library.GlobalScope(library.AgentCodex)
	if err := lib.Install(t.Context(), skills[0].ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	before, err := lib.ListSkills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	skillScope, err := lib.ListSkillScopes(t.Context(), library.SkillScopeFilter{})
	if err != nil {
		t.Fatal(err)
	}
	archive = zipArchive(t, map[string]string{"first/SKILL.md": skillDocument("first", "new"), "second/SKILL.md": skillDocument("second", "old")})
	failed = true
	clear(requests)
	results, err := lib.CheckUpdates(t.Context(), []library.CollectionID{broken, good, 0, good, 99999})
	if err != nil || len(results) != 4 {
		t.Fatalf("checks = %#v, %v", results, err)
	}
	for i, id := range []library.CollectionID{broken, good, 0, 99999} {
		if results[i].CollectionID != id {
			t.Fatalf("order = %#v", results)
		}
	}
	if requests["/good"] != 1 {
		t.Fatalf("good source read %d times; want once", requests["/good"])
	}
	statuses := func(check library.CollectionUpdateCheck) []library.UpdateCheckStatus {
		var got []library.UpdateCheckStatus
		for _, skill := range check.Skills {
			got = append(got, skill.LastUpdateCheck.Status)
		}
		return got
	}
	if !library.IsCode(results[0].Error, library.SourceUnreachable) || !reflect.DeepEqual(statuses(results[0]), []library.UpdateCheckStatus{library.UpdateFailed, library.UpdateFailed}) {
		t.Fatalf("broken = %#v", results[0])
	}
	if results[1].Error != nil || results[1].Source.Label != "https://source.example/good" || !reflect.DeepEqual(statuses(results[1]), []library.UpdateCheckStatus{library.UpdateAvailable, library.UpdateCurrent}) {
		t.Fatalf("good = %#v", results[1])
	}
	requireErrorDetails(t, results[2].Error, library.InvalidArgument, library.ErrorDetails{Field: "collections"})
	if !library.IsCode(results[3].Error, library.NotFound) || results[3].Skills != nil {
		t.Fatalf("missing = %#v", results[3])
	}
	after, err := lib.ListSkills(t.Context())
	for i := range after {
		after[i].LastUpdateCheck = nil // 检查只改检查结论,技能与来源的其他信息保持不变。
	}
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("mutated Library = %#v, %v", after, err)
	}
	afterSkillScopes, err := lib.ListSkillScopes(t.Context(), library.SkillScopeFilter{})
	if err != nil || !reflect.DeepEqual(skillScope, afterSkillScopes) {
		t.Fatalf("mutated skill scopes = %#v, %v", afterSkillScopes, err)
	}
	for _, original := range skills {
		got, err := lib.GetSkill(t.Context(), original.ID)
		if err != nil || !reflect.DeepEqual(skillFiles(t, lib, got), skillFiles(t, lib, original)) {
			t.Fatalf("mutated content = %#v, %v", got, err)
		}
		versions, err := lib.ListVersions(t.Context(), original.ID)
		if err != nil || len(versions) != 1 {
			t.Fatalf("mutated history = %#v, %v", versions, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := lib.CheckUpdates(ctx, []library.CollectionID{good}); !library.IsCode(err, library.Canceled) || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel = %v", err)
	}
	if results, err := lib.CheckUpdates(t.Context(), nil); err != nil || len(results) != 0 {
		t.Fatalf("empty = %#v, %v", results, err)
	}
	// 来源读到了但里面没有 first:first 记为来源里已删除,不算检查失败。
	archive = zipArchive(t, map[string]string{"second/SKILL.md": skillDocument("second", "old")})
	results, err = lib.CheckUpdates(t.Context(), []library.CollectionID{good})
	if err != nil || len(results) != 1 || results[0].Error != nil || !reflect.DeepEqual(statuses(results[0]), []library.UpdateCheckStatus{library.UpdateDeleted, library.UpdateCurrent}) {
		t.Fatalf("removed from source = %#v, %v", results, err)
	}
}

func TestCheckUpdatesSeparatesRefsAndDetectsOnlyExecutableChanges(t *testing.T) {
	repository, _ := githubRemote(t)
	runGit(t, repository, "tag", "fixed")
	lib := openTestLibrary(t)
	var skills []library.Skill
	for _, ref := range []string{"trunk", "fixed"} {
		preview, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: "owner/repo", Ref: ref})
		if err != nil {
			t.Fatal(err)
		}
		fetched, err := lib.Fetch(t.Context(), preview.ID, candidateNames(preview))
		if err != nil {
			t.Fatal(err)
		}
		skills = append(skills, fetched...)
	}
	local := filepath.Join(t.TempDir(), "local")
	writeLocalSkill(t, local, "local content")
	skill, err := fetchLocalDir(t.Context(), lib, local)
	if err != nil {
		t.Fatal(err)
	}
	skills = append(skills, skill)
	for _, root := range []string{repository, local} {
		if err := os.Chmod(filepath.Join(root, "SKILL.md"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	commitGit(t, repository, "executable mode")
	results, err := lib.CheckUpdates(t.Context(), []library.CollectionID{skills[0].CollectionID, skills[1].CollectionID, skills[2].CollectionID})
	if err != nil || len(results) != 3 {
		t.Fatalf("results = %#v, %v", results, err)
	}
	for i, want := range []library.UpdateCheckStatus{library.UpdateAvailable, library.UpdateCurrent, library.UpdateAvailable} {
		if results[i].Error != nil || len(results[i].Skills) != 1 || results[i].Skills[0].LastUpdateCheck.Status != want {
			t.Fatalf("ref and mode = %#v", results)
		}
	}
	for _, skill := range skills {
		if versions, err := lib.ListVersions(t.Context(), skill.ID); err != nil || len(versions) != 1 {
			t.Fatalf("history = %#v, %v", versions, err)
		}
	}
}

func TestCheckUpdatesCancellationReturnsOnlyCompletedCollections(t *testing.T) {
	lib := openTestLibrary(t)
	archive := zipArchive(t, map[string]string{"first/SKILL.md": skillDocument("first", "old"), "second/SKILL.md": skillDocument("second", "old")})
	spec := serveArchive(t, archive)
	first, err := lib.PreviewFetch(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	a, err := lib.Fetch(t.Context(), first.ID, candidateNames(first))
	if err != nil {
		t.Fatal(err)
	}
	spec.Input += "?second"
	second, err := lib.PreviewFetch(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	b, err := lib.Fetch(t.Context(), second.ID, candidateNames(second))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	transport := http.DefaultClient.Transport
	http.DefaultClient.Transport = fetchTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.RawQuery == "second" {
			cancel()
			return nil, r.Context().Err()
		}
		return transport.RoundTrip(r)
	})
	results, err := lib.CheckUpdates(ctx, []library.CollectionID{a[0].CollectionID, b[0].CollectionID})
	if !errors.Is(err, context.Canceled) || len(results) != 1 || results[0].CollectionID != a[0].CollectionID || len(results[0].Skills) != 2 {
		t.Fatalf("canceled checks = %#v, %v", results, err)
	}
	for _, skill := range a {
		if state := updateCheckState(t, lib, skill.ID); state == nil || state.Status != library.UpdateCurrent {
			t.Fatalf("completed check = %+v", state)
		}
	}
	for _, skill := range b {
		if state := updateCheckState(t, lib, skill.ID); state != nil {
			t.Fatalf("canceled check = %+v", state)
		}
	}
}

// 结论

func TestUpdateChecksReplaceEarlierConclusionsAndRemovalStartsUnchecked(t *testing.T) {
	lib := openTestLibrary(t)
	skill, source := fetchLocalSkill(t, lib, "example", "old")
	for _, status := range []library.UpdateCheckStatus{library.UpdateCurrent, library.UpdateAvailable, library.UpdateFailed, library.UpdateCurrent} {
		before := time.Now()
		switch status {
		case library.UpdateAvailable:
			writeLocalSkill(t, source, "new")
		case library.UpdateFailed:
			if err := os.Rename(source, source+"-offline"); err != nil {
				t.Fatal(err)
			}
		case library.UpdateCurrent:
			writeLocalSkill(t, source, "old")
		}
		got, err := checkSkillUpdate(t, lib, t.Context(), skill.ID)
		if got != status || (err != nil) != (status == library.UpdateFailed) {
			t.Fatalf("check = %v, %v; want %s", got, err, status)
		}
		state := updateCheckState(t, lib, skill.ID)
		if state == nil || state.Status != status || state.CheckedAt.Before(before) {
			t.Fatalf("state = %+v, want %s", state, status)
		}
	}
	if err := lib.RemoveSkill(t.Context(), skill.ID, library.RemoveSkillOptions{}); err != nil {
		t.Fatal(err)
	}
	if skills, err := lib.ListSkills(t.Context()); err != nil || len(skills) != 0 {
		t.Fatalf("after removal = %+v, %v", skills, err)
	}
	skill, err := fetchLocalDir(t.Context(), lib, source)
	if err != nil {
		t.Fatal(err)
	}
	if got := updateCheckState(t, lib, skill.ID); got != nil {
		t.Fatalf("refetched check = %+v", got)
	}
}

func TestUpdateCheckSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")
	lib, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
	unchecked := fetchNamedSkill(t, lib, "unchecked")
	current := fetchNamedSkill(t, lib, "current")
	failed, offline := fetchLocalSkill(t, lib, "failed", "old")
	if err := os.Rename(offline, offline+"-offline"); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	writeLocalSkill(t, filepath.Join(root, "kept"), "keep")
	writeLocalSkill(t, filepath.Join(root, "deleted"), "gone")
	preview, err := lib.PreviewFetch(t.Context(), localRequest(root))
	if err != nil {
		t.Fatal(err)
	}
	pair := fetchSkills(t, lib, preview, []string{"deleted", "kept"})
	if err := os.RemoveAll(filepath.Join(root, "deleted")); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.CheckUpdates(t.Context(), []library.CollectionID{current.CollectionID, failed.CollectionID, pair[0].CollectionID}); err != nil {
		t.Fatal(err)
	}
	want := map[library.SkillID]*library.UpdateCheck{unchecked.ID: nil}
	for id, status := range map[library.SkillID]library.UpdateCheckStatus{current.ID: library.UpdateCurrent, failed.ID: library.UpdateFailed, pair[0].ID: library.UpdateDeleted, pair[1].ID: library.UpdateCurrent} {
		state := updateCheckState(t, lib, id)
		if state == nil || state.Status != status {
			t.Fatalf("skill %d = %+v; want %s", id, state, status)
		}
		want[id] = state
	}
	skill, source := fetchLocalSkill(t, lib, "example", "old")
	writeLocalSkill(t, source, "new")
	start := time.Now()
	if status, err := checkSkillUpdate(t, lib, t.Context(), skill.ID); err != nil || status != library.UpdateAvailable {
		t.Fatalf("check = %v, %v", status, err)
	}
	want[skill.ID] = updateCheckState(t, lib, skill.ID)
	if want[skill.ID].CheckedAt.Before(start) || want[skill.ID].CheckedAt.After(time.Now()) {
		t.Fatalf("state = %+v", want[skill.ID])
	}
	if err := lib.Close(); err != nil {
		t.Fatal(err)
	}
	lib, err = library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for id, state := range want {
		if got := updateCheckState(t, lib, id); !reflect.DeepEqual(got, state) {
			t.Fatalf("reopened %d = %+v, want %+v", id, got, state)
		}
	}
}

func TestCheckDuringUpdateGivesTheUpdatedConclusion(t *testing.T) {
	for _, name := range []string{"changed", "unchanged"} {
		changed := name == "changed"
		t.Run(name, func(t *testing.T) {
			lib := openTestLibrary(t)
			gate := newNetworkGate()
			old := zipArchive(t, map[string]string{"demo/SKILL.md": skillDocument("demo", "old")})
			var archive atomic.Pointer[[]byte]
			archive.Store(&old)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				snapshot := archive.Load()
				gate.pass()
				_, _ = w.Write(*snapshot)
			}))
			t.Cleanup(server.Close)
			skill := fetchURLSkill(t, lib, server.URL, "demo")
			if _, err := checkSkillUpdate(t, lib, t.Context(), skill.ID); err != nil {
				t.Fatal(err)
			}
			latest := zipArchive(t, map[string]string{"demo/SKILL.md": skillDocument("demo", "new")})
			archive.Store(&latest)
			gate.arm()
			var wg sync.WaitGroup
			var checks []library.CollectionUpdateCheck
			var checkErr error
			wg.Go(func() { checks, checkErr = lib.CheckUpdates(t.Context(), []library.CollectionID{skill.CollectionID}) })
			gate.awaitEntered(t)
			gate.disarm()
			if !changed {
				archive.Store(&old)
			}
			_, err := lib.Update(t.Context(), mustPlanUpdate(t, lib, skill.ID))
			gate.open()
			wg.Wait()
			if err != nil {
				t.Fatal(err)
			}
			// 检查读到的是更新前的来源,结论却以更新写下的已是最新为准,也不报错。
			if checkErr != nil || len(checks) != 1 || checks[0].Error != nil || len(checks[0].Skills) != 1 || checks[0].Skills[0].LastUpdateCheck.Status != library.UpdateCurrent {
				t.Fatalf("racing check = %+v, %v", checks, checkErr)
			}
			if got := updateCheckState(t, lib, skill.ID); !reflect.DeepEqual(got, checks[0].Skills[0].LastUpdateCheck) {
				t.Fatalf("stored state = %+v", got)
			}
		})
	}
}

func TestOverlappingCheckSuccessReplacesConcurrentFailure(t *testing.T) {
	lib := openTestLibrary(t)
	gate := newNetworkGate()
	var failing atomic.Bool
	archive := zipArchive(t, map[string]string{"demo/SKILL.md": skillDocument("demo", "old")})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failing.Load() {
			http.Error(w, "offline", http.StatusServiceUnavailable)
			return
		}
		snapshot := archive
		gate.pass()
		_, _ = w.Write(snapshot)
	}))
	t.Cleanup(server.Close)
	skill := fetchURLSkill(t, lib, server.URL, "demo")
	archive = zipArchive(t, map[string]string{"demo/SKILL.md": skillDocument("demo", "new")})
	gate.arm()
	var wg sync.WaitGroup
	var checks []library.CollectionUpdateCheck
	var checkErr error
	wg.Go(func() { checks, checkErr = lib.CheckUpdates(t.Context(), []library.CollectionID{skill.CollectionID}) })
	gate.awaitEntered(t)
	gate.disarm()
	failing.Store(true)
	if status, err := checkSkillUpdate(t, lib, t.Context(), skill.ID); err == nil || status != library.UpdateFailed {
		t.Fatalf("concurrent check = %v, %v; want failed", status, err)
	}
	gate.open()
	wg.Wait()
	if checkErr != nil || len(checks) != 1 || checks[0].Error != nil || len(checks[0].Skills) != 1 || checks[0].Skills[0].LastUpdateCheck.Status != library.UpdateAvailable {
		t.Fatalf("overlapping check = %+v, %v", checks, checkErr)
	}
	if got := updateCheckState(t, lib, skill.ID); got == nil || got.Status != library.UpdateAvailable {
		t.Fatalf("state = %+v", got)
	}
}

func TestLateCheckDoesNotWriteToTheSameSkillFetchedAgain(t *testing.T) {
	ctx := t.Context()
	lib := openTestLibrary(t)
	gate := newNetworkGate()
	var mu sync.Mutex
	body := "v1"
	server := serveGatedArchive(t, gate, func() []byte {
		mu.Lock()
		defer mu.Unlock()
		return zipArchive(t, map[string]string{"a/SKILL.md": skillDocument("a", body)})
	})
	a := fetchURLSkill(t, lib, server.URL, "a")
	mu.Lock()
	body = "v2"
	mu.Unlock()
	gate.arm()
	var wg sync.WaitGroup
	wg.Go(func() { _, _ = lib.CheckUpdates(ctx, []library.CollectionID{a.CollectionID}) })
	gate.awaitEntered(t)
	gate.disarm()
	if err := lib.RemoveSkill(ctx, a.ID, library.RemoveSkillOptions{}); err != nil {
		t.Fatal(err)
	}
	again := fetchURLSkill(t, lib, server.URL, "a")
	if again.ID == a.ID {
		t.Fatalf("fetched again with the removed skill's ID %d", a.ID)
	}
	gate.open()
	wg.Wait()
	if skill, err := lib.GetSkill(ctx, again.ID); err != nil || skill.LastUpdateCheck != nil {
		t.Fatalf("skill fetched again after the late check = %#v, %v; want no conclusion", skill.LastUpdateCheck, err)
	}
}
