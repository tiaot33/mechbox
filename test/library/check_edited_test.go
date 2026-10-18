package library_test

import (
	"path/filepath"
	"testing"

	"github.com/tiaot33/mechbox/internal/library"
)

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
