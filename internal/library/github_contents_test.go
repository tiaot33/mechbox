package library_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
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
	"time"

	"github.com/tiaot33/skills-tui/internal/library"
)

func TestGitHubPreviewTransfersOnlySkillMarkdown(t *testing.T) {
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
	preview, err := lib.PreviewFetch(t.Context(), gitHubRequest())
	if err != nil {
		t.Fatal(err)
	}
	if preview.Ref != "main" || preview.Revision != fx.commit {
		t.Fatalf("PreviewFetch Ref / Revision = %q / %q, want main / pinned commit %q", preview.Ref, preview.Revision, fx.commit)
	}
	want := []library.Candidate{
		{Name: "first", Path: "first"},
		{Name: "second", Path: "second"},
	}
	if !reflect.DeepEqual(preview.Candidates, want) {
		t.Fatalf("PreviewFetch candidates = %#v, want %#v", preview.Candidates, want)
	}
	if skills, err := lib.ListSkills(t.Context()); err != nil || len(skills) != 0 {
		t.Fatalf("preview wrote Library: %v, %v", skills, err)
	}
	assertGitHubPreviewRequests(t, api, fx, "first/SKILL.md", "second/SKILL.md")
}

func TestGitHubFetchTransfersSelectedSkillAndRecordsTreeSHA(t *testing.T) {
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
	preview, err := lib.PreviewFetch(t.Context(), gitHubRequest())
	if err != nil {
		t.Fatal(err)
	}
	api.Reset()
	fetched, err := getFetchedSkills(t.Context(), lib, preview, []string{"first"})
	if err != nil || len(fetched) != 1 || fetched[0].Name != "first" {
		t.Fatalf("Fetch = %#v, %v", fetched, err)
	}
	wantFiles := []library.File{
		{Path: "SKILL.md", Content: []byte(skillDocument("first", "first body")), Mode: library.ModeRegular},
		{Path: "data.bin", Content: []byte{0, 1, 2, 255}, Mode: library.ModeRegular},
		{Path: "scripts/run", Content: []byte{0, 255, 1}, Mode: library.ModeExecutable},
	}
	if !reflect.DeepEqual(fetched[0].Files, wantFiles) {
		t.Fatalf("fetched files = %#v, want %#v", fetched[0].Files, wantFiles)
	}
	wantSHA := fx.treeSHA("first")
	if fetched[0].GitTreeSHA != wantSHA {
		t.Fatalf("Fetch GitTreeSHA = %q, want %q", fetched[0].GitTreeSHA, wantSHA)
	}
	if got := readPersistedGitTreeSHA(t, lib, fetched[0].ID); got != wantSHA {
		t.Fatalf("persisted GitTreeSHA = %q, want %q", got, wantSHA)
	}
	commits, trees, _ := gitHubAPIKindCounts(t, api)
	if commits != 0 || trees != 0 {
		t.Fatalf("Fetch commits=%d trees=%d, want blobs only: %#v", commits, trees, api.snapshot())
	}
	assertGitHubBlobPaths(t, api, "first/data.bin", "first/scripts/run")
	if _, err := lib.Fetch(t.Context(), preview.ID, []string{"first"}); !library.IsCode(err, library.NotFound) {
		t.Fatalf("successful handle reused: %v", err)
	}
}

func TestGitHubFetchUsesPinnedCommitAfterHeadAdvances(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md": {content: []byte(skillDocument("demo", "pinned")), mode: 0o644},
		"demo/note.md":  {content: []byte("original note"), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	wantSHA := fx.treeSHA("demo")
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)

	lib := openTestLibrary(t)
	preview, err := lib.PreviewFetch(t.Context(), gitHubRequest())
	if err != nil {
		t.Fatal(err)
	}
	if preview.Revision != fx.commit {
		t.Fatalf("PreviewFetch.Revision = %q, want %q", preview.Revision, fx.commit)
	}

	writeFile(t, filepath.Join(repo, "demo", "SKILL.md"), []byte(skillDocument("demo", "advanced")), 0o644)
	writeFile(t, filepath.Join(repo, "demo", "note.md"), []byte("advanced note"), 0o644)
	commitGit(t, repo, "advance")
	advanced := inspectGitHubRepo(t, repo)
	api.Swap(advanced)
	if advanced.commit == fx.commit {
		t.Fatal("fixture HEAD did not advance")
	}

	fetched, err := getFetchedSkills(t.Context(), lib, preview, []string{"demo"})
	if err != nil || len(fetched) != 1 {
		t.Fatalf("Fetch = %#v, %v", fetched, err)
	}
	if fetched[0].Source.LastSeenRef != fx.commit {
		t.Fatalf("LastSeenRef = %q, want pinned %q", fetched[0].Source.LastSeenRef, fx.commit)
	}
	want := []library.File{
		{Path: "SKILL.md", Content: []byte(skillDocument("demo", "pinned")), Mode: library.ModeRegular},
		{Path: "note.md", Content: []byte("original note"), Mode: library.ModeRegular},
	}
	if !reflect.DeepEqual(fetched[0].Files, want) {
		t.Fatalf("fetched advanced HEAD content = %#v", fetched[0].Files)
	}
	if fetched[0].GitTreeSHA != wantSHA {
		t.Fatalf("GitTreeSHA = %q, want pinned %q", fetched[0].GitTreeSHA, wantSHA)
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
	preview, err := lib.PreviewFetch(t.Context(), gitHubRequest())
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, skill := range preview.Candidates {
		names = append(names, skill.Name)
	}
	if !reflect.DeepEqual(names, []string{"good", "other"}) && !reflect.DeepEqual(names, []string{"other", "good"}) {
		t.Fatalf("fallback candidates = %#v", preview)
	}
	fetched, err := getFetchedSkills(t.Context(), lib, preview, []string{"good"})
	if err != nil || len(fetched) != 1 {
		t.Fatalf("fallback fetch = %#v, %v", fetched, err)
	}
	want := []library.File{
		{Path: "SKILL.md", Content: []byte(skillDocument("good", "body")), Mode: library.ModeRegular},
		{Path: "scripts/run", Content: []byte{9, 8, 7}, Mode: library.ModeExecutable},
	}
	if !reflect.DeepEqual(fetched[0].Files, want) {
		t.Fatalf("fallback files = %#v", fetched[0].Files)
	}
	if got := readPersistedGitTreeSHA(t, lib, fetched[0].ID); got != gitRevParse(t, repo, "HEAD:good") {
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
	preview, err := lib.PreviewFetch(t.Context(), gitHubRequest())
	if err != nil || len(preview.Candidates) != 1 || preview.Candidates[0].Name != "demo" {
		t.Fatalf("truncated fallback = %#v, %v", preview, err)
	}
	fetched, err := getFetchedSkills(t.Context(), lib, preview, candidateNames(preview))
	if err != nil || fetched[0].GitTreeSHA != fx.treeSHA("demo") {
		t.Fatalf("truncated fetch = %#v, %v", fetched, err)
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
	preview, err := lib.PreviewFetch(t.Context(), gitHubRequest())
	if err != nil || len(preview.Candidates) != 1 {
		t.Fatalf("incomplete blob fallback = %#v, %v", preview, err)
	}
	fetched, err := getFetchedSkills(t.Context(), lib, preview, candidateNames(preview))
	if err != nil || string(fetched[0].Files[0].Content) != skillDocument("demo", "body") {
		t.Fatalf("incomplete blob fetch = %#v, %v", fetched, err)
	}
}

func TestGitHubFetchFailureKeepsHandleForRetry(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644},
		"demo/extra.md": {content: []byte("asset"), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)

	lib := openTestLibrary(t)
	preview, err := lib.PreviewFetch(t.Context(), gitHubRequest())
	if err != nil {
		t.Fatal(err)
	}
	api.setFailBlobs(true)
	fetched, err := lib.Fetch(t.Context(), preview.ID, []string{"demo"})
	if err == nil || len(fetched) != 0 || !library.IsCode(err, library.SourceUnreachable) || library.IsCode(err, library.Internal) {
		t.Fatalf("failed fetch = %#v, %v; want SourceUnreachable", fetched, err)
	}
	if skills, err := lib.ListSkills(t.Context()); err != nil || len(skills) != 0 {
		t.Fatalf("failed fetch wrote Library: %v, %v", skills, err)
	}
	api.setFailBlobs(false)
	fetched, err = lib.Fetch(t.Context(), preview.ID, []string{"demo"})
	if err != nil || len(fetched) != 1 || fetched[0].Name != "demo" {
		t.Fatalf("retry fetch = %#v, %v", fetched, err)
	}
}

func TestGitHubFetchMapsForbiddenBlobsToPermission(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644},
		"demo/extra.md": {content: []byte("asset"), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)

	lib := openTestLibrary(t)
	preview, err := lib.PreviewFetch(t.Context(), gitHubRequest())
	if err != nil {
		t.Fatal(err)
	}
	api.setBlobStatus(http.StatusForbidden)
	fetched, err := lib.Fetch(t.Context(), preview.ID, []string{"demo"})
	if err == nil || len(fetched) != 0 || !library.IsCode(err, library.Permission) {
		t.Fatalf("forbidden blob = %#v, %v; want Permission", fetched, err)
	}
	redirectGitHubClone(t, repo)
	fetched, err = lib.Fetch(t.Context(), preview.ID, []string{"demo"})
	if err != nil || len(fetched) != 1 || fetched[0].Name != "demo" {
		t.Fatalf("retry after forbidden blob = %#v, %v", fetched, err)
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
			preview, err := lib.PreviewFetch(t.Context(), gitHubRequest())
			if err != nil || len(preview.Candidates) != 1 || preview.Candidates[0].Name != "demo" {
				t.Fatalf("token retry PreviewFetch = %#v, %v", preview, err)
			}
			if !api.sawAnonymousThenToken() {
				t.Fatalf("requests = %#v, want anonymous %d then token retry", api.snapshot(), status)
			}
			fetched, err := getFetchedSkills(t.Context(), lib, preview, candidateNames(preview))
			if err != nil || fetched[0].GitTreeSHA != fx.treeSHA("demo") {
				t.Fatalf("token retry fetch = %#v, %v", fetched, err)
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
	_, err := lib.PreviewFetch(t.Context(), gitHubRequest())
	if err == nil || strings.Contains(err.Error(), token) {
		t.Fatalf("error exposed token: %v", err)
	}
}

func TestGitHubPreviewHonorsSubpathAndNamesWithoutChangingSource(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"skills/first/SKILL.md":  {content: []byte(skillDocument("first", "one")), mode: 0o644},
		"skills/second/SKILL.md": {content: []byte(skillDocument("second", "two")), mode: 0o644},
		"other/SKILL.md":         {content: []byte(skillDocument("other", "nope")), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)

	lib := openTestLibrary(t)
	spec := gitHubRequest()
	spec.GitHub.Input = "owner/repo@first"
	spec.GitHub.Subpath = "skills"
	preview, err := lib.PreviewFetch(t.Context(), spec)
	if err != nil || len(preview.Candidates) != 1 || preview.Candidates[0].Name != "first" || preview.Candidates[0].Path != "skills/first" {
		t.Fatalf("filtered PreviewFetch = %#v, %v", preview, err)
	}
	assertNoBlobPath(t, api, "other/SKILL.md")
	fetched, err := getFetchedSkills(t.Context(), lib, preview, candidateNames(preview))
	if err != nil || len(fetched) != 1 || fetched[0].Name != "first" {
		t.Fatalf("filtered fetch = %#v, %v", fetched, err)
	}
	if fetched[0].GitTreeSHA != fx.treeSHA("skills/first") {
		t.Fatalf("GitTreeSHA = %q, want %q", fetched[0].GitTreeSHA, fx.treeSHA("skills/first"))
	}
}

func TestGitHubRootSkillPreviewAndFetchUseRootTreeSHA(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"SKILL.md":                   {content: []byte(skillDocument("root-skill", "root")), mode: 0o644},
		"notes.md":                   {content: []byte("extra"), mode: 0o644},
		"references/nested/SKILL.md": {content: []byte(skillDocument("nested", "keep")), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	serveGitHubAPI(t, fx)
	blockGitHubClone(t)

	lib := openTestLibrary(t)
	preview, err := lib.PreviewFetch(t.Context(), gitHubRequest())
	if err != nil || len(preview.Candidates) != 1 || preview.Candidates[0].Path != "." || preview.Candidates[0].Name != "root-skill" {
		t.Fatalf("root PreviewFetch = %#v, %v", preview, err)
	}
	fetched, err := getFetchedSkills(t.Context(), lib, preview, candidateNames(preview))
	if err != nil || fetched[0].GitTreeSHA != fx.rootTree {
		t.Fatalf("root fetch = %#v, %v", fetched, err)
	}
	want := []library.File{
		{Path: "SKILL.md", Content: []byte(skillDocument("root-skill", "root")), Mode: library.ModeRegular},
		{Path: "notes.md", Content: []byte("extra"), Mode: library.ModeRegular},
		{Path: "references/nested/SKILL.md", Content: []byte(skillDocument("nested", "keep")), Mode: library.ModeRegular},
	}
	if !reflect.DeepEqual(fetched[0].Files, want) {
		t.Fatalf("root files = %#v", fetched[0].Files)
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
	preview, err := lib.PreviewFetch(t.Context(), gitHubRequest())
	if err != nil || len(preview.Candidates) != 1 || preview.Candidates[0].Name != "good" || len(preview.Diagnostics) != 1 {
		t.Fatalf("unsupported object fallback = %#v, %v", preview, err)
	}
	fetched, err := getFetchedSkills(t.Context(), lib, preview, []string{"good"})
	if err != nil || string(fetched[0].Files[0].Content) != skillDocument("good", "body") {
		t.Fatalf("unsupported object fetch = %#v, %v", fetched, err)
	}
}

// 并发上限与实现里的 fetchConcurrency 一致。
const gitHubFetchConcurrency = 8

func TestGitHubPreviewReadsSkillMarkdownConcurrentlyWithinLimitAndKeepsOrder(t *testing.T) {
	files := map[string]repoFile{}
	var names []string
	for i := range 20 {
		name := fmt.Sprintf("skill-%02d", i)
		names = append(names, name)
		files[name+"/SKILL.md"] = repoFile{content: []byte(skillDocument(name, "body")), mode: 0o644}
	}
	// 同名候选仍是遍历顺序里先到的留下,即使后到的先读完。
	files["skill-99/SKILL.md"] = repoFile{content: []byte(skillDocument("skill-00", "duplicate")), mode: 0o644}
	fx := inspectGitHubRepo(t, writeGitHubRepo(t, files))
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)
	api.holdBlobs(50 * time.Millisecond)

	preview, err := openTestLibrary(t).PreviewFetch(t.Context(), gitHubRequest())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, skill := range preview.Candidates {
		got = append(got, skill.Name)
	}
	if !slices.Equal(got, names) {
		t.Fatalf("candidates = %v, want %v", got, names)
	}
	wantDiagnostics := []library.PreviewDiagnostic{{Path: "skill-99", Reason: "duplicate skill name, first occurrence kept"}}
	if !reflect.DeepEqual(preview.Diagnostics, wantDiagnostics) {
		t.Fatalf("diagnostics = %#v, want %#v", preview.Diagnostics, wantDiagnostics)
	}
	if peak := api.peakBlobs(); peak != gitHubFetchConcurrency {
		t.Fatalf("concurrent SKILL.md reads peaked at %d, want %d", peak, gitHubFetchConcurrency)
	}
}

func TestGitHubFetchReadsSelectedFilesConcurrentlyWithinLimit(t *testing.T) {
	files := map[string]repoFile{}
	for _, name := range []string{"first", "second", "third"} {
		files[name+"/SKILL.md"] = repoFile{content: []byte(skillDocument(name, "body")), mode: 0o644}
		for i := range 5 {
			files[fmt.Sprintf("%s/ref/%d.md", name, i)] = repoFile{content: fmt.Appendf(nil, "%s %d", name, i), mode: 0o644}
		}
	}
	fx := inspectGitHubRepo(t, writeGitHubRepo(t, files))
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)

	lib := openTestLibrary(t)
	preview, err := lib.PreviewFetch(t.Context(), gitHubRequest())
	if err != nil {
		t.Fatal(err)
	}
	api.holdBlobs(50 * time.Millisecond)
	fetched, err := getFetchedSkills(t.Context(), lib, preview, candidateNames(preview))
	if err != nil || len(fetched) != 3 {
		t.Fatalf("Fetch = %#v, %v", fetched, err)
	}
	for _, skill := range fetched {
		if len(skill.Files) != 6 {
			t.Fatalf("%s has %d files, want 6", skill.Name, len(skill.Files))
		}
		for _, file := range skill.Files {
			want := files[skill.Name+"/"+file.Path].content
			if !slices.Equal(file.Content, want) {
				t.Fatalf("%s/%s = %q, want %q", skill.Name, file.Path, file.Content, want)
			}
		}
	}
	if peak := api.peakBlobs(); peak != gitHubFetchConcurrency {
		t.Fatalf("concurrent blob fetches peaked at %d, want %d", peak, gitHubFetchConcurrency)
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

// gitHubRequest 是对 owner/repo 的 main 分支做入库预览的请求;显式给出 ref,不发现默认分支。
func gitHubRequest() library.PreviewFetchRequest {
	return library.PreviewFetchRequest{GitHub: &library.GitHubInput{Input: "owner/repo", Ref: "main"}}
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
	redirectGitHubRemote(t, "file://"+repo)
}

// redirectGitHubRemote 把 owner/repo 的 HTTPS 与 SSH 远端都改写到 remote,并隔离本机 gh 登录态。
func redirectGitHubRemote(t *testing.T, remote string) {
	t.Helper()
	stubGitHubCLI(t, "exit 1\n")
	config := filepath.Join(t.TempDir(), "gitconfig")
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	runGit(t, t.TempDir(), "config", "--file", config, "url."+remote+".insteadOf", "https://github.com/owner/repo.git")
	runGit(t, t.TempDir(), "config", "--file", config, "--add", "url."+remote+".insteadOf", "git@github.com:owner/repo.git")
}

// serveGitHubCloneOnly 让 GitHub API 一律 500,并把 owner/repo 的克隆重定向到本地仓库 repo:
// 入库预览与入库走浅克隆回退。
func serveGitHubCloneOnly(t *testing.T, repo string) {
	t.Helper()
	serveGitHubRemoteOnly(t, "file://"+repo)
}

// serveGitHubRemoteOnly 让 GitHub API 一律 500,并把 owner/repo 的 git 远端改写到 remote。
func serveGitHubRemoteOnly(t *testing.T, remote string) {
	t.Helper()
	redirectGitHubRemote(t, remote)
	client := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: fetchTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusInternalServerError, Body: io.NopCloser(strings.NewReader("boom")), Header: make(http.Header)}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = client })
}

// gitHubCloneRequest 让 owner/repo 走浅克隆回退并指向本地仓库 repository,返回对其 main 分支做入库预览的请求。
func gitHubCloneRequest(t *testing.T, repository string) library.PreviewFetchRequest {
	t.Helper()
	serveGitHubCloneOnly(t, repository)
	return gitHubRequest()
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
	history            []gitHubFixture // Swap 之前服务过的各个 head
	// blobGate 若非 nil,对 gatedBlob 这个 SHA 的 blob 请求先过闸;用来把 Fetch 的物化阶段停在网络上。
	blobGate  *networkGate
	gatedBlob string
	// blobHold 非零时每个 blob 请求在途停留这么久,blobPeak 记下同时在途的最大数,用来观察并发度。
	blobHold     time.Duration
	blobInFlight int
	blobPeak     int
}

// holdBlobs 让之后的每个 blob 请求在途停留 hold,并清零并发峰值。
func (m *gitHubAPIMock) holdBlobs(hold time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.blobHold, m.blobInFlight, m.blobPeak = hold, 0, 0
}

func (m *gitHubAPIMock) peakBlobs() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.blobPeak
}

// gateBlob 让指定 SHA 的 blob 请求受 gate 控制;须在发出请求前调用。
func (m *gitHubAPIMock) gateBlob(gate *networkGate, sha string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.blobGate, m.gatedBlob = gate, sha
}

// head 在当前与历史 head 里找第一个满足 match 的。
func (m *gitHubAPIMock) head(match func(gitHubFixture) bool) (gitHubFixture, bool) {
	heads := append([]gitHubFixture{m.fx}, m.history...)
	i := slices.IndexFunc(heads, match)
	if i < 0 {
		return gitHubFixture{}, false
	}
	return heads[i], true
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

// Swap 把远端推进到新的 fixture;之前服务过的 commit 及其 tree 仍可按 SHA 取到,如同真实仓库里的历史对象。
func (m *gitHubAPIMock) Swap(fx gitHubFixture) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.history = append(m.history, cloneGitHubFixture(m.fx))
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
	gate, gated := m.blobGate, m.gatedBlob
	m.mu.Unlock()
	if gate != nil && r.URL.Path == "/repos/owner/repo/git/blobs/"+gated {
		gate.pass()
	}
	if strings.HasPrefix(r.URL.Path, "/repos/owner/repo/git/blobs/") {
		m.mu.Lock()
		hold := m.blobHold
		m.blobInFlight++
		m.blobPeak = max(m.blobPeak, m.blobInFlight)
		m.mu.Unlock()
		time.Sleep(hold)
		defer func() {
			m.mu.Lock()
			m.blobInFlight--
			m.mu.Unlock()
		}()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	authed := r.Header.Get("Authorization") != ""
	m.requests = append(m.requests, gitHubAPIRequest{path: r.URL.Path, query: r.URL.RawQuery, authed: authed, accept: r.Header.Get("Accept")})
	if m.anonymousStatus != 0 && !authed {
		return m.errorResponse(m.anonymousStatus), nil
	}
	if ref, ok := strings.CutPrefix(r.URL.Path, "/repos/owner/repo/commits/"); ok {
		head, found := m.fx, ref == "main"
		if !found {
			head, found = m.head(func(h gitHubFixture) bool { return strings.EqualFold(ref, h.commit) })
		}
		if !found {
			return jsonResponse(http.StatusNotFound, map[string]string{"message": "Not Found"}), nil
		}
		return jsonResponse(http.StatusOK, map[string]any{
			"sha": head.commit,
			"commit": map[string]any{
				"tree": map[string]string{"sha": head.rootTree},
			},
		}), nil
	}
	if sha, ok := strings.CutPrefix(r.URL.Path, "/repos/owner/repo/git/trees/"); ok {
		head, found := m.head(func(h gitHubFixture) bool { return sha == h.rootTree || sha == h.commit })
		if !found {
			return jsonResponse(http.StatusNotFound, map[string]string{"message": "Not Found"}), nil
		}
		return jsonResponse(http.StatusOK, map[string]any{
			"sha":       head.rootTree,
			"tree":      head.entries,
			"truncated": head.truncated,
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
			t.Fatalf("preview requested extra blob %s (%v)", sha, api.blobPaths())
		}
	}
}

func assertGitHubFetchBlobs(t *testing.T, api *gitHubAPIMock, fx gitHubFixture, skillPath string) {
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
			t.Fatal("Fetch re-resolved ref")
		}
		sha, ok := strings.CutPrefix(req.path, "/repos/owner/repo/git/blobs/")
		if !ok {
			continue
		}
		if !allowed[sha] {
			t.Fatalf("Fetch requested blob outside %s: %s (%v)", skillPath, sha, api.blobPaths())
		}
	}
	var requested []string
	for _, p := range api.blobPaths() {
		if p != "" {
			requested = append(requested, p)
		}
	}
	if len(requested) == 0 {
		t.Fatal("Fetch requested no blobs")
	}
}

func assertNoBlobPath(t *testing.T, api *gitHubAPIMock, filePath string) {
	t.Helper()
	for _, p := range api.blobPaths() {
		if p == filePath {
			t.Fatalf("requested %s", filePath)
		}
	}
}
