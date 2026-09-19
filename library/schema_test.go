package library_test

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tiaot33/skills-tui/library"
)

// Open 只补缺失的 Agent 行、不覆盖已有布局;已按身份记录的 Activation 在重开后按现行规则派生目录,
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
		library.GlobalScope{Agent: library.AgentClaudeCode},
		library.GlobalScope{Agent: library.AgentCodex},
		library.ProjectScope{Agent: library.AgentCodex, ProjectID: project.ID},
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
	want := []library.Activation{
		{SkillID: skill.ID, Scope: scopes[0], Directory: filepath.Join(home, ".custom", "global"), Individual: true},
		{SkillID: skill.ID, Scope: scopes[1], Directory: filepath.Join(home, ".agents", "skills"), Individual: true},
		{SkillID: skill.ID, Scope: scopes[2], Directory: filepath.Join(project.Path, ".agents", "skills"), Individual: true},
	}
	for range 2 {
		reopened, err := library.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = reopened.Close() })
		got, err := reopened.ListActivations(ctx)
		if err != nil || !reflect.DeepEqual(withoutActivatedAt(got), want) {
			t.Fatalf("ListActivations after Open = %#v, %v; want %#v", got, err, want)
		}
		for _, activation := range want {
			if err := reopened.Activate(ctx, skill.ID, activation.Scope, library.ActivateOptions{}); err != nil {
				t.Fatal(err)
			}
		}
		if err := reopened.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOpenRejectsMissingScopeIdentityConstraint(t *testing.T) {
	for _, index := range []string{"activations_global_scope", "activations_project_scope"} {
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
