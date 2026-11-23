//go:build !windows

package library_test

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tiaot33/mechbox/internal/library"
	"github.com/tiaot33/mechbox/internal/library/platformtest"
)

// 选了 WSL 发行版时,各 Agent 的全局目录在发行版的主目录下,经 \\wsl.localhost\<发行版>\... 读写。
// 测试里 platformtest.EmulateWSL 让「发行版」就是本机的 sh,发行版里的路径就是本机路径,
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
	setTestHome(t, home)
	// 充当发行版的是本机的 sh,发行版的主目录就是它看到的 $HOME。
	t.Setenv("HOME", home)
	platformtest.EmulateWSL(t, "sh")
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
	if err != nil || agents[0].Code != library.AgentClaudeCode || !agents[0].Detected || agents[0].GlobalDirectories[0] != directory {
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

	if err := lib.Uninstall(ctx, skill.ID, scope, library.UninstallOptions{}); err != nil {
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
	log := recordWSLCommands(t)
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

// recordWSLCommands 让充当发行版的 sh 把收到的每条命令追加进一个文件,返回这个文件。
func recordWSLCommands(t *testing.T) string {
	t.Helper()
	log := filepath.Join(t.TempDir(), "requests")
	shell := filepath.Join(t.TempDir(), "sh")
	writeFile(t, shell, []byte("#!/bin/sh\ntee -a '"+log+"' | sh\n"), 0o755)
	platformtest.EmulateWSL(t, shell)
	return log
}

func wslCommands(t *testing.T, log string) int {
	t.Helper()
	return len(wslCommandsSince(t, log, 0))
}

// wslCommandsDuring 返回 run 执行期间往发行版发的每条命令(参数加脚本)。
func wslCommandsDuring(t *testing.T, log string, run func()) []string {
	t.Helper()
	before := wslCommands(t, log)
	run()
	return wslCommandsSince(t, log, before)
}

func wslCommandsSince(t *testing.T, log string, skip int) []string {
	t.Helper()
	content, err := os.ReadFile(log)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var commands []string
	for _, command := range strings.Split(string(content), "\nset --")[1:] {
		// 每条命令末尾取输出长度时也有一句 set --,它不是一条新命令。
		if !strings.HasPrefix(command, " $(wc -c") {
			commands = append(commands, command)
		}
	}
	return commands[min(skip, len(commands)):]
}

// 往发行版发的几种命令的脚本里各有一句别的脚本没有的,按它数命令。
const (
	wslListCommand      = `[ -f "$e/SKILL.md" ]`
	wslCanonicalCommand = `rest=${c##*/}`
	wslInspectCommand   = `printf 'irregular\0'`
)

func countWSLCommands(commands []string, script string) int {
	count := 0
	for _, command := range commands {
		if strings.Contains(command, script) {
			count++
		}
	}
	return count
}

// 几个 Agent 共用 .agents/skills(其中一个经符号链接指过去的别的写法),登记了几个项目:列未纳管技能、同名技能时,
// 规范化后相同的目录只列一次,规范化一次批量做完;每个作用域里的路径仍是它自己的写法。
func TestWSLDistroListsSharedDirectoryOnce(t *testing.T) {
	lib, home := openWSLLibrary(t)
	ctx := t.Context()
	log := recordWSLCommands(t)
	sharing := []library.AgentCode{library.AgentCodex}
	directories := map[library.AgentCode]string{library.AgentCodex: ".agents/skills"}
	for i := range 3 {
		agent, err := lib.AddAgent(ctx, library.AgentInput{Name: fmt.Sprintf("Shared %d", i), GlobalDirectories: []string{"~/.agents/skills"}, ProjectDirectories: []string{".agents/skills"}})
		if err != nil {
			t.Fatal(err)
		}
		sharing = append(sharing, agent.Code)
		directories[agent.Code] = ".agents/skills"
	}
	alias, err := lib.AddAgent(ctx, library.AgentInput{Name: "Alias", GlobalDirectories: []string{"~/.alias/skills"}, ProjectDirectories: []string{".alias/skills"}})
	if err != nil {
		t.Fatal(err)
	}
	sharing = append(sharing, alias.Code)
	directories[alias.Code] = ".alias/skills"
	layout := func(root string) {
		writeFile(t, filepath.Join(root, ".agents/skills/dup/SKILL.md"), []byte(skillDocument("dup", "v1")), 0o644)
		if err := os.Symlink(".agents", filepath.Join(root, ".alias")); err != nil {
			t.Fatal(err)
		}
	}
	layout(home)
	var projects []library.RegisteredProject
	for range 5 {
		root, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		layout(root)
		project, err := lib.RegisterProject(ctx, wslPath(t, root))
		if err != nil {
			t.Fatal(err)
		}
		projects = append(projects, project)
	}
	scopeIn := func(agent library.AgentCode, project library.ProjectID) library.Scope {
		if project == 0 {
			return library.GlobalScope(agent)
		}
		return library.ProjectScope(agent, project)
	}
	folderIn := func(agent library.AgentCode, project *library.RegisteredProject) string {
		root := home
		if project != nil {
			root = strings.TrimPrefix(strings.ReplaceAll(project.Path, `\`, "/"), "//wsl.localhost/"+t.Name())
		}
		return wslPath(t, filepath.Join(root, directories[agent], "dup"))
	}
	// 共用的 .agents/skills 在主目录和每个项目里各一个,Claude Code 的 .claude/skills 也各一个。
	distinct := 2 * (1 + len(projects))

	var unmanaged []library.UnmanagedSkill
	commands := wslCommandsDuring(t, log, func() {
		if unmanaged, err = lib.ListUnmanagedSkills(ctx); err != nil {
			t.Fatal(err)
		}
	})
	if got := countWSLCommands(commands, wslListCommand); got != distinct {
		t.Errorf("ListUnmanagedSkills listed %d directories, want %d", got, distinct)
	}
	// 只给列出的目录规范化一次:没有是符号链接的技能文件夹,不用判断链接是否指向 Agent 目录外面。
	if got := countWSLCommands(commands, wslCanonicalCommand); got != 1 {
		t.Errorf("ListUnmanagedSkills ran %d canonical commands, want 1", got)
	}
	var want []string
	for _, agent := range sharing {
		want = append(want, folderIn(agent, nil))
		for i := range projects {
			want = append(want, folderIn(agent, &projects[i]))
		}
	}
	var got []string
	for _, skill := range unmanaged {
		got = append(got, skill.Folder)
		if skill.Scope.Agent == alias.Code && !slices.Contains(skill.SameFolder, scopeIn(library.AgentCodex, skill.Scope.ProjectID)) {
			t.Errorf("%s: SameFolder = %v, want it to include Codex", skill.Folder, skill.SameFolder)
		}
		if skill.Scope.Agent == library.AgentCodex && !slices.Equal(skill.SameFolder, []library.Scope{scopeIn(alias.Code, skill.Scope.ProjectID)}) {
			t.Errorf("%s: SameFolder = %v, want only the alias", skill.Folder, skill.SameFolder)
		}
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("unmanaged folders = %q\nwant %q", got, want)
	}

	var same []library.SameNameSkill
	commands = wslCommandsDuring(t, log, func() {
		if same, err = lib.ListSameNameSkills(ctx); err != nil {
			t.Fatal(err)
		}
	})
	if got := countWSLCommands(commands, wslListCommand); got != distinct {
		t.Errorf("ListSameNameSkills listed %d directories, want %d", got, distinct)
	}
	if got := countWSLCommands(commands, wslCanonicalCommand); got != 1 {
		t.Errorf("ListSameNameSkills ran %d canonical commands, want 1", got)
	}
	if len(same) != len(sharing)*len(projects) {
		t.Fatalf("same-name skills = %+v", same)
	}
	for _, skill := range same {
		project := projects[slices.IndexFunc(projects, func(p library.RegisteredProject) bool { return p.ID == skill.Second.Scope.ProjectID })]
		agent := skill.Second.Scope.Agent
		if skill.Kind != library.SameNameGlobalAndProject || skill.First.Folder != folderIn(agent, nil) || skill.Second.Folder != folderIn(agent, &project) {
			t.Errorf("same-name skill = %+v", skill)
		}
	}
}

// 技能文件夹是符号链接时,判断它是否指向 Agent 目录外面要用到全部收录 Agent 在全局和每个已登记项目里的目录:
// 项目多时这些目录有几千个,在发行版里一条命令规范化完,不为每条路径启动进程。
func TestWSLDistroChecksLinkTargetsInOneCommand(t *testing.T) {
	lib, home := openWSLLibrary(t)
	ctx := t.Context()
	log := recordWSLCommands(t)
	writeFile(t, filepath.Join(home, ".agents/skills/inside/SKILL.md"), []byte(skillDocument("inside", "v1")), 0o644)
	writeFile(t, filepath.Join(home, "code/outside/SKILL.md"), []byte(skillDocument("outside", "v1")), 0o644)
	for _, name := range []string{"inside", "outside"} {
		target := filepath.Join(home, ".agents/skills", name)
		if name == "outside" {
			target = filepath.Join(home, "code/outside")
		}
		if err := os.MkdirAll(filepath.Join(home, ".claude/skills"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(home, ".claude/skills", name)); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 25 {
		root := filepath.Join(home, "projects", fmt.Sprintf("p%02d", i))
		writeFile(t, filepath.Join(root, ".claude/skills", fmt.Sprintf("own-%02d", i), "SKILL.md"), []byte(skillDocument(fmt.Sprintf("own-%02d", i), "v1")), 0o644)
		if _, err := lib.RegisterProject(ctx, wslPath(t, root)); err != nil {
			t.Fatal(err)
		}
	}

	var unmanaged []library.UnmanagedSkill
	var err error
	commands := wslCommandsDuring(t, log, func() {
		if unmanaged, err = lib.ListUnmanagedSkills(ctx); err != nil {
			t.Fatal(err)
		}
	})
	outside := map[string]bool{}
	for _, skill := range unmanaged {
		if skill.Scope == library.GlobalScope(library.AgentClaudeCode) {
			outside[skill.Name] = skill.LinkOutside
		}
	}
	if !maps.Equal(outside, map[string]bool{"inside": false, "outside": true}) {
		t.Errorf("Claude Code global LinkOutside = %v", outside)
	}
	// 一次给列出的目录规范化,一次给判断链接是否指向 Agent 目录外面。
	if got := countWSLCommands(commands, wslCanonicalCommand); got != 2 {
		t.Errorf("ListUnmanagedSkills ran %d canonical commands, want 2", got)
	}
}

// 70 多个 Agent 时:检测一条命令查完全部检测目录;巡检时几个 Agent 共用的同一个文件夹只读一次,每一处都给出状态。
func TestWSLDistroDetectsAndInspectsManyAgentsInBatches(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	setTestHome(t, home)
	t.Setenv("HOME", home)
	log := recordWSLCommands(t)
	path := filepath.Join(t.TempDir(), "library.db")
	if err := openTestLibraryAt(t, path).Close(); err != nil {
		t.Fatal(err)
	}
	const pool, sharing = 72, 10
	detected := map[library.AgentCode]bool{library.AgentClaudeCode: false, library.AgentCodex: true}
	writeFile(t, filepath.Join(home, ".codex/config.toml"), nil, 0o644)
	for i := range pool {
		code, detect := fmt.Sprintf("pool-%02d", i), fmt.Sprintf(".pool-%02d", i)
		directory := detect + "/skills"
		if i < sharing {
			directory = ".agents/skills"
		}
		editLibraryFixture(t, path, `INSERT INTO agents(code, name, global_paths, project_paths, detect_paths, enabled) VALUES (?, ?, json_array(?), json_array(?), json_array(?), 1)`, code, code, directory, directory, detect)
		switch {
		case i == 4:
			if err := os.Symlink(".codex", filepath.Join(home, detect)); err != nil {
				t.Fatal(err)
			}
			detected[library.AgentCode(code)] = true
		case i%3 == 0:
			if err := os.MkdirAll(filepath.Join(home, detect), 0o755); err != nil {
				t.Fatal(err)
			}
			detected[library.AgentCode(code)] = true
		case i%3 == 1:
			writeFile(t, filepath.Join(home, detect), nil, 0o644)
			detected[library.AgentCode(code)] = false
		default:
			detected[library.AgentCode(code)] = false
		}
	}
	lib := openTestLibraryAt(t, path)
	ctx := t.Context()
	if err := lib.SetWSLDistro(ctx, t.Name()); err != nil {
		t.Fatal(err)
	}

	var agents []library.Agent
	commands := wslCommandsDuring(t, log, func() {
		if agents, err = lib.ListAgents(ctx); err != nil {
			t.Fatal(err)
		}
	})
	if len(commands) != 1 {
		t.Errorf("ListAgents ran %d commands, want 1", len(commands))
	}
	got := map[library.AgentCode]bool{}
	for _, agent := range agents {
		got[agent.Code] = agent.Detected
	}
	if !maps.Equal(got, detected) {
		t.Fatalf("detected = %v\nwant %v", got, detected)
	}

	// 再添加几个全局读 ~/.agents/skills 和自己目录的 Agent:每一处都要查自己目录里有没有指过来的旧链接,也是一条命令查完。
	addFromPool(t, lib, "cursor", "gemini-cli", "github-copilot")
	const twoDirectories = 3
	skill := fetchNamedSkill(t, lib, "shared")
	if err := lib.Install(ctx, skill.ID, library.GlobalScope(library.AgentCodex), library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	var inspections []library.SkillScopeInspection
	commands = wslCommandsDuring(t, log, func() {
		if inspections, err = lib.InspectSkillScopes(ctx, library.SkillScopeFilter{}); err != nil {
			t.Fatal(err)
		}
	})
	if len(inspections) != 1+sharing+twoDirectories {
		t.Fatalf("inspections = %+v", inspections)
	}
	if stats := countWSLCommands(commands, wslInspectCommand); stats > 3 {
		t.Errorf("InspectSkillScopes ran %d folder commands for %d places, want them batched", stats, len(inspections))
	}
	for _, inspection := range inspections {
		if inspection.Status != library.SkillScopeCurrent || inspection.Folder != wslPath(t, filepath.Join(home, ".agents/skills/shared")) {
			t.Errorf("%s %s: %s", inspection.Scope.Agent, inspection.Folder, inspection.Status)
		}
	}
	folder := "'" + filepath.Join(home, ".agents/skills/shared") + "'"
	reads := 0
	for _, command := range commands {
		if strings.Contains(command, wslInspectCommand) && !strings.HasPrefix(command, " '-'") {
			reads += strings.Count(command, folder)
		}
	}
	if reads != 1 {
		t.Errorf("InspectSkillScopes read the shared folder %d times, want 1", reads)
	}

	commands = wslCommandsDuring(t, log, func() {
		if _, err := lib.ListSameNameSkills(ctx); err != nil {
			t.Fatal(err)
		}
	})
	if got := countWSLCommands(commands, wslCanonicalCommand); got != 1 {
		t.Errorf("ListSameNameSkills ran %d canonical commands, want 1", got)
	}
}

func TestWSLDistroUnavailable(t *testing.T) {
	lib := openTestLibrary(t)
	platformtest.EmulateWSL(t, "/nonexistent/sh")
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

// 发行版主目录下的技能文件夹和本机主目录下的一样，来源就是这个文件夹本身，不把整个主目录当来源。
func TestWSLDistroSkillFolderUnderHomeIsItsOwnSource(t *testing.T) {
	lib, home := openWSLLibrary(t)
	writeFile(t, filepath.Join(home, "mine", "SKILL.md"), []byte(skillDocument("mine", "")), 0o644)
	writeFile(t, filepath.Join(home, ".claude/skills/own/SKILL.md"), []byte(skillDocument("own", "")), 0o644)
	folder := wslPath(t, filepath.Join(home, "mine"))
	preview, err := lib.PreviewFetch(t.Context(), localRequest(folder))
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Candidates) != 1 || preview.Candidates[0].Path != "." || preview.Source.Label != folder {
		t.Fatalf("preview = %#v; want the folder itself as the source", preview)
	}
}

// 存着 WSL 发行版、全局目录却暂时留在 Windows 本机时(启动时发行版打不开，或还没换过去),只列出不自动纳入:
// 全局安装不分主目录，这时记下的安装切回发行版后都会显示成缺失。切回发行版后照常纳入。
func TestUnmanagedCopiesAreNotAdoptedWhileTheDistroIsUnavailable(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	setTestHome(t, home)
	t.Setenv("HOME", home)
	platformtest.EmulateWSL(t, "sh")
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "library.db")
	before := openTestLibraryAt(t, path)
	if err := before.SetWSLDistro(ctx, t.Name()); err != nil {
		t.Fatal(err)
	}
	skill := fetchNamedSkill(t, before, "pdf")
	codex := library.GlobalScope(library.AgentCodex)
	if err := before.Install(ctx, skill.ID, codex, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := before.Close(); err != nil {
		t.Fatal(err)
	}
	claude := library.GlobalScope(library.AgentClaudeCode)
	if err := os.CopyFS(filepath.Join(home, ".claude", "skills", "pdf"), os.DirFS(filepath.Join(home, ".agents", "skills", "pdf"))); err != nil {
		t.Fatal(err)
	}

	lib := openTestLibraryAt(t, path)
	if distro, err := lib.WSLDistro(ctx); err != nil || distro != t.Name() {
		t.Fatalf("WSLDistro = %q, %v, want the stored distro", distro, err)
	}
	if here := unmanagedIn(t, lib, claude, "pdf"); here.SkillID != skill.ID {
		t.Fatalf("unmanaged = %#v, want it recognized as pdf", here)
	}
	if plan := planFetchUnmanaged(t, lib, claude); len(plan) != 1 || plan[0].SkillID != skill.ID {
		t.Fatalf("plan = %#v, want the copy listed, not adopted", plan)
	}
	if inspections, err := lib.InspectSkillScopes(ctx, library.SkillScopeFilter{Scope: &claude}); err != nil || len(inspections) != 0 {
		t.Fatalf("InspectSkillScopes(claude) = %#v, %v, want nothing recorded", inspections, err)
	}

	if err := lib.SetWSLDistro(ctx, t.Name()); err != nil {
		t.Fatal(err)
	}
	requireUnmanagedSkills(t, lib, nil)
	requireScopeStatus(t, lib, skill.ID, claude, library.SkillScopeCurrent)
}

// 手填添加 Agent 时检测出错(如发行版里的命令一时失败):什么都不添加,用户照原样重试不会加出两个。以前先写进数据库再检测。
func TestAddAgentAddsNothingWhenDetectionFails(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	setTestHome(t, home)
	t.Setenv("HOME", home)
	// 充当发行版的 sh 外面套一层:命令里出现 BREAK-DETECTION 时这个会话就断掉,检测那里的目录就会出错。
	shell := filepath.Join(t.TempDir(), "flaky-sh")
	writeFile(t, shell, []byte("#!/bin/sh\nwhile IFS= read -r line; do case \"$line\" in *BREAK-DETECTION*) exit 1;; esac; printf '%s\\n' \"$line\"; done | sh \"$@\"\n"), 0o755)
	platformtest.EmulateWSL(t, shell)
	lib := openTestLibrary(t)
	if err := lib.SetWSLDistro(t.Context(), t.Name()); err != nil {
		t.Fatal(err)
	}
	before := len(listAgents(t, lib))
	_, err = lib.AddAgent(t.Context(), library.AgentInput{
		Name: "Mine", GlobalDirectories: []string{wslPath(t, filepath.Join(home, ".mine/skills"))}, ProjectDirectories: []string{".mine/skills"},
		DetectDirectories: []string{wslPath(t, filepath.Join(home, "BREAK-DETECTION"))},
	})
	if err == nil {
		t.Fatal("AddAgent() with a failing detection = nil error; want the failure reported")
	}
	if after := len(listAgents(t, lib)); after != before {
		t.Fatalf("ListAgents() after a failed AddAgent has %d agents, want %d", after, before)
	}
}
