package library_test

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tiaot33/skills-tui/internal/library"
)

// 入库即记录钉住的 commit:远端没变时 CheckUpdates 只发一次 commits/{ref} 就能判定无更新,
// 不取 recursive tree、不取任何 blob;这个判定随库文件持久化,Close 后重新 Open 仍然成立。
func TestGitHubCheckUpdatesOnlyResolvesCommitWhenRemoteUnchangedAcrossReopen(t *testing.T) {
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
	preview, err := lib.PreviewFetch(ctx, gitHubRequest())
	if err != nil {
		t.Fatal(err)
	}
	fetched, err := lib.Fetch(ctx, preview.ID, candidateNames(preview))
	if err != nil || len(fetched) != 1 {
		t.Fatalf("Fetch = %#v, %v", fetched, err)
	}
	id := fetched[0].ID
	before := snapshotSkill(t, lib, id)

	api.Reset()
	if available, err := checkSingleUpdate(t, lib, ctx, id); err != nil || available {
		t.Fatalf("unchanged CheckUpdates = %v, %v", available, err)
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
	if available, err := checkSingleUpdate(t, reopened, ctx, id); err != nil || available {
		t.Fatalf("reopened CheckUpdates = %v, %v", available, err)
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
	preview, err := lib.PreviewFetch(ctx, gitHubRequest())
	if err != nil {
		t.Fatal(err)
	}
	fetched, err := lib.Fetch(ctx, preview.ID, candidateNames(preview))
	if err != nil || len(fetched) != 2 {
		t.Fatalf("Fetch = %#v, %v", fetched, err)
	}
	a, b := fetched[0], fetched[1]
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
	assertGitHubCheckDoesNotReadSkillFiles(t, api)
	if versions, err := lib.ListVersions(ctx, a.ID); err != nil || len(versions) != 1 {
		t.Fatalf("unchanged Update(a) wrote history: %#v, %v", versions, err)
	}

	api.Reset()
	if available, err := checkSingleUpdate(t, lib, ctx, b.ID); err != nil || !available {
		t.Fatalf("CheckUpdates(b) after Update(a) = %v, %v", available, err)
	}
	if b2, err := lib.GetSkill(ctx, b.ID); err != nil || b2.Source.LastSeenRef != changed.commit {
		t.Fatalf("b shares the advanced Source ref: %#v, %v", b2, err)
	}

	api.Reset()
	if available, err := checkSingleUpdate(t, lib, ctx, a.ID); err != nil || available {
		t.Fatalf("CheckUpdates(a) after Update(a) = %v, %v", available, err)
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
	preview, err := lib.PreviewFetch(ctx, gitHubRequest())
	if err != nil {
		t.Fatal(err)
	}
	fetched, err := lib.Fetch(ctx, preview.ID, candidateNames(preview))
	if err != nil || len(fetched) != 1 {
		t.Fatalf("Fetch = %#v, %v", fetched, err)
	}
	id := fetched[0].ID

	writeFile(t, filepath.Join(repo, "unrelated", "file"), []byte("more noise"), 0o644)
	commitGit(t, repo, "unrelated")
	advanced := inspectGitHubRepo(t, repo)
	api.Swap(advanced)
	before := snapshotSkill(t, lib, id)

	api.Reset()
	if available, err := checkSingleUpdate(t, lib, ctx, id); err != nil || available {
		t.Fatalf("advanced commit CheckUpdates = %v, %v", available, err)
	}
	assertGitHubCheckDoesNotReadSkillFiles(t, api)
	if after := snapshotSkill(t, lib, id); !reflect.DeepEqual(after, before) {
		t.Fatalf("check wrote Library: %#v, want %#v", after, before)
	}

	api.Reset()
	result, err := lib.Update(ctx, id)
	if err != nil || result.Changed || result.Skill.Source.LastSeenRef != advanced.commit || readPersistedGitTreeSHA(t, lib, result.Skill.ID) != fx.treeSHA("demo") {
		t.Fatalf("unchanged Update = %#v, %v", result, err)
	}
	// 紧挨着的 CheckUpdates 已取过新 root tree,Update 只再解析 branch commit。
	assertGitHubCommitOnly(t, api, 1)
	if versions, err := lib.ListVersions(ctx, id); err != nil || len(versions) != 1 {
		t.Fatalf("unchanged Update wrote history: %#v, %v", versions, err)
	}

	api.Reset()
	if available, err := checkSingleUpdate(t, lib, ctx, id); err != nil || available {
		t.Fatalf("reconfirmed CheckUpdates = %v, %v", available, err)
	}
	assertGitHubCommitOnly(t, api, 1)
}

// 钉住完整 40 位 SHA 的 Skill:入库预览已按该 commit 写入 memo,之后检查零请求并报告无更新;
// 重开(memo 清空)后也只发一次 commits。
func TestGitHubPinnedCommitCheckOnlyResolvesCommit(t *testing.T) {
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
	spec := gitHubRequest()
	spec.GitHub.Ref = fx.commit
	preview, err := lib.PreviewFetch(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	fetched, err := lib.Fetch(ctx, preview.ID, candidateNames(preview))
	if err != nil || len(fetched) != 1 {
		t.Fatalf("Fetch = %#v, %v", fetched, err)
	}
	id := fetched[0].ID

	api.Reset()
	if available, err := checkSingleUpdate(t, lib, ctx, id); err != nil || available {
		t.Fatalf("pinned CheckUpdates = %v, %v", available, err)
	}
	// 入库预览已按完整 SHA 写入 memo,钉住的 commit 不再出网。
	assertGitHubCommitOnly(t, api, 0)

	writeFile(t, filepath.Join(repo, "demo", "SKILL.md"), []byte(skillDocument("demo", "moved on")), 0o644)
	commitGit(t, repo, "branch moves on")
	api.Swap(inspectGitHubRepo(t, repo))
	api.Reset()
	if available, err := checkSingleUpdate(t, lib, ctx, id); err != nil || available {
		t.Fatalf("pinned CheckUpdates after branch advance = %v, %v", available, err)
	}
	assertGitHubCommitOnly(t, api, 0)
	api.Reset()
	if checks, err := lib.CheckUpdates(ctx, []library.SkillID{id}); err != nil || len(checks) != 1 || checks[0].Error != nil || checks[0].Available {
		t.Fatalf("pinned CheckUpdates = %#v, %v", checks, err)
	}
	assertGitHubCommitOnly(t, api, 0)

	// 冷启动没有 memo:钉住的 SHA 仍只解析一次 commit,不取 tree。
	if err := lib.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	api.Reset()
	if available, err := checkSingleUpdate(t, reopened, ctx, id); err != nil || available {
		t.Fatalf("pinned CheckUpdates after reopen = %v, %v", available, err)
	}
	assertGitHubCommitOnly(t, api, 1)
}

// CheckUpdates 里同一 Source 下"命中"与"未命中"混合:一次 commits,至多一次 trees,命中的 Skill 不参与 tree 比较。
// 同进程内 tree 已在 memo 里,冷启动后才能看到"恰好一次 trees"。
func TestGitHubCheckUpdatesMixedConfirmedSkillsReadTreeAtMostOnce(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"a/SKILL.md": {content: []byte(skillDocument("a", "a body")), mode: 0o644},
		"b/SKILL.md": {content: []byte(skillDocument("b", "b body")), mode: 0o644},
		"c/SKILL.md": {content: []byte(skillDocument("c", "c body")), mode: 0o644},
		"c/notes.md": {content: []byte("c notes"), mode: 0o644},
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
	preview, err := lib.PreviewFetch(ctx, gitHubRequest())
	if err != nil {
		t.Fatal(err)
	}
	fetched, err := getFetchedSkills(ctx, lib, preview, candidateNames(preview))
	if err != nil || len(fetched) != 3 {
		t.Fatalf("Fetch = %#v, %v", fetched, err)
	}
	byName := map[string]library.Skill{}
	for _, skill := range fetched {
		byName[skill.Name] = skill
	}
	a, b, c := byName["a"], byName["b"], byName["c"]

	// 只改 c,再让 a 在新 commit 上重新确认:a 命中,b(目录未变)与 c(目录已变)未命中。
	writeFile(t, filepath.Join(repo, "c", "notes.md"), []byte("c changed"), 0o644)
	commitGit(t, repo, "change c")
	changed := inspectGitHubRepo(t, repo)
	api.Swap(changed)
	if result, err := lib.Update(ctx, a.ID); err != nil || result.Changed {
		t.Fatalf("Update(a) = %#v, %v", result, err)
	}
	before := []skillSnapshot{snapshotSkill(t, lib, a.ID), snapshotSkill(t, lib, b.ID), snapshotSkill(t, lib, c.ID)}

	ids := []library.SkillID{a.ID, b.ID, c.ID, a.ID}
	assertMixedChecks := func(lib *library.Library) {
		t.Helper()
		checks, err := lib.CheckUpdates(ctx, ids)
		if err != nil || len(checks) != 3 {
			t.Fatalf("mixed CheckUpdates = %#v, %v", checks, err)
		}
		for i, want := range []bool{false, false, true} {
			if checks[i].Error != nil || checks[i].Available != want {
				t.Fatalf("mixed CheckUpdates[%d] = %#v, want available=%v", i, checks[i], want)
			}
		}
		after := []skillSnapshot{snapshotSkill(t, lib, a.ID), snapshotSkill(t, lib, b.ID), snapshotSkill(t, lib, c.ID)}
		if !reflect.DeepEqual(after, before) {
			t.Fatalf("CheckUpdates wrote Library: %#v, want %#v", after, before)
		}
	}

	api.Reset()
	assertMixedChecks(lib)
	// Update(a) 已取过新 root tree,CheckUpdates 命中 memo,只再解析 branch commit。
	commits, trees, blobs := gitHubAPIKindCounts(t, api)
	if commits != 1 || trees != 0 || blobs != 0 {
		t.Fatalf("mixed CheckUpdates commits=%d trees=%d blobs=%d, want one commit: %#v", commits, trees, blobs, api.snapshot())
	}

	// 冷启动:a 命中确认 commit 不参与比较,b 与 c 共用恰好一次 tree;发现阶段只读候选的 SKILL.md。
	if err := lib.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	api.Reset()
	assertMixedChecks(reopened)
	assertGitHubCheckDoesNotReadSkillFiles(t, api)
	commits, trees, _ = gitHubAPIKindCounts(t, api)
	if commits != 1 || trees != 1 {
		t.Fatalf("cold mixed CheckUpdates commits=%d trees=%d, want exactly one of each: %#v", commits, trees, api.snapshot())
	}
}

// 版本未记录确认 commit(用当前 schema 清空字段来构造):首次检查按 tree SHA 做完整比较;
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
	lib := openTestLibraryAt(t, path)
	ctx := t.Context()
	preview, err := lib.PreviewFetch(ctx, gitHubRequest())
	if err != nil {
		t.Fatal(err)
	}
	fetched, err := lib.Fetch(ctx, preview.ID, candidateNames(preview))
	if err != nil || len(fetched) != 1 {
		t.Fatalf("Fetch = %#v, %v", fetched, err)
	}
	id := fetched[0].ID
	if err := lib.Close(); err != nil {
		t.Fatal(err)
	}
	editLibraryFixture(t, path, `UPDATE skill_versions SET source_state = json_remove(source_state, '$.commit')`)
	lib = openTestLibraryAt(t, path)

	api.Reset()
	before := snapshotSkill(t, lib, id)
	if available, err := checkSingleUpdate(t, lib, ctx, id); err != nil || available {
		t.Fatalf("unknown commit CheckUpdates = %v, %v", available, err)
	}
	assertGitHubCheckDoesNotReadSkillFiles(t, api)
	// 走完整比较的检查同样只读:不能顺手把确认 commit 补上——重开后(memo 清空)检查仍须取 tree。
	if after := snapshotSkill(t, lib, id); !reflect.DeepEqual(after, before) {
		t.Fatalf("CheckUpdates wrote Library: %#v, want %#v", after, before)
	}
	if err := lib.Close(); err != nil {
		t.Fatal(err)
	}
	lib = openTestLibraryAt(t, path)
	api.Reset()
	if available, err := checkSingleUpdate(t, lib, ctx, id); err != nil || available {
		t.Fatalf("unknown commit CheckUpdates after reopen = %v, %v", available, err)
	}
	assertGitHubCheckDoesNotReadSkillFiles(t, api)

	api.Reset()
	result, err := lib.Update(ctx, id)
	if err != nil || result.Changed || readPersistedGitTreeSHA(t, lib, result.Skill.ID) != fx.treeSHA("demo") {
		t.Fatalf("unchanged Update = %#v, %v", result, err)
	}
	if versions, err := lib.ListVersions(ctx, id); err != nil || len(versions) != 1 {
		t.Fatalf("unchanged Update wrote history: %#v, %v", versions, err)
	}
	api.Reset()
	if available, err := checkSingleUpdate(t, lib, ctx, id); err != nil || available {
		t.Fatalf("confirmed CheckUpdates = %v, %v", available, err)
	}
	assertGitHubCommitOnly(t, api, 1)
}
