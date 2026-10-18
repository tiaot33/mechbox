package library_test

import (
	"testing"

	"github.com/tiaot33/skills-tui/internal/library"
)

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
