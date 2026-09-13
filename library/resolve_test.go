package library_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/tiaot33/skills-tui/library"
)

// Resolve(全局 Scope) 按该 Agent 的规则算出 $HOME 下的目录;测试隔离 HOME,不碰真实主目录。
func TestResolveGlobalScopeUsesHomeAndAgentRule(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)

	cases := map[library.AgentCode]string{
		library.AgentClaudeCode: filepath.Join(home, ".claude", "skills"),
		library.AgentCodex:      filepath.Join(home, ".agents", "skills"),
	}
	for agent, want := range cases {
		got, err := lib.Resolve(ctx, library.GlobalScope{Agent: agent})
		if err != nil || got != want {
			t.Fatalf("Resolve(GlobalScope{%s}) = %q, %v; want %q", agent, got, err, want)
		}
	}
}

// Resolve(项目 Scope) 按该 Agent 的规则算出 Registered project 根下的目录。
func TestResolveProjectScopeUsesRegisteredRootAndAgentRule(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	cases := map[library.AgentCode]string{
		library.AgentClaudeCode: filepath.Join(project.Path, ".claude", "skills"),
		library.AgentCodex:      filepath.Join(project.Path, ".agents", "skills"),
	}
	for agent, want := range cases {
		got, err := lib.Resolve(ctx, library.ProjectScope{Agent: agent, ProjectID: project.ID})
		if err != nil || got != want {
			t.Fatalf("Resolve(ProjectScope{%s, %d}) = %q, %v; want %q", agent, project.ID, got, err, want)
		}
	}
}

// 拼错 Agent 代码或引用未登记的项目都得到 NotFound,而不是算出一条不存在的目录。
func TestResolveUnknownAgentOrProjectIsNotFound(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	cases := map[string]library.ScopeIdentity{
		"unknown agent in global scope":    library.GlobalScope{Agent: "cursor"},
		"unknown agent in project scope":   library.ProjectScope{Agent: "cursor", ProjectID: project.ID},
		"unregistered project":             library.ProjectScope{Agent: library.AgentClaudeCode, ProjectID: project.ID + 1},
		"empty agent code in global scope": library.GlobalScope{},
	}
	for name, scope := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := lib.Resolve(ctx, scope)
			if !library.IsCode(err, library.NotFound) {
				t.Fatalf("Resolve(%#v) = %q, %v; want NotFound", scope, got, err)
			}
		})
	}
	// 没有 Scope 就无从解析:这是参数错误,不是「找不到」。
	if got, err := lib.Resolve(ctx, nil); !library.IsCode(err, library.InvalidArgument) {
		t.Fatalf("Resolve(nil) = %q, %v; want InvalidArgument", got, err)
	}
}

// Resolve 只是预览:不留下 Activation,也不在磁盘上创建目标目录。
func TestResolveIsReadOnly(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	scopes := []library.ScopeIdentity{
		library.GlobalScope{Agent: library.AgentCodex},
		library.ProjectScope{Agent: library.AgentClaudeCode, ProjectID: project.ID},
	}
	for _, scope := range scopes {
		directory, err := lib.Resolve(ctx, scope)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(directory); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("Resolve(%#v) left %q on disk: %v", scope, directory, err)
		}
	}
	activations, err := lib.ListActivations(ctx)
	if err != nil || len(activations) != 0 {
		t.Fatalf("ListActivations() after Resolve = %#v, %v; want none", activations, err)
	}
}

// 目录规则来自库内 agents 表的那一行,而不是代码里的常量:改了表里的规则,Resolve 随之变化。
func TestResolveReadsLayoutRulesFromAgentsTable(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(t.TempDir(), "library.db")
	lib, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.Close(); err != nil {
		t.Fatal(err)
	}
	// 只用 SQL 构造输入文件:改写 codex 的两段规则,再多加一个非内置 Agent。
	editLibraryFixture(t, path, `
		UPDATE agents SET global_path = '.custom/global', project_path = '.custom/project' WHERE code = 'codex';
		INSERT INTO agents(code, name, global_path, project_path) VALUES ('another', 'Another', '.another/skills', 'tools/another');
	`)
	reopened, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	cases := map[library.ScopeIdentity]string{
		library.GlobalScope{Agent: library.AgentCodex}:                         filepath.Join(home, ".custom", "global"),
		library.ProjectScope{Agent: library.AgentCodex, ProjectID: project.ID}: filepath.Join(project.Path, ".custom", "project"),
		library.GlobalScope{Agent: "another"}:                                  filepath.Join(home, ".another", "skills"),
		library.ProjectScope{Agent: "another", ProjectID: project.ID}:          filepath.Join(project.Path, "tools", "another"),
	}
	for scope, want := range cases {
		got, err := reopened.Resolve(ctx, scope)
		if err != nil || got != want {
			t.Fatalf("Resolve(%#v) = %q, %v; want %q", scope, got, err, want)
		}
	}
}
