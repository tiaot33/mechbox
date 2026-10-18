package library_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/tiaot33/skills-tui/internal/library"
)

// 入库时认出技能的真实来源

// writeSourceRecords 写一份 npx skills 的来源记录:skills 按技能名列出记录。
func writeSourceRecords(t *testing.T, path string, version int, skills map[string]map[string]string) {
	t.Helper()
	content, err := json.Marshal(map[string]any{"version": version, "skills": skills})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, content, 0o644)
}

// globalRecordsPath 让全局的来源记录落在临时的 XDG_STATE_HOME 下,返回它的路径。
func globalRecordsPath(t *testing.T) string {
	t.Helper()
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	return filepath.Join(state, "skills", ".skill-lock.json")
}

// computedHash 按 npx skills 项目里来源记录的算法算技能文件夹的哈希:按相对路径排序,依次把路径和内容送进 SHA-256。
func computedHash(files map[string]string) string {
	hash := sha256.New()
	for _, path := range slices.Sorted(func(yield func(string) bool) {
		for path := range files {
			if !yield(path) {
				return
			}
		}
	}) {
		hash.Write([]byte(path))
		hash.Write([]byte(files[path]))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// sourcedSkillFixture 是一个 GitHub 来源 owner/repo(skills/pdf 与 skills/docx,经 REST API 读取)和一个 HOME,
// 在 Claude Code 全局目录里放着 npx skills 装的 pdf。
type sourcedSkillFixture struct {
	lib     *library.Library
	repo    string
	api     *gitHubAPIMock
	home    string
	claude  library.Scope
	folder  string
	v1Tree  string
	v1Files map[string]string
}

func newSourcedSkillFixture(t *testing.T) sourcedSkillFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	repo := writeGitHubRepo(t, map[string]repoFile{
		"skills/pdf/SKILL.md":       {content: []byte(skillDocument("pdf", "v1")), mode: 0o644},
		"skills/pdf/scripts/run.sh": {content: []byte(pdfScript), mode: 0o755},
		"skills/docx/SKILL.md":      {content: []byte(skillDocument("docx", "v1")), mode: 0o644},
	})
	api := serveGitHubAPI(t, inspectGitHubRepo(t, repo))
	blockGitHubClone(t)
	lib := openTestLibrary(t)
	claude := library.GlobalScope(library.AgentClaudeCode)
	f := sourcedSkillFixture{
		lib: lib, repo: repo, api: api, home: home, claude: claude,
		folder:  filepath.Join(scopeDirectory(t, lib, claude), "pdf"),
		v1Tree:  gitRevParse(t, repo, "HEAD:skills/pdf"),
		v1Files: map[string]string{"SKILL.md": skillDocument("pdf", "v1"), "scripts/run.sh": pdfScript},
	}
	return f
}

func mustPreview(t *testing.T, lib *library.Library, req library.PreviewFetchRequest) library.FetchPreview {
	t.Helper()
	preview, err := lib.PreviewFetch(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	return preview
}

// pdfScript 是来源里 pdf 带的可执行脚本,npx skills 装的文件夹里也有,哈希按多个文件和可执行位算。
const pdfScript = "#!/bin/sh\necho pdf\n"

func (f sourcedSkillFixture) writeDisk(t *testing.T, body string) {
	t.Helper()
	writePDFFolder(t, f.folder, body)
}

func writePDFFolder(t *testing.T, folder, body string) {
	t.Helper()
	writeFile(t, filepath.Join(folder, "SKILL.md"), []byte(skillDocument("pdf", body)), 0o644)
	writeFile(t, filepath.Join(folder, "scripts", "run.sh"), []byte(pdfScript), 0o755)
}

// publish 让作者在来源里把 pdf 改成 body 并提交。
func (f sourcedSkillFixture) publish(t *testing.T, body string) {
	t.Helper()
	writeFile(t, filepath.Join(f.repo, "skills", "pdf", "SKILL.md"), []byte(skillDocument("pdf", body)), 0o644)
	commitGit(t, f.repo, "pdf "+body)
	f.api.Swap(inspectGitHubRepo(t, f.repo))
}

func (f sourcedSkillFixture) fetch(t *testing.T) library.FetchUnmanagedResult {
	t.Helper()
	result, err := f.lib.FetchUnmanaged(t.Context(), library.FetchUnmanagedRequest{Scope: f.claude, Name: "pdf", SourceInput: "owner/repo", SourceRef: "main"})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func (f sourcedSkillFixture) currentVersion(t *testing.T, skill library.Skill) library.SkillVersion {
	t.Helper()
	version, err := f.lib.GetVersion(t.Context(), skill.VersionID)
	if err != nil {
		t.Fatal(err)
	}
	return version
}

func (f sourcedSkillFixture) planned(t *testing.T, skill library.Skill) (library.UpdatePlan, library.PlannedSkill) {
	t.Helper()
	plan, err := f.lib.PlanUpdate(t.Context(), skill.CollectionID)
	if err != nil {
		t.Fatal(err)
	}
	return plan, plannedSkill(t, plan, skill.ID)
}

func TestFetchUnmanagedWithTheSourceContentJoinsTheCollection(t *testing.T) {
	ctx := t.Context()
	f := newSourcedSkillFixture(t)
	docx := fetchSkills(t, f.lib, mustPreview(t, f.lib, gitHubRequest()), []string{"docx"})[0]
	f.writeDisk(t, "v1")
	before := snapshotTree(t, f.home)

	result := f.fetch(t)
	if after := snapshotTree(t, f.home); !reflect.DeepEqual(after, before) {
		t.Fatalf("FetchUnmanaged changed disk: before %v, after %v", before, after)
	}
	skill := result.Skill
	head := gitRevParse(t, f.repo, "HEAD")
	if result.Matched || skill.Name != "pdf" || skill.CollectionID != docx.CollectionID || skill.Revision != head {
		t.Fatalf("FetchUnmanaged() = %#v, want pdf in the owner/repo collection at %s", result, head)
	}
	if version := f.currentVersion(t, skill); version.MaybeEdited {
		t.Fatalf("version = %#v, want the source's version", version)
	}
	requireScopeStatus(t, f.lib, skill.ID, f.claude, library.SkillScopeCurrent)
	requireCheck(t, f.lib, skill.ID, library.UpdateCurrent, false, false)

	// 来源里的其他技能都算用户见过的,不会冒出来当成新技能。
	collections, err := f.lib.ListCollections(ctx)
	if err != nil || len(collections) != 1 || !reflect.DeepEqual(collections[0].Skills, []library.SkillID{docx.ID, skill.ID}) || len(collections[0].NewSkills) != 0 {
		t.Fatalf("ListCollections() = %#v, %v, want one collection with docx and pdf and no new skills", collections, err)
	}

	// 作者再改,照常有新版本、计入角标,更新后 Agent 用上新版,能回滚。
	f.publish(t, "v2")
	requireCheck(t, f.lib, skill.ID, library.UpdateAvailable, false, true)
	if _, planned := f.planned(t, skill); planned.Edited || planned.Change != library.PlannedChanged {
		t.Fatalf("planned = %#v, want a plain change", planned)
	}
}

func TestFetchUnmanagedOnlyFromSourceCountsSeenSkills(t *testing.T) {
	f := newSourcedSkillFixture(t)
	f.writeDisk(t, "v1")
	skill := f.fetch(t).Skill
	writeFile(t, filepath.Join(f.repo, "skills", "xlsx", "SKILL.md"), []byte(skillDocument("xlsx", "v1")), 0o644)
	commitGit(t, f.repo, "add xlsx")
	f.api.Swap(inspectGitHubRepo(t, f.repo))
	requireCheck(t, f.lib, skill.ID, library.UpdateCurrent, false, false)
	collections, err := f.lib.ListCollections(t.Context())
	if err != nil || len(collections) != 1 || !reflect.DeepEqual(collections[0].NewSkills, []string{"xlsx"}) {
		t.Fatalf("ListCollections() = %#v, %v, want only xlsx as a new skill", collections, err)
	}
}

func TestFetchUnmanagedUntouchedButBehindIsAnOrdinaryUpdate(t *testing.T) {
	for _, records := range []string{"global", "project"} {
		t.Run(records, func(t *testing.T) {
			ctx := t.Context()
			f := newSourcedSkillFixture(t)
			scope := f.claude
			folder := f.folder
			if records == "global" {
				writeSourceRecords(t, filepath.Join(f.home, ".agents", ".skill-lock.json"), 3, map[string]map[string]string{
					"pdf": {"source": "owner/repo", "sourceType": "github", "sourceUrl": "https://github.com/owner/repo.git", "ref": "main", "skillFolderHash": f.v1Tree},
				})
			} else {
				project := t.TempDir()
				registered, err := f.lib.RegisterProject(ctx, project)
				if err != nil {
					t.Fatal(err)
				}
				scope = library.ProjectScope(library.AgentClaudeCode, registered.ID)
				folder = filepath.Join(scopeDirectory(t, f.lib, scope), "pdf")
				writeSourceRecords(t, filepath.Join(project, "skills-lock.json"), 1, map[string]map[string]string{
					"pdf": {"source": "owner/repo", "sourceType": "github", "ref": "main", "computedHash": computedHash(f.v1Files)},
				})
			}
			writePDFFolder(t, folder, "v1")
			// 用户装了以后,作者又发了新版:磁盘上是作者的旧版。
			f.publish(t, "v2")
			listed := slices.IndexFunc(listUnmanagedSkills(t, f.lib), func(skill library.UnmanagedSkill) bool { return skill.Folder == folder })
			if listed < 0 || listUnmanagedSkills(t, f.lib)[listed].SourceInput != "owner/repo" {
				t.Fatalf("ListUnmanagedSkills() = %#v, want pdf from owner/repo", listUnmanagedSkills(t, f.lib))
			}

			result, err := f.lib.FetchUnmanaged(ctx, library.FetchUnmanagedRequest{Scope: scope, Name: "pdf", SourceInput: "owner/repo", SourceRef: "main"})
			if err != nil {
				t.Fatal(err)
			}
			skill := result.Skill
			if skill.Revision != "" || skill.CollectionID == 0 || skill.Edited {
				t.Fatalf("FetchUnmanaged() = %#v, want a skill in the collection with an unknown source version", result)
			}
			if version := f.currentVersion(t, skill); version.MaybeEdited {
				t.Fatalf("version = %#v, want the author's old version, not a possible edit", version)
			}
			if files := skillFiles(t, f.lib, skill); string(files[0].Content) != skillDocument("pdf", "v1") {
				t.Fatalf("files = %#v, want what is on disk", files)
			}
			requireScopeStatus(t, f.lib, skill.ID, scope, library.SkillScopeCurrent)
			requireCheck(t, f.lib, skill.ID, library.UpdateAvailable, false, true)
			if _, planned := f.planned(t, skill); planned.Edited || planned.Change != library.PlannedChanged {
				t.Fatalf("planned = %#v, want a plain change", planned)
			}
		})
	}
}

func TestFetchUnmanagedThatMayBeEditedWarnsBeforeTheUpdate(t *testing.T) {
	for _, tc := range []struct {
		name   string
		record map[string]string
	}{
		{"no source record", nil},
		{"hash does not match", map[string]string{"source": "owner/repo", "sourceType": "github", "ref": "main", "skillFolderHash": "0123456789abcdef0123456789abcdef01234567"}},
		{"record names another source", map[string]string{"source": "other/repo", "sourceType": "github", "ref": "main"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			f := newSourcedSkillFixture(t)
			if tc.record != nil {
				record := tc.record
				if record["source"] == "other/repo" {
					record["skillFolderHash"] = gitRevParse(t, f.repo, "HEAD:skills/pdf")
				}
				writeSourceRecords(t, filepath.Join(f.home, ".agents", ".skill-lock.json"), 3, map[string]map[string]string{"pdf": record})
			}
			if tc.name == "record names another source" {
				f.writeDisk(t, "v1")
				f.publish(t, "v2")
			} else {
				f.writeDisk(t, "edited")
			}

			skill := f.fetch(t).Skill
			version := f.currentVersion(t, skill)
			if !version.MaybeEdited || skill.Revision != "" || !skill.Edited {
				t.Fatalf("version = %#v, skill = %#v, want a possible edit with an unknown source version", version, skill)
			}
			requireScopeStatus(t, f.lib, skill.ID, f.claude, library.SkillScopeCurrent)

			// 第一次检查就报有新版本,计入角标;更新计划写明会换掉修改。
			requireCheck(t, f.lib, skill.ID, library.UpdateAvailable, false, true)
			plan, planned := f.planned(t, skill)
			if !planned.Edited || planned.Change != library.PlannedChanged {
				t.Fatalf("planned = %#v, want a change marked as edited", planned)
			}
			// 用户跳过,角标消失,直到作者再改。
			if err := f.lib.SkipUpdate(ctx, library.SkipRequest{Plan: plan.ID, Skills: []library.SkillID{skill.ID}}); err != nil {
				t.Fatal(err)
			}
			requireCheck(t, f.lib, skill.ID, library.UpdateAvailable, true, false)
			f.publish(t, "v3")
			requireCheck(t, f.lib, skill.ID, library.UpdateAvailable, false, true)

			// 勾上更新换成作者的新版,能回滚回这份修改。
			updated, err := updateSkill(t, f.lib, ctx, skill.ID)
			if err != nil {
				t.Fatal(err)
			}
			if version := f.currentVersion(t, updated.Skill); version.MaybeEdited || updated.Skill.Edited {
				t.Fatalf("version after update = %#v, skill = %#v, want the source's version", version, updated.Skill)
			}
			requireCheck(t, f.lib, skill.ID, library.UpdateCurrent, false, false)
			history, err := f.lib.ListUpdateHistory(ctx, library.UpdateHistoryFilter{SkillID: skill.ID})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.lib.Rollback(ctx, history[0].ID); err != nil {
				t.Fatal(err)
			}
			rolledBack, err := f.lib.GetSkill(ctx, skill.ID)
			if err != nil || rolledBack.VersionID != skill.VersionID {
				t.Fatalf("GetSkill() after rollback = %#v, %v, want version %d", rolledBack, err, skill.VersionID)
			}
		})
	}
}

func TestFetchUnmanagedMaybeEditedClearsWhenTheAuthorCatchesUp(t *testing.T) {
	f := newSourcedSkillFixture(t)
	f.writeDisk(t, "edited")
	skill := f.fetch(t).Skill
	// 作者正好改成了和磁盘上一样:更新时确认没有变化,这一版就成了来源里的,之后作者再改是普通的更新。
	f.publish(t, "edited")
	requireCheck(t, f.lib, skill.ID, library.UpdateCurrent, false, false)
	if _, err := f.lib.Update(t.Context(), mustPlanUpdate(t, f.lib, skill.ID)); err != nil {
		t.Fatal(err)
	}
	if version := f.currentVersion(t, skill); version.MaybeEdited || version.Revision != gitRevParse(t, f.repo, "HEAD") {
		t.Fatalf("version = %#v, want it confirmed as the source's version", version)
	}
	f.publish(t, "v3")
	requireCheck(t, f.lib, skill.ID, library.UpdateAvailable, false, true)
	if _, planned := f.planned(t, skill); planned.Edited {
		t.Fatalf("planned = %#v, want a plain change", planned)
	}
}

func TestMaybeEditedSkillUpdatedFromScopeStillWarnsBeforeTheAuthorsVersion(t *testing.T) {
	ctx := t.Context()
	f := newSourcedSkillFixture(t)
	f.writeDisk(t, "edited")
	skill := f.fetch(t).Skill
	// 用户接着在目录里改,再用这份修改更新:新版本基于那份说不清的版本,照样提示会换掉修改。
	f.writeDisk(t, "edited again")
	plan, err := f.lib.PlanUpdateFromScope(ctx, skill.ID, f.claude)
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.lib.Update(ctx, library.UpdateRequest{Plan: plan.ID, Skills: []library.SkillID{skill.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if version := f.currentVersion(t, result.Skills[0].Skill); version.BasedOn != skill.VersionID || version.MaybeEdited {
		t.Fatalf("version = %#v, want an edit based on version %d", version, skill.VersionID)
	}
	requireCheck(t, f.lib, skill.ID, library.UpdateAvailable, false, true)
	if _, planned := f.planned(t, skill); !planned.Edited || planned.Change != library.PlannedChanged {
		t.Fatalf("planned = %#v, want a change marked as edited", planned)
	}
}

func TestFetchUnmanagedWithSameNameInTheCollection(t *testing.T) {
	ctx := t.Context()
	f := newSourcedSkillFixture(t)
	pdf := fetchSkills(t, f.lib, mustPreview(t, f.lib, gitHubRequest()), []string{"pdf"})[0]
	codex := library.GlobalScope(library.AgentCodex)
	codexDirectory := scopeDirectory(t, f.lib, codex)

	// 和集合里的 pdf 的某个版本一样:只补记这一处安装。
	f.writeDisk(t, "v1")
	result := f.fetch(t)
	if !result.Matched || result.Skill.ID != pdf.ID {
		t.Fatalf("FetchUnmanaged() = %#v, want the install recorded for pdf", result)
	}
	requireScopeStatus(t, f.lib, pdf.ID, f.claude, library.SkillScopeCurrent)

	// 内容和它的哪个版本都对不上:不合并,库和磁盘都不变。
	writePDFFolder(t, filepath.Join(codexDirectory, "pdf"), "a variant")
	before := snapshotTree(t, f.home)
	_, err := f.lib.FetchUnmanaged(ctx, library.FetchUnmanagedRequest{Scope: codex, Name: "pdf", SourceInput: "owner/repo", SourceRef: "main"})
	requireErrorDetails(t, err, library.AlreadyExists, library.ErrorDetails{SkillID: pdf.ID, Name: "pdf"})
	if after := snapshotTree(t, f.home); !reflect.DeepEqual(after, before) {
		t.Fatalf("FetchUnmanaged changed disk: before %v, after %v", before, after)
	}
	if versions, err := f.lib.ListVersions(ctx, pdf.ID); err != nil || len(versions) != 1 {
		t.Fatalf("ListVersions() = %#v, %v, want pdf untouched", versions, err)
	}
	if unmanaged := listUnmanagedSkills(t, f.lib); len(unmanaged) != 1 || unmanaged[0].Scope != codex {
		t.Fatalf("ListUnmanagedSkills() = %#v, want the variant still unmanaged", unmanaged)
	}
	// 用户可以清空来源,作为没有来源的技能入库。
	result, err = f.lib.FetchUnmanaged(ctx, library.FetchUnmanagedRequest{Scope: codex, Name: "pdf"})
	if err != nil || result.Skill.CollectionID != 0 {
		t.Fatalf("FetchUnmanaged() without a source = %#v, %v, want a skill without a source", result, err)
	}
}

func TestFetchUnmanagedSourceErrorsLeaveEverythingUnchanged(t *testing.T) {
	ctx := t.Context()
	f := newSourcedSkillFixture(t)
	writeFile(t, filepath.Join(f.home, ".claude", "skills", "notes", "SKILL.md"), []byte(skillDocument("notes", "mine")), 0o644)
	f.writeDisk(t, "v1")
	local := t.TempDir()
	before := snapshotTree(t, f.home)
	for _, tc := range []struct {
		name string
		req  library.FetchUnmanagedRequest
		code library.ErrorCode
		want library.ErrorDetails
	}{
		{"skill not in the source", library.FetchUnmanagedRequest{Name: "notes", SourceInput: "owner/repo", SourceRef: "main"}, library.NotFound, library.ErrorDetails{Name: "notes"}},
		{"unreachable", library.FetchUnmanagedRequest{Name: "pdf", SourceInput: "https://gitlab.com/group/missing.git"}, library.SourceUnreachable, library.ErrorDetails{}},
		{"invalid input", library.FetchUnmanagedRequest{Name: "pdf", SourceInput: "not a source"}, library.InvalidArgument, library.ErrorDetails{Field: "source_input"}},
		{"ref on a local directory", library.FetchUnmanagedRequest{Name: "pdf", SourceInput: local, SourceRef: "main"}, library.InvalidArgument, library.ErrorDetails{Field: "source_ref"}},
		{"ref without a source", library.FetchUnmanagedRequest{Name: "pdf", SourceRef: "main"}, library.InvalidArgument, library.ErrorDetails{Field: "source_ref"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "unreachable" {
				isolateGit(t)
			}
			tc.req.Scope = f.claude
			_, err := f.lib.FetchUnmanaged(ctx, tc.req)
			got := requireLibraryError(t, err, tc.code)
			if tc.want != (library.ErrorDetails{}) && got.Details != tc.want {
				t.Fatalf("details = %#v, want %#v", got.Details, tc.want)
			}
		})
	}
	if skills, err := f.lib.ListSkills(ctx); err != nil || len(skills) != 0 {
		t.Fatalf("ListSkills() = %#v, %v, want an empty library", skills, err)
	}
	if after := snapshotTree(t, f.home); !reflect.DeepEqual(after, before) {
		t.Fatalf("failed fetches changed disk: before %v, after %v", before, after)
	}
	if err := os.RemoveAll(local); err != nil {
		t.Fatal(err)
	}
}

// 来源记录里的哈希对不上磁盘,却等于来源现在的那份:装的就是来源现在这一版,磁盘不一样是用户改过,作者之后没动过。
// 入库的版本是基于来源这一版的修改,和用目录里的修改更新之后一样:作者没再改时没有更新,作者再改才报、写明会换掉修改。
func TestFetchUnmanagedEditedAfterInstallingTheLatestFollowsOnlyTheAuthor(t *testing.T) {
	for _, records := range []string{"global", "project"} {
		t.Run(records, func(t *testing.T) {
			ctx := t.Context()
			f := newSourcedSkillFixture(t)
			scope := f.claude
			folder := f.folder
			if records == "global" {
				writeSourceRecords(t, filepath.Join(f.home, ".agents", ".skill-lock.json"), 3, map[string]map[string]string{
					"pdf": {"source": "owner/repo", "sourceType": "github", "sourceUrl": "https://github.com/owner/repo.git", "ref": "main", "skillFolderHash": f.v1Tree},
				})
			} else {
				project := t.TempDir()
				registered, err := f.lib.RegisterProject(ctx, project)
				if err != nil {
					t.Fatal(err)
				}
				scope = library.ProjectScope(library.AgentClaudeCode, registered.ID)
				folder = filepath.Join(scopeDirectory(t, f.lib, scope), "pdf")
				writeSourceRecords(t, filepath.Join(project, "skills-lock.json"), 1, map[string]map[string]string{
					"pdf": {"source": "owner/repo", "sourceType": "github", "ref": "main", "computedHash": computedHash(f.v1Files)},
				})
			}
			writePDFFolder(t, folder, "edited")

			result, err := f.lib.FetchUnmanaged(ctx, library.FetchUnmanagedRequest{Scope: scope, Name: "pdf", SourceInput: "owner/repo", SourceRef: "main"})
			if err != nil {
				t.Fatal(err)
			}
			skill := result.Skill
			head := gitRevParse(t, f.repo, "HEAD")
			if skill.Revision != head || !skill.UpdatedAt.IsZero() || !skill.Edited {
				t.Fatalf("FetchUnmanaged() = %#v, want the source version the edits are based on (%s) and no update yet", skill, head)
			}
			version := f.currentVersion(t, skill)
			if version.MaybeEdited || version.BasedOn == 0 || version.Revision != head {
				t.Fatalf("version = %#v, want an edit based on the source's version", version)
			}
			base, err := f.lib.GetVersion(ctx, version.BasedOn)
			if err != nil || base.Revision != head || base.BasedOn != 0 {
				t.Fatalf("base version = %#v, %v, want the source's version at %s", base, err, head)
			}
			if files := skillFiles(t, f.lib, skill); string(files[0].Content) != skillDocument("pdf", "edited") {
				t.Fatalf("files = %#v, want what is on disk", files)
			}
			requireScopeStatus(t, f.lib, skill.ID, scope, library.SkillScopeCurrent)

			// 作者没再改:第一次检查更新没有更新。
			requireCheck(t, f.lib, skill.ID, library.UpdateCurrent, false, false)
			if _, planned := f.planned(t, skill); !planned.Edited || planned.Change != library.PlannedUnchanged {
				t.Fatalf("planned = %#v, want no change on an edited skill", planned)
			}
			// 作者再改:报有新版本,更新会换掉修改。
			f.publish(t, "v2")
			requireCheck(t, f.lib, skill.ID, library.UpdateAvailable, false, true)
			if _, planned := f.planned(t, skill); !planned.Edited || planned.Change != library.PlannedChanged {
				t.Fatalf("planned = %#v, want a change marked as edited", planned)
			}
			// 勾上更新换成作者的新版,能回滚回这份修改,回滚后照旧是基于来源那一版的修改。
			updated, err := updateSkill(t, f.lib, ctx, skill.ID)
			if err != nil {
				t.Fatal(err)
			}
			if updated.Skill.Edited || string(skillFiles(t, f.lib, updated.Skill)[0].Content) != skillDocument("pdf", "v2") {
				t.Fatalf("updated = %#v, want the author's new version", updated.Skill)
			}
			history, err := f.lib.ListUpdateHistory(ctx, library.UpdateHistoryFilter{SkillID: skill.ID})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.lib.Rollback(ctx, history[0].ID); err != nil {
				t.Fatal(err)
			}
			rolledBack, err := f.lib.GetSkill(ctx, skill.ID)
			if err != nil || rolledBack.VersionID != skill.VersionID || !rolledBack.Edited || rolledBack.Revision != head {
				t.Fatalf("GetSkill() after rollback = %#v, %v, want the edits based on %s back", rolledBack, err, head)
			}
			if _, planned := f.planned(t, rolledBack); !planned.Edited || planned.Change != library.PlannedChanged {
				t.Fatalf("planned after rollback = %#v, want the author's change still marked as edited", planned)
			}
		})
	}
}

// 项目记录里的内容哈希不算可执行位:作者只改了脚本的可执行位时,记录照样等于来源现在的那份,却不能断定装的就是这一版。
// 磁盘上的文件模式和来源里的不一样时,保守地当作说不清有没有修改:第一次检查更新就报有新版本,作者的权限改动不被漏掉。
func TestFetchUnmanagedDoesNotTakeAModeOnlyChangeAsTheVersionTheEditsAreBasedOn(t *testing.T) {
	ctx := t.Context()
	f := newSourcedSkillFixture(t)
	project := t.TempDir()
	registered, err := f.lib.RegisterProject(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	scope := library.ProjectScope(library.AgentClaudeCode, registered.ID)
	folder := filepath.Join(scopeDirectory(t, f.lib, scope), "pdf")
	writeSourceRecords(t, filepath.Join(project, "skills-lock.json"), 1, map[string]map[string]string{
		"pdf": {"source": "owner/repo", "sourceType": "github", "ref": "main", "computedHash": computedHash(f.v1Files)},
	})
	writePDFFolder(t, folder, "edited")
	if err := os.Chmod(filepath.Join(f.repo, "skills", "pdf", "scripts", "run.sh"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitGit(t, f.repo, "run.sh is no longer executable")
	f.api.Swap(inspectGitHubRepo(t, f.repo))

	result, err := f.lib.FetchUnmanaged(ctx, library.FetchUnmanagedRequest{Scope: scope, Name: "pdf", SourceInput: "owner/repo", SourceRef: "main"})
	if err != nil {
		t.Fatal(err)
	}
	skill := result.Skill
	if version := f.currentVersion(t, skill); !version.MaybeEdited || version.BasedOn != 0 || skill.Revision != "" {
		t.Fatalf("version = %#v, skill = %#v, want a possible edit with an unknown source version", version, skill)
	}
	requireCheck(t, f.lib, skill.ID, library.UpdateAvailable, false, true)
}
