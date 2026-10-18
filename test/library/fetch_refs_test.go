package library_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/tiaot33/mechbox/internal/library"
)

// recordHTTP 让所有 HTTP 请求失败,并记下请求过的地址。
func recordHTTP(t *testing.T) func() []string {
	t.Helper()
	var mu sync.Mutex
	var requested []string
	client := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: fetchTransport(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		requested = append(requested, r.URL.String())
		return &http.Response{StatusCode: http.StatusInternalServerError, Body: io.NopCloser(strings.NewReader("boom")), Request: r}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = client })
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), requested...)
	}
}

// recordGit 在 PATH 前面放一个记下每次 git 调用的参数、再交给真正 git 的脚本。
func recordGit(t *testing.T) func() []string {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	writeFile(t, filepath.Join(dir, "git"), []byte("#!/bin/sh\necho \"$*\" >> '"+log+"'\nexec '"+real+"' \"$@\"\n"), 0o755)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() []string {
		content, err := os.ReadFile(log)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		return strings.Split(strings.TrimSpace(string(content)), "\n")
	}
}

func requireNoClone(t *testing.T, calls []string) {
	t.Helper()
	for _, call := range calls {
		for field := range strings.FieldsSeq(call) {
			if field == "clone" || field == "fetch" {
				t.Fatalf("git %s; want only ls-remote", call)
			}
		}
	}
}

// refsRepository 在 repository 上加分支 release/v2、轻量 tag v1 与附注 tag v2。
func refsRepository(t *testing.T, repository string) {
	t.Helper()
	runGit(t, repository, "branch", "release/v2")
	runGit(t, repository, "tag", "v1")
	runGit(t, repository, "tag", "-a", "v2", "-m", "v2")
}

func TestInputWithoutSchemeStartingWithDomainGetsHTTPS(t *testing.T) {
	t.Run("github", func(t *testing.T) {
		_, sha := githubRemote(t)
		lib := openTestLibrary(t)
		for _, input := range []string{"github.com/owner/repo", "GitHub.com/owner/repo#trunk"} {
			preview, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: input})
			if err != nil {
				t.Fatalf("%s: %v", input, err)
			}
			if want := (library.Source{Type: library.SourceGitHub, Label: "owner/repo", Ref: "trunk", ViewURL: preview.Source.ViewURL}); preview.Source != want || preview.Revision != sha {
				t.Fatalf("%s: source = %#v, revision %s; want %#v at %s", input, preview.Source, preview.Revision, want, sha)
			}
		}
	})
	t.Run("other domain", func(t *testing.T) {
		isolateGit(t)
		var requested []string
		client := http.DefaultClient
		http.DefaultClient = &http.Client{Transport: fetchTransport(func(r *http.Request) (*http.Response, error) {
			requested = append(requested, r.URL.String())
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("---\nname: example\ndescription: d\n---\nbody")), Request: r}, nil
		})}
		t.Cleanup(func() { http.DefaultClient = client })
		lib := openTestLibrary(t)
		preview, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: "git.company.com/org/repo"})
		if err != nil {
			t.Fatal(err)
		}
		if preview.Source.Type != library.SourceURL || len(requested) == 0 || requested[0] != "https://git.company.com/org/repo" {
			t.Fatalf("source = %#v, requests %v; want the https link probed as a download first", preview.Source, requested)
		}
	})
	t.Run("shorthand and paths unchanged", func(t *testing.T) {
		requested := recordHTTP(t)
		marker := forbidGit(t)
		lib := openTestLibrary(t)
		// 第一段不带点的 a/b 仍是 GitHub 简写,交给 git 解析分支,不补协议。
		if _, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: "owner/repo.name"}); !library.IsCode(err, library.SourceUnreachable) {
			t.Fatalf("owner/repo.name = %v; want a GitHub repository", err)
		}
		if _, err := os.Stat(marker); err != nil {
			t.Fatalf("owner/repo.name did not reach git: %v", err)
		}
		// ./ 开头的路径即使第一段带点也是本地目录。
		dir := t.TempDir()
		writeLocalSkill(t, filepath.Join(dir, "a.b"), "body")
		t.Chdir(dir)
		preview, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: "./a.b"})
		if err != nil || preview.Source.Type != library.SourceLocal {
			t.Fatalf("./a.b = %#v, %v; want a local path", preview.Source, err)
		}
		if got := requested(); len(got) != 0 {
			t.Fatalf("requests = %v", got)
		}
	})
}

func TestListSourceRefsForGitHubUsesOnlyLsRemote(t *testing.T) {
	repository, _ := githubRemote(t)
	refsRepository(t, repository)
	requested := recordHTTP(t)
	calls := recordGit(t)
	lib := openTestLibrary(t)
	want := library.SourceRefs{Branches: []string{"release/v2", "trunk"}, Tags: []string{"v1", "v2"}, DefaultBranch: "trunk"}
	for input, ref := range map[string]string{
		"owner/repo":            "",
		"owner/repo@example":    "",
		"github.com/owner/repo": "",
		"https://github.com/owner/repo/tree/release/v2/skills":            "release/v2",
		"https://github.com/owner/repo/tree/trunk":                        "trunk",
		"git@github.com:owner/repo.git#v1":                                "v1",
		"https://github.com/owner/repo/tree/trunk/skills#trunk":           "trunk",
		"https://github.com/owner/repo/tree/release/v2/skills#release/v2": "release/v2",
	} {
		got, err := lib.ListSourceRefs(t.Context(), input)
		want.InputRef = ref
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("%s = %#v, %v; want %#v", input, got, err, want)
		}
	}
	if got := requested(); len(got) != 0 {
		t.Fatalf("GitHub requests = %v; want none", got)
	}
	requireNoClone(t, calls())
}

func TestListSourceRefsLeavesUnknownDefaultBranchEmpty(t *testing.T) {
	repository, _ := githubRemote(t)
	runGit(t, repository, "checkout", "--quiet", "--detach")
	lib := openTestLibrary(t)
	got, err := lib.ListSourceRefs(t.Context(), "owner/repo")
	if err != nil || got.DefaultBranch != "" || !reflect.DeepEqual(got.Branches, []string{"trunk"}) {
		t.Fatalf("refs = %#v, %v", got, err)
	}
}

func TestListSourceRefsForGitLabAndSSHDoesNotClone(t *testing.T) {
	config := isolateGit(t)
	repository := skillRepository(t)
	refsRepository(t, repository)
	redirectRemote(t, config, repository, "https://gitlab.com/group/repo.git")
	redirectRemote(t, config, repository, "https://git.example.com/org/repo.git")
	wrapper := filepath.Join(t.TempDir(), "ssh")
	writeFile(t, wrapper, []byte("#!/bin/sh\ncase \" $* \" in *' -oBatchMode=yes '*) ;; *) exit 9 ;; esac\nexec git-upload-pack \"$TEST_SSH_REPOSITORY\"\n"), 0o755)
	t.Setenv("GIT_SSH_COMMAND", "'"+wrapper+"'")
	t.Setenv("GIT_SSH_VARIANT", "ssh")
	t.Setenv("TEST_SSH_REPOSITORY", repository)
	requested := recordHTTP(t)
	calls := recordGit(t)
	lib := openTestLibrary(t)
	want := library.SourceRefs{Branches: []string{"main", "release/v2"}, Tags: []string{"v1", "v2"}, DefaultBranch: "main"}
	for input, ref := range map[string]string{
		"gitlab.com/group/repo":                                  "",
		"gitlab:group/repo#v2":                                   "v2",
		"https://gitlab.com/group/repo/-/tree/release/v2/skills": "release/v2",
		"git@git.example.com:org/repo.git":                       "",
		"ssh://git@git.example.com/org/repo#main":                "main",
		"https://git.example.com/org/repo.git":                   "",
	} {
		got, err := lib.ListSourceRefs(t.Context(), input)
		want.InputRef = ref
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("%s = %#v, %v; want %#v", input, got, err, want)
		}
	}
	if got := requested(); len(got) != 0 {
		t.Fatalf("requests = %v; want none", got)
	}
	requireNoClone(t, calls())
}

func TestListSourceRefsRejectsInputsThatAreNotGitRepositoriesWithoutNetwork(t *testing.T) {
	requested := recordHTTP(t)
	marker := forbidGit(t)
	lib := openTestLibrary(t)
	for _, input := range []string{
		"",
		t.TempDir(),
		"./skills",
		"https://github.com/owner/repo/archive/refs/heads/main.zip",
		"raw.githubusercontent.com/owner/repo/main/SKILL.md",
		"https://example.com/skills.zip",
		"example.com/skills",
		"owner",
	} {
		_, err := lib.ListSourceRefs(t.Context(), input)
		requireErrorDetails(t, err, library.InvalidArgument, library.ErrorDetails{Field: "input"})
	}
	if got := requested(); len(got) != 0 {
		t.Fatalf("requests = %v; want none", got)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("git was called; want no network access")
	}
}

func TestListSourceRefsRejectsBadRefsBeforeContactingRemote(t *testing.T) {
	forbidden := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	t.Cleanup(forbidden.Close)
	redirectGitHubRemote(t, forbidden.URL+"/owner/repo.git")
	config := os.Getenv("GIT_CONFIG_GLOBAL")
	runGit(t, t.TempDir(), "config", "--file", config, "url."+forbidden.URL+"/.insteadOf", "https://")
	recordHTTP(t)
	calls := recordGit(t)
	lib := openTestLibrary(t)
	for _, input := range []string{
		"owner/repo#bad..ref",
		"https://git.example.com/org/repo.git#bad..ref",
		// tree 链接与 #ref 指向不同的分支,入库预览无论选哪个都会报错。
		"https://github.com/owner/repo/tree/main/skills#v1",
		"https://gitlab.com/group/repo/-/tree/main/skills#v1",
	} {
		_, err := lib.ListSourceRefs(t.Context(), input)
		requireErrorDetails(t, err, library.InvalidArgument, library.ErrorDetails{Field: "input"})
		_, err = lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: input})
		requireErrorDetails(t, err, library.InvalidArgument, library.ErrorDetails{Field: "input"})
	}
	for _, call := range calls() {
		if strings.Contains(call, "ls-remote") {
			t.Fatalf("git %s; want the remote untouched", call)
		}
	}
}

func TestListSourceRefsFailsLikePreviewFetch(t *testing.T) {
	missing := "file://" + filepath.Join(t.TempDir(), "missing")
	forbidden := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	t.Cleanup(forbidden.Close)
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T)
		input string
		code  library.ErrorCode
	}{
		{name: "github missing", input: "owner/repo", code: library.SourceUnreachable, setup: func(t *testing.T) { redirectGitHubRemote(t, missing) }},
		{name: "git missing", input: "https://git.example.com/org/missing.git", code: library.SourceUnreachable, setup: func(t *testing.T) { isolateGit(t) }},
		{name: "github forbidden", input: "owner/repo", code: library.Permission, setup: func(t *testing.T) { redirectGitHubRemote(t, forbidden.URL+"/owner/repo.git") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.setup(t)
			recordHTTP(t)
			lib := openTestLibrary(t)
			_, previewErr := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: tc.input})
			_, err := lib.ListSourceRefs(t.Context(), tc.input)
			preview := requireLibraryError(t, previewErr, tc.code)
			got := requireLibraryError(t, err, tc.code)
			if got.Details != preview.Details {
				t.Fatalf("details = %#v; preview %#v", got.Details, preview.Details)
			}
		})
	}
}
