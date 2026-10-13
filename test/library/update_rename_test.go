package library_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/tiaot33/skills-tui/internal/library"
)

// 来源里改名的技能:推测、确认、否认与回滚

const renamedBody = "Render PDF pages.\nUse the script in scripts/run.sh.\nKeep the page order.\nAsk before overwriting files.\n"

// renameFixture 从 URL 来源入库 pdf-tools,装到 Claude Code 与 Codex 的全局,加进一个组,然后作者把它改名成 pdf-kit。
type renameFixture struct {
	lib      *library.Library
	server   *archiveServer
	skill    library.Skill
	group    library.GroupID
	claude   library.Scope
	codex    library.Scope
	dirs     map[library.Scope]string
	versions int
}

func newRenameFixture(t *testing.T) *renameFixture {
	t.Helper()
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	fx := &renameFixture{lib: openTestLibrary(t), claude: library.GlobalScope(library.AgentClaudeCode), codex: library.GlobalScope(library.AgentCodex), dirs: map[library.Scope]string{}}
	fx.server = newArchiveServer(t, zipArchive(t, map[string]string{
		"pdf-tools/SKILL.md":       skillDocument("pdf-tools", renamedBody),
		"pdf-tools/scripts/run.sh": "#!/bin/sh\necho render\n",
		"notes/SKILL.md":           skillDocument("notes", "v1"),
	}), "")
	preview, err := fx.lib.PreviewFetch(ctx, fx.server.request())
	if err != nil {
		t.Fatal(err)
	}
	skills := fetchSkills(t, fx.lib, preview, []string{"pdf-tools", "notes"})
	fx.skill = skills[slices.IndexFunc(skills, func(s library.Skill) bool { return s.Name == "pdf-tools" })]
	group, err := fx.lib.CreateGroup(ctx, "docs")
	if err != nil {
		t.Fatal(err)
	}
	fx.group = group.ID
	if _, err := fx.lib.AddSkillToGroup(ctx, group.ID, fx.skill.ID); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []library.Scope{fx.claude, fx.codex} {
		if err := fx.lib.Install(ctx, fx.skill.ID, scope, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
		if fx.dirs[scope], err = fx.lib.ScopeDirectory(ctx, scope); err != nil {
			t.Fatal(err)
		}
	}
	fx.server.serve(zipArchive(t, map[string]string{
		"pdf-kit/SKILL.md":       skillDocument("pdf-kit", renamedBody),
		"pdf-kit/scripts/run.sh": "#!/bin/sh\necho render\n",
		"notes/SKILL.md":         skillDocument("notes", "v1"),
	}), "")
	return fx
}

func TestRenameIsGuessedAndCountsTowardTheBadge(t *testing.T) {
	fx := newRenameFixture(t)
	check := checkedSkill(t, fx.lib, fx.skill.ID)
	if check.Status != library.UpdateDeleted || check.RenamedTo != "pdf-kit" || !check.Pending {
		t.Fatalf("check = %#v; want deleted, probably renamed to pdf-kit, pending", check)
	}
	requireNewSkills(t, fx.lib, fx.skill.CollectionID, nil)
	plan, err := fx.lib.PlanUpdate(t.Context(), fx.skill.CollectionID)
	if err != nil {
		t.Fatal(err)
	}
	planned := plan.Skills[slices.IndexFunc(plan.Skills, func(p library.PlannedSkill) bool { return p.Skill.ID == fx.skill.ID })]
	if planned.Change != library.PlannedDeleted || planned.RenamedTo != "pdf-kit" || planned.Diff == nil {
		t.Fatalf("planned = %#v", planned)
	}
	if len(plan.Installs) != 2 {
		t.Fatalf("installs = %#v; want both places of the renamed skill", plan.Installs)
	}
	// 继续保留之后,推测出的改名照样计入。
	if _, err := fx.lib.KeepDeletedSkill(t.Context(), fx.skill.ID); err != nil {
		t.Fatal(err)
	}
	if skill, err := fx.lib.GetSkill(t.Context(), fx.skill.ID); err != nil || !skill.LastUpdateCheck.Pending {
		t.Fatalf("kept but renamed = %#v, %v", skill.LastUpdateCheck, err)
	}
}

func TestConfirmedRenameKeepsHistoryGroupsAndInstalls(t *testing.T) {
	fx := newRenameFixture(t)
	ctx := t.Context()
	plan, err := fx.lib.PlanUpdate(ctx, fx.skill.CollectionID)
	if err != nil {
		t.Fatal(err)
	}
	// 只勾 Claude Code 那一处:它换成新文件夹名,Codex 那处保持旧文件夹名。
	result, err := fx.lib.Update(ctx, library.UpdateRequest{Plan: plan.ID, Skills: []library.SkillID{fx.skill.ID}, Installs: []library.ReinstallRequest{{SkillID: fx.skill.ID, Scope: fx.claude}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Installs[0].Outcome != library.Reinstalled || result.Skills[0].Skill.Name != "pdf-kit" || result.Skills[0].Skill.ID != fx.skill.ID {
		t.Fatalf("Update = %#v", result)
	}
	skill, err := fx.lib.GetSkill(ctx, fx.skill.ID)
	if err != nil || skill.Name != "pdf-kit" || skill.LastUpdateCheck.Status != library.UpdateCurrent {
		t.Fatalf("renamed skill = %#v, %v", skill, err)
	}
	if versions, err := fx.lib.ListVersions(ctx, fx.skill.ID); err != nil || len(versions) != 2 {
		t.Fatalf("versions = %#v, %v; want the history carried over", versions, err)
	}
	if members := groupMembers(t, fx.lib, fx.group); !slices.Equal(members, []library.SkillID{fx.skill.ID}) {
		t.Fatalf("group members = %v", members)
	}
	requireInstalledFolder(t, fx.dirs[fx.claude], "pdf-kit", renamedBody)
	if _, err := os.Stat(filepath.Join(fx.dirs[fx.claude], "pdf-tools")); !os.IsNotExist(err) {
		t.Fatalf("old folder in the reinstalled place: %v", err)
	}
	requireInstalledFolder(t, fx.dirs[fx.codex], "pdf-tools", renamedBody)
	inspections, err := fx.lib.InspectSkillScopes(ctx, library.SkillScopeFilter{SkillID: fx.skill.ID})
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[library.AgentCode]library.SkillScopeInspection{}
	for _, inspection := range inspections {
		statuses[inspection.Scope.Agent] = inspection
	}
	if got := statuses[library.AgentClaudeCode]; got.Status != library.SkillScopeCurrent || got.Folder != filepath.Join(fx.dirs[fx.claude], "pdf-kit") {
		t.Fatalf("claude inspection = %#v", got)
	}
	// 没勾的地方按装出去时的文件夹名巡检,不报找不到。
	if got := statuses[library.AgentCodex]; got.Status != library.SkillScopeStale || got.Folder != filepath.Join(fx.dirs[fx.codex], "pdf-tools") {
		t.Fatalf("codex inspection = %#v", got)
	}
	// 之后单独重新安装那一处,也换成新文件夹名。
	reinstall(t, fx.lib, library.ReinstallRequest{SkillID: fx.skill.ID, Scope: fx.codex})
	requireInstalledFolder(t, fx.dirs[fx.codex], "pdf-kit", renamedBody)
	if _, err := os.Stat(filepath.Join(fx.dirs[fx.codex], "pdf-tools")); !os.IsNotExist(err) {
		t.Fatalf("old folder after reinstall: %v", err)
	}
	if check := checkedSkill(t, fx.lib, fx.skill.ID); check.Status != library.UpdateCurrent || check.Pending {
		t.Fatalf("check after the rename = %#v", check)
	}
}

func TestRejectedRenameIsNotGuessedAgain(t *testing.T) {
	fx := newRenameFixture(t)
	ctx := t.Context()
	plan, err := fx.lib.PlanUpdate(ctx, fx.skill.CollectionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.lib.RejectRename(ctx, plan.ID, fx.skill.ID); err != nil {
		t.Fatal(err)
	}
	if skill, err := fx.lib.GetSkill(ctx, fx.skill.ID); err != nil || skill.LastUpdateCheck.RenamedTo != "" || skill.LastUpdateCheck.Status != library.UpdateDeleted {
		t.Fatalf("after rejecting = %#v, %v", skill.LastUpdateCheck, err)
	}
	requireNewSkills(t, fx.lib, fx.skill.CollectionID, []string{"pdf-kit"})
	check := checkedSkill(t, fx.lib, fx.skill.ID)
	if check.Status != library.UpdateDeleted || check.RenamedTo != "" || !check.Pending {
		t.Fatalf("rechecked = %#v; want deleted without a guess", check)
	}
	requireNewSkills(t, fx.lib, fx.skill.CollectionID, []string{"pdf-kit"})
	if err := fx.lib.RejectRename(ctx, plan.ID, 9999); !library.IsCode(err, library.InvalidArgument) {
		t.Fatalf("reject without a guess = %v", err)
	}
}

func TestRollbackOfARenameRestoresNameAndFolders(t *testing.T) {
	fx := newRenameFixture(t)
	ctx := t.Context()
	plan, err := fx.lib.PlanUpdate(ctx, fx.skill.CollectionID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fx.lib.Update(ctx, library.UpdateRequest{Plan: plan.ID, Skills: []library.SkillID{fx.skill.ID}, Installs: []library.ReinstallRequest{{SkillID: fx.skill.ID, Scope: fx.claude}}})
	if err != nil {
		t.Fatal(err)
	}
	rollback, err := fx.lib.Rollback(ctx, result.Record)
	if err != nil || len(rollback.Installs) != 1 || rollback.Installs[0].Outcome != library.RollbackReinstalled || rollback.Skills[0].Name != "pdf-tools" || rollback.Skills[0].FromName != "pdf-kit" {
		t.Fatalf("rollback = %#v, %v", rollback, err)
	}
	skill, err := fx.lib.GetSkill(ctx, fx.skill.ID)
	if err != nil || skill.Name != "pdf-tools" || skill.VersionID != fx.skill.VersionID {
		t.Fatalf("after rollback = %#v, %v", skill, err)
	}
	for _, scope := range []library.Scope{fx.claude, fx.codex} {
		requireInstalledFolder(t, fx.dirs[scope], "pdf-tools", renamedBody)
		if _, err := os.Stat(filepath.Join(fx.dirs[scope], "pdf-kit")); !os.IsNotExist(err) {
			t.Fatalf("new folder left in %s: %v", scope.Agent, err)
		}
	}
	for _, inspection := range inspectSkillScopes(t, fx.lib) {
		if inspection.SkillID == fx.skill.ID && inspection.Status != library.SkillScopeCurrent {
			t.Fatalf("inspection after rollback = %#v", inspection)
		}
	}
	// 回滚掉的改名不再计入角标。
	if check := checkedSkill(t, fx.lib, fx.skill.ID); check.Status != library.UpdateDeleted || check.RenamedTo != "" || check.Pending {
		t.Fatalf("check after rolling back the rename = %#v", check)
	}
}
