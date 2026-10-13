package library_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/tiaot33/skills-tui/internal/library"
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
			// 更新计划只读:库里的版本与检查结论都不变。
			after, err := lib.GetSkill(ctx, pdf.ID)
			if err != nil || !reflect.DeepEqual(after, pdf) {
				t.Fatalf("plan changed skill: %#v, %v", after, err)
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

// 计入角标

func TestPendingCountsChangesAndDeletionsButNotLocalSources(t *testing.T) {
	ctx := t.Context()
	lib := openTestLibrary(t)
	local, source := fetchLocalSkill(t, lib, "local", "v1")
	writeLocalSkill(t, source, "v2")
	if check := checkedSkill(t, lib, local.ID); check.Status != library.UpdateAvailable || check.Pending || check.Revision != "" {
		t.Fatalf("local check = %#v; want available but not pending", check)
	}

	archive := zipArchive(t, map[string]string{"a/SKILL.md": skillDocument("a", "v1"), "b/SKILL.md": skillDocument("b", "v1")})
	server := newArchiveServer(t, archive, `"v1"`)
	preview, err := lib.PreviewFetch(ctx, server.request())
	if err != nil {
		t.Fatal(err)
	}
	skills := fetchSkills(t, lib, preview, []string{"a", "b"})
	// 来源没变(304)时,结论也带上来源版本。
	if check := checkedSkill(t, lib, skills[0].ID); check.Status != library.UpdateCurrent || check.Pending || check.Revision != preview.Revision {
		t.Fatalf("unchanged url check = %#v; want current at %s", check, preview.Revision)
	}
	changed := zipArchive(t, map[string]string{"a/SKILL.md": skillDocument("a", "v2")})
	server.serve(changed, `"v2"`)
	sum := sha256.Sum256(changed)
	revision := hex.EncodeToString(sum[:])
	if check := checkedSkill(t, lib, skills[0].ID); check.Status != library.UpdateAvailable || !check.Pending || check.Skipped || check.Revision != revision {
		t.Fatalf("changed url check = %#v; want pending at %s", check, revision)
	}
	if check := checkedSkill(t, lib, skills[1].ID); check.Status != library.UpdateDeleted || !check.Pending || check.Revision != revision {
		t.Fatalf("deleted url check = %#v; want pending", check)
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

// 继续保留

func TestKeptDeletedSkillStopsCountingAndSurvivesRestart(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "library.db")
	lib, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	server := newArchiveServer(t, zipArchive(t, map[string]string{"a/SKILL.md": skillDocument("a", "v1"), "b/SKILL.md": skillDocument("b", "v1")}), "")
	preview, err := lib.PreviewFetch(ctx, server.request())
	if err != nil {
		t.Fatal(err)
	}
	skills := fetchSkills(t, lib, preview, []string{"a", "b"})
	a := skills[0]
	if _, err := lib.KeepDeletedSkill(ctx, a.ID); !library.IsCode(err, library.FailedPrecondition) {
		t.Fatalf("keep a skill not deleted = %v; want FailedPrecondition", err)
	}
	server.serve(zipArchive(t, map[string]string{"b/SKILL.md": skillDocument("b", "v1")}), "")
	if check := checkedSkill(t, lib, a.ID); check.Status != library.UpdateDeleted || !check.Pending {
		t.Fatalf("deleted = %#v; want pending", check)
	}
	kept, err := lib.KeepDeletedSkill(ctx, a.ID)
	if err != nil || !kept.KeptDeleted || kept.LastUpdateCheck.Pending {
		t.Fatalf("KeepDeletedSkill = %#v, %v", kept, err)
	}
	if err := lib.Close(); err != nil {
		t.Fatal(err)
	}
	lib = openTestLibraryAt(t, path)
	if skill, err := lib.GetSkill(ctx, a.ID); err != nil || !skill.KeptDeleted || skill.LastUpdateCheck.Pending {
		t.Fatalf("after restart = %#v, %v", skill, err)
	}
	if check := checkedSkill(t, lib, a.ID); check.Status != library.UpdateDeleted || check.Pending {
		t.Fatalf("rechecked kept skill = %#v; want deleted, not pending", check)
	}
	// 来源里又有了这个技能,标记自动失效。
	server.serve(zipArchive(t, map[string]string{"a/SKILL.md": skillDocument("a", "v2"), "b/SKILL.md": skillDocument("b", "v1")}), "")
	if check := checkedSkill(t, lib, a.ID); check.Status != library.UpdateAvailable || !check.Pending {
		t.Fatalf("restored = %#v; want pending", check)
	}
	if skill, err := lib.GetSkill(ctx, a.ID); err != nil || skill.KeptDeleted {
		t.Fatalf("mark after restore = %#v, %v; want cleared", skill, err)
	}
}

// 新技能

func TestNewSkillsAreOnlyThoseAppearingAfterFetch(t *testing.T) {
	for _, mode := range gitHubModes {
		for _, entry := range []struct {
			name    string
			request func() library.PreviewFetchRequest
		}{
			{"skills.sh", func() library.PreviewFetchRequest {
				return library.PreviewFetchRequest{GitHub: &library.GitHubInput{Input: "owner/repo@first", Ref: "main"}}
			}},
			{"subpath", func() library.PreviewFetchRequest {
				return library.PreviewFetchRequest{GitHub: &library.GitHubInput{Input: "owner/repo", Ref: "main", Subpath: "skills"}}
			}},
		} {
			t.Run(mode.name+"/"+entry.name, func(t *testing.T) {
				ctx := t.Context()
				repo := writeGitHubRepo(t, map[string]repoFile{
					"skills/first/SKILL.md":  {content: []byte(skillDocument("first", "v1")), mode: 0o644},
					"skills/second/SKILL.md": {content: []byte(skillDocument("second", "v1")), mode: 0o644},
					"other/SKILL.md":         {content: []byte(skillDocument("other", "v1")), mode: 0o644},
				})
				advance := mode.serve(t, repo)
				path := filepath.Join(t.TempDir(), "library.db")
				lib := openTestLibraryAt(t, path)
				preview, err := lib.PreviewFetch(ctx, entry.request())
				if err != nil {
					t.Fatal(err)
				}
				first := fetchSkills(t, lib, preview, []string{"first"})[0]
				// 仓库里原有、入库时没选的技能不算新技能。
				writeFile(t, filepath.Join(repo, "skills", "first", "notes.md"), []byte("change"), 0o644)
				commitGit(t, repo, "change first")
				advance()
				checkedSkill(t, lib, first.ID)
				requireNewSkills(t, lib, first.CollectionID, nil)

				writeFile(t, filepath.Join(repo, "skills", "third", "SKILL.md"), []byte(skillDocument("third", "v1")), 0o644)
				commitGit(t, repo, "add third")
				advance()
				if check := checkedSkill(t, lib, first.ID); !check.Pending {
					t.Fatalf("first = %#v; want pending for its own change only", check)
				}
				requireNewSkills(t, lib, first.CollectionID, []string{"third"})
				if err := lib.Close(); err != nil {
					t.Fatal(err)
				}
				lib = openTestLibraryAt(t, path)
				requireNewSkills(t, lib, first.CollectionID, []string{"third"})
				requireCandidateFlags(t, lib, first.CollectionID, map[string]bool{"first": false, "second": false, "third": true, "other": false})

				if err := lib.MarkNotInterested(ctx, first.CollectionID, []string{"third"}); err != nil {
					t.Fatal(err)
				}
				requireNewSkills(t, lib, first.CollectionID, nil)
				requireCandidateFlags(t, lib, first.CollectionID, map[string]bool{"first": false, "second": false, "third": false, "other": false})
			})
		}
	}
}

func TestMarkNotInterestedValidatesInput(t *testing.T) {
	ctx := t.Context()
	lib := openTestLibrary(t)
	skill := fetchNamedSkill(t, lib, "demo")
	if err := lib.MarkNotInterested(ctx, 9999, []string{"x"}); !library.IsCode(err, library.NotFound) {
		t.Fatalf("missing collection = %v", err)
	}
	if err := lib.MarkNotInterested(ctx, skill.CollectionID, nil); !library.IsCode(err, library.InvalidArgument) {
		t.Fatalf("no names = %v", err)
	}
	if err := lib.MarkNotInterested(ctx, skill.CollectionID, []string{"../bad"}); !library.IsCode(err, library.InvalidArgument) {
		t.Fatalf("invalid name = %v", err)
	}
}

// checkedSkill 检查技能所在的集合,返回它存下的结论。
func checkedSkill(t *testing.T, lib *library.Library, id library.SkillID) library.UpdateCheck {
	t.Helper()
	if _, err := checkSkillUpdate(t, lib, t.Context(), id); err != nil {
		t.Fatal(err)
	}
	skill, err := lib.GetSkill(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return *skill.LastUpdateCheck
}

// requireNewSkills 断言集合上给出的新技能名。
func requireNewSkills(t *testing.T, lib *library.Library, id library.CollectionID, want []string) {
	t.Helper()
	collections, err := lib.ListCollections(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, collection := range collections {
		if collection.ID == id {
			if !reflect.DeepEqual(collection.NewSkills, want) {
				t.Fatalf("new skills = %#v; want %#v", collection.NewSkills, want)
			}
			return
		}
	}
	t.Fatalf("collection %d missing", id)
}

// requireCandidateFlags 对集合再做入库预览,断言每个候选是否标为新技能。
func requireCandidateFlags(t *testing.T, lib *library.Library, id library.CollectionID, want map[string]bool) {
	t.Helper()
	preview, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Collection: id})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, candidate := range preview.Candidates {
		got[candidate.Name] = candidate.New
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates new = %v; want %v", got, want)
	}
}
