package library_test

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tiaot33/skills-tui/internal/library"
)

// 改了作者的技能,照样跟着作者走

// takeClaudeEdits 用 Claude Code 全局里的修改更新库里的 pdf,不换别的地方,返回更新后的技能。
func (f scopeEditFixture) takeClaudeEdits(t *testing.T) library.Skill {
	t.Helper()
	plan, err := f.lib.PlanUpdateFromScope(t.Context(), f.skill.ID, f.claude)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Skills[0].Edited {
		t.Fatalf("plan from scope = %#v; want Edited false", plan.Skills[0])
	}
	result, err := f.lib.Update(t.Context(), library.UpdateRequest{Plan: plan.ID, Skills: []library.SkillID{f.skill.ID}})
	if err != nil {
		t.Fatal(err)
	}
	return result.Skills[0].Skill
}

func (f scopeEditFixture) publish(t *testing.T, files map[string]string) {
	t.Helper()
	f.server.serve(zipArchive(t, files), "")
}

func plannedSkill(t *testing.T, plan library.UpdatePlan, id library.SkillID) library.PlannedSkill {
	t.Helper()
	for _, planned := range plan.Skills {
		if planned.Skill.ID == id {
			return planned
		}
	}
	t.Fatalf("plan = %#v; skill %d missing", plan, id)
	return library.PlannedSkill{}
}

func requireCheck(t *testing.T, lib *library.Library, id library.SkillID, status library.UpdateCheckStatus, skipped, pending bool) {
	t.Helper()
	if got, err := checkSkillUpdate(t, lib, t.Context(), id); err != nil || got != status {
		t.Fatalf("check = %q, %v; want %q", got, err, status)
	}
	check := updateCheckState(t, lib, id)
	if check.Skipped != skipped || check.Pending != pending {
		t.Fatalf("check = %#v; want skipped %v, pending %v", check, skipped, pending)
	}
}

func TestEditedSkillHasNoUpdateWhileTheAuthorHasNotChangedIt(t *testing.T) {
	ctx := t.Context()
	f := newScopeEditFixture(t)
	edited := f.takeClaudeEdits(t)

	requireCheck(t, f.lib, f.skill.ID, library.UpdateCurrent, false, false)
	plan, err := f.lib.PlanUpdate(ctx, f.skill.CollectionID)
	if err != nil {
		t.Fatal(err)
	}
	planned := plannedSkill(t, plan, f.skill.ID)
	if planned.Change != library.PlannedUnchanged || !planned.Edited || planned.Diff != nil {
		t.Fatalf("planned = %#v; want unchanged and marked as edited", planned)
	}
	if file, err := f.lib.GetUpdatePlanFile(ctx, plan.ID, f.skill.ID, "SKILL.md"); err == nil && string(file.Content) != skillDocument("pdf", "edited in claude") {
		t.Fatalf("plan file = %q; want the edits, not the version they are based on", file.Content)
	}

	// 确认没变化时只前移来源版本,不产生新版本,修改留在库里。
	result, err := f.lib.Update(ctx, library.UpdateRequest{Plan: plan.ID, Skills: []library.SkillID{f.skill.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Record != 0 || result.Skills[0].Changed || result.Skills[0].Skill.VersionID != edited.VersionID {
		t.Fatalf("Update = %#v; want nothing to take", result)
	}
	requireCheck(t, f.lib, f.skill.ID, library.UpdateCurrent, false, false)
	requireSkillFiles(t, f.lib, f.skill.ID, "edited in claude")

	// 作者发布的内容正好就是这份修改,也没有要换的。
	f.publish(t, map[string]string{"pdf/SKILL.md": skillDocument("pdf", "edited in claude"), "pdf/scripts/run.sh": "echo hi\n"})
	requireCheck(t, f.lib, f.skill.ID, library.UpdateCurrent, false, false)
}

func TestEditedSkillTakesTheAuthorsNewVersionAndRollsBackToTheEdits(t *testing.T) {
	ctx := t.Context()
	f := newScopeEditFixture(t)
	edited := f.takeClaudeEdits(t)
	f.publish(t, map[string]string{"pdf/SKILL.md": skillDocument("pdf", "v2")})

	requireCheck(t, f.lib, f.skill.ID, library.UpdateAvailable, false, true)
	plan, err := f.lib.PlanUpdate(ctx, f.skill.CollectionID)
	if err != nil {
		t.Fatal(err)
	}
	planned := plannedSkill(t, plan, f.skill.ID)
	wantDiff := library.VersionDiff{Changed: []string{"SKILL.md"}, Added: []string{}, Deleted: []string{"scripts/run.sh"}}
	if planned.Change != library.PlannedChanged || !planned.Edited || planned.Diff == nil || !reflect.DeepEqual(*planned.Diff, wantDiff) {
		t.Fatalf("planned = %#v; want a change from the edits to the author's new version", planned)
	}

	result, err := f.lib.Update(ctx, library.UpdateRequest{
		Plan:     plan.ID,
		Skills:   []library.SkillID{f.skill.ID},
		Installs: []library.ReinstallRequest{{SkillID: f.skill.ID, Scope: f.claude}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Skills[0].Changed || result.Skills[0].PreviousVersionID != edited.VersionID || result.Installs[0].Outcome != library.Reinstalled {
		t.Fatalf("Update = %#v", result)
	}
	requireSkillFiles(t, f.lib, f.skill.ID, "v2")
	requireInstalledFolder(t, f.directories[f.claude], "pdf", "v2")
	version, err := f.lib.GetVersion(ctx, result.Skills[0].Skill.VersionID)
	if err != nil || version.BasedOn != 0 {
		t.Fatalf("version = %#v, %v; want a version from the source", version, err)
	}
	requireCheck(t, f.lib, f.skill.ID, library.UpdateCurrent, false, false)
	if plan, err := f.lib.PlanUpdate(ctx, f.skill.CollectionID); err != nil || plannedSkill(t, plan, f.skill.ID).Edited {
		t.Fatalf("plan = %#v, %v; want the skill no longer edited", plan, err)
	}

	// 回滚回这份修改:库与那次装过的地方都回到修改,作者的这次改动算作跳过。
	if _, err := f.lib.Rollback(ctx, result.Record); err != nil {
		t.Fatal(err)
	}
	skill, err := f.lib.GetSkill(ctx, f.skill.ID)
	if err != nil || skill.VersionID != edited.VersionID {
		t.Fatalf("skill = %#v, %v; want the edits back", skill, err)
	}
	requireInstalledFolder(t, f.directories[f.claude], "pdf", "edited in claude")
	requireCheck(t, f.lib, f.skill.ID, library.UpdateAvailable, true, false)
}

func TestSkippingTheAuthorsChangeKeepsTheEditsUntilTheAuthorChangesAgain(t *testing.T) {
	ctx := t.Context()
	f := newScopeEditFixture(t)
	edited := f.takeClaudeEdits(t)
	f.publish(t, map[string]string{"pdf/SKILL.md": skillDocument("pdf", "v2")})

	plan, err := f.lib.PlanUpdate(ctx, f.skill.CollectionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.lib.SkipUpdate(ctx, library.SkipRequest{Plan: plan.ID, Skills: []library.SkillID{f.skill.ID}}); err != nil {
		t.Fatal(err)
	}
	requireCheck(t, f.lib, f.skill.ID, library.UpdateAvailable, true, false)
	if plan, err := f.lib.PlanUpdate(ctx, f.skill.CollectionID); err != nil || !plannedSkill(t, plan, f.skill.ID).Skipped {
		t.Fatalf("plan = %#v, %v; want the skipped change listed", plan, err)
	}
	if skill, err := f.lib.GetSkill(ctx, f.skill.ID); err != nil || skill.VersionID != edited.VersionID {
		t.Fatalf("skill = %#v, %v; want the edits kept", skill, err)
	}

	f.publish(t, map[string]string{"pdf/SKILL.md": skillDocument("pdf", "v3")})
	requireCheck(t, f.lib, f.skill.ID, library.UpdateAvailable, false, true)
}

func TestEditedSkillInGitSourceFollowsOnlyItsOwnChanges(t *testing.T) {
	ctx := t.Context()
	config := isolateGit(t)
	repository := skillRepository(t)
	runGit(t, repository, "rm", "--quiet", "SKILL.md")
	commitGit(t, repository, "Move every skill under skills")
	redirectRemote(t, config, repository, "https://gitlab.com/group/repo.git")
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	preview, err := lib.PreviewFetch(ctx, library.PreviewFetchRequest{Input: "https://gitlab.com/group/repo"})
	if err != nil {
		t.Fatal(err)
	}
	fetched := fetchSkills(t, lib, preview, []string{"alpha", "beta"})
	alpha := fetched[0]
	claude := library.GlobalScope(library.AgentClaudeCode)
	if err := lib.Install(ctx, alpha.ID, claude, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	directory, err := lib.ScopeDirectory(ctx, claude)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(directory, "alpha", "SKILL.md"), []byte(skillDocument("alpha", "edited")), 0o644)
	plan, err := lib.PlanUpdateFromScope(ctx, alpha.ID, claude)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Update(ctx, library.UpdateRequest{Plan: plan.ID, Skills: []library.SkillID{alpha.ID}}); err != nil {
		t.Fatal(err)
	}

	// 作者只改了同一来源里的别的技能。
	writeFile(t, filepath.Join(repository, "skills", "beta", "SKILL.md"), []byte(skillDocument("beta", "second")), 0o644)
	commitGit(t, repository, "beta")
	requireCheck(t, lib, alpha.ID, library.UpdateCurrent, false, false)

	writeFile(t, filepath.Join(repository, "skills", "alpha", "SKILL.md"), []byte(skillDocument("alpha", "second")), 0o644)
	commitGit(t, repository, "alpha")
	requireCheck(t, lib, alpha.ID, library.UpdateAvailable, false, true)
}

func requireSkillFiles(t *testing.T, lib *library.Library, id library.SkillID, body string) {
	t.Helper()
	skill, err := lib.GetSkill(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	file, err := lib.GetVersionFile(t.Context(), skill.VersionID, "SKILL.md")
	if err != nil || string(file.Content) != skillDocument(skill.Name, body) {
		t.Fatalf("SKILL.md = %q, %v; want %q", file.Content, err, body)
	}
}
