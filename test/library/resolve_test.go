package library_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/tiaot33/skills-tui/internal/library"
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
		got, err := lib.Resolve(ctx, library.GlobalScope(agent))
		if err != nil || got != want {
			t.Fatalf("Resolve(GlobalScope(%s)) = %q, %v; want %q", agent, got, err, want)
		}
	}
}

// 全局目录以 Open 时的 $HOME 为准;之后改环境变量不能让 Uninstall 删到别处。
func TestResolveAndUninstallUseHomeBoundAtOpen(t *testing.T) {
	ctx := t.Context()
	homeA, homeB := t.TempDir(), t.TempDir()
	t.Setenv("HOME", homeA)
	lib := openTestLibrary(t)
	skill := fetchExampleSkill(t, lib, "example")
	scope := library.GlobalScope(library.AgentCodex)
	if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(homeA, ".agents", "skills", skill.Name, "SKILL.md")
	foreign := filepath.Join(homeB, ".agents", "skills", skill.Name, "SKILL.md")
	writeFile(t, foreign, []byte("other home content"), 0o644)
	t.Setenv("HOME", homeB)
	want := filepath.Join(homeA, ".agents", "skills")
	got, err := lib.Resolve(ctx, scope)
	if err != nil || got != want {
		t.Fatalf("Resolve after HOME change = %q, %v; want Open-time %q", got, err, want)
	}
	if err := lib.Uninstall(ctx, skill.ID, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(original); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("original skill scope remains: %v", err)
	}
	content, err := os.ReadFile(foreign)
	if err != nil || string(content) != "other home content" {
		t.Fatalf("unrelated skill scope in second HOME was removed: %q, %v", content, err)
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
		got, err := lib.Resolve(ctx, library.ProjectScope(agent, project.ID))
		if err != nil || got != want {
			t.Fatalf("Resolve(ProjectScope(%s, %d)) = %q, %v; want %q", agent, project.ID, got, err, want)
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

	cases := map[string]library.Scope{
		"unknown agent in global scope":    library.GlobalScope("cursor"),
		"unknown agent in project scope":   library.ProjectScope("cursor", project.ID),
		"unregistered project":             library.ProjectScope(library.AgentClaudeCode, project.ID+1),
		"empty agent code in global scope": library.GlobalScope(""),
	}
	for name, scope := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := lib.Resolve(ctx, scope)
			if !library.IsCode(err, library.NotFound) {
				t.Fatalf("Resolve(%#v) = %q, %v; want NotFound", scope, got, err)
			}
		})
	}
}

// Resolve 只读:不留下安装,也不在磁盘上创建目标目录。
func TestResolveIsReadOnly(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	scopes := []library.Scope{
		library.GlobalScope(library.AgentCodex),
		library.ProjectScope(library.AgentClaudeCode, project.ID),
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
	skillScopes, err := lib.ListSkillScopes(ctx)
	if err != nil || len(skillScopes) != 0 {
		t.Fatalf("ListSkillScopes() after Resolve = %#v, %v; want none", skillScopes, err)
	}
}
