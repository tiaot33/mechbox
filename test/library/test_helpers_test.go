package library_test

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tiaot33/skills-tui/internal/library"
)

// archiveServer 打桩 URL 来源的压缩包服务端(接管 http.DefaultClient),可配置 ETag 或 Last-Modified;
// 请求带的条件头与当前 validator 相同时回 304。
type archiveServer struct {
	mu                sync.Mutex
	archive           []byte
	etag              string
	lastModified      string
	alwaysNotModified bool
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
func serveArchive(t *testing.T, archive []byte) library.PreviewFetchRequest {
	t.Helper()
	return newArchiveServer(t, archive, "").request()
}

func (s *archiveServer) request() library.PreviewFetchRequest {
	return library.PreviewFetchRequest{URL: "https://source.example/skills.zip"}
}

// serve 替换远端内容与 ETag;etag 为空表示不再给出任何 validator。
func (s *archiveServer) serve(archive []byte, etag string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.archive, s.etag, s.lastModified = archive, etag, ""
}

// serveLastModified 让服务端只给出 Last-Modified。
func (s *archiveServer) serveLastModified(archive []byte, lastModified string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.archive, s.etag, s.lastModified = archive, "", lastModified
}

// serveNotModified 让服务端对任何请求(哪怕不带条件头)都回 304,模拟行为不端的服务端。
func (s *archiveServer) serveNotModified() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.alwaysNotModified = true
}

func (s *archiveServer) roundTrip(r *http.Request) (*http.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	header := make(http.Header)
	if s.etag != "" {
		header.Set("ETag", s.etag)
	}
	if s.lastModified != "" {
		header.Set("Last-Modified", s.lastModified)
	}
	if s.alwaysNotModified ||
		(s.etag != "" && r.Header.Get("If-None-Match") == s.etag) ||
		(s.lastModified != "" && r.Header.Get("If-Modified-Since") == s.lastModified) {
		return &http.Response{StatusCode: http.StatusNotModified, Header: header, Body: http.NoBody}, nil
	}
	return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(bytes.NewReader(s.archive))}, nil
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

// withoutCreatedAt 去掉首次安装时间,供不关心时间戳的既有 DeepEqual 断言使用。
func withoutCreatedAt(skillScopes []library.SkillScope) []library.SkillScope {
	out := append([]library.SkillScope(nil), skillScopes...)
	for i := range out {
		out[i].CreatedAt = time.Time{}
	}
	return out
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

// editLibraryFixture 用 SQL 直接改写一个已关闭的库文件,只用于构造测试输入;断言仍须经技能库公开方法。
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

// fetchExampleSkill 在隔离目录里入库一个带可执行脚本的 skill,供安装用例复用。
func fetchExampleSkill(t *testing.T, lib *library.Library, name string) library.Skill {
	t.Helper()
	source := filepath.Join(t.TempDir(), name)
	writeLocalSkill(t, source, "# "+name+"\n")
	writeFile(t, filepath.Join(source, "scripts", "run.sh"), []byte("#!/bin/sh\n"), 0o755)
	skill, err := fetchLocalDir(t.Context(), lib, source)
	if err != nil {
		t.Fatal(err)
	}
	return skill
}

func fetchNamedSkill(t *testing.T, lib *library.Library, name string) library.Skill {
	t.Helper()
	return fetchSkillWithBody(t, lib, name, "# "+name+"\n")
}

// fetchSkillWithBody 从一个独立的临时 Source 入库指定名字与 SKILL.md 正文的 skill;
// 多次以同名调用会得到不同 Source 的同名 skill,靠正文区分。
func fetchSkillWithBody(t *testing.T, lib *library.Library, name, body string) library.Skill {
	t.Helper()
	skill, _ := fetchLocalSkill(t, lib, name, body)
	return skill
}

// fetchLocalSkill 从独立的本地 Source 入库 skill,并返回 Source 目录,供之后改内容再 Update。
func fetchLocalSkill(t *testing.T, lib *library.Library, name, body string) (library.Skill, string) {
	t.Helper()
	source := filepath.Join(t.TempDir(), name)
	writeLocalSkill(t, source, body)
	skill, err := fetchLocalDir(t.Context(), lib, source)
	if err != nil {
		t.Fatal(err)
	}
	return skill, source
}

// listSkillScopes 读出全部 SkillScope,失败即终止测试。
func listSkillScopes(t *testing.T, lib *library.Library) []library.SkillScope {
	t.Helper()
	skillScopes, err := lib.ListSkillScopes(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return skillScopes
}

// requireNoStagedEntries 断言 Scope 目录下没有以 "." 开头的暂存残留(安装暂存或删除暂存)。
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

// requireInstalledFolder 断言 directory 下 name 文件夹的 SKILL.md 正文。
func requireInstalledFolder(t *testing.T, directory, name, body string) {
	t.Helper()
	requireSkillFile(t, directory, name, skillDocument(name, body))
}

// requireSkillFile 断言 directory/name/SKILL.md 的原样内容,用于手动写入或改过的文件夹。
func requireSkillFile(t *testing.T, directory, name, want string) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(directory, name, "SKILL.md"))
	if err != nil || string(content) != want {
		t.Fatalf("skill scope %s/SKILL.md = %q, %v; want %q", name, content, err, want)
	}
}

func openTestLibrary(t *testing.T) *library.Library {
	t.Helper()
	return openTestLibraryAt(t, filepath.Join(t.TempDir(), "library.db"))
}

func openTestLibraryAt(t *testing.T, path string) *library.Library {
	t.Helper()
	lib, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
	return lib
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

// localRequest 是对本地目录 path 做入库预览的请求。
func localRequest(path string) library.PreviewFetchRequest {
	return library.PreviewFetchRequest{Local: path}
}

// candidateNames 收集入库预览中全部候选的名字,供"全部入库"的用例传给 Fetch。
func candidateNames(preview library.FetchPreview) []string {
	names := make([]string, len(preview.Candidates))
	for i, candidate := range preview.Candidates {
		names[i] = candidate.Name
	}
	return names
}

// fetchSkills 把 names 从入库预览入库,失败即终止测试。
func fetchSkills(t *testing.T, lib *library.Library, preview library.FetchPreview, names []string) []library.Skill {
	t.Helper()
	skills, err := lib.Fetch(t.Context(), preview.ID, names)
	if err != nil {
		t.Fatal(err)
	}
	return skills
}

// fetchLocalDir 对本地目录做入库预览,并把其中唯一的候选入库,供只需一个本地技能的用例复用。
func fetchLocalDir(ctx context.Context, lib *library.Library, dir string) (library.Skill, error) {
	preview, err := lib.PreviewFetch(ctx, localRequest(dir))
	if err != nil {
		return library.Skill{}, err
	}
	if len(preview.Candidates) != 1 {
		return library.Skill{}, fmt.Errorf("local preview of %s found %d candidates, want 1", dir, len(preview.Candidates))
	}
	fetched, err := lib.Fetch(ctx, preview.ID, candidateNames(preview))
	if err != nil {
		return library.Skill{}, err
	}
	return fetched[0], nil
}

// writeLocalSkill 在 dir 写入 SKILL.md:frontmatter 的名字取目录名,描述为空,正文为 body。
func writeLocalSkill(t *testing.T, dir, body string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "SKILL.md"), []byte(skillDocument(filepath.Base(dir), body)), 0o644)
}

// fetchURLSkill 经 PreviewFetch + Fetch 从 URL 远端把名为 name 的 skill 入库。
func fetchURLSkill(t *testing.T, lib *library.Library, url, name string) library.Skill {
	t.Helper()
	preview, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{URL: url})
	if err != nil {
		t.Fatal(err)
	}
	return fetchSkills(t, lib, preview, []string{name})[0]
}

// stubGitHubCLI 隔离本机 gh 登录态,仅在进程边界提供受控响应。
func stubGitHubCLI(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "gh"), []byte("#!/bin/sh\n"+script), 0o755)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// checkSingleUpdate 通过公开批量接口检查一个技能,校验结果数量和身份,再交给场景断言状态或逐项错误。
func checkSingleUpdate(t *testing.T, lib *library.Library, ctx context.Context, id library.SkillID) (bool, error) {
	t.Helper()
	checks, err := lib.CheckUpdates(ctx, []library.SkillID{id})
	if err != nil {
		t.Fatalf("CheckUpdates(%d) failed: %v; want a completed item", id, err)
	}
	if len(checks) != 1 || checks[0].SkillID != id {
		t.Fatalf("CheckUpdates(%d) = %#v; want one matching result", id, checks)
	}
	if checks[0].Error != nil {
		return false, checks[0].Error
	}
	return checks[0].Available, nil
}

// updateCheckState 经列表接口读取指定技能的最近检查记录。
func updateCheckState(t *testing.T, lib *library.Library, id library.SkillID) *library.UpdateCheckState {
	t.Helper()
	skills, err := lib.ListSkills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range skills {
		if skill.ID == id {
			return skill.LastUpdateCheck
		}
	}
	t.Fatalf("skill %d missing", id)
	return nil
}

// requireMemberResults 断言组安装 / 加入成员的逐条结果;跳过项只比较错误码与 Details,不比较 Message。
func requireMemberResults(t *testing.T, got []library.GroupMemberResult, err error, want []library.GroupMemberResult) {
	t.Helper()
	if err != nil {
		t.Fatalf("member results error = %v, want nil", err)
	}
	normalized := make([]library.GroupMemberResult, len(got))
	for i, result := range got {
		normalized[i] = result
		if result.Error != nil {
			normalized[i].Error = &library.Error{Code: result.Error.Code, Details: result.Error.Details}
		}
	}
	if want == nil {
		want = []library.GroupMemberResult{}
	}
	if !reflect.DeepEqual(normalized, want) {
		t.Fatalf("member results = %s; want %s", memberResultsString(got), memberResultsString(want))
	}
}

// memberResultsString 把逐条结果格式化为一行,供失败信息对照。
func memberResultsString(results []library.GroupMemberResult) string {
	var parts []string
	for _, result := range results {
		part := fmt.Sprintf("{skill %d %+v %s", result.SkillID, result.Scope, result.Outcome)
		if result.Error != nil {
			part += fmt.Sprintf(" %s %+v", result.Error.Code, result.Error.Details)
		}
		parts = append(parts, part+"}")
	}
	return "[" + strings.Join(parts, " ") + "]"
}

// skippedMember 构造一条被跳过的期望结果。
func skippedMember(id library.SkillID, scope library.Scope, code library.ErrorCode, details library.ErrorDetails) library.GroupMemberResult {
	return library.GroupMemberResult{SkillID: id, Scope: scope, Outcome: library.MemberSkipped, Error: &library.Error{Code: code, Details: details}}
}

// withoutGroupCreatedAt 去掉组安装建立时间,供不关心时间戳的 DeepEqual 断言使用。
func withoutGroupCreatedAt(installs []library.GroupInstall) []library.GroupInstall {
	out := append([]library.GroupInstall(nil), installs...)
	for i := range out {
		out[i].CreatedAt = time.Time{}
	}
	return out
}

// skillFiles 读出技能当前版本的全部文件。
func skillFiles(t *testing.T, lib *library.Library, skill library.Skill) []library.File {
	t.Helper()
	version, err := lib.GetVersion(t.Context(), skill.VersionID)
	if err != nil {
		t.Fatal(err)
	}
	return version.Files
}
