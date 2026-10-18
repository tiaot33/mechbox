package library_test

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/tiaot33/mechbox/internal/library"
)

// 接管以后,符号链接的那一处在库第一次写它时换成独立的文件夹

type linkedFixture struct {
	lib          *library.Library
	server       *archiveServer
	skill        library.Skill
	home         string
	claude       library.Scope
	codex        library.Scope
	claudeFolder string
	codexFolder  string
}

// newLinkedFixture 从压缩包入库 tdd,装到 Codex 全局与 Claude Code 全局,再把 Claude Code 那一处换成 npx skills 的布局:
// 指向 ~/.agents/skills/tdd 的相对符号链接。
func newLinkedFixture(t *testing.T) linkedFixture {
	t.Helper()
	ctx := t.Context()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	server := newArchiveServer(t, zipArchive(t, map[string]string{"tdd/SKILL.md": skillDocument("tdd", "v1")}), "")
	skill := fetchArchive(t, lib, server.request())[0]
	f := linkedFixture{lib: lib, server: server, skill: skill, home: home, claude: library.GlobalScope(library.AgentClaudeCode), codex: library.GlobalScope(library.AgentCodex)}
	for _, scope := range []library.Scope{f.codex, f.claude} {
		if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	f.claudeFolder = filepath.Join(scopeDirectory(t, lib, f.claude), "tdd")
	f.codexFolder = filepath.Join(scopeDirectory(t, lib, f.codex), "tdd")
	if f.codexFolder != filepath.Join(home, ".agents", "skills", "tdd") {
		t.Fatalf("codex folder = %q; want ~/.agents/skills/tdd", f.codexFolder)
	}
	f.link(t)
	return f
}

func (f linkedFixture) link(t *testing.T) {
	t.Helper()
	if err := os.RemoveAll(f.claudeFolder); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "..", ".agents", "skills", "tdd"), f.claudeFolder); err != nil {
		t.Fatal(err)
	}
}

func (f linkedFixture) inspections(t *testing.T) map[library.Scope]library.SkillScopeInspection {
	t.Helper()
	inspections, err := f.lib.InspectSkillScopes(t.Context(), library.SkillScopeFilter{SkillID: f.skill.ID})
	if err != nil {
		t.Fatal(err)
	}
	byScope := map[library.Scope]library.SkillScopeInspection{}
	for _, inspection := range inspections {
		byScope[inspection.Scope] = inspection
	}
	return byScope
}

func requireLinkInfo(t *testing.T, inspections map[library.Scope]library.SkillScopeInspection, f linkedFixture) {
	t.Helper()
	claude, codex := inspections[f.claude], inspections[f.codex]
	if claude.LinkTarget != f.codexFolder || len(claude.LinkedBy) != 0 {
		t.Fatalf("claude inspection = %#v; want a link to %s", claude, f.codexFolder)
	}
	if codex.LinkTarget != "" || !slices.Contains(codex.LinkedBy, f.claude) {
		t.Fatalf("codex inspection = %#v; want it linked by claude", codex)
	}
}

func requireOwnFolder(t *testing.T, folder, body string) {
	t.Helper()
	info, err := os.Lstat(folder)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Fatalf("%s mode = %v; want a folder of its own", folder, info.Mode())
	}
	requireInstalledFolder(t, filepath.Dir(folder), filepath.Base(folder), body)
}

func TestInspectSkillScopesTellsWhereALinkedFolderPoints(t *testing.T) {
	f := newLinkedFixture(t)
	inspections := f.inspections(t)
	requireLinkInfo(t, inspections, f)
	if inspections[f.claude].Status != library.SkillScopeCurrent {
		t.Fatalf("claude status = %q; want the linked content compared", inspections[f.claude].Status)
	}
	filtered, err := f.lib.InspectSkillScopes(t.Context(), library.SkillScopeFilter{Scope: &f.codex})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || !slices.Contains(filtered[0].LinkedBy, f.claude) {
		t.Fatalf("InspectSkillScopes(codex) = %#v; want the link from outside the filter named", filtered)
	}

	dangling := filepath.Join(f.home, "gone")
	if err := os.Remove(f.claudeFolder); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dangling, f.claudeFolder); err != nil {
		t.Fatal(err)
	}
	if claude := f.inspections(t)[f.claude]; claude.LinkTarget != dangling || claude.Status != library.SkillScopeModified {
		t.Fatalf("dangling claude inspection = %#v; want the link target and modified", claude)
	}
}

func TestUpdateWritesALinkedPlaceAsAFolderOfItsOwn(t *testing.T) {
	ctx := t.Context()
	f := newLinkedFixture(t)
	f.server.serve(zipArchive(t, map[string]string{"tdd/SKILL.md": skillDocument("tdd", "v2")}), "")
	plan, err := f.lib.PlanUpdate(ctx, f.skill.CollectionID)
	if err != nil {
		t.Fatal(err)
	}
	planned := map[library.Scope]library.SkillScopeInspection{}
	for _, install := range plan.Installs {
		planned[install.Scope] = install
	}
	requireLinkInfo(t, planned, f)

	// 先换真实文件夹:之后链接那一处读到的已是新版本,仍然要换成独立的一份。
	result, err := f.lib.Update(ctx, library.UpdateRequest{Plan: plan.ID, Skills: []library.SkillID{f.skill.ID}, Installs: []library.ReinstallRequest{{SkillID: f.skill.ID, Scope: f.codex}, {SkillID: f.skill.ID, Scope: f.claude}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, install := range result.Installs {
		if install.Outcome != library.Reinstalled {
			t.Fatalf("update installs = %#v; want both places written", result.Installs)
		}
	}
	requireOwnFolder(t, f.claudeFolder, "v2")
	requireOwnFolder(t, f.codexFolder, "v2")

	// 用户又把 Claude Code 那一处链回去;回滚时它照样换成独立的一份,装回旧版。
	f.link(t)
	rollback, err := f.lib.Rollback(ctx, result.Record)
	if err != nil {
		t.Fatal(err)
	}
	for _, install := range rollback.Installs {
		if install.Outcome != library.RollbackReinstalled {
			t.Fatalf("rollback installs = %#v; want both places put back", rollback.Installs)
		}
	}
	requireOwnFolder(t, f.claudeFolder, "v1")
	requireOwnFolder(t, f.codexFolder, "v1")
}

func TestReinstallWritesALinkedPlaceWithoutTouchingTheLinkedFolder(t *testing.T) {
	ctx := t.Context()
	f := newLinkedFixture(t)
	writeFile(t, filepath.Join(f.codexFolder, "SKILL.md"), []byte(skillDocument("tdd", "edited in codex")), 0o644)
	if claude := f.inspections(t)[f.claude]; claude.Status != library.SkillScopeModified || claude.LinkTarget != f.codexFolder {
		t.Fatalf("claude inspection = %#v; want a modified link", claude)
	}
	results, err := f.lib.Reinstall(ctx, []library.ReinstallRequest{{SkillID: f.skill.ID, Scope: f.claude, Overwrite: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Outcome != library.Reinstalled {
		t.Fatalf("Reinstall() = %#v; want the linked place written", results)
	}
	requireOwnFolder(t, f.claudeFolder, "v1")
	requireInstalledFolder(t, filepath.Dir(f.codexFolder), "tdd", "edited in codex")
	inspections := f.inspections(t)
	if inspections[f.codex].Status != library.SkillScopeModified || len(inspections[f.codex].LinkedBy) != 0 {
		t.Fatalf("codex inspection = %#v; want the edits left and no link", inspections[f.codex])
	}
}

func TestUninstallGivesLinkedPlacesAFolderOfTheirOwnFirst(t *testing.T) {
	ctx := t.Context()
	f := newLinkedFixture(t)
	writeFile(t, filepath.Join(f.codexFolder, "SKILL.md"), []byte(skillDocument("tdd", "edited in codex")), 0o644)
	if err := f.lib.Uninstall(ctx, f.skill.ID, f.codex); err != nil {
		t.Fatal(err)
	}
	if exists, err := pathExists(f.codexFolder); err != nil || exists {
		t.Fatalf("codex folder exists = %v, %v; want it removed", exists, err)
	}
	requireOwnFolder(t, f.claudeFolder, "edited in codex")
	inspections := f.inspections(t)
	if _, ok := inspections[f.codex]; ok || inspections[f.claude].Status != library.SkillScopeModified || inspections[f.claude].LinkTarget != "" {
		t.Fatalf("inspections = %#v; want only claude, still installed with the content it showed", inspections)
	}
	requireNoStagedEntries(t, filepath.Dir(f.claudeFolder))
}

func TestUninstallTheLinkLeavesTheLinkedFolder(t *testing.T) {
	ctx := t.Context()
	f := newLinkedFixture(t)
	if err := f.lib.Uninstall(ctx, f.skill.ID, f.claude); err != nil {
		t.Fatal(err)
	}
	if exists, err := pathExists(f.claudeFolder); err != nil || exists {
		t.Fatalf("claude link exists = %v, %v; want it removed", exists, err)
	}
	requireOwnFolder(t, f.codexFolder, "v1")
}

func TestRemoveSkillRemovesLinkedPlacesWithTheirFolder(t *testing.T) {
	ctx := t.Context()
	f := newLinkedFixture(t)
	if err := f.lib.RemoveSkill(ctx, f.skill.ID, library.RemoveSkillOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, folder := range []string{f.claudeFolder, f.codexFolder} {
		if exists, err := pathExists(folder); err != nil || exists {
			t.Fatalf("%s exists = %v, %v; want it removed", folder, exists, err)
		}
	}
}

func TestDeleteAgentGivesLinkedPlacesAFolderOfTheirOwnFirst(t *testing.T) {
	ctx := t.Context()
	f := newLinkedFixture(t)
	agent, err := f.lib.AddAgent(ctx, library.CustomAgent{Name: "Mine", GlobalDirectory: "~/.mine/skills", ProjectDirectory: ".mine/skills"})
	if err != nil {
		t.Fatal(err)
	}
	mine := library.GlobalScope(agent.Code)
	if err := f.lib.Install(ctx, f.skill.ID, mine, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	mineFolder := filepath.Join(f.home, ".mine", "skills", "tdd")
	writeFile(t, filepath.Join(mineFolder, "SKILL.md"), []byte(skillDocument("tdd", "edited in mine")), 0o644)
	if err := os.Remove(f.claudeFolder); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(mineFolder, f.claudeFolder); err != nil {
		t.Fatal(err)
	}
	if _, err := f.lib.DeleteAgent(ctx, agent.Code, library.DeleteAgentOptions{}); err != nil {
		t.Fatal(err)
	}
	if exists, err := pathExists(mineFolder); err != nil || exists {
		t.Fatalf("custom agent folder exists = %v, %v; want it removed", exists, err)
	}
	requireOwnFolder(t, f.claudeFolder, "edited in mine")
}

func pathExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

// 指向 Agent 目录以外的符号链接

func TestUnmanagedLinksOutsideAgentDirectoriesAreMarkedAndNotSelected(t *testing.T) {
	ctx := t.Context()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	claude := library.GlobalScope(library.AgentClaudeCode)
	codex := library.GlobalScope(library.AgentCodex)
	claudeDirectory := scopeDirectory(t, lib, claude)
	if err := os.MkdirAll(filepath.Join(home, "code", "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	project, err := lib.RegisterProject(ctx, filepath.Join(home, "code", "app"))
	if err != nil {
		t.Fatal(err)
	}
	projectDirectory := scopeDirectory(t, lib, library.ProjectScope(library.AgentClaudeCode, project.ID))

	writeUnmanagedSkill(t, scopeDirectory(t, lib, codex), "pdf", "read pdfs")
	authored := writeUnmanagedSkill(t, filepath.Join(home, "code", "my-skills"), "foo", "my own skill")
	inProject := writeUnmanagedSkill(t, projectDirectory, "bar", "a project skill")
	if err := os.MkdirAll(claudeDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{"pdf": filepath.Join("..", "..", ".agents", "skills", "pdf"), "foo": authored, "bar": inProject} {
		if err := os.Symlink(target, filepath.Join(claudeDirectory, name)); err != nil {
			t.Fatal(err)
		}
	}

	listed, err := lib.ListUnmanagedSkills(ctx)
	if err != nil {
		t.Fatal(err)
	}
	outside := map[string]bool{}
	for _, skill := range listed {
		if skill.Scope == claude {
			outside[skill.Name] = skill.LinkOutside
		}
	}
	if want := map[string]bool{"bar": false, "foo": true, "pdf": false}; !reflect.DeepEqual(outside, want) {
		t.Fatalf("claude LinkOutside = %v; want only the link to the author's folder marked", outside)
	}

	plan, err := lib.PlanFetchUnmanaged(ctx, library.PlanFetchUnmanagedRequest{Scopes: []library.Scope{claude, codex}})
	if err != nil {
		t.Fatal(err)
	}
	selected := map[string]bool{}
	for _, planned := range plan {
		selected[planned.Name] = planned.Selected
		if planned.LinkOutside != (planned.Name == "foo") {
			t.Fatalf("planned %s LinkOutside = %v", planned.Name, planned.LinkOutside)
		}
	}
	if want := map[string]bool{"bar": true, "foo": false, "pdf": true}; !reflect.DeepEqual(selected, want) {
		t.Fatalf("selected = %v; want the link outside Agent directories left unchecked", selected)
	}
}

// 没选的、链着被覆盖的文件夹的那一处:先换成独立的一份,保持现在的内容,不跟着变。

func TestUpdateGivesAnUncheckedLinkedPlaceItsOwnFolderWithItsContent(t *testing.T) {
	ctx := t.Context()
	f := newLinkedFixture(t)
	f.server.serve(zipArchive(t, map[string]string{"tdd/SKILL.md": skillDocument("tdd", "v2")}), "")
	plan, err := f.lib.PlanUpdate(ctx, f.skill.CollectionID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.lib.Update(ctx, library.UpdateRequest{Plan: plan.ID, Skills: []library.SkillID{f.skill.ID}, Installs: []library.ReinstallRequest{{SkillID: f.skill.ID, Scope: f.codex}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Installs) != 1 || result.Installs[0].Scope != f.codex {
		t.Fatalf("update installs = %#v; want only codex recorded", result.Installs)
	}
	requireOwnFolder(t, f.codexFolder, "v2")
	requireOwnFolder(t, f.claudeFolder, "v1")
	if status := f.inspections(t)[f.claude].Status; status != library.SkillScopeStale {
		t.Fatalf("claude status = %q; want it left on the old version", status)
	}

	// 用户又链回去;回滚只装回 Codex,链着它的 Claude Code 保持现在看到的新版本。
	f.link(t)
	rollback, err := f.lib.Rollback(ctx, result.Record)
	if err != nil {
		t.Fatal(err)
	}
	if len(rollback.Installs) != 1 || rollback.Installs[0].Scope != f.codex || rollback.Installs[0].Outcome != library.RollbackReinstalled {
		t.Fatalf("rollback installs = %#v; want only codex put back", rollback.Installs)
	}
	requireOwnFolder(t, f.codexFolder, "v1")
	requireOwnFolder(t, f.claudeFolder, "v2")
}

func TestReinstallTheLinkedFolderLeavesAnUnselectedLinkWithWhatItShowed(t *testing.T) {
	ctx := t.Context()
	f := newLinkedFixture(t)
	writeFile(t, filepath.Join(f.codexFolder, "SKILL.md"), []byte(skillDocument("tdd", "edited in codex")), 0o644)
	results, err := f.lib.Reinstall(ctx, []library.ReinstallRequest{{SkillID: f.skill.ID, Scope: f.codex, Overwrite: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Outcome != library.Reinstalled {
		t.Fatalf("Reinstall() = %#v", results)
	}
	requireOwnFolder(t, f.codexFolder, "v1")
	requireOwnFolder(t, f.claudeFolder, "edited in codex")
}

// 多级链:Claude Code → Mine → Codex 的真实文件夹。删掉中间那个链接前,下游的那一处先换成独立的一份。

func TestUninstallTheMiddleLinkGivesTheDownstreamPlaceItsOwnFolder(t *testing.T) {
	ctx := t.Context()
	f := newLinkedFixture(t)
	agent, err := f.lib.AddAgent(ctx, library.CustomAgent{Name: "Mine", GlobalDirectory: "~/.mine/skills", ProjectDirectory: ".mine/skills"})
	if err != nil {
		t.Fatal(err)
	}
	mine := library.GlobalScope(agent.Code)
	if err := f.lib.Install(ctx, f.skill.ID, mine, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	mineFolder := filepath.Join(f.home, ".mine", "skills", "tdd")
	if err := os.RemoveAll(mineFolder); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(f.codexFolder, mineFolder); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(f.claudeFolder); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(mineFolder, f.claudeFolder); err != nil {
		t.Fatal(err)
	}
	inspections := f.inspections(t)
	if !slices.Contains(inspections[mine].LinkedBy, f.claude) || inspections[mine].LinkTarget != f.codexFolder {
		t.Fatalf("mine inspection = %#v; want a link linked by claude", inspections[mine])
	}
	if linkedBy := inspections[f.codex].LinkedBy; !slices.Contains(linkedBy, f.claude) || !slices.Contains(linkedBy, mine) {
		t.Fatalf("codex LinkedBy = %v; want both links along the chain", linkedBy)
	}
	writeFile(t, filepath.Join(f.codexFolder, "SKILL.md"), []byte(skillDocument("tdd", "edited in codex")), 0o644)

	if err := f.lib.Uninstall(ctx, f.skill.ID, mine); err != nil {
		t.Fatal(err)
	}
	if exists, err := pathExists(mineFolder); err != nil || exists {
		t.Fatalf("mine link exists = %v, %v; want it removed", exists, err)
	}
	requireOwnFolder(t, f.claudeFolder, "edited in codex")
	requireInstalledFolder(t, filepath.Dir(f.codexFolder), "tdd", "edited in codex")
}

func TestUninstallGroupGivesLinkedPlacesAFolderOfTheirOwnFirst(t *testing.T) {
	ctx := t.Context()
	f := newLinkedFixture(t)
	group, err := f.lib.CreateGroup(ctx, "tests")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.lib.AddSkillToGroup(ctx, group.ID, f.skill.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.lib.InstallGroup(ctx, group.ID, f.codex, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := f.lib.Uninstall(ctx, f.skill.ID, f.codex); err != nil {
		t.Fatal(err)
	}
	requireOwnFolder(t, f.codexFolder, "v1")
	if err := f.lib.UninstallGroup(ctx, group.ID, f.codex); err != nil {
		t.Fatal(err)
	}
	if exists, err := pathExists(f.codexFolder); err != nil || exists {
		t.Fatalf("codex folder exists = %v, %v; want it removed", exists, err)
	}
	requireOwnFolder(t, f.claudeFolder, "v1")
}

func TestEditAgentMigrationGivesLinkedPlacesAFolderOfTheirOwn(t *testing.T) {
	ctx := t.Context()
	f := newLinkedFixture(t)
	agent, err := f.lib.AddAgent(ctx, library.CustomAgent{Name: "Mine", GlobalDirectory: "~/.mine/skills", ProjectDirectory: ".mine/skills"})
	if err != nil {
		t.Fatal(err)
	}
	mine := library.GlobalScope(agent.Code)
	if err := f.lib.Install(ctx, f.skill.ID, mine, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	mineFolder := filepath.Join(f.home, ".mine", "skills", "tdd")
	writeFile(t, filepath.Join(mineFolder, "SKILL.md"), []byte(skillDocument("tdd", "edited in mine")), 0o644)
	if err := os.Remove(f.claudeFolder); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(mineFolder, f.claudeFolder); err != nil {
		t.Fatal(err)
	}
	if _, err := f.lib.EditAgent(ctx, agent.Code, library.CustomAgent{Name: "Mine", GlobalDirectory: "~/.mine2/skills", ProjectDirectory: ".mine/skills"}, library.EditAgentOptions{Migrate: true}); err != nil {
		t.Fatal(err)
	}
	if exists, err := pathExists(mineFolder); err != nil || exists {
		t.Fatalf("old folder exists = %v, %v; want it moved", exists, err)
	}
	requireOwnFolder(t, filepath.Join(f.home, ".mine2", "skills", "tdd"), "edited in mine")
	requireOwnFolder(t, f.claudeFolder, "edited in mine")
}

// 迁移的那一处本身是符号链接:新目录里是它指向的内容的独立一份,链接指向的文件夹不动。
func TestEditAgentMigratesALinkedPlaceAsAFolderOfItsOwn(t *testing.T) {
	ctx := t.Context()
	f := newLinkedFixture(t)
	agent, err := f.lib.AddAgent(ctx, library.CustomAgent{Name: "Mine", GlobalDirectory: "~/.mine/skills", ProjectDirectory: ".mine/skills"})
	if err != nil {
		t.Fatal(err)
	}
	mine := library.GlobalScope(agent.Code)
	if err := f.lib.Install(ctx, f.skill.ID, mine, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	mineFolder := filepath.Join(f.home, ".mine", "skills", "tdd")
	if err := os.RemoveAll(mineFolder); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(f.codexFolder, mineFolder); err != nil {
		t.Fatal(err)
	}
	if _, err := f.lib.EditAgent(ctx, agent.Code, library.CustomAgent{Name: "Mine", GlobalDirectory: "~/.mine2/skills", ProjectDirectory: ".mine/skills"}, library.EditAgentOptions{Migrate: true}); err != nil {
		t.Fatal(err)
	}
	requireOwnFolder(t, filepath.Join(f.home, ".mine2", "skills", "tdd"), "v1")
	requireOwnFolder(t, f.codexFolder, "v1")
	if exists, err := pathExists(mineFolder); err != nil || exists {
		t.Fatalf("old link exists = %v, %v; want it removed", exists, err)
	}
}

// 删不掉被链着的文件夹时整次撤销:链接还是原来的链接,文件夹和安装记录都不变。
func TestUninstallFailureKeepsTheLinkAndTheLinkedFolder(t *testing.T) {
	ctx := t.Context()
	f := newLinkedFixture(t)
	makeScopeReadOnly(t, filepath.Dir(f.codexFolder))
	if err := f.lib.Uninstall(ctx, f.skill.ID, f.codex); err == nil {
		t.Fatal("Uninstall() succeeded; want the removal to fail")
	}
	if target, err := os.Readlink(f.claudeFolder); err != nil || target != filepath.Join("..", "..", ".agents", "skills", "tdd") {
		t.Fatalf("claude link = %q, %v; want it restored", target, err)
	}
	requireOwnFolder(t, f.codexFolder, "v1")
	requireNoStagedEntries(t, filepath.Dir(f.claudeFolder))
	inspections := f.inspections(t)
	if len(inspections) != 2 || inspections[f.claude].LinkTarget != f.codexFolder {
		t.Fatalf("inspections = %#v; want both places still installed", inspections)
	}
}

// 只是大小写不同的路径指向 Agent 目录(macOS 默认的卷不区分大小写):不算指向 Agent 目录以外。区分大小写的文件系统上跳过。
func TestUnmanagedLinkToACaseVariantOfAnAgentDirectoryIsNotOutside(t *testing.T) {
	ctx := t.Context()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	claudeDirectory := scopeDirectory(t, lib, library.GlobalScope(library.AgentClaudeCode))
	writeUnmanagedSkill(t, scopeDirectory(t, lib, library.GlobalScope(library.AgentCodex)), "pdf", "read pdfs")
	variant := filepath.Join(home, ".AGENTS", "skills", "pdf")
	if info, err := os.Stat(variant); err != nil || !info.IsDir() {
		t.Skip("the file system is case-sensitive")
	}
	if err := os.MkdirAll(claudeDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(variant, filepath.Join(claudeDirectory, "pdf")); err != nil {
		t.Fatal(err)
	}
	listed, err := lib.ListUnmanagedSkills(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range listed {
		if skill.LinkOutside {
			t.Fatalf("%#v marked as linking outside Agent directories", skill)
		}
	}
}
