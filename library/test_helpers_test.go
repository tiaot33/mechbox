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
	"testing"

	"github.com/tiaot33/skills-tui/library"
)

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

// stubHTTPResponse 让所有 HTTP 请求都得到同一个响应,用于模拟远端返回的状态与内容。
func stubHTTPResponse(t *testing.T, status int, body io.Reader) {
	t.Helper()
	client := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: fetchTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: status, Body: io.NopCloser(body), Header: make(http.Header)}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = client })
}

// stubGitHubCLI 隔离本机 gh 登录态,仅在进程边界提供受控响应。
func stubGitHubCLI(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "gh"), []byte("#!/bin/sh\n"+script), 0o755)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
