package library_test

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tiaot33/skills-tui/internal/library"
)

// Open 只补缺失的 Agent 行、不覆盖已有 Agent 目录;已按身份记录的 SkillScope 在重开后按 agents 表重新算出目录,
// 并可继续按身份安装。
func TestOpenRepairsMissingAgentsAndAcceptsExistingRules(t *testing.T) {
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
		library.ProjectScope(library.AgentCodex, project.ID),
	}
	for _, scope := range scopes {
		if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := lib.Close(); err != nil {
		t.Fatal(err)
	}
	// 只用 SQL 构造缺 Agent 行且另一行 Agent 目录被改过的输入文件;结果仍经技能库公开方法观察。
	editLibraryFixture(t, path, `
		DELETE FROM agents WHERE code = 'codex';
		UPDATE agents SET name = 'Custom Claude', global_path = '.custom/global', project_path = '.custom/project' WHERE code = 'claude-code';
	`)
	// 目录按 agents 表算出:claude-code 用表里保留的自定义规则,补种子的 codex 用种子规则。
	want := []library.SkillScope{
		{SkillID: skill.ID, Scope: scopes[0], Directory: filepath.Join(home, ".custom", "global"), Direct: true},
		{SkillID: skill.ID, Scope: scopes[1], Directory: filepath.Join(home, ".agents", "skills"), Direct: true},
		{SkillID: skill.ID, Scope: scopes[2], Directory: filepath.Join(project.Path, ".agents", "skills"), Direct: true},
	}
	for range 2 {
		reopened, err := library.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = reopened.Close() })
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
