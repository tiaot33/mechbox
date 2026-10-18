package library_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tiaot33/skills-tui/internal/library"
)

// 改了作者的技能,照样跟着作者走

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

func TestRereadingLocalSourceReplacesTheEditsWithTheDirectory(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	skill, dir := fetchLocalSkill(t, lib, "notes", "from the directory")
	claude := library.GlobalScope(library.AgentClaudeCode)
	if err := lib.Install(ctx, skill.ID, claude, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	directory, err := lib.ScopeDirectory(ctx, claude)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(directory, "notes", "SKILL.md"), []byte(strings.Replace(string(original), "from the directory", "edited in claude", 1)), 0o644)
	plan, err := lib.PlanUpdateFromScope(ctx, skill.ID, claude)
	if err != nil {
		t.Fatal(err)
	}
	edited, err := lib.Update(ctx, library.UpdateRequest{Plan: plan.ID, Skills: []library.SkillID{skill.ID}})
	if err != nil {
		t.Fatal(err)
	}

	// 本地目录来源的「重新读取」:目录没变,也照样拿目录里的内容和库里的修改比较,换回目录里的内容。
	plan, err = lib.PlanUpdate(ctx, skill.CollectionID)
	if err != nil {
		t.Fatal(err)
	}
	planned := plannedSkill(t, plan, skill.ID)
	if planned.Change != library.PlannedChanged || !planned.Edited {
		t.Fatalf("planned = %#v; want the directory's content as a change", planned)
	}
	result, err := lib.Update(ctx, library.UpdateRequest{Plan: plan.ID, Skills: []library.SkillID{skill.ID}, Installs: []library.ReinstallRequest{{SkillID: skill.ID, Scope: claude}}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Skills[0].Changed || result.Skills[0].PreviousVersionID != edited.Skills[0].Skill.VersionID {
		t.Fatalf("Update = %#v", result)
	}
	file, err := lib.GetVersionFile(ctx, result.Skills[0].Skill.VersionID, "SKILL.md")
	if err != nil || string(file.Content) != string(original) {
		t.Fatalf("SKILL.md = %q, %v; want the directory's content", file.Content, err)
	}
	if content, err := os.ReadFile(filepath.Join(directory, "notes", "SKILL.md")); err != nil || string(content) != string(original) {
		t.Fatalf("installed SKILL.md = %q, %v; want the directory's content", content, err)
	}
}
