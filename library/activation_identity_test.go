package library_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tiaot33/skills-tui/library"
)

// installExampleSkill 在隔离目录里安装一个带可执行脚本的 skill,供身份 Scope 的激活用例复用。
func installExampleSkill(t *testing.T, lib library.Library, name string) library.Skill {
	t.Helper()
	source := filepath.Join(t.TempDir(), name)
	writeFile(t, filepath.Join(source, "SKILL.md"), []byte("# "+name+"\n"), 0o644)
	writeFile(t, filepath.Join(source, "scripts", "run.sh"), []byte("#!/bin/sh\n"), 0o755)
	skill, err := lib.Install(t.Context(), library.LocalSource{Path: source})
	if err != nil {
		t.Fatal(err)
	}
	return skill
}

// 用全局 Scope 激活:文件夹落在 Resolve(该 Scope) 的目录下,留下一条按身份记录的独立 Activation。
func TestActivateGlobalScopeProjectsIntoResolvedDirectory(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	skill := installExampleSkill(t, lib, "example-skill")

	scope := library.GlobalScope{Agent: library.AgentCodex}
	if err := lib.Activate(ctx, skill.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	directory, err := lib.Resolve(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if directory != filepath.Join(home, ".agents", "skills") {
		t.Fatalf("Resolve(%#v) = %q; want under isolated HOME", scope, directory)
	}
	content, err := os.ReadFile(filepath.Join(directory, skill.Name, "SKILL.md"))
	if err != nil || string(content) != "# example-skill\n" {
		t.Fatalf("projected SKILL.md = %q, %v", content, err)
	}
	info, err := os.Stat(filepath.Join(directory, skill.Name, "scripts", "run.sh"))
	if err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("projected script = %v, %v; want executable", info, err)
	}

	activations, err := lib.ListActivations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []library.Activation{{
		SkillID:    skill.ID,
		Scope:      scope,
		Directory:  directory,
		Individual: true,
	}}
	if !reflect.DeepEqual(activations, want) {
		t.Fatalf("ListActivations() = %#v, want %#v", activations, want)
	}
}

// 用项目 Scope 激活:文件夹落在该 Registered project 根下 Resolve 出的目录;Deactivate 后
// 目录与记录一起消失,Library 里的 skill 仍在;再次 Deactivate 得到 NotFound。
func TestActivateAndDeactivateProjectScope(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	skill := installExampleSkill(t, lib, "example-skill")
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	scope := library.ProjectScope{Agent: library.AgentClaudeCode, ProjectID: project.ID}
	if err := lib.Activate(ctx, skill.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	directory, err := lib.Resolve(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if directory != filepath.Join(project.Path, ".claude", "skills") {
		t.Fatalf("Resolve(%#v) = %q; want under registered project root", scope, directory)
	}
	target := filepath.Join(directory, skill.Name)
	if _, err := os.Stat(filepath.Join(target, "SKILL.md")); err != nil {
		t.Fatalf("project projection missing: %v", err)
	}
	activations, err := lib.ListActivations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []library.Activation{{
		SkillID:    skill.ID,
		Scope:      scope,
		Directory:  directory,
		Individual: true,
	}}
	if !reflect.DeepEqual(activations, want) {
		t.Fatalf("ListActivations() = %#v, want %#v", activations, want)
	}

	if err := lib.Deactivate(ctx, skill.ID, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deactivated directory still exists: %v", err)
	}
	if _, err := lib.GetSkill(ctx, skill.ID); err != nil {
		t.Fatalf("Deactivate removed library skill: %v", err)
	}
	if activations, err := lib.ListActivations(ctx); err != nil || len(activations) != 0 {
		t.Fatalf("ListActivations() after deactivate = %#v, %v", activations, err)
	}
	if err := lib.Deactivate(ctx, skill.ID, scope); !library.IsCode(err, library.NotFound) {
		t.Fatalf("Deactivate() inactive skill error = %v, want NotFound", err)
	}
}

// 身份型 Scope 上的冲突策略与路径型 Scope 一致:外来文件夹默认 AlreadyExists,Overwrite 替换;
// 对自己的投影再次激活幂等刷新,不算冲突。
func TestActivateIdentityScopeConflictPolicy(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	skill := installExampleSkill(t, lib, "example-skill")
	scope := library.GlobalScope{Agent: library.AgentClaudeCode}
	directory, err := lib.Resolve(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(directory, skill.Name)
	writeFile(t, filepath.Join(target, "old.txt"), []byte("old"), 0o644)

	if err := lib.Activate(ctx, skill.ID, scope, library.ActivateOptions{}); !library.IsCode(err, library.AlreadyExists) {
		t.Fatalf("Activate() onto foreign directory = %v, want AlreadyExists", err)
	}
	if _, err := os.Stat(filepath.Join(target, "old.txt")); err != nil {
		t.Fatalf("failed Activate touched the foreign directory: %v", err)
	}
	if err := lib.Activate(ctx, skill.ID, scope, library.ActivateOptions{OnConflict: library.Overwrite}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, "old.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Overwrite kept foreign file: %v", err)
	}

	// 目标现在是我们自己的投影:默认策略下再次激活成功并刷新内容。
	writeFile(t, filepath.Join(target, "SKILL.md"), []byte("edited\n"), 0o644)
	if err := lib.Activate(ctx, skill.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatalf("re-Activate under Fail policy = %v, want success", err)
	}
	content, err := os.ReadFile(filepath.Join(target, "SKILL.md"))
	if err != nil || string(content) != "# example-skill\n" {
		t.Fatalf("re-Activate did not refresh SKILL.md: %q, %v", content, err)
	}
	activations, err := lib.ListActivations(ctx)
	want := []library.Activation{{SkillID: skill.ID, Scope: scope, Directory: directory, Individual: true}}
	if err != nil || !reflect.DeepEqual(activations, want) {
		t.Fatalf("ListActivations() after re-Activate = %#v, %v; want %#v", activations, err, want)
	}
}

// 身份型 Scope 上的多依据语义与路径型 Scope 一致:独立 + Group 叠加;DeactivateGroup 只撤组依据,
// 独立撑着的副本留下;仅组依据的投影单项 Deactivate 得 NotFound;组未激活时 DeactivateGroup 得 NotFound。
func TestActivateGroupOnIdentityScopePreservesIndependentActivation(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	first := installExampleSkill(t, lib, "first")
	second := installExampleSkill(t, lib, "second")
	group, err := lib.CreateGroup(ctx, "shipping")
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range []library.Skill{first, second} {
		if err := lib.AddSkillToGroup(ctx, group.ID, skill.ID); err != nil {
			t.Fatal(err)
		}
	}
	scope := library.GlobalScope{Agent: library.AgentCodex}
	directory, err := lib.Resolve(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.DeactivateGroup(ctx, group.ID, scope); !library.IsCode(err, library.NotFound) {
		t.Fatalf("DeactivateGroup() before activation = %v, want NotFound", err)
	}

	if err := lib.Activate(ctx, first.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := lib.ActivateGroup(ctx, group.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatalf("ActivateGroup onto own projection under Fail policy = %v, want success", err)
	}
	if err := lib.ActivateGroup(ctx, group.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatalf("re-ActivateGroup under Fail policy = %v, want success", err)
	}
	for _, skill := range []library.Skill{first, second} {
		if _, err := os.Stat(filepath.Join(directory, skill.Name, "SKILL.md")); err != nil {
			t.Fatalf("member %q was not projected: %v", skill.Name, err)
		}
	}
	activations, err := lib.ListActivations(ctx)
	want := []library.Activation{
		{SkillID: first.ID, Scope: scope, Directory: directory, Individual: true, ViaGroups: []library.GroupID{group.ID}},
		{SkillID: second.ID, Scope: scope, Directory: directory, ViaGroups: []library.GroupID{group.ID}},
	}
	if err != nil || !reflect.DeepEqual(activations, want) {
		t.Fatalf("ListActivations() = %#v, %v; want %#v", activations, err, want)
	}

	if err := lib.Deactivate(ctx, second.ID, scope); !library.IsCode(err, library.NotFound) {
		t.Fatalf("Deactivate() via-group-only member = %v, want NotFound", err)
	}
	if err := lib.DeactivateGroup(ctx, group.ID, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, first.Name, "SKILL.md")); err != nil {
		t.Fatalf("independent activation was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, second.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("via-group-only activation remains: %v", err)
	}
	activations, err = lib.ListActivations(ctx)
	want = []library.Activation{{SkillID: first.ID, Scope: scope, Directory: directory, Individual: true}}
	if err != nil || !reflect.DeepEqual(activations, want) {
		t.Fatalf("ListActivations() after DeactivateGroup = %#v, %v; want %#v", activations, err, want)
	}
}

// 整组激活在身份路径上仍是原子的:某成员目标被外来文件夹占用时整组拒绝,不留半组投影。
func TestActivateGroupOnIdentityScopeIsAtomic(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	first := installExampleSkill(t, lib, "first")
	second := installExampleSkill(t, lib, "second")
	group, err := lib.CreateGroup(ctx, "shipping")
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range []library.Skill{first, second} {
		if err := lib.AddSkillToGroup(ctx, group.ID, skill.ID); err != nil {
			t.Fatal(err)
		}
	}
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	scope := library.ProjectScope{Agent: library.AgentCodex, ProjectID: project.ID}
	directory, err := lib.Resolve(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(directory, second.Name, "manual.txt"), []byte("manual"), 0o644)

	if err := lib.ActivateGroup(ctx, group.ID, scope, library.ActivateOptions{}); !library.IsCode(err, library.AlreadyExists) {
		t.Fatalf("ActivateGroup() conflict error = %v, want AlreadyExists", err)
	}
	if _, err := os.Stat(filepath.Join(directory, first.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("first member was partially activated: %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, second.Name, "manual.txt")); err != nil {
		t.Fatalf("conflicting directory changed: %v", err)
	}
	if activations, err := lib.ListActivations(ctx); err != nil || len(activations) != 0 {
		t.Fatalf("ListActivations() = %#v, %v; want none", activations, err)
	}
}

// 同一 skill 可同时占用两个 Agent 的全局 Scope,以及同一 Agent 的全局与项目 Scope:各是一条 Activation。
func TestIdentityScopesOfOneSkillAreIndependentActivations(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	skill := installExampleSkill(t, lib, "example-skill")
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	scopes := []library.Scope{
		library.GlobalScope{Agent: library.AgentClaudeCode},
		library.GlobalScope{Agent: library.AgentCodex},
		library.ProjectScope{Agent: library.AgentClaudeCode, ProjectID: project.ID},
	}
	for _, scope := range scopes {
		if err := lib.Activate(ctx, skill.ID, scope, library.ActivateOptions{}); err != nil {
			t.Fatalf("Activate(%#v) = %v", scope, err)
		}
	}
	activations, err := lib.ListActivations(ctx)
	if err != nil || len(activations) != len(scopes) {
		t.Fatalf("ListActivations() = %#v, %v; want %d activations", activations, err, len(scopes))
	}
	for _, scope := range scopes {
		directory, err := lib.Resolve(ctx, scope)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(directory, skill.Name, "SKILL.md")); err != nil {
			t.Fatalf("projection for %#v missing: %v", scope, err)
		}
	}
	// 撤销其中一条不影响其余两条。
	if err := lib.Deactivate(ctx, skill.ID, scopes[0]); err != nil {
		t.Fatal(err)
	}
	activations, err = lib.ListActivations(ctx)
	if err != nil || len(activations) != 2 {
		t.Fatalf("ListActivations() after one Deactivate = %#v, %v; want 2", activations, err)
	}
	for _, activation := range activations {
		if activation.Scope == scopes[0] {
			t.Fatalf("deactivated scope still listed: %#v", activation)
		}
	}
}

// UnregisterProject 也清掉按身份记录的项目 Scope Activation:默认保留磁盘副本,Remove 时一起删,
// 同目录里的外来文件夹不动;该 Agent 的全局 Activation 不受影响。
func TestUnregisterProjectClearsIdentityProjectActivations(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	skill := installExampleSkill(t, lib, "example-skill")
	global := library.GlobalScope{Agent: library.AgentCodex}
	if err := lib.Activate(ctx, skill.ID, global, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}

	keepProject, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	keepScope := library.ProjectScope{Agent: library.AgentCodex, ProjectID: keepProject.ID}
	if err := lib.Activate(ctx, skill.ID, keepScope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	keepDirectory, err := lib.Resolve(ctx, keepScope)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.UnregisterProject(ctx, keepProject.ID, library.UnregisterProjectOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(keepDirectory, skill.Name, "SKILL.md")); err != nil {
		t.Fatalf("default Keep removed project projection: %v", err)
	}

	removeProject, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	removeScope := library.ProjectScope{Agent: library.AgentCodex, ProjectID: removeProject.ID}
	group, err := lib.CreateGroup(ctx, "project group")
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.AddSkillToGroup(ctx, group.ID, skill.ID); err != nil {
		t.Fatal(err)
	}
	if err := lib.ActivateGroup(ctx, group.ID, removeScope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	removeDirectory, err := lib.Resolve(ctx, removeScope)
	if err != nil {
		t.Fatal(err)
	}
	manualFile := filepath.Join(removeDirectory, "manual-skill", "mine.txt")
	writeFile(t, manualFile, []byte("mine"), 0o644)
	if err := lib.UnregisterProject(ctx, removeProject.ID, library.UnregisterProjectOptions{ProjectedCopies: library.Remove}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(removeDirectory, skill.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Remove left project projection behind: %v", err)
	}
	if _, err := os.Stat(manualFile); err != nil {
		t.Fatalf("Remove touched an untracked directory: %v", err)
	}

	globalDirectory, err := lib.Resolve(ctx, global)
	if err != nil {
		t.Fatal(err)
	}
	activations, err := lib.ListActivations(ctx)
	want := []library.Activation{{SkillID: skill.ID, Scope: global, Directory: globalDirectory, Individual: true}}
	if err != nil || !reflect.DeepEqual(activations, want) {
		t.Fatalf("ListActivations() after unregister = %#v, %v; want %#v", activations, err, want)
	}
}

// DeleteGroup 与 RemoveSkill 在身份型记录上保持原有语义:删组把仅由该组撑着的投影转为独立保留;
// RemoveSkill 按 ProjectedCopies 决定磁盘副本去留。
func TestDeleteGroupAndRemoveSkillOnIdentityScope(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	skill := installExampleSkill(t, lib, "example-skill")
	group, err := lib.CreateGroup(ctx, "g")
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.AddSkillToGroup(ctx, group.ID, skill.ID); err != nil {
		t.Fatal(err)
	}
	scope := library.GlobalScope{Agent: library.AgentClaudeCode}
	if err := lib.ActivateGroup(ctx, group.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	directory, err := lib.Resolve(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.DeleteGroup(ctx, group.ID); err != nil {
		t.Fatal(err)
	}
	activations, err := lib.ListActivations(ctx)
	want := []library.Activation{{SkillID: skill.ID, Scope: scope, Directory: directory, Individual: true}}
	if err != nil || !reflect.DeepEqual(activations, want) {
		t.Fatalf("ListActivations() after DeleteGroup = %#v, %v; want %#v", activations, err, want)
	}
	if _, err := os.Stat(filepath.Join(directory, skill.Name, "SKILL.md")); err != nil {
		t.Fatalf("DeleteGroup removed projection: %v", err)
	}

	if err := lib.RemoveSkill(ctx, skill.ID, library.RemoveSkillOptions{ProjectedCopies: library.Remove}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, skill.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("RemoveSkill(Remove) left projection: %v", err)
	}
	if activations, err := lib.ListActivations(ctx); err != nil || len(activations) != 0 {
		t.Fatalf("ListActivations() after RemoveSkill = %#v, %v; want none", activations, err)
	}
}

// 两个不同身份的 Scope 解析到同一目录(把 $HOME 本身登记为项目)时,第二次激活不能悄悄改写
// 第一条 Activation 的身份:得到 AlreadyExists,原记录与磁盘保持原样。
func TestActivateDoesNotRewriteAnotherScopeSharingTheDirectory(t *testing.T) {
	ctx := t.Context()
	// RegisterProject 会解析符号链接(macOS 的 /var → /private/var),HOME 先取规范路径以便两边一致。
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	skill := installExampleSkill(t, lib, "example-skill")
	project, err := lib.RegisterProject(ctx, home)
	if err != nil {
		t.Fatal(err)
	}
	global := library.GlobalScope{Agent: library.AgentCodex}
	projectScope := library.ProjectScope{Agent: library.AgentCodex, ProjectID: project.ID}
	directory, err := lib.Resolve(ctx, global)
	if err != nil {
		t.Fatal(err)
	}
	if projectDirectory, err := lib.Resolve(ctx, projectScope); err != nil || projectDirectory != directory {
		t.Fatalf("precondition: scopes should share a directory, got %q vs %q (%v)", projectDirectory, directory, err)
	}

	if err := lib.Activate(ctx, skill.ID, global, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, opts := range []library.ActivateOptions{{}, {OnConflict: library.Overwrite}} {
		if err := lib.Activate(ctx, skill.ID, projectScope, opts); !library.IsCode(err, library.AlreadyExists) {
			t.Fatalf("Activate(%#v, %#v) = %v, want AlreadyExists", projectScope, opts, err)
		}
	}
	activations, err := lib.ListActivations(ctx)
	want := []library.Activation{{SkillID: skill.ID, Scope: global, Directory: directory, Individual: true}}
	if err != nil || !reflect.DeepEqual(activations, want) {
		t.Fatalf("ListActivations() = %#v, %v; want %#v", activations, err, want)
	}
	if _, err := os.Stat(filepath.Join(directory, skill.Name, "SKILL.md")); err != nil {
		t.Fatalf("global projection lost: %v", err)
	}
}

// 身份 Scope 里的错误指认在激活时同样得到 NotFound,不会写到任何目录。
func TestActivateUnknownAgentOrProjectIsNotFound(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	skill := installExampleSkill(t, lib, "example-skill")

	for name, scope := range map[string]library.Scope{
		"unknown agent":        library.GlobalScope{Agent: "cursor"},
		"unregistered project": library.ProjectScope{Agent: library.AgentClaudeCode, ProjectID: 42},
	} {
		t.Run(name, func(t *testing.T) {
			if err := lib.Activate(ctx, skill.ID, scope, library.ActivateOptions{}); !library.IsCode(err, library.NotFound) {
				t.Fatalf("Activate(%#v) = %v, want NotFound", scope, err)
			}
		})
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed Activate wrote into HOME: %v, %v", entries, err)
	}
}
