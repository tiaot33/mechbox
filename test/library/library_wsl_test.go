//go:build !windows

package library_test

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tiaot33/mechbox/internal/library"
)

// 选了 WSL 发行版时,各 Agent 的全局目录在发行版的主目录下,经 \\wsl.localhost\<发行版>\... 读写。
// 测试里 MECHBOX_TEST_WSL_SHELL 让「发行版」就是本机的 sh,发行版里的路径就是本机路径,
// 所以 \\wsl.localhost\<发行版>\<本机路径> 与本机路径指同一处,可以直接核对磁盘。
// 每个发行版的 sh 启动一次、一直用下去,带着启动时的 HOME,所以每个测试用自己名字的发行版。

func wslPath(t *testing.T, path string) string {
	return `\\wsl.localhost\` + t.Name() + strings.ReplaceAll(path, "/", `\`)
}

func openWSLLibrary(t *testing.T) (*library.Library, string) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("MECHBOX_TEST_WSL_SHELL", "sh")
	lib := openTestLibrary(t)
	if err := lib.SetWSLDistro(t.Context(), t.Name()); err != nil {
		t.Fatal(err)
	}
	return lib, home
}

func TestWSLDistroMovesGlobalDirectories(t *testing.T) {
	lib, home := openWSLLibrary(t)
	ctx := t.Context()
	distros, err := lib.ListWSLDistros(ctx)
	if err != nil || !slices.Equal(distros, []string{"test"}) {
		t.Fatalf("ListWSLDistros = %v, %v", distros, err)
	}
	if got := lib.HomeDirectory(); got != wslPath(t, home) {
		t.Fatalf("HomeDirectory = %q", got)
	}
	directory, err := lib.ScopeDirectory(ctx, library.GlobalScope(library.AgentClaudeCode))
	if err != nil || directory != wslPath(t, filepath.Join(home, ".claude/skills")) {
		t.Fatalf("ScopeDirectory = %q, %v", directory, err)
	}
	writeFile(t, filepath.Join(home, ".claude/skills/own/SKILL.md"), []byte(skillDocument("own", "v1")), 0o644)
	agents, err := lib.ListAgents(ctx)
	if err != nil || agents[0].Code != library.AgentClaudeCode || !agents[0].Detected || agents[0].GlobalDirectory != directory {
		t.Fatalf("agents = %+v, %v", agents, err)
	}
	if err := lib.SetWSLDistro(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if got := lib.HomeDirectory(); got != home {
		t.Fatalf("HomeDirectory after switching back = %q", got)
	}
}

func TestWSLDistroInstallKeepsExecutableScripts(t *testing.T) {
	lib, home := openWSLLibrary(t)
	ctx := t.Context()
	source := filepath.Join(t.TempDir(), "runner")
	writeFile(t, filepath.Join(source, "SKILL.md"), []byte(skillDocument("runner", "v1")), 0o644)
	writeFile(t, filepath.Join(source, "scripts/run.sh"), []byte("#!/bin/sh\necho run\n"), 0o755)
	source, _ = filepath.EvalSymlinks(source)
	skill, err := fetchLocalDir(ctx, lib, wslPath(t, source))
	if err != nil {
		t.Fatal(err)
	}
	files := skillFiles(t, lib, skill)
	if i := slices.IndexFunc(files, func(f library.File) bool { return f.Path == "scripts/run.sh" }); i < 0 || files[i].Mode != library.ModeExecutable {
		t.Fatalf("fetched files = %+v", files)
	}

	scope := library.GlobalScope(library.AgentClaudeCode)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(home, ".claude/skills/runner/scripts/run.sh")
	if info, err := os.Stat(script); err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("installed script = %v, %v", info, err)
	}
	requireWSLStatus(t, lib, skill.ID, library.SkillScopeCurrent)

	writeFile(t, script, []byte("#!/bin/sh\necho changed\n"), 0o755)
	requireWSLStatus(t, lib, skill.ID, library.SkillScopeModified)
	results, err := lib.Reinstall(ctx, []library.ReinstallRequest{{SkillID: skill.ID, Scope: scope, Overwrite: true}})
	if err != nil || len(results) != 1 || results[0].Error != nil {
		t.Fatalf("Reinstall = %+v, %v", results, err)
	}
	requireWSLStatus(t, lib, skill.ID, library.SkillScopeCurrent)

	if err := lib.Uninstall(ctx, skill.ID, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude/skills/runner")); !os.IsNotExist(err) {
		t.Fatalf("uninstalled folder: %v", err)
	}
	requireNoStagedEntries(t, filepath.Join(home, ".claude/skills"))
}

func TestWSLDistroTakesOverNpxSymlinks(t *testing.T) {
	lib, home := openWSLLibrary(t)
	ctx := t.Context()
	writeFile(t, filepath.Join(home, ".agents/skills/tdd/SKILL.md"), []byte(skillDocument("tdd", "v1")), 0o644)
	writeFile(t, filepath.Join(home, ".agents/skills/tdd/check.sh"), []byte("#!/bin/sh\n"), 0o755)
	link := filepath.Join(home, ".claude/skills/tdd")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../.agents/skills/tdd", link); err != nil {
		t.Fatal(err)
	}

	unmanaged, err := lib.ListUnmanagedSkills(ctx)
	if err != nil {
		t.Fatal(err)
	}
	claude := library.GlobalScope(library.AgentClaudeCode)
	i := slices.IndexFunc(unmanaged, func(s library.UnmanagedSkill) bool { return s.Scope == claude && s.Name == "tdd" })
	if i < 0 || unmanaged[i].LinkTarget != wslPath(t, filepath.Join(home, ".agents/skills/tdd")) || unmanaged[i].Folder != wslPath(t, link) {
		t.Fatalf("unmanaged = %+v", unmanaged)
	}
	result, err := lib.FetchUnmanaged(ctx, library.FetchUnmanagedRequest{Scope: claude, Name: "tdd"})
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("fetching changed the link: %v %v", info, err)
	}
	inspections, err := lib.InspectSkillScopes(ctx, library.SkillScopeFilter{SkillID: result.Skill.ID})
	if err != nil {
		t.Fatal(err)
	}
	j := slices.IndexFunc(inspections, func(s library.SkillScopeInspection) bool { return s.Scope == claude })
	if j < 0 || inspections[j].Status != library.SkillScopeCurrent || inspections[j].LinkTarget != wslPath(t, filepath.Join(home, ".agents/skills/tdd")) {
		t.Fatalf("inspections = %+v", inspections)
	}

	results, err := lib.Reinstall(ctx, []library.ReinstallRequest{{SkillID: result.Skill.ID, Scope: claude}})
	if err != nil || len(results) != 1 || results[0].Error != nil {
		t.Fatalf("Reinstall = %+v, %v", results, err)
	}
	info, err := os.Lstat(link)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("reinstalled folder is still a link: %v %v", info, err)
	}
	if info, err := os.Stat(filepath.Join(link, "check.sh")); err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("reinstalled script = %v, %v", info, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents/skills/tdd/SKILL.md")); err != nil {
		t.Fatalf("link target touched: %v", err)
	}
}

func TestWSLDistroProjects(t *testing.T) {
	lib, _ := openWSLLibrary(t)
	ctx := t.Context()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, ".claude/skills/local/SKILL.md"), []byte(skillDocument("local", "v1")), 0o644)
	project, err := lib.RegisterProject(ctx, wslPath(t, root))
	if err != nil {
		t.Fatal(err)
	}
	if project.Path != wslPath(t, root) || project.Name != filepath.Base(root) || project.Missing {
		t.Fatalf("project = %+v", project)
	}
	unmanaged, err := lib.ListUnmanagedSkills(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scope := library.ProjectScope(library.AgentClaudeCode, project.ID)
	if !slices.ContainsFunc(unmanaged, func(s library.UnmanagedSkill) bool { return s.Scope == scope && s.Name == "local" }) {
		t.Fatalf("unmanaged = %+v", unmanaged)
	}
	if _, err := lib.RegisterProject(ctx, wslPath(t, filepath.Join(root, "missing"))); err == nil {
		t.Fatal("registered a missing project")
	}
}

// 巡检、列出未纳管技能、整批入库前的确认一次读很多技能文件夹:往发行版发的命令按批执行,次数不随文件夹个数增长。
func TestWSLDistroReadsManyFoldersInFewCommands(t *testing.T) {
	lib, home := openWSLLibrary(t)
	ctx := t.Context()
	log := filepath.Join(t.TempDir(), "requests")
	shell := filepath.Join(t.TempDir(), "sh")
	writeFile(t, shell, []byte("#!/bin/sh\ntee -a '"+log+"' | sh\n"), 0o755)
	t.Setenv("MECHBOX_TEST_WSL_SHELL", shell)
	codex := library.GlobalScope(library.AgentCodex)
	claude := library.GlobalScope(library.AgentClaudeCode)
	add := func(from, to int) {
		var items []library.UnmanagedFetchItem
		for i := from; i < to; i++ {
			name := fmt.Sprintf("skill-%d", i)
			writeFile(t, filepath.Join(home, ".agents/skills", name, "SKILL.md"), []byte(skillDocument(name, "v1")), 0o644)
			writeFile(t, filepath.Join(home, ".agents/skills", name, "scripts/run.sh"), []byte("#!/bin/sh\n"), 0o755)
			link := filepath.Join(home, ".claude/skills", name)
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("../../.agents/skills/"+name, link); err != nil {
				t.Fatal(err)
			}
			items = append(items, library.UnmanagedFetchItem{Folders: []library.FolderInScope{{Scope: codex, Name: name}}})
		}
		fetched, err := lib.FetchUnmanagedSkills(ctx, library.FetchUnmanagedSkillsRequest{Skills: items})
		if err != nil {
			t.Fatal(err)
		}
		for _, result := range fetched {
			if result.Error != nil {
				t.Fatalf("FetchUnmanagedSkills: %+v", result.Error)
			}
		}
	}
	commands := func() map[string]int {
		counts := map[string]int{}
		for name, operation := range map[string]func() error{
			"ListUnmanagedSkills": func() error { _, err := lib.ListUnmanagedSkills(ctx); return err },
			"PlanFetchUnmanaged": func() error {
				_, err := lib.PlanFetchUnmanaged(ctx, library.PlanFetchUnmanagedRequest{Scopes: []library.Scope{claude}})
				return err
			},
			"InspectSkillScopes": func() error {
				inspections, err := lib.InspectSkillScopes(ctx, library.SkillScopeFilter{})
				for _, inspection := range inspections {
					if inspection.Status != library.SkillScopeCurrent {
						t.Fatalf("%s: %s", inspection.Folder, inspection.Status)
					}
				}
				return err
			},
		} {
			before := wslCommands(t, log)
			if err := operation(); err != nil {
				t.Fatal(err)
			}
			counts[name] = wslCommands(t, log) - before
		}
		return counts
	}
	add(0, 2)
	few := commands()
	for name, count := range few {
		if count == 0 {
			t.Fatalf("%s ran no commands in the distribution", name)
		}
	}
	add(2, 8)
	if many := commands(); !maps.Equal(few, many) {
		t.Fatalf("commands with 2 folders = %v, with 8 = %v", few, many)
	}
}

func wslCommands(t *testing.T, log string) int {
	t.Helper()
	content, err := os.ReadFile(log)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return bytes.Count(content, []byte("\nset --"))
}

func TestWSLDistroUnavailable(t *testing.T) {
	lib := openTestLibrary(t)
	t.Setenv("MECHBOX_TEST_WSL_SHELL", "/nonexistent/sh")
	home := lib.HomeDirectory()
	err := lib.SetWSLDistro(t.Context(), "gone")
	requireLibraryError(t, err, library.FailedPrecondition)
	if lib.HomeDirectory() != home {
		t.Fatalf("home changed after a failed switch: %q", lib.HomeDirectory())
	}
}

func requireWSLStatus(t *testing.T, lib *library.Library, id library.SkillID, want library.SkillScopeStatus) {
	t.Helper()
	inspections, err := lib.InspectSkillScopes(t.Context(), library.SkillScopeFilter{SkillID: id})
	if err != nil {
		t.Fatal(err)
	}
	if len(inspections) == 0 {
		t.Fatal("no inspections")
	}
	for _, inspection := range inspections {
		if inspection.Status != want {
			t.Fatalf("%v status = %s, want %s", inspection.Scope, inspection.Status, want)
		}
	}
}
