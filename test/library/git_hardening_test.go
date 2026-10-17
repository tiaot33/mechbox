package library_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tiaot33/skills-tui/internal/library"
)

func hangingGitServer(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func TestFetchesRegularSkillWhenRepositoryDeclaresLFSWithoutGitLFS(t *testing.T) {
	repository := t.TempDir()
	runGit(t, repository, "init", "-b", "main")
	writeFile(t, filepath.Join(repository, "demo", "SKILL.md"), []byte(skillDocument("demo", "plain skill")), 0o644)
	writeFile(t, filepath.Join(repository, ".gitattributes"), []byte("*.bin filter=lfs diff=lfs merge=lfs -text\n"), 0o644)
	writeFile(t, filepath.Join(repository, "blobs", "data.bin"), []byte("version https://git-lfs.github.com/spec/v1\noid sha256:88d4266fd4e6338d13b845fcf289579d209c897823b9217da3e161936f031589\nsize 4\n"), 0o644)
	lfsArgs := []string{"-c", "filter.lfs.smudge=", "-c", "filter.lfs.process=", "-c", "filter.lfs.required=false"}
	runGit(t, repository, append(lfsArgs, "add", ".")...)
	runGit(t, repository, append(lfsArgs, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "lfs")...)

	marker := filepath.Join(t.TempDir(), "lfs-invoked")
	bin := t.TempDir()
	writeFile(t, filepath.Join(bin, "git-lfs"), []byte("#!/bin/sh\necho invoked > \"$GIT_LFS_MARKER\"\nexit 1\n"), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GIT_LFS_MARKER", marker)

	lib := openTestLibrary(t)
	preview, err := lib.PreviewFetch(t.Context(), gitHubCloneRequest(t, repository))
	if err != nil {
		t.Fatalf("PreviewFetch with LFS attributes and no git-lfs: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("git-lfs was invoked during PreviewFetch")
	}
	fetched, err := lib.Fetch(t.Context(), preview.ID, []string{"demo"})
	if err != nil || len(fetched) != 1 || fetched[0].Name != "demo" {
		t.Fatalf("Fetch = %#v, %v", fetched, err)
	}
	if got := string(skillFiles(t, lib, fetched[0])[0].Content); !strings.Contains(got, "plain skill") {
		t.Fatalf("fetched content = %q", got)
	}
}

func TestPreviewRejectsExtGitProtocol(t *testing.T) {
	sentinel := filepath.Join(t.TempDir(), "pwned")
	// 公开输入只接受 github.com;用户 git 配置仍可把它改写成 ext 协议,Library 必须拒绝执行。
	serveGitHubRemoteOnly(t, "ext::touch "+sentinel)
	runGit(t, t.TempDir(), "config", "--file", os.Getenv("GIT_CONFIG_GLOBAL"), "protocol.ext.allow", "always")

	lib := openTestLibrary(t)
	_, err := lib.PreviewFetch(t.Context(), gitHubRequest())
	if _, statErr := os.Stat(sentinel); statErr == nil {
		t.Fatal("ext protocol executed a host command")
	}
	if err == nil {
		t.Fatal("PreviewFetch accepted ext protocol")
	}
}

func TestPreviewGitTimesOutWithoutCallerDeadline(t *testing.T) {
	t.Setenv("SKILLS_GIT_TIMEOUT", "1s")
	lib := openTestLibrary(t)
	start := time.Now()
	serveGitHubRemoteOnly(t, hangingGitServer(t))
	_, err := lib.PreviewFetch(context.Background(), gitHubRequest())
	elapsed := time.Since(start)
	if elapsed > 10*time.Second {
		t.Fatalf("PreviewFetch hung for %v without independent git timeout", elapsed)
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		t.Fatalf("timeout leaked caller context error: %v", err)
	}
	if !library.IsCode(err, library.SourceUnreachable) || !strings.Contains(strings.ToLower(err.Error()), "timed out") {
		t.Fatalf("timeout error = %v", err)
	}
}

func TestCheckUpdatesAndUpdateGitTimeOutWithoutCallerDeadline(t *testing.T) {
	repository, _ := githubRemote(t)
	t.Setenv("SKILLS_GIT_TIMEOUT", "1s")
	lib := openTestLibrary(t)
	preview, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: "owner/repo"})
	if err != nil {
		t.Fatal(err)
	}
	fetched, err := lib.Fetch(t.Context(), preview.ID, candidateNames(preview))
	if err != nil {
		t.Fatal(err)
	}
	remote := hangingGitServer(t)
	config := os.Getenv("GIT_CONFIG_GLOBAL")
	runGit(t, repository, "config", "--file", config, "--unset-all", "url.file://"+repository+".insteadOf")
	runGit(t, repository, "config", "--file", config, "url."+remote+"/.insteadOf", "https://github.com/")
	runGit(t, repository, "config", "--file", config, "--add", "url."+remote+"/.insteadOf", "git@github.com:")

	for _, name := range []string{"CheckUpdates", "Update"} {
		t.Run(name, func(t *testing.T) {
			start := time.Now()
			var err error
			if name == "CheckUpdates" {
				_, err = checkSkillUpdate(t, lib, t.Context(), fetched[0].ID)
			} else {
				_, err = updateSkill(t, lib, t.Context(), fetched[0].ID)
			}
			elapsed := time.Since(start)
			if elapsed > 10*time.Second {
				t.Fatalf("%s hung for %v without independent git timeout", name, elapsed)
			}
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				t.Fatalf("timeout leaked caller context error: %v", err)
			}
			if !library.IsCode(err, library.SourceUnreachable) || !strings.Contains(strings.ToLower(err.Error()), "timed out") {
				t.Fatalf("timeout error = %v", err)
			}
		})
	}
}

func TestPreviewGitCancelStopsImmediately(t *testing.T) {
	lib := openTestLibrary(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		cancel()
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	serveGitHubRemoteOnly(t, server.URL)
	start := time.Now()
	_, err := lib.PreviewFetch(ctx, gitHubRequest())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("in-flight cancel = %v", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatal("cancel did not stop git")
	}
}

func TestPreviewGitDistinguishesFailureReasonsWithoutExposingCredentials(t *testing.T) {
	repository, _ := githubRemote(t)
	lib := openTestLibrary(t)
	secret := "secret-token-value"

	forbidden := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(forbidden.Close)
	authURL := strings.Replace(forbidden.URL, "://", "://user:"+secret+"@", 1)

	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closed.Close()

	for _, tc := range []struct {
		name    string
		remote  string
		ref     string
		code    library.ErrorCode
		message string
	}{
		{
			name:    "authentication",
			remote:  authURL,
			ref:     "trunk",
			code:    library.Permission,
			message: "authentication failed",
		},
		{
			name:    "missing ref",
			remote:  "file://" + repository,
			ref:     "does-not-exist",
			code:    library.NotFound,
			message: "ref not found",
		},
		{
			name:    "unreachable",
			remote:  closed.URL,
			ref:     "trunk",
			code:    library.SourceUnreachable,
			message: "could not be reached",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			serveGitHubRemoteOnly(t, tc.remote)
			_, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: "owner/repo", Ref: tc.ref})
			if !library.IsCode(err, tc.code) || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("error = %v, want %s containing %q", err, tc.code, tc.message)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("error exposed credentials: %v", err)
			}
		})
	}
}
