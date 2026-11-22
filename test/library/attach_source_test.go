package library_test

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tiaot33/mechbox/internal/library"
)

// 给没有来源的技能接上来源记录里记着的来源

// sourcelessPDF 在 Claude Code 全局放一份 body 的 pdf,不带来源入库,返回这个没有来源的技能。
func (f sourcedSkillFixture) sourcelessPDF(t *testing.T, body string) library.Skill {
	t.Helper()
	f.writeDisk(t, body)
	result, err := f.lib.FetchUnmanaged(t.Context(), library.FetchUnmanagedRequest{Scope: f.claude, Name: "pdf"})
	if err != nil || result.Matched || result.Skill.CollectionID != 0 {
		t.Fatalf("FetchUnmanaged() without a source = %#v, %v, want a new skill without a source", result, err)
	}
	return result.Skill
}

// recordGlobal 在全局来源记录里记下 pdf 来自 owner/repo 的 main,哈希是 hash。
func (f sourcedSkillFixture) recordGlobal(t *testing.T, hash string) {
	t.Helper()
	writeSourceRecords(t, globalRecordsPath(f.home), 3, map[string]map[string]string{
		"pdf": {"source": "owner/repo", "sourceType": "github", "sourceUrl": "https://github.com/owner/repo.git", "ref": "main", "skillFolderHash": hash},
	})
}

func (f sourcedSkillFixture) attach(t *testing.T, id library.SkillID) library.Skill {
	t.Helper()
	skill, err := f.lib.AttachSource(t.Context(), library.AttachSourceRequest{SkillID: id, SourceInput: "owner/repo", SourceRef: "main"})
	if err != nil {
		t.Fatal(err)
	}
	return skill
}

func TestAttachSourceRecordedForAnAdoptedCopy(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newSourcedSkillFixture(t)
	pdf := f.sourcelessPDF(t, "v1")
	codex := library.GlobalScope(library.AgentCodex)
	writePDFFolder(t, filepath.Join(scopeDirectory(t, f.lib, codex), "pdf"), "v1")
	f.recordGlobal(t, f.v1Tree)
	if unmanaged := listUnmanagedSkills(t, f.lib); len(unmanaged) != 0 {
		t.Fatalf("ListUnmanagedSkills() = %#v, want the Codex copy adopted", unmanaged)
	}
	here := requireScopeStatus(t, f.lib, pdf.ID, codex, library.SkillScopeCurrent)
	if here.SourceInput != "owner/repo" || here.SourceRef != "main" {
		t.Fatalf("Codex inspection = %#v, want the recorded source owner/repo · main", here)
	}
	before := snapshotTree(t, f.home)
	group, err := f.lib.CreateGroup(ctx, "docs")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.lib.AddSkillToGroup(ctx, group.ID, pdf.ID); err != nil {
		t.Fatal(err)
	}
	scopesBefore, err := f.lib.ListSkillScopes(ctx, library.SkillScopeFilter{SkillID: pdf.ID})
	if err != nil {
		t.Fatal(err)
	}

	skill := f.attach(t, pdf.ID)
	head := gitRevParse(t, f.repo, "HEAD")
	if skill.ID != pdf.ID || skill.CollectionID == 0 || skill.Source.Type != library.SourceGitHub || skill.Revision != head || skill.VersionID != pdf.VersionID {
		t.Fatalf("AttachSource() = %#v, want pdf in the owner/repo collection at %s, same version", skill, head)
	}
	if after := snapshotTree(t, f.home); !reflect.DeepEqual(after, before) {
		t.Fatalf("AttachSource changed disk: before %v, after %v", before, after)
	}
	if versions, err := f.lib.ListVersions(ctx, pdf.ID); err != nil || len(versions) != 1 || versions[0].MaybeEdited {
		t.Fatalf("ListVersions() = %#v, %v, want the one version, as the source's", versions, err)
	}
	if scopes, err := f.lib.ListSkillScopes(ctx, library.SkillScopeFilter{SkillID: pdf.ID}); err != nil || !reflect.DeepEqual(scopes, scopesBefore) {
		t.Fatalf("ListSkillScopes() = %#v, %v, want %#v", scopes, err, scopesBefore)
	}
	if groups, err := f.lib.ListGroups(ctx); err != nil || len(groups) != 1 || !reflect.DeepEqual(groups[0].Members, []library.SkillID{pdf.ID}) {
		t.Fatalf("ListGroups() = %#v, %v, want pdf still in docs", groups, err)
	}
	if here := requireScopeStatus(t, f.lib, pdf.ID, codex, library.SkillScopeCurrent); here.SourceInput != "" {
		t.Fatalf("Codex inspection = %#v, want no recorded source once the skill has one", here)
	}
	requireCheck(t, f.lib, pdf.ID, library.UpdateCurrent, false, false)

	// 接上之后和从来源入库的一样:作者再改,照常有新版本,更新计划不提示换掉修改。
	f.publish(t, "v2")
	requireCheck(t, f.lib, pdf.ID, library.UpdateAvailable, false, true)
	if _, planned := f.planned(t, skill); planned.Edited || planned.Change != library.PlannedChanged {
		t.Fatalf("planned = %#v, want a plain change", planned)
	}

	_, err = f.lib.AttachSource(ctx, library.AttachSourceRequest{SkillID: pdf.ID, SourceInput: "owner/repo", SourceRef: "main"})
	requireErrorDetails(t, err, library.FailedPrecondition, library.ErrorDetails{SkillID: pdf.ID})
}

func TestAttachSourceBehindTheAuthor(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		hash        func(f sourcedSkillFixture) string
		maybeEdited bool
	}{
		{name: "record matches the library", hash: func(f sourcedSkillFixture) string { return f.v1Tree }},
		{name: "record does not match", hash: func(sourcedSkillFixture) string { return "0000" }, maybeEdited: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			f := newSourcedSkillFixture(t)
			pdf := f.sourcelessPDF(t, "v1")
			f.recordGlobal(t, tc.hash(f))
			f.publish(t, "v2")

			skill := f.attach(t, pdf.ID)
			if skill.Revision != "" || skill.Edited != tc.maybeEdited {
				t.Fatalf("AttachSource() = %#v, want an unknown revision, edited %v", skill, tc.maybeEdited)
			}
			if versions, err := f.lib.ListVersions(ctx, pdf.ID); err != nil || len(versions) != 1 || versions[0].MaybeEdited != tc.maybeEdited {
				t.Fatalf("ListVersions() = %#v, %v, want one version with MaybeEdited %v", versions, err, tc.maybeEdited)
			}
			requireCheck(t, f.lib, pdf.ID, library.UpdateAvailable, false, true)
			if _, planned := f.planned(t, skill); planned.Edited != tc.maybeEdited {
				t.Fatalf("planned = %#v, want Edited %v", planned, tc.maybeEdited)
			}
		})
	}
}

func TestAttachSourceRecordedForATeamCopy(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newSourcedSkillFixture(t)
	pdf := f.sourcelessPDF(t, "v1")
	project := t.TempDir()
	registered, err := f.lib.RegisterProject(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	scope := library.ProjectScope(library.AgentClaudeCode, registered.ID)
	writePDFFolder(t, filepath.Join(scopeDirectory(t, f.lib, scope), "pdf"), "v1")
	writeSourceRecords(t, filepath.Join(project, "skills-lock.json"), 1, map[string]map[string]string{
		"pdf": {"source": "owner/repo", "sourceType": "github", "ref": "main", "computedHash": computedHash(f.v1Files)},
	})

	// 记在项目 skills-lock.json 里的不自动纳入,照旧显示在 pdf 下面,带着来源记录认出的来源。
	here := unmanagedIn(t, f.lib, scope, "pdf")
	if here.SkillID != pdf.ID || !here.InProjectLock || here.SourceInput != "owner/repo" || here.SourceRef != "main" {
		t.Fatalf("unmanaged = %#v, want pdf in the project lock, recorded from owner/repo · main", here)
	}
	plan, err := f.lib.PlanFetchUnmanaged(ctx, library.PlanFetchUnmanagedRequest{Scopes: []library.Scope{scope}})
	if err != nil || len(plan) != 1 || plan[0].SkillID != pdf.ID || plan[0].SourceInput != "" {
		t.Fatalf("PlanFetchUnmanaged() = %#v, %v, want pdf only recorded, without a source to fetch from", plan, err)
	}

	f.publish(t, "v2")
	skill := f.attach(t, pdf.ID)
	if skill.CollectionID == 0 || skill.Edited {
		t.Fatalf("AttachSource() = %#v, want the author's older version in the collection", skill)
	}
	if here := unmanagedIn(t, f.lib, scope, "pdf"); here.SkillID != pdf.ID || here.SourceInput != "" {
		t.Fatalf("unmanaged = %#v, want no recorded source once pdf has one", here)
	}
}

func TestAttachSourceWithSameNameInTheCollection(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newSourcedSkillFixture(t)
	pdf := f.sourcelessPDF(t, "mine")
	sourced := fetchSkills(t, f.lib, mustPreview(t, f.lib, gitHubRequest()), []string{"pdf"})[0]

	_, err := f.lib.AttachSource(ctx, library.AttachSourceRequest{SkillID: pdf.ID, SourceInput: "owner/repo", SourceRef: "main"})
	requireErrorDetails(t, err, library.AlreadyExists, library.ErrorDetails{SkillID: sourced.ID, Name: "pdf"})
	if skill, err := f.lib.GetSkill(ctx, pdf.ID); err != nil || skill.CollectionID != 0 {
		t.Fatalf("GetSkill() = %#v, %v, want pdf still without a source", skill, err)
	}
}

func TestAttachSourceErrors(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newSourcedSkillFixture(t)
	writeFile(t, filepath.Join(scopeDirectory(t, f.lib, f.claude), "xlsx", "SKILL.md"), []byte(skillDocument("xlsx", "v1")), 0o644)
	xlsx, err := f.lib.FetchUnmanaged(ctx, library.FetchUnmanagedRequest{Scope: f.claude, Name: "xlsx"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = f.lib.AttachSource(ctx, library.AttachSourceRequest{SkillID: xlsx.Skill.ID, SourceInput: "  "})
	requireErrorDetails(t, err, library.InvalidArgument, library.ErrorDetails{Field: "source_input"})
	_, err = f.lib.AttachSource(ctx, library.AttachSourceRequest{SkillID: 999, SourceInput: "owner/repo"})
	requireLibraryError(t, err, library.NotFound)
	_, err = f.lib.AttachSource(ctx, library.AttachSourceRequest{SkillID: xlsx.Skill.ID, SourceInput: "owner/repo", SourceRef: "main"})
	requireErrorDetails(t, err, library.NotFound, library.ErrorDetails{Name: "xlsx"})
	if skill, err := f.lib.GetSkill(ctx, xlsx.Skill.ID); err != nil || skill.CollectionID != 0 {
		t.Fatalf("GetSkill() = %#v, %v, want xlsx still without a source", skill, err)
	}
	if collections, err := f.lib.ListCollections(ctx); err != nil || len(collections) != 0 {
		t.Fatalf("ListCollections() = %#v, %v, want none", collections, err)
	}
}
