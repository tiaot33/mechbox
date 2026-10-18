package library_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/tiaot33/mechbox/internal/library"
)

// 更新计划

func TestUpdatePlanShowsRisksCommitsDiffAndSourceContent(t *testing.T) {
	for _, mode := range gitHubModes {
		t.Run(mode.name, func(t *testing.T) {
			ctx := t.Context()
			repo := writeGitHubRepo(t, map[string]repoFile{
				"pdf/SKILL.md":       {content: []byte(skillDocument("pdf", "v1")), mode: 0o644},
				"pdf/scripts/old.sh": {content: []byte("#!/bin/sh\necho old\n"), mode: 0o755},
				"pdf/tool.txt":       {content: []byte("plain"), mode: 0o644},
				"docx/SKILL.md":      {content: []byte(skillDocument("docx", "v1")), mode: 0o644},
				"notes/SKILL.md":     {content: []byte(skillDocument("notes", "v1")), mode: 0o644},
			})
			advance := mode.serve(t, repo)
			lib := openTestLibrary(t)
			preview, err := lib.PreviewFetch(ctx, gitHubRequest())
			if err != nil {
				t.Fatal(err)
			}
			byName := map[string]library.Skill{}
			for _, skill := range fetchSkills(t, lib, preview, []string{"pdf", "docx", "notes"}) {
				byName[skill.Name] = skill
			}
			pdf, docx, notes := byName["pdf"], byName["docx"], byName["notes"]

			writeFile(t, filepath.Join(repo, "pdf", "scripts", "old.sh"), []byte("#!/bin/sh\necho changed\n"), 0o755)
			writeFile(t, filepath.Join(repo, "pdf", "scripts", "new.sh"), []byte("#!/bin/sh\necho new\n"), 0o755)
			writeFile(t, filepath.Join(repo, "pdf", "helper.py"), []byte("print('hi')\n"), 0o644)
			writeFile(t, filepath.Join(repo, "pdf", "README.md"), []byte("docs"), 0o644)
			if err := os.Chmod(filepath.Join(repo, "pdf", "tool.txt"), 0o755); err != nil {
				t.Fatal(err)
			}
			commitGit(t, repo, "change pdf")
			advance()
			writeFile(t, filepath.Join(repo, "docx", "SKILL.md"), []byte(skillDocument("docx", "v2")), 0o644)
			commitGit(t, repo, "change docx")
			advance()

			plan, err := lib.PlanUpdate(ctx, pdf.CollectionID)
			if err != nil {
				t.Fatal(err)
			}
			head := gitRevParse(t, repo, "HEAD")
			if plan.ID == "" || plan.CollectionID != pdf.CollectionID || plan.Revision != head || plan.Source.Label != "owner/repo" {
				t.Fatalf("plan header = %#v", plan)
			}
			var messages []string
			for _, commit := range plan.Commits {
				messages = append(messages, commit.Message)
			}
			if !reflect.DeepEqual(messages, []string{"change docx", "change pdf"}) || plan.Commits[0].SHA != head {
				t.Fatalf("commits = %#v; want newest first since the oldest revision", plan.Commits)
			}
			changes := map[string]library.PlannedChange{}
			for _, planned := range plan.Skills {
				changes[planned.Skill.Name] = planned.Change
			}
			if !reflect.DeepEqual(changes, map[string]library.PlannedChange{"pdf": library.PlannedChanged, "docx": library.PlannedChanged, "notes": library.PlannedUnchanged}) {
				t.Fatalf("changes = %v", changes)
			}
			planned := plan.Skills[slices.IndexFunc(plan.Skills, func(p library.PlannedSkill) bool { return p.Skill.ID == pdf.ID })]
			wantRisks := []library.UpdateRisk{
				{Kind: library.RiskScriptAdded, Path: "helper.py"},
				{Kind: library.RiskExecutableAdded, Path: "scripts/new.sh"},
				{Kind: library.RiskScriptChanged, Path: "scripts/old.sh"},
				{Kind: library.RiskExecutableAdded, Path: "tool.txt"},
			}
			if !reflect.DeepEqual(planned.Risks, wantRisks) {
				t.Fatalf("risks = %#v; want %#v", planned.Risks, wantRisks)
			}
			wantDiff := &library.VersionDiff{Changed: []string{"scripts/old.sh", "tool.txt"}, Added: []string{"README.md", "helper.py", "scripts/new.sh"}, Deleted: []string{}}
			if !reflect.DeepEqual(planned.Diff, wantDiff) {
				t.Fatalf("diff = %#v; want %#v", planned.Diff, wantDiff)
			}
			for _, other := range plan.Skills {
				if other.Skill.ID == docx.ID && (other.Risks != nil || other.Diff == nil) || other.Skill.ID == notes.ID && (other.Risks != nil || other.Diff != nil) {
					t.Fatalf("planned %s = %#v", other.Skill.Name, other)
				}
			}
			file, err := lib.GetUpdatePlanFile(ctx, plan.ID, pdf.ID, "scripts/new.sh")
			if err != nil || string(file.Content) != "#!/bin/sh\necho new\n" || file.Mode != library.ModeExecutable {
				t.Fatalf("GetUpdatePlanFile = %#v, %v", file, err)
			}
			if _, err := lib.GetUpdatePlanFile(ctx, plan.ID, pdf.ID, "missing"); !library.IsCode(err, library.NotFound) {
				t.Fatalf("missing plan file = %v", err)
			}
			if _, err := lib.GetUpdatePlanFile(ctx, "unknown", pdf.ID, "SKILL.md"); !library.IsCode(err, library.NotFound) {
				t.Fatalf("unknown plan = %v", err)
			}
			// 更新计划只读:库里的版本不变,只和检查更新一样写下结论。
			after, err := lib.GetSkill(ctx, pdf.ID)
			if err != nil || after.LastUpdateCheck == nil || after.LastUpdateCheck.Status != library.UpdateAvailable || !after.LastUpdateCheck.Pending || after.LastUpdateCheck.Revision != head {
				t.Fatalf("conclusion after plan = %#v, %v", after.LastUpdateCheck, err)
			}
			after.LastUpdateCheck = nil
			if !reflect.DeepEqual(after, pdf) {
				t.Fatalf("plan changed skill: %#v", after)
			}
		})
	}
}

func TestUpdatePlanListsEachInstallOfChangedSkills(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	root := t.TempDir()
	for _, name := range []string{"pdf", "docx", "notes", "gone"} {
		writeLocalSkill(t, filepath.Join(root, name), "v1")
	}
	preview, err := lib.PreviewFetch(ctx, localRequest(root))
	if err != nil {
		t.Fatal(err)
	}
	skills := fetchSkills(t, lib, preview, candidateNames(preview))
	byName := map[string]library.Skill{}
	for _, skill := range skills {
		byName[skill.Name] = skill
	}
	global := library.GlobalScope(library.AgentClaudeCode)
	for _, name := range []string{"pdf", "docx", "notes", "gone"} {
		if err := lib.Install(ctx, byName[name].ID, global, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	directory, err := lib.ScopeDirectory(ctx, global)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(directory, "pdf", "SKILL.md"), []byte("edited by hand"), 0o644)
	writeLocalSkill(t, filepath.Join(root, "pdf"), "v2")
	writeLocalSkill(t, filepath.Join(root, "docx"), "v2")
	if err := os.RemoveAll(filepath.Join(root, "gone")); err != nil {
		t.Fatal(err)
	}
	plan, err := lib.PlanUpdate(ctx, byName["pdf"].CollectionID)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, install := range plan.Installs {
		got = append(got, fmt.Sprintf("%d %s %s", install.SkillID, install.Folder, install.Status))
	}
	want := []string{
		fmt.Sprintf("%d %s %s", byName["pdf"].ID, filepath.Join(directory, "pdf"), library.SkillScopeModified),
		fmt.Sprintf("%d %s %s", byName["docx"].ID, filepath.Join(directory, "docx"), library.SkillScopeCurrent),
	}
	slices.Sort(got)
	slices.Sort(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("installs = %v; want %v", got, want)
	}
	for _, planned := range plan.Skills {
		if planned.Skill.Name == "gone" && planned.Change != library.PlannedDeleted {
			t.Fatalf("gone = %#v; want deleted", planned)
		}
	}
	if plan.Commits != nil || plan.Revision != "" {
		t.Fatalf("local plan = %#v; want no commits and no revision", plan)
	}
	if _, err := lib.GetUpdatePlanFile(ctx, plan.ID, byName["gone"].ID, "SKILL.md"); !library.IsCode(err, library.NotFound) {
		t.Fatalf("deleted skill plan file = %v", err)
	}
	if _, err := lib.PlanUpdate(ctx, 9999); !library.IsCode(err, library.NotFound) {
		t.Fatalf("missing collection = %v", err)
	}
	if _, err := lib.PlanUpdate(ctx, 0); !library.IsCode(err, library.InvalidArgument) {
		t.Fatalf("zero collection = %v", err)
	}
}

// 跳过

func TestSkipStopsCountingUntilTheSkillChangesAgain(t *testing.T) {
	ctx := t.Context()
	lib := openTestLibrary(t)
	server := newArchiveServer(t, zipArchive(t, map[string]string{"a/SKILL.md": skillDocument("a", "v1"), "b/SKILL.md": skillDocument("b", "v1")}), "")
	preview, err := lib.PreviewFetch(ctx, server.request())
	if err != nil {
		t.Fatal(err)
	}
	skills := fetchSkills(t, lib, preview, []string{"a", "b"})
	a, b := skills[0], skills[1]
	serveSkills := func(aBody, bBody string) {
		server.serve(zipArchive(t, map[string]string{"a/SKILL.md": skillDocument("a", aBody), "b/SKILL.md": skillDocument("b", bBody)}), "")
	}
	serveSkills("v2", "v1")
	if check := checkedSkill(t, lib, a.ID); !check.Pending {
		t.Fatalf("a before skip = %#v; want pending", check)
	}
	plan, err := lib.PlanUpdate(ctx, a.CollectionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.SkipUpdate(ctx, library.SkipRequest{Plan: plan.ID, Skills: []library.SkillID{b.ID}}); !library.IsCode(err, library.InvalidArgument) {
		t.Fatalf("skip unchanged skill = %v; want InvalidArgument", err)
	}
	if err := lib.SkipUpdate(ctx, library.SkipRequest{Plan: plan.ID, Skills: []library.SkillID{a.ID}, WholeRevision: true}); !library.IsCode(err, library.InvalidArgument) {
		t.Fatalf("skip with both skills and whole revision = %v; want InvalidArgument", err)
	}
	if err := lib.SkipUpdate(ctx, library.SkipRequest{Plan: "unknown", Skills: []library.SkillID{a.ID}}); !library.IsCode(err, library.NotFound) {
		t.Fatalf("skip with unknown plan = %v; want NotFound", err)
	}
	if err := lib.SkipUpdate(ctx, library.SkipRequest{Plan: plan.ID, Skills: []library.SkillID{a.ID}}); err != nil {
		t.Fatal(err)
	}
	// 跳过后马上不计入,不用再检查一次;版本不变。
	if skill, err := lib.GetSkill(ctx, a.ID); err != nil || !skill.LastUpdateCheck.Skipped || skill.LastUpdateCheck.Pending || skill.VersionID != a.VersionID {
		t.Fatalf("a after skip = %#v, %v", skill, err)
	}
	if check := checkedSkill(t, lib, a.ID); check.Status != library.UpdateAvailable || !check.Skipped || check.Pending {
		t.Fatalf("a rechecked after skip = %#v; want skipped", check)
	}
	again, err := lib.PlanUpdate(ctx, a.CollectionID)
	if err != nil || !again.Skills[0].Skipped || again.Skills[0].Change != library.PlannedChanged {
		t.Fatalf("plan after skip = %#v, %v; want a listed as skipped", again.Skills, err)
	}
	// 同一来源里别的技能变了,不影响跳过。
	serveSkills("v2", "v2")
	if check := checkedSkill(t, lib, a.ID); !check.Skipped || check.Pending {
		t.Fatalf("a after b changed = %#v; want still skipped", check)
	}
	if check := checkedSkill(t, lib, b.ID); !check.Pending {
		t.Fatalf("b = %#v; want pending", check)
	}
	// 这个技能又变了,重新计入。
	serveSkills("v3", "v2")
	if check := checkedSkill(t, lib, a.ID); check.Skipped || !check.Pending {
		t.Fatalf("a changed again = %#v; want pending", check)
	}
	// 跳过整个来源版本:计划里有变化的技能全部跳过。
	whole, err := lib.PlanUpdate(ctx, a.CollectionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.SkipUpdate(ctx, library.SkipRequest{Plan: whole.ID, WholeRevision: true}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []library.SkillID{a.ID, b.ID} {
		if check := checkedSkill(t, lib, id); !check.Skipped || check.Pending {
			t.Fatalf("skill %d after skipping the whole revision = %#v", id, check)
		}
	}
}

// 审查补充:计划之后的写回要认得出技能和库已经换了。

func TestSkipRejectsRemovedSkillAndReplacedLibrary(t *testing.T) {
	ctx := t.Context()
	newS3Stub(t)
	lib := openTestLibrary(t)
	a, aSource := fetchLocalSkill(t, lib, "a", "v1")
	writeLocalSkill(t, aSource, "v2")
	plan, err := lib.PlanUpdate(ctx, a.CollectionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.RemoveSkill(ctx, a.ID, library.RemoveSkillOptions{}); err != nil {
		t.Fatal(err)
	}
	b, bSource := fetchLocalSkill(t, lib, "b", "v1")
	if b.ID == a.ID {
		t.Fatalf("b reused the ID %d of a removed skill", b.ID)
	}
	if err := lib.SkipUpdate(ctx, library.SkipRequest{Plan: plan.ID, Skills: []library.SkillID{a.ID}}); !library.IsCode(err, library.NotFound) {
		t.Fatalf("skip for a removed skill = %v; want NotFound", err)
	}
	writeLocalSkill(t, bSource, "v2")
	plan, err = lib.PlanUpdate(ctx, b.CollectionID)
	if err != nil {
		t.Fatal(err)
	}
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
	if err := lib.SkipUpdate(ctx, library.SkipRequest{Plan: plan.ID, Skills: []library.SkillID{b.ID}}); !library.IsCode(err, library.Conflict) {
		t.Fatalf("skip after the library was replaced = %v; want Conflict", err)
	}
}
