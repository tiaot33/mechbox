package library_test

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tiaot33/skills-tui/internal/library"
)

// 内置 Agent 的名称和目录以应用版本为准:库里的行缺了或被改过,重开后都恢复为应用内置的值;
// 已按身份记录的 SkillScope 按恢复后的目录算出,并可继续按身份安装。
func TestOpenRestoresBuiltinAgentsFromApplication(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(t.TempDir(), "library.db")
	lib, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
	ctx := t.Context()
	source := filepath.Join(t.TempDir(), "example")
	writeLocalSkill(t, source, "example\n")
	skill, err := fetchLocalDir(ctx, lib, source)
	if err != nil {
		t.Fatal(err)
	}
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	scopes := []library.Scope{
		library.GlobalScope(library.AgentClaudeCode),
		library.GlobalScope(library.AgentCodex),
		library.ProjectScope(library.AgentClaudeCode, project.ID),
	}
	for _, scope := range scopes {
		if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := lib.Close(); err != nil {
		t.Fatal(err)
	}
	// 只用 SQL 构造缺 Agent 行且另一行名称与目录被改过的输入文件,相当于旧版应用写下的库;结果仍经技能库公开方法观察。
	editLibraryFixture(t, path, `
		DELETE FROM agents WHERE code = 'codex';
		UPDATE agents SET name = 'Old Claude', global_path = '.old/global', project_path = '.old/project' WHERE code = 'claude-code';
	`)
	want := []library.SkillScope{
		{SkillID: skill.ID, Scope: scopes[0], Directory: filepath.Join(home, ".claude", "skills"), Direct: true},
		{SkillID: skill.ID, Scope: scopes[2], Directory: filepath.Join(project.Path, ".claude", "skills"), Direct: true},
		{SkillID: skill.ID, Scope: scopes[1], Directory: filepath.Join(home, ".agents", "skills"), Direct: true},
	}
	for range 2 {
		reopened, err := library.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = reopened.Close() })
		agents, err := reopened.ListAgents(ctx)
		if err != nil || !reflect.DeepEqual(agents, seededAgents()) {
			t.Fatalf("ListAgents after Open = %#v, %v; want %#v", agents, err, seededAgents())
		}
		got, err := reopened.ListSkillScopes(ctx)
		if err != nil || !reflect.DeepEqual(withoutCreatedAt(got), want) {
			t.Fatalf("ListSkillScopes after Open = %#v, %v; want %#v", got, err, want)
		}
		for _, skillScope := range want {
			if err := reopened.Install(ctx, skill.ID, skillScope.Scope, library.InstallOptions{}); err != nil {
				t.Fatal(err)
			}
		}
		if err := reopened.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// 库里有、本版应用不认识的 Agent(例如从装了更新版本应用的机器同步来)保持原样:照样列出,目录按库里的规则算。
func TestOpenKeepsAgentsUnknownToThisVersion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(t.TempDir(), "library.db")
	lib, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.Close(); err != nil {
		t.Fatal(err)
	}
	editLibraryFixture(t, path, `INSERT INTO agents(code, name, global_path, project_path) VALUES ('future-agent', 'Future Agent', '.future/skills', '.future/project-skills')`)
	reopened := openTestLibraryAt(t, path)
	ctx := t.Context()
	agents, err := reopened.ListAgents(ctx)
	want := append(seededAgents(), library.Agent{Code: "future-agent", Name: "Future Agent"})
	if err != nil || !reflect.DeepEqual(agents, want) {
		t.Fatalf("ListAgents after Open = %#v, %v; want %#v", agents, err, want)
	}
	directory, err := reopened.ScopeDirectory(ctx, library.GlobalScope("future-agent"))
	if wantDirectory := filepath.Join(home, ".future", "skills"); err != nil || directory != wantDirectory {
		t.Fatalf("ScopeDirectory(future-agent) = %q, %v; want %q", directory, err, wantDirectory)
	}
}

func TestOpenRejectsMissingScopeIdentityConstraint(t *testing.T) {
	for _, index := range []string{"skill_scopes_global_scope", "skill_scopes_project_scope", "group_installs_global_scope", "group_installs_project_scope"} {
		t.Run(index, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "library.db")
			lib, err := library.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := lib.Close(); err != nil {
				t.Fatal(err)
			}
			editLibraryFixture(t, path, `DROP INDEX `+index+`; DELETE FROM agents WHERE code = 'codex'`)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			invalid, err := library.Open(path)
			if err == nil {
				_ = invalid.Close()
			}
			requireLibraryError(t, err, library.FailedPrecondition)
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("rejected Open changed the library file: %v", err)
			}
		})
	}
}

// 产品尚未发布,不迁移旧版本库:user_version 不是当前版本的库被拒绝,文件保持不变。
func TestOpenRejectsOtherSchemaVersionsWithoutChangingTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")
	lib, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.Close(); err != nil {
		t.Fatal(err)
	}
	editLibraryFixture(t, path, `PRAGMA user_version = 9`)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	old, err := library.Open(path)
	if err == nil {
		_ = old.Close()
	}
	requireLibraryError(t, err, library.FailedPrecondition)
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("rejected Open changed the library file: %v", err)
	}
}

// 用户选错了文件(不是 SQLite 数据库)时,Open 返回 FailedPrecondition,文件保持不变。
func TestOpenRejectsNonDatabaseFileWithoutChangingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.txt")
	before := bytes.Repeat([]byte("not a sqlite database\n"), 256)
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	lib, err := library.Open(path)
	if err == nil {
		_ = lib.Close()
	}
	requireLibraryError(t, err, library.FailedPrecondition)
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("rejected Open changed the file: %v", err)
	}
}
