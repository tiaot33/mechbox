package library_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/tiaot33/skills-tui/internal/library"
)

// 第一次看到作用域时，一次确认全部入库

func planFetchUnmanaged(t *testing.T, lib *library.Library, scopes ...library.Scope) []library.PlannedUnmanagedSkill {
	t.Helper()
	plan, err := lib.PlanFetchUnmanaged(t.Context(), library.PlanFetchUnmanagedRequest{Scopes: scopes})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

// selectedItems 按计划的默认勾选组成整批入库的请求，带上认出的来源。
func selectedItems(plan []library.PlannedUnmanagedSkill) library.FetchUnmanagedSkillsRequest {
	req := library.FetchUnmanagedSkillsRequest{Skills: []library.UnmanagedFetchItem{}}
	for _, planned := range plan {
		if planned.Selected {
			req.Skills = append(req.Skills, itemOf(planned))
		}
	}
	return req
}

// itemOf 把计划里的一条原样转成整批入库请求里的一项：几处文件夹一起，带上认出的来源。
func itemOf(planned library.PlannedUnmanagedSkill) library.UnmanagedFetchItem {
	item := library.UnmanagedFetchItem{SourceInput: planned.SourceInput, SourceRef: planned.SourceRef}
	for _, folder := range planned.Folders {
		item.Folders = append(item.Folders, library.FolderInScope{Scope: folder.Scope, Name: folder.Name})
	}
	return item
}

func fetchUnmanagedSkills(t *testing.T, lib *library.Library, req library.FetchUnmanagedSkillsRequest) []library.FetchedUnmanagedSkill {
	t.Helper()
	results, err := lib.FetchUnmanagedSkills(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != len(req.Skills) {
		t.Fatalf("FetchUnmanagedSkills() = %d results, want %d", len(results), len(req.Skills))
	}
	return results
}

func plannedNamed(t *testing.T, plan []library.PlannedUnmanagedSkill, name string) []library.PlannedUnmanagedSkill {
	t.Helper()
	var found []library.PlannedUnmanagedSkill
	for _, planned := range plan {
		if planned.Name == name {
			found = append(found, planned)
		}
	}
	if len(found) == 0 {
		t.Fatalf("plan %#v has no %s", plan, name)
	}
	return found
}

func folderPaths(planned library.PlannedUnmanagedSkill) []string {
	paths := []string{}
	for _, folder := range planned.Folders {
		paths = append(paths, folder.Folder)
	}
	return paths
}

func touchTree(t *testing.T, root string, at time.Time) {
	t.Helper()
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(path, at, at)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestFetchUnmanagedSkillsTakesOverGlobalSkillsInOneConfirmation(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	lib := openTestLibrary(t)
	claude := library.GlobalScope(library.AgentClaudeCode)
	codex := library.GlobalScope(library.AgentCodex)
	claudeDirectory := scopeDirectory(t, lib, claude)
	codexDirectory := scopeDirectory(t, lib, codex)

	// npx skills 的默认做法:真实文件夹在 ~/.agents/skills/pdf,Claude Code 的目录里用符号链接指过去。
	real := writeUnmanagedSkill(t, codexDirectory, "pdf", "read pdfs")
	if err := os.MkdirAll(claudeDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(claudeDirectory, "pdf")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	// 手动拷进两个 Agent 目录、内容一样的技能。
	notes := writeUnmanagedSkill(t, claudeDirectory, "notes", "my notes")
	copied := writeUnmanagedSkill(t, codexDirectory, "notes", "my notes")
	draft := writeUnmanagedSkill(t, claudeDirectory, "draft", "only for claude")
	before := snapshotTree(t, codexDirectory, notes, draft)

	plan := planFetchUnmanaged(t, lib, claude, codex)
	if len(plan) != 3 {
		t.Fatalf("plan = %#v, want draft, notes and pdf", plan)
	}
	for _, planned := range plan {
		if !planned.Selected || planned.Conflict || planned.Error != nil || planned.InRepository || planned.InProjectLock {
			t.Fatalf("planned %s = %#v, want selected without conflict", planned.Name, planned)
		}
	}
	if got := folderPaths(plannedNamed(t, plan, "pdf")[0]); !reflect.DeepEqual(got, []string{link, real}) {
		t.Fatalf("pdf folders = %v, want the link and the real folder", got)
	}
	if got := folderPaths(plannedNamed(t, plan, "notes")[0]); !reflect.DeepEqual(got, []string{notes, copied}) {
		t.Fatalf("notes folders = %v, want both copies", got)
	}
	if only := planFetchUnmanaged(t, lib, codex); len(only) != 2 || len(plannedNamed(t, only, "pdf")[0].Folders) != 1 {
		t.Fatalf("plan for Codex = %#v, want only Codex folders", only)
	}

	results := fetchUnmanagedSkills(t, lib, selectedItems(plan))
	for _, result := range results {
		if result.Error != nil || result.Skill.ID == 0 || result.Matched {
			t.Fatalf("result = %#v, want a new skill", result)
		}
	}
	if after := snapshotTree(t, codexDirectory, notes, draft); !reflect.DeepEqual(after, before) {
		t.Fatalf("FetchUnmanagedSkills changed disk: before %v, after %v", before, after)
	}
	if got, err := os.Readlink(link); err != nil || got != real {
		t.Fatalf("Readlink() = %q, %v, want the link left as it was", got, err)
	}
	// 内容一样的几处入库成一个技能，各处都记为安装，巡检都是正常。
	skills, err := lib.ListSkills(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := skillNames(skills); !reflect.DeepEqual(got, []string{"draft", "notes", "pdf"}) {
		t.Fatalf("skills = %v, want one skill per name", got)
	}
	for _, result := range results {
		for _, folder := range result.Folders {
			requireScopeStatus(t, lib, result.Skill.ID, folder.Scope, library.SkillScopeCurrent)
		}
	}
	requireUnmanagedSkills(t, lib, nil)
	if plan := planFetchUnmanaged(t, lib, claude, codex); len(plan) != 0 {
		t.Fatalf("plan after fetching = %#v, want nothing left", plan)
	}
}

func TestPlanFetchUnmanagedListsConflictsAndSelectsTheNewest(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	claude := library.GlobalScope(library.AgentClaudeCode)
	codex := library.GlobalScope(library.AgentCodex)
	older := writeUnmanagedSkill(t, scopeDirectory(t, lib, claude), "pdf", "the old copy")
	newer := writeUnmanagedSkill(t, scopeDirectory(t, lib, codex), "pdf", "the new copy")
	now := time.Now()
	touchTree(t, older, now.Add(-48*time.Hour))
	touchTree(t, newer, now.Add(-time.Hour))

	plan := planFetchUnmanaged(t, lib, claude, codex)
	conflicting := plannedNamed(t, plan, "pdf")
	if len(conflicting) != 2 {
		t.Fatalf("pdf = %#v, want two conflicting copies", conflicting)
	}
	selected := map[string]bool{}
	for _, planned := range conflicting {
		if !planned.Conflict || len(planned.Folders) != 1 || planned.ModifiedAt.IsZero() {
			t.Fatalf("planned = %#v, want a conflicting copy with its modification time", planned)
		}
		selected[planned.Folders[0].Folder] = planned.Selected
	}
	if !reflect.DeepEqual(selected, map[string]bool{older: false, newer: true}) {
		t.Fatalf("selected = %v, want only the newest copy", selected)
	}
	if !conflicting[0].ModifiedAt.After(conflicting[1].ModifiedAt) {
		t.Fatalf("plan order = %#v, want the newest copy first", conflicting)
	}

	results := fetchUnmanagedSkills(t, lib, selectedItems(plan))
	if results[0].Error != nil || results[0].Skill.Description != "the new copy" {
		t.Fatalf("results = %#v, want the newest copy fetched", results)
	}
	// 其余的仍是未纳管技能，不被覆盖，之后想要再入库。
	requireUnmanagedSkills(t, lib, []library.UnmanagedSkill{{Scope: claude, Name: "pdf", Description: "the old copy", Folder: older}})
	if content, err := os.ReadFile(filepath.Join(older, "SKILL.md")); err != nil || string(content) != "---\nname: pdf\ndescription: the old copy\n---\n# pdf\n" {
		t.Fatalf("old copy = %q, %v, want it untouched", content, err)
	}
	skills, err := lib.ListSkills(ctx)
	if err != nil || len(skills) != 1 {
		t.Fatalf("ListSkills() = %#v, %v, want one skill", skills, err)
	}
}

func TestPlanFetchUnmanagedLeavesTeamSkillsOfTheProjectUnselected(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", "")
	lib := openTestLibrary(t)
	repository := t.TempDir()
	runGit(t, repository, "init", "-b", "main")
	project, err := lib.RegisterProject(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	scope := library.ProjectScope(library.AgentClaudeCode, project.ID)
	directory := scopeDirectory(t, lib, scope)
	writeUnmanagedSkill(t, directory, "team", "committed by the team")
	writeFile(t, filepath.Join(repository, "README.md"), []byte("project\n"), 0o644)
	commitGit(t, repository, "team skill")
	// skills-lock.json 提交在仓库里，技能文件夹本身没提交：它是团队约定装的。
	writeUnmanagedSkill(t, directory, "lint", "installed by the lock file")
	writeSourceRecords(t, filepath.Join(repository, "skills-lock.json"), 1, map[string]map[string]string{
		"lint": {"source": "./vendor/skills", "sourceType": "local", "computedHash": "abc"},
	})
	writeUnmanagedSkill(t, directory, "mine", "my own")

	plan := planFetchUnmanaged(t, lib, scope)
	got := map[string][3]bool{}
	for _, planned := range plan {
		got[planned.Name] = [3]bool{planned.Selected, planned.InRepository, planned.InProjectLock}
	}
	want := map[string][3]bool{
		"lint": {false, false, true},
		"mine": {true, false, false},
		"team": {false, true, false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("plan = %v, want %v", got, want)
	}
	if lint := plannedNamed(t, plan, "lint")[0]; lint.SourceInput != filepath.Join(project.Path, "vendor", "skills") {
		t.Fatalf("lint = %#v, want the source from the lock file", lint)
	}
}

func TestPlanFetchUnmanagedListsFoldersThatCannotBeFetched(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	claude := library.GlobalScope(library.AgentClaudeCode)
	directory := scopeDirectory(t, lib, claude)
	broken := filepath.Join(directory, "broken")
	writeFile(t, filepath.Join(broken, "SKILL.md"), []byte("no frontmatter\n"), 0o644)
	writeUnmanagedSkill(t, directory, "fine", "works")

	plan := planFetchUnmanaged(t, lib, claude)
	if len(plan) != 2 {
		t.Fatalf("plan = %#v, want broken and fine", plan)
	}
	planned := plannedNamed(t, plan, "broken")[0]
	if planned.Selected || planned.Error == nil || planned.Error.Code != library.SourceInvalid || planned.Error.Details.Path != broken {
		t.Fatalf("broken = %#v, want an unselected folder with the reason", planned)
	}
	if fine := plannedNamed(t, plan, "fine")[0]; !fine.Selected {
		t.Fatalf("fine = %#v, want it selected", fine)
	}
}

func TestPlanFetchUnmanagedMergesFoldersSharedByAgents(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	shared, err := lib.AddAgent(ctx, library.CustomAgent{Name: "Shared", GlobalDirectory: "~/.claude/skills", ProjectDirectory: ".shared/skills"})
	if err != nil {
		t.Fatal(err)
	}
	claude := library.GlobalScope(library.AgentClaudeCode)
	folder := writeUnmanagedSkill(t, scopeDirectory(t, lib, claude), "pdf", "read pdfs")

	plan := planFetchUnmanaged(t, lib, claude, library.GlobalScope(shared.Code))
	if len(plan) != 1 || len(plan[0].Folders) != 2 {
		t.Fatalf("plan = %#v, want one skill in both agents", plan)
	}
	results := fetchUnmanagedSkills(t, lib, selectedItems(plan))
	if results[0].Error != nil {
		t.Fatalf("result = %#v, want the shared folder fetched", results[0])
	}
	skillScopes := listSkillScopes(t, lib)
	if len(skillScopes) != 2 || skillScopes[0].Folder != folder || skillScopes[1].Folder != folder {
		t.Fatalf("skill scopes = %#v, want the shared folder listed for both agents", skillScopes)
	}
	requireUnmanagedSkills(t, lib, nil)
}

func TestFetchUnmanagedSkillsReadsSourcesAndKeepsGoingWhenOneFails(t *testing.T) {
	ctx := t.Context()
	f := newSourcedSkillFixture(t)
	recordsPath := globalRecordsPath(t)
	f.writeDisk(t, "v1")
	directory := filepath.Dir(f.folder)
	writeFile(t, filepath.Join(directory, "docx", "SKILL.md"), []byte(skillDocument("docx", "v1")), 0o644)
	writeFile(t, filepath.Join(directory, "notes", "SKILL.md"), []byte(skillDocument("notes", "mine")), 0o644)
	writeFile(t, filepath.Join(directory, "lost", "SKILL.md"), []byte(skillDocument("lost", "gone")), 0o644)
	writeSourceRecords(t, recordsPath, 3, map[string]map[string]string{
		"pdf":  {"source": "owner/repo", "sourceType": "github", "ref": "main", "skillPath": "skills/pdf/SKILL.md"},
		"docx": {"source": "owner/repo", "sourceType": "github", "ref": "main", "skillPath": "skills/docx/SKILL.md"},
		"lost": {"source": "owner/repo", "sourceType": "github", "ref": "main", "skillPath": "skills/lost/SKILL.md"},
	})
	before := snapshotTree(t, f.home)

	plan := planFetchUnmanaged(t, f.lib, f.claude)
	if len(plan) != 4 || plannedNamed(t, plan, "pdf")[0].SourceInput != "owner/repo" || plannedNamed(t, plan, "notes")[0].SourceInput != "" {
		t.Fatalf("plan = %#v, want sources from the records", plan)
	}
	req := selectedItems(plan)
	results := fetchUnmanagedSkills(t, f.lib, req)
	byName := map[string]library.FetchedUnmanagedSkill{}
	for i, result := range results {
		if !reflect.DeepEqual(result.Folders, req.Skills[i].Folders) {
			t.Fatalf("result %d folders = %#v, want %#v", i, result.Folders, req.Skills[i].Folders)
		}
		byName[req.Skills[i].Folders[0].Name] = result
	}
	// 来源里没有 lost:只有它没入库，其余照常入库。
	if lost := byName["lost"]; lost.Error == nil || lost.Error.Code != library.NotFound || lost.Skill.ID != 0 {
		t.Fatalf("lost = %#v, want NotFound", lost)
	}
	pdf, docx, notes := byName["pdf"], byName["docx"], byName["notes"]
	if pdf.Error != nil || docx.Error != nil || notes.Error != nil {
		t.Fatalf("results = %#v, want pdf, docx and notes fetched", results)
	}
	if pdf.Skill.CollectionID == 0 || pdf.Skill.CollectionID != docx.Skill.CollectionID || notes.Skill.CollectionID != 0 {
		t.Fatalf("collections = %d, %d, %d, want pdf and docx in one collection and notes without a source", pdf.Skill.CollectionID, docx.Skill.CollectionID, notes.Skill.CollectionID)
	}
	if pdf.Skill.Revision == "" {
		t.Fatalf("pdf = %#v, want the source revision", pdf.Skill)
	}
	if after := snapshotTree(t, f.home); !reflect.DeepEqual(after, before) {
		t.Fatalf("FetchUnmanagedSkills changed disk: before %v, after %v", before, after)
	}
	requireUnmanagedSkills(t, f.lib, []library.UnmanagedSkill{{
		Scope: f.claude, Name: "lost", Folder: filepath.Join(directory, "lost"), SourceInput: "owner/repo", SourceRef: "main",
	}})

	// 清空来源后，读不成来源的那个作为没有来源的技能入库。
	retry := itemOf(plannedNamed(t, planFetchUnmanaged(t, f.lib, f.claude), "lost")[0])
	retry.SourceInput, retry.SourceRef = "", ""
	again := fetchUnmanagedSkills(t, f.lib, library.FetchUnmanagedSkillsRequest{Skills: []library.UnmanagedFetchItem{retry}})
	if again[0].Error != nil || again[0].Skill.CollectionID != 0 {
		t.Fatalf("retry = %#v, want a skill without a source", again[0])
	}
	if skills, err := f.lib.ListSkills(ctx); err != nil || len(skills) != 4 {
		t.Fatalf("ListSkills() = %#v, %v, want four skills", skills, err)
	}
}

func TestFetchUnmanagedSkillsErrors(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	claude := library.GlobalScope(library.AgentClaudeCode)
	codex := library.GlobalScope(library.AgentCodex)
	writeUnmanagedSkill(t, scopeDirectory(t, lib, claude), "pdf", "one copy")
	writeUnmanagedSkill(t, scopeDirectory(t, lib, codex), "pdf", "another copy")
	pdf := library.FolderInScope{Scope: claude, Name: "pdf"}

	for _, tc := range []struct {
		name  string
		req   library.FetchUnmanagedSkillsRequest
		field string
	}{
		{"no folders", library.FetchUnmanagedSkillsRequest{Skills: []library.UnmanagedFetchItem{{}}}, "folders"},
		{"invalid name", library.FetchUnmanagedSkillsRequest{Skills: []library.UnmanagedFetchItem{{Folders: []library.FolderInScope{{Scope: claude, Name: "../pdf"}}}}}, "name"},
		{"folder twice", library.FetchUnmanagedSkillsRequest{Skills: []library.UnmanagedFetchItem{{Folders: []library.FolderInScope{pdf}}, {Folders: []library.FolderInScope{pdf}}}}, "folders"},
		{"ref without a source", library.FetchUnmanagedSkillsRequest{Skills: []library.UnmanagedFetchItem{{Folders: []library.FolderInScope{pdf}, SourceRef: "main"}}}, "source_ref"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := lib.FetchUnmanagedSkills(ctx, tc.req)
			requireErrorDetails(t, err, library.InvalidArgument, library.ErrorDetails{Field: tc.field})
		})
	}
	if _, err := lib.PlanFetchUnmanaged(ctx, library.PlanFetchUnmanagedRequest{Scopes: []library.Scope{{Kind: library.ScopeGlobal, Agent: "nobody"}}}); err == nil {
		t.Fatal("PlanFetchUnmanaged() with an unknown agent succeeded")
	} else {
		requireLibraryError(t, err, library.NotFound)
	}

	// 内容不一样的几处不能当成一个技能入库;没有这个文件夹的报 NotFound。都只算这一条失败。
	results := fetchUnmanagedSkills(t, lib, library.FetchUnmanagedSkillsRequest{Skills: []library.UnmanagedFetchItem{
		{Folders: []library.FolderInScope{pdf, {Scope: codex, Name: "pdf"}}},
		{Folders: []library.FolderInScope{{Scope: claude, Name: "missing"}}},
	}})
	if results[0].Error == nil || results[0].Error.Code != library.FailedPrecondition {
		t.Fatalf("result = %#v, want FailedPrecondition for folders that differ", results[0])
	}
	if results[1].Error == nil || results[1].Error.Code != library.NotFound {
		t.Fatalf("result = %#v, want NotFound for a missing folder", results[1])
	}
	if skills, err := lib.ListSkills(ctx); err != nil || len(skills) != 0 {
		t.Fatalf("ListSkills() = %#v, %v, want an empty library", skills, err)
	}
}
