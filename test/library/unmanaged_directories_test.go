package library_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/tiaot33/mechbox/internal/library"
)

// cursorHome 添加读 ~/.agents/skills 与 ~/.cursor/skills 两个全局目录的 Cursor,返回库与这两个目录。
func cursorHome(t *testing.T) (lib *library.Library, agents, cursor string) {
	t.Helper()
	home := t.TempDir()
	setTestHome(t, home)
	lib = openTestLibrary(t)
	addFromPool(t, lib, "cursor")
	return lib, filepath.Join(home, ".agents", "skills"), filepath.Join(home, ".cursor", "skills")
}

func skillScopesOf(t *testing.T, lib *library.Library, id library.SkillID) map[library.Scope]library.SkillScope {
	t.Helper()
	listed, err := lib.ListSkillScopes(t.Context(), library.SkillScopeFilter{SkillID: id})
	if err != nil {
		t.Fatal(err)
	}
	scopes := map[library.Scope]library.SkillScope{}
	for _, skillScope := range listed {
		scopes[skillScope.Scope] = skillScope
	}
	return scopes
}

func unmanagedNamed(t *testing.T, lib *library.Library, scope library.Scope, name string) []library.UnmanagedSkill {
	t.Helper()
	var found []library.UnmanagedSkill
	for _, skill := range listUnmanagedSkills(t, lib) {
		if skill.Scope == scope && skill.Name == name {
			found = append(found, skill)
		}
	}
	return found
}

// Cursor 附加目录里的未纳管技能能扫出,原地入库:磁盘不动,记下它在 ~/.cursor/skills,巡检为当前版本,只读 ~/.agents/skills 的 Codex 不算已装。
// 新装的技能写进第 1 个目录,Codex 和 Cursor 都算已装。再把 foo 装到 Codex:安装计划指出 Cursor 会读到两份,装完 Cursor 的 foo 仍只有一处。
func TestFolderInAnAdditionalDirectoryIsFetchedInPlace(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	lib, agents, cursor := cursorHome(t)
	global, codex := library.GlobalScope("cursor"), library.GlobalScope(library.AgentCodex)
	writeUnmanagedSkill(t, cursor, "foo", "mine")
	found := unmanagedNamed(t, lib, global, "foo")
	if len(found) != 1 || found[0].Directory != cursor || found[0].Folder != filepath.Join(cursor, "foo") {
		t.Fatalf("unmanaged foo = %#v; want the folder in %s", found, cursor)
	}
	if len(unmanagedNamed(t, lib, codex, "foo")) != 0 {
		t.Fatal("Codex lists foo, but it does not read ~/.cursor/skills")
	}
	_, err := lib.FetchUnmanaged(ctx, library.FetchUnmanagedRequest{Scope: global, Directory: filepath.Join(t.TempDir(), "skills"), Name: "foo"})
	requireErrorDetails(t, err, library.InvalidArgument, library.ErrorDetails{Field: "directory"})
	before := snapshotTree(t, filepath.Dir(cursor))
	fetched, err := lib.FetchUnmanaged(ctx, library.FetchUnmanagedRequest{Scope: global, Directory: found[0].Directory, Name: "foo"})
	if err != nil {
		t.Fatal(err)
	}
	if after := snapshotTree(t, filepath.Dir(cursor)); !reflect.DeepEqual(after, before) {
		t.Fatalf("FetchUnmanaged changed the disk: before %v, after %v", before, after)
	}
	foo := fetched.Skill
	scopes := skillScopesOf(t, lib, foo.ID)
	if len(scopes) != 1 || scopes[global].Directory != cursor || scopes[global].Folder != filepath.Join(cursor, "foo") || scopes[global].SharedOnly {
		t.Fatalf("ListSkillScopes(foo) = %#v; want only Cursor, in %s", scopes, cursor)
	}
	inspections, err := lib.InspectSkillScopes(ctx, library.SkillScopeFilter{SkillID: foo.ID})
	if err != nil || len(inspections) != 1 || inspections[0].Status != library.SkillScopeCurrent {
		t.Fatalf("InspectSkillScopes(foo) = %#v, %v; want current", inspections, err)
	}

	bar := fetchNamedSkill(t, lib, "bar")
	if err := lib.Install(ctx, bar.ID, global, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	requireInstalledFolder(t, agents, "bar", "# bar\n")
	requireNoInstalledFolder(t, cursor, "bar")
	scopes = skillScopesOf(t, lib, bar.ID)
	if len(scopes) != 2 || scopes[global].Directory != agents || !scopes[codex].SharedOnly {
		t.Fatalf("ListSkillScopes(bar) = %#v; want Cursor in %s and Codex through it", scopes, agents)
	}

	plan, err := lib.PlanInstall(ctx, foo.ID, codex)
	if err != nil || plan.Status != library.InstallReady || plan.Folder != filepath.Join(agents, "foo") || !reflect.DeepEqual(plan.ReadsTwice, []library.Scope{global}) {
		t.Fatalf("PlanInstall(foo, codex) = %#v, %v; want ready in %s, Cursor reading it twice", plan, err, agents)
	}
	if err := lib.Install(ctx, foo.ID, codex, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	requireSkillFile(t, agents, "foo", "---\nname: foo\ndescription: mine\n---\n# foo\n")
	scopes = skillScopesOf(t, lib, foo.ID)
	if len(scopes) != 2 || scopes[global].Directory != cursor || scopes[codex].Directory != agents || len(scopes[global].SharedWith) != 0 {
		t.Fatalf("ListSkillScopes(foo) = %#v; want Cursor still only in %s", scopes, cursor)
	}
	if same := sameNameSkills(t, lib); len(same) != 0 {
		t.Fatalf("ListSameNameSkills() = %#v; want none for the same skill in two directories", same)
	}
}

// 在附加目录里接管的技能,更新、回滚、卸载、移除技能都作用在那个目录里的文件夹,第 1 个目录不动。
func TestInstallInAnAdditionalDirectoryIsUpdatedAndRemovedInPlace(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	lib, agents, cursor := cursorHome(t)
	global := library.GlobalScope("cursor")
	foo, source := fetchLocalSkill(t, lib, "foo", "v1")
	writeLocalSkill(t, filepath.Join(cursor, "foo"), "v1")
	listUnmanagedSkills(t, lib)
	if scopes := skillScopesOf(t, lib, foo.ID); scopes[global].Directory != cursor {
		t.Fatalf("ListSkillScopes(foo) = %#v; want the identical folder in %s taken over", scopes, cursor)
	}

	writeLocalSkill(t, source, "v2")
	plan, err := lib.PlanUpdate(ctx, foo.CollectionID)
	if err != nil || len(plan.Installs) != 1 || plan.Installs[0].Folder != filepath.Join(cursor, "foo") {
		t.Fatalf("PlanUpdate() = %#v, %v; want the folder in %s", plan.Installs, err, cursor)
	}
	result, err := lib.Update(ctx, library.UpdateRequest{Plan: plan.ID, Skills: []library.SkillID{foo.ID}, Installs: []library.ReinstallRequest{{SkillID: foo.ID, Scope: global}}})
	if err != nil {
		t.Fatal(err)
	}
	requireInstalledFolder(t, cursor, "foo", "v2")
	requireNoInstalledFolder(t, agents, "foo")
	if _, err := lib.Rollback(ctx, result.Record); err != nil {
		t.Fatal(err)
	}
	requireInstalledFolder(t, cursor, "foo", "v1")
	writeFile(t, filepath.Join(cursor, "foo", "SKILL.md"), []byte("edited"), 0o644)
	reinstalled, err := lib.Reinstall(ctx, []library.ReinstallRequest{{SkillID: foo.ID, Scope: global, Overwrite: true}})
	if err != nil || reinstalled[0].Outcome != library.Reinstalled {
		t.Fatalf("Reinstall() = %#v, %v", reinstalled, err)
	}
	requireInstalledFolder(t, cursor, "foo", "v1")
	requireNoInstalledFolder(t, agents, "foo")
	if err := lib.Uninstall(ctx, foo.ID, global, library.UninstallOptions{}); err != nil {
		t.Fatal(err)
	}
	requireNoInstalledFolder(t, cursor, "foo")

	writeLocalSkill(t, filepath.Join(cursor, "foo"), "v1")
	listUnmanagedSkills(t, lib)
	if err := lib.RemoveSkill(ctx, foo.ID, library.RemoveSkillOptions{}); err != nil {
		t.Fatal(err)
	}
	requireNoInstalledFolder(t, cursor, "foo")
}

// 旧版 npx skills 在 ~/.cursor/skills 留下指向 ~/.agents/skills 里已入库文件夹的链接:Cursor 下只算一处,不列未纳管、不报同名;
// 从 Codex 卸载时巡检给出这个链接(只算旧链接,不算拷贝),卸载把它一并删掉。
func TestLinkToAFolderInAnotherDirectoryCountsOnceAndGoesWithIt(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	lib, agents, cursor := cursorHome(t)
	global, codex := library.GlobalScope("cursor"), library.GlobalScope(library.AgentCodex)
	foo := fetchNamedSkill(t, lib, "foo")
	if err := lib.Install(ctx, foo.ID, codex, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cursor, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(cursor, "foo")
	if err := os.Symlink(filepath.Join(agents, "foo"), link); err != nil {
		t.Fatal(err)
	}
	if scopes := skillScopesOf(t, lib, foo.ID); len(scopes) != 2 || scopes[global].Directory != agents {
		t.Fatalf("ListSkillScopes(foo) = %#v; want Cursor once, in %s", scopes, agents)
	}
	if found := unmanagedNamed(t, lib, global, "foo"); len(found) != 0 {
		t.Fatalf("unmanaged foo = %#v; want the link not listed", found)
	}
	if same := sameNameSkills(t, lib); len(same) != 0 {
		t.Fatalf("ListSameNameSkills() = %#v; want none", same)
	}
	inspections, err := lib.InspectSkillScopes(ctx, library.SkillScopeFilter{SkillID: foo.ID, Scope: &codex})
	if err != nil || len(inspections) != 1 || !reflect.DeepEqual(inspections[0].StaleLinks, []string{link}) || len(inspections[0].Copies) != 0 {
		t.Fatalf("InspectSkillScopes(foo, codex) = %#v, %v; want the link %s to be removed with it, not listed as a copy", inspections, err, link)
	}
	if err := lib.Uninstall(ctx, foo.ID, codex, library.UninstallOptions{DeleteLinks: []string{link}}); err != nil {
		t.Fatal(err)
	}
	requireNoInstalledFolder(t, agents, "foo")
	if _, err := os.Lstat(link); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("link after uninstall: %v; want it removed", err)
	}
}

// ~/.cursor/skills 里有 ~/.agents/skills 里已入库技能内容一致的拷贝:不列出、不报同名,对它入库返回 FailedPrecondition;
// 库里的技能更新后对不上了,它作为未纳管技能出现,和新版一起标为同名。
func TestCopyInAnotherDirectoryIsHiddenUntilTheSkillMovesOn(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	lib, agents, cursor := cursorHome(t)
	global, codex := library.GlobalScope("cursor"), library.GlobalScope(library.AgentCodex)
	foo, source := fetchLocalSkill(t, lib, "foo", "v1")
	if err := lib.Install(ctx, foo.ID, codex, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	writeLocalSkill(t, filepath.Join(cursor, "foo"), "v1")
	if found := unmanagedNamed(t, lib, global, "foo"); len(found) != 0 {
		t.Fatalf("unmanaged foo = %#v; want the copy not listed", found)
	}
	if same := sameNameSkills(t, lib); len(same) != 0 {
		t.Fatalf("ListSameNameSkills() = %#v; want none", same)
	}
	_, err := lib.FetchUnmanaged(ctx, library.FetchUnmanagedRequest{Scope: global, Directory: cursor, Name: "foo"})
	requireErrorDetails(t, err, library.FailedPrecondition, library.ErrorDetails{Path: filepath.Join(cursor, "foo"), SkillID: foo.ID})
	if scopes := skillScopesOf(t, lib, foo.ID); len(scopes) != 2 || scopes[global].Directory != agents {
		t.Fatalf("ListSkillScopes(foo) = %#v; want Cursor only through %s", scopes, agents)
	}

	writeLocalSkill(t, source, "v2")
	plan, err := lib.PlanUpdate(ctx, foo.CollectionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Update(ctx, library.UpdateRequest{Plan: plan.ID, Skills: []library.SkillID{foo.ID}, Installs: []library.ReinstallRequest{{SkillID: foo.ID, Scope: codex}}}); err != nil {
		t.Fatal(err)
	}
	found := unmanagedNamed(t, lib, global, "foo")
	if len(found) != 1 || found[0].Directory != cursor || found[0].SkillID != foo.ID || found[0].VersionID != foo.VersionID {
		t.Fatalf("unmanaged foo = %#v; want the copy listed as foo's older version", found)
	}
	want := []library.SameNameSkill{{
		Kind: library.SameNameTwoDirectories, Name: "foo",
		First:  library.SameNameFolder{Scope: global, Directory: agents, Folder: filepath.Join(agents, "foo"), SkillID: foo.ID},
		Second: library.SameNameFolder{Scope: global, Directory: cursor, Folder: filepath.Join(cursor, "foo")},
	}}
	if same := sameNameSkills(t, lib); !reflect.DeepEqual(same, want) {
		t.Fatalf("ListSameNameSkills() = %#v, want %#v", same, want)
	}
}

// foo 装给 Cursor(写进 ~/.agents/skills),~/.cursor/skills 里有一份和它内容一致的拷贝:巡检这一处给出这份拷贝;
// 指向别处一份一致内容的链接也算;同名但内容不同的不算。从 Cursor 卸载后 Cursor 照样读得到拷贝,它被纳入,Cursor 仍算装着 foo。
func TestInspectionListsCopiesTheAgentStillReadsAfterRemoval(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	lib, agents, cursor := cursorHome(t)
	global := library.GlobalScope("cursor")
	foo, _ := fetchLocalSkill(t, lib, "foo", "v1")
	if err := lib.Install(ctx, foo.ID, global, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	copies := func() []string {
		t.Helper()
		inspections, err := lib.InspectSkillScopes(ctx, library.SkillScopeFilter{SkillID: foo.ID, Scope: &global})
		if err != nil || len(inspections) != 1 || inspections[0].Folder != filepath.Join(agents, "foo") || len(inspections[0].StaleLinks) != 0 {
			t.Fatalf("InspectSkillScopes(foo, cursor) = %#v, %v; want the install in %s, no stale links", inspections, err, agents)
		}
		return inspections[0].Copies
	}
	copied := filepath.Join(cursor, "foo")
	writeLocalSkill(t, copied, "edited")
	if got := copies(); len(got) != 0 {
		t.Fatalf("Copies with different content = %v; want none", got)
	}
	elsewhere := filepath.Join(t.TempDir(), "foo")
	writeLocalSkill(t, elsewhere, "v1")
	if err := os.RemoveAll(copied); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, copied); err != nil {
		t.Fatal(err)
	}
	if got := copies(); !reflect.DeepEqual(got, []string{copied}) {
		t.Fatalf("Copies with a link to the same content = %v; want [%s]", got, copied)
	}
	if err := os.Remove(copied); err != nil {
		t.Fatal(err)
	}
	writeLocalSkill(t, copied, "v1")
	if got := copies(); !reflect.DeepEqual(got, []string{copied}) {
		t.Fatalf("Copies = %v; want [%s]", got, copied)
	}

	if err := lib.Uninstall(ctx, foo.ID, global, library.UninstallOptions{}); err != nil {
		t.Fatal(err)
	}
	requireNoInstalledFolder(t, agents, "foo")
	listUnmanagedSkills(t, lib)
	if scopes := skillScopesOf(t, lib, foo.ID); scopes[global].Directory != cursor {
		t.Fatalf("ListSkillScopes(foo) after uninstall = %#v; want Cursor still installed through the copy in %s", scopes, cursor)
	}
}

// 同一个作用域两个目录里有两个不同的技能都叫 foo:都列出,按不知道标为同名;只读 ~/.agents/skills 的 Codex 那里不算同名。
func TestDifferentSkillsWithOneNameInTwoDirectoriesAreBothListed(t *testing.T) {
	t.Parallel()
	lib, agents, cursor := cursorHome(t)
	global := library.GlobalScope("cursor")
	writeUnmanagedSkill(t, agents, "foo", "one")
	writeUnmanagedSkill(t, cursor, "foo", "another")
	found := unmanagedNamed(t, lib, global, "foo")
	if len(found) != 2 || found[0].Directory != agents || found[1].Directory != cursor {
		t.Fatalf("unmanaged foo in Cursor = %#v; want both, in directory order", found)
	}
	want := []library.SameNameSkill{{
		Kind: library.SameNameTwoDirectories, Name: "foo",
		First:  library.SameNameFolder{Scope: global, Directory: agents, Folder: filepath.Join(agents, "foo")},
		Second: library.SameNameFolder{Scope: global, Directory: cursor, Folder: filepath.Join(cursor, "foo")},
	}}
	if same := sameNameSkills(t, lib); !reflect.DeepEqual(same, want) {
		t.Fatalf("ListSameNameSkills() = %#v, want %#v", same, want)
	}
	sources, err := lib.FindUnmanagedSources(t.Context(), library.FindUnmanagedSourcesRequest{Folders: []library.FolderInScope{{Scope: global, Directory: cursor, Name: "foo"}}})
	if err != nil || len(sources) != 0 {
		t.Fatalf("FindUnmanagedSources() = %#v, %v; want nothing found offline", sources, err)
	}
	fetched, err := lib.FetchUnmanaged(t.Context(), library.FetchUnmanagedRequest{Scope: global, Directory: cursor, Name: "foo"})
	if err != nil || fetched.Skill.Description != "another" {
		t.Fatalf("FetchUnmanaged(cursor/foo) = %#v, %v; want the folder in %s", fetched, err, cursor)
	}
	if found := unmanagedNamed(t, lib, global, "foo"); len(found) != 1 || found[0].Directory != agents {
		t.Fatalf("unmanaged foo in Cursor after fetch = %#v; want only the one in %s", found, agents)
	}
}

func sameNameSkills(t *testing.T, lib *library.Library) []library.SameNameSkill {
	t.Helper()
	same, err := lib.ListSameNameSkills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return slices.DeleteFunc(same, func(skill library.SameNameSkill) bool { return skill.Kind == library.SameNameGlobalAndProject })
}

// 旧链接只算认这一处的 Agent 的:另一个 Agent 的目录列表里也有这个目录,但它自己在前面的目录装着这个技能,
// 它别的目录里指向这一处的链接不是旧链接,卸载时留着。
func TestLinkOfAnAgentThatRecognizesAnotherPlaceIsNotStale(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	home := t.TempDir()
	setTestHome(t, home)
	lib := openTestLibrary(t)
	agents, links := filepath.Join(home, ".agents", "skills"), filepath.Join(home, ".links", "skills")
	other, err := lib.AddAgent(ctx, library.AgentInput{
		Name: "Other", GlobalDirectories: []string{filepath.Join(home, ".other", "skills"), agents, links}, ProjectDirectories: []string{".other/skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	foo := fetchNamedSkill(t, lib, "foo")
	codex := library.GlobalScope(library.AgentCodex)
	for _, scope := range []library.Scope{library.GlobalScope(other.Code), codex} {
		if err := lib.Install(ctx, foo.ID, scope, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(links, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(links, "foo")
	if err := os.Symlink(filepath.Join(agents, "foo"), link); err != nil {
		t.Fatal(err)
	}
	inspections, err := lib.InspectSkillScopes(ctx, library.SkillScopeFilter{SkillID: foo.ID, Scope: &codex})
	if err != nil || len(inspections) != 1 || len(inspections[0].StaleLinks) != 0 {
		t.Fatalf("InspectSkillScopes(foo, codex) = %#v, %v; want no stale links", inspections, err)
	}
	if err := lib.Uninstall(ctx, foo.ID, codex, library.UninstallOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("link after uninstall: %v; want it kept", err)
	}
}

// 注销项目、删除文件夹时,认那一处的 Agent 在项目里别的技能目录中指向它的旧链接一并删掉。
func TestUnregisterProjectRemovesStaleLinks(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	home := t.TempDir()
	setTestHome(t, home)
	lib := openTestLibrary(t)
	agent, err := lib.AddAgent(ctx, library.AgentInput{Name: "Two", GlobalDirectories: []string{"~/.two/skills"}, ProjectDirectories: []string{".one/skills", ".two/skills"}})
	if err != nil {
		t.Fatal(err)
	}
	project := registerProject(t, lib)
	foo := fetchNamedSkill(t, lib, "foo")
	if err := lib.Install(ctx, foo.ID, library.ProjectScope(agent.Code, project.ID), library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	links := filepath.Join(project.Path, ".two", "skills")
	if err := os.MkdirAll(links, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(links, "foo")
	if err := os.Symlink(filepath.Join(project.Path, ".one", "skills", "foo"), link); err != nil {
		t.Fatal(err)
	}
	if err := lib.UnregisterProject(ctx, project.ID, library.UnregisterProjectOptions{DeleteLinks: []string{link}}); err != nil {
		t.Fatal(err)
	}
	requireNoInstalledFolder(t, filepath.Join(project.Path, ".one", "skills"), "foo")
	if _, err := os.Lstat(link); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("link after unregistering: %v; want it removed", err)
	}
}

// 卸载前算会丢的提到按实际文件夹判断:记录在链接上、真实文件夹在 Cursor 的另一个目录里时,卸载只删链接,
// Cursor 里提到它的技能仍找得到它,只读链接那个目录的 Codex 找不到;同时卸载的技能,另一个目录里有同名的另一份时,
// 也不再算它会丢掉提到。
func TestLostMentionsFollowTheActualFolders(t *testing.T) {
	t.Parallel()
	lib, agents, cursor := cursorHome(t)
	skills := fetchMentionSource(t, lib, mentionSource(t, map[string]string{"entry": "Run /ship.\n", "ship": "# ship\n"}))
	global := library.GlobalScope("cursor")
	entry, ship := skills["entry"].ID, skills["ship"].ID
	install(t, lib, ship, global)
	install(t, lib, entry, global)
	if err := os.MkdirAll(cursor, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(agents, "ship"), filepath.Join(cursor, "ship")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(cursor, "ship"), filepath.Join(agents, "ship")); err != nil {
		t.Fatal(err)
	}
	requireMissingMentions(t, listLostMentions(t, lib, library.SkillInScope{SkillID: ship, Scope: global}), []library.MissingMention{
		{SkillID: entry, Scope: library.GlobalScope(library.AgentCodex), Name: "ship", MentionedSkillID: ship},
	})

	writeUnmanagedSkill(t, cursor, "entry", "another entry")
	requireMissingMentions(t, listLostMentions(t, lib,
		library.SkillInScope{SkillID: ship, Scope: global}, library.SkillInScope{SkillID: entry, Scope: global},
	), nil)
}

// 整批入库时,一项不填目录、另一项填第 1 个目录,是同一个文件夹:返回 InvalidArgument,什么都不入库。
func TestFetchUnmanagedSkillsRejectsTheSameFolderWrittenTwoWays(t *testing.T) {
	t.Parallel()
	lib, agents, _ := cursorHome(t)
	writeUnmanagedSkill(t, agents, "foo", "foo")
	global := library.GlobalScope("cursor")
	_, err := lib.FetchUnmanagedSkills(t.Context(), library.FetchUnmanagedSkillsRequest{Skills: []library.UnmanagedFetchItem{
		{Folders: []library.FolderInScope{{Scope: global, Name: "foo"}}},
		{Folders: []library.FolderInScope{{Scope: global, Directory: agents, Name: "foo"}}},
	}})
	requireLibraryError(t, err, library.InvalidArgument)
	if skills, err := lib.ListSkills(t.Context()); err != nil || len(skills) != 0 {
		t.Fatalf("ListSkills() = %#v, %v; want nothing fetched", skills, err)
	}
}

// npx skills 把技能放在 ~/.agents/skills、在 ~/.claude/skills 留链接:Codex 移回候选池后,链接仍指在 Agent 目录里,不算指向外面。
func TestLinkIntoAPoolAgentsDirectoryIsNotOutside(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	home := t.TempDir()
	setTestHome(t, home)
	lib := openTestLibrary(t)
	if _, err := lib.DeleteAgent(ctx, library.AgentCodex, library.DeleteAgentOptions{}); err != nil {
		t.Fatal(err)
	}
	agents, claude := filepath.Join(home, ".agents", "skills"), filepath.Join(home, ".claude", "skills")
	writeUnmanagedSkill(t, agents, "foo", "foo")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(agents, "foo"), filepath.Join(claude, "foo")); err != nil {
		t.Fatal(err)
	}
	found := unmanagedNamed(t, lib, library.GlobalScope(library.AgentClaudeCode), "foo")
	if len(found) != 1 || found[0].LinkTarget == "" || found[0].LinkOutside {
		t.Fatalf("unmanaged foo = %#v; want a link not counted as outside", found)
	}
}
