package library_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/tiaot33/mechbox/internal/library"
)

// 来源里改名的技能:推测、确认、否认与回滚

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
