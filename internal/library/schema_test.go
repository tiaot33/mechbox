package library_test

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tiaot33/skills-tui/internal/library"
)

// Open 只补缺失的 Agent 行、不覆盖已有布局;已按身份记录的投影记录在重开后按现行规则派生目录,
// 并可继续按身份激活。
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
	writeFile(t, filepath.Join(source, "SKILL.md"), []byte("example\n"), 0o644)
	skill, err := lib.Install(ctx, library.LocalSource{Path: source})
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
		if err := lib.Activate(ctx, skill.ID, scope, library.ActivateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := lib.Close(); err != nil {
		t.Fatal(err)
	}
	// 只用 SQL 构造缺 Agent 行且另一行布局被改过的输入文件;结果仍由公开门面观察。
	editLibraryFixture(t, path, `
		DELETE FROM agents WHERE code = 'codex';
		UPDATE agents SET name = 'Custom Claude', global_path = '.custom/global', project_path = '.custom/project' WHERE code = 'claude-code';
	`)
	// 目录按现行布局派生:claude-code 用表里保留的自定义规则,补种子的 codex 用种子规则。
	want := []library.Projection{
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
		got, err := reopened.ListProjections(ctx)
		if err != nil || !reflect.DeepEqual(withoutCreatedAt(got), want) {
			t.Fatalf("ListProjections after Open = %#v, %v; want %#v", got, err, want)
		}
		for _, projection := range want {
			if err := reopened.Activate(ctx, skill.ID, projection.Scope, library.ActivateOptions{}); err != nil {
				t.Fatal(err)
			}
		}
		if err := reopened.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOpenRejectsMissingScopeIdentityConstraint(t *testing.T) {
	for _, index := range []string{"projections_global_scope", "projections_project_scope", "group_activations_global_scope", "group_activations_project_scope"} {
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
			if invalid, err := library.Open(path); err == nil {
				_ = invalid.Close()
				t.Fatal("Open accepted a library without its Scope identity constraint")
			}
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
	editLibraryFixture(t, path, `PRAGMA user_version = 8`)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if old, err := library.Open(path); err == nil {
		_ = old.Close()
		t.Fatal("Open accepted a library with an older schema version")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("rejected Open changed the library file: %v", err)
	}
}
