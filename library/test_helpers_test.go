package library_test

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tiaot33/skills-tui/library"
)

// archiveServer 打桩 URL / Store 来源的压缩包服务端(接管 http.DefaultClient):记录每个请求的头,
// 可配置 ETag 或 Last-Modified;请求带 If-None-Match / If-Modified-Since 且与当前 validator 相同时回 304,
// 304 的正文若被读取会被计数——按契约应恒为零字节。
type archiveServer struct {
	mu                   sync.Mutex
	archive              []byte
	etag                 string
	lastModified         string
	alwaysNotModified    bool // 不看条件头一律回 304:模拟行为不端的服务端
	requests             []http.Header
	notModifiedBodyReads atomic.Int64
}

// newArchiveServer 接管 http.DefaultClient 提供 archive;etag 为空表示服务端不给 validator。
func newArchiveServer(t *testing.T, archive []byte, etag string) *archiveServer {
	t.Helper()
	server := &archiveServer{archive: archive, etag: etag}
	client := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: fetchTransport(server.roundTrip)}
	t.Cleanup(func() { http.DefaultClient = client })
	return server
}

// serveArchive 是最简形态:固定 200 + 压缩包,不给 validator。
func serveArchive(t *testing.T, archive []byte) library.SourceSpec {
	t.Helper()
	return newArchiveServer(t, archive, "").spec()
}

func (s *archiveServer) spec() library.SourceSpec {
	return library.SourceSpec{Type: library.SourceURL, Locator: library.SourceLocator{URL: "https://source.example/skills.zip"}}
}

// serve 替换远端内容与 validator;etag 为空表示服务端不再给出任何 validator(也清掉 Last-Modified)。
func (s *archiveServer) serve(archive []byte, etag string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.archive, s.etag, s.lastModified = archive, etag, ""
}

// serveLastModified 让服务端只给出 Last-Modified(不给 ETag)。
func (s *archiveServer) serveLastModified(archive []byte, lastModified string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.archive, s.etag, s.lastModified = archive, "", lastModified
}

// serveNotModified 让服务端对任何请求(哪怕不带条件头)都回 304。
func (s *archiveServer) serveNotModified() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.alwaysNotModified = true
}

// snapshot 返回到目前为止收到的全部请求头;调用后清空,便于逐操作断言。
func (s *archiveServer) snapshot() []http.Header {
	s.mu.Lock()
	defer s.mu.Unlock()
	requests := s.requests
	s.requests = nil
	return requests
}

// assertNoBodyRead 断言至今所有 304 响应的正文一个字节都没被读过。
func (s *archiveServer) assertNoBodyRead(t *testing.T) {
	t.Helper()
	if n := s.notModifiedBodyReads.Load(); n != 0 {
		t.Fatalf("304 body read %d bytes, want 0", n)
	}
}

func (s *archiveServer) roundTrip(r *http.Request) (*http.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, r.Header.Clone())
	header := make(http.Header)
	if s.etag != "" {
		header.Set("ETag", s.etag)
	}
	if s.lastModified != "" {
		header.Set("Last-Modified", s.lastModified)
	}
	matched := s.alwaysNotModified ||
		(s.etag != "" && r.Header.Get("If-None-Match") == s.etag) ||
		(s.lastModified != "" && r.Header.Get("If-Modified-Since") == s.lastModified)
	if matched {
		body := &countingReader{reader: bytes.NewReader(s.archive), read: &s.notModifiedBodyReads}
		return &http.Response{StatusCode: http.StatusNotModified, Header: header, Body: io.NopCloser(body)}, nil
	}
	return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(bytes.NewReader(s.archive))}, nil
}

// countingReader 记录经它读出的字节数。
type countingReader struct {
	reader io.Reader
	read   *atomic.Int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.reader.Read(p)
	c.read.Add(int64(n))
	return n, err
}

func requireLibraryError(t *testing.T, err error, code library.ErrorCode) *library.Error {
	t.Helper()
	typed, ok := errors.AsType[*library.Error](err)
	if !ok || typed.Code != code {
		t.Fatalf("error = %T %v, want *library.Error %s", err, err, code)
	}
	if typed.Message == "" {
		t.Fatal("empty error message")
	}
	return typed
}

func requireErrorDetails(t *testing.T, err error, code library.ErrorCode, want library.ErrorDetails) *library.Error {
	t.Helper()
	typed := requireLibraryError(t, err, code)
	if typed.Details != want {
		t.Fatalf("details = %#v, want %#v", typed.Details, want)
	}
	return typed
}

func seededAgents() []library.Agent {
	return []library.Agent{
		{Code: library.AgentClaudeCode, Name: "Claude Code"},
		{Code: library.AgentCodex, Name: "Codex"},
	}
}

func skillIDs(skills []library.Skill) []library.SkillID {
	ids := make([]library.SkillID, len(skills))
	for i, skill := range skills {
		ids[i] = skill.ID
	}
	return ids
}

func skillNames(skills []library.Skill) []string {
	names := make([]string, len(skills))
	for i, skill := range skills {
		names[i] = skill.Name
	}
	return names
}

func summaryNames(skills []library.SkillSummary) []string {
	names := make([]string, len(skills))
	for i, skill := range skills {
		names[i] = skill.Name
	}
	return names
}

func summaryIDs(skills []library.SkillSummary) []library.SkillID {
	ids := make([]library.SkillID, len(skills))
	for i, skill := range skills {
		ids[i] = skill.ID
	}
	return ids
}

// withoutActivatedAt 去掉首次激活时间,供不关心时间戳的既有 DeepEqual 断言使用。
func withoutActivatedAt(activations []library.Activation) []library.Activation {
	out := append([]library.Activation(nil), activations...)
	for i := range out {
		out[i].ActivatedAt = time.Time{}
	}
	return out
}

func summaryOf(skill library.Skill) library.SkillSummary {
	return library.SkillSummary{
		ID:          skill.ID,
		Name:        skill.Name,
		Description: skill.Description,
		Source:      skill.Source,
		InstalledAt: skill.InstalledAt,
		UpdatedAt:   skill.UpdatedAt,
	}
}

func writeFile(t *testing.T, path string, content []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, mode); err != nil {
		t.Fatal(err)
	}
}

func zipArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, content := range files {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func runGit(t *testing.T, directory string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-c", "core.excludesFile=/dev/null", "-c", "commit.gpgsign=false", "-C", directory}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func commitGit(t *testing.T, repository, message string) {
	t.Helper()
	runGit(t, repository, "add", ".")
	runGit(t, repository, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", message)
}

// editLibraryFixture 用 SQL 直接改写一个已关闭的库文件,只用于构造测试输入;断言仍须经公开门面。
func editLibraryFixture(t *testing.T, path, statement string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(t.Context(), statement, args...); err != nil {
		t.Fatal(err)
	}
}

// readLibraryScalar 直读库文件里的一个标量(表里的值或 PRAGMA):skill 被移除后门面已看不到它,只能这样核对
// "没有部分写入";journal_mode / user_version 则是磁盘上的可观察事实,不是实现细节。
func readLibraryScalar(t *testing.T, path, query string, args ...any) string {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var value string
	if err := db.QueryRowContext(t.Context(), query, args...).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func journalMode(t *testing.T, path string) string {
	t.Helper()
	return readLibraryScalar(t, path, `PRAGMA journal_mode`)
}

func installNamedSkill(t *testing.T, lib library.Library, name string) library.Skill {
	t.Helper()
	return installSkillWithBody(t, lib, name, "# "+name+"\n")
}

// installSkillWithBody 从一个独立的临时 Source 安装指定名字与 SKILL.md 正文的 skill;
// 多次以同名调用会得到不同 Source 的同名 skill,靠正文区分。
func installSkillWithBody(t *testing.T, lib library.Library, name, body string) library.Skill {
	t.Helper()
	source := filepath.Join(t.TempDir(), name)
	writeFile(t, filepath.Join(source, "SKILL.md"), []byte(body), 0o644)
	skill, err := lib.Install(t.Context(), library.LocalSource{Path: source})
	if err != nil {
		t.Fatal(err)
	}
	return skill
}

// requireNoStagedEntries 断言 Scope 目录下没有以 "." 开头的暂存残留(激活暂存或删除暂存)。
func requireNoStagedEntries(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name()[0] == '.' {
			t.Fatalf("staged entry remains in %s: %s", directory, entry.Name())
		}
	}
}

// makeScopeReadOnly 让 directory 不可写,使其中的改名 / 删除失败;测试结束自动恢复。
func makeScopeReadOnly(t *testing.T, directory string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("running as root bypasses the injected permission fault")
	}
	if err := os.Chmod(directory, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(directory, 0o755) })
}

// requireProjection 断言 directory 下 name 文件夹的 SKILL.md 正文。
func requireProjection(t *testing.T, directory, name, body string) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(directory, name, "SKILL.md"))
	if err != nil || string(content) != body {
		t.Fatalf("projection %s/SKILL.md = %q, %v; want %q", name, content, err, body)
	}
}

func openTestLibrary(t *testing.T) library.Library {
	t.Helper()
	lib, err := library.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
	return lib
}

// skillSnapshot 是经门面读回的一个 Skill 及其版本列表,用于断言检查前后 Library 完全一致(只读契约)。
type skillSnapshot struct {
	skill    library.Skill
	versions []library.SkillVersion
}

func snapshotSkill(t *testing.T, lib library.Library, id library.SkillID) skillSnapshot {
	t.Helper()
	skill, err := lib.GetSkill(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	versions, err := lib.ListVersions(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return skillSnapshot{skill: skill, versions: versions}
}

// assertGitHubCommitOnly 断言检查只解析了 commits 次 commit,没有取 tree、没有取任何 blob:
// 当前版本的确认 commit 命中时的请求形状。commit 未命中、须取 tree 的场景用 assertGitHubCheckDoesNotFetchSkillFiles。
func assertGitHubCommitOnly(t *testing.T, api *gitHubAPIMock, commits int) {
	t.Helper()
	gotCommits, trees, blobs := gitHubAPIKindCounts(t, api)
	if gotCommits != commits || trees != 0 || blobs != 0 {
		t.Fatalf("requests commits=%d trees=%d blobs=%d, want commits=%d only: %#v", gotCommits, trees, blobs, commits, api.snapshot())
	}
}

// stubHTTPResponse 让所有 HTTP 请求都得到同一个响应,用于模拟远端返回的状态与内容。
func stubHTTPResponse(t *testing.T, status int, body io.Reader) {
	t.Helper()
	client := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: fetchTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: status, Body: io.NopCloser(body), Header: make(http.Header)}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = client })
}

// networkGate 让测试里的远端在收到请求后停住,直到测试放行。
// 用它证明某个操作处于网络阶段时,其他操作不必等它。新建时不拦截,arm 后开始拦截,open 放行且不再拦截。
type networkGate struct {
	hold    atomic.Bool
	entered chan struct{}
	release chan struct{}
}

func newNetworkGate() *networkGate {
	return &networkGate{entered: make(chan struct{}, 64), release: make(chan struct{})}
}

// arm 开始拦截之后到达的请求;disarm 停止拦截新请求,但已挡住的仍等 open。
func (g *networkGate) arm()    { g.hold.Store(true) }
func (g *networkGate) disarm() { g.hold.Store(false) }

// pass 在远端 handler 里调用:拦截开启时上报"已到达"并等待放行。
func (g *networkGate) pass() {
	if !g.hold.Load() {
		return
	}
	g.entered <- struct{}{}
	<-g.release
}

// awaitEntered 等到至少一个请求已被挡住;远端一直没收到请求则测试失败。
func (g *networkGate) awaitEntered(t *testing.T) {
	t.Helper()
	select {
	case <-g.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("remote never received the request")
	}
}

// open 放行全部被挡住的请求;之后的请求也不再拦截。
func (g *networkGate) open() {
	g.hold.Store(false)
	close(g.release)
}

// stubGitHubCLI 隔离本机 gh 登录态,仅在进程边界提供受控响应。
func stubGitHubCLI(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "gh"), []byte("#!/bin/sh\n"+script), 0o755)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
