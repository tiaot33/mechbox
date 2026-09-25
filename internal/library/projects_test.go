package library_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tiaot33/skills-tui/internal/library"
)

// 登记项目后,该项目与各 Agent 组成项目 Scope:激活落在 Resolve 给出的目录;UnregisterProject
// 清掉项目 Scope 记录,磁盘上的投影按 ProjectedCopies 决定去留,外来文件夹与全局激活不动。
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
	writeFile(t, filepath.Join(source, "SKILL.md"), []byte("# Example\n"), 0o644)
	skill, err := lib.Install(ctx, library.LocalSource{Path: source})
	if err != nil {
		t.Fatal(err)
	}
	for _, agent := range []library.AgentCode{library.AgentClaudeCode, library.AgentCodex} {
		if err := lib.Activate(ctx, skill.ID, library.GlobalScope(agent), library.ActivateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	globalProjections, err := lib.ListProjections(ctx)
	if err != nil || len(globalProjections) != 2 {
		t.Fatalf("global projections = %#v, %v; want 2", globalProjections, err)
	}
	if err := lib.Activate(ctx, skill.ID, keepScope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	keptProjection := filepath.Join(keepDirectory, skill.Name, "SKILL.md")
	if _, err := os.Stat(keptProjection); err != nil {
		t.Fatal(err)
	}
	if err := lib.UnregisterProject(ctx, project.ID, library.UnregisterProjectOptions{ProjectedCopies: library.Keep}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(keptProjection); err != nil {
		t.Fatalf("explicit Keep removed project projection: %v", err)
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
	if err := lib.AddSkillToGroup(ctx, group.ID, skill.ID); err != nil {
		t.Fatal(err)
	}
	if err := lib.ActivateGroup(ctx, group.ID, removeScope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	manualFile := filepath.Join(removeDirectory, "manual-skill", "mine.txt")
	writeFile(t, manualFile, []byte("mine"), 0o644)
	if err := lib.UnregisterProject(ctx, removeProject.ID, library.UnregisterProjectOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(removeDirectory, skill.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Remove left project projection behind: %v", err)
	}
	if _, err := os.Stat(manualFile); err != nil {
		t.Fatalf("Remove touched an untracked directory: %v", err)
	}
	projects, err = lib.ListRegisteredProjects(ctx)
	if err != nil || len(projects) != 0 {
		t.Fatalf("ListRegisteredProjects() after unregister = %#v, %v", projects, err)
	}
	projections, err := lib.ListProjections(ctx)
	if err != nil || !reflect.DeepEqual(projections, globalProjections) {
		t.Fatalf("ListProjections() after unregister = %#v, %v; want %#v", projections, err, globalProjections)
	}
	for _, projection := range globalProjections {
		requireProjection(t, projection.Directory, skill.Name, "# Example\n")
	}
	if err := lib.UnregisterProject(ctx, library.ProjectID(9999), library.UnregisterProjectOptions{}); !library.IsCode(err, library.NotFound) {
		t.Fatalf("UnregisterProject() unknown project error = %v", err)
	}
	if _, err := os.Stat(manualFile); err != nil {
		t.Fatalf("unknown unregister touched disk: %v", err)
	}
}

// UnregisterProject(Remove) 先删记录、把项目下全部投影改名暂存、提交后才真正删除:改名失败时
// 项目、记录与文件夹都原样不动;投影已被手动删掉时仍成功;成功后不留暂存残留。
func TestUnregisterProjectRemoveKeepsLibraryAndDiskTogether(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	present := installNamedSkill(t, lib, "present")
	missing := installNamedSkill(t, lib, "missing")
	scope := library.ProjectScope(library.AgentCodex, project.ID)
	directory, err := lib.Resolve(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range []library.Skill{present, missing} {
		if err := lib.Activate(ctx, skill.ID, scope, library.ActivateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.RemoveAll(filepath.Join(directory, missing.Name)); err != nil {
		t.Fatal(err)
	}
	before, err := lib.ListProjections(ctx)
	if err != nil {
		t.Fatal(err)
	}

	makeScopeReadOnly(t, directory)
	err = lib.UnregisterProject(ctx, project.ID, library.UnregisterProjectOptions{ProjectedCopies: library.Remove})
	requireLibraryError(t, err, library.Permission)
	if err := os.Chmod(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	projects, err := lib.ListRegisteredProjects(ctx)
	if err != nil || !reflect.DeepEqual(projects, []library.RegisteredProject{project}) {
		t.Fatalf("failed Remove changed projects: %#v, %v", projects, err)
	}
	after, err := lib.ListProjections(ctx)
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("failed Remove changed projections: %#v, %v; want %#v", after, err, before)
	}
	content, err := os.ReadFile(filepath.Join(directory, present.Name, "SKILL.md"))
	if err != nil || string(content) != "# present\n" {
		t.Fatalf("failed Remove changed projection: %q, %v", content, err)
	}
	requireNoStagedEntries(t, directory)

	if err := lib.UnregisterProject(ctx, project.ID, library.UnregisterProjectOptions{ProjectedCopies: library.Remove}); err != nil {
		t.Fatalf("Remove with a manually deleted projection = %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, present.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Remove left project projection behind: %v", err)
	}
	projects, err = lib.ListRegisteredProjects(ctx)
	if err != nil || len(projects) != 0 {
		t.Fatalf("ListRegisteredProjects() after unregister = %#v, %v", projects, err)
	}
	projections, err := lib.ListProjections(ctx)
	if err != nil || len(projections) != 0 {
		t.Fatalf("ListProjections() after unregister = %#v, %v", projections, err)
	}
	requireNoStagedEntries(t, directory)
}
