package library_test

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tiaot33/skills-tui/internal/library"
)

// Scope 以 {"kind","agent","project_id"?} 往返 JSON:读出来的就是传回去的,没有目录字段。
func TestScopeJSONRoundTrip(t *testing.T) {
	cases := map[string]struct {
		scope library.Scope
		json  string
	}{
		"global":  {library.GlobalScope(library.AgentCodex), `{"kind":"global","agent":"codex"}`},
		"project": {library.ProjectScope(library.AgentClaudeCode, 7), `{"kind":"project","agent":"claude-code","project_id":7}`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			encoded, err := json.Marshal(c.scope)
			if err != nil {
				t.Fatal(err)
			}
			if string(encoded) != c.json {
				t.Fatalf("Marshal(%#v) = %s, want %s", c.scope, encoded, c.json)
			}
			var decoded library.Scope
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			if decoded != c.scope {
				t.Fatalf("Unmarshal(%s) = %#v, want %#v", encoded, decoded, c.scope)
			}
		})
	}
}

// ListSkillScopes 读出的 Scope 经 JSON 往返后仍能原样传回 Uninstall。
func TestSkillScopeRoundTripsThroughJSONIntoUninstall(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	skill := fetchExampleSkill(t, lib, "demo")
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.Install(ctx, skill.ID, library.ProjectScope(library.AgentCodex, project.ID), library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	skillScopes, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{})
	if err != nil || len(skillScopes) != 1 {
		t.Fatalf("ListSkillScopes() = %#v, %v; want one skill scope", skillScopes, err)
	}
	encoded, err := json.Marshal(skillScopes[0])
	if err != nil {
		t.Fatal(err)
	}
	var decoded library.SkillScope
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := lib.Uninstall(ctx, decoded.SkillID, decoded.Scope); err != nil {
		t.Fatalf("Uninstall(decoded scope %#v) = %v", decoded.Scope, err)
	}
	if skillScopes, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{}); err != nil || len(skillScopes) != 0 {
		t.Fatalf("ListSkillScopes() after Uninstall = %#v, %v; want none", skillScopes, err)
	}
}

// 每个接收 Scope 的方法在入口拒绝非法组合,Details.Field 指向出错的字段;不触及技能或分组是否存在。
func TestInvalidScopeIsInvalidArgumentOnEveryScopeMethod(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)

	scopes := map[string]struct {
		scope library.Scope
		field string
	}{
		"zero value":                {library.Scope{}, "kind"},
		"unknown kind":              {library.Scope{Kind: "workspace", Agent: library.AgentCodex}, "kind"},
		"global with a project":     {library.Scope{Kind: library.ScopeGlobal, Agent: library.AgentCodex, ProjectID: 1}, "project_id"},
		"project without a project": {library.Scope{Kind: library.ScopeProject, Agent: library.AgentCodex}, "project_id"},
		"project with negative id":  {library.Scope{Kind: library.ScopeProject, Agent: library.AgentCodex, ProjectID: -1}, "project_id"},
	}
	methods := map[string]func(library.Scope) error{
		"ScopeDirectory": func(scope library.Scope) error {
			_, err := lib.ScopeDirectory(ctx, scope)
			return err
		},
		"PlanInstall": func(scope library.Scope) error {
			_, err := lib.PlanInstall(ctx, 1, scope)
			return err
		},
		"Install": func(scope library.Scope) error {
			return lib.Install(ctx, 1, scope, library.InstallOptions{})
		},
		"Uninstall": func(scope library.Scope) error {
			return lib.Uninstall(ctx, 1, scope)
		},
		"PlanGroupInstall": func(scope library.Scope) error {
			_, err := lib.PlanGroupInstall(ctx, 1, scope)
			return err
		},
		"InstallGroup": func(scope library.Scope) error {
			_, err := lib.InstallGroup(ctx, 1, scope, library.InstallOptions{})
			return err
		},
		"UninstallGroup": func(scope library.Scope) error {
			return lib.UninstallGroup(ctx, 1, scope)
		},
	}
	for method, call := range methods {
		for name, c := range scopes {
			t.Run(method+"/"+name, func(t *testing.T) {
				requireErrorDetails(t, call(c.scope), library.InvalidArgument, library.ErrorDetails{Field: c.field})
			})
		}
	}
}

// ScopeDirectory(全局 Scope) 按该 Agent 的规则算出 $HOME 下的目录;测试隔离 HOME,不碰真实主目录。
func TestScopeDirectoryGlobalScopeUsesHomeAndAgentRule(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)

	cases := map[library.AgentCode]string{
		library.AgentClaudeCode: filepath.Join(home, ".claude", "skills"),
		library.AgentCodex:      filepath.Join(home, ".agents", "skills"),
	}
	for agent, want := range cases {
		got, err := lib.ScopeDirectory(ctx, library.GlobalScope(agent))
		if err != nil || got != want {
			t.Fatalf("ScopeDirectory(GlobalScope(%s)) = %q, %v; want %q", agent, got, err, want)
		}
	}
}

// 全局目录以 Open 时的 $HOME 为准;之后改环境变量不能让 Uninstall 删到别处。
func TestScopeDirectoryAndUninstallUseHomeBoundAtOpen(t *testing.T) {
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
	got, err := lib.ScopeDirectory(ctx, scope)
	if err != nil || got != want {
		t.Fatalf("ScopeDirectory after HOME change = %q, %v; want Open-time %q", got, err, want)
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

// ScopeDirectory(项目 Scope) 按该 Agent 的规则算出 Registered project 根下的目录。
func TestScopeDirectoryProjectScopeUsesRegisteredRootAndAgentRule(t *testing.T) {
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
		got, err := lib.ScopeDirectory(ctx, library.ProjectScope(agent, project.ID))
		if err != nil || got != want {
			t.Fatalf("ScopeDirectory(ProjectScope(%s, %d)) = %q, %v; want %q", agent, project.ID, got, err, want)
		}
	}
}

// 拼错 Agent 代码或引用未登记的项目都得到 NotFound,而不是算出一条不存在的目录。
func TestScopeDirectoryUnknownAgentOrProjectIsNotFound(t *testing.T) {
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
			got, err := lib.ScopeDirectory(ctx, scope)
			if !library.IsCode(err, library.NotFound) {
				t.Fatalf("ScopeDirectory(%#v) = %q, %v; want NotFound", scope, got, err)
			}
		})
	}
}

// ScopeDirectory 只读:不留下安装,也不在磁盘上创建目标目录。
func TestScopeDirectoryIsReadOnly(t *testing.T) {
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
		directory, err := lib.ScopeDirectory(ctx, scope)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(directory); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("ScopeDirectory(%#v) left %q on disk: %v", scope, directory, err)
		}
	}
	skillScopes, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{})
	if err != nil || len(skillScopes) != 0 {
		t.Fatalf("ListSkillScopes() after ScopeDirectory = %#v, %v; want none", skillScopes, err)
	}
}

// 登记项目后,该项目与各 Agent 组成项目 Scope:安装落在 ScopeDirectory 给出的目录;UnregisterProject
// 清掉项目 Scope 记录,磁盘上的技能文件夹按 Folders 决定去留,外来文件夹与全局安装不动。
func TestRegisteredProjectScopesAndUnregisterOptions(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	if _, err := lib.RegisterProject(ctx, ""); err == nil {
		t.Fatal("RegisterProject() accepted an empty path")
	} else {
		requireErrorDetails(t, err, library.InvalidArgument, library.ErrorDetails{Field: "path"})
	}
	notDir := filepath.Join(t.TempDir(), "file")
	writeFile(t, notDir, []byte("x"), 0o644)
	if _, err := lib.RegisterProject(ctx, notDir); err == nil {
		t.Fatal("RegisterProject() accepted a file")
	} else {
		requireErrorDetails(t, err, library.InvalidArgument, library.ErrorDetails{Field: "path"})
	}

	projectRoot := t.TempDir()
	project, err := lib.RegisterProject(ctx, projectRoot+string(os.PathSeparator)+".")
	if err != nil {
		t.Fatal(err)
	}
	wantRoot, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	if project.ID == 0 || project.Path != wantRoot {
		t.Fatalf("RegisterProject() = %#v, want canonical path %q", project, wantRoot)
	}
	if _, err := lib.RegisterProject(ctx, projectRoot); !library.IsCode(err, library.AlreadyExists) {
		t.Fatalf("duplicate RegisterProject() error = %v", err)
	} else {
		requireErrorDetails(t, err, library.AlreadyExists, library.ErrorDetails{ProjectID: project.ID, Path: project.Path})
	}
	projects, err := lib.ListRegisteredProjects(ctx)
	if err != nil || !reflect.DeepEqual(projects, []library.RegisteredProject{project}) {
		t.Fatalf("ListRegisteredProjects() = %#v, %v", projects, err)
	}
	keepScope := library.ProjectScope(library.AgentCodex, project.ID)
	keepDirectory, err := lib.ScopeDirectory(ctx, keepScope)
	if err != nil {
		t.Fatal(err)
	}
	if keepDirectory != filepath.Join(project.Path, ".agents", "skills") {
		t.Fatalf("ScopeDirectory(%#v) = %q; want under registered project root", keepScope, keepDirectory)
	}

	source := filepath.Join(t.TempDir(), "example-skill")
	writeLocalSkill(t, source, "# Example\n")
	skill, err := fetchLocalDir(ctx, lib, source)
	if err != nil {
		t.Fatal(err)
	}
	for _, agent := range []library.AgentCode{library.AgentClaudeCode, library.AgentCodex} {
		if err := lib.Install(ctx, skill.ID, library.GlobalScope(agent), library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	globalSkillScopes, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{})
	if err != nil || len(globalSkillScopes) != 2 {
		t.Fatalf("global skill scopes = %#v, %v; want 2", globalSkillScopes, err)
	}
	if err := lib.Install(ctx, skill.ID, keepScope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	keptSkillScope := filepath.Join(keepDirectory, skill.Name, "SKILL.md")
	if _, err := os.Stat(keptSkillScope); err != nil {
		t.Fatal(err)
	}
	if err := lib.UnregisterProject(ctx, project.ID, library.UnregisterProjectOptions{Folders: library.Keep}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(keptSkillScope); err != nil {
		t.Fatalf("explicit Keep removed project skill scope: %v", err)
	}
	if _, err := lib.ScopeDirectory(ctx, keepScope); !library.IsCode(err, library.NotFound) {
		t.Fatalf("ScopeDirectory() unregistered project scope error = %v, want NotFound", err)
	}

	removeProject, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	removeScope := library.ProjectScope(library.AgentClaudeCode, removeProject.ID)
	removeDirectory, err := lib.ScopeDirectory(ctx, removeScope)
	if err != nil {
		t.Fatal(err)
	}
	if removeDirectory != filepath.Join(removeProject.Path, ".claude", "skills") {
		t.Fatalf("ScopeDirectory(%#v) = %q; want under registered project root", removeScope, removeDirectory)
	}
	group, err := lib.CreateGroup(ctx, "project group")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.AddSkillToGroup(ctx, group.ID, skill.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.InstallGroup(ctx, group.ID, removeScope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	manualFile := filepath.Join(removeDirectory, "manual-skill", "mine.txt")
	writeFile(t, manualFile, []byte("mine"), 0o644)
	if err := lib.UnregisterProject(ctx, removeProject.ID, library.UnregisterProjectOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(removeDirectory, skill.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Remove left project skill scope behind: %v", err)
	}
	if _, err := os.Stat(manualFile); err != nil {
		t.Fatalf("Remove touched an untracked directory: %v", err)
	}
	projects, err = lib.ListRegisteredProjects(ctx)
	if err != nil || len(projects) != 0 {
		t.Fatalf("ListRegisteredProjects() after unregister = %#v, %v", projects, err)
	}
	skillScopes, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{})
	if err != nil || !reflect.DeepEqual(skillScopes, globalSkillScopes) {
		t.Fatalf("ListSkillScopes() after unregister = %#v, %v; want %#v", skillScopes, err, globalSkillScopes)
	}
	for _, skillScope := range globalSkillScopes {
		requireInstalledFolder(t, filepath.Dir(skillScope.Folder), skill.Name, "# Example\n")
	}
	if err := lib.UnregisterProject(ctx, library.ProjectID(9999), library.UnregisterProjectOptions{}); !library.IsCode(err, library.NotFound) {
		t.Fatalf("UnregisterProject() unknown project error = %v", err)
	}
	if _, err := os.Stat(manualFile); err != nil {
		t.Fatalf("unknown unregister touched disk: %v", err)
	}
}

// UnregisterProject(Remove) 先删记录、把项目下全部技能文件夹改名暂存、提交后才真正删除:改名失败时
// 项目、记录与文件夹都原样不动;技能文件夹已被手动删掉时仍成功;成功后不留暂存残留。
func TestUnregisterProjectRemoveKeepsLibraryAndDiskTogether(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	present := fetchNamedSkill(t, lib, "present")
	missing := fetchNamedSkill(t, lib, "missing")
	scope := library.ProjectScope(library.AgentCodex, project.ID)
	directory, err := lib.ScopeDirectory(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range []library.Skill{present, missing} {
		if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.RemoveAll(filepath.Join(directory, missing.Name)); err != nil {
		t.Fatal(err)
	}
	before, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{})
	if err != nil {
		t.Fatal(err)
	}

	makeScopeReadOnly(t, directory)
	err = lib.UnregisterProject(ctx, project.ID, library.UnregisterProjectOptions{Folders: library.Remove})
	requireLibraryError(t, err, library.Permission)
	if err := os.Chmod(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	projects, err := lib.ListRegisteredProjects(ctx)
	if err != nil || !reflect.DeepEqual(projects, []library.RegisteredProject{project}) {
		t.Fatalf("failed Remove changed projects: %#v, %v", projects, err)
	}
	after, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{})
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("failed Remove changed skill scopes: %#v, %v; want %#v", after, err, before)
	}
	content, err := os.ReadFile(filepath.Join(directory, present.Name, "SKILL.md"))
	if err != nil || string(content) != skillDocument("present", "# present\n") {
		t.Fatalf("failed Remove changed skill scope: %q, %v", content, err)
	}
	requireNoStagedEntries(t, directory)

	if err := lib.UnregisterProject(ctx, project.ID, library.UnregisterProjectOptions{Folders: library.Remove}); err != nil {
		t.Fatalf("Remove with a manually deleted skill scope = %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, present.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Remove left project skill scope behind: %v", err)
	}
	projects, err = lib.ListRegisteredProjects(ctx)
	if err != nil || len(projects) != 0 {
		t.Fatalf("ListRegisteredProjects() after unregister = %#v, %v", projects, err)
	}
	skillScopes, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{})
	if err != nil || len(skillScopes) != 0 {
		t.Fatalf("ListSkillScopes() after unregister = %#v, %v", skillScopes, err)
	}
	requireNoStagedEntries(t, directory)
}

// 项目名是项目目录的名字;列表按项目名排序,同名再按路径,库不为同名项目改名。
func TestRegisteredProjectsAreNamedAndSortedByName(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	var registered []library.RegisteredProject
	for _, root := range []string{
		filepath.Join(t.TempDir(), "zeta"),
		filepath.Join(t.TempDir(), "alpha"),
		filepath.Join(t.TempDir(), "same"),
		filepath.Join(t.TempDir(), "same"),
	} {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		project, err := lib.RegisterProject(ctx, root)
		if err != nil {
			t.Fatal(err)
		}
		if project.Name != filepath.Base(root) || project.Missing {
			t.Fatalf("RegisterProject(%q) = %#v; want Name %q and not missing", root, project, filepath.Base(root))
		}
		registered = append(registered, project)
	}
	zeta, alpha, same1, same2 := registered[0], registered[1], registered[2], registered[3]
	if same2.Path < same1.Path {
		same1, same2 = same2, same1
	}
	projects, err := lib.ListRegisteredProjects(ctx)
	if want := []library.RegisteredProject{alpha, same1, same2, zeta}; err != nil || !reflect.DeepEqual(projects, want) {
		t.Fatalf("ListRegisteredProjects() = %#v, %v; want %#v", projects, err, want)
	}
}

// 路径不存在(或路径中间有一段是文件)是参数错误,不是来源连不上。
func TestRegisterProjectMissingPathIsInvalidArgument(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	file := filepath.Join(t.TempDir(), "file")
	writeFile(t, file, []byte("x"), 0o644)
	for _, path := range []string{filepath.Join(t.TempDir(), "absent"), filepath.Join(file, "child")} {
		_, err := lib.RegisterProject(ctx, path)
		requireErrorDetails(t, err, library.InvalidArgument, library.ErrorDetails{Field: "path"})
	}
	if projects, err := lib.ListRegisteredProjects(ctx); err != nil || len(projects) != 0 {
		t.Fatalf("ListRegisteredProjects() = %#v, %v; want none", projects, err)
	}
}

// 项目目录不在这台机器上时,列表里标出 Missing;目录回来后标记随之消失。
func TestListRegisteredProjectsMarksProjectsNotOnThisMachine(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	root := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	project, err := lib.RegisterProject(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(project.Path); err != nil {
		t.Fatal(err)
	}
	missing := project
	missing.Missing = true
	projects, err := lib.ListRegisteredProjects(ctx)
	if err != nil || !reflect.DeepEqual(projects, []library.RegisteredProject{missing}) {
		t.Fatalf("ListRegisteredProjects() with project removed = %#v, %v; want %#v", projects, err, missing)
	}
	if err := os.MkdirAll(project.Path, 0o755); err != nil {
		t.Fatal(err)
	}
	projects, err = lib.ListRegisteredProjects(ctx)
	if err != nil || !reflect.DeepEqual(projects, []library.RegisteredProject{project}) {
		t.Fatalf("ListRegisteredProjects() with project restored = %#v, %v; want %#v", projects, err, project)
	}
}

// 项目目录树被移走、原来的上级目录处留下一个同名文件时,该项目同样算不在这台机器上:列表照常返回,其他项目不受影响。
func TestListRegisteredProjectsMarksProjectWhoseParentBecameAFile(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	parent := filepath.Join(t.TempDir(), "parent")
	if err := os.MkdirAll(filepath.Join(parent, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	moved, err := lib.RegisterProject(ctx, filepath.Join(parent, "app"))
	if err != nil {
		t.Fatal(err)
	}
	other, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Dir(moved.Path), filepath.Join(t.TempDir(), "elsewhere")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Dir(moved.Path), []byte("not a directory"), 0o644)
	missing := moved
	missing.Missing = true
	want := []library.RegisteredProject{missing, other}
	if other.Name < missing.Name {
		want = []library.RegisteredProject{other, missing}
	}
	projects, err := lib.ListRegisteredProjects(ctx)
	if err != nil || !reflect.DeepEqual(projects, want) {
		t.Fatalf("ListRegisteredProjects() = %#v, %v; want %#v", projects, err, want)
	}
}

// 项目不在这台机器上时也能注销:要删的文件夹本来就不在,算成功,安装记录与组安装一并撤掉。
func TestUnregisterProjectNotOnThisMachine(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	skill := fetchNamedSkill(t, lib, "demo")
	scope := library.ProjectScope(library.AgentCodex, project.ID)
	if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	group, err := lib.CreateGroup(ctx, "project group")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.InstallGroup(ctx, group.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(project.Path); err != nil {
		t.Fatal(err)
	}
	if err := lib.UnregisterProject(ctx, project.ID, library.UnregisterProjectOptions{}); err != nil {
		t.Fatalf("UnregisterProject() of a project not on this machine = %v", err)
	}
	if projects, err := lib.ListRegisteredProjects(ctx); err != nil || len(projects) != 0 {
		t.Fatalf("ListRegisteredProjects() after unregister = %#v, %v", projects, err)
	}
	if skillScopes := listSkillScopes(t, lib); len(skillScopes) != 0 {
		t.Fatalf("ListSkillScopes() after unregister = %#v", skillScopes)
	}
	if installs, err := lib.ListGroupInstalls(ctx); err != nil || len(installs) != 0 {
		t.Fatalf("ListGroupInstalls() after unregister = %#v, %v", installs, err)
	}
}

// 改项目位置后项目 ID、直接安装与组安装都延续,目录改按新位置算;旧位置的文件夹原样留着,库不再管。
// 新位置的状态照常由巡检给出:新位置没有这些文件夹时是 missing,整个项目目录搬过去时是 current。
func TestRelocateProjectKeepsInstallsAndLeavesOldFolders(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	oldRoot := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(oldRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	project, err := lib.RegisterProject(ctx, oldRoot)
	if err != nil {
		t.Fatal(err)
	}
	direct := fetchNamedSkill(t, lib, "direct")
	member := fetchNamedSkill(t, lib, "member")
	directScope := library.ProjectScope(library.AgentCodex, project.ID)
	groupScope := library.ProjectScope(library.AgentClaudeCode, project.ID)
	if err := lib.Install(ctx, direct.ID, directScope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	group, err := lib.CreateGroup(ctx, "project group")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.AddSkillToGroup(ctx, group.ID, member.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.InstallGroup(ctx, group.ID, groupScope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	groupInstalls, err := lib.ListGroupInstalls(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before := listSkillScopes(t, lib)

	emptyRoot := filepath.Join(t.TempDir(), "app-moved")
	if err := os.MkdirAll(emptyRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	relocated, err := lib.RelocateProject(ctx, project.ID, emptyRoot)
	if err != nil {
		t.Fatal(err)
	}
	wantPath, err := filepath.EvalSymlinks(emptyRoot)
	if err != nil {
		t.Fatal(err)
	}
	want := library.RegisteredProject{ID: project.ID, Name: "app-moved", Path: wantPath}
	if relocated != want {
		t.Fatalf("RelocateProject() = %#v, want %#v", relocated, want)
	}
	if projects, err := lib.ListRegisteredProjects(ctx); err != nil || !reflect.DeepEqual(projects, []library.RegisteredProject{want}) {
		t.Fatalf("ListRegisteredProjects() after relocate = %#v, %v; want %#v", projects, err, want)
	}
	for _, scope := range []library.Scope{directScope, groupScope} {
		directory, err := lib.ScopeDirectory(ctx, scope)
		if err != nil || filepath.Dir(filepath.Dir(directory)) != wantPath {
			t.Fatalf("ScopeDirectory(%#v) after relocate = %q, %v; want under %q", scope, directory, err, wantPath)
		}
	}
	after := listSkillScopes(t, lib)
	if len(after) != len(before) {
		t.Fatalf("ListSkillScopes() after relocate = %#v, want same records as %#v", after, before)
	}
	for i := range after {
		moved := before[i]
		moved.Folder = filepath.Join(wantPath, strings.TrimPrefix(before[i].Folder, project.Path))
		if !reflect.DeepEqual(after[i], moved) {
			t.Fatalf("ListSkillScopes()[%d] after relocate = %#v, want %#v", i, after[i], moved)
		}
	}
	if got, err := lib.ListGroupInstalls(ctx); err != nil || !reflect.DeepEqual(got, groupInstalls) {
		t.Fatalf("ListGroupInstalls() after relocate = %#v, %v; want %#v", got, err, groupInstalls)
	}
	for _, skill := range []library.Skill{direct, member} {
		matches, err := filepath.Glob(filepath.Join(project.Path, "*", "skills", skill.Name, "SKILL.md"))
		if err != nil || len(matches) != 1 {
			t.Fatalf("old folder of %s after relocate = %v, %v; want left in place", skill.Name, matches, err)
		}
	}
	requireInspectionStatuses(t, lib, library.SkillScopeMissing, library.SkillScopeMissing)

	movedRoot := filepath.Join(t.TempDir(), "app")
	if err := os.Rename(project.Path, movedRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.RelocateProject(ctx, project.ID, movedRoot); err != nil {
		t.Fatal(err)
	}
	requireInspectionStatuses(t, lib, library.SkillScopeCurrent, library.SkillScopeCurrent)
}

// 改项目位置的路径规则与登记相同;新路径已被另一个项目占用是 AlreadyExists,改到原路径什么都不变。
func TestRelocateProjectRejectsInvalidOrTakenPaths(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	other, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "file")
	writeFile(t, file, []byte("x"), 0o644)
	for _, path := range []string{"", filepath.Join(t.TempDir(), "absent"), file} {
		_, err := lib.RelocateProject(ctx, project.ID, path)
		requireErrorDetails(t, err, library.InvalidArgument, library.ErrorDetails{Field: "path"})
	}
	_, err = lib.RelocateProject(ctx, project.ID, other.Path)
	requireErrorDetails(t, err, library.AlreadyExists, library.ErrorDetails{ProjectID: other.ID, Path: other.Path})
	if _, err := lib.RelocateProject(ctx, other.ID+1, t.TempDir()); !library.IsCode(err, library.NotFound) {
		t.Fatalf("RelocateProject() unknown project error = %v, want NotFound", err)
	}
	same, err := lib.RelocateProject(ctx, project.ID, project.Path)
	if err != nil || same != project {
		t.Fatalf("RelocateProject() to its own path = %#v, %v; want %#v", same, err, project)
	}
	projects, err := lib.ListRegisteredProjects(ctx)
	want := []library.RegisteredProject{project, other}
	if project.Path > other.Path {
		want = []library.RegisteredProject{other, project}
	}
	if err != nil || !reflect.DeepEqual(projects, want) {
		t.Fatalf("ListRegisteredProjects() = %#v, %v; want %#v", projects, err, want)
	}
}
