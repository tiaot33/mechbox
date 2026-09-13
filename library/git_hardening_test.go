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

	"github.com/tiaot33/skills-tui/library"
)

func hangingGitServer(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func TestFetchInstallsRegularSkillWhenRepositoryDeclaresLFSWithoutGitLFS(t *testing.T) {
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
	fetch, err := lib.Fetch(t.Context(), library.SourceSpec{
		Type:    library.SourceGitHub,
		Locator: library.SourceLocator{Repository: repository, Branch: "main"},
	})
	if err != nil {
		t.Fatalf("Fetch with LFS attributes and no git-lfs: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("git-lfs was invoked during Fetch")
	}
	installed, err := lib.InstallFetch(t.Context(), fetch.ID, []string{"demo"})
	if err != nil || len(installed) != 1 || installed[0].Name != "demo" {
		t.Fatalf("InstallFetch = %#v, %v", installed, err)
	}
	if got := string(installed[0].Files[0].Content); !strings.Contains(got, "plain skill") {
		t.Fatalf("installed content = %q", got)
	}
}

func TestFetchRejectsExtGitProtocol(t *testing.T) {
	sentinel := filepath.Join(t.TempDir(), "pwned")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	runGit(t, t.TempDir(), "config", "--file", os.Getenv("GIT_CONFIG_GLOBAL"), "protocol.ext.allow", "always")

	lib := openTestLibrary(t)
	_, err := lib.Fetch(t.Context(), library.SourceSpec{
		Type:    library.SourceGitHub,
		Locator: library.SourceLocator{Repository: "ext::touch " + sentinel, Branch: "main"},
	})
	if _, statErr := os.Stat(sentinel); statErr == nil {
		t.Fatal("ext protocol executed a host command")
	}
	if err == nil {
		t.Fatal("Fetch accepted ext protocol")
	}
}

func TestFetchGitTimesOutWithoutCallerDeadline(t *testing.T) {
	t.Setenv("SKILLS_GIT_TIMEOUT", "1s")
	lib := openTestLibrary(t)
	start := time.Now()
	_, err := lib.Fetch(context.Background(), library.SourceSpec{
		Type:    library.SourceGitHub,
		Locator: library.SourceLocator{Repository: hangingGitServer(t), Branch: "main"},
	})
	elapsed := time.Since(start)
	if elapsed > 10*time.Second {
		t.Fatalf("Fetch hung for %v without independent git timeout", elapsed)
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		t.Fatalf("timeout leaked caller context error: %v", err)
	}
	if !library.IsCode(err, library.SourceUnreachable) || !strings.Contains(strings.ToLower(err.Error()), "timed out") {
		t.Fatalf("timeout error = %v", err)
	}
}

func TestCheckUpdateAndUpdateGitTimeOutWithoutCallerDeadline(t *testing.T) {
	repository, _ := githubRemote(t)
	t.Setenv("SKILLS_GIT_TIMEOUT", "1s")
	lib := openTestLibrary(t)
	spec, err := lib.ResolveSource(t.Context(), "owner/repo", library.ResolveSourceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	fetch, err := lib.Fetch(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	installed, err := lib.InstallFetch(t.Context(), fetch.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	remote := hangingGitServer(t)
	config := os.Getenv("GIT_CONFIG_GLOBAL")
	runGit(t, repository, "config", "--file", config, "--unset-all", "url.file://"+repository+".insteadOf")
	runGit(t, repository, "config", "--file", config, "url."+remote+"/.insteadOf", "https://github.com/")
	runGit(t, repository, "config", "--file", config, "--add", "url."+remote+"/.insteadOf", "git@github.com:")

	for _, name := range []string{"CheckUpdate", "Update"} {
		t.Run(name, func(t *testing.T) {
			start := time.Now()
			var err error
			if name == "CheckUpdate" {
				_, err = lib.CheckUpdate(context.Background(), installed[0].ID)
			} else {
				_, err = lib.Update(context.Background(), installed[0].ID)
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

func TestFetchGitCancelStopsImmediately(t *testing.T) {
	lib := openTestLibrary(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		cancel()
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	start := time.Now()
	_, err := lib.Fetch(ctx, library.SourceSpec{
		Type:    library.SourceGitHub,
		Locator: library.SourceLocator{Repository: server.URL, Branch: "main"},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("in-flight cancel = %v", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatal("cancel did not stop git")
	}
}

func TestFetchGitDistinguishesFailureReasonsWithoutExposingCredentials(t *testing.T) {
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
		locator library.SourceLocator
		code    library.ErrorCode
		message string
	}{
		{
			name:    "authentication",
			locator: library.SourceLocator{Repository: authURL, Branch: "trunk"},
			code:    library.Permission,
			message: "authentication failed",
		},
		{
			name:    "missing ref",
			locator: library.SourceLocator{Repository: repository, Branch: "does-not-exist"},
			code:    library.SourceUnreachable,
			message: "ref not found",
		},
		{
			name:    "unreachable",
			locator: library.SourceLocator{Repository: closed.URL, Branch: "trunk"},
			code:    library.SourceUnreachable,
			message: "could not be reached",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := lib.Fetch(t.Context(), library.SourceSpec{Type: library.SourceGitHub, Locator: tc.locator})
			if !library.IsCode(err, tc.code) || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("error = %v, want %s containing %q", err, tc.code, tc.message)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("error exposed credentials: %v", err)
			}
		})
	}
}
