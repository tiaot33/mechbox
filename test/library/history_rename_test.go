package library_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tiaot33/mechbox/internal/library"
)

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
	// 回滚掉的改名算作跳过:不再计入角标,改主意了在更新计划里照样能勾上。
	if check := checkedSkill(t, fx.lib, fx.skill.ID); check.Status != library.UpdateDeleted || check.RenamedTo != "pdf-kit" || !check.Skipped || check.Pending {
		t.Fatalf("check after rolling back the rename = %#v", check)
	}
	requireNewSkills(t, fx.lib, fx.skill.CollectionID, nil)
	again, err := fx.lib.Update(ctx, mustPlanUpdate(t, fx.lib, fx.skill.ID))
	if err != nil || again.Skills[0].Skill.Name != "pdf-kit" {
		t.Fatalf("renaming again after the rollback = %#v, %v", again, err)
	}
}

func TestRollingBackAnEarlierUpdateRestoresTheNameItHad(t *testing.T) {
	fx := newRenameFixture(t)
	ctx := t.Context()
	// 先回到改名前的来源,更新一次内容,再改名并确认。
	fx.server.serve(zipArchive(t, map[string]string{
		"pdf-tools/SKILL.md":       skillDocument("pdf-tools", renamedBody+"One more line.\n"),
		"pdf-tools/scripts/run.sh": "#!/bin/sh\necho render\n",
		"notes/SKILL.md":           skillDocument("notes", "v1"),
	}), "")
	first, err := fx.lib.Update(ctx, mustPlanUpdate(t, fx.lib, fx.skill.ID))
	if err != nil {
		t.Fatal(err)
	}
	fx.server.serve(zipArchive(t, map[string]string{
		"pdf-kit/SKILL.md":       skillDocument("pdf-kit", renamedBody+"One more line.\n"),
		"pdf-kit/scripts/run.sh": "#!/bin/sh\necho render\n",
		"notes/SKILL.md":         skillDocument("notes", "v1"),
	}), "")
	if _, err := fx.lib.Update(ctx, mustPlanUpdate(t, fx.lib, fx.skill.ID)); err != nil {
		t.Fatal(err)
	}
	if skill, err := fx.lib.GetSkill(ctx, fx.skill.ID); err != nil || skill.Name != "pdf-kit" {
		t.Fatalf("after confirming the rename = %#v, %v", skill, err)
	}
	if _, err := fx.lib.Rollback(ctx, first.Record); err != nil {
		t.Fatal(err)
	}
	skill, err := fx.lib.GetSkill(ctx, fx.skill.ID)
	if err != nil || skill.Name != "pdf-tools" || skill.VersionID != fx.skill.VersionID {
		t.Fatalf("after rolling back the earlier update = %#v, %v; want pdf-tools at the first version", skill, err)
	}
	for _, scope := range []library.Scope{fx.claude, fx.codex} {
		requireInstalledFolder(t, fx.dirs[scope], "pdf-tools", renamedBody)
	}
}

func TestRolledBackRenameIsStillOfferedAfterAnotherFetch(t *testing.T) {
	fx := newRenameFixture(t)
	ctx := t.Context()
	result, err := fx.lib.Update(ctx, mustPlanUpdate(t, fx.lib, fx.skill.ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.lib.Rollback(ctx, result.Record); err != nil {
		t.Fatal(err)
	}
	// 之后从同一来源再入库一个技能:入库会把来源里的 pdf-kit 记为见过,回滚掉的改名照样给出。
	fx.server.serve(zipArchive(t, map[string]string{
		"pdf-kit/SKILL.md":       skillDocument("pdf-kit", renamedBody),
		"pdf-kit/scripts/run.sh": "#!/bin/sh\necho render\n",
		"notes/SKILL.md":         skillDocument("notes", "v1"),
		"extra/SKILL.md":         skillDocument("extra", "v1"),
	}), "")
	preview, err := fx.lib.PreviewFetch(ctx, library.PreviewFetchRequest{Collection: fx.skill.CollectionID})
	if err != nil {
		t.Fatal(err)
	}
	fetchSkills(t, fx.lib, preview, []string{"extra"})
	if check := checkedSkill(t, fx.lib, fx.skill.ID); check.RenamedTo != "pdf-kit" || !check.Skipped || check.Pending {
		t.Fatalf("check = %#v; want the rolled back rename, skipped", check)
	}
	if again, err := fx.lib.Update(ctx, mustPlanUpdate(t, fx.lib, fx.skill.ID)); err != nil || again.Skills[0].Skill.Name != "pdf-kit" {
		t.Fatalf("renaming again = %#v, %v", again, err)
	}
}

func TestRollingBackChainedRenamesOutOfOrderKeepsTheSourceName(t *testing.T) {
	fx := newRenameFixture(t)
	ctx := t.Context()
	first, err := fx.lib.Update(ctx, mustPlanUpdate(t, fx.lib, fx.skill.ID))
	if err != nil {
		t.Fatal(err)
	}
	fx.server.serve(zipArchive(t, map[string]string{
		"pdf-suite/SKILL.md":       skillDocument("pdf-suite", renamedBody),
		"pdf-suite/scripts/run.sh": "#!/bin/sh\necho render\n",
		"notes/SKILL.md":           skillDocument("notes", "v1"),
	}), "")
	second, err := fx.lib.Update(ctx, mustPlanUpdate(t, fx.lib, fx.skill.ID))
	if err != nil || second.Skills[0].Skill.Name != "pdf-suite" {
		t.Fatalf("second rename = %#v, %v", second, err)
	}
	for _, record := range []library.UpdateRecordID{first.Record, second.Record} {
		if _, err := fx.lib.Rollback(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	if check := checkedSkill(t, fx.lib, fx.skill.ID); check.RenamedTo != "pdf-suite" || check.Pending {
		t.Fatalf("check = %#v; want the source's name, skipped", check)
	}
}

func TestRenameWithdrawnBySourceClearsTheRememberedName(t *testing.T) {
	fx := newRenameFixture(t)
	ctx := t.Context()
	first, err := fx.lib.Update(ctx, mustPlanUpdate(t, fx.lib, fx.skill.ID))
	if err != nil {
		t.Fatal(err)
	}
	pdfKit := map[string]string{
		"pdf-kit/SKILL.md":       skillDocument("pdf-kit", renamedBody),
		"pdf-kit/scripts/run.sh": "#!/bin/sh\necho render\n",
		"notes/SKILL.md":         skillDocument("notes", "v1"),
	}
	fx.server.serve(zipArchive(t, map[string]string{
		"pdf-suite/SKILL.md":       skillDocument("pdf-suite", renamedBody),
		"pdf-suite/scripts/run.sh": "#!/bin/sh\necho render\n",
		"notes/SKILL.md":           skillDocument("notes", "v1"),
	}), "")
	second, err := fx.lib.Update(ctx, mustPlanUpdate(t, fx.lib, fx.skill.ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.lib.Rollback(ctx, second.Record); err != nil {
		t.Fatal(err)
	}
	// 作者撤回了第二次改名:来源里又是 pdf-kit,检查确认后记下的 pdf-suite 作废。
	fx.server.serve(zipArchive(t, pdfKit), "")
	if check := checkedSkill(t, fx.lib, fx.skill.ID); check.Status != library.UpdateCurrent {
		t.Fatalf("check = %#v; want current", check)
	}
	if _, err := fx.lib.Rollback(ctx, first.Record); err != nil {
		t.Fatal(err)
	}
	if check := checkedSkill(t, fx.lib, fx.skill.ID); check.RenamedTo != "pdf-kit" || check.Pending {
		t.Fatalf("check after rolling back the first rename = %#v; want pdf-kit, skipped", check)
	}
}
