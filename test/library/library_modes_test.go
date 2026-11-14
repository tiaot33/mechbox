//go:build !windows

package library_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/tiaot33/mechbox/internal/library"
)

// Windows 本机的目录读不出可执行位。测试里 MECHBOX_TEST_NO_FILE_MODES 让本机的目录也这样:
// 读出的文件一律是普通文件,和在 Windows 上一样;库写出去的文件照样带可执行位。

func withoutFileModes(t *testing.T) {
	t.Helper()
	t.Setenv("MECHBOX_TEST_NO_FILE_MODES", "1")
}

func installExample(t *testing.T, lib *library.Library, scopes ...library.Scope) library.Skill {
	t.Helper()
	skill := fetchExampleSkill(t, lib, "alpha")
	for _, scope := range scopes {
		if err := lib.Install(t.Context(), skill.ID, scope, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	return skill
}

func requireExecutableScript(t *testing.T, lib *library.Library, version library.VersionID) {
	t.Helper()
	file, err := lib.GetVersionFile(t.Context(), version, "scripts/run.sh")
	if err != nil || file.Mode != library.ModeExecutable {
		t.Fatalf("scripts/run.sh in version %d = %+v, %v; want it executable", version, file, err)
	}
}

func TestInspectWithoutFileModesComparesOnlyContent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	claude := library.GlobalScope(library.AgentClaudeCode)
	skill := installExample(t, lib, claude)
	withoutFileModes(t)

	requireScopeStatus(t, lib, skill.ID, claude, library.SkillScopeCurrent)
	writeFile(t, filepath.Join(filepath.Join(scopeDirectory(t, lib, claude), "alpha"), "SKILL.md"), []byte(skillDocument("alpha", "edited")), 0o644)
	requireScopeStatus(t, lib, skill.ID, claude, library.SkillScopeModified)
}

func TestUpdateFromScopeWithoutFileModesKeepsExecutableBitAndRollsBack(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	ctx := t.Context()
	claude, codex := library.GlobalScope(library.AgentClaudeCode), library.GlobalScope(library.AgentCodex)
	skill := installExample(t, lib, claude, codex)
	withoutFileModes(t)

	writeFile(t, filepath.Join(filepath.Join(scopeDirectory(t, lib, claude), "alpha"), "SKILL.md"), []byte(skillDocument("alpha", "edited")), 0o644)
	plan, err := lib.PlanUpdateFromScope(ctx, skill.ID, claude)
	if err != nil {
		t.Fatal(err)
	}
	if diff := plan.Skills[0].Diff; diff == nil || !slices.Equal(diff.Changed, []string{"SKILL.md"}) || len(diff.Added)+len(diff.Deleted) != 0 {
		t.Fatalf("diff = %#v; want only SKILL.md, not the script whose mode can't be read", diff)
	}
	result, err := lib.Update(ctx, library.UpdateRequest{Plan: plan.ID, Skills: []library.SkillID{skill.ID}, Installs: []library.ReinstallRequest{{SkillID: skill.ID, Scope: codex}}})
	if err != nil {
		t.Fatal(err)
	}
	updated := result.Skills[0].Skill.VersionID
	requireExecutableScript(t, lib, updated)
	if info, err := os.Stat(filepath.Join(filepath.Join(scopeDirectory(t, lib, codex), "alpha"), "scripts", "run.sh")); err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("codex script = %v, %v; want it written executable", info, err)
	}

	rollback, err := lib.Rollback(ctx, result.Record)
	if err != nil {
		t.Fatal(err)
	}
	for _, install := range rollback.Installs {
		if install.Outcome != library.RollbackReinstalled {
			t.Fatalf("rollback installs = %#v; want every place put back, none taken as modified", rollback.Installs)
		}
	}
	// 回滚只换回那次更新装过的地方;取内容的那一处留着那份修改,是库里的旧版本,不算用户改过。
	requireScopeStatus(t, lib, skill.ID, claude, library.SkillScopeStale)
	requireScopeStatus(t, lib, skill.ID, codex, library.SkillScopeCurrent)
}

func TestUnmanagedCopyWithoutFileModesIsRecognized(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	ctx := t.Context()
	claude, codex := library.GlobalScope(library.AgentClaudeCode), library.GlobalScope(library.AgentCodex)
	skill := installExample(t, lib, claude)
	copied := filepath.Join(scopeDirectory(t, lib, codex), "alpha")
	writeFile(t, filepath.Join(copied, "SKILL.md"), []byte(skillDocument("alpha", "# alpha\n")), 0o644)
	writeFile(t, filepath.Join(copied, "scripts", "run.sh"), []byte("#!/bin/sh\n"), 0o644)
	withoutFileModes(t)

	if found := unmanagedIn(t, lib, codex, "alpha"); found.SkillID != skill.ID {
		t.Fatalf("unmanaged = %#v; want the codex copy recognized as skill %d", found, skill.ID)
	}
	fetched, err := lib.FetchUnmanaged(ctx, library.FetchUnmanagedRequest{Scope: codex, Name: "alpha"})
	if err != nil || !fetched.Matched || fetched.Skill.ID != skill.ID {
		t.Fatalf("FetchUnmanaged = %#v, %v; want it recorded as an install of skill %d", fetched, err, skill.ID)
	}
}

func TestFetchUnmanagedWithSourceWithoutFileModesTakesSourceModes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	ctx := t.Context()
	source := filepath.Join(t.TempDir(), "alpha")
	writeLocalSkill(t, source, "# alpha\n")
	writeFile(t, filepath.Join(source, "scripts", "run.sh"), []byte("#!/bin/sh\n"), 0o755)
	claude := library.GlobalScope(library.AgentClaudeCode)
	copied := filepath.Join(scopeDirectory(t, lib, claude), "alpha")
	writeLocalSkill(t, copied, "# alpha\n")
	writeFile(t, filepath.Join(copied, "scripts", "run.sh"), []byte("#!/bin/sh\n"), 0o644)
	withoutFileModes(t)

	fetched, err := lib.FetchUnmanaged(ctx, library.FetchUnmanagedRequest{Scope: claude, Name: "alpha", SourceInput: source})
	if err != nil {
		t.Fatal(err)
	}
	versions, err := lib.ListVersions(ctx, fetched.Skill.ID)
	if err != nil || len(versions) != 1 || versions[0].MaybeEdited {
		t.Fatalf("versions = %#v, %v; want the source's version, not a possibly edited copy", versions, err)
	}
	requireExecutableScript(t, lib, fetched.Skill.VersionID)
	requireCheck(t, lib, fetched.Skill.ID, library.UpdateCurrent, false, false)
}

func TestSourceLatestWithoutFileModesIsRecognized(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	ctx := t.Context()
	source := filepath.Join(t.TempDir(), "alpha")
	writeLocalSkill(t, source, "# alpha\n")
	writeFile(t, filepath.Join(source, "scripts", "run.sh"), []byte("#!/bin/sh\n"), 0o755)
	skill, err := fetchLocalDir(ctx, lib, source)
	if err != nil {
		t.Fatal(err)
	}
	claude, codex := library.GlobalScope(library.AgentClaudeCode), library.GlobalScope(library.AgentCodex)
	if err := lib.Install(ctx, skill.ID, claude, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	writeLocalSkill(t, source, "# alpha, newer\n")
	requireCheck(t, lib, skill.ID, library.UpdateAvailable, false, false)
	for _, folder := range []string{filepath.Join(scopeDirectory(t, lib, claude), "alpha"), filepath.Join(scopeDirectory(t, lib, codex), "alpha")} {
		writeLocalSkill(t, folder, "# alpha, newer\n")
		writeFile(t, filepath.Join(folder, "scripts", "run.sh"), []byte("#!/bin/sh\n"), 0o644)
	}
	withoutFileModes(t)

	if inspection := requireScopeStatus(t, lib, skill.ID, claude, library.SkillScopeModified); !inspection.SourceLatest {
		t.Fatalf("inspection = %#v; want the place updated outside the app recognized as the source's latest", inspection)
	}
	if found := unmanagedIn(t, lib, codex, "alpha"); found.SourceLatestOf != skill.ID {
		t.Fatalf("unmanaged = %#v; want the codex copy recognized as the source's latest of skill %d", found, skill.ID)
	}
}

// Windows 本机的一份读不出可执行位,只按内容认作库里的技能;WSL 里同样内容、可执行位和库里不同的一份不是那个技能。
// 整批入库的计划不能借着前一份把后一份也并进「库里的技能」,入库时也不能因此多出一个技能。
func TestPlanFetchUnmanagedKeepsWSLCopyWithOtherModesApart(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MECHBOX_TEST_WSL_SHELL", "sh")
	lib := openTestLibrary(t)
	ctx := t.Context()
	claude, codex := library.GlobalScope(library.AgentClaudeCode), library.GlobalScope(library.AgentCodex)
	skill := installExample(t, lib, claude)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, folder := range []string{filepath.Join(scopeDirectory(t, lib, codex), "alpha"), filepath.Join(root, ".claude", "skills", "alpha")} {
		writeLocalSkill(t, folder, "# alpha\n")
		writeFile(t, filepath.Join(folder, "scripts", "run.sh"), []byte("#!/bin/sh\n"), 0o644)
	}
	project, err := lib.RegisterProject(ctx, wslPath(t, root))
	if err != nil {
		t.Fatal(err)
	}
	inProject := library.ProjectScope(library.AgentClaudeCode, project.ID)
	withoutFileModes(t)

	plan, err := lib.PlanFetchUnmanaged(ctx, library.PlanFetchUnmanagedRequest{Scopes: []library.Scope{codex, inProject}})
	if err != nil {
		t.Fatal(err)
	}
	recognized := map[library.Scope]library.SkillID{}
	for _, planned := range plan {
		for _, folder := range planned.Folders {
			recognized[folder.Scope] = planned.SkillID
		}
		if len(planned.Folders) != 1 {
			t.Fatalf("plan = %#v; want the Windows copy and the WSL copy with other modes apart", plan)
		}
	}
	if recognized[codex] != skill.ID || recognized[inProject] != 0 {
		t.Fatalf("recognized = %v; want only the Windows copy taken as skill %d", recognized, skill.ID)
	}
	results, err := lib.FetchUnmanagedSkills(ctx, library.FetchUnmanagedSkillsRequest{Skills: []library.UnmanagedFetchItem{{Folders: []library.FolderInScope{{Scope: codex, Name: "alpha"}}}}})
	if err != nil || len(results) != 1 || results[0].Error != nil || !results[0].Matched || results[0].Skill.ID != skill.ID {
		t.Fatalf("FetchUnmanagedSkills = %#v, %v; want the Windows copy recorded as skill %d", results, err, skill.ID)
	}
	if skills, err := lib.ListSkills(ctx); err != nil || len(skills) != 1 {
		t.Fatalf("skills = %#v, %v; want no new skill", skills, err)
	}
}
