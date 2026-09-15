package library_test

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"errors"
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
	source := filepath.Join(t.TempDir(), name)
	writeFile(t, filepath.Join(source, "SKILL.md"), []byte("# "+name+"\n"), 0o644)
	skill, err := lib.Install(t.Context(), library.LocalSource{Path: source})
	if err != nil {
		t.Fatal(err)
	}
	return skill
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

// stubGitHubCLI 隔离本机 gh 登录态,仅在进程边界提供受控响应。
func stubGitHubCLI(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "gh"), []byte("#!/bin/sh\n"+script), 0o755)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
