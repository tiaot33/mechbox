package library_test

import (
	"slices"
	"testing"

	"github.com/tiaot33/mechbox/internal/library"
)

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
