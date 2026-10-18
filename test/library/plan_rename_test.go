package library_test

import (
	"testing"

	"github.com/tiaot33/mechbox/internal/library"
)

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

func TestGuessedRenameCanBeSkipped(t *testing.T) {
	fx := newRenameFixture(t)
	ctx := t.Context()
	plan, err := fx.lib.PlanUpdate(ctx, fx.skill.CollectionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.lib.SkipUpdate(ctx, library.SkipRequest{Plan: plan.ID, WholeRevision: true}); err != nil {
		t.Fatal(err)
	}
	if check := checkedSkill(t, fx.lib, fx.skill.ID); check.RenamedTo != "pdf-kit" || !check.Skipped || check.Pending {
		t.Fatalf("skipped rename = %#v", check)
	}
}
