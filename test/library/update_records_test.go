package library_test

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/tiaot33/skills-tui/internal/library"
)

// 按更新计划更新并同时重新安装

func TestUpdateReinstallsCheckedPlacesOnly(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	skill, source := fetchLocalSkill(t, lib, "pdf", "v1")
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	claude := library.GlobalScope(library.AgentClaudeCode)
	codex := library.GlobalScope(library.AgentCodex)
	edited := library.ProjectScope(library.AgentClaudeCode, project.ID)
	directories := map[library.Scope]string{}
	for _, scope := range []library.Scope{claude, codex, edited} {
		if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
		if directories[scope], err = lib.ScopeDirectory(ctx, scope); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(directories[edited], "pdf", "SKILL.md"), []byte("edited by hand"), 0o644)
	writeLocalSkill(t, source, "v2")
	plan, err := lib.PlanUpdate(ctx, skill.CollectionID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := lib.Update(ctx, library.UpdateRequest{
		Plan:     plan.ID,
		Skills:   []library.SkillID{skill.ID},
		Installs: []library.ReinstallRequest{{SkillID: skill.ID, Scope: claude}, {SkillID: skill.ID, Scope: edited}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Record == 0 || len(result.Skills) != 1 || !result.Skills[0].Changed {
		t.Fatalf("Update = %#v", result)
	}
	if len(result.Installs) != 2 || result.Installs[0].Outcome != library.Reinstalled || result.Installs[1].Outcome != library.ReinstallFailed || result.Installs[1].Error.Code != library.AlreadyExists {
		t.Fatalf("installs = %#v; want the checked place reinstalled and the edited one refused", result.Installs)
	}
	requireInstalledFolder(t, directories[claude], "pdf", "v2")
	requireInstalledFolder(t, directories[codex], "pdf", "v1")
	requireSkillFile(t, directories[edited], "pdf", "edited by hand")
	if _, err := lib.Update(ctx, library.UpdateRequest{Plan: plan.ID, Skills: []library.SkillID{skill.ID}}); !library.IsCode(err, library.NotFound) {
		t.Fatalf("reusing the plan = %v; want NotFound", err)
	}
}

func TestUpdateBringsTheWholeCollectionToOneRevision(t *testing.T) {
	ctx := t.Context()
	lib := openTestLibrary(t)
	server := newArchiveServer(t, zipArchive(t, map[string]string{"a/SKILL.md": skillDocument("a", "v1"), "b/SKILL.md": skillDocument("b", "v1"), "c/SKILL.md": skillDocument("c", "v1")}), "")
	skills := fetchArchive(t, lib, server.request())
	server.serve(zipArchive(t, map[string]string{"a/SKILL.md": skillDocument("a", "v2"), "b/SKILL.md": skillDocument("b", "v2"), "c/SKILL.md": skillDocument("c", "v1")}), "")
	plan, err := lib.PlanUpdate(ctx, skills[0].CollectionID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := lib.Update(ctx, library.UpdateRequest{Plan: plan.ID, Skills: []library.SkillID{skills[0].ID, skills[1].ID}})
	if err != nil || result.Revision != plan.Revision {
		t.Fatalf("Update = %#v, %v", result, err)
	}
	for _, skill := range skills {
		got, err := lib.GetSkill(ctx, skill.ID)
		if err != nil || got.Revision != plan.Revision || got.LastUpdateCheck.Status != library.UpdateCurrent {
			t.Fatalf("%s after updating the collection = %#v, %v; want revision %s", skill.Name, got, err, plan.Revision)
		}
	}
	if versions, err := lib.ListVersions(ctx, skills[2].ID); err != nil || len(versions) != 1 {
		t.Fatalf("unchanged skill versions = %#v, %v; want no new version", versions, err)
	}
}

func TestUpdateConflictsOnlyWhenACheckedSkillChangedAgain(t *testing.T) {
	ctx := t.Context()
	lib := openTestLibrary(t)
	server := newArchiveServer(t, zipArchive(t, map[string]string{"a/SKILL.md": skillDocument("a", "v1"), "b/SKILL.md": skillDocument("b", "v1")}), "")
	skills := fetchArchive(t, lib, server.request())
	a, b := skills[0], skills[1]
	server.serve(zipArchive(t, map[string]string{"a/SKILL.md": skillDocument("a", "v2"), "b/SKILL.md": skillDocument("b", "v1")}), "")
	plan, err := lib.PlanUpdate(ctx, a.CollectionID)
	if err != nil {
		t.Fatal(err)
	}
	server.serve(zipArchive(t, map[string]string{"a/SKILL.md": skillDocument("a", "v3"), "b/SKILL.md": skillDocument("b", "v1")}), "")
	_, err = lib.Update(ctx, library.UpdateRequest{Plan: plan.ID, Skills: []library.SkillID{a.ID}})
	requireErrorDetails(t, err, library.Conflict, library.ErrorDetails{SkillID: a.ID})
	if versions, err := lib.ListVersions(ctx, a.ID); err != nil || len(versions) != 1 {
		t.Fatalf("versions after conflict = %#v, %v", versions, err)
	}
	// 只有同一来源里别的技能变了:照常更新,换到来源现在的来源版本。
	plan, err = lib.PlanUpdate(ctx, a.CollectionID)
	if err != nil {
		t.Fatal(err)
	}
	server.serve(zipArchive(t, map[string]string{"a/SKILL.md": skillDocument("a", "v3"), "b/SKILL.md": skillDocument("b", "v2")}), "")
	result, err := lib.Update(ctx, library.UpdateRequest{Plan: plan.ID, Skills: []library.SkillID{a.ID}})
	if err != nil || result.Revision == plan.Revision || string(skillFiles(t, lib, result.Skills[0].Skill)[0].Content) != skillDocument("a", "v3") {
		t.Fatalf("Update when only b changed = %#v, %v; want a at v3 on the newer revision", result, err)
	}
	if got, err := lib.GetSkill(ctx, b.ID); err != nil || got.VersionID != b.VersionID {
		t.Fatalf("b = %#v, %v; want untouched", got, err)
	}
}

func TestUpdateRefusesToWriteIntoAPulledLibrary(t *testing.T) {
	ctx := t.Context()
	newS3Stub(t)
	lib := openTestLibrary(t)
	skill, source := fetchLocalSkill(t, lib, "a", "v1")
	writeLocalSkill(t, source, "v2")
	request := mustPlanUpdate(t, lib, skill.ID)
	push, err := lib.PlanSyncPush(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var expected library.RemoteIdentity
	if push.Remote != nil {
		expected = push.Remote.Identity
	}
	if _, err := lib.SyncPush(ctx, expected); err != nil {
		t.Fatal(err)
	}
	pull, err := lib.PlanSyncPull(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.SyncPull(ctx, pull.Remote.Identity); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Update(ctx, request); !library.IsCode(err, library.Conflict) {
		t.Fatalf("Update after the library was replaced = %v; want Conflict", err)
	}
	if versions, err := lib.ListVersions(ctx, skill.ID); err != nil || len(versions) != 1 {
		t.Fatalf("versions = %#v, %v; want nothing written", versions, err)
	}
}

// 更新记录、单独重试与回滚

func TestRetriedPlaceIsRolledBackWithTheUpdate(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	skill, source := fetchLocalSkill(t, lib, "pdf", "v1")
	projectPath := t.TempDir()
	project, err := lib.RegisterProject(ctx, projectPath)
	if err != nil {
		t.Fatal(err)
	}
	global := library.GlobalScope(library.AgentClaudeCode)
	inProject := library.ProjectScope(library.AgentCodex, project.ID)
	for _, scope := range []library.Scope{global, inProject} {
		if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	globalDir, _ := lib.ScopeDirectory(ctx, global)
	projectDir, _ := lib.ScopeDirectory(ctx, inProject)
	writeLocalSkill(t, source, "v2")
	plan, err := lib.PlanUpdate(ctx, skill.CollectionID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(plan.Installs, func(i library.SkillScopeInspection) bool { return i.Scope == inProject }) {
		t.Fatalf("plan installs = %#v; want the project place", plan.Installs)
	}
	// 项目暂时不在这台机器上:库和全局照样换上,这一处没换上并写明原因。
	if err := os.Rename(projectPath, projectPath+"-away"); err != nil {
		t.Fatal(err)
	}
	installs := []library.ReinstallRequest{{SkillID: skill.ID, Scope: global}, {SkillID: skill.ID, Scope: inProject}}
	result, err := lib.Update(ctx, library.UpdateRequest{Plan: plan.ID, Skills: []library.SkillID{skill.ID}, Installs: installs})
	if err != nil {
		t.Fatal(err)
	}
	if result.Installs[0].Outcome != library.Reinstalled || result.Installs[1].Outcome != library.ReinstallFailed || result.Installs[1].Error.Code != library.FailedPrecondition {
		t.Fatalf("installs = %#v", result.Installs)
	}
	requireInstalledFolder(t, globalDir, "pdf", "v2")
	if err := os.Rename(projectPath+"-away", projectPath); err != nil {
		t.Fatal(err)
	}
	retried, err := lib.RetryUpdateInstalls(ctx, result.Record, installs[1:])
	if err != nil || len(retried) != 1 || retried[0].Outcome != library.Reinstalled {
		t.Fatalf("RetryUpdateInstalls = %#v, %v", retried, err)
	}
	requireInstalledFolder(t, projectDir, "pdf", "v2")

	rollback, err := lib.Rollback(ctx, result.Record)
	if err != nil {
		t.Fatal(err)
	}
	want := []library.RollbackInstall{{SkillID: skill.ID, Scope: global, Outcome: library.RollbackReinstalled}, {SkillID: skill.ID, Scope: inProject, Outcome: library.RollbackReinstalled}}
	if !reflect.DeepEqual(rollback.Installs, want) {
		t.Fatalf("rollback installs = %#v; want %#v", rollback.Installs, want)
	}
	requireInstalledFolder(t, globalDir, "pdf", "v1")
	requireInstalledFolder(t, projectDir, "pdf", "v1")
	got, err := lib.GetSkill(ctx, skill.ID)
	if err != nil || got.VersionID != skill.VersionID {
		t.Fatalf("skill after rollback = %#v, %v; want version %d", got, err, skill.VersionID)
	}
	if _, err := lib.RetryUpdateInstalls(ctx, result.Record, installs[1:]); !library.IsCode(err, library.FailedPrecondition) {
		t.Fatalf("retry after rollback = %v; want FailedPrecondition", err)
	}
}

func TestRollbackRestoresLibraryAndCheckedPlacesAndSkipsTheChange(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	server := newArchiveServer(t, zipArchive(t, map[string]string{"pdf/SKILL.md": skillDocument("pdf", "v1"), "docx/SKILL.md": skillDocument("docx", "v1")}), "")
	skills := fetchArchive(t, lib, server.request())
	docx, pdf := skills[0], skills[1]
	claude := library.GlobalScope(library.AgentClaudeCode)
	codex := library.GlobalScope(library.AgentCodex)
	for _, scope := range []library.Scope{claude, codex} {
		for _, skill := range skills {
			if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
				t.Fatal(err)
			}
		}
	}
	claudeDir, _ := lib.ScopeDirectory(ctx, claude)
	codexDir, _ := lib.ScopeDirectory(ctx, codex)
	server.serve(zipArchive(t, map[string]string{"pdf/SKILL.md": skillDocument("pdf", "v2"), "docx/SKILL.md": skillDocument("docx", "v2")}), "")
	plan, err := lib.PlanUpdate(ctx, pdf.CollectionID)
	if err != nil {
		t.Fatal(err)
	}
	var installs []library.ReinstallRequest
	for _, install := range plan.Installs {
		installs = append(installs, library.ReinstallRequest{SkillID: install.SkillID, Scope: install.Scope})
	}
	result, err := lib.Update(ctx, library.UpdateRequest{Plan: plan.ID, Skills: []library.SkillID{pdf.ID, docx.ID}, Installs: installs})
	if err != nil {
		t.Fatal(err)
	}
	// 之后用户在 Codex 那里改了 pdf:回滚不动它,列出原因。
	writeFile(t, filepath.Join(codexDir, "pdf", "SKILL.md"), []byte("edited by hand"), 0o644)
	rollback, err := lib.Rollback(ctx, result.Record)
	if err != nil {
		t.Fatal(err)
	}
	outcomes := map[string]library.RollbackOutcome{}
	for _, install := range rollback.Installs {
		name := map[library.SkillID]string{pdf.ID: "pdf", docx.ID: "docx"}[install.SkillID]
		outcomes[name+"@"+string(install.Scope.Agent)] = install.Outcome
	}
	wantOutcomes := map[string]library.RollbackOutcome{
		"pdf@claude-code":  library.RollbackReinstalled,
		"docx@claude-code": library.RollbackReinstalled,
		"pdf@codex":        library.RollbackModified,
		"docx@codex":       library.RollbackReinstalled,
	}
	if !reflect.DeepEqual(outcomes, wantOutcomes) {
		t.Fatalf("rollback outcomes = %v; want %v", outcomes, wantOutcomes)
	}
	requireInstalledFolder(t, claudeDir, "pdf", "v1")
	requireSkillFile(t, codexDir, "pdf", "edited by hand")
	for _, skill := range skills {
		got, err := lib.GetSkill(ctx, skill.ID)
		if err != nil || got.VersionID != skill.VersionID || !got.LastUpdateCheck.Skipped || got.LastUpdateCheck.Pending {
			t.Fatalf("%s after rollback = %#v, %v; want the old version and the change skipped", skill.Name, got, err)
		}
		// 检查更新后,回滚掉的改动仍不计入角标。
		if check := checkedSkill(t, lib, skill.ID); !check.Skipped || check.Pending {
			t.Fatalf("%s rechecked after rollback = %#v", skill.Name, check)
		}
	}
	// 想要时在更新计划里找回来再更新。
	again, err := lib.PlanUpdate(ctx, pdf.CollectionID)
	if err != nil || again.Skills[0].Change != library.PlannedChanged || !again.Skills[0].Skipped {
		t.Fatalf("plan after rollback = %#v, %v", again.Skills, err)
	}
	history, err := lib.ListUpdateHistory(ctx, library.UpdateHistoryFilter{CollectionID: pdf.CollectionID})
	if err != nil || len(history) != 2 || history[0].Kind != library.UpdateRecordRollback || history[0].RollsBack != result.Record ||
		history[1].ID != result.Record || history[1].RolledBackBy != history[0].ID || len(history[1].Installs) != 4 {
		t.Fatalf("history = %#v, %v", history, err)
	}
	if _, err := lib.Rollback(ctx, result.Record); !library.IsCode(err, library.FailedPrecondition) {
		t.Fatalf("second rollback = %v; want FailedPrecondition", err)
	}
	if _, err := lib.Rollback(ctx, history[0].ID); !library.IsCode(err, library.InvalidArgument) {
		t.Fatalf("rolling back a rollback = %v; want InvalidArgument", err)
	}
}

func TestRollbackOfAnEarlierUpdateAndOfARemovedSkill(t *testing.T) {
	ctx := t.Context()
	lib := openTestLibrary(t)
	server := newArchiveServer(t, zipArchive(t, map[string]string{"a/SKILL.md": skillDocument("a", "v1"), "b/SKILL.md": skillDocument("b", "v1")}), "")
	skills := fetchArchive(t, lib, server.request())
	a, b := skills[0], skills[1]
	update := func(body string) library.UpdateResult {
		t.Helper()
		server.serve(zipArchive(t, map[string]string{"a/SKILL.md": skillDocument("a", body), "b/SKILL.md": skillDocument("b", body)}), "")
		result, err := lib.Update(ctx, mustPlanUpdate(t, lib, a.ID, b.ID))
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	first := update("v2")
	update("v3")
	if err := lib.RemoveSkill(ctx, b.ID, library.RemoveSkillOptions{}); err != nil {
		t.Fatal(err)
	}
	// 之后又被更新过也一样换回那次之前的版本,不要求先回滚之后那次;已移除的技能列出来。
	rollback, err := lib.Rollback(ctx, first.Record)
	if err != nil {
		t.Fatal(err)
	}
	if len(rollback.Skills) != 1 || rollback.Skills[0].SkillID != a.ID || rollback.Skills[0].ToVersionID != a.VersionID ||
		len(rollback.Removed) != 1 || rollback.Removed[0].SkillID != b.ID || rollback.Removed[0].Name != "b" {
		t.Fatalf("rollback = %#v", rollback)
	}
	if got, err := lib.GetSkill(ctx, a.ID); err != nil || got.VersionID != a.VersionID {
		t.Fatalf("a = %#v, %v; want the version before the first update", got, err)
	}
}

func TestUpdateHistoryByTheSkillShowsCommitMessages(t *testing.T) {
	for _, mode := range gitHubModes {
		t.Run(mode.name, func(t *testing.T) {
			ctx := t.Context()
			repo := writeGitHubRepo(t, map[string]repoFile{
				"pdf/SKILL.md":  {content: []byte(skillDocument("pdf", "v1")), mode: 0o644},
				"docx/SKILL.md": {content: []byte(skillDocument("docx", "v1")), mode: 0o644},
			})
			advance := mode.serve(t, repo)
			lib := openTestLibrary(t)
			preview, err := lib.PreviewFetch(ctx, gitHubRequest())
			if err != nil {
				t.Fatal(err)
			}
			skills := fetchSkills(t, lib, preview, []string{"pdf", "docx"})
			docx, pdf := skills[0], skills[1]
			writeFile(t, filepath.Join(repo, "pdf", "SKILL.md"), []byte(skillDocument("pdf", "v2")), 0o644)
			commitGit(t, repo, "fix pdf rendering")
			advance()
			if _, err := updateSkill(t, lib, ctx, pdf.ID); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(repo, "docx", "SKILL.md"), []byte(skillDocument("docx", "v2")), 0o644)
			commitGit(t, repo, "docx tweaks")
			advance()
			if _, err := updateSkill(t, lib, ctx, docx.ID); err != nil {
				t.Fatal(err)
			}
			history, err := lib.ListUpdateHistory(ctx, library.UpdateHistoryFilter{SkillID: pdf.ID})
			if err != nil || len(history) != 1 || history[0].Kind != library.UpdateRecordUpdate || len(history[0].Skills) != 1 || history[0].Skills[0].SkillID != pdf.ID {
				t.Fatalf("history of pdf = %#v, %v", history, err)
			}
			if len(history[0].Commits) != 1 || history[0].Commits[0].Message != "fix pdf rendering" {
				t.Fatalf("commits = %#v", history[0].Commits)
			}
			all, err := lib.ListUpdateHistory(ctx, library.UpdateHistoryFilter{CollectionID: pdf.CollectionID})
			if err != nil || len(all) != 2 {
				t.Fatalf("collection history = %#v, %v", all, err)
			}
		})
	}
}

// 审查补充

func TestCanceledRollbackContinuesOnTheNextCall(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	skill, source := fetchLocalSkill(t, lib, "pdf", "v1")
	claude := library.GlobalScope(library.AgentClaudeCode)
	codex := library.GlobalScope(library.AgentCodex)
	for _, scope := range []library.Scope{claude, codex} {
		if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	claudeDir, _ := lib.ScopeDirectory(ctx, claude)
	codexDir, _ := lib.ScopeDirectory(ctx, codex)
	writeLocalSkill(t, source, "v2")
	request := mustPlanUpdate(t, lib, skill.ID)
	request.Installs = []library.ReinstallRequest{{SkillID: skill.ID, Scope: claude}, {SkillID: skill.ID, Scope: codex}}
	result, err := lib.Update(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	cancelAfterFirst := cancelWhen{Context: ctx, cond: func() bool {
		content, err := os.ReadFile(filepath.Join(claudeDir, "pdf", "SKILL.md"))
		if err != nil || string(content) != skillDocument("pdf", "v1") {
			return false
		}
		entries, err := os.ReadDir(claudeDir)
		return err == nil && !slices.ContainsFunc(entries, func(entry os.DirEntry) bool { return strings.HasPrefix(entry.Name(), ".") })
	}}
	partial, err := lib.Rollback(cancelAfterFirst, result.Record)
	if !library.IsCode(err, library.Canceled) || len(partial.Installs) != 1 {
		t.Fatalf("canceled rollback = %#v, %v", partial, err)
	}
	requireInstalledFolder(t, codexDir, "pdf", "v2")
	rest, err := lib.Rollback(ctx, result.Record)
	if err != nil || rest.Record != partial.Record || len(rest.Installs) != 2 || rest.Installs[1].Outcome != library.RollbackReinstalled {
		t.Fatalf("continued rollback = %#v, %v", rest, err)
	}
	requireInstalledFolder(t, codexDir, "pdf", "v1")
	if _, err := lib.Rollback(ctx, result.Record); !library.IsCode(err, library.FailedPrecondition) {
		t.Fatalf("rollback after finishing = %v; want FailedPrecondition", err)
	}
	if versions, err := lib.ListVersions(ctx, skill.ID); err != nil || len(versions) != 2 {
		t.Fatalf("versions = %#v, %v", versions, err)
	}
}

func TestFailedRetryKeepsAnEarlierSuccessForRollback(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	skill, source := fetchLocalSkill(t, lib, "pdf", "v1")
	global := library.GlobalScope(library.AgentCodex)
	if err := lib.Install(ctx, skill.ID, global, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	directory, _ := lib.ScopeDirectory(ctx, global)
	writeLocalSkill(t, source, "v2")
	request := mustPlanUpdate(t, lib, skill.ID)
	request.Installs = []library.ReinstallRequest{{SkillID: skill.ID, Scope: global}}
	first, err := lib.Update(ctx, request)
	if err != nil || first.Installs[0].Outcome != library.Reinstalled {
		t.Fatalf("first update = %#v, %v", first, err)
	}
	// 之后的更新不动这一处,再重试第一次更新:技能已换了版本,这一处 Conflict,但「第一次更新装过这里」不丢。
	writeLocalSkill(t, source, "v3")
	if _, err := updateSkill(t, lib, ctx, skill.ID); err != nil {
		t.Fatal(err)
	}
	retried, err := lib.RetryUpdateInstalls(ctx, first.Record, request.Installs)
	if err != nil || retried[0].Outcome != library.ReinstallFailed || retried[0].Error.Code != library.Conflict {
		t.Fatalf("retry = %#v, %v", retried, err)
	}
	rollback, err := lib.Rollback(ctx, first.Record)
	if err != nil || len(rollback.Installs) != 1 || rollback.Installs[0].Outcome != library.RollbackReinstalled {
		t.Fatalf("rollback = %#v, %v", rollback, err)
	}
	requireInstalledFolder(t, directory, "pdf", "v1")
}

func TestRollbackLeavesAProjectRegisteredAfterTheUpdate(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	skill, source := fetchLocalSkill(t, lib, "pdf", "v1")
	old, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	scope := library.ProjectScope(library.AgentCodex, old.ID)
	if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	writeLocalSkill(t, source, "v2")
	request := mustPlanUpdate(t, lib, skill.ID)
	request.Installs = []library.ReinstallRequest{{SkillID: skill.ID, Scope: scope}}
	result, err := lib.Update(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.UnregisterProject(ctx, old.ID, library.UnregisterProjectOptions{}); err != nil {
		t.Fatal(err)
	}
	fresh, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil || fresh.ID == old.ID {
		t.Fatalf("new project = %#v, %v; must not reuse ID %d", fresh, err, old.ID)
	}
	freshScope := library.ProjectScope(library.AgentCodex, fresh.ID)
	if err := lib.Install(ctx, skill.ID, freshScope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	directory, _ := lib.ScopeDirectory(ctx, freshScope)
	rollback, err := lib.Rollback(ctx, result.Record)
	if err != nil || len(rollback.Installs) != 1 || rollback.Installs[0].Outcome != library.RollbackUninstalled {
		t.Fatalf("rollback = %#v, %v", rollback, err)
	}
	requireInstalledFolder(t, directory, "pdf", "v2")
}
