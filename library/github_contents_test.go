package library_test

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/tiaot33/skills-tui/library"
)

func TestGitHubFetchPreviewTransfersOnlySkillMarkdown(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"first/SKILL.md":      {content: []byte(skillDocument("first", "first body")), mode: 0o644},
		"first/scripts/run":   {content: []byte{0, 255, 1}, mode: 0o755},
		"second/SKILL.md":     {content: []byte(skillDocument("second", "second body")), mode: 0o644},
		"second/notes.md":     {content: []byte("unselected notes"), mode: 0o644},
		"unrelated/large.bin": {content: []byte("do not transfer"), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)

	lib := openTestLibrary(t)
	fetch, err := lib.Fetch(t.Context(), gitHubSpec())
	if err != nil {
		t.Fatal(err)
	}
	if fetch.Ref != fx.commit {
		t.Fatalf("Fetch.Ref = %q, want pinned commit %q", fetch.Ref, fx.commit)
	}
	// GitHub API mock 对 skills.sh 回 502,审计降级为 unavailable,不影响预览。
	unavailable := &library.SkillAudit{Availability: library.AuditUnavailable}
	want := []library.FetchSkill{
		{Name: "first", Path: "first", Audit: unavailable},
		{Name: "second", Path: "second", Audit: unavailable},
	}
	if !reflect.DeepEqual(fetch.Skills, want) {
		t.Fatalf("Fetch candidates = %#v, want %#v", fetch.Skills, want)
	}
	listed, err := lib.ListFetchSkills(t.Context(), fetch.ID)
	if err != nil || !reflect.DeepEqual(listed.Skills, want) {
		t.Fatalf("ListFetchSkills = %#v, %v", listed, err)
	}
	if skills, err := lib.ListSkills(t.Context()); err != nil || len(skills) != 0 {
		t.Fatalf("preview wrote Library: %v, %v", skills, err)
	}
	assertGitHubPreviewRequests(t, api, fx, "first/SKILL.md", "second/SKILL.md")
}

func TestGitHubInstallFetchTransfersSelectedSkillAndRecordsTreeSHA(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"first/SKILL.md":    {content: []byte(skillDocument("first", "first body")), mode: 0o644},
		"first/scripts/run": {content: []byte{0, 255, 1}, mode: 0o755},
		"first/data.bin":    {content: []byte{0, 1, 2, 255}, mode: 0o644},
		"second/SKILL.md":   {content: []byte(skillDocument("second", "second body")), mode: 0o644},
		"second/extra.md":   {content: []byte("other skill"), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)

	lib := openTestLibrary(t)
	fetch, err := lib.Fetch(t.Context(), gitHubSpec())
	if err != nil {
		t.Fatal(err)
	}
	api.Reset()
	installed, err := lib.InstallFetch(t.Context(), fetch.ID, []string{"first"})
	if err != nil || len(installed) != 1 || installed[0].Name != "first" {
		t.Fatalf("InstallFetch = %#v, %v", installed, err)
	}
	wantFiles := []library.File{
		{Path: "SKILL.md", Content: []byte(skillDocument("first", "first body")), Mode: library.ModeRegular},
		{Path: "data.bin", Content: []byte{0, 1, 2, 255}, Mode: library.ModeRegular},
		{Path: "scripts/run", Content: []byte{0, 255, 1}, Mode: library.ModeExecutable},
	}
	if !reflect.DeepEqual(installed[0].Files, wantFiles) {
		t.Fatalf("installed files = %#v, want %#v", installed[0].Files, wantFiles)
	}
	wantSHA := fx.treeSHA("first")
	if installed[0].GitTreeSHA != wantSHA {
		t.Fatalf("InstallFetch GitTreeSHA = %q, want %q", installed[0].GitTreeSHA, wantSHA)
	}
	if got := readPersistedGitTreeSHA(t, lib, installed[0].ID); got != wantSHA {
		t.Fatalf("persisted GitTreeSHA = %q, want %q", got, wantSHA)
	}
	assertGitHubInstallBlobs(t, api, fx, "first")
	if _, err := lib.InstallFetch(t.Context(), fetch.ID, []string{"first"}); !library.IsCode(err, library.NotFound) {
		t.Fatalf("successful handle reused: %v", err)
	}
}

func TestGitHubInstallFetchUsesPinnedCommitAfterHeadAdvances(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md": {content: []byte(skillDocument("demo", "pinned")), mode: 0o644},
		"demo/note.md":  {content: []byte("original note"), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	wantSHA := fx.treeSHA("demo")
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)

	lib := openTestLibrary(t)
	fetch, err := lib.Fetch(t.Context(), gitHubSpec())
	if err != nil {
		t.Fatal(err)
	}
	if fetch.Ref != fx.commit {
		t.Fatalf("Fetch.Ref = %q, want %q", fetch.Ref, fx.commit)
	}

	writeFile(t, filepath.Join(repo, "demo", "SKILL.md"), []byte(skillDocument("demo", "advanced")), 0o644)
	writeFile(t, filepath.Join(repo, "demo", "note.md"), []byte("advanced note"), 0o644)
	commitGit(t, repo, "advance")
	advanced := inspectGitHubRepo(t, repo)
	api.Swap(advanced)
	if advanced.commit == fx.commit {
		t.Fatal("fixture HEAD did not advance")
	}

	installed, err := lib.InstallFetch(t.Context(), fetch.ID, []string{"demo"})
	if err != nil || len(installed) != 1 {
		t.Fatalf("InstallFetch = %#v, %v", installed, err)
	}
	if installed[0].Source.LastSeenRef != fx.commit {
		t.Fatalf("LastSeenRef = %q, want pinned %q", installed[0].Source.LastSeenRef, fx.commit)
	}
	want := []library.File{
		{Path: "SKILL.md", Content: []byte(skillDocument("demo", "pinned")), Mode: library.ModeRegular},
		{Path: "note.md", Content: []byte("original note"), Mode: library.ModeRegular},
	}
	if !reflect.DeepEqual(installed[0].Files, want) {
		t.Fatalf("installed advanced HEAD content = %#v", installed[0].Files)
	}
	if installed[0].GitTreeSHA != wantSHA {
		t.Fatalf("GitTreeSHA = %q, want pinned %q", installed[0].GitTreeSHA, wantSHA)
	}
}

func TestGitHubContentsFallsBackToCloneWhenAPIUnavailable(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"good/SKILL.md":     {content: []byte(skillDocument("good", "body")), mode: 0o644},
		"good/scripts/run":  {content: []byte{9, 8, 7}, mode: 0o755},
		"bad/SKILL.md":      {content: []byte(skillDocument("bad", "body")), mode: 0o644},
		"other/SKILL.md":    {content: []byte(skillDocument("other", "other")), mode: 0o644},
		"other/ignored.bin": {content: []byte("other"), mode: 0o644},
	})
	if err := os.Symlink("/does-not-exist", filepath.Join(repo, "bad", "link")); err != nil {
		t.Fatal(err)
	}
	commitGit(t, repo, "broken candidate")
	redirectGitHubClone(t, repo)
	client := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: fetchTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusInternalServerError, Body: io.NopCloser(strings.NewReader("boom")), Header: make(http.Header)}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = client })

	lib := openTestLibrary(t)
	fetch, err := lib.Fetch(t.Context(), gitHubSpec())
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, skill := range fetch.Skills {
		names = append(names, skill.Name)
	}
	if !reflect.DeepEqual(names, []string{"good", "other"}) && !reflect.DeepEqual(names, []string{"other", "good"}) {
		t.Fatalf("fallback candidates = %#v", fetch)
	}
	installed, err := lib.InstallFetch(t.Context(), fetch.ID, []string{"good"})
	if err != nil || len(installed) != 1 {
		t.Fatalf("fallback install = %#v, %v", installed, err)
	}
	want := []library.File{
		{Path: "SKILL.md", Content: []byte(skillDocument("good", "body")), Mode: library.ModeRegular},
		{Path: "scripts/run", Content: []byte{9, 8, 7}, Mode: library.ModeExecutable},
	}
	if !reflect.DeepEqual(installed[0].Files, want) {
		t.Fatalf("fallback files = %#v", installed[0].Files)
	}
	if got := readPersistedGitTreeSHA(t, lib, installed[0].ID); got != gitRevParse(t, repo, "HEAD:good") {
		t.Fatalf("fallback GitTreeSHA = %q", got)
	}
}

func TestGitHubContentsFallsBackToCloneWhenTreeTruncated(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	fx.truncated = true
	serveGitHubAPI(t, fx)
	redirectGitHubClone(t, repo)

	lib := openTestLibrary(t)
	fetch, err := lib.Fetch(t.Context(), gitHubSpec())
	if err != nil || len(fetch.Skills) != 1 || fetch.Skills[0].Name != "demo" {
		t.Fatalf("truncated fallback = %#v, %v", fetch, err)
	}
	installed, err := lib.InstallFetch(t.Context(), fetch.ID, nil)
	if err != nil || installed[0].GitTreeSHA != fx.treeSHA("demo") {
		t.Fatalf("truncated install = %#v, %v", installed, err)
	}
}

func TestGitHubContentsFallsBackToCloneWhenBlobIncomplete(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	fx.incompleteBlob = fx.blobSHA("demo/SKILL.md")
	serveGitHubAPI(t, fx)
	redirectGitHubClone(t, repo)

	lib := openTestLibrary(t)
	fetch, err := lib.Fetch(t.Context(), gitHubSpec())
	if err != nil || len(fetch.Skills) != 1 {
		t.Fatalf("incomplete blob fallback = %#v, %v", fetch, err)
	}
	installed, err := lib.InstallFetch(t.Context(), fetch.ID, nil)
	if err != nil || string(installed[0].Files[0].Content) != skillDocument("demo", "body") {
		t.Fatalf("incomplete blob install = %#v, %v", installed, err)
	}
}

func TestGitHubInstallFetchFailureKeepsHandleForRetry(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644},
		"demo/extra.md": {content: []byte("asset"), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)

	lib := openTestLibrary(t)
	fetch, err := lib.Fetch(t.Context(), gitHubSpec())
	if err != nil {
		t.Fatal(err)
	}
	api.setFailBlobs(true)
	installed, err := lib.InstallFetch(t.Context(), fetch.ID, []string{"demo"})
	if err == nil || len(installed) != 0 || !library.IsCode(err, library.SourceUnreachable) || library.IsCode(err, library.Internal) {
		t.Fatalf("failed install = %#v, %v; want SourceUnreachable", installed, err)
	}
	if skills, err := lib.ListSkills(t.Context()); err != nil || len(skills) != 0 {
		t.Fatalf("failed install wrote Library: %v, %v", skills, err)
	}
	if _, err := lib.ListFetchSkills(t.Context(), fetch.ID); err != nil {
		t.Fatalf("failed install discarded handle: %v", err)
	}
	api.setFailBlobs(false)
	installed, err = lib.InstallFetch(t.Context(), fetch.ID, []string{"demo"})
	if err != nil || len(installed) != 1 || installed[0].Name != "demo" {
		t.Fatalf("retry install = %#v, %v", installed, err)
	}
}

func TestGitHubInstallFetchMapsForbiddenBlobsToPermission(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644},
		"demo/extra.md": {content: []byte("asset"), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)

	lib := openTestLibrary(t)
	fetch, err := lib.Fetch(t.Context(), gitHubSpec())
	if err != nil {
		t.Fatal(err)
	}
	api.setBlobStatus(http.StatusForbidden)
	installed, err := lib.InstallFetch(t.Context(), fetch.ID, []string{"demo"})
	if err == nil || len(installed) != 0 || !library.IsCode(err, library.Permission) {
		t.Fatalf("forbidden blob = %#v, %v; want Permission", installed, err)
	}
	if _, err := lib.ListFetchSkills(t.Context(), fetch.ID); err != nil {
		t.Fatalf("forbidden blob discarded handle: %v", err)
	}
}

func TestGitHubAnonymousAPIRetriesWithTokenThenSucceedsWithoutClone(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusTooManyRequests} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			api := serveGitHubAPI(t, fx)
			t.Setenv("GITHUB_TOKEN", "test-github-token")
			t.Setenv("GH_TOKEN", "")
			api.anonymousStatus = status
			blockGitHubClone(t)

			lib := openTestLibrary(t)
			fetch, err := lib.Fetch(t.Context(), gitHubSpec())
			if err != nil || len(fetch.Skills) != 1 || fetch.Skills[0].Name != "demo" {
				t.Fatalf("token retry Fetch = %#v, %v", fetch, err)
			}
			if !api.sawAnonymousThenToken() {
				t.Fatalf("requests = %#v, want anonymous %d then token retry", api.snapshot(), status)
			}
			installed, err := lib.InstallFetch(t.Context(), fetch.ID, nil)
			if err != nil || installed[0].GitTreeSHA != fx.treeSHA("demo") {
				t.Fatalf("token retry install = %#v, %v", installed, err)
			}
		})
	}
}

func TestGitHubAPIErrorsDoNotExposeToken(t *testing.T) {
	const token = "super-secret-github-token"
	t.Setenv("GITHUB_TOKEN", token)
	t.Setenv("GH_TOKEN", token)
	blockGitHubClone(t)
	client := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: fetchTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Body:       io.NopCloser(strings.NewReader("token=" + token)),
			Header:     make(http.Header),
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = client })

	lib := openTestLibrary(t)
	_, err := lib.Fetch(t.Context(), gitHubSpec())
	if err == nil || strings.Contains(err.Error(), token) {
		t.Fatalf("error exposed token: %v", err)
	}
}

func TestGitHubFetchHonorsSubpathAndNamesWithoutChangingSource(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"skills/first/SKILL.md":  {content: []byte(skillDocument("first", "one")), mode: 0o644},
		"skills/second/SKILL.md": {content: []byte(skillDocument("second", "two")), mode: 0o644},
		"other/SKILL.md":         {content: []byte(skillDocument("other", "nope")), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)

	lib := openTestLibrary(t)
	spec := gitHubSpec()
	spec.Subpath = "skills"
	spec.Names = []string{"first"}
	fetch, err := lib.Fetch(t.Context(), spec)
	if err != nil || len(fetch.Skills) != 1 || fetch.Skills[0].Name != "first" || fetch.Skills[0].Path != "skills/first" {
		t.Fatalf("filtered Fetch = %#v, %v", fetch, err)
	}
	assertNoBlobPath(t, api, "other/SKILL.md")
	installed, err := lib.InstallFetch(t.Context(), fetch.ID, nil)
	if err != nil || len(installed) != 1 || installed[0].Name != "first" {
		t.Fatalf("filtered install = %#v, %v", installed, err)
	}
	if installed[0].GitTreeSHA != fx.treeSHA("skills/first") {
		t.Fatalf("GitTreeSHA = %q, want %q", installed[0].GitTreeSHA, fx.treeSHA("skills/first"))
	}
}

func TestGitHubRootSkillPreviewAndInstallUseRootTreeSHA(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"SKILL.md":                   {content: []byte(skillDocument("root-skill", "root")), mode: 0o644},
		"notes.md":                   {content: []byte("extra"), mode: 0o644},
		"references/nested/SKILL.md": {content: []byte(skillDocument("nested", "keep")), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	serveGitHubAPI(t, fx)
	blockGitHubClone(t)

	lib := openTestLibrary(t)
	fetch, err := lib.Fetch(t.Context(), gitHubSpec())
	if err != nil || len(fetch.Skills) != 1 || fetch.Skills[0].Path != "." || fetch.Skills[0].Name != "root-skill" {
		t.Fatalf("root Fetch = %#v, %v", fetch, err)
	}
	installed, err := lib.InstallFetch(t.Context(), fetch.ID, nil)
	if err != nil || installed[0].GitTreeSHA != fx.rootTree {
		t.Fatalf("root install = %#v, %v", installed, err)
	}
	want := []library.File{
		{Path: "SKILL.md", Content: []byte(skillDocument("root-skill", "root")), Mode: library.ModeRegular},
		{Path: "notes.md", Content: []byte("extra"), Mode: library.ModeRegular},
		{Path: "references/nested/SKILL.md", Content: []byte(skillDocument("nested", "keep")), Mode: library.ModeRegular},
	}
	if !reflect.DeepEqual(installed[0].Files, want) {
		t.Fatalf("root files = %#v", installed[0].Files)
	}
}

func TestGitHubContentsFallsBackToCloneWhenSkillHasUnsupportedObject(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"good/SKILL.md":    {content: []byte(skillDocument("good", "body")), mode: 0o644},
		"good/scripts/run": {content: []byte{1, 2, 3}, mode: 0o755},
		"bad/SKILL.md":     {content: []byte(skillDocument("bad", "body")), mode: 0o644},
	})
	if err := os.Symlink("/does-not-exist", filepath.Join(repo, "bad", "link")); err != nil {
		t.Fatal(err)
	}
	commitGit(t, repo, "symlink")
	fx := inspectGitHubRepo(t, repo)
	serveGitHubAPI(t, fx)
	redirectGitHubClone(t, repo)

	lib := openTestLibrary(t)
	fetch, err := lib.Fetch(t.Context(), gitHubSpec())
	if err != nil || len(fetch.Skills) != 1 || fetch.Skills[0].Name != "good" || len(fetch.Diagnostics) != 1 {
		t.Fatalf("unsupported object fallback = %#v, %v", fetch, err)
	}
	installed, err := lib.InstallFetch(t.Context(), fetch.ID, []string{"good"})
	if err != nil || string(installed[0].Files[0].Content) != skillDocument("good", "body") {
		t.Fatalf("unsupported object install = %#v, %v", installed, err)
	}
}

type repoFile struct {
	content []byte
	mode    os.FileMode
}

type gitHubFixture struct {
	commit          string
	rootTree        string
	entries         []gitHubTreeEntry
	blobs           map[string][]byte
	paths           map[string]gitHubTreeEntry
	truncated       bool
	incompleteBlob  string
	anonymousStatus int
	failBlobs       bool
}

type gitHubTreeEntry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
	Size int    `json:"size,omitempty"`
}

func (fx gitHubFixture) treeSHA(skillPath string) string {
	if skillPath == "" || skillPath == "." {
		return fx.rootTree
	}
	entry, ok := fx.paths[skillPath]
	if !ok || entry.Type != "tree" {
		return ""
	}
	return entry.SHA
}

func (fx gitHubFixture) blobSHA(filePath string) string {
	entry, ok := fx.paths[filePath]
	if !ok {
		return ""
	}
	return entry.SHA
}

func gitHubSpec() library.SourceSpec {
	return library.SourceSpec{
		Type:    library.SourceGitHub,
		Locator: library.SourceLocator{Repository: "https://github.com/owner/repo.git", Branch: "main"},
	}
}

func writeGitHubRepo(t *testing.T, files map[string]repoFile) string {
	t.Helper()
	repo := t.TempDir()
	runGit(t, repo, "init", "-b", "main")
	for name, file := range files {
		writeFile(t, filepath.Join(repo, filepath.FromSlash(name)), file.content, file.mode)
	}
	commitGit(t, repo, "initial")
	return repo
}

func inspectGitHubRepo(t *testing.T, repo string) gitHubFixture {
	t.Helper()
	fx := gitHubFixture{
		commit:   gitRevParse(t, repo, "HEAD"),
		rootTree: gitRevParse(t, repo, "HEAD^{tree}"),
		blobs:    map[string][]byte{},
		paths:    map[string]gitHubTreeEntry{},
	}
	output, err := exec.Command("git", "-c", "core.excludesFile=/dev/null", "-C", repo, "ls-tree", "-r", "-t", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(strings.TrimSpace(string(output)), "\n") {
		mode, rest, ok := strings.Cut(line, " ")
		if !ok {
			t.Fatalf("ls-tree: %q", line)
		}
		typ, rest, ok := strings.Cut(rest, " ")
		if !ok {
			t.Fatalf("ls-tree: %q", line)
		}
		sha, filePath, ok := strings.Cut(rest, "\t")
		if !ok {
			t.Fatalf("ls-tree: %q", line)
		}
		entry := gitHubTreeEntry{Path: filePath, Mode: mode, Type: typ, SHA: sha}
		if typ == "blob" {
			content, err := exec.Command("git", "-c", "core.excludesFile=/dev/null", "-C", repo, "cat-file", "-p", sha).Output()
			if err != nil {
				t.Fatal(err)
			}
			entry.Size = len(content)
			fx.blobs[sha] = content
		}
		fx.entries = append(fx.entries, entry)
		fx.paths[filePath] = entry
	}
	return fx
}

func blockGitHubClone(t *testing.T) {
	t.Helper()
	redirectGitHubClone(t, filepath.Join(t.TempDir(), "missing-remote"))
}

func redirectGitHubClone(t *testing.T, repo string) {
	t.Helper()
	stubGitHubCLI(t, "exit 1\n")
	config := filepath.Join(t.TempDir(), "gitconfig")
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	runGit(t, t.TempDir(), "config", "--file", config, "url.file://"+repo+".insteadOf", "https://github.com/owner/repo.git")
	runGit(t, t.TempDir(), "config", "--file", config, "--add", "url.file://"+repo+".insteadOf", "git@github.com:owner/repo.git")
}

type gitHubAPIMock struct {
	mu                 sync.Mutex
	fx                 gitHubFixture
	anonymousStatus    int
	failBlobs          bool
	blobStatus         int
	rateLimitRemaining string
	rateLimitReset     string
	requests           []gitHubAPIRequest
}

type gitHubAPIRequest struct {
	path   string
	query  string
	authed bool
	accept string
}

func serveGitHubAPI(t *testing.T, fx gitHubFixture) *gitHubAPIMock {
	t.Helper()
	stubGitHubCLI(t, "exit 1\n")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	mock := &gitHubAPIMock{fx: cloneGitHubFixture(fx), anonymousStatus: fx.anonymousStatus, failBlobs: fx.failBlobs}
	client := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: fetchTransport(mock.roundTrip)}
	t.Cleanup(func() { http.DefaultClient = client })
	return mock
}

func cloneGitHubFixture(fx gitHubFixture) gitHubFixture {
	cloned := fx
	cloned.blobs = maps.Clone(fx.blobs)
	cloned.paths = maps.Clone(fx.paths)
	cloned.entries = slices.Clone(fx.entries)
	return cloned
}

func (m *gitHubAPIMock) Swap(fx gitHubFixture) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fx.blobs == nil {
		m.fx.blobs = map[string][]byte{}
	}
	maps.Copy(m.fx.blobs, fx.blobs)
	if m.fx.paths == nil {
		m.fx.paths = map[string]gitHubTreeEntry{}
	}
	maps.Copy(m.fx.paths, fx.paths)
	m.fx.commit = fx.commit
	m.fx.rootTree = fx.rootTree
	m.fx.entries = slices.Clone(fx.entries)
	m.fx.truncated = fx.truncated
	m.fx.incompleteBlob = fx.incompleteBlob
}

func (m *gitHubAPIMock) setFailBlobs(fail bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failBlobs = fail
}

func (m *gitHubAPIMock) setBlobStatus(status int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.blobStatus = status
}

func (m *gitHubAPIMock) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = nil
}

func (m *gitHubAPIMock) snapshot() []gitHubAPIRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.requests)
}

func (m *gitHubAPIMock) blobPaths() []string {
	m.mu.Lock()
	fx := m.fx
	requests := slices.Clone(m.requests)
	m.mu.Unlock()
	var paths []string
	shaPath := map[string]string{}
	for p, entry := range fx.paths {
		if entry.Type == "blob" {
			shaPath[entry.SHA] = p
		}
	}
	for _, req := range requests {
		if sha, ok := strings.CutPrefix(req.path, "/repos/owner/repo/git/blobs/"); ok {
			paths = append(paths, shaPath[sha])
		}
	}
	return paths
}

func (m *gitHubAPIMock) sawAnonymousThenToken() bool {
	requests := m.snapshot()
	sawAnon, sawToken := false, false
	for _, req := range requests {
		if !req.authed {
			sawAnon = true
		}
		if sawAnon && req.authed {
			sawToken = true
		}
	}
	return sawAnon && sawToken
}

func (m *gitHubAPIMock) roundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != "api.github.com" {
		return &http.Response{StatusCode: http.StatusBadGateway, Body: http.NoBody, Header: make(http.Header)}, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	authed := r.Header.Get("Authorization") != ""
	m.requests = append(m.requests, gitHubAPIRequest{path: r.URL.Path, query: r.URL.RawQuery, authed: authed, accept: r.Header.Get("Accept")})
	if m.anonymousStatus != 0 && !authed {
		return m.errorResponse(m.anonymousStatus), nil
	}
	if ref, ok := strings.CutPrefix(r.URL.Path, "/repos/owner/repo/commits/"); ok {
		if ref != "main" && !strings.EqualFold(ref, m.fx.commit) {
			return jsonResponse(http.StatusNotFound, map[string]string{"message": "Not Found"}), nil
		}
		return jsonResponse(http.StatusOK, map[string]any{
			"sha": m.fx.commit,
			"commit": map[string]any{
				"tree": map[string]string{"sha": m.fx.rootTree},
			},
		}), nil
	}
	if sha, ok := strings.CutPrefix(r.URL.Path, "/repos/owner/repo/git/trees/"); ok {
		if sha != m.fx.rootTree && sha != m.fx.commit {
			return jsonResponse(http.StatusNotFound, map[string]string{"message": "Not Found"}), nil
		}
		return jsonResponse(http.StatusOK, map[string]any{
			"sha":       m.fx.rootTree,
			"tree":      m.fx.entries,
			"truncated": m.fx.truncated,
		}), nil
	}
	if sha, ok := strings.CutPrefix(r.URL.Path, "/repos/owner/repo/git/blobs/"); ok {
		if m.blobStatus != 0 {
			return m.errorResponse(m.blobStatus), nil
		}
		if m.failBlobs || sha == m.fx.incompleteBlob {
			return jsonResponse(http.StatusOK, map[string]any{"sha": sha, "encoding": "base64", "content": "", "size": 99}), nil
		}
		content, ok := m.fx.blobs[sha]
		if !ok {
			return jsonResponse(http.StatusNotFound, map[string]string{"message": "Not Found"}), nil
		}
		return jsonResponse(http.StatusOK, map[string]any{
			"sha":      sha,
			"encoding": "base64",
			"content":  base64.StdEncoding.EncodeToString(content),
			"size":     len(content),
		}), nil
	}
	return jsonResponse(http.StatusNotFound, map[string]string{"message": "Not Found"}), nil
}

func (m *gitHubAPIMock) errorResponse(status int) *http.Response {
	response := jsonResponse(status, map[string]string{"message": "unavailable"})
	response.Header.Set("X-RateLimit-Remaining", m.rateLimitRemaining)
	response.Header.Set("X-RateLimit-Reset", m.rateLimitReset)
	return response
}

func jsonResponse(status int, body any) *http.Response {
	payload, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	header := make(http.Header)
	header.Set("Content-Type", "application/json")
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(string(payload))), Header: header}
}

func assertGitHubPreviewRequests(t *testing.T, api *gitHubAPIMock, fx gitHubFixture, skillMarkdown ...string) {
	t.Helper()
	var blobs []string
	sawCommit, sawTree := false, false
	for _, req := range api.snapshot() {
		switch {
		case strings.HasPrefix(req.path, "/repos/owner/repo/commits/"):
			sawCommit = true
		case strings.HasPrefix(req.path, "/repos/owner/repo/git/trees/"):
			if !strings.Contains(req.query, "recursive") {
				t.Fatalf("tree request missing recursive: %s?%s", req.path, req.query)
			}
			sawTree = true
		case strings.HasPrefix(req.path, "/repos/owner/repo/git/blobs/"):
			sha, _ := strings.CutPrefix(req.path, "/repos/owner/repo/git/blobs/")
			blobs = append(blobs, sha)
		default:
			t.Fatalf("unexpected API request %s", req.path)
		}
	}
	if !sawCommit || !sawTree {
		t.Fatalf("preview requests missing commit or tree: %#v", api.snapshot())
	}
	want := map[string]bool{}
	for _, p := range skillMarkdown {
		want[fx.blobSHA(p)] = true
	}
	if len(blobs) != len(want) {
		t.Fatalf("preview blobs = %v (%v), want only %v", blobs, api.blobPaths(), skillMarkdown)
	}
	for _, sha := range blobs {
		if !want[sha] {
			t.Fatalf("preview fetched extra blob %s (%v)", sha, api.blobPaths())
		}
	}
}

func assertGitHubInstallBlobs(t *testing.T, api *gitHubAPIMock, fx gitHubFixture, skillPath string) {
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
	for _, req := range api.snapshot() {
		if strings.HasPrefix(req.path, "/repos/owner/repo/commits/") {
			t.Fatal("InstallFetch re-resolved ref")
		}
		sha, ok := strings.CutPrefix(req.path, "/repos/owner/repo/git/blobs/")
		if !ok {
			continue
		}
		if !allowed[sha] {
			t.Fatalf("InstallFetch fetched blob outside %s: %s (%v)", skillPath, sha, api.blobPaths())
		}
	}
	var fetched []string
	for _, p := range api.blobPaths() {
		if p != "" {
			fetched = append(fetched, p)
		}
	}
	if len(fetched) == 0 {
		t.Fatal("InstallFetch fetched no blobs")
	}
}

func assertNoBlobPath(t *testing.T, api *gitHubAPIMock, filePath string) {
	t.Helper()
	for _, p := range api.blobPaths() {
		if p == filePath {
			t.Fatalf("fetched %s", filePath)
		}
	}
}
