package library_test

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

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

func summaryOf(skill library.Skill) library.SkillSummary {
	return library.SkillSummary{ID: skill.ID, Name: skill.Name, Source: skill.Source}
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

// stubGitHubCLI 隔离本机 gh 登录态,仅在进程边界提供受控响应。
func stubGitHubCLI(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "gh"), []byte("#!/bin/sh\n"+script), 0o755)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
