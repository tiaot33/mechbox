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

	"github.com/tiaot33/skills-tui/internal/library"
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
	_, err = lib.Update(ctx, singleUpdate(first))
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
		preview, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{URL: "https://source.example" + suffix})
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
		preview, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{GitHub: &library.GitHubInput{Input: "owner/repo", Ref: ref}})
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
	spec.URL += "?second"
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

// 更新

func TestUpdateCarriesSelectedSkillsToOneSourceRevision(t *testing.T) {
	ctx := t.Context()
	lib := openTestLibrary(t)
	server := newArchiveServer(t, zipArchive(t, map[string]string{
		"first/SKILL.md":  skillDocument("first", "old"),
		"second/SKILL.md": skillDocument("second", "old"),
		"third/SKILL.md":  skillDocument("third", "old"),
	}), "")
	var requests atomic.Int32
	transport := http.DefaultClient.Transport
	http.DefaultClient.Transport = fetchTransport(func(r *http.Request) (*http.Response, error) {
		requests.Add(1)
		return transport.RoundTrip(r)
	})
	skills := fetchArchive(t, lib, server.request())
	first, second, third := skills[0], skills[1], skills[2]
	server.serve(zipArchive(t, map[string]string{
		"first/SKILL.md":  skillDocument("first", "new"),
		"second/SKILL.md": skillDocument("second", "new"),
		"third/SKILL.md":  skillDocument("third", "new"),
	}), "")
	requests.Store(0)
	result, err := lib.Update(ctx, library.UpdateRequest{CollectionID: first.CollectionID, Skills: []library.SkillID{second.ID, first.ID, second.ID}})
	if err != nil || len(result.Skills) != 2 || result.Skills[0].Skill.ID != second.ID || result.Skills[1].Skill.ID != first.ID {
		t.Fatalf("Update = %#v, %v", result, err)
	}
	if requests.Load() != 1 {
		t.Fatalf("source read %d times; want once", requests.Load())
	}
	if result.Source != first.Source {
		t.Fatalf("result source = %#v; want %#v", result.Source, first.Source)
	}
	for i, before := range []library.Skill{second, first} {
		updated := result.Skills[i]
		if !updated.Changed || updated.PreviousVersionID != before.VersionID || updated.Skill.VersionID == before.VersionID {
			t.Fatalf("updated %s = %#v", before.Name, updated)
		}
		if updated.Skill.Revision == before.Revision || updated.Skill.Revision != result.Skills[0].Skill.Revision {
			t.Fatalf("revision of %s = %q; want the same new source revision", before.Name, updated.Skill.Revision)
		}
		// 入库后从未检查过的技能,更新后的结论也是已是最新。
		if check := updated.Skill.LastUpdateCheck; check == nil || check.Status != library.UpdateCurrent {
			t.Fatalf("conclusion of %s = %+v", before.Name, check)
		}
		// 更新前后的版本直接拿来比较。
		diff, err := lib.DiffVersions(ctx, updated.PreviousVersionID, updated.Skill.VersionID)
		if err != nil || !reflect.DeepEqual(diff.Changed, []string{"SKILL.md"}) {
			t.Fatalf("diff of %s = %#v, %v", before.Name, diff, err)
		}
		if previous, err := lib.GetVersion(ctx, updated.PreviousVersionID); err != nil || previous.Revision != before.Revision {
			t.Fatalf("previous version of %s = %#v, %v", before.Name, previous, err)
		}
	}
	// 没勾的技能完全不动。
	if got, err := lib.GetSkill(ctx, third.ID); err != nil || !reflect.DeepEqual(got, third) {
		t.Fatalf("unselected skill = %#v, %v; want %#v", got, err, third)
	}
}

func TestUpdateIsAllOrNothing(t *testing.T) {
	ctx := t.Context()
	lib := openTestLibrary(t)
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
	if _, err := lib.CheckUpdates(ctx, []library.CollectionID{first.CollectionID}); err != nil {
		t.Fatal(err)
	}
	before, err := lib.ListSkills(ctx)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "a", "SKILL.md"), []byte(skillDocument("first", "v2")), 0o644)
	if err := os.RemoveAll(filepath.Join(root, "b")); err != nil {
		t.Fatal(err)
	}
	_, err = lib.Update(ctx, library.UpdateRequest{CollectionID: first.CollectionID, Skills: []library.SkillID{first.ID, second.ID}})
	requireErrorDetails(t, err, library.NotFound, library.ErrorDetails{SkillID: second.ID, Name: second.Name})
	after, err := lib.ListSkills(ctx)
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("after failed update = %#v, %v; want %#v", after, err, before)
	}
	if versions, err := lib.ListVersions(ctx, first.ID); err != nil || len(versions) != 1 {
		t.Fatalf("first versions = %#v, %v", versions, err)
	}
}

// 事务里已写下前一个技能、后一个技能才发现读取期间被改过或被移除时,整批回滚:前一个技能的版本和结论都不变。
func TestUpdateRollsBackEarlierSkillsWhenALaterOneChangedDuringSourceRead(t *testing.T) {
	for _, race := range []string{"update", "remove"} {
		t.Run(race, func(t *testing.T) {
			ctx := t.Context()
			gate := newNetworkGate()
			var archive atomic.Pointer[[]byte]
			old := zipArchive(t, map[string]string{"first/SKILL.md": skillDocument("first", "old"), "second/SKILL.md": skillDocument("second", "old")})
			archive.Store(&old)
			server := serveGatedArchive(t, gate, func() []byte { return *archive.Load() })
			lib := openTestLibrary(t)
			preview, err := lib.PreviewFetch(ctx, library.PreviewFetchRequest{URL: server.URL + "/skills.zip"})
			if err != nil {
				t.Fatal(err)
			}
			fetched := fetchSkills(t, lib, preview, []string{"first", "second"})
			first, second := fetched[0], fetched[1]
			if _, err := lib.CheckUpdates(ctx, []library.CollectionID{first.CollectionID}); err != nil {
				t.Fatal(err)
			}
			before, err := lib.GetSkill(ctx, first.ID)
			if err != nil || before.LastUpdateCheck == nil {
				t.Fatalf("first before = %#v, %v", before, err)
			}
			newer := zipArchive(t, map[string]string{"first/SKILL.md": skillDocument("first", "new"), "second/SKILL.md": skillDocument("second", "new")})
			archive.Store(&newer)
			gate.arm()
			var wg sync.WaitGroup
			var updateErr error
			wg.Go(func() {
				_, updateErr = lib.Update(ctx, library.UpdateRequest{CollectionID: first.CollectionID, Skills: []library.SkillID{first.ID, second.ID}})
			})
			gate.awaitEntered(t)
			gate.disarm()
			mustFinishBeforeRelease(t, gate, race, func(ctx context.Context) error {
				if race == "remove" {
					return lib.RemoveSkill(ctx, second.ID, library.RemoveSkillOptions{})
				}
				_, err := lib.Update(ctx, singleUpdate(second))
				return err
			})
			gate.open()
			wg.Wait()
			want := library.Conflict
			if race == "remove" {
				want = library.NotFound
			}
			requireErrorDetails(t, updateErr, want, library.ErrorDetails{SkillID: second.ID})
			if after, err := lib.GetSkill(ctx, first.ID); err != nil || !reflect.DeepEqual(after, before) {
				t.Fatalf("first after rollback = %#v, %v; want %#v", after, err, before)
			}
			if versions, err := lib.ListVersions(ctx, first.ID); err != nil || len(versions) != 1 {
				t.Fatalf("first versions = %#v, %v; want only the fetched one", versions, err)
			}
			if race == "update" {
				if versions, err := lib.ListVersions(ctx, second.ID); err != nil || len(versions) != 2 {
					t.Fatalf("second versions = %#v, %v; want the racing update kept", versions, err)
				}
			}
		})
	}
}

// 同一 URL 集合里的技能记着不同的 validator:按第一个技能的 validator 得到 304 后,来源在补读整包前又发布了新内容。
// 检查与更新都按补读到的那一份判断,不把 304 的结论套在第一个技能上,也不给旧内容记上新的来源版本。
func TestURLCollectionIsJudgedAgainstOneArchiveWhenValidatorsDiffer(t *testing.T) {
	ctx := t.Context()
	lib := openTestLibrary(t)
	server := newArchiveServer(t, zipArchive(t, map[string]string{"first/SKILL.md": skillDocument("first", "v1"), "second/SKILL.md": skillDocument("second", "v1")}), `"v1"`)
	skills := fetchArchive(t, lib, server.request())
	first, second := skills[0], skills[1]
	server.serve(zipArchive(t, map[string]string{"first/SKILL.md": skillDocument("first", "v2"), "second/SKILL.md": skillDocument("second", "v1")}), `"v2"`)
	if _, err := updateSkill(t, lib, ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	v3 := zipArchive(t, map[string]string{"first/SKILL.md": skillDocument("first", "v3"), "second/SKILL.md": skillDocument("second", "v3")})
	http.DefaultClient.Transport = fetchTransport(func(r *http.Request) (*http.Response, error) {
		header := make(http.Header)
		if r.Header.Get("If-None-Match") == `"v2"` {
			header.Set("ETag", `"v2"`)
			return &http.Response{StatusCode: http.StatusNotModified, Header: header, Body: http.NoBody}, nil
		}
		header.Set("ETag", `"v3"`)
		return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(bytes.NewReader(v3))}, nil
	})
	checks, err := lib.CheckUpdates(ctx, []library.CollectionID{first.CollectionID})
	if err != nil || len(checks) != 1 || checks[0].Error != nil || len(checks[0].Skills) != 2 {
		t.Fatalf("CheckUpdates = %#v, %v", checks, err)
	}
	for _, skill := range checks[0].Skills {
		if skill.LastUpdateCheck.Status != library.UpdateAvailable {
			t.Fatalf("check of %s = %+v; want available", skill.Name, skill.LastUpdateCheck)
		}
	}
	result, err := lib.Update(ctx, library.UpdateRequest{CollectionID: first.CollectionID, Skills: []library.SkillID{first.ID, second.ID}})
	if err != nil || len(result.Skills) != 2 {
		t.Fatalf("Update = %#v, %v", result, err)
	}
	for _, updated := range result.Skills {
		if !updated.Changed || string(skillFiles(t, lib, updated.Skill)[0].Content) != skillDocument(updated.Skill.Name, "v3") {
			t.Fatalf("updated %s = %#v; want v3", updated.Skill.Name, updated)
		}
		if updated.Skill.Revision != result.Skills[0].Skill.Revision {
			t.Fatalf("revisions = %q, %q; want one source revision", result.Skills[0].Skill.Revision, updated.Skill.Revision)
		}
	}
	for _, skill := range []library.Skill{first, second} {
		if status, err := checkSkillUpdate(t, lib, ctx, skill.ID); err != nil || status != library.UpdateCurrent {
			t.Fatalf("check of %s after update = %v, %v", skill.Name, status, err)
		}
	}
}

func TestUpdateRejectsInvalidRequests(t *testing.T) {
	ctx := t.Context()
	lib := openTestLibrary(t)
	skill := fetchNamedSkill(t, lib, "one")
	other := fetchNamedSkill(t, lib, "other")
	for _, tc := range []struct {
		name string
		req  library.UpdateRequest
		code library.ErrorCode
		want library.ErrorDetails
	}{
		{"no collection", library.UpdateRequest{Skills: []library.SkillID{skill.ID}}, library.InvalidArgument, library.ErrorDetails{Field: "collection_id"}},
		{"no skills", library.UpdateRequest{CollectionID: skill.CollectionID}, library.InvalidArgument, library.ErrorDetails{Field: "skills"}},
		{"invalid skill ID", library.UpdateRequest{CollectionID: skill.CollectionID, Skills: []library.SkillID{0}}, library.InvalidArgument, library.ErrorDetails{Field: "skills"}},
		{"missing skill", library.UpdateRequest{CollectionID: skill.CollectionID, Skills: []library.SkillID{skill.ID, 9999}}, library.NotFound, library.ErrorDetails{SkillID: 9999}},
		{"skill from another collection", library.UpdateRequest{CollectionID: skill.CollectionID, Skills: []library.SkillID{skill.ID, other.ID}}, library.InvalidArgument, library.ErrorDetails{Field: "skills", SkillID: other.ID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := lib.Update(ctx, tc.req)
			requireErrorDetails(t, err, tc.code, tc.want)
		})
	}
}

func TestUpdateMarksSelectedSkillsCurrent(t *testing.T) {
	for _, name := range []string{"changed", "unchanged", "never checked"} {
		t.Run(name, func(t *testing.T) {
			lib := openTestLibrary(t)
			skill, source := fetchLocalSkill(t, lib, "example", "old")
			writeLocalSkill(t, source, "new")
			var checkedAt time.Time
			if name != "never checked" {
				if status, err := checkSkillUpdate(t, lib, t.Context(), skill.ID); err != nil || status != library.UpdateAvailable {
					t.Fatalf("check = %v, %v", status, err)
				}
				checkedAt = updateCheckState(t, lib, skill.ID).CheckedAt
			}
			if name == "unchanged" {
				writeLocalSkill(t, source, "old")
			}
			result, err := updateSkill(t, lib, t.Context(), skill.ID)
			if err != nil || result.Changed != (name != "unchanged") {
				t.Fatalf("update = %+v, %v", result, err)
			}
			got := updateCheckState(t, lib, skill.ID)
			if got == nil || got.Status != library.UpdateCurrent || got.CheckedAt.Before(checkedAt) || !reflect.DeepEqual(got, result.Skill.LastUpdateCheck) {
				t.Fatalf("after update = %+v", got)
			}
		})
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
			_, err := lib.Update(t.Context(), singleUpdate(skill))
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

// 再预览一次集合,看作者又发布了哪些技能

func TestPreviewFetchCollectionListsNewSkills(t *testing.T) {
	ctx := t.Context()
	lib := openTestLibrary(t)
	root := t.TempDir()
	writeLocalSkill(t, filepath.Join(root, "first"), "v1")
	preview, err := lib.PreviewFetch(ctx, localRequest(root))
	if err != nil {
		t.Fatal(err)
	}
	first := fetchSkills(t, lib, preview, []string{"first"})[0]
	writeLocalSkill(t, filepath.Join(root, "second"), "v1")
	again, err := lib.PreviewFetch(ctx, library.PreviewFetchRequest{Collection: first.CollectionID})
	if err != nil {
		t.Fatal(err)
	}
	want := []library.Candidate{{Fetched: first.ID, Name: "first", Path: "first"}, {New: true, Name: "second", Path: "second"}}
	if !reflect.DeepEqual(again.Candidates, want) {
		t.Fatalf("candidates = %#v; want %#v", again.Candidates, want)
	}
	second := fetchSkills(t, lib, again, []string{"second"})[0]
	if second.CollectionID != first.CollectionID {
		t.Fatalf("new skill joined collection %d; want %d", second.CollectionID, first.CollectionID)
	}
	if _, err := lib.PreviewFetch(ctx, library.PreviewFetchRequest{Collection: 9999}); !library.IsCode(err, library.NotFound) {
		t.Fatalf("missing collection = %v; want NotFound", err)
	}
}
