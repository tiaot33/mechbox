package library_test

import (
	"bytes"
	"database/sql"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tiaot33/skills-tui/library"
)

func TestGitHubCheckAndUpdateUseDirectoryTreeSHAWithoutFetchingFiles(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"first/SKILL.md":      {content: []byte(skillDocument("first", "first body")), mode: 0o644},
		"first/scripts/run":   {content: []byte{0, 255, 1}, mode: 0o755},
		"second/SKILL.md":     {content: []byte(skillDocument("second", "second body")), mode: 0o644},
		"second/notes.md":     {content: []byte("second notes"), mode: 0o644},
		"unrelated/large.bin": {content: []byte("noise"), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)

	lib := openTestLibrary(t)
	ctx := t.Context()
	fetch, err := lib.Fetch(ctx, gitHubSpec())
	if err != nil {
		t.Fatal(err)
	}
	installed, err := lib.InstallFetch(ctx, fetch.ID, nil)
	if err != nil || len(installed) != 2 {
		t.Fatalf("InstallFetch = %#v, %v", installed, err)
	}
	first, second := installed[0], installed[1]
	if first.Name != "first" {
		first, second = second, first
	}
	scope := library.Scope{Agent: library.AgentCodex, Path: filepath.Join(t.TempDir(), "skills")}
	if err := lib.Activate(ctx, first.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	activations, err := lib.ListActivations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before, err := lib.GetSkill(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}

	api.Reset()
	if available, err := lib.CheckUpdate(ctx, first.ID); err != nil || available {
		t.Fatalf("unchanged CheckUpdate = %v, %v", available, err)
	}
	assertGitHubTreeOnly(t, api)
	current, err := lib.GetSkill(ctx, first.ID)
	if err != nil || !reflect.DeepEqual(current, before) {
		t.Fatalf("check wrote Library: %#v, %v", current, err)
	}

	writeFile(t, filepath.Join(repo, "unrelated", "large.bin"), []byte("more noise"), 0o644)
	commitGit(t, repo, "unrelated path")
	other := inspectGitHubRepo(t, repo)
	if other.treeSHA("first") != fx.treeSHA("first") || other.commit == fx.commit {
		t.Fatal("fixture did not keep skill SHA while advancing commit")
	}
	api.Swap(other)
	api.Reset()
	if available, err := lib.CheckUpdate(ctx, first.ID); err != nil || available {
		t.Fatalf("unrelated path CheckUpdate = %v, %v", available, err)
	}
	if available, err := lib.CheckUpdate(ctx, second.ID); err != nil || available {
		t.Fatalf("unrelated path second CheckUpdate = %v, %v", available, err)
	}
	assertGitHubTreeOnly(t, api)
	stale, err := lib.GetSkill(ctx, first.ID)
	if err != nil || stale.Source.LastSeenRef != fx.commit {
		t.Fatalf("check refreshed last_seen_ref: %#v, %v", stale, err)
	}

	result, err := lib.Update(ctx, first.ID)
	if err != nil || result.Changed || result.Note == "" {
		t.Fatalf("unchanged Update = %#v, %v", result, err)
	}
	if result.Skill.GitTreeSHA != fx.treeSHA("first") {
		t.Fatalf("unchanged Update GitTreeSHA = %q, want %q", result.Skill.GitTreeSHA, fx.treeSHA("first"))
	}
	if result.Skill.Source.LastSeenRef != other.commit {
		t.Fatalf("unchanged Update last_seen_ref = %q, want new commit %q", result.Skill.Source.LastSeenRef, other.commit)
	}
	if versions, err := lib.ListVersions(ctx, first.ID); err != nil || len(versions) != 1 {
		t.Fatalf("unchanged Update created version: %#v, %v", versions, err)
	}
	assertGitHubTreeOnly(t, api)

	writeFile(t, filepath.Join(repo, "first", "SKILL.md"), []byte(skillDocument("first", "updated body")), 0o644)
	writeFile(t, filepath.Join(repo, "first", "scripts", "run"), []byte{9, 8, 7}, 0o755)
	commitGit(t, repo, "change first")
	changed := inspectGitHubRepo(t, repo)
	if changed.treeSHA("first") == fx.treeSHA("first") {
		t.Fatal("first skill tree SHA did not change")
	}
	if changed.treeSHA("second") != fx.treeSHA("second") {
		t.Fatal("second skill tree SHA changed")
	}
	api.Swap(changed)
	api.Reset()
	if available, err := lib.CheckUpdate(ctx, first.ID); err != nil || !available {
		t.Fatalf("changed CheckUpdate = %v, %v", available, err)
	}
	if available, err := lib.CheckUpdate(ctx, second.ID); err != nil || available {
		t.Fatalf("unchanged sibling CheckUpdate = %v, %v", available, err)
	}
	assertGitHubTreeOnly(t, api)
	afterCheck, err := lib.GetSkill(ctx, first.ID)
	if err != nil || string(afterCheck.Files[0].Content) != skillDocument("first", "first body") {
		t.Fatalf("check mutated files: %#v, %v", afterCheck, err)
	}
	if versions, err := lib.ListVersions(ctx, first.ID); err != nil || len(versions) != 1 {
		t.Fatalf("check created version: %#v, %v", versions, err)
	}

	api.Reset()
	checks, err := lib.CheckUpdates(ctx, []library.SkillID{first.ID, second.ID, first.ID})
	if err != nil || len(checks) != 2 || checks[0].SkillID != first.ID || checks[1].SkillID != second.ID {
		t.Fatalf("CheckUpdates = %#v, %v", checks, err)
	}
	if checks[0].Error != nil || !checks[0].Available || checks[1].Error != nil || checks[1].Available {
		t.Fatalf("CheckUpdates availability = %#v", checks)
	}
	_, trees, blobs := gitHubAPIKindCounts(t, api)
	if trees != 1 || blobs != 0 {
		t.Fatalf("CheckUpdates remote access trees=%d blobs=%d (%v), want one tree and no blobs", trees, blobs, api.blobPaths())
	}
	if versions, err := lib.ListVersions(ctx, first.ID); err != nil || len(versions) != 1 {
		t.Fatalf("CheckUpdates wrote history: %#v, %v", versions, err)
	}

	api.Reset()
	result, err = lib.Update(ctx, first.ID)
	if err != nil || !result.Changed || result.Note == "" {
		t.Fatalf("changed Update = %#v, %v", result, err)
	}
	wantFiles := []library.File{
		{Path: "SKILL.md", Content: []byte(skillDocument("first", "updated body")), Mode: library.ModeRegular},
		{Path: "scripts/run", Content: []byte{9, 8, 7}, Mode: library.ModeExecutable},
	}
	if !reflect.DeepEqual(result.Skill.Files, wantFiles) {
		t.Fatalf("updated files = %#v, want %#v", result.Skill.Files, wantFiles)
	}
	if result.Skill.GitTreeSHA != changed.treeSHA("first") {
		t.Fatalf("updated GitTreeSHA = %q, want %q", result.Skill.GitTreeSHA, changed.treeSHA("first"))
	}
	if got := readPersistedGitTreeSHA(t, lib, first.ID); got != changed.treeSHA("first") {
		t.Fatalf("persisted GitTreeSHA = %q, want %q", got, changed.treeSHA("first"))
	}
	assertGitHubUpdateBlobs(t, api, changed, "first")
	if versions, err := lib.ListVersions(ctx, first.ID); err != nil || len(versions) != 2 {
		t.Fatalf("versions after change = %#v, %v", versions, err)
	}
	content, err := os.ReadFile(filepath.Join(scope.Path, first.Name, "SKILL.md"))
	if err != nil || string(content) != skillDocument("first", "first body") {
		t.Fatalf("projection rewritten: %q, %v", content, err)
	}
	after, err := lib.ListActivations(ctx)
	if err != nil || !reflect.DeepEqual(after, activations) {
		t.Fatalf("activation records changed: %#v, %v", after, err)
	}
}

func TestGitHubEmptyTreeSHAFallsBackToFilesThenBackfillsOnUpdate(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"legacy/SKILL.md":  {content: []byte(skillDocument("legacy", "old body")), mode: 0o644},
		"legacy/data.bin":  {content: []byte{1, 2, 3}, mode: 0o644},
		"sibling/SKILL.md": {content: []byte(skillDocument("sibling", "sibling body")), mode: 0o644},
		"sibling/notes.md": {content: []byte("notes"), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)

	path := filepath.Join(t.TempDir(), "library.db")
	lib, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
	ctx := t.Context()
	fetch, err := lib.Fetch(ctx, gitHubSpec())
	if err != nil {
		t.Fatal(err)
	}
	installed, err := lib.InstallFetch(ctx, fetch.ID, nil)
	if err != nil || len(installed) != 2 {
		t.Fatalf("InstallFetch = %#v, %v", installed, err)
	}
	legacy, sibling := installed[0], installed[1]
	if legacy.Name != "legacy" {
		legacy, sibling = sibling, legacy
	}
	if err := lib.Close(); err != nil {
		t.Fatal(err)
	}
	clearPersistedGitTreeSHA(t, path, legacy.ID)
	lib, err = library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
	cleared, err := lib.GetSkill(ctx, legacy.ID)
	if err != nil || cleared.GitTreeSHA != "" {
		t.Fatalf("cleared GitTreeSHA = %#v, %v", cleared, err)
	}
	if got := readPersistedGitTreeSHA(t, lib, sibling.ID); got != fx.treeSHA("sibling") {
		t.Fatalf("sibling SHA was cleared: %q", got)
	}

	api.Reset()
	if available, err := lib.CheckUpdate(ctx, legacy.ID); err != nil || available {
		t.Fatalf("empty SHA same files CheckUpdate = %v, %v", available, err)
	}
	if _, _, blobs := gitHubAPIKindCounts(t, api); blobs == 0 {
		t.Fatal("empty SHA check did not read skill files")
	}
	if got := readPersistedGitTreeSHA(t, lib, legacy.ID); got != "" {
		t.Fatalf("check backfilled SHA: %q", got)
	}

	api.Reset()
	result, err := lib.Update(ctx, legacy.ID)
	if err != nil || result.Changed {
		t.Fatalf("empty SHA same files Update = %#v, %v", result, err)
	}
	if result.Skill.GitTreeSHA != fx.treeSHA("legacy") {
		t.Fatalf("backfilled GitTreeSHA = %q, want %q", result.Skill.GitTreeSHA, fx.treeSHA("legacy"))
	}
	if got := readPersistedGitTreeSHA(t, lib, legacy.ID); got != fx.treeSHA("legacy") {
		t.Fatalf("persisted backfill = %q, want %q", got, fx.treeSHA("legacy"))
	}
	if versions, err := lib.ListVersions(ctx, legacy.ID); err != nil || len(versions) != 1 {
		t.Fatalf("backfill created version: %#v, %v", versions, err)
	}

	api.Reset()
	if available, err := lib.CheckUpdate(ctx, legacy.ID); err != nil || available {
		t.Fatalf("after backfill CheckUpdate = %v, %v", available, err)
	}
	assertGitHubTreeOnly(t, api)

	writeFile(t, filepath.Join(repo, "sibling", "notes.md"), []byte("changed notes"), 0o644)
	commitGit(t, repo, "change sibling")
	changed := inspectGitHubRepo(t, repo)
	api.Swap(changed)
	if err := lib.Close(); err != nil {
		t.Fatal(err)
	}
	clearPersistedGitTreeSHA(t, path, sibling.ID)
	lib, err = library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })

	api.Reset()
	if available, err := lib.CheckUpdate(ctx, sibling.ID); err != nil || !available {
		t.Fatalf("empty SHA changed files CheckUpdate = %v, %v", available, err)
	}
	result, err = lib.Update(ctx, sibling.ID)
	if err != nil || !result.Changed {
		t.Fatalf("empty SHA changed files Update = %#v, %v", result, err)
	}
	if result.Skill.GitTreeSHA != changed.treeSHA("sibling") {
		t.Fatalf("new version GitTreeSHA = %q, want %q", result.Skill.GitTreeSHA, changed.treeSHA("sibling"))
	}
	if versions, err := lib.ListVersions(ctx, sibling.ID); err != nil || len(versions) != 2 {
		t.Fatalf("changed empty SHA versions = %#v, %v", versions, err)
	}
}

func TestURLCheckAndUpdateStillCompareFilesAndKeepEmptyGitTreeSHA(t *testing.T) {
	archive := zipArchive(t, map[string]string{
		"demo/SKILL.md": skillDocument("demo", "old"),
		"demo/run":      "echo old",
	})
	client := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: fetchTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(archive)), Header: make(http.Header)}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = client })
	spec := library.SourceSpec{Type: library.SourceURL, Locator: library.SourceLocator{URL: "https://source.example/skills.zip"}}
	lib := openTestLibrary(t)
	ctx := t.Context()
	fetch, err := lib.Fetch(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	installed, err := lib.InstallFetch(ctx, fetch.ID, nil)
	if err != nil || len(installed) != 1 {
		t.Fatalf("InstallFetch = %#v, %v", installed, err)
	}
	if installed[0].GitTreeSHA != "" {
		t.Fatalf("URL GitTreeSHA = %q, want empty", installed[0].GitTreeSHA)
	}
	if available, err := lib.CheckUpdate(ctx, installed[0].ID); err != nil || available {
		t.Fatalf("unchanged URL CheckUpdate = %v, %v", available, err)
	}

	archive = zipArchive(t, map[string]string{
		"demo/SKILL.md": skillDocument("demo", "new"),
		"demo/run":      "echo new",
	})
	if available, err := lib.CheckUpdate(ctx, installed[0].ID); err != nil || !available {
		t.Fatalf("changed URL CheckUpdate = %v, %v", available, err)
	}
	result, err := lib.Update(ctx, installed[0].ID)
	if err != nil || !result.Changed || result.Skill.GitTreeSHA != "" {
		t.Fatalf("URL Update = %#v, %v", result, err)
	}
	if got := readPersistedGitTreeSHA(t, lib, installed[0].ID); got != "" {
		t.Fatalf("URL persisted GitTreeSHA = %q, want empty", got)
	}
}

func assertGitHubUpdateBlobs(t *testing.T, api *gitHubAPIMock, fx gitHubFixture, skillPath string) {
	t.Helper()
	allowed := map[string]bool{}
	for p, entry := range fx.paths {
		if entry.Type != "blob" {
			continue
		}
		if skillPath == "." || p == skillPath || strings.HasPrefix(p, skillPath+"/") {
			allowed[entry.SHA] = true
		}
	}
	var fetched []string
	for _, req := range api.snapshot() {
		if !strings.HasPrefix(req.path, "/repos/owner/repo/git/blobs/") {
			continue
		}
		sha := strings.TrimPrefix(req.path, "/repos/owner/repo/git/blobs/")
		if !allowed[sha] {
			t.Fatalf("Update fetched blob outside %s: %s (%v)", skillPath, sha, api.blobPaths())
		}
		fetched = append(fetched, sha)
	}
	if len(fetched) == 0 {
		t.Fatal("Update fetched no blobs")
	}
}

func assertGitHubTreeOnly(t *testing.T, api *gitHubAPIMock) {
	t.Helper()
	commits, trees, blobs := gitHubAPIKindCounts(t, api)
	if commits == 0 || trees == 0 {
		t.Fatalf("missing commit or tree: %#v", api.snapshot())
	}
	if blobs != 0 {
		t.Fatalf("fetched blobs %v", api.blobPaths())
	}
}

func gitHubAPIKindCounts(t *testing.T, api *gitHubAPIMock) (commits, trees, blobs int) {
	t.Helper()
	for _, req := range api.snapshot() {
		switch {
		case strings.HasPrefix(req.path, "/repos/owner/repo/commits/"):
			commits++
		case strings.HasPrefix(req.path, "/repos/owner/repo/git/trees/"):
			trees++
		case strings.HasPrefix(req.path, "/repos/owner/repo/git/blobs/"):
			blobs++
		default:
			t.Fatalf("unexpected API request %s", req.path)
		}
	}
	return commits, trees, blobs
}

func clearPersistedGitTreeSHA(t *testing.T, dbPath string, id library.SkillID) {
	t.Helper()
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	result, err := db.Exec(`UPDATE skill_versions SET git_tree_sha = '' WHERE skill_id = ?`, id)
	if err != nil {
		t.Fatal(err)
	}
	n, err := result.RowsAffected()
	if err != nil || n == 0 {
		t.Fatalf("clear git_tree_sha: rows=%d, err=%v", n, err)
	}
}
