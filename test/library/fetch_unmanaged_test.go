package library_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"github.com/tiaot33/skills-tui/internal/library"
)

// 从作用域入库未纳管技能

func fetchUnmanaged(t *testing.T, lib *library.Library, scope library.Scope, name string) library.FetchUnmanagedResult {
	t.Helper()
	result, err := lib.FetchUnmanaged(t.Context(), library.FetchUnmanagedRequest{Scope: scope, Name: name})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func requireScopeStatus(t *testing.T, lib *library.Library, id library.SkillID, scope library.Scope, want library.SkillScopeStatus) library.SkillScopeInspection {
	t.Helper()
	inspections, err := lib.InspectSkillScopes(t.Context(), library.SkillScopeFilter{SkillID: id, Scope: &scope})
	if err != nil {
		t.Fatal(err)
	}
	if len(inspections) != 1 || inspections[0].Status != want {
		t.Fatalf("InspectSkillScopes(%d, %v) = %#v, want one %s", id, scope, inspections, want)
	}
	return inspections[0]
}

func TestFetchUnmanagedSkillLeavesDiskAsItIsAndHasNoSource(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	claude := library.GlobalScope(library.AgentClaudeCode)
	directory := scopeDirectory(t, lib, claude)
	folder := writeUnmanagedSkill(t, directory, "notes", "my notes")
	writeFile(t, filepath.Join(folder, "scripts", "run.sh"), []byte("#!/bin/sh\necho notes\n"), 0o755)
	before := snapshotTree(t, home)

	result := fetchUnmanaged(t, lib, claude, "notes")
	if after := snapshotTree(t, home); !reflect.DeepEqual(after, before) {
		t.Fatalf("FetchUnmanaged changed disk: before %v, after %v", before, after)
	}
	skill := result.Skill
	if result.Matched || skill.Name != "notes" || skill.Description != "my notes" || skill.CollectionID != 0 || skill.Source != (library.Source{}) || skill.LastUpdateCheck != nil {
		t.Fatalf("FetchUnmanaged() = %#v, want a new skill without a source", result)
	}
	if got, err := lib.GetSkill(ctx, skill.ID); err != nil || !reflect.DeepEqual(got, skill) {
		t.Fatalf("GetSkill() = %#v, %v, want %#v", got, err, skill)
	}
	files := skillFiles(t, lib, skill)
	if len(files) != 2 || files[1].Path != "scripts/run.sh" || files[1].Mode != library.ModeExecutable {
		t.Fatalf("files = %#v, want SKILL.md and an executable script", files)
	}
	inspection := requireScopeStatus(t, lib, skill.ID, claude, library.SkillScopeCurrent)
	if !inspection.Direct || inspection.Folder != folder || inspection.VersionID != skill.VersionID {
		t.Fatalf("inspection = %#v, want a direct install of the folder in its current version", inspection)
	}
	requireUnmanagedSkills(t, lib, nil)

	// 没有来源:不在任何集合里,检查更新碰不到它,也不计入角标。
	if collections, err := lib.ListCollections(ctx); err != nil || len(collections) != 0 {
		t.Fatalf("ListCollections() = %#v, %v, want none", collections, err)
	}
	sourced := fetchNamedSkill(t, lib, "sourced")
	if _, err := lib.CheckUpdates(ctx, []library.CollectionID{sourced.CollectionID}); err != nil {
		t.Fatal(err)
	}
	if got, err := lib.GetSkill(ctx, skill.ID); err != nil || got.LastUpdateCheck != nil {
		t.Fatalf("GetSkill() after CheckUpdates = %#v, %v, want no update check", got, err)
	}
}

func TestFetchUnmanagedSymlinkedFolderKeepsTheLink(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	claude := library.GlobalScope(library.AgentClaudeCode)
	codex := library.GlobalScope(library.AgentCodex)
	claudeDirectory := scopeDirectory(t, lib, claude)
	codexDirectory := scopeDirectory(t, lib, codex)
	// npx skills 的布局:真实文件夹在 ~/.agents/skills/pdf,Claude Code 的目录里是指过去的符号链接。
	real := writeUnmanagedSkill(t, codexDirectory, "pdf", "read pdfs")
	if err := os.MkdirAll(claudeDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(claudeDirectory, "pdf")
	target := filepath.Join("..", "..", ".agents", "skills", "pdf")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, real)

	first := fetchUnmanaged(t, lib, claude, "pdf")
	if first.Matched || first.Skill.Name != "pdf" {
		t.Fatalf("FetchUnmanaged(claude) = %#v, want a new skill", first)
	}
	if got, err := os.Readlink(link); err != nil || got != target {
		t.Fatalf("Readlink() = %q, %v, want the link left as it was", got, err)
	}
	if after := snapshotTree(t, real); !reflect.DeepEqual(after, before) {
		t.Fatalf("FetchUnmanaged changed the linked folder: before %v, after %v", before, after)
	}
	requireScopeStatus(t, lib, first.Skill.ID, claude, library.SkillScopeCurrent)

	// 链接指向的真实文件夹内容一样:只给同一个技能补记 Codex 这一处。
	second := fetchUnmanaged(t, lib, codex, "pdf")
	if !second.Matched || second.Skill.ID != first.Skill.ID {
		t.Fatalf("FetchUnmanaged(codex) = %#v, want the skill fetched from the link", second)
	}
	requireScopeStatus(t, lib, first.Skill.ID, codex, library.SkillScopeCurrent)
	if skills, err := lib.ListSkills(t.Context()); err != nil || len(skills) != 1 {
		t.Fatalf("ListSkills() = %#v, %v, want one skill", skills, err)
	}
	requireUnmanagedSkills(t, lib, nil)
}

func TestFetchUnmanagedRecordsAnInstallWhenTheContentMatchesAVersion(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	claude := library.GlobalScope(library.AgentClaudeCode)
	codex := library.GlobalScope(library.AgentCodex)
	claudeDirectory := scopeDirectory(t, lib, claude)
	codexDirectory := scopeDirectory(t, lib, codex)
	skill, source := fetchLocalSkill(t, lib, "pdf", "v1")
	v1 := skill.VersionID

	// 手动拷进 Claude Code 目录的一份,和库里的当前版本一致。
	writeFile(t, filepath.Join(claudeDirectory, "pdf", "SKILL.md"), []byte(skillDocument("pdf", "v1")), 0o644)
	result := fetchUnmanaged(t, lib, claude, "pdf")
	if !result.Matched || result.Skill.ID != skill.ID || result.Skill.CollectionID != skill.CollectionID {
		t.Fatalf("FetchUnmanaged() = %#v, want the existing skill %d", result, skill.ID)
	}
	requireScopeStatus(t, lib, skill.ID, claude, library.SkillScopeCurrent)

	// 来源往前走了以后,Codex 目录里还是旧版本的一份:补记安装,巡检为落后。
	writeLocalSkill(t, source, "v2")
	updated, err := updateSkill(t, lib, ctx, skill.ID)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(codexDirectory, "pdf", "SKILL.md"), []byte(skillDocument("pdf", "v1")), 0o644)
	result = fetchUnmanaged(t, lib, codex, "pdf")
	if !result.Matched || result.Skill.ID != skill.ID || result.Skill.VersionID != updated.Skill.VersionID {
		t.Fatalf("FetchUnmanaged() = %#v, want the existing skill at its current version", result)
	}
	if inspection := requireScopeStatus(t, lib, skill.ID, codex, library.SkillScopeStale); inspection.VersionID != v1 {
		t.Fatalf("inspection = %#v, want version %d", inspection, v1)
	}
	if skills, err := lib.ListSkills(ctx); err != nil || len(skills) != 1 {
		t.Fatalf("ListSkills() = %#v, %v, want one skill", skills, err)
	}
}

func TestFetchUnmanagedCreatesASeparateSkillWhenTheContentDiffers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	lib := openTestLibrary(t)
	claude := library.GlobalScope(library.AgentClaudeCode)
	codex := library.GlobalScope(library.AgentCodex)
	project, err := lib.RegisterProject(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	inProject := library.ProjectScope(library.AgentClaudeCode, project.ID)
	sourced := fetchNamedSkill(t, lib, "pdf")
	writeUnmanagedSkill(t, scopeDirectory(t, lib, claude), "pdf", "mine")
	writeUnmanagedSkill(t, scopeDirectory(t, lib, codex), "pdf", "another")
	writeUnmanagedSkill(t, scopeDirectory(t, lib, inProject), "pdf", "mine")

	mine := fetchUnmanaged(t, lib, claude, "pdf")
	another := fetchUnmanaged(t, lib, codex, "pdf")
	if mine.Matched || another.Matched || mine.Skill.ID == sourced.ID || another.Skill.ID == mine.Skill.ID || another.Skill.CollectionID != 0 {
		t.Fatalf("FetchUnmanaged() = %#v and %#v, want two new skills without a source", mine, another)
	}
	// 同一个作用域里已经装着的技能不算一致:项目里那份和 mine 一样,记到 mine 上。
	same := fetchUnmanaged(t, lib, inProject, "pdf")
	if !same.Matched || same.Skill.ID != mine.Skill.ID {
		t.Fatalf("FetchUnmanaged(project) = %#v, want skill %d", same, mine.Skill.ID)
	}
}

func TestFetchUnmanagedKeepsTheFolderNameWhenItDiffersFromTheSkillName(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	claude := library.GlobalScope(library.AgentClaudeCode)
	codex := library.GlobalScope(library.AgentCodex)
	directory := scopeDirectory(t, lib, claude)
	folder := filepath.Join(directory, "old-notes")
	writeFile(t, filepath.Join(folder, "SKILL.md"), []byte(skillDocument("notes", "body")), 0o644)

	skill := fetchUnmanaged(t, lib, claude, "old-notes").Skill
	if skill.Name != "notes" {
		t.Fatalf("skill name = %q, want the name in SKILL.md", skill.Name)
	}
	if inspection := requireScopeStatus(t, lib, skill.ID, claude, library.SkillScopeCurrent); inspection.Folder != folder {
		t.Fatalf("installed folder = %q, want %q", inspection.Folder, folder)
	}
	requireUnmanagedSkills(t, lib, nil)
	// 装到别的作用域用技能名。
	if err := lib.Install(ctx, skill.ID, codex, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	requireSkillFile(t, scopeDirectory(t, lib, codex), "notes", skillDocument("notes", "body"))
}

func TestFetchUnmanagedSkillCommittedInTheProjectRepository(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	root := t.TempDir()
	project, err := lib.RegisterProject(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	scope := library.ProjectScope(library.AgentClaudeCode, project.ID)
	writeUnmanagedSkill(t, scopeDirectory(t, lib, scope), "team", "team skill")
	runGit(t, root, "init", "-b", "main")
	commitGit(t, root, "add team skill")
	if got := listUnmanagedSkills(t, lib); len(got) != 1 || !got[0].InRepository {
		t.Fatalf("ListUnmanagedSkills() = %#v, want the committed folder", got)
	}
	before := snapshotTree(t, root)

	skill := fetchUnmanaged(t, lib, scope, "team").Skill
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("FetchUnmanaged changed the repository: before %v, after %v", before, after)
	}
	requireScopeStatus(t, lib, skill.ID, scope, library.SkillScopeCurrent)
	requireUnmanagedSkills(t, lib, nil)
}

// 入库后和其他技能一样:装到别的作用域、用目录里的修改更新并回滚、移除。
func TestFetchedUnmanagedSkillIsUsedLikeOtherSkills(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	claude := library.GlobalScope(library.AgentClaudeCode)
	codex := library.GlobalScope(library.AgentCodex)
	claudeDirectory := scopeDirectory(t, lib, claude)
	codexDirectory := scopeDirectory(t, lib, codex)
	folder := writeUnmanagedSkill(t, claudeDirectory, "notes", "v1")
	skill := fetchUnmanaged(t, lib, claude, "notes").Skill
	v1 := skill.VersionID
	original, err := os.ReadFile(filepath.Join(folder, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}

	if err := lib.Install(ctx, skill.ID, codex, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	requireSkillFile(t, codexDirectory, "notes", string(original))

	// 之后在目录里改,就是被手动改过:用这份修改更新,Codex 那一处跟着换上。
	edited := "---\nname: notes\ndescription: v2\n---\n# notes\n"
	writeFile(t, filepath.Join(folder, "SKILL.md"), []byte(edited), 0o644)
	requireScopeStatus(t, lib, skill.ID, claude, library.SkillScopeModified)
	plan, err := lib.PlanUpdateFromScope(ctx, skill.ID, claude)
	if err != nil {
		t.Fatal(err)
	}
	if plan.CollectionID != 0 || plan.Source != (library.Source{}) || len(plan.Installs) != 1 {
		t.Fatalf("PlanUpdateFromScope() = %#v, want a plan without a collection and the Codex install", plan)
	}
	result, err := lib.Update(ctx, library.UpdateRequest{Plan: plan.ID, Skills: []library.SkillID{skill.ID}, Installs: []library.ReinstallRequest{{SkillID: skill.ID, Scope: codex}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Installs[0].Outcome != library.Reinstalled || result.Skills[0].Skill.Description != "v2" || result.Skills[0].Skill.LastUpdateCheck != nil {
		t.Fatalf("Update() = %#v", result)
	}
	requireSkillFile(t, codexDirectory, "notes", edited)
	records, err := lib.ListUpdateHistory(ctx, library.UpdateHistoryFilter{SkillID: skill.ID})
	if err != nil || len(records) != 1 || records[0].CollectionID != 0 || records[0].FromScope == nil {
		t.Fatalf("ListUpdateHistory() = %#v, %v, want the update from the scope", records, err)
	}
	rollback, err := lib.Rollback(ctx, records[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rollback.Skills) != 1 || rollback.Skills[0].ToVersionID != v1 {
		t.Fatalf("Rollback() = %#v, want version %d back", rollback, v1)
	}
	requireSkillFile(t, codexDirectory, "notes", string(original))
	if got, err := lib.GetSkill(ctx, skill.ID); err != nil || got.LastUpdateCheck != nil || got.CollectionID != 0 {
		t.Fatalf("GetSkill() after rollback = %#v, %v, want still no update check", got, err)
	}
	if collections, err := lib.ListCollections(ctx); err != nil || len(collections) != 0 {
		t.Fatalf("ListCollections() = %#v, %v, want none", collections, err)
	}

	if err := lib.RemoveSkill(ctx, skill.ID, library.RemoveSkillOptions{Folders: library.Keep}); err != nil {
		t.Fatal(err)
	}
	if got := listUnmanagedSkills(t, lib); len(got) != 2 {
		t.Fatalf("ListUnmanagedSkills() after removal = %#v, want both folders unmanaged again", got)
	}
}

func TestFetchedUnmanagedSkillTravelsWithSync(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	stub := newS3Stub(t)
	stub.secrets["key"] = "secret"
	lib := openTestLibrary(t)
	lib.SetSyncSettings(&library.SyncSettings{Endpoint: stub.endpoint, Bucket: "bucket", Key: "library.db", Region: "region", AccessKeyID: "key", SecretAccessKey: "secret"})
	claude := library.GlobalScope(library.AgentClaudeCode)
	writeUnmanagedSkill(t, scopeDirectory(t, lib, claude), "notes", "my notes")
	skill := fetchUnmanaged(t, lib, claude, "notes").Skill
	if _, err := lib.SyncPush(ctx, ""); err != nil {
		t.Fatal(err)
	}

	other := openTestLibrary(t)
	other.SetSyncSettings(&library.SyncSettings{Endpoint: stub.endpoint, Bucket: "bucket", Key: "library.db", Region: "region", AccessKeyID: "key", SecretAccessKey: "secret"})
	plan, err := other.PlanSyncPull(ctx)
	if err != nil || !plan.CanPull {
		t.Fatalf("PlanSyncPull() = %#v, %v", plan, err)
	}
	if err := other.SyncPull(ctx, plan.Remote.Identity); err != nil {
		t.Fatal(err)
	}
	if got, err := other.GetSkill(ctx, skill.ID); err != nil || !reflect.DeepEqual(got, skill) {
		t.Fatalf("GetSkill() after pull = %#v, %v, want %#v", got, err, skill)
	}
	requireScopeStatus(t, other, skill.ID, claude, library.SkillScopeCurrent)
}

func TestFetchUnmanagedErrors(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	claude := library.GlobalScope(library.AgentClaudeCode)
	directory := scopeDirectory(t, lib, claude)
	fetch := func(scope library.Scope, name string) error {
		_, err := lib.FetchUnmanaged(ctx, library.FetchUnmanagedRequest{Scope: scope, Name: name})
		return err
	}

	for _, name := range []string{"", "a/b", "..", ".system"} {
		requireErrorDetails(t, fetch(claude, name), library.InvalidArgument, library.ErrorDetails{Field: "name"})
	}
	requireLibraryError(t, fetch(library.GlobalScope("missing"), "notes"), library.NotFound)
	requireLibraryError(t, fetch(library.ProjectScope(library.AgentClaudeCode, 99), "notes"), library.NotFound)
	requireErrorDetails(t, fetch(claude, "notes"), library.NotFound, library.ErrorDetails{Path: filepath.Join(directory, "notes")})

	// 库装的不能再入库。
	installed := fetchNamedSkill(t, lib, "installed")
	if err := lib.Install(ctx, installed.ID, claude, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	requireErrorDetails(t, fetch(claude, "installed"), library.FailedPrecondition, library.ErrorDetails{Path: filepath.Join(directory, "installed"), SkillID: installed.ID})

	// 文件夹里有符号链接。
	linked := writeUnmanagedSkill(t, directory, "linked", "has a link")
	if err := os.Symlink(filepath.Join(linked, "SKILL.md"), filepath.Join(linked, "alias.md")); err != nil {
		t.Fatal(err)
	}
	requireErrorDetails(t, fetch(claude, "linked"), library.FailedPrecondition, library.ErrorDetails{Path: linked})

	// SKILL.md 读不出 frontmatter。
	broken := filepath.Join(directory, "broken")
	writeFile(t, filepath.Join(broken, "SKILL.md"), []byte("no frontmatter\n"), 0o644)
	requireErrorDetails(t, fetch(claude, "broken"), library.SourceInvalid, library.ErrorDetails{Path: broken})

	// 超出读取上限。
	large := writeUnmanagedSkill(t, directory, "large", "too many files")
	for i := range 1000 {
		writeFile(t, filepath.Join(large, "files", strconv.Itoa(i)), []byte("x"), 0o644)
	}
	requireErrorDetails(t, fetch(claude, "large"), library.SourceInvalid, library.ErrorDetails{Path: large})

	if skills, err := lib.ListSkills(ctx); err != nil || len(skills) != 1 {
		t.Fatalf("ListSkills() = %#v, %v, want only the installed skill", skills, err)
	}
}
