package library_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tiaot33/skills-tui/library"
)

func TestGitHubAnonymousRateLimitExpiresForLibrary(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644},
	})
	api := serveGitHubAPI(t, inspectGitHubRepo(t, repo))
	blockGitHubClone(t)
	t.Setenv("GITHUB_TOKEN", "test-token")
	reset := time.Now().Add(2 * time.Second).Truncate(time.Second)
	api.anonymousStatus = http.StatusForbidden
	api.rateLimitRemaining = "0"
	api.rateLimitReset = strconv.FormatInt(reset.Unix(), 10)
	lib := openTestLibrary(t)
	if _, err := lib.Fetch(t.Context(), gitHubSpec()); err != nil {
		t.Fatal(err)
	}
	api.Reset()
	if _, err := lib.Fetch(t.Context(), gitHubSpec()); err != nil {
		t.Fatal(err)
	}
	for _, req := range api.snapshot() {
		if !req.authed {
			t.Fatal("Fetch retried anonymous API before rate limit reset")
		}
	}
	api.Reset()
	if _, err := openTestLibrary(t).Fetch(t.Context(), gitHubSpec()); err != nil {
		t.Fatal(err)
	}
	if !api.sawAnonymousThenToken() {
		t.Fatal("another Library inherited the rate limit window")
	}
	time.Sleep(time.Until(reset) + time.Millisecond)
	api.anonymousStatus = 0
	api.Reset()
	if _, err := lib.Fetch(t.Context(), gitHubSpec()); err != nil {
		t.Fatal(err)
	}
	for _, req := range api.snapshot() {
		if req.authed {
			t.Fatal("Fetch did not return to anonymous API after reset")
		}
	}
}

func TestGitHubCLIAuthenticationPreviewsAndInstallsWithoutEnvironmentToken(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md":    {content: []byte(skillDocument("demo", "body")), mode: 0o644},
		"demo/scripts/run": {content: []byte{0, 255, 1}, mode: 0o755},
	})
	fx := inspectGitHubRepo(t, repo)
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusTooManyRequests} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			api := serveGitHubAPI(t, fx)
			api.anonymousStatus = status
			blockGitHubClone(t)
			serveGitHubCLI(t, fx)
			t.Setenv("GH_HOST", "unrelated.example")

			lib := openTestLibrary(t)
			fetch, err := lib.Fetch(t.Context(), gitHubSpec())
			if err != nil || fetch.Ref != fx.commit || len(fetch.Skills) != 1 || fetch.Skills[0].Name != "demo" {
				t.Fatalf("authenticated preview = %#v, %v", fetch, err)
			}
			if skills, err := lib.ListSkills(t.Context()); err != nil || len(skills) != 0 {
				t.Fatalf("preview wrote Library: %#v, %v", skills, err)
			}
			installed, err := lib.InstallFetch(t.Context(), fetch.ID, nil)
			if err != nil || len(installed) != 1 {
				t.Fatalf("authenticated install = %#v, %v", installed, err)
			}
			want := []library.File{
				{Path: "SKILL.md", Content: []byte(skillDocument("demo", "body")), Mode: library.ModeRegular},
				{Path: "scripts/run", Content: []byte{0, 255, 1}, Mode: library.ModeExecutable},
			}
			if !reflect.DeepEqual(installed[0].Files, want) || installed[0].GitTreeSHA != fx.treeSHA("demo") {
				t.Fatalf("authenticated content = %#v", installed[0])
			}
			if _, err := lib.ListFetchSkills(t.Context(), fetch.ID); !library.IsCode(err, library.NotFound) {
				t.Fatalf("successful install kept handle: %v", err)
			}
		})
	}
}

func TestGitHubInstallRateLimitFallsBackToPinnedSelectedSkills(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"skills/first/SKILL.md":    {content: []byte(skillDocument("first", "pinned")), mode: 0o644},
		"skills/first/scripts/run": {content: []byte{0, 255, 1}, mode: 0o755},
		"skills/second/SKILL.md":   {content: []byte(skillDocument("second", "unselected")), mode: 0o644},
		"other/SKILL.md":           {content: []byte(skillDocument("first", "outside subpath")), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, fx)
	redirectGitHubClone(t, repo)
	lib := openTestLibrary(t)
	spec := gitHubSpec()
	spec.Subpath = "skills"
	fetch, err := lib.Fetch(t.Context(), spec)
	if err != nil || fetch.Ref != fx.commit || len(fetch.Skills) != 2 {
		t.Fatalf("preview = %#v, %v", fetch, err)
	}
	writeFile(t, filepath.Join(repo, "skills/first/scripts/run"), []byte("new HEAD"), 0o755)
	commitGit(t, repo, "advance HEAD after preview")
	api.setBlobStatus(http.StatusTooManyRequests)
	api.rateLimitReset = strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
	installed, err := lib.InstallFetch(t.Context(), fetch.ID, []string{"first"})
	if err != nil || len(installed) != 1 {
		t.Fatalf("rate limited install = %#v, %v", installed, err)
	}
	want := []library.File{
		{Path: "SKILL.md", Content: []byte(skillDocument("first", "pinned")), Mode: library.ModeRegular},
		{Path: "scripts/run", Content: []byte{0, 255, 1}, Mode: library.ModeExecutable},
	}
	if !reflect.DeepEqual(installed[0].Files, want) || installed[0].GitTreeSHA != fx.treeSHA("skills/first") || installed[0].Source.LastSeenRef != fx.commit || installed[0].Source.Locator != spec.Locator {
		t.Fatalf("fallback lost pinned content or Source identity: %#v", installed[0])
	}
	if skills, err := lib.ListSkills(t.Context()); err != nil || len(skills) != 1 || skills[0].Name != "first" {
		t.Fatalf("installed unselected skills: %#v, %v", skills, err)
	}
	if _, err := lib.ListFetchSkills(t.Context(), fetch.ID); !library.IsCode(err, library.NotFound) {
		t.Fatalf("successful fallback kept handle: %v", err)
	}
}

func TestGitHubInstallRateLimitFailureRemainsAtomicAndRetryable(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"first/SKILL.md":  {content: []byte(skillDocument("first", "body")), mode: 0o644},
		"first/data":      {content: []byte{0, 1}, mode: 0o644},
		"second/SKILL.md": {content: []byte(skillDocument("second", "body")), mode: 0o644},
		"second/data":     {content: []byte{0, 255}, mode: 0o755},
	})
	fx := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)
	lib := openTestLibrary(t)
	fetch, err := lib.Fetch(t.Context(), gitHubSpec())
	if err != nil {
		t.Fatal(err)
	}
	transport := http.DefaultClient.Transport
	http.DefaultClient.Transport = fetchTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/"+fx.blobSHA("second/data")) {
			response := jsonResponse(http.StatusTooManyRequests, map[string]string{"message": "rate limited"})
			response.Header.Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
			return response, nil
		}
		return transport.RoundTrip(r)
	})
	if installed, err := lib.InstallFetch(t.Context(), fetch.ID, nil); err == nil || len(installed) != 0 || (!library.IsCode(err, library.SourceUnreachable) && !library.IsCode(err, library.Permission)) {
		t.Fatalf("unavailable install = %#v, %v", installed, err)
	}
	if skills, err := lib.ListSkills(t.Context()); err != nil || len(skills) != 0 {
		t.Fatalf("failed install wrote partial Library: %#v, %v", skills, err)
	}
	if candidates, err := lib.ListFetchSkills(t.Context(), fetch.ID); err != nil || !reflect.DeepEqual(candidates, fetch.FetchCandidates) {
		t.Fatalf("failed install changed candidates: %#v, %v", candidates, err)
	}
	redirectGitHubClone(t, repo)
	api.Reset()
	installed, err := lib.InstallFetch(t.Context(), fetch.ID, nil)
	if err != nil || len(installed) != 2 || installed[0].GitTreeSHA != fx.treeSHA("first") || installed[1].GitTreeSHA != fx.treeSHA("second") {
		t.Fatalf("retry = %#v, %v", installed, err)
	}
	if len(api.snapshot()) != 0 {
		t.Fatalf("retry used anonymous API during rate limit window: %#v", api.snapshot())
	}
	if _, err := lib.ListFetchSkills(t.Context(), fetch.ID); !library.IsCode(err, library.NotFound) {
		t.Fatalf("successful retry kept handle: %v", err)
	}
}

func TestGitHubExpiredEnvironmentTokenUsesCLIAuthentication(t *testing.T) {
	fx := inspectGitHubRepo(t, writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644},
		"demo/data":     {content: []byte{0, 255}, mode: 0o644},
	}))
	api := serveGitHubAPI(t, fx)
	api.anonymousStatus = http.StatusNotFound
	blockGitHubClone(t)
	serveGitHubCLI(t, fx)
	t.Setenv("GITHUB_TOKEN", "expired-github-token")
	t.Setenv("GH_TOKEN", "expired-gh-token")
	transport := http.DefaultClient.Transport
	http.DefaultClient.Transport = fetchTransport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "" {
			return jsonResponse(http.StatusUnauthorized, map[string]string{"message": "expired token"}), nil
		}
		return transport.RoundTrip(r)
	})
	lib := openTestLibrary(t)
	fetch, err := lib.Fetch(t.Context(), gitHubSpec())
	if err != nil {
		t.Fatal(err)
	}
	installed, err := lib.InstallFetch(t.Context(), fetch.ID, nil)
	if err != nil || len(installed) != 1 || installed[0].GitTreeSHA != fx.treeSHA("demo") || !reflect.DeepEqual(installed[0].Files[1].Content, []byte{0, 255}) {
		t.Fatalf("expired token blocked CLI authentication: %#v, %v", installed, err)
	}
}

func TestGitHubRateLimitedFetchSkipsAnonymousWithoutEnvironmentToken(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644},
		"demo/data":     {content: []byte{0, 255}, mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	for _, cliAvailable := range []bool{true, false} {
		t.Run(fmt.Sprintf("CLI=%v", cliAvailable), func(t *testing.T) {
			api := serveGitHubAPI(t, fx)
			api.anonymousStatus = http.StatusTooManyRequests
			api.rateLimitReset = strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
			if cliAvailable {
				blockGitHubClone(t)
				serveGitHubCLI(t, fx)
			} else {
				redirectGitHubClone(t, repo)
			}
			lib := openTestLibrary(t)
			if _, err := lib.Fetch(t.Context(), gitHubSpec()); err != nil {
				t.Fatal(err)
			}
			api.Reset()
			fetch, err := lib.Fetch(t.Context(), gitHubSpec())
			if err != nil {
				t.Fatal(err)
			}
			installed, err := lib.InstallFetch(t.Context(), fetch.ID, nil)
			if err != nil || len(installed) != 1 || installed[0].GitTreeSHA != fx.treeSHA("demo") {
				t.Fatalf("rate limited install = %#v, %v", installed, err)
			}
			if len(api.snapshot()) != 0 {
				t.Fatalf("retried anonymous API during rate limit window: %#v", api.snapshot())
			}
		})
	}
}

func TestGitHubDoesNotRememberBareForbiddenOrExpiredRateLimit(t *testing.T) {
	fx := inspectGitHubRepo(t, writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644},
	}))
	for _, status := range []int{http.StatusForbidden, http.StatusTooManyRequests} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			api := serveGitHubAPI(t, fx)
			api.anonymousStatus = status
			api.rateLimitRemaining = "10"
			reset := time.Now().Add(time.Hour)
			if status == http.StatusTooManyRequests {
				reset = time.Now().Add(-time.Minute)
			}
			api.rateLimitReset = strconv.FormatInt(reset.Unix(), 10)
			blockGitHubClone(t)
			t.Setenv("GH_TOKEN", "test-gh-token")
			lib := openTestLibrary(t)
			if _, err := lib.Fetch(t.Context(), gitHubSpec()); err != nil {
				t.Fatal(err)
			}
			api.Reset()
			if _, err := lib.Fetch(t.Context(), gitHubSpec()); err != nil || !api.sawAnonymousThenToken() {
				t.Fatalf("Fetch incorrectly skipped anonymous API: %v, %#v", err, api.snapshot())
			}
		})
	}
}

func TestGitHubRateLimitedChecksAndUpdatesKeepTreeSHASemantics(t *testing.T) {
	for _, auth := range []string{"token", "cli", "clone"} {
		t.Run(auth, func(t *testing.T) {
			repo := writeGitHubRepo(t, map[string]repoFile{
				"first/SKILL.md":  {content: []byte(skillDocument("first", "body")), mode: 0o644},
				"first/data":      {content: []byte{0, 1}, mode: 0o755},
				"second/SKILL.md": {content: []byte(skillDocument("second", "body")), mode: 0o644},
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
				t.Fatalf("install = %#v, %v", installed, err)
			}
			api.anonymousStatus = http.StatusTooManyRequests
			api.rateLimitReset = strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
			api.setFailBlobs(true)
			switch auth {
			case "token":
				t.Setenv("GH_TOKEN", "test-token")
			case "cli":
				treeOnly := fx
				treeOnly.blobs = nil
				serveGitHubCLI(t, treeOnly)
			case "clone":
				redirectGitHubClone(t, repo)
			}
			if available, err := lib.CheckUpdate(ctx, installed[0].ID); err != nil || available {
				t.Fatalf("unchanged check = %v, %v", available, err)
			}
			api.Reset()
			checks, err := lib.CheckUpdates(ctx, []library.SkillID{installed[0].ID, installed[1].ID, installed[0].ID})
			if err != nil || len(checks) != 2 || checks[0].Error != nil || checks[1].Error != nil || checks[0].Available || checks[1].Available {
				t.Fatalf("unchanged batch = %#v, %v", checks, err)
			}
			if auth == "token" {
				_, trees, blobs := gitHubAPIKindCounts(t, api)
				if trees != 1 || blobs != 0 {
					t.Fatalf("batch fetched %d trees and %d blobs", trees, blobs)
				}
			}
			for _, before := range installed {
				if after, err := lib.GetSkill(ctx, before.ID); err != nil || !reflect.DeepEqual(after, before) {
					t.Fatalf("checks wrote Library: %#v, %v", after, err)
				}
			}
			result, err := lib.Update(ctx, installed[0].ID)
			if err != nil || result.Changed || result.Skill.GitTreeSHA != fx.treeSHA("first") {
				t.Fatalf("unchanged update = %#v, %v", result, err)
			}
			if versions, err := lib.ListVersions(ctx, installed[0].ID); err != nil || len(versions) != 1 {
				t.Fatalf("unchanged update wrote history: %#v, %v", versions, err)
			}
			writeFile(t, filepath.Join(repo, "first/data"), []byte{0, 255}, 0o755)
			commitGit(t, repo, "change first")
			changed := inspectGitHubRepo(t, repo)
			api.Swap(changed)
			api.setFailBlobs(false)
			if auth == "cli" {
				serveGitHubCLI(t, changed)
			}
			api.Reset()
			checks, err = lib.CheckUpdates(ctx, []library.SkillID{installed[0].ID, installed[1].ID})
			if err != nil || len(checks) != 2 || checks[0].Error != nil || checks[1].Error != nil || !checks[0].Available || checks[1].Available {
				t.Fatalf("changed batch = %#v, %v", checks, err)
			}
			if auth == "token" {
				_, trees, _ := gitHubAPIKindCounts(t, api)
				if trees != 1 {
					t.Fatalf("changed batch fetched %d trees", trees)
				}
			}
			result, err = lib.Update(ctx, installed[0].ID)
			if err != nil || !result.Changed || result.Skill.GitTreeSHA != changed.treeSHA("first") || result.Skill.Source.LastSeenRef != changed.commit {
				t.Fatalf("changed update = %#v, %v", result, err)
			}
			if !reflect.DeepEqual(result.Skill.Files[1], library.File{Path: "data", Content: []byte{0, 255}, Mode: library.ModeExecutable}) {
				t.Fatalf("updated content = %#v", result.Skill.Files)
			}
			if versions, err := lib.ListVersions(ctx, installed[0].ID); err != nil || len(versions) != 2 {
				t.Fatalf("changed update history = %#v, %v", versions, err)
			}
		})
	}
}

func TestGitHubAuthenticationFailureKeepsPermissionAndHidesOutput(t *testing.T) {
	const secret = "private-token-and-cli-output"
	serveGitHubAPI(t, inspectGitHubRepo(t, writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644},
		"demo/extra":    {content: []byte("asset"), mode: 0o644},
	})))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	config := filepath.Join(t.TempDir(), "gitconfig")
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, remote := range []string{"https://github.com/owner/repo.git", "git@github.com:owner/repo.git"} {
		runGit(t, t.TempDir(), "config", "--file", config, "--add", "url."+server.URL+".insteadOf", remote)
	}
	lib := openTestLibrary(t)
	fetch, err := lib.Fetch(t.Context(), gitHubSpec())
	if err != nil {
		t.Fatal(err)
	}
	installed, err := lib.InstallFetch(t.Context(), fetch.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := lib.Fetch(t.Context(), gitHubSpec())
	if err != nil {
		t.Fatal(err)
	}
	stubGitHubCLI(t, "echo '"+secret+"'\necho '"+secret+"' >&2\nexit 1\n")
	t.Setenv("GITHUB_TOKEN", secret)
	client := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: fetchTransport(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusForbidden, map[string]string{"message": secret}), nil
	})}
	t.Cleanup(func() { http.DefaultClient = client })
	for _, operation := range []struct {
		name string
		run  func() error
	}{
		{"Fetch", func() error { _, err := lib.Fetch(t.Context(), gitHubSpec()); return err }},
		{"InstallFetch", func() error { _, err := lib.InstallFetch(t.Context(), retry.ID, nil); return err }},
		{"CheckUpdate", func() error { _, err := lib.CheckUpdate(t.Context(), installed[0].ID); return err }},
		{"Update", func() error { _, err := lib.Update(t.Context(), installed[0].ID); return err }},
	} {
		t.Run(operation.name, func(t *testing.T) {
			err := operation.run()
			if !library.IsCode(err, library.Permission) || strings.Contains(fmt.Sprintf("%+v", err), secret) {
				t.Fatalf("authentication failure = %v, want sanitized Permission", err)
			}
		})
	}
	if _, err := lib.ListFetchSkills(t.Context(), retry.ID); err != nil {
		t.Fatalf("failed install discarded handle: %v", err)
	}
}

func TestGitHubCLICancellationStopsChildProcesses(t *testing.T) {
	api := serveGitHubAPI(t, gitHubFixture{})
	api.anonymousStatus = http.StatusTooManyRequests
	blockGitHubClone(t)
	ready := filepath.Join(t.TempDir(), "ready")
	escaped := filepath.Join(t.TempDir(), "escaped")
	t.Setenv("GH_TEST_READY", ready)
	t.Setenv("GH_TEST_ESCAPED", escaped)
	stubGitHubCLI(t, `sh -c 'echo ready > "$GH_TEST_READY"; sleep 1; echo escaped > "$GH_TEST_ESCAPED"' &
wait
`)
	lib := openTestLibrary(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := lib.Fetch(ctx, gitHubSpec())
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("remote operation did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled Fetch = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled Fetch did not stop promptly")
	}
	time.Sleep(1100 * time.Millisecond)
	if _, err := os.Stat(escaped); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("child process survived cancellation: %v", err)
	}
}

func serveGitHubCLI(t *testing.T, fx gitHubFixture) {
	t.Helper()
	var script strings.Builder
	script.WriteString(`[ "$1" = api ] || exit 1
[ "$3 $4 $5 $6" = '--method GET --hostname github.com' ] || exit 1
[ -n "$GH_PROMPT_DISABLED" ] || exit 1
[ -z "$GITHUB_TOKEN$GH_TOKEN" ] || exit 1
case "$2" in
`)
	respond := func(endpoint string, payload any) {
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&script, "'%s') cat <<'JSON'\n%s\nJSON\n;;\n", endpoint, body)
	}
	respond("/repos/owner/repo/commits/main", map[string]any{
		"sha": fx.commit, "commit": map[string]any{"tree": map[string]string{"sha": fx.rootTree}},
	})
	respond("/repos/owner/repo/git/trees/"+fx.rootTree+"?recursive=1", map[string]any{
		"tree": fx.entries, "truncated": fx.truncated,
	})
	for sha, content := range fx.blobs {
		respond("/repos/owner/repo/git/blobs/"+sha, map[string]any{
			"encoding": "base64", "content": base64.StdEncoding.EncodeToString(content), "size": len(content),
		})
	}
	script.WriteString("*) exit 1 ;;\nesac\n")
	stubGitHubCLI(t, script.String())
}
