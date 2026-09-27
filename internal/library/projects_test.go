package library_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tiaot33/skills-tui/internal/library"
)

// 登记项目后,该项目与各 Agent 组成项目 Scope:安装落在 Resolve 给出的目录;UnregisterProject
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
	keepDirectory, err := lib.Resolve(ctx, keepScope)
	if err != nil {
		t.Fatal(err)
	}
	if keepDirectory != filepath.Join(project.Path, ".agents", "skills") {
		t.Fatalf("Resolve(%#v) = %q; want under registered project root", keepScope, keepDirectory)
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
	globalSkillScopes, err := lib.ListSkillScopes(ctx)
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
	if _, err := lib.Resolve(ctx, keepScope); !library.IsCode(err, library.NotFound) {
		t.Fatalf("Resolve() unregistered project scope error = %v, want NotFound", err)
	}

	removeProject, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	removeScope := library.ProjectScope(library.AgentClaudeCode, removeProject.ID)
	removeDirectory, err := lib.Resolve(ctx, removeScope)
	if err != nil {
		t.Fatal(err)
	}
	if removeDirectory != filepath.Join(removeProject.Path, ".claude", "skills") {
		t.Fatalf("Resolve(%#v) = %q; want under registered project root", removeScope, removeDirectory)
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
	skillScopes, err := lib.ListSkillScopes(ctx)
	if err != nil || !reflect.DeepEqual(skillScopes, globalSkillScopes) {
		t.Fatalf("ListSkillScopes() after unregister = %#v, %v; want %#v", skillScopes, err, globalSkillScopes)
	}
	for _, skillScope := range globalSkillScopes {
		requireInstalledFolder(t, skillScope.Directory, skill.Name, "# Example\n")
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
	directory, err := lib.Resolve(ctx, scope)
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
	before, err := lib.ListSkillScopes(ctx)
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
	after, err := lib.ListSkillScopes(ctx)
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
	skillScopes, err := lib.ListSkillScopes(ctx)
	if err != nil || len(skillScopes) != 0 {
		t.Fatalf("ListSkillScopes() after unregister = %#v, %v", skillScopes, err)
	}
	requireNoStagedEntries(t, directory)
}
