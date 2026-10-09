package library_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tiaot33/skills-tui/internal/library"
)

func githubRemote(t *testing.T) (string, string) {
	t.Helper()
	repository := t.TempDir()
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	runGit(t, repository, "init", "-b", "trunk")
	writeFile(t, filepath.Join(repository, "SKILL.md"), []byte(skillDocument("example", "original")), 0o644)
	commitGit(t, repository, "initial")
	runGit(t, repository, "config", "--file", os.Getenv("GIT_CONFIG_GLOBAL"), "url.file://"+repository+".insteadOf", "https://github.com/owner/repo.git")
	runGit(t, repository, "config", "--file", os.Getenv("GIT_CONFIG_GLOBAL"), "--add", "url.file://"+repository+".insteadOf", "git@github.com:owner/repo.git")
	client := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: fetchTransport(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("github api unavailable")
	})}
	t.Cleanup(func() { http.DefaultClient = client })
	output, err := exec.Command("git", "-C", repository, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return repository, strings.TrimSpace(string(output))
}

func TestGitSourceRejectsSymlinkSubpathsAndPreservesInFlightCancellation(t *testing.T) {
	repository, _ := githubRemote(t)
	if err := os.Symlink(t.TempDir(), filepath.Join(repository, "outside")); err != nil {
		t.Fatal(err)
	}
	commitGit(t, repository, "link")
	lib := openTestLibrary(t)
	for _, subpath := range []string{"../outside", ".git", "outside", "outside/nested"} {
		_, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{GitHub: &library.GitHubInput{Input: "owner/repo", Ref: "trunk", Subpath: subpath}})
		if !library.IsCode(err, library.InvalidArgument) {
			t.Fatalf("subpath %q: %v", subpath, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { cancel(); <-r.Context().Done() }))
	t.Cleanup(server.Close)
	serveGitHubRemoteOnly(t, server.URL)
	_, err := lib.PreviewFetch(ctx, library.PreviewFetchRequest{GitHub: &library.GitHubInput{Input: "owner/repo", Ref: "trunk"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("in-flight cancel = %v", err)
	}
}

func TestPreviewUsesConfiguredSSHAuthenticationWithoutPrompts(t *testing.T) {
	for _, setting := range []string{"core.sshCommand", "GIT_SSH", "GIT_SSH_COMMAND"} {
		t.Run(setting, func(t *testing.T) {
			repository, _ := githubRemote(t)
			t.Setenv("GIT_SSH_COMMAND", "")
			t.Setenv("GIT_SSH", "")
			t.Setenv("GIT_SSH_VARIANT", "ssh")
			t.Setenv("TEST_SSH_REPOSITORY", repository)
			wrapper := filepath.Join(t.TempDir(), "ssh wrapper")
			writeFile(t, wrapper, []byte("#!/bin/sh\ncase \" $* \" in *' -oBatchMode=yes '*) ;; *) exit 9 ;; esac\n[ \"$GIT_TERMINAL_PROMPT\" = 0 ] || exit 10\nexec git-upload-pack \"$TEST_SSH_REPOSITORY\"\n"), 0o755)
			// 把 HTTPS 远端改写成 SSH 地址,克隆必须经过配置的 SSH 命令。
			config := os.Getenv("GIT_CONFIG_GLOBAL")
			runGit(t, repository, "config", "--file", config, "--unset-all", "url.file://"+repository+".insteadOf")
			runGit(t, repository, "config", "--file", config, "url.git@example.invalid:repo.insteadOf", "https://github.com/owner/repo.git")
			switch setting {
			case "core.sshCommand":
				runGit(t, repository, "config", "--file", os.Getenv("GIT_CONFIG_GLOBAL"), setting, "'"+wrapper+"'")
			case "GIT_SSH":
				t.Setenv(setting, wrapper)
			case "GIT_SSH_COMMAND":
				t.Setenv(setting, "'"+wrapper+"'")
			}
			lib := openTestLibrary(t)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			preview, err := lib.PreviewFetch(ctx, library.PreviewFetchRequest{GitHub: &library.GitHubInput{Input: "owner/repo", Ref: "trunk"}})
			if err != nil || len(preview.Candidates) != 1 || preview.Candidates[0].Name != "example" {
				t.Fatalf("configured SSH = %#v, %v", preview, err)
			}
		})
	}
}

func TestPreviewFetchSelectsTreeAndNameWithoutSplittingSource(t *testing.T) {
	repository, _ := githubRemote(t)
	if err := os.Remove(filepath.Join(repository, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"first", "second"} {
		writeFile(t, filepath.Join(repository, "skills", name, "SKILL.md"), []byte(skillDocument(name, "selected")), 0o644)
	}
	commitGit(t, repository, "multiple skills")
	lib := openTestLibrary(t)
	var collectionID library.CollectionID
	for _, tc := range []struct{ input, name string }{
		{"https://github.com/owner/repo/tree/trunk/skills/first", "first"},
		{"owner/repo@second", "second"},
	} {
		preview, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{GitHub: &library.GitHubInput{Input: tc.input}})
		if err != nil || preview.Ref != "trunk" || len(preview.Candidates) != 1 || preview.Candidates[0].Name != tc.name || preview.Candidates[0].Path != "skills/"+tc.name {
			t.Fatalf("preview %q = %#v, %v", tc.input, preview, err)
		}
		fetched, err := lib.Fetch(t.Context(), preview.ID, candidateNames(preview))
		if err != nil {
			t.Fatal(err)
		}
		if collectionID != 0 && collectionID != fetched[0].CollectionID {
			t.Fatal("subpath split collection")
		}
		collectionID = fetched[0].CollectionID
	}
	for _, name := range []string{"first", "second"} {
		writeFile(t, filepath.Join(repository, "skills", name, "SKILL.md"), []byte(skillDocument(name, "advanced")), 0o644)
	}
	commitGit(t, repository, "advance")
	skills, err := lib.ListSkills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range skills {
		if status, err := checkSkillUpdate(t, lib, t.Context(), skill.ID); err != nil || status != library.UpdateAvailable {
			t.Fatalf("check = %v, %v", status, err)
		}
	}
	runGit(t, repository, "branch", "feature/topic")
	tree := "https://github.com/owner/repo/tree/feature/topic/skills"
	if _, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{GitHub: &library.GitHubInput{Input: tree}}); !library.IsCode(err, library.InvalidArgument) {
		t.Fatalf("slash ref without explicit ref: %v", err)
	}
	preview, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{GitHub: &library.GitHubInput{Input: tree, Ref: "feature/topic"}})
	if err != nil || preview.Ref != "feature/topic" || len(preview.Candidates) != 2 {
		t.Fatalf("explicit tree preview = %#v, %v", preview, err)
	}
	if fetched, err := lib.Fetch(t.Context(), preview.ID, candidateNames(preview)); err != nil || fetched[0].Source.ViewURL != "https://github.com/owner/repo/tree/feature/topic" {
		t.Fatalf("slash ref source = %#v, %v", fetched, err)
	}
	for _, input := range []library.GitHubInput{
		{Input: tree, Ref: "feature/topic", Subpath: "other"},
		{Input: "owner/repo#trunk", Ref: "feature/topic"},
	} {
		if _, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{GitHub: &input}); !library.IsCode(err, library.InvalidArgument) {
			t.Fatalf("conflicting input %#v: %v", input, err)
		}
	}
}

func TestPreviewFetchRejectsBadGitHubInputsAndPreservesCancellation(t *testing.T) {
	_, _ = githubRemote(t)
	lib := openTestLibrary(t)
	for _, input := range []string{"", "owner", "https://gitlab.com/owner/repo", "https://token@github.com/owner/repo", "https://github.com/owner/repo/tree/trunk/../outside", "owner/repo#", "owner/repo@", "owner/repo#--upload-pack=bad", "owner/repo#bad..ref"} {
		if _, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{GitHub: &library.GitHubInput{Input: input}}); !library.IsCode(err, library.InvalidArgument) {
			t.Errorf("%q: %v", input, err)
		}
	}
	_, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{GitHub: &library.GitHubInput{Input: "owner/repo#does-not-exist"}})
	requireErrorDetails(t, err, library.NotFound, library.ErrorDetails{Name: "does-not-exist"})
	_, err = lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{GitHub: &library.GitHubInput{Input: "owner/repo@missing"}})
	requireErrorDetails(t, err, library.NotFound, library.ErrorDetails{Name: "missing"})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, input := range []library.GitHubInput{{Input: "owner/repo"}, {Input: "owner/repo", Ref: "trunk"}} {
		if _, err := lib.PreviewFetch(ctx, library.PreviewFetchRequest{GitHub: &input}); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel preview %#v = %v", input, err)
		}
	}
}

func TestPreviewFetchDoesNotAssumeMainWhenDefaultBranchIsUnknown(t *testing.T) {
	repository, _ := githubRemote(t)
	// 远端 HEAD 游离时 ls-remote 给不出默认分支。
	runGit(t, repository, "checkout", "--quiet", "--detach")
	lib := openTestLibrary(t)
	_, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{GitHub: &library.GitHubInput{Input: "owner/repo"}})
	if !library.IsCode(err, library.SourceUnreachable) {
		t.Fatalf("unknown default branch = %v, want SourceUnreachable", err)
	}
	if preview, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{GitHub: &library.GitHubInput{Input: "owner/repo", Ref: "trunk"}}); err != nil || preview.Ref != "trunk" {
		t.Fatalf("explicit ref = %#v, %v", preview, err)
	}
}

func TestGitHubInputsPreviewTheRequestedRefAndShareSource(t *testing.T) {
	repository, sha := githubRemote(t)
	runGit(t, repository, "tag", "v1")
	lib := openTestLibrary(t)
	var fetchedID library.SkillID
	for _, input := range []string{"owner/repo", "https://github.com/Owner/Repo.git", "git@github.com:owner/repo.git", "ssh://git@github.com/owner/repo", "owner/repo#trunk"} {
		preview, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{GitHub: &library.GitHubInput{Input: input}})
		if err != nil || preview.Ref != "trunk" || preview.Revision != sha || len(preview.Candidates) != 1 {
			t.Fatalf("preview %q = %#v, %v", input, preview, err)
		}
		if fetchedID == 0 {
			fetched, err := lib.Fetch(t.Context(), preview.ID, candidateNames(preview))
			if err != nil {
				t.Fatal(err)
			}
			if got := fetched[0].Source; got.Label != "owner/repo" || got.Ref != "trunk" {
				t.Fatalf("source = %#v, want owner/repo at trunk", got)
			}
			fetchedID = fetched[0].ID
		} else if preview.Candidates[0].Fetched != fetchedID {
			t.Fatalf("equivalent input %q previews another Source: %#v", input, preview.Candidates)
		}
	}
	collections, err := lib.ListCollections(t.Context())
	if err != nil || len(collections) != 1 {
		t.Fatalf("collections = %#v, %v", collections, err)
	}
	writeFile(t, filepath.Join(repository, "SKILL.md"), []byte(skillDocument("example", "new")), 0o644)
	commitGit(t, repository, "advance")
	for _, ref := range []string{"v1", sha, strings.ToUpper(sha)} {
		preview, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{GitHub: &library.GitHubInput{Input: "owner/repo", Ref: ref}})
		if err != nil || preview.Ref != strings.ToLower(ref) || preview.Revision != sha {
			t.Fatalf("ref %s: %#v, %v", ref, preview, err)
		}
	}
}

func TestGitSourceKeepsPermissionErrorWhenOtherTransportIsUnavailable(t *testing.T) {
	repository, _ := githubRemote(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	t.Cleanup(server.Close)
	config := os.Getenv("GIT_CONFIG_GLOBAL")
	runGit(t, repository, "config", "--file", config, "--unset-all", "url.file://"+repository+".insteadOf")
	runGit(t, repository, "config", "--file", config, "url."+server.URL+"/.insteadOf", "https://github.com/")
	runGit(t, repository, "config", "--file", config, "url.file://"+filepath.Join(t.TempDir(), "missing")+"/.insteadOf", "git@github.com:")
	lib := openTestLibrary(t)
	// 省略 ref 时在默认分支发现阶段失败,给出 ref 时在克隆阶段失败,都保留 HTTPS 的认证错误。
	if _, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{GitHub: &library.GitHubInput{Input: "owner/repo"}}); !library.IsCode(err, library.Permission) {
		t.Fatalf("default branch fallback error = %v", err)
	}
	if _, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{GitHub: &library.GitHubInput{Input: "owner/repo", Ref: "trunk"}}); !library.IsCode(err, library.Permission) {
		t.Fatalf("preview fallback error = %v", err)
	}
}
