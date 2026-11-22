package library_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tiaot33/mechbox/internal/library"
)

// globalAndProject 是 Agent 的全局和一个项目里同名的一对文件夹;技能目录由 Folder 已能看出,requireSameNameSkills 不比它。
func globalAndProject(agent library.AgentCode, project library.ProjectID, name string, global, local library.SameNameFolder) library.SameNameSkill {
	global.Scope, local.Scope = library.GlobalScope(agent), library.ProjectScope(agent, project)
	return library.SameNameSkill{Kind: library.SameNameGlobalAndProject, Name: name, First: global, Second: local}
}

// requireSameNameSkills 断言同名结果,不比技能目录(由 Folder 已能看出,多技能目录的用例专门核对)。
func requireSameNameSkills(t *testing.T, lib *library.Library, want []library.SameNameSkill) {
	t.Helper()
	got, err := lib.ListSameNameSkills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for i := range got {
		got[i].First.Directory, got[i].Second.Directory = "", ""
	}
	if want == nil {
		want = []library.SameNameSkill{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListSameNameSkills() = %#v, want %#v", got, want)
	}
}

func TestListSameNameSkillsMarksGlobalAndProjectFoldersWhetherOrNotTheLibraryInstalledThem(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	home := t.TempDir()
	setTestHome(t, home)
	lib := openTestLibrary(t)
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	other, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	claudeGlobal := library.GlobalScope(library.AgentClaudeCode)
	claudeProject := library.ProjectScope(library.AgentClaudeCode, project.ID)
	codexGlobal := library.GlobalScope(library.AgentCodex)

	// 库把 pdf 装进了项目,全局还有一个手动拷的 pdf。
	pdf := fetchNamedSkill(t, lib, "pdf")
	if err := lib.Install(ctx, pdf.ID, claudeProject, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	copied := writeUnmanagedSkill(t, scopeDirectory(t, lib, claudeGlobal), "pdf", "hand copied")
	// 反过来:库装在全局,项目里有一个提交在仓库或自己写的同名文件夹。
	docx := fetchNamedSkill(t, lib, "docx")
	if err := lib.Install(ctx, docx.ID, claudeGlobal, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	ownDocx := writeUnmanagedSkill(t, scopeDirectory(t, lib, claudeProject), "docx", "my docx")
	// 两处都是库装的同一个技能,也标出来。
	xlsx := fetchNamedSkill(t, lib, "xlsx")
	for _, scope := range []library.Scope{claudeGlobal, claudeProject} {
		if err := lib.Install(ctx, xlsx.ID, scope, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	// Codex 的全局和两个项目里都有 cli-tool,每个项目各一条;只在一处的不算同名。
	cli := writeUnmanagedSkill(t, scopeDirectory(t, lib, codexGlobal), "cli-tool", "from npx skills")
	cliHere := writeUnmanagedSkill(t, scopeDirectory(t, lib, library.ProjectScope(library.AgentCodex, project.ID)), "cli-tool", "vendored")
	cliThere := writeUnmanagedSkill(t, scopeDirectory(t, lib, library.ProjectScope(library.AgentCodex, other.ID)), "cli-tool", "vendored")
	writeUnmanagedSkill(t, scopeDirectory(t, lib, codexGlobal), "only-global", "")
	writeUnmanagedSkill(t, scopeDirectory(t, lib, library.ProjectScope(library.AgentCodex, project.ID)), "only-project", "")
	// 不同 Agent 之间同名不算:Claude Code 全局的 pdf 和 Codex 项目里的 pdf。
	writeUnmanagedSkill(t, scopeDirectory(t, lib, library.ProjectScope(library.AgentCodex, project.ID)), "pdf", "codex pdf")

	folder := func(scope library.Scope, name string) string {
		return filepath.Join(scopeDirectory(t, lib, scope), name)
	}
	before := snapshotTree(t, home)
	requireSameNameSkills(t, lib, []library.SameNameSkill{
		globalAndProject(library.AgentClaudeCode, project.ID, "docx", library.SameNameFolder{Folder: folder(claudeGlobal, "docx"), SkillID: docx.ID}, library.SameNameFolder{Folder: ownDocx}),
		globalAndProject(library.AgentClaudeCode, project.ID, "pdf", library.SameNameFolder{Folder: copied}, library.SameNameFolder{Folder: folder(claudeProject, "pdf"), SkillID: pdf.ID}),
		globalAndProject(library.AgentClaudeCode, project.ID, "xlsx", library.SameNameFolder{Folder: folder(claudeGlobal, "xlsx"), SkillID: xlsx.ID}, library.SameNameFolder{Folder: folder(claudeProject, "xlsx"), SkillID: xlsx.ID}),
		globalAndProject(library.AgentCodex, project.ID, "cli-tool", library.SameNameFolder{Folder: cli}, library.SameNameFolder{Folder: cliHere}),
		globalAndProject(library.AgentCodex, other.ID, "cli-tool", library.SameNameFolder{Folder: cli}, library.SameNameFolder{Folder: cliThere}),
	})
	if after := snapshotTree(t, home); !reflect.DeepEqual(after, before) {
		t.Fatalf("ListSameNameSkills changed disk: before %v, after %v", before, after)
	}

	// 安装记录还在、文件夹不在了的,Agent 看不到,不算同名。
	if err := os.RemoveAll(folder(claudeProject, "xlsx")); err != nil {
		t.Fatal(err)
	}
	// 项目不在这台机器上时那里没有结果。
	if err := os.RemoveAll(other.Path); err != nil {
		t.Fatal(err)
	}
	got, err := lib.ListSameNameSkills(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, skill := range got {
		names = append(names, string(skill.Second.Scope.Agent)+"/"+skill.Name)
	}
	if want := []string{"claude-code/docx", "claude-code/pdf", "codex/cli-tool"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("ListSameNameSkills() names = %v, want %v", names, want)
	}
}

func TestSameNameRuleComesWithTheAgent(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	home := t.TempDir()
	setTestHome(t, home)
	lib := openTestLibrary(t)
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// 和 Codex 共用目录的自定义 Agent 也看得到同名,但没有已知规则,只标出同名。
	custom, err := lib.AddAgent(ctx, library.AgentInput{Name: "Shared", GlobalDirectories: []string{"~/.agents/skills"}, ProjectDirectories: []string{".agents/skills"}})
	if err != nil {
		t.Fatal(err)
	}
	rules := map[library.AgentCode]library.SameNameRule{}
	for _, agent := range listAgents(t, lib) {
		rules[agent.Code] = agent.SameName
	}
	want := map[library.AgentCode]library.SameNameRule{
		library.AgentClaudeCode: library.SameNameGlobalWins,
		library.AgentCodex:      library.SameNameBothShown,
		custom.Code:             library.SameNameUnknown,
	}
	if !reflect.DeepEqual(rules, want) {
		t.Fatalf("Agent.SameName = %v, want %v", rules, want)
	}

	global := writeUnmanagedSkill(t, scopeDirectory(t, lib, library.GlobalScope(library.AgentCodex)), "pdf", "")
	local := writeUnmanagedSkill(t, scopeDirectory(t, lib, library.ProjectScope(library.AgentCodex, project.ID)), "pdf", "")
	pair := func(agent library.AgentCode) library.SameNameSkill {
		return globalAndProject(agent, project.ID, "pdf", library.SameNameFolder{Folder: global}, library.SameNameFolder{Folder: local})
	}
	requireSameNameSkills(t, lib, []library.SameNameSkill{pair(library.AgentCodex), pair(custom.Code)})
}

func TestListSameNameSkillsMatchesTheNameInSkillMD(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	setTestHome(t, t.TempDir())
	lib := openTestLibrary(t)
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	global := scopeDirectory(t, lib, library.GlobalScope(library.AgentCodex))
	local := scopeDirectory(t, lib, library.ProjectScope(library.AgentCodex, project.ID))
	write := func(directory, folder, name string) string {
		path := filepath.Join(directory, folder)
		writeFile(t, filepath.Join(path, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: d\n---\n"), 0o644)
		return path
	}
	// 文件夹名不同、SKILL.md 里的 name 相同:Agent 认作同名。全局里有两个 name 相同的文件夹时各一条。
	pdf := write(global, "pdf", "pdf")
	pdfCopy := write(global, "pdf-copy", "pdf")
	pdfTools := write(local, "pdf-tools", "pdf")
	// 文件夹名相同、name 不同:不同名。
	write(global, "notes", "notes")
	write(local, "notes", "team-notes")
	// frontmatter 读不出时按文件夹名。
	broken := filepath.Join(global, "broken")
	writeFile(t, filepath.Join(broken, "SKILL.md"), []byte("no frontmatter\n"), 0o644)
	brokenHere := filepath.Join(local, "broken")
	writeFile(t, filepath.Join(brokenHere, "SKILL.md"), []byte("no frontmatter\n"), 0o644)

	requireSameNameSkills(t, lib, []library.SameNameSkill{
		globalAndProject(library.AgentCodex, project.ID, "broken", library.SameNameFolder{Folder: broken}, library.SameNameFolder{Folder: brokenHere}),
		globalAndProject(library.AgentCodex, project.ID, "pdf", library.SameNameFolder{Folder: pdf}, library.SameNameFolder{Folder: pdfTools}),
		globalAndProject(library.AgentCodex, project.ID, "pdf", library.SameNameFolder{Folder: pdfCopy}, library.SameNameFolder{Folder: pdfTools}),
	})
}
