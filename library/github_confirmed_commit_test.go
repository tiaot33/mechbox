package library_test

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tiaot33/skills-tui/library"
)

// 安装即记录钉住的 commit:远端没变时 CheckUpdate 只发一次 commits/{ref} 就能判定无更新,
// 不取 recursive tree、不取任何 blob;这个判定随库文件持久化,Close 后重新 Open 仍然成立。
func TestGitHubCheckUpdateOnlyResolvesCommitWhenRemoteUnchangedAcrossReopen(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644},
		"demo/data.bin": {content: []byte{1, 2, 3}, mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)
	path := filepath.Join(t.TempDir(), "library.db")
	lib, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
	ctx := t.Context()
	fetch, err := lib.Fetch(ctx, gitHubSpec())
	if err != nil {
		t.Fatal(err)
	}
	installed, err := lib.InstallFetch(ctx, fetch.ID, nil)
	if err != nil || len(installed) != 1 {
		t.Fatalf("InstallFetch = %#v, %v", installed, err)
	}
	id := installed[0].ID
	before := snapshotSkill(t, lib, id)

	api.Reset()
	if available, err := lib.CheckUpdate(ctx, id); err != nil || available {
		t.Fatalf("unchanged CheckUpdate = %v, %v", available, err)
	}
	assertGitHubCommitOnly(t, api, 1)
	if after := snapshotSkill(t, lib, id); !reflect.DeepEqual(after, before) {
		t.Fatalf("check wrote Library: %#v, want %#v", after, before)
	}

	if err := lib.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	api.Reset()
	if available, err := reopened.CheckUpdate(ctx, id); err != nil || available {
		t.Fatalf("reopened CheckUpdate = %v, %v", available, err)
	}
	assertGitHubCommitOnly(t, api, 1)
	if after := snapshotSkill(t, reopened, id); !reflect.DeepEqual(after, before) {
		t.Fatalf("reopened check wrote Library: %#v, want %#v", after, before)
	}
}

// 确认 commit 是版本级的:同一 Source 下只更新了 A,B 的检查仍然要发现 B 有待更新——
// Source 级 last_seen_ref 已被 Update(A) 推进到新 commit,不能拿它替 B 作判断。
func TestGitHubConfirmedCommitIsPerVersionNotPerSource(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"a/SKILL.md": {content: []byte(skillDocument("a", "a body")), mode: 0o644},
		"a/notes.md": {content: []byte("a notes"), mode: 0o644},
		"b/SKILL.md": {content: []byte(skillDocument("b", "b body")), mode: 0o644},
		"b/notes.md": {content: []byte("b notes"), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)
	lib := openTestLibrary(t)
	ctx := t.Context()
	fetch, err := lib.Fetch(ctx, gitHubSpec())
	if err != nil {
		t.Fatal(err)
	}
	installed, err := lib.InstallFetch(ctx, fetch.ID, nil)
	if err != nil || len(installed) != 2 {
		t.Fatalf("InstallFetch = %#v, %v", installed, err)
	}
	a, b := installed[0], installed[1]
	if a.Name != "a" {
		a, b = b, a
	}

	writeFile(t, filepath.Join(repo, "b", "notes.md"), []byte("b changed"), 0o644)
	commitGit(t, repo, "change b")
	changed := inspectGitHubRepo(t, repo)
	if changed.treeSHA("a") != fx.treeSHA("a") || changed.treeSHA("b") == fx.treeSHA("b") {
		t.Fatal("fixture did not change only b")
	}
	api.Swap(changed)

	api.Reset()
	result, err := lib.Update(ctx, a.ID)
	if err != nil || result.Changed || result.Skill.Source.LastSeenRef != changed.commit {
		t.Fatalf("Update(a) = %#v, %v", result, err)
	}
	assertGitHubCheckDoesNotFetchSkillFiles(t, api)
	if versions, err := lib.ListVersions(ctx, a.ID); err != nil || len(versions) != 1 {
		t.Fatalf("unchanged Update(a) wrote history: %#v, %v", versions, err)
	}

	api.Reset()
	if available, err := lib.CheckUpdate(ctx, b.ID); err != nil || !available {
		t.Fatalf("CheckUpdate(b) after Update(a) = %v, %v", available, err)
	}
	if b2, err := lib.GetSkill(ctx, b.ID); err != nil || b2.Source.LastSeenRef != changed.commit {
		t.Fatalf("b shares the advanced Source ref: %#v, %v", b2, err)
	}

	api.Reset()
	if available, err := lib.CheckUpdate(ctx, a.ID); err != nil || available {
		t.Fatalf("CheckUpdate(a) after Update(a) = %v, %v", available, err)
	}
	assertGitHubCommitOnly(t, api, 1)
}

// 远端 commit 前进但目标目录未变:检查须取 tree(commit 不再命中)但不取 Skill 文件;
// 一次无变化的 Update 把当前版本在新 commit 上重新确认,之后检查回到只发 commits 的快路径。
func TestGitHubUnchangedUpdateReconfirmsVersionOnAdvancedCommit(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md":  {content: []byte(skillDocument("demo", "body")), mode: 0o644},
		"demo/data.bin":  {content: []byte{1, 2, 3}, mode: 0o644},
		"unrelated/file": {content: []byte("noise"), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)
	lib := openTestLibrary(t)
	ctx := t.Context()
	fetch, err := lib.Fetch(ctx, gitHubSpec())
	if err != nil {
		t.Fatal(err)
	}
	installed, err := lib.InstallFetch(ctx, fetch.ID, nil)
	if err != nil || len(installed) != 1 {
		t.Fatalf("InstallFetch = %#v, %v", installed, err)
	}
	id := installed[0].ID

	writeFile(t, filepath.Join(repo, "unrelated", "file"), []byte("more noise"), 0o644)
	commitGit(t, repo, "unrelated")
	advanced := inspectGitHubRepo(t, repo)
	api.Swap(advanced)
	before := snapshotSkill(t, lib, id)

	api.Reset()
	if available, err := lib.CheckUpdate(ctx, id); err != nil || available {
		t.Fatalf("advanced commit CheckUpdate = %v, %v", available, err)
	}
	assertGitHubCheckDoesNotFetchSkillFiles(t, api)
	if after := snapshotSkill(t, lib, id); !reflect.DeepEqual(after, before) {
		t.Fatalf("check wrote Library: %#v, want %#v", after, before)
	}

	api.Reset()
	result, err := lib.Update(ctx, id)
	if err != nil || result.Changed || result.Skill.Source.LastSeenRef != advanced.commit || result.Skill.GitTreeSHA != fx.treeSHA("demo") {
		t.Fatalf("unchanged Update = %#v, %v", result, err)
	}
	assertGitHubCheckDoesNotFetchSkillFiles(t, api)
	if versions, err := lib.ListVersions(ctx, id); err != nil || len(versions) != 1 {
		t.Fatalf("unchanged Update wrote history: %#v, %v", versions, err)
	}

	api.Reset()
	if available, err := lib.CheckUpdate(ctx, id); err != nil || available {
		t.Fatalf("reconfirmed CheckUpdate = %v, %v", available, err)
	}
	assertGitHubCommitOnly(t, api, 1)
}

// 钉住完整 40 位 SHA 的 Skill:分支再怎么前进,检查都只解析一次该 commit 并报告无更新。
func TestGitHubPinnedCommitCheckOnlyResolvesCommit(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644},
		"demo/data.bin": {content: []byte{1, 2, 3}, mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)
	lib := openTestLibrary(t)
	ctx := t.Context()
	spec := gitHubSpec()
	spec.Locator.Branch = fx.commit
	fetch, err := lib.Fetch(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	installed, err := lib.InstallFetch(ctx, fetch.ID, nil)
	if err != nil || len(installed) != 1 {
		t.Fatalf("InstallFetch = %#v, %v", installed, err)
	}
	id := installed[0].ID

	api.Reset()
	if available, err := lib.CheckUpdate(ctx, id); err != nil || available {
		t.Fatalf("pinned CheckUpdate = %v, %v", available, err)
	}
	assertGitHubCommitOnly(t, api, 1)

	writeFile(t, filepath.Join(repo, "demo", "SKILL.md"), []byte(skillDocument("demo", "moved on")), 0o644)
	commitGit(t, repo, "branch moves on")
	api.Swap(inspectGitHubRepo(t, repo))
	api.Reset()
	if available, err := lib.CheckUpdate(ctx, id); err != nil || available {
		t.Fatalf("pinned CheckUpdate after branch advance = %v, %v", available, err)
	}
	assertGitHubCommitOnly(t, api, 1)
	api.Reset()
	if checks, err := lib.CheckUpdates(ctx, []library.SkillID{id}); err != nil || len(checks) != 1 || checks[0].Error != nil || checks[0].Available {
		t.Fatalf("pinned CheckUpdates = %#v, %v", checks, err)
	}
	assertGitHubCommitOnly(t, api, 1)
}

// CheckUpdates 里同一 Source 下"命中"与"未命中"混合:一次 commits,至多一次 trees,命中的 Skill 不参与 tree 比较。
func TestGitHubCheckUpdatesMixedConfirmedSkillsFetchTreeAtMostOnce(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"a/SKILL.md": {content: []byte(skillDocument("a", "a body")), mode: 0o644},
		"b/SKILL.md": {content: []byte(skillDocument("b", "b body")), mode: 0o644},
		"c/SKILL.md": {content: []byte(skillDocument("c", "c body")), mode: 0o644},
		"c/notes.md": {content: []byte("c notes"), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)
	lib := openTestLibrary(t)
	ctx := t.Context()
	fetch, err := lib.Fetch(ctx, gitHubSpec())
	if err != nil {
		t.Fatal(err)
	}
	installed, err := lib.InstallFetch(ctx, fetch.ID, nil)
	if err != nil || len(installed) != 3 {
		t.Fatalf("InstallFetch = %#v, %v", installed, err)
	}
	byName := map[string]library.Skill{}
	for _, skill := range installed {
		byName[skill.Name] = skill
	}
	a, b, c := byName["a"], byName["b"], byName["c"]

	// 全部命中:一次 commits,零 trees。
	api.Reset()
	checks, err := lib.CheckUpdates(ctx, []library.SkillID{a.ID, b.ID, c.ID})
	if err != nil || len(checks) != 3 || checks[0].Available || checks[1].Available || checks[2].Available || checks[0].Error != nil || checks[1].Error != nil || checks[2].Error != nil {
		t.Fatalf("all confirmed CheckUpdates = %#v, %v", checks, err)
	}
	assertGitHubCommitOnly(t, api, 1)

	// 只改 c,再让 a 在新 commit 上重新确认:a 命中,b(目录未变)与 c(目录已变)未命中。
	writeFile(t, filepath.Join(repo, "c", "notes.md"), []byte("c changed"), 0o644)
	commitGit(t, repo, "change c")
	changed := inspectGitHubRepo(t, repo)
	api.Swap(changed)
	if result, err := lib.Update(ctx, a.ID); err != nil || result.Changed {
		t.Fatalf("Update(a) = %#v, %v", result, err)
	}
	before := []skillSnapshot{snapshotSkill(t, lib, a.ID), snapshotSkill(t, lib, b.ID), snapshotSkill(t, lib, c.ID)}

	api.Reset()
	checks, err = lib.CheckUpdates(ctx, []library.SkillID{a.ID, b.ID, c.ID, a.ID})
	if err != nil || len(checks) != 3 {
		t.Fatalf("mixed CheckUpdates = %#v, %v", checks, err)
	}
	for i, want := range []bool{false, false, true} {
		if checks[i].Error != nil || checks[i].Available != want {
			t.Fatalf("mixed CheckUpdates[%d] = %#v, want available=%v", i, checks[i], want)
		}
	}
	commits, trees, _ := gitHubAPIKindCounts(t, api)
	if commits != 1 || trees != 1 {
		t.Fatalf("mixed CheckUpdates commits=%d trees=%d, want 1 and 1: %#v", commits, trees, api.snapshot())
	}
	assertGitHubCheckDoesNotFetchSkillFiles(t, api)
	after := []skillSnapshot{snapshotSkill(t, lib, a.ID), snapshotSkill(t, lib, b.ID), snapshotSkill(t, lib, c.ID)}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("CheckUpdates wrote Library: %#v, want %#v", after, before)
	}
}

// 迁移后的旧版本没有确认 commit(用 editLibraryFixture 清空来模拟):首次检查按 tree SHA 做完整比较;
// 一次 Update 补写确认 commit 后进入只发 commits 的快路径。
func TestGitHubVersionWithoutConfirmedCommitComparesTreeUntilUpdated(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644},
		"demo/data.bin": {content: []byte{1, 2, 3}, mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)
	path := filepath.Join(t.TempDir(), "library.db")
	lib, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
	ctx := t.Context()
	fetch, err := lib.Fetch(ctx, gitHubSpec())
	if err != nil {
		t.Fatal(err)
	}
	installed, err := lib.InstallFetch(ctx, fetch.ID, nil)
	if err != nil || len(installed) != 1 {
		t.Fatalf("InstallFetch = %#v, %v", installed, err)
	}
	id := installed[0].ID
	if err := lib.Close(); err != nil {
		t.Fatal(err)
	}
	editLibraryFixture(t, path, `UPDATE skill_versions SET confirmed_commit = ''`)
	lib, err = library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })

	api.Reset()
	if available, err := lib.CheckUpdate(ctx, id); err != nil || available {
		t.Fatalf("unknown commit CheckUpdate = %v, %v", available, err)
	}
	assertGitHubCheckDoesNotFetchSkillFiles(t, api)
	// 只读:再查一次仍然要取 tree。
	api.Reset()
	if available, err := lib.CheckUpdate(ctx, id); err != nil || available {
		t.Fatalf("second unknown commit CheckUpdate = %v, %v", available, err)
	}
	assertGitHubCheckDoesNotFetchSkillFiles(t, api)

	api.Reset()
	result, err := lib.Update(ctx, id)
	if err != nil || result.Changed || result.Skill.GitTreeSHA != fx.treeSHA("demo") {
		t.Fatalf("unchanged Update = %#v, %v", result, err)
	}
	if versions, err := lib.ListVersions(ctx, id); err != nil || len(versions) != 1 {
		t.Fatalf("unchanged Update wrote history: %#v, %v", versions, err)
	}
	api.Reset()
	if available, err := lib.CheckUpdate(ctx, id); err != nil || available {
		t.Fatalf("confirmed CheckUpdate = %v, %v", available, err)
	}
	assertGitHubCommitOnly(t, api, 1)
}
