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

	"github.com/tiaot33/skills-tui/library"
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
		_, err := lib.Fetch(t.Context(), library.SourceSpec{Type: library.SourceGitHub, Locator: library.SourceLocator{Repository: repository, Branch: "trunk"}, Subpath: subpath})
		if !library.IsCode(err, library.InvalidArgument) {
			t.Fatalf("subpath %q: %v", subpath, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { cancel(); <-r.Context().Done() }))
	t.Cleanup(server.Close)
	_, err := lib.Fetch(ctx, library.SourceSpec{Type: library.SourceGitHub, Locator: library.SourceLocator{Repository: server.URL, Branch: "trunk"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("in-flight cancel = %v", err)
	}
}

func TestGitSourceClassifiesPermissionWithoutExposingCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	t.Cleanup(server.Close)
	lib := openTestLibrary(t)
	location := strings.Replace(server.URL, "://", "://secret-user:secret-password@", 1)
	_, err := lib.Fetch(t.Context(), library.SourceSpec{Type: library.SourceGitHub, Locator: library.SourceLocator{Repository: location, Branch: "trunk"}})
	if !library.IsCode(err, library.Permission) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("permission = %v", err)
	}
}

func TestFetchUsesConfiguredSSHAuthenticationWithoutPrompts(t *testing.T) {
	for _, setting := range []string{"core.sshCommand", "GIT_SSH", "GIT_SSH_COMMAND"} {
		t.Run(setting, func(t *testing.T) {
			repository, _ := githubRemote(t)
			t.Setenv("GIT_SSH_COMMAND", "")
			t.Setenv("GIT_SSH", "")
			t.Setenv("GIT_SSH_VARIANT", "ssh")
			t.Setenv("TEST_SSH_REPOSITORY", repository)
			wrapper := filepath.Join(t.TempDir(), "ssh wrapper")
			writeFile(t, wrapper, []byte("#!/bin/sh\ncase \" $* \" in *' -oBatchMode=yes '*) ;; *) exit 9 ;; esac\n[ \"$GIT_TERMINAL_PROMPT\" = 0 ] || exit 10\nexec git-upload-pack \"$TEST_SSH_REPOSITORY\"\n"), 0o755)
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
			fetch, err := lib.Fetch(ctx, library.SourceSpec{Type: library.SourceGitHub, Locator: library.SourceLocator{Repository: "git@example.invalid:repo", Branch: "trunk"}})
			if err != nil || len(fetch.Skills) != 1 || fetch.Skills[0].Name != "example" {
				t.Fatalf("configured SSH = %#v, %v", fetch, err)
			}
		})
	}
}

func TestResolveSelectsTreeAndNameWithoutSplittingSource(t *testing.T) {
	repository, _ := githubRemote(t)
	if err := os.Remove(filepath.Join(repository, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"first", "second"} {
		writeFile(t, filepath.Join(repository, "skills", name, "SKILL.md"), []byte(skillDocument(name, "selected")), 0o644)
	}
	commitGit(t, repository, "multiple skills")
	lib := openTestLibrary(t)
	var sourceID library.SourceID
	for _, tc := range []struct{ input, subpath, name string }{
		{"https://github.com/owner/repo/tree/trunk/skills/first", "skills/first", "first"},
		{"owner/repo@second", "", "second"},
	} {
		spec, err := lib.ResolveSource(t.Context(), tc.input, library.ResolveSourceOptions{})
		if err != nil || spec.Subpath != tc.subpath {
			t.Fatalf("resolve = %#v, %v", spec, err)
		}
		fetch, err := lib.Fetch(t.Context(), spec)
		if err != nil || len(fetch.Skills) != 1 || fetch.Skills[0].Name != tc.name || fetch.Skills[0].Path != "skills/"+tc.name {
			t.Fatalf("fetch = %#v, %v", fetch, err)
		}
		installed, err := lib.InstallFetch(t.Context(), fetch.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		if sourceID != 0 && sourceID != installed[0].Source.ID {
			t.Fatal("subpath split Source")
		}
		sourceID = installed[0].Source.ID
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
		if available, err := lib.CheckUpdate(t.Context(), skill.ID); err != nil || !available {
			t.Fatalf("check = %v, %v", available, err)
		}
	}
	runGit(t, repository, "branch", "feature/topic")
	if _, err := lib.ResolveSource(t.Context(), "https://github.com/owner/repo/tree/feature/topic/skills", library.ResolveSourceOptions{}); !library.IsCode(err, library.InvalidArgument) {
		t.Fatalf("slash ref without explicit ref: %v", err)
	}
	spec, err := lib.ResolveSource(t.Context(), "https://github.com/owner/repo/tree/feature/topic/skills", library.ResolveSourceOptions{Ref: "feature/topic"})
	if err != nil || spec.Subpath != "skills" {
		t.Fatalf("explicit tree = %#v, %v", spec, err)
	}
	if fetch, err := lib.Fetch(t.Context(), spec); err != nil || len(fetch.Skills) != 2 {
		t.Fatalf("explicit tree fetch = %#v, %v", fetch, err)
	}
}

func TestResolveAndFetchRejectBadInputsAndPreserveCancellation(t *testing.T) {
	_, _ = githubRemote(t)
	lib := openTestLibrary(t)
	for _, input := range []string{"", "owner", "https://gitlab.com/owner/repo", "https://token@github.com/owner/repo", "https://github.com/owner/repo/tree/trunk/../outside", "owner/repo#", "owner/repo@", "owner/repo#--upload-pack=bad", "owner/repo#bad..ref"} {
		if _, err := lib.ResolveSource(t.Context(), input, library.ResolveSourceOptions{}); !library.IsCode(err, library.InvalidArgument) {
			t.Errorf("%q: %v", input, err)
		}
	}
	spec, err := lib.ResolveSource(t.Context(), "owner/repo#does-not-exist", library.ResolveSourceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = lib.Fetch(t.Context(), spec)
	requireErrorDetails(t, err, library.NotFound, library.ErrorDetails{Name: "does-not-exist"})
	spec, err = lib.ResolveSource(t.Context(), "owner/repo@missing", library.ResolveSourceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = lib.Fetch(t.Context(), spec)
	requireErrorDetails(t, err, library.NotFound, library.ErrorDetails{Name: "missing"})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := lib.ResolveSource(ctx, "owner/repo", library.ResolveSourceOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel resolve = %v", err)
	}
	if _, err := lib.Fetch(ctx, spec); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel fetch = %v", err)
	}
}

func TestResolveGitHubInputsFetchTheRequestedRefAndShareSource(t *testing.T) {
	repository, sha := githubRemote(t)
	runGit(t, repository, "tag", "v1")
	lib := openTestLibrary(t)
	var sourceID library.SourceID
	for _, input := range []string{"owner/repo", "https://github.com/Owner/Repo.git", "git@github.com:owner/repo.git", "ssh://git@github.com/owner/repo", "owner/repo#trunk"} {
		spec, err := lib.ResolveSource(t.Context(), input, library.ResolveSourceOptions{})
		if err != nil || spec.Locator.Repository != "https://github.com/owner/repo.git" || spec.Locator.Branch != "trunk" {
			t.Fatalf("resolve %q = %#v, %v", input, spec, err)
		}
		fetch, err := lib.Fetch(t.Context(), spec)
		if err != nil || fetch.Ref != sha {
			t.Fatalf("fetch = %#v, %v", fetch, err)
		}
		if sourceID == 0 {
			installed, err := lib.InstallFetch(t.Context(), fetch.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			sourceID = installed[0].Source.ID
		} else {
			if _, err := lib.InstallFetch(t.Context(), fetch.ID, nil); err == nil {
				t.Fatalf("equivalent input created another Source: %v", err)
			}
		}
	}
	collections, err := lib.ListCollections(t.Context())
	if err != nil || len(collections) != 1 {
		t.Fatalf("collections = %#v, %v", collections, err)
	}
	writeFile(t, filepath.Join(repository, "SKILL.md"), []byte(skillDocument("example", "new")), 0o644)
	commitGit(t, repository, "advance")
	for _, ref := range []string{"v1", sha, strings.ToUpper(sha)} {
		spec, err := lib.ResolveSource(t.Context(), "owner/repo", library.ResolveSourceOptions{Ref: ref})
		if err != nil {
			t.Fatal(err)
		}
		fetch, err := lib.Fetch(t.Context(), spec)
		if err != nil || fetch.Ref != sha {
			t.Fatalf("ref %s: %#v, %v", ref, fetch, err)
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
	if _, err := lib.ResolveSource(t.Context(), "owner/repo", library.ResolveSourceOptions{}); !library.IsCode(err, library.Permission) {
		t.Fatalf("resolve fallback error = %v", err)
	}
	if _, err := lib.Fetch(t.Context(), library.SourceSpec{Type: library.SourceGitHub, Locator: library.SourceLocator{Repository: "https://github.com/owner/repo.git", Branch: "trunk"}}); !library.IsCode(err, library.Permission) {
		t.Fatalf("fetch fallback error = %v", err)
	}
}
