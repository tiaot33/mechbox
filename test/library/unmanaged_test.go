package library_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tiaot33/skills-tui/internal/library"
)

func listUnmanagedSkills(t *testing.T, lib *library.Library) []library.UnmanagedSkill {
	t.Helper()
	got, err := lib.ListUnmanagedSkills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func requireUnmanagedSkills(t *testing.T, lib *library.Library, want []library.UnmanagedSkill) {
	t.Helper()
	got := listUnmanagedSkills(t, lib)
	if want == nil {
		want = []library.UnmanagedSkill{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListUnmanagedSkills() = %#v, want %#v", got, want)
	}
}

// writeUnmanagedSkill 在 directory 下手写一个技能文件夹,模拟命令行工具或手动拷进 Agent 目录的技能。
func writeUnmanagedSkill(t *testing.T, directory, name, description string) string {
	t.Helper()
	folder := filepath.Join(directory, name)
	writeFile(t, filepath.Join(folder, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: "+description+"\n---\n# "+name+"\n"), 0o644)
	return folder
}

func scopeDirectory(t *testing.T, lib *library.Library, scope library.Scope) string {
	t.Helper()
	directory, err := lib.ScopeDirectory(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	return directory
}

func TestListUnmanagedSkillsListsFoldersTheLibraryDidNotInstall(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	claude := library.GlobalScope(library.AgentClaudeCode)
	codex := library.GlobalScope(library.AgentCodex)
	claudeDirectory := scopeDirectory(t, lib, claude)
	codexDirectory := scopeDirectory(t, lib, codex)

	installed := fetchNamedSkill(t, lib, "installed")
	if err := lib.Install(ctx, installed.ID, claude, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	shared := fetchNamedSkill(t, lib, "shared")
	if err := lib.Install(ctx, shared.ID, codex, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	// 和 Codex 共用全局目录的自定义 Agent:Codex 装的 shared 对它也是库装的。
	custom, err := lib.AddAgent(ctx, library.CustomAgent{Name: "Shared", GlobalDirectory: "~/.agents/skills", ProjectDirectory: ".shared/skills"})
	if err != nil {
		t.Fatal(err)
	}

	notes := writeUnmanagedSkill(t, claudeDirectory, "notes", "my notes")
	cli := writeUnmanagedSkill(t, codexDirectory, "cli-tool", "from npx skills")
	// 不算技能文件夹的:没有 SKILL.md 的文件夹、普通文件、以点开头的文件夹(Codex 的 .system)。
	writeFile(t, filepath.Join(claudeDirectory, "scratch", "README.md"), []byte("not a skill\n"), 0o644)
	writeFile(t, filepath.Join(claudeDirectory, "README.md"), []byte("not a skill\n"), 0o644)
	writeUnmanagedSkill(t, filepath.Join(codexDirectory, ".system"), "bundled", "system skill")
	writeUnmanagedSkill(t, codexDirectory, ".hidden", "hidden")
	// SKILL.md 读不出 frontmatter 的照样列出,只是没有描述。
	broken := filepath.Join(claudeDirectory, "broken")
	writeFile(t, filepath.Join(broken, "SKILL.md"), []byte("no frontmatter\n"), 0o644)

	before := snapshotTree(t, home)
	requireUnmanagedSkills(t, lib, []library.UnmanagedSkill{
		{Scope: claude, Name: "broken", Folder: broken},
		{Scope: claude, Name: "notes", Description: "my notes", Folder: notes},
		{Scope: codex, Name: "cli-tool", Description: "from npx skills", Folder: cli},
		{Scope: library.GlobalScope(custom.Code), Name: "cli-tool", Description: "from npx skills", Folder: cli},
	})
	if after := snapshotTree(t, home); !reflect.DeepEqual(after, before) {
		t.Fatalf("ListUnmanagedSkills changed disk: before %v, after %v", before, after)
	}
}

func TestListUnmanagedSkillsListsSymlinkedFolders(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	claude := library.GlobalScope(library.AgentClaudeCode)
	codex := library.GlobalScope(library.AgentCodex)
	claudeDirectory := scopeDirectory(t, lib, claude)
	codexDirectory := scopeDirectory(t, lib, codex)

	// npx skills 的默认做法:真实文件夹在 ~/.agents/skills/pdf,其他 Agent 的目录里用符号链接指过去。
	real := writeUnmanagedSkill(t, codexDirectory, "pdf", "read pdfs")
	if err := os.MkdirAll(claudeDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(claudeDirectory, "pdf")
	if err := os.Symlink(filepath.Join("..", "..", ".agents", "skills", "pdf"), link); err != nil {
		t.Fatal(err)
	}
	// 指向不存在之处、指向普通文件的符号链接不是技能文件夹。
	if err := os.Symlink(filepath.Join(home, "gone"), filepath.Join(claudeDirectory, "dangling")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(real, "SKILL.md"), filepath.Join(claudeDirectory, "file-link")); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}

	requireUnmanagedSkills(t, lib, []library.UnmanagedSkill{
		{Scope: claude, Name: "pdf", Description: "read pdfs", Folder: link, LinkTarget: resolved},
		{Scope: codex, Name: "pdf", Description: "read pdfs", Folder: real},
	})
}

func TestListUnmanagedSkillsMarksFoldersCommittedInTheProjectRepository(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	repository := t.TempDir()
	runGit(t, repository, "init", "-b", "main")
	writeFile(t, filepath.Join(repository, "README.md"), []byte("project\n"), 0o644)
	project, err := lib.RegisterProject(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	scope := library.ProjectScope(library.AgentClaudeCode, project.ID)
	directory := scopeDirectory(t, lib, scope)

	team := writeUnmanagedSkill(t, directory, "team", "shared by the team")
	if err := os.MkdirAll(filepath.Join(repository, "vendor", "linked"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeUnmanagedSkill(t, filepath.Join(repository, "vendor"), "linked", "committed as a symlink")
	if err := os.Symlink(filepath.Join("..", "..", "vendor", "linked"), filepath.Join(directory, "linked")); err != nil {
		t.Fatal(err)
	}
	commitGit(t, repository, "team skills")
	// 只在工作区、没提交的不算在项目仓库里;库装的不列出。
	mine := writeUnmanagedSkill(t, directory, "mine", "not committed")
	installed := fetchNamedSkill(t, lib, "installed")
	if err := lib.Install(ctx, installed.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(repository, "vendor", "linked"))
	if err != nil {
		t.Fatal(err)
	}

	requireUnmanagedSkills(t, lib, []library.UnmanagedSkill{
		{Scope: scope, Name: "linked", Description: "committed as a symlink", Folder: filepath.Join(directory, "linked"), LinkTarget: resolved, LinkOutside: true, InRepository: true},
		{Scope: scope, Name: "mine", Description: "not committed", Folder: mine},
		{Scope: scope, Name: "team", Description: "shared by the team", Folder: team, InRepository: true},
	})
}

func TestListUnmanagedSkillsInProjectThatIsNotARepository(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	root := t.TempDir()
	project, err := lib.RegisterProject(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	scope := library.ProjectScope(library.AgentCodex, project.ID)
	folder := writeUnmanagedSkill(t, scopeDirectory(t, lib, scope), "plain", "plain folder")
	requireUnmanagedSkills(t, lib, []library.UnmanagedSkill{{Scope: scope, Name: "plain", Description: "plain folder", Folder: folder}})

	// 项目不在这台机器上时那个作用域没有结果,也不报错。
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	requireUnmanagedSkills(t, lib, nil)
}

func TestInspectSkillScopesFollowsSymlinkedInstalledFolder(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	scope := library.GlobalScope(library.AgentClaudeCode)
	skill := fetchExampleSkill(t, lib, "pdf")
	if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	// 装出去的文件夹换成指向一份相同内容的符号链接(如 npx skills 的布局):仍是当前版本,也不算未纳管技能。
	folder := filepath.Join(scopeDirectory(t, lib, scope), skill.Name)
	real := filepath.Join(home, ".agents", "skills", skill.Name)
	if err := os.MkdirAll(filepath.Dir(real), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(folder, real); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, folder); err != nil {
		t.Fatal(err)
	}
	requireInspection(t, lib, library.SkillScopeCurrent, skill.VersionID)
	requireUnmanagedSkills(t, lib, []library.UnmanagedSkill{
		{Scope: library.GlobalScope(library.AgentCodex), Name: skill.Name, Description: "", Folder: real},
	})

	// 链接指向的内容被改过才是 modified;指向不存在之处也算改过。
	writeFile(t, filepath.Join(real, "SKILL.md"), []byte("# edited\n"), 0o644)
	requireInspection(t, lib, library.SkillScopeModified, 0)
	if err := os.RemoveAll(real); err != nil {
		t.Fatal(err)
	}
	requireInspection(t, lib, library.SkillScopeModified, 0)

	// 链接指向自己(循环)时也只是 modified,巡检照常返回,可以确认覆盖后重新安装。
	if err := os.Remove(folder); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(folder, folder); err != nil {
		t.Fatal(err)
	}
	requireInspection(t, lib, library.SkillScopeModified, 0)
	requireReinstallResults(t, reinstall(t, lib, library.ReinstallRequest{SkillID: skill.ID, Scope: scope, Overwrite: true}), []library.ReinstallResult{reinstalledResult(skill.ID, scope)})
	requireInspection(t, lib, library.SkillScopeCurrent, skill.VersionID)
}

func TestListUnmanagedSkillsDoesNotFollowSymlinkedAgentDirectories(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	claude := library.GlobalScope(library.AgentClaudeCode)
	codex := library.GlobalScope(library.AgentCodex)
	codexDirectory := scopeDirectory(t, lib, codex)
	if err := os.MkdirAll(codexDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	// Claude Code 的全局目录整个是指向 Codex 全局目录的符号链接:共用只按目录设置判断,不跟随符号链接,
	// 所以只装到 Codex 的技能在 Claude Code 里没有安装记录,是未纳管技能。
	claudeDirectory := scopeDirectory(t, lib, claude)
	if err := os.MkdirAll(filepath.Dir(claudeDirectory), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(codexDirectory, claudeDirectory); err != nil {
		t.Fatal(err)
	}
	skill := fetchNamedSkill(t, lib, "pdf")
	if err := lib.Install(ctx, skill.ID, codex, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	requireUnmanagedSkills(t, lib, []library.UnmanagedSkill{
		{Scope: claude, Name: "pdf", Folder: filepath.Join(claudeDirectory, "pdf")},
	})
}

func TestListUnmanagedSkillsReadsSourceRecordsOfNpxSkills(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	t.Setenv("HOME", home)
	recordsPath := globalRecordsPath(t)
	lib := openTestLibrary(t)
	claude := library.GlobalScope(library.AgentClaudeCode)
	claudeDirectory := scopeDirectory(t, lib, claude)
	writeUnmanagedSkill(t, claudeDirectory, "pdf", "read pdfs")
	writeUnmanagedSkill(t, claudeDirectory, "site", "from a site")
	writeUnmanagedSkill(t, claudeDirectory, "packaged", "from npm")
	writeUnmanagedSkill(t, claudeDirectory, "mine", "hand written")
	// 文件夹名和技能名不同:记录按 SKILL.md 里的 name 对上。
	writeFile(t, filepath.Join(claudeDirectory, "Lab-Notes", "SKILL.md"), []byte("---\nname: lab-notes\ndescription: notes\n---\n"), 0o644)
	writeSourceRecords(t, recordsPath, 3, map[string]map[string]string{
		"pdf":       {"source": "owner/repo", "sourceType": "github", "sourceUrl": "https://github.com/owner/repo.git", "ref": "main", "skillPath": "skills/pdf/SKILL.md"},
		"site":      {"source": "example.com", "sourceType": "well-known", "sourceUrl": "https://example.com/docs"},
		"packaged":  {"source": "some-package", "sourceType": "node_modules"},
		"lab-notes": {"source": "group/notes", "sourceType": "gitlab", "sourceUrl": "https://gitlab.com/group/notes.git"},
	})

	project := t.TempDir()
	registered, err := lib.RegisterProject(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	projectScope := library.ProjectScope(library.AgentClaudeCode, registered.ID)
	projectDirectory := scopeDirectory(t, lib, projectScope)
	writeUnmanagedSkill(t, projectDirectory, "lint", "lint rules")
	writeSourceRecords(t, filepath.Join(project, "skills-lock.json"), 1, map[string]map[string]string{
		"lint": {"source": "./vendor/skills", "sourceType": "local", "computedHash": "abc"},
	})

	sources := map[string][2]string{}
	for _, skill := range listUnmanagedSkills(t, lib) {
		sources[skill.Folder] = [2]string{skill.SourceInput, skill.SourceRef}
	}
	want := map[string][2]string{
		filepath.Join(claudeDirectory, "Lab-Notes"): {"https://gitlab.com/group/notes.git", ""},
		filepath.Join(claudeDirectory, "mine"):      {"", ""},
		filepath.Join(claudeDirectory, "packaged"):  {"", ""},
		filepath.Join(claudeDirectory, "pdf"):       {"owner/repo", "main"},
		filepath.Join(claudeDirectory, "site"):      {"https://example.com/docs", ""},
		filepath.Join(projectDirectory, "lint"):     {filepath.Join(registered.Path, "vendor", "skills"), ""},
	}
	if !reflect.DeepEqual(sources, want) {
		t.Fatalf("sources = %v, want %v", sources, want)
	}

	// 旧版本的全局来源记录 npx skills 自己也不认,这里同样不认。
	writeSourceRecords(t, recordsPath, 2, map[string]map[string]string{"pdf": {"source": "owner/repo", "sourceType": "github"}})
	for _, skill := range listUnmanagedSkills(t, lib) {
		if skill.Name == "pdf" && skill.SourceInput != "" {
			t.Fatalf("pdf = %#v, want no source from version 2 records", skill)
		}
	}
}
