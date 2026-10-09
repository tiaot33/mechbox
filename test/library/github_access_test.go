package library_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tiaot33/skills-tui/internal/library"
)

// fakeGH 写一个假的 gh:`gh auth token` 打印 token,token 为空时表示没登录、退出码 1;每次调用记一行。
// 返回 gh 的位置与读取调用次数的函数。
func fakeGH(t *testing.T, token string) (path string, calls func() int) {
	t.Helper()
	dir := t.TempDir()
	path, log := filepath.Join(dir, "gh"), filepath.Join(dir, "calls")
	script := "#!/bin/sh\necho \"$*\" >> '" + log + "'\n"
	if token == "" {
		script += "exit 1\n"
	} else {
		script += "[ \"$1 $2\" = 'auth token' ] || exit 1\necho '" + token + "'\n"
	}
	writeFile(t, path, []byte(script), 0o755)
	return path, func() int {
		content, err := os.ReadFile(log)
		if os.IsNotExist(err) {
			return 0
		}
		if err != nil {
			t.Fatal(err)
		}
		return strings.Count(string(content), "\n")
	}
}

// fakeGHOnPath 把假的 gh 放进 PATH,用来验证没给 gh 位置时技能库自己找到它。
func fakeGHOnPath(t *testing.T, token string) func() int {
	t.Helper()
	path, calls := fakeGH(t, token)
	t.Setenv("PATH", filepath.Dir(path)+string(os.PathListSeparator)+os.Getenv("PATH"))
	return calls
}

func setGitHubSettings(t *testing.T, lib *library.Library, settings library.GitHubSettings) {
	t.Helper()
	if err := lib.SetGitHubSettings(settings); err != nil {
		t.Fatal(err)
	}
}

// privateGitHubRepo 起一个匿名读不到的 owner/repo:API 对匿名请求回 404,克隆被挡住。
func privateGitHubRepo(t *testing.T) *gitHubAPIMock {
	t.Helper()
	repo := writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md":    {content: []byte(skillDocument("demo", "body")), mode: 0o644},
		"demo/scripts/run": {content: []byte{0, 255, 1}, mode: 0o755},
	})
	api := serveGitHubAPI(t, inspectGitHubRepo(t, repo))
	api.limitAnonymous(http.StatusNotFound, "", "", "")
	blockGitHubClone(t)
	return api
}

func requirePreviewAndFetch(t *testing.T, lib *library.Library) {
	t.Helper()
	preview, err := lib.PreviewFetch(t.Context(), gitHubRequest())
	if err != nil || len(preview.Candidates) != 1 {
		t.Fatalf("preview = %#v, %v", preview, err)
	}
	fetched := fetchSkills(t, lib, preview, candidateNames(preview))
	if len(skillFiles(t, lib, fetched[0])) != 2 {
		t.Fatalf("files = %#v", skillFiles(t, lib, fetched[0]))
	}
}

func requireOnlyCredential(t *testing.T, got []string, want string) {
	t.Helper()
	if len(got) == 0 || slices.ContainsFunc(got, func(token string) bool { return token != want }) {
		t.Fatalf("credentials = %q, want only %q", got, want)
	}
}

func TestGitHubUsesTokenBeforeGH(t *testing.T) {
	api := privateGitHubRepo(t)
	api.addAccount("pat", "alice")
	api.addAccount("gh-token", "bob")
	gh, ghCalls := fakeGH(t, "gh-token")
	lib := openTestLibrary(t)
	setGitHubSettings(t, lib, library.GitHubSettings{Token: "pat", AllowGH: true, GHPath: gh})

	requirePreviewAndFetch(t, lib)
	requireOnlyCredential(t, api.takeCredentials(), "pat")
	if n := ghCalls(); n != 0 {
		t.Fatalf("gh called %d times while the token works", n)
	}
}

func TestGitHubRejectedTokenFallsBackToGH(t *testing.T) {
	for _, location := range []string{"configured", "PATH"} {
		t.Run(location, func(t *testing.T) {
			api := privateGitHubRepo(t)
			api.addAccount("gh-token", "bob")
			settings := library.GitHubSettings{Token: "expired", AllowGH: true}
			var ghCalls func() int
			if location == "configured" {
				settings.GHPath, ghCalls = fakeGH(t, "gh-token")
			} else {
				ghCalls = fakeGHOnPath(t, "gh-token")
			}
			lib := openTestLibrary(t)
			setGitHubSettings(t, lib, settings)

			requirePreviewAndFetch(t, lib)
			credentials := api.takeCredentials()
			if credentials[0] != "expired" {
				t.Fatalf("first request used %q, want the token", credentials[0])
			}
			// 令牌被拒记下之后,之后的请求不再先试令牌;gh 的登录只读一次。
			requireOnlyCredential(t, credentials[1:], "gh-token")
			if n := ghCalls(); n != 1 {
				t.Fatalf("gh called %d times, want 1", n)
			}
		})
	}
}

func TestGitHubDoesNotCallGHWhenNotAllowed(t *testing.T) {
	for _, settings := range []library.GitHubSettings{{}, {Token: "expired"}} {
		t.Run("token "+strconv.Quote(settings.Token), func(t *testing.T) {
			api := privateGitHubRepo(t)
			api.addAccount("gh-token", "bob")
			ghCalls := fakeGHOnPath(t, "gh-token")
			lib := openTestLibrary(t)
			setGitHubSettings(t, lib, settings)

			if _, err := lib.PreviewFetch(t.Context(), gitHubRequest()); err == nil {
				t.Fatal("PreviewFetch read a private repository without an allowed identity")
			}
			access, err := lib.CheckGitHubAccess(t.Context())
			if err != nil || access.Identity != library.GitHubAnonymous || access.GH != (library.GHAccess{Status: library.GHDisabled}) {
				t.Fatalf("access = %#v, %v", access, err)
			}
			if n := ghCalls(); n != 0 {
				t.Fatalf("gh called %d times while not allowed", n)
			}
		})
	}
}

func TestGitHubSettingsChangeDropsRememberedRejection(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644}})
	api := serveGitHubAPI(t, inspectGitHubRepo(t, repo))
	blockGitHubClone(t)
	lib := openTestLibrary(t)
	settings := library.GitHubSettings{Token: "pat"}
	setGitHubSettings(t, lib, settings)
	preview := func() []string {
		t.Helper()
		if _, err := lib.PreviewFetch(t.Context(), gitHubRequest()); err != nil {
			t.Fatal(err)
		}
		return api.takeCredentials()
	}

	// GitHub 还不认这个令牌:被拒后匿名读公开仓库。
	if got := preview(); got[0] != "pat" || slices.Contains(got[1:], "pat") {
		t.Fatalf("credentials = %q, want the token once then anonymous", got)
	}
	api.addAccount("pat", "alice")
	requireOnlyCredential(t, preview(), "")

	// 重新交一次设置,哪怕内容相同,之前的判断也作废。
	setGitHubSettings(t, lib, settings)
	requireOnlyCredential(t, preview(), "pat")
}

func TestGitHubRateLimitedReportsRetryTime(t *testing.T) {
	reset := time.Date(2026, 10, 9, 14, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name                         string
		status                       int
		remaining, reset, retryAfter string
		want                         func(time.Time) bool
	}{
		{"remaining zero", http.StatusForbidden, "0", strconv.FormatInt(reset.Unix(), 10), "", func(at time.Time) bool { return at.Equal(reset) }},
		{"retry after", http.StatusTooManyRequests, "", "", "120", func(at time.Time) bool {
			return at.After(time.Now().Add(110*time.Second)) && at.Before(time.Now().Add(130*time.Second))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := writeGitHubRepo(t, map[string]repoFile{"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644}})
			api := serveGitHubAPI(t, inspectGitHubRepo(t, repo))
			blockGitHubClone(t)
			lib := openTestLibrary(t)
			preview, err := lib.PreviewFetch(t.Context(), gitHubRequest())
			if err != nil {
				t.Fatal(err)
			}
			fetched := fetchSkills(t, lib, preview, candidateNames(preview))
			api.limitAnonymous(tc.status, tc.remaining, tc.reset, tc.retryAfter)

			for name, run := range map[string]func() error{
				"PreviewFetch": func() error { _, err := lib.PreviewFetch(t.Context(), gitHubRequest()); return err },
				"CheckUpdates": func() error { _, err := checkSkillUpdate(t, lib, t.Context(), fetched[0].ID); return err },
				"Update":       func() error { _, err := updateSkill(t, lib, t.Context(), fetched[0].ID); return err },
			} {
				typed := requireLibraryError(t, run(), library.RateLimited)
				if !tc.want(typed.Details.RetryAt) {
					t.Fatalf("%s RetryAt = %v", name, typed.Details.RetryAt)
				}
			}
		})
	}
}

func TestCheckGitHubAccess(t *testing.T) {
	missingGH := filepath.Join(t.TempDir(), "gh")
	for _, tc := range []struct {
		name     string
		ghToken  string
		settings func(gh string) library.GitHubSettings
		want     func(gh string) library.GitHubAccess
	}{
		{"anonymous", "", func(string) library.GitHubSettings { return library.GitHubSettings{} }, func(string) library.GitHubAccess {
			return library.GitHubAccess{Identity: library.GitHubAnonymous, RateLimit: library.GitHubRateLimit{Limit: 60, Remaining: 42, ResetAt: gitHubRateReset}, Token: library.GitHubTokenNone, GH: library.GHAccess{Status: library.GHDisabled}}
		}},
		{"token and gh", "gh-token", func(gh string) library.GitHubSettings {
			return library.GitHubSettings{Token: "pat", AllowGH: true, GHPath: gh}
		}, func(gh string) library.GitHubAccess {
			return library.GitHubAccess{Identity: library.GitHubPersonalToken, Login: "alice", RateLimit: library.GitHubRateLimit{Limit: 5000, Remaining: 4987, ResetAt: gitHubRateReset}, Token: library.GitHubTokenValid, GH: library.GHAccess{Status: library.GHLoggedIn, Path: gh, Login: "bob"}}
		}},
		{"rejected token falls back to gh", "gh-token", func(gh string) library.GitHubSettings {
			return library.GitHubSettings{Token: "expired", AllowGH: true, GHPath: gh}
		}, func(gh string) library.GitHubAccess {
			return library.GitHubAccess{Identity: library.GitHubGHLogin, Login: "bob", RateLimit: library.GitHubRateLimit{Limit: 5000, Remaining: 4987, ResetAt: gitHubRateReset}, Token: library.GitHubTokenRejected, GH: library.GHAccess{Status: library.GHLoggedIn, Path: gh, Login: "bob"}}
		}},
		{"gh not logged in", "", func(gh string) library.GitHubSettings {
			return library.GitHubSettings{AllowGH: true, GHPath: gh}
		}, func(gh string) library.GitHubAccess {
			return library.GitHubAccess{Identity: library.GitHubAnonymous, RateLimit: library.GitHubRateLimit{Limit: 60, Remaining: 42, ResetAt: gitHubRateReset}, Token: library.GitHubTokenNone, GH: library.GHAccess{Status: library.GHNotLoggedIn, Path: gh}}
		}},
		{"gh not found", "", func(string) library.GitHubSettings {
			return library.GitHubSettings{Token: "pat", AllowGH: true, GHPath: missingGH}
		}, func(string) library.GitHubAccess {
			return library.GitHubAccess{Identity: library.GitHubPersonalToken, Login: "alice", RateLimit: library.GitHubRateLimit{Limit: 5000, Remaining: 4987, ResetAt: gitHubRateReset}, Token: library.GitHubTokenValid, GH: library.GHAccess{Status: library.GHNotFound}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := serveGitHubAPI(t, gitHubFixture{})
			api.addAccount("pat", "alice")
			api.addAccount("gh-token", "bob")
			gh, _ := fakeGH(t, tc.ghToken)
			lib := openTestLibrary(t)
			setGitHubSettings(t, lib, tc.settings(gh))

			access, err := lib.CheckGitHubAccess(t.Context())
			if want := tc.want(gh); err != nil || !reflect.DeepEqual(access, want) {
				t.Fatalf("CheckGitHubAccess = %#v, %v; want %#v", access, err, want)
			}
			encoded, err := json.Marshal(access)
			if err != nil {
				t.Fatal(err)
			}
			var decoded library.GitHubAccess
			if err := json.Unmarshal(encoded, &decoded); err != nil || !reflect.DeepEqual(decoded, access) {
				t.Fatalf("JSON round trip = %#v, %v; want %#v", decoded, err, access)
			}
		})
	}
}

func TestCheckGitHubAccessSeesGHLoginAfterSettingsAreSet(t *testing.T) {
	api := serveGitHubAPI(t, gitHubFixture{})
	api.addAccount("gh-token", "bob")
	dir := t.TempDir()
	gh := filepath.Join(dir, "gh")
	lib := openTestLibrary(t)
	setGitHubSettings(t, lib, library.GitHubSettings{AllowGH: true, GHPath: gh})
	if access, err := lib.CheckGitHubAccess(t.Context()); err != nil || access.GH.Status != library.GHNotFound {
		t.Fatalf("before installing gh = %#v, %v", access, err)
	}
	// 用户随后装好并登录了 gh:再查一次就能看到,不用重新交设置。
	writeFile(t, gh, []byte("#!/bin/sh\necho gh-token\n"), 0o755)
	if access, err := lib.CheckGitHubAccess(t.Context()); err != nil || access.Identity != library.GitHubGHLogin || access.GH.Login != "bob" {
		t.Fatalf("after installing gh = %#v, %v", access, err)
	}
}

func TestSetGitHubSettingsRejectsRelativeGHPath(t *testing.T) {
	lib := openTestLibrary(t)
	err := lib.SetGitHubSettings(library.GitHubSettings{AllowGH: true, GHPath: "bin/gh"})
	requireErrorDetails(t, err, library.InvalidArgument, library.ErrorDetails{Field: "gh_path"})
}
