package library_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tiaot33/mechbox/internal/library"
)

// 用目录里的修改更新

func TestUpdateFromScopeTakesTheEditsAndReinstallsCheckedPlaces(t *testing.T) {
	ctx := t.Context()
	f := newScopeEditFixture(t)
	lib, skill := f.lib, f.skill
	writeFile(t, filepath.Join(f.directories[f.project], "pdf", "SKILL.md"), []byte(skillDocument("pdf", "edited in project")), 0o644)
	before := updateCheckState(t, lib, skill.ID)

	plan, err := lib.PlanUpdateFromScope(ctx, skill.ID, f.claude)
	if err != nil {
		t.Fatal(err)
	}
	if plan.FromScope == nil || *plan.FromScope != f.claude || plan.CollectionID != skill.CollectionID || plan.Revision != "" || len(plan.Skills) != 1 {
		t.Fatalf("plan = %#v", plan)
	}
	planned := plan.Skills[0]
	wantDiff := library.VersionDiff{Changed: []string{"SKILL.md"}, Added: []string{"scripts/run.sh"}, Deleted: []string{}}
	if planned.Skill.ID != skill.ID || planned.Change != library.PlannedChanged || planned.Diff == nil || !reflect.DeepEqual(*planned.Diff, wantDiff) {
		t.Fatalf("planned skill = %#v", planned)
	}
	if !reflect.DeepEqual(planned.Risks, []library.UpdateRisk{{Kind: library.RiskScriptAdded, Path: "scripts/run.sh"}}) {
		t.Fatalf("risks = %#v", planned.Risks)
	}
	places := map[library.Scope]library.SkillScopeStatus{}
	for _, install := range plan.Installs {
		places[install.Scope] = install.Status
	}
	if want := map[library.Scope]library.SkillScopeStatus{f.codex: library.SkillScopeCurrent, f.project: library.SkillScopeModified}; !reflect.DeepEqual(places, want) {
		t.Fatalf("plan installs = %v; want the other places without the one the edits come from", places)
	}
	file, err := lib.GetUpdatePlanFile(ctx, plan.ID, skill.ID, "SKILL.md")
	if err != nil || string(file.Content) != skillDocument("pdf", "edited in claude") {
		t.Fatalf("GetUpdatePlanFile = %q, %v", file.Content, err)
	}

	result, err := lib.Update(ctx, library.UpdateRequest{
		Plan:     plan.ID,
		Skills:   []library.SkillID{skill.ID},
		Installs: []library.ReinstallRequest{{SkillID: skill.ID, Scope: f.codex}, {SkillID: skill.ID, Scope: f.project, Overwrite: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Record == 0 || result.Revision != "" || len(result.Skills) != 1 || !result.Skills[0].Changed || result.Skills[0].PreviousVersionID != skill.VersionID {
		t.Fatalf("Update = %#v", result)
	}
	if len(result.Installs) != 2 || result.Installs[0].Outcome != library.Reinstalled || result.Installs[1].Outcome != library.Reinstalled {
		t.Fatalf("installs = %#v", result.Installs)
	}
	for _, scope := range []library.Scope{f.claude, f.codex, f.project} {
		requireInstalledFolder(t, f.directories[scope], "pdf", "edited in claude")
		if content, err := os.ReadFile(filepath.Join(f.directories[scope], "pdf", "scripts", "run.sh")); err != nil || string(content) != "echo hi\n" {
			t.Fatalf("%v scripts/run.sh = %q, %v", scope, content, err)
		}
	}
	if want := map[library.Scope]library.SkillScopeStatus{f.claude: library.SkillScopeCurrent, f.codex: library.SkillScopeCurrent, f.project: library.SkillScopeCurrent}; !reflect.DeepEqual(f.statuses(t), want) {
		t.Fatalf("statuses = %v; want every place on the new version", f.statuses(t))
	}

	updated, err := lib.GetSkill(ctx, skill.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.VersionID != result.Skills[0].Skill.VersionID || updated.Revision != skill.Revision {
		t.Fatalf("skill = %#v; want the new version on the revision the edits are based on", updated)
	}
	if after := updateCheckState(t, lib, skill.ID); !reflect.DeepEqual(after, before) {
		t.Fatalf("update check = %#v; want it untouched (%#v)", after, before)
	}
	versions, err := lib.ListVersions(ctx, skill.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 || versions[1].ID != updated.VersionID || versions[1].BasedOn != skill.VersionID || versions[1].Revision != versions[0].Revision || versions[0].BasedOn != 0 {
		t.Fatalf("versions = %#v", versions)
	}
	if diff, err := lib.DiffVersions(ctx, versions[0].ID, versions[1].ID); err != nil || !reflect.DeepEqual(diff, wantDiff) {
		t.Fatalf("DiffVersions = %#v, %v", diff, err)
	}

	history, err := lib.ListUpdateHistory(ctx, library.UpdateHistoryFilter{SkillID: skill.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].ID != result.Record || history[0].Kind != library.UpdateRecordUpdate || history[0].FromScope == nil || *history[0].FromScope != f.claude || history[0].Revision != "" {
		t.Fatalf("history = %#v", history)
	}
	if want := []library.RecordedSkill{{SkillID: skill.ID, Name: "pdf", FromVersionID: skill.VersionID, ToVersionID: updated.VersionID}}; !reflect.DeepEqual(history[0].Skills, want) {
		t.Fatalf("recorded skills = %#v", history[0].Skills)
	}

	rollback, err := lib.Rollback(ctx, result.Record)
	if err != nil {
		t.Fatal(err)
	}
	if want := []library.RecordedSkill{{SkillID: skill.ID, Name: "pdf", FromVersionID: updated.VersionID, ToVersionID: skill.VersionID}}; !reflect.DeepEqual(rollback.Skills, want) {
		t.Fatalf("rolled back skills = %#v", rollback.Skills)
	}
	if len(rollback.Installs) != 2 || rollback.Installs[0].Outcome != library.RollbackReinstalled || rollback.Installs[1].Outcome != library.RollbackReinstalled {
		t.Fatalf("rollback installs = %#v; want the places the update reinstalled put back", rollback.Installs)
	}
	requireInstalledFolder(t, f.directories[f.codex], "pdf", "v1")
	requireInstalledFolder(t, f.directories[f.project], "pdf", "v1")
	requireInstalledFolder(t, f.directories[f.claude], "pdf", "edited in claude")
	rolledBack, err := lib.GetSkill(ctx, skill.ID)
	if err != nil || rolledBack.VersionID != skill.VersionID {
		t.Fatalf("skill after rollback = %#v, %v", rolledBack, err)
	}
	if after := updateCheckState(t, lib, skill.ID); !reflect.DeepEqual(after, before) {
		t.Fatalf("update check after rollback = %#v; want it untouched (%#v)", after, before)
	}
	if status := f.statuses(t)[f.claude]; status != library.SkillScopeStale {
		t.Fatalf("place the edits came from = %s; want it left as it was, matching the rolled back version", status)
	}
}

func TestUpdateFromScopeLeavesUncheckedPlacesAndRefusesEditedOnesWithoutConsent(t *testing.T) {
	ctx := t.Context()
	f := newScopeEditFixture(t)
	lib, skill := f.lib, f.skill
	writeFile(t, filepath.Join(f.directories[f.project], "pdf", "SKILL.md"), []byte("edited in project"), 0o644)
	plan, err := lib.PlanUpdateFromScope(ctx, skill.ID, f.claude)
	if err != nil {
		t.Fatal(err)
	}
	result, err := lib.Update(ctx, library.UpdateRequest{
		Plan:     plan.ID,
		Skills:   []library.SkillID{skill.ID},
		Installs: []library.ReinstallRequest{{SkillID: skill.ID, Scope: f.project}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Installs) != 1 || result.Installs[0].Outcome != library.ReinstallFailed || result.Installs[0].Error.Code != library.AlreadyExists {
		t.Fatalf("installs = %#v; want the edited place refused", result.Installs)
	}
	requireInstalledFolder(t, f.directories[f.codex], "pdf", "v1")
	requireSkillFile(t, f.directories[f.project], "pdf", "edited in project")
	if want := map[library.Scope]library.SkillScopeStatus{f.claude: library.SkillScopeCurrent, f.codex: library.SkillScopeStale, f.project: library.SkillScopeModified}; !reflect.DeepEqual(f.statuses(t), want) {
		t.Fatalf("statuses = %v", f.statuses(t))
	}
	retried, err := lib.RetryUpdateInstalls(ctx, result.Record, []library.ReinstallRequest{{SkillID: skill.ID, Scope: f.codex}})
	if err != nil || len(retried) != 1 || retried[0].Outcome != library.Reinstalled {
		t.Fatalf("RetryUpdateInstalls = %#v, %v", retried, err)
	}
	requireInstalledFolder(t, f.directories[f.codex], "pdf", "edited in claude")
}

func TestUpdateFromScopeWithLocalSource(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	skill, _ := fetchLocalSkill(t, lib, "pdf", "v1")
	claude := library.GlobalScope(library.AgentClaudeCode)
	if err := lib.Install(ctx, skill.ID, claude, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	directory, err := lib.ScopeDirectory(ctx, claude)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(directory, "pdf", "SKILL.md"), []byte(skillDocument("pdf", "edited")), 0o644)
	plan, err := lib.PlanUpdateFromScope(ctx, skill.ID, claude)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Installs) != 0 {
		t.Fatalf("plan installs = %#v; want none besides the place the edits come from", plan.Installs)
	}
	if _, err := lib.Update(ctx, library.UpdateRequest{Plan: plan.ID, Skills: []library.SkillID{skill.ID}}); err != nil {
		t.Fatal(err)
	}
	updated, err := lib.GetSkill(ctx, skill.ID)
	if err != nil {
		t.Fatal(err)
	}
	files := skillFiles(t, lib, updated)
	if len(files) != 1 || string(files[0].Content) != skillDocument("pdf", "edited") {
		t.Fatalf("library files = %#v", files)
	}
}

func TestUpdateFromScopeRechecksTheFolderAndTheRequest(t *testing.T) {
	ctx := t.Context()
	f := newScopeEditFixture(t)
	lib, skill := f.lib, f.skill
	plan, err := lib.PlanUpdateFromScope(ctx, skill.ID, f.claude)
	if err != nil {
		t.Fatal(err)
	}
	err = lib.SkipUpdate(ctx, library.SkipRequest{Plan: plan.ID, Skills: []library.SkillID{skill.ID}})
	requireErrorDetails(t, err, library.InvalidArgument, library.ErrorDetails{Field: "plan"})

	_, err = lib.Update(ctx, library.UpdateRequest{Plan: plan.ID, Skills: []library.SkillID{skill.ID}, Installs: []library.ReinstallRequest{{SkillID: skill.ID, Scope: f.claude, Overwrite: true}}})
	requireErrorDetails(t, err, library.InvalidArgument, library.ErrorDetails{Field: "installs", SkillID: skill.ID})

	writeFile(t, filepath.Join(f.directories[f.claude], "pdf", "SKILL.md"), []byte(skillDocument("pdf", "edited again")), 0o644)
	_, err = lib.Update(ctx, library.UpdateRequest{Plan: plan.ID, Skills: []library.SkillID{skill.ID}})
	requireErrorDetails(t, err, library.Conflict, library.ErrorDetails{SkillID: skill.ID})

	versions, err := lib.ListVersions(ctx, skill.ID)
	if err != nil || len(versions) != 1 {
		t.Fatalf("versions = %#v, %v; want nothing written", versions, err)
	}
	history, err := lib.ListUpdateHistory(ctx, library.UpdateHistoryFilter{SkillID: skill.ID})
	if err != nil || len(history) != 0 {
		t.Fatalf("history = %#v, %v; want nothing recorded", history, err)
	}
}

// 几个 Agent 共用一个目录时,取内容的文件夹也是它们的,计划里不列,更新时也不能勾。
func TestUpdateFromScopeLeavesOutAgentsSharingTheFolder(t *testing.T) {
	ctx := t.Context()
	f := newScopeEditFixture(t)
	lib, skill := f.lib, f.skill
	shared, err := lib.AddAgent(ctx, library.CustomAgent{Name: "Shared", GlobalDirectory: "~/.claude/skills", ProjectDirectory: ".shared/skills"})
	if err != nil {
		t.Fatal(err)
	}
	sharedScope := library.GlobalScope(shared.Code)
	plan, err := lib.PlanUpdateFromScope(ctx, skill.ID, f.claude)
	if err != nil {
		t.Fatal(err)
	}
	for _, install := range plan.Installs {
		if install.Scope == f.claude || install.Scope == sharedScope {
			t.Fatalf("plan installs = %#v; want neither scope of the shared folder", plan.Installs)
		}
	}
	_, err = lib.Update(ctx, library.UpdateRequest{Plan: plan.ID, Skills: []library.SkillID{skill.ID}, Installs: []library.ReinstallRequest{{SkillID: skill.ID, Scope: sharedScope}}})
	requireErrorDetails(t, err, library.InvalidArgument, library.ErrorDetails{Field: "installs", SkillID: skill.ID})
}

// 一处是指向另一处的符号链接时,两处是同一个文件夹,内容就是这份修改:计划里不把另一处列为被改过,更新时也不能勾。
func TestUpdateFromScopeTreatsPlacesLinkedToTheSameFolderAsOne(t *testing.T) {
	ctx := t.Context()
	f := newScopeEditFixture(t)
	lib, skill := f.lib, f.skill
	claudeFolder, codexFolder := filepath.Join(f.directories[f.claude], "pdf"), filepath.Join(f.directories[f.codex], "pdf")
	if err := os.RemoveAll(claudeFolder); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(codexFolder, claudeFolder); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(codexFolder, "SKILL.md"), []byte(skillDocument("pdf", "edited in codex")), 0o644)

	plan, err := lib.PlanUpdateFromScope(ctx, skill.ID, f.claude)
	if err != nil {
		t.Fatal(err)
	}
	places := map[library.Scope]library.SkillScopeStatus{}
	for _, install := range plan.Installs {
		places[install.Scope] = install.Status
	}
	if want := map[library.Scope]library.SkillScopeStatus{f.project: library.SkillScopeCurrent}; !reflect.DeepEqual(places, want) {
		t.Fatalf("plan installs = %v; want the place linked to the same folder left out", places)
	}
	_, err = lib.Update(ctx, library.UpdateRequest{Plan: plan.ID, Skills: []library.SkillID{skill.ID}, Installs: []library.ReinstallRequest{{SkillID: skill.ID, Scope: f.codex, Overwrite: true}}})
	requireErrorDetails(t, err, library.InvalidArgument, library.ErrorDetails{Field: "installs", SkillID: skill.ID})

	result, err := lib.Update(ctx, library.UpdateRequest{Plan: plan.ID, Skills: []library.SkillID{skill.ID}, Installs: []library.ReinstallRequest{{SkillID: skill.ID, Scope: f.project}}})
	if err != nil {
		t.Fatal(err)
	}
	if want := map[library.Scope]library.SkillScopeStatus{f.claude: library.SkillScopeCurrent, f.codex: library.SkillScopeCurrent, f.project: library.SkillScopeCurrent}; !reflect.DeepEqual(f.statuses(t), want) {
		t.Fatalf("statuses = %v; want every place on the new version", f.statuses(t))
	}
	if target, err := os.Readlink(claudeFolder); err != nil || target != codexFolder {
		t.Fatalf("claude folder link = %q, %v; want it left as it was", target, err)
	}
	requireRollbackLeavesTheEditedFolder(t, f, result.Record, codexFolder, "edited in codex")
}

// requireRollbackLeavesTheEditedFolder 回滚一次用目录里的修改更新:只装回那次换过的项目,取内容的那个文件夹原样不动。
func requireRollbackLeavesTheEditedFolder(t *testing.T, f scopeEditFixture, record library.UpdateRecordID, folder, body string) {
	t.Helper()
	rollback, err := f.lib.Rollback(t.Context(), record)
	if err != nil {
		t.Fatal(err)
	}
	if len(rollback.Installs) != 1 || rollback.Installs[0].Scope != f.project || rollback.Installs[0].Outcome != library.RollbackReinstalled {
		t.Fatalf("rollback installs = %#v; want only the project put back", rollback.Installs)
	}
	requireInstalledFolder(t, f.directories[f.project], "pdf", "v1")
	if content, err := os.ReadFile(filepath.Join(folder, "SKILL.md")); err != nil || string(content) != skillDocument("pdf", body) {
		t.Fatalf("folder the edits came from = %q, %v; want it left as it was", content, err)
	}
}

// 符号链接的目标只是大小写不同(macOS 默认的卷不区分大小写):仍是同一个文件夹。区分大小写的文件系统上跳过。
func TestUpdateFromScopeTreatsACaseVariantLinkAsTheSameFolder(t *testing.T) {
	ctx := t.Context()
	f := newScopeEditFixture(t)
	lib, skill := f.lib, f.skill
	claudeFolder, codexFolder := filepath.Join(f.directories[f.claude], "pdf"), filepath.Join(f.directories[f.codex], "pdf")
	variant := filepath.Join(filepath.Dir(codexFolder), "PDF")
	if info, err := os.Stat(variant); err != nil || !info.IsDir() {
		t.Skip("the file system is case-sensitive")
	}
	if err := os.RemoveAll(claudeFolder); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(variant, claudeFolder); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(codexFolder, "SKILL.md"), []byte(skillDocument("pdf", "edited in codex")), 0o644)

	plan, err := lib.PlanUpdateFromScope(ctx, skill.ID, f.claude)
	if err != nil {
		t.Fatal(err)
	}
	for _, install := range plan.Installs {
		if install.Scope == f.codex {
			t.Fatalf("plan installs = %#v; want the case-variant folder left out", plan.Installs)
		}
	}
	_, err = lib.Update(ctx, library.UpdateRequest{Plan: plan.ID, Skills: []library.SkillID{skill.ID}, Installs: []library.ReinstallRequest{{SkillID: skill.ID, Scope: f.codex, Overwrite: true}}})
	requireErrorDetails(t, err, library.InvalidArgument, library.ErrorDetails{Field: "installs", SkillID: skill.ID})
	result, err := lib.Update(ctx, library.UpdateRequest{Plan: plan.ID, Skills: []library.SkillID{skill.ID}, Installs: []library.ReinstallRequest{{SkillID: skill.ID, Scope: f.project}}})
	if err != nil {
		t.Fatal(err)
	}
	requireRollbackLeavesTheEditedFolder(t, f, result.Record, codexFolder, "edited in codex")
}
