package library_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tiaot33/mechbox/internal/library"
)

func TestRollbackOfARenameRestoresNameAndFolders(t *testing.T) {
	t.Parallel()
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

// 确认改名之后来源里新名字的那份又变了,检查更新已经报着;回滚改名时把来源里现在的那份记为跳过,再检查更新也不计入角标。
func TestRollingBackARenameSkipsTheNewerContentAlreadySeen(t *testing.T) {
	t.Parallel()
	fx := newRenameFixture(t)
	ctx := t.Context()
	result, err := fx.lib.Update(ctx, mustPlanUpdate(t, fx.lib, fx.skill.ID))
	if err != nil {
		t.Fatal(err)
	}
	fx.server.serve(zipArchive(t, map[string]string{
		"pdf-kit/SKILL.md":       skillDocument("pdf-kit", renamedBody+"Newer.\n"),
		"pdf-kit/scripts/run.sh": "#!/bin/sh\necho render\n",
		"notes/SKILL.md":         skillDocument("notes", "v1"),
	}), "")
	if check := checkedSkill(t, fx.lib, fx.skill.ID); check.Status != library.UpdateAvailable || !check.Pending {
		t.Fatalf("check before rollback = %#v; want the newer pdf-kit pending", check)
	}
	if _, err := fx.lib.Rollback(ctx, result.Record); err != nil {
		t.Fatal(err)
	}
	if check := checkedSkill(t, fx.lib, fx.skill.ID); check.RenamedTo != "pdf-kit" || check.Pending || !check.Skipped {
		t.Fatalf("rechecked after rolling back the rename = %#v; want the rename skipped", check)
	}
}

func TestRollingBackAnEarlierUpdateRestoresTheNameItHad(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

// 更新之后作者把技能改了名,检查更新已推测出改名;回滚那次更新,来源没再变时推测出的改名也带着回滚掉的改动,
// 回滚完成时和之后检查更新、更新计划都一致地不计入角标;来源再变了才重新计入。
// 那次更新加了文件,回滚回去的版本和改名后的技能不再那么像,推测照样留着。
func TestRollbackAlsoSkipsTheRenameGuessedBefore(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	lib := openTestLibrary(t)
	server := newArchiveServer(t, zipArchive(t, map[string]string{"pdf/SKILL.md": skillDocument("pdf", renamedBody)}), "")
	pdf := fetchArchive(t, lib, server.request())[0]
	server.serve(zipArchive(t, map[string]string{
		"pdf/SKILL.md":       skillDocument("pdf", renamedBody),
		"pdf/scripts/run.sh": "#!/bin/sh\necho render\n",
	}), "")
	updated, err := lib.Update(ctx, mustPlanUpdate(t, lib, pdf.ID))
	if err != nil {
		t.Fatal(err)
	}
	renamed := func(script string) {
		server.serve(zipArchive(t, map[string]string{
			"pdf-kit/SKILL.md":       skillDocument("pdf-kit", renamedBody),
			"pdf-kit/scripts/run.sh": script,
		}), "")
	}
	renamed("#!/bin/sh\necho render\n")
	if check := checkedSkill(t, lib, pdf.ID); check.RenamedTo != "pdf-kit" || !check.Pending {
		t.Fatalf("check before rollback = %#v; want renamed to pdf-kit, pending", check)
	}
	if _, err := lib.Rollback(ctx, updated.Record); err != nil {
		t.Fatal(err)
	}
	got, err := lib.GetSkill(ctx, pdf.ID)
	if err != nil || got.VersionID != pdf.VersionID || got.LastUpdateCheck.RenamedTo != "pdf-kit" || got.LastUpdateCheck.Pending || !got.LastUpdateCheck.Skipped {
		t.Fatalf("after rollback = %#v, %v; want v1 with the rename skipped", got.LastUpdateCheck, err)
	}
	if check := checkedSkill(t, lib, pdf.ID); check.RenamedTo != "pdf-kit" || check.Pending || !check.Skipped {
		t.Fatalf("rechecked after rollback = %#v; want the rename still skipped", check)
	}
	plan, err := lib.PlanUpdate(ctx, pdf.CollectionID)
	if err != nil {
		t.Fatal(err)
	}
	if planned := plannedSkill(t, plan, pdf.ID); planned.RenamedTo != "pdf-kit" || !planned.Skipped {
		t.Fatalf("plan after rollback = %#v; want the rename offered as skipped", planned)
	}
	renamed("#!/bin/sh\necho render all\n")
	if check := checkedSkill(t, lib, pdf.ID); check.RenamedTo != "pdf-kit" || !check.Pending || check.Skipped {
		t.Fatalf("checked after the source moved again = %#v; want pending", check)
	}
}

// 确认改名的那次更新和之后的一次内容更新都回滚:先回滚改名,再回滚之后那次,技能回到新名字,
// 来源里同名的那份就是这个技能,不是改名推测;它带着回滚掉的改动,一并跳过。
func TestRollingBackTheRenameThenTheLaterUpdateLeavesNoRenameGuess(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	fx := newRenameFixture(t)
	renamedUpdate, err := fx.lib.Update(ctx, mustPlanUpdate(t, fx.lib, fx.skill.ID))
	if err != nil {
		t.Fatal(err)
	}
	fx.server.serve(zipArchive(t, map[string]string{
		"pdf-kit/SKILL.md":       skillDocument("pdf-kit", renamedBody+"Number every page.\n"),
		"pdf-kit/scripts/run.sh": "#!/bin/sh\necho render\n",
		"notes/SKILL.md":         skillDocument("notes", "v1"),
	}), "")
	later, err := fx.lib.Update(ctx, mustPlanUpdate(t, fx.lib, fx.skill.ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.lib.Rollback(ctx, renamedUpdate.Record); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.lib.Rollback(ctx, later.Record); err != nil {
		t.Fatal(err)
	}
	got, err := fx.lib.GetSkill(ctx, fx.skill.ID)
	if err != nil || got.Name != "pdf-kit" || got.LastUpdateCheck.Status != library.UpdateAvailable || got.LastUpdateCheck.RenamedTo != "" || got.LastUpdateCheck.Pending || !got.LastUpdateCheck.Skipped {
		t.Fatalf("after both rollbacks = %s %#v, %v; want pdf-kit with the later change skipped and no rename guess", got.Name, got.LastUpdateCheck, err)
	}
	if check := checkedSkill(t, fx.lib, fx.skill.ID); check.Status != library.UpdateAvailable || check.RenamedTo != "" || check.Pending || !check.Skipped {
		t.Fatalf("rechecked after both rollbacks = %#v; want the later change still skipped", check)
	}
}
