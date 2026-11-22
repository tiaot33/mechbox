package library_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tiaot33/mechbox/internal/library"
)

const mirrorPrefix = "https://gh-proxy.org/"

// fakeGitHubMirror 充当 gh-proxy:把发到 gh-proxy.org 的请求还原成原始地址交给之前的假远端,并记下经镜像与直连 GitHub 的请求。
// status 非 0 时镜像一律回这个状态码;比较两个提交的接口和真的 gh-proxy 一样被拦下(403)。
type fakeGitHubMirror struct {
	next http.RoundTripper

	mu         sync.Mutex
	status     int
	mirrored   []string
	direct     []string
	authorized []string
}

// serveGitHubMirror 之后，这个测试里发往镜像的 git 命令都连不上(改写到不存在的本地仓库),除非测试用 mapGitRemotes 另外指定，
// 所以测试从不访问真的 gh-proxy.org。
func serveGitHubMirror(t *testing.T) *fakeGitHubMirror {
	t.Helper()
	m := &fakeGitHubMirror{next: testTransport(t)}
	stubHTTP(t, m.roundTrip)
	addGitConfig(t, "url.file://"+filepath.Join(t.TempDir(), "unreachable-mirror")+"/.insteadOf", mirrorPrefix)
	return m
}

func (m *fakeGitHubMirror) roundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != "gh-proxy.org" {
		m.mu.Lock()
		m.direct = append(m.direct, r.URL.String())
		m.mu.Unlock()
		return m.next.RoundTrip(r)
	}
	original, err := url.Parse(strings.TrimPrefix(r.URL.String(), mirrorPrefix))
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.mirrored = append(m.mirrored, original.String())
	if authorization := r.Header.Get("Authorization"); authorization != "" {
		m.authorized = append(m.authorized, authorization)
	}
	status := m.status
	m.mu.Unlock()
	if strings.Contains(original.Path, "/compare/") {
		status = http.StatusForbidden
	}
	if status != 0 {
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("mirror says no")), Header: make(http.Header)}, nil
	}
	forwarded := r.Clone(r.Context())
	forwarded.URL, forwarded.Host = original, ""
	return m.next.RoundTrip(forwarded)
}

func (m *fakeGitHubMirror) setStatus(status int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status = status
}

// take 返回并清空记下的请求:经镜像的原始地址、直连 GitHub 的地址、发给镜像的 Authorization 头。
func (m *fakeGitHubMirror) take() (mirrored, direct, authorized []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	mirrored, direct, authorized = m.mirrored, m.direct, m.authorized
	m.mirrored, m.direct, m.authorized = nil, nil, nil
	return mirrored, direct, authorized
}

func setGitHubMirror(t *testing.T, lib *library.Library, on bool) {
	t.Helper()
	if err := lib.SetGitHubMirror(t.Context(), on); err != nil {
		t.Fatal(err)
	}
}

// mapGitRemotes 让这个测试里的 git 把 from 的每个地址改写到对应的 to,并隔离本机 gh 登录态。
func mapGitRemotes(t *testing.T, remotes map[string]string) {
	t.Helper()
	stubGitHubCLI(t, "exit 1\n")
	for from, to := range remotes {
		addGitConfig(t, "url."+to+".insteadOf", from)
	}
}

// addGitConfig 往这个测试里 git 读的全局配置文件里加一项;还没有配置文件时建一个。
func addGitConfig(t *testing.T, key, value string) {
	t.Helper()
	world := worldOf(t)
	world.mu.Lock()
	var config string
	for _, pair := range world.env {
		if value, ok := strings.CutPrefix(pair, "GIT_CONFIG_GLOBAL="); ok {
			config = value
		}
	}
	world.mu.Unlock()
	if config == "" {
		config = filepath.Join(t.TempDir(), "gitconfig")
		setTestEnv(t, "GIT_CONFIG_GLOBAL", config)
		setTestEnv(t, "GIT_CONFIG_NOSYSTEM", "1")
	}
	runGit(t, t.TempDir(), "config", "--file", config, "--add", key, value)
}

func TestGitHubMirrorIsOffByDefaultAndSurvivesReopen(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "library.db")
	lib := openTestLibraryAt(t, path)
	if on, err := lib.GitHubMirror(t.Context()); err != nil || on {
		t.Fatalf("default GitHubMirror = %v, %v; want off", on, err)
	}
	setGitHubMirror(t, lib, true)
	if on, err := lib.GitHubMirror(t.Context()); err != nil || !on {
		t.Fatalf("GitHubMirror after turning on = %v, %v", on, err)
	}
	if err := lib.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openTestLibraryAt(t, path)
	if on, err := reopened.GitHubMirror(t.Context()); err != nil || !on {
		t.Fatalf("GitHubMirror after reopen = %v, %v; want on", on, err)
	}
	setGitHubMirror(t, reopened, false)
	if on, err := reopened.GitHubMirror(t.Context()); err != nil || on {
		t.Fatalf("GitHubMirror after turning off = %v, %v", on, err)
	}
}

func TestGitHubMirrorStaysLocalAcrossSync(t *testing.T) {
	t.Parallel()
	s3Server := newS3Stub(t)
	machineA := openTestLibrary(t)
	setSyncSettings(t, machineA, s3Server.settings)
	setGitHubMirror(t, machineA, true)
	if _, err := machineA.SyncPush(t.Context()); err != nil {
		t.Fatalf("SyncPush: %v", err)
	}
	machineB := openTestLibrary(t)
	setSyncSettings(t, machineB, s3Server.settings)
	backups, err := machineB.ListSyncBackups(t.Context())
	if err != nil || len(backups) != 1 {
		t.Fatalf("ListSyncBackups = %+v, %v", backups, err)
	}
	if err := machineB.SyncPull(t.Context(), backups[0].ID); err != nil {
		t.Fatalf("SyncPull: %v", err)
	}
	if on, err := machineB.GitHubMirror(t.Context()); err != nil || on {
		t.Fatalf("machine B GitHubMirror after pull = %v, %v; want machine A's switch left behind", on, err)
	}
	setGitHubMirror(t, machineB, true)
	if err := machineB.SyncPull(t.Context(), backups[0].ID); err != nil {
		t.Fatalf("SyncPull: %v", err)
	}
	if on, err := machineB.GitHubMirror(t.Context()); err != nil || !on {
		t.Fatalf("machine B GitHubMirror after second pull = %v, %v; want its own switch kept", on, err)
	}
}

func TestGitHubMirrorReadsPublicRepositoryThroughMirror(t *testing.T) {
	t.Parallel()
	fx := inspectGitHubRepo(t, writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644},
	}))
	serveGitHubAPI(t, fx).serveDownloads(true, true, false)
	blockGitHubClone(t)
	mirror := serveGitHubMirror(t)
	lib := openTestLibrary(t)
	setGitHubToken(t, lib, "stored-token")
	setGitHubMirror(t, lib, true)

	preview, err := lib.PreviewFetch(t.Context(), gitHubRequest())
	if err != nil {
		t.Fatal(err)
	}
	skill := fetchSkills(t, lib, preview, []string{"demo"})[0]
	if string(skillFiles(t, lib, skill)[0].Content) != skillDocument("demo", "body") {
		t.Fatalf("files = %#v", skillFiles(t, lib, skill))
	}
	mirrored, direct, authorized := mirror.take()
	if len(mirrored) == 0 || len(direct) != 0 || len(authorized) != 0 {
		t.Fatalf("mirrored %q, direct %q, authorization sent to mirror %q; want every request through the mirror without token", mirrored, direct, authorized)
	}
	if skill.Source.Label != "owner/repo" || !strings.HasPrefix(skill.Source.ViewURL, "https://github.com/owner/repo/") {
		t.Fatalf("source = %#v; want original GitHub address", skill.Source)
	}

	setGitHubMirror(t, lib, false)
	if _, err := lib.PreviewFetch(t.Context(), gitHubRequest()); err != nil {
		t.Fatal(err)
	}
	if mirrored, direct, _ := mirror.take(); len(mirrored) != 0 || len(direct) == 0 {
		t.Fatalf("after turning off mirrored %q, direct %q; want direct only", mirrored, direct)
	}
}

func TestGitHubMirrorNeverCarriesToken(t *testing.T) {
	t.Parallel()
	fx := inspectGitHubRepo(t, writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md": {content: []byte(skillDocument("demo", "private")), mode: 0o644},
	}))
	api := serveGitHubAPI(t, fx)
	api.anonymousStatus = http.StatusNotFound
	api.acceptedToken = "private-token"
	blockGitHubClone(t)
	mirror := serveGitHubMirror(t)
	lib := openTestLibrary(t)
	setGitHubToken(t, lib, "private-token")
	setGitHubMirror(t, lib, true)

	preview, err := lib.PreviewFetch(t.Context(), gitHubRequest())
	if err != nil {
		t.Fatal(err)
	}
	fetchSkills(t, lib, preview, []string{"demo"})
	_, direct, authorized := mirror.take()
	if len(authorized) != 0 {
		t.Fatalf("authorization sent to mirror %q", authorized)
	}
	if !slices.Contains(api.takeAuthorizations(), "Bearer private-token") || len(direct) == 0 {
		t.Fatalf("direct %q; want the token request sent straight to GitHub", direct)
	}
}

func TestGitHubMirrorFallsBackToDirectWhenUnavailable(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusBadGateway, http.StatusForbidden, http.StatusTooManyRequests} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			fx := inspectGitHubRepo(t, writeGitHubRepo(t, map[string]repoFile{
				"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644},
			}))
			serveGitHubAPI(t, fx)
			blockGitHubClone(t)
			mirror := serveGitHubMirror(t)
			mirror.setStatus(status)
			lib := openTestLibrary(t)
			setGitHubMirror(t, lib, true)
			preview, err := lib.PreviewFetch(t.Context(), gitHubRequest())
			if err != nil {
				t.Fatalf("preview with mirror answering %d: %v", status, err)
			}
			fetchSkills(t, lib, preview, []string{"demo"})
			if mirrored, direct, _ := mirror.take(); len(mirrored) == 0 || len(direct) < len(mirrored) {
				t.Fatalf("mirrored %q, direct %q; want each failed mirror request retried directly", mirrored, direct)
			}
		})
	}
}

func TestGitHubMirrorClonesThroughMirrorThenDirect(t *testing.T) {
	t.Parallel()
	const (
		direct   = "https://github.com/owner/repo.git"
		ssh      = "git@github.com:owner/repo.git"
		mirrored = mirrorPrefix + direct
	)
	cases := []struct {
		name    string
		remotes func(repo, missing string) map[string]string
	}{
		{"through mirror", func(repo, missing string) map[string]string {
			return map[string]string{mirrored: repo, direct: missing, ssh: missing}
		}},
		// 私有仓库:git 不会把 github.com 的凭据交给镜像,镜像失败后先直连 HTTPS。
		{"direct HTTPS after mirror", func(repo, missing string) map[string]string {
			return map[string]string{mirrored: missing, direct: repo, ssh: missing}
		}},
		{"SSH last", func(repo, missing string) map[string]string {
			return map[string]string{mirrored: missing, direct: missing, ssh: repo}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := writeGitHubRepo(t, map[string]repoFile{
				"demo/SKILL.md": {content: []byte(skillDocument("demo", "cloned")), mode: 0o644},
			})
			stubHTTPResponse(t, http.StatusInternalServerError, strings.NewReader("boom"))
			mapGitRemotes(t, tc.remotes("file://"+repo, "file://"+filepath.Join(t.TempDir(), "missing")))
			lib := openTestLibrary(t)
			setGitHubMirror(t, lib, true)
			preview, err := lib.PreviewFetch(t.Context(), gitHubRequest())
			if err != nil {
				t.Fatal(err)
			}
			skill := fetchSkills(t, lib, preview, []string{"demo"})[0]
			if string(skillFiles(t, lib, skill)[0].Content) != skillDocument("demo", "cloned") {
				t.Fatalf("files = %#v", skillFiles(t, lib, skill))
			}
			refs, err := lib.ListSourceRefs(t.Context(), "owner/repo")
			if err != nil || !slices.Contains(refs.Branches, "main") {
				t.Fatalf("refs = %#v, %v", refs, err)
			}
		})
	}
}

// 比较两个提交的接口经镜像会被拦下;打开镜像时更新计划经镜像用 git 取提交说明,不再直连那个接口。
func TestGitHubMirrorUpdatePlanListsCommitsThroughGit(t *testing.T) {
	t.Parallel()
	repo := writeGitHubRepo(t, map[string]repoFile{"demo/SKILL.md": {content: []byte(skillDocument("demo", "v1")), mode: 0o644}})
	api := serveGitHubAPI(t, inspectGitHubRepo(t, repo))
	mapGitRemotes(t, map[string]string{mirrorPrefix + "https://github.com/owner/repo.git": "file://" + repo})
	mirror := serveGitHubMirror(t)
	lib := openTestLibrary(t)
	setGitHubMirror(t, lib, true)
	preview, err := lib.PreviewFetch(t.Context(), gitHubRequest())
	if err != nil {
		t.Fatal(err)
	}
	skill := fetchSkills(t, lib, preview, []string{"demo"})[0]
	writeFile(t, filepath.Join(repo, "demo/SKILL.md"), []byte(skillDocument("demo", "v2")), 0o644)
	commitGit(t, repo, "second change")
	api.Swap(inspectGitHubRepo(t, repo))
	mirror.take()

	plan, err := lib.PlanUpdate(t.Context(), skill.CollectionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Commits) != 1 || strings.TrimSpace(plan.Commits[0].Message) != "second change" {
		t.Fatalf("commits = %#v; want the new commit", plan.Commits)
	}
	mirrored, direct, _ := mirror.take()
	if slices.ContainsFunc(append(mirrored, direct...), func(location string) bool { return strings.Contains(location, "/compare/") }) {
		t.Fatalf("mirrored %q, direct %q; want commits read with git instead of the compare API", mirrored, direct)
	}
}

func TestGitHubMirrorDownloadsGitHubLinks(t *testing.T) {
	t.Parallel()
	const link = "https://github.com/owner/repo/releases/download/v1/skills.zip"
	archive := zipArchive(t, map[string]string{"demo/SKILL.md": skillDocument("demo", "zipped")})
	stubHTTP(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != link {
			return &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody, Header: make(http.Header)}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(archive)), Header: make(http.Header)}, nil
	})
	mirror := serveGitHubMirror(t)
	lib := openTestLibrary(t)
	setGitHubMirror(t, lib, true)
	skill := fetchURLSkill(t, lib, link, "demo")
	if skill.Source.Label != link {
		t.Fatalf("source = %#v; want the original link kept", skill.Source)
	}
	if mirrored, direct, _ := mirror.take(); !slices.Contains(mirrored, link) || len(direct) != 0 {
		t.Fatalf("mirrored %q, direct %q; want the link downloaded through the mirror", mirrored, direct)
	}
}

// 带查询串的 GitHub 链接(如私有仓库 Raw 按钮给出的 ?token=...)可能带着临时令牌，不发给镜像。
func TestGitHubMirrorSendsLinksWithQueryDirect(t *testing.T) {
	t.Parallel()
	const link = "https://raw.githubusercontent.com/owner/private/main/skills.zip?token=GHSAT0secret"
	archive := zipArchive(t, map[string]string{"demo/SKILL.md": skillDocument("demo", "private")})
	stubHTTP(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != link {
			return &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody, Header: make(http.Header)}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(archive)), Header: make(http.Header)}, nil
	})
	mirror := serveGitHubMirror(t)
	lib := openTestLibrary(t)
	setGitHubMirror(t, lib, true)
	fetchURLSkill(t, lib, link, "demo")
	if mirrored, direct, _ := mirror.take(); len(mirrored) != 0 || !slices.Contains(direct, link) {
		t.Fatalf("mirrored %q, direct %q; want the tokened link sent straight to GitHub", mirrored, direct)
	}
}

// 镜像已经给出准确的错误(分支不存在)时直接报出来，不再改走直连。
func TestGitHubMirrorKeepsMirrorAnswerForMissingRef(t *testing.T) {
	t.Parallel()
	mirrored := writeGitHubRepo(t, map[string]repoFile{"demo/SKILL.md": {content: []byte(skillDocument("demo", "main")), mode: 0o644}})
	direct := writeGitHubRepo(t, map[string]repoFile{"demo/SKILL.md": {content: []byte(skillDocument("demo", "main")), mode: 0o644}})
	runGit(t, direct, "branch", "dev")
	stubHTTPResponse(t, http.StatusInternalServerError, strings.NewReader("boom"))
	serveGitHubMirror(t)
	mapGitRemotes(t, map[string]string{
		mirrorPrefix + "https://github.com/owner/repo.git": "file://" + mirrored,
		"https://github.com/owner/repo.git":                "file://" + direct,
		"git@github.com:owner/repo.git":                    "file://" + direct,
	})
	lib := openTestLibrary(t)
	setGitHubMirror(t, lib, true)
	_, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: "owner/repo", Ref: "dev"})
	requireLibraryError(t, err, library.NotFound)
}

// 试镜像的 git 命令清掉用户全局配置里的 http.extraHeader 与凭据助手；直连照常带上。
func TestGitHubMirrorGitDropsConfiguredCredentials(t *testing.T) {
	t.Parallel()
	repo := writeGitHubRepo(t, map[string]repoFile{"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644}})
	var mu sync.Mutex
	seen := map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")[0]] = r.Header.Get("Authorization")
		mu.Unlock()
		http.Error(w, "unavailable", http.StatusBadGateway)
	}))
	t.Cleanup(server.Close)
	stubHTTPResponse(t, http.StatusInternalServerError, strings.NewReader("boom"))
	serveGitHubMirror(t)
	mapGitRemotes(t, map[string]string{
		mirrorPrefix + "https://github.com/owner/repo.git": server.URL + "/mirror/repo.git",
		"https://github.com/owner/repo.git":                server.URL + "/direct/repo.git",
		"git@github.com:owner/repo.git":                    "file://" + repo,
	})
	addGitConfig(t, "http.extraHeader", "Authorization: Bearer configured-secret")
	lib := openTestLibrary(t)
	setGitHubMirror(t, lib, true)
	if _, err := lib.PreviewFetch(t.Context(), gitHubRequest()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	mirrorAuth, mirrorSeen := seen["mirror"]
	if !mirrorSeen || mirrorAuth != "" || seen["direct"] != "Bearer configured-secret" {
		t.Fatalf("Authorization seen = %q; want none to the mirror, the configured header on the direct remote", seen)
	}
}

// 镜像连得上但卡着不回时，只占一半时限，剩下的留给直连。
func TestGitHubMirrorStalledGitFallsBackInTime(t *testing.T) {
	t.Parallel()
	repo := writeGitHubRepo(t, map[string]repoFile{"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644}})
	stalled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(stalled.Close)
	stubHTTPResponse(t, http.StatusInternalServerError, strings.NewReader("boom"))
	serveGitHubMirror(t)
	mapGitRemotes(t, map[string]string{
		mirrorPrefix + "https://github.com/owner/repo.git": stalled.URL + "/repo.git",
		"https://github.com/owner/repo.git":                "file://" + repo,
	})
	lib := openTestLibrary(t)
	setGitHubMirror(t, lib, true)
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
	defer cancel()
	if _, err := lib.ListSourceRefs(ctx, "owner/repo"); err != nil {
		t.Fatalf("refs with a stalled mirror: %v", err)
	}
}

// 关着镜像时被 GitHub 限流，记下的冷却时间不该拦住打开镜像后的请求。
func TestGitHubMirrorTurningOnClearsRateLimitCooldown(t *testing.T) {
	t.Parallel()
	fx := inspectGitHubRepo(t, writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644},
	}))
	api := serveGitHubAPI(t, fx)
	api.mu.Lock()
	api.anonymousStatus, api.rateLimitRemaining, api.rateLimitReset = http.StatusForbidden, "0", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
	api.mu.Unlock()
	blockGitHubClone(t)
	mirror := serveGitHubMirror(t)
	lib := openTestLibrary(t)
	if _, err := lib.PreviewFetch(t.Context(), gitHubRequest()); err == nil {
		t.Fatal("preview while rate limited succeeded")
	}
	api.mu.Lock()
	api.anonymousStatus = 0
	api.mu.Unlock()
	mirror.take()
	setGitHubMirror(t, lib, true)
	if _, err := lib.PreviewFetch(t.Context(), gitHubRequest()); err != nil {
		t.Fatalf("preview through the mirror after the direct rate limit: %v", err)
	}
	if mirrored, _, _ := mirror.take(); len(mirrored) == 0 {
		t.Fatal("no request went through the mirror")
	}
}
