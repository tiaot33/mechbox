package library_test

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tiaot33/mechbox/internal/library"
)

// 按内容认出作用域里的技能是库里的哪个、哪一版:同一个技能就是一个技能

// recognizeFixture 是一个 HOME、一个库和几个 well-known 站点(每个站点的技能都是单个 SKILL.md)。
type recognizeFixture struct {
	lib    *library.Library
	home   string
	site   *searchedSite
	claude library.Scope
	codex  library.Scope
	sites  map[string]map[string]string
}

func newRecognizeFixture(t *testing.T) *recognizeFixture {
	t.Helper()
	isolateGit(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	return &recognizeFixture{
		lib: openTestLibrary(t), home: home, site: serveSearchedSite(t, nil),
		claude: library.GlobalScope(library.AgentClaudeCode), codex: library.GlobalScope(library.AgentCodex),
		sites: map[string]map[string]string{},
	}
}

// publish 让站点 base 上的技能 name 是 body。
func (f *recognizeFixture) publish(t *testing.T, base, name, body string) {
	t.Helper()
	if f.sites[base] == nil {
		f.sites[base] = map[string]string{}
	}
	f.sites[base][name] = body
	var artifacts []artifact
	for each, content := range f.sites[base] {
		artifacts = append(artifacts, artifact{name: each, kind: "skill-md", content: []byte(skillDocument(each, content))})
	}
	artifactIndex(t, f.site.site, base, artifacts)
}

func (f *recognizeFixture) fetch(t *testing.T, base, name string) library.Skill {
	t.Helper()
	return fetchSkills(t, f.lib, mustPreview(t, f.lib, library.PreviewFetchRequest{Input: base}), []string{name})[0]
}

func (f *recognizeFixture) install(t *testing.T, skill library.Skill, scopes ...library.Scope) {
	t.Helper()
	for _, scope := range scopes {
		if err := f.lib.Install(t.Context(), skill.ID, scope, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}
}

func (f *recognizeFixture) write(t *testing.T, scope library.Scope, name, body string) {
	t.Helper()
	writeFile(t, filepath.Join(scopeDirectory(t, f.lib, scope), name, "SKILL.md"), []byte(skillDocument(name, body)), 0o644)
}

func (f *recognizeFixture) project(t *testing.T) library.Scope {
	t.Helper()
	project, err := f.lib.RegisterProject(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return library.ProjectScope(library.AgentClaudeCode, project.ID)
}

func unmanagedIn(t *testing.T, lib *library.Library, scope library.Scope, name string) library.UnmanagedSkill {
	t.Helper()
	for _, skill := range listUnmanagedSkills(t, lib) {
		if skill.Scope == scope && skill.Name == name {
			return skill
		}
	}
	t.Fatalf("%s is not unmanaged in %v", name, scope)
	return library.UnmanagedSkill{}
}

func inspectionsOf(t *testing.T, lib *library.Library, id library.SkillID) map[library.Scope]library.SkillScopeInspection {
	t.Helper()
	inspections, err := lib.InspectSkillScopes(t.Context(), library.SkillScopeFilter{SkillID: id})
	if err != nil {
		t.Fatal(err)
	}
	result := map[library.Scope]library.SkillScopeInspection{}
	for _, inspection := range inspections {
		result[inspection.Scope] = inspection
	}
	return result
}

func TestUnmanagedCopyOfALibrarySkillIsThatSkill(t *testing.T) {
	ctx := t.Context()
	f := newRecognizeFixture(t)
	f.publish(t, "https://author.example", "alpha", "v1")
	alpha := f.fetch(t, "https://author.example", "alpha")
	f.install(t, alpha, f.claude)
	// Codex 全局里有一份一模一样的,npx skills 的来源记录说它来自一个 fork。
	f.write(t, f.codex, "alpha", "v1")
	writeSourceRecords(t, globalRecordsPath(f.home), 3, map[string]map[string]string{
		"alpha": {"source": "fork.example", "sourceType": "well-known", "sourceUrl": "https://fork.example"},
	})
	f.publish(t, "https://fork.example", "alpha", "v1")
	// beta 不在库里,照旧认不出。
	f.write(t, f.codex, "beta", "beta")

	here := unmanagedIn(t, f.lib, f.codex, "alpha")
	if here.SkillID != alpha.ID || here.VersionID != alpha.VersionID || here.SourceLatestOf != 0 {
		t.Fatalf("here = %#v, want alpha's current version", here)
	}
	// 库里的技能排在来源记录前面:不再预填 fork。
	if here.SourceInput != "" || here.SourceRef != "" {
		t.Fatalf("copy source = %q %q, want the library skill's", here.SourceInput, here.SourceRef)
	}
	if beta := unmanagedIn(t, f.lib, f.codex, "beta"); beta.SkillID != 0 || beta.VersionID != 0 || beta.SourceLatestOf != 0 {
		t.Fatalf("beta = %#v", beta)
	}
	// 已经装在这个作用域的技能不算:Claude Code 全局里装着 alpha,另一个同样内容的文件夹不认作 alpha。
	writeFile(t, filepath.Join(scopeDirectory(t, f.lib, f.claude), "alpha-copy", "SKILL.md"), []byte(skillDocument("alpha", "v1")), 0o644)
	if other := unmanagedIn(t, f.lib, f.claude, "alpha-copy"); other.SkillID != 0 {
		t.Fatalf("alpha-here = %#v, want unrecognized in a scope alpha is installed in", other)
	}

	// 整批入库确认里也是库里的 alpha:不带来源,默认勾上。
	planned := plannedNamed(t, planFetchUnmanaged(t, f.lib, f.codex), "alpha")[0]
	if planned.SkillID != alpha.ID || planned.VersionID != alpha.VersionID || planned.SourceInput != "" || !planned.Selected {
		t.Fatalf("planned = %#v", planned)
	}

	// 用户手动填了 fork 的链接入库,也只给 alpha 补记这一处安装,不多出 fork 的集合。
	result, err := f.lib.FetchUnmanaged(ctx, library.FetchUnmanagedRequest{Scope: f.codex, Name: "alpha", SourceInput: "https://fork.example"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Matched || result.Skill.ID != alpha.ID {
		t.Fatalf("fetched = %#v, want alpha matched", result)
	}
	collections, err := f.lib.ListCollections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(collections) != 1 || collections[0].ID != alpha.CollectionID {
		t.Fatalf("collections = %#v, want only author.example", collections)
	}
	if skills, err := f.lib.ListSkills(ctx); err != nil || len(skills) != 1 {
		t.Fatalf("skills = %v, %v", skillNames(skills), err)
	}
	if status := inspectionsOf(t, f.lib, alpha.ID)[f.codex].Status; status != library.SkillScopeCurrent {
		t.Fatalf("codex = %s, want current", status)
	}
}

func TestUnmanagedCopyOfAnOlderVersionIsThatVersion(t *testing.T) {
	ctx := t.Context()
	f := newRecognizeFixture(t)
	f.publish(t, "https://author.example", "alpha", "v1")
	alpha := f.fetch(t, "https://author.example", "alpha")
	f.publish(t, "https://author.example", "alpha", "v2")
	if _, err := updateSkill(t, f.lib, ctx, alpha.ID); err != nil {
		t.Fatal(err)
	}
	project := f.project(t)
	f.write(t, project, "alpha", "v1")

	here := unmanagedIn(t, f.lib, project, "alpha")
	if here.SkillID != alpha.ID || here.VersionID != alpha.VersionID {
		t.Fatalf("here = %#v, want alpha's first version %d", here, alpha.VersionID)
	}
	// 按作者的来源入库:不报集合里已有同名技能,补记安装,巡检为落后。
	result, err := f.lib.FetchUnmanaged(ctx, library.FetchUnmanagedRequest{Scope: project, Name: "alpha", SourceInput: "https://author.example"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Matched || result.Skill.ID != alpha.ID {
		t.Fatalf("fetched = %#v", result)
	}
	if inspection := inspectionsOf(t, f.lib, alpha.ID)[project]; inspection.Status != library.SkillScopeStale || inspection.VersionID != alpha.VersionID {
		t.Fatalf("project = %#v, want stale at the first version", inspection)
	}
}

func TestCopyUpdatedOutsideTheLibraryToTheAuthorsVersionIsANewVersion(t *testing.T) {
	ctx := t.Context()
	f := newRecognizeFixture(t)
	f.publish(t, "https://author.example", "alpha", "v1")
	alpha := f.fetch(t, "https://author.example", "alpha")
	f.install(t, alpha, f.claude, f.codex)
	project := f.project(t)
	// 作者发了 v2。Codex 全局用命令行工具更新到了 v2,Claude Code 全局是用户自己改的,项目里另有一份 v2。
	f.publish(t, "https://author.example", "alpha", "v2")
	f.write(t, f.codex, "alpha", "v2")
	f.write(t, f.claude, "alpha", "my edit")
	f.write(t, project, "alpha", "v2")

	// 还没检查更新时不知道作者的 v2。
	if here := unmanagedIn(t, f.lib, project, "alpha"); here.SourceLatestOf != 0 || here.SkillID != 0 {
		t.Fatalf("here before checking = %#v", here)
	}
	if inspection := inspectionsOf(t, f.lib, alpha.ID)[f.codex]; inspection.Status != library.SkillScopeModified || inspection.SourceLatest {
		t.Fatalf("codex before checking = %#v", inspection)
	}
	if _, err := f.lib.CheckUpdates(ctx, []library.CollectionID{alpha.CollectionID}); err != nil {
		t.Fatal(err)
	}

	here := unmanagedIn(t, f.lib, project, "alpha")
	if here.SourceLatestOf != alpha.ID || here.SkillID != 0 || here.VersionID != 0 {
		t.Fatalf("here = %#v, want the author's latest of alpha", here)
	}
	planned := plannedNamed(t, planFetchUnmanaged(t, f.lib, project), "alpha")[0]
	if planned.SourceLatestOf != alpha.ID || planned.Selected {
		t.Fatalf("planned = %#v, want the author's latest, not selected", planned)
	}
	inspections := inspectionsOf(t, f.lib, alpha.ID)
	if codex := inspections[f.codex]; codex.Status != library.SkillScopeModified || !codex.SourceLatest {
		t.Fatalf("codex = %#v, want modified to the author's latest", codex)
	}
	if claude := inspections[f.claude]; claude.Status != library.SkillScopeModified || claude.SourceLatest {
		t.Fatalf("claude = %#v, want the user's edit", claude)
	}
	// 跟着作者走就是更新:更新计划里那一处同样标出。
	plan, err := f.lib.PlanUpdate(ctx, alpha.CollectionID)
	if err != nil {
		t.Fatal(err)
	}
	latest := map[library.Scope]bool{}
	for _, install := range plan.Installs {
		latest[install.Scope] = install.SourceLatest
	}
	if want := map[library.Scope]bool{f.codex: true, f.claude: false}; !reflect.DeepEqual(latest, want) {
		t.Fatalf("plan installs = %v, want %v", latest, want)
	}
	// 只勾 Codex 全局、不同意覆盖:那里已经是这一版,不用再换。
	result, err := f.lib.Update(ctx, library.UpdateRequest{Plan: plan.ID, Skills: []library.SkillID{alpha.ID}, Installs: []library.ReinstallRequest{{SkillID: alpha.ID, Scope: f.codex}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Installs) != 1 || result.Installs[0].Outcome != library.ReinstallCurrent {
		t.Fatalf("installs = %#v, want codex already current", result.Installs)
	}
	updated := result.Skills[0].Skill
	inspections = inspectionsOf(t, f.lib, alpha.ID)
	if inspections[f.codex].Status != library.SkillScopeCurrent || inspections[f.claude].Status != library.SkillScopeModified {
		t.Fatalf("after update = %#v", inspections)
	}
	// 项目里那份现在就是库里的 alpha。
	here = unmanagedIn(t, f.lib, project, "alpha")
	if here.SkillID != alpha.ID || here.VersionID != updated.VersionID || here.SourceLatestOf != 0 {
		t.Fatalf("copy after update = %#v", here)
	}
	if fetched, err := f.lib.FetchUnmanaged(ctx, library.FetchUnmanagedRequest{Scope: project, Name: "alpha", SourceInput: "https://author.example"}); err != nil || !fetched.Matched {
		t.Fatalf("fetched = %#v, %v", fetched, err)
	}
}

func TestFindUnmanagedSourcesPrefersSourcesTheLibraryFollows(t *testing.T) {
	ctx := t.Context()
	f := newRecognizeFixture(t)
	body := "one\ntwo\nthree\nfour\nfive\nsix"
	// 库里在跟 author 和 mirror 两个来源。alpha 在 fork 和 mirror 上和磁盘一模一样,在 author 上差一行。
	f.publish(t, "https://author.example", "other", "other")
	f.publish(t, "https://author.example", "alpha", "one\ntwo\nthree\nfour\nfive\nseven")
	f.publish(t, "https://mirror.example", "gamma", "gamma")
	f.publish(t, "https://mirror.example", "alpha", body)
	f.publish(t, "https://fork.example", "alpha", body)
	other := f.fetch(t, "https://author.example", "other")
	gamma := f.fetch(t, "https://mirror.example", "gamma")
	f.write(t, f.claude, "alpha", body)
	// Codex 全局里的 other 就是库里的那个:不去 skills.sh 找。
	f.write(t, f.codex, "other", "other")
	f.site.searchResults(t, "alpha", [2]string{"alpha", "fork.example"}, [2]string{"alpha", "mirror.example"}, [2]string{"alpha", "author.example"})
	f.site.searchResults(t, "other", [2]string{"other", "fork.example"})

	folders := []library.FolderInScope{{Scope: f.claude, Name: "alpha"}, {Scope: f.codex, Name: "other"}}
	// 在跟的来源优先于得分,几个都在跟时按集合的先后:author 在前。
	want := []library.FoundUnmanagedSource{{Scope: f.claude, Name: "alpha", SourceInput: "https://author.example"}}
	if found := findUnmanagedSources(t, f.lib, folders...); !reflect.DeepEqual(found, want) {
		t.Fatalf("found = %#v, want %#v", found, want)
	}
	if got := f.site.requested("skills.sh/api/search"); got != 1 {
		t.Fatalf("searched skills.sh %d times, want only for alpha", got)
	}
	// 用户把 mirror 拖到前面,就取 mirror。
	if err := f.lib.ReorderCollections(ctx, []library.CollectionID{gamma.CollectionID, other.CollectionID}); err != nil {
		t.Fatal(err)
	}
	want[0].SourceInput = "https://mirror.example"
	if found := findUnmanagedSources(t, f.lib, folders...); !reflect.DeepEqual(found, want) {
		t.Fatalf("found after reorder = %#v, want %#v", found, want)
	}
}
