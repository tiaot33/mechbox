package library_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tiaot33/skills-tui/internal/library"
)

// serveGatedArchive 起一个受 gate 控制的 zip 远端;archive 每次请求时读取,便于测试中途换内容。
func serveGatedArchive(t *testing.T, gate *networkGate, archive func() []byte) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		gate.pass()
		_, _ = w.Write(archive())
	}))
	t.Cleanup(server.Close)
	return server
}

// mustFinishBeforeRelease 断言 run 在网络放行之前就返回了(没有等在库锁上)。
// 库锁不理会 ctx,所以在独立 goroutine 里跑,超时即判定被网络阶段挡住。
func mustFinishBeforeRelease(t *testing.T, gate *networkGate, what string, run func(ctx context.Context) error) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- run(t.Context()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("%s while another operation is on the network: %v", what, err)
		}
	case <-time.After(5 * time.Second):
		gate.open()
		t.Fatalf("%s blocked behind another operation's network phase", what)
	}
}

func TestUpdateNetworkPhaseDoesNotBlockInstallOrListing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	gate := newNetworkGate()
	var archive atomic.Pointer[[]byte]
	old := zipArchive(t, map[string]string{"demo/SKILL.md": skillDocument("demo", "old")})
	archive.Store(&old)
	server := serveGatedArchive(t, gate, func() []byte { return *archive.Load() })
	lib := openTestLibrary(t)
	demo := fetchURLSkill(t, lib, server.URL+"/skills.zip", "demo")
	other := fetchNamedSkill(t, lib, "other")

	updated := zipArchive(t, map[string]string{"demo/SKILL.md": skillDocument("demo", "new")})
	archive.Store(&updated)
	gate.arm()
	var wg sync.WaitGroup
	var result library.UpdateResult
	var updateErr error
	wg.Go(func() { result, updateErr = lib.Update(t.Context(), demo.ID) })
	gate.awaitEntered(t)

	mustFinishBeforeRelease(t, gate, "Install", func(ctx context.Context) error {
		return lib.Install(ctx, other.ID, library.GlobalScope(library.AgentCodex), library.InstallOptions{})
	})
	mustFinishBeforeRelease(t, gate, "ListSkills", func(ctx context.Context) error {
		_, err := lib.ListSkills(ctx)
		return err
	})

	gate.open()
	wg.Wait()
	if updateErr != nil || !result.Changed {
		t.Fatalf("Update after release = %#v, %v; want Changed", result, updateErr)
	}
	if versions, err := lib.ListVersions(t.Context(), demo.ID); err != nil || len(versions) != 2 {
		t.Fatalf("versions = %v, %v; want 2", versions, err)
	}
}

func TestUpdateFailsWithNotFoundWhenSkillRemovedDuringSourceRead(t *testing.T) {
	for _, remoteChanged := range []bool{true, false} {
		name := "unchanged remote"
		if remoteChanged {
			name = "changed remote"
		}
		t.Run(name, func(t *testing.T) {
			gate := newNetworkGate()
			var archive atomic.Pointer[[]byte]
			old := zipArchive(t, map[string]string{"demo/SKILL.md": skillDocument("demo", "old")})
			archive.Store(&old)
			server := serveGatedArchive(t, gate, func() []byte { return *archive.Load() })
			lib := openTestLibrary(t)
			demo := fetchURLSkill(t, lib, server.URL+"/skills.zip", "demo")

			if remoteChanged {
				updated := zipArchive(t, map[string]string{"demo/SKILL.md": skillDocument("demo", "new")})
				archive.Store(&updated)
			}
			gate.arm()
			var wg sync.WaitGroup
			var result library.UpdateResult
			var updateErr error
			wg.Go(func() { result, updateErr = lib.Update(t.Context(), demo.ID) })
			gate.awaitEntered(t)
			mustFinishBeforeRelease(t, gate, "RemoveSkill", func(ctx context.Context) error {
				return lib.RemoveSkill(ctx, demo.ID, library.RemoveSkillOptions{})
			})
			gate.open()
			wg.Wait()

			requireErrorDetails(t, updateErr, library.NotFound, library.ErrorDetails{SkillID: demo.ID})
			if result.Skill.ID != 0 {
				t.Fatalf("Update returned a skill after NotFound: %#v", result)
			}
			if skills, err := lib.ListSkills(t.Context()); err != nil || len(skills) != 0 {
				t.Fatalf("skills after racing removal = %v, %v; want none", skills, err)
			}
		})
	}
}

// 另一个 Library 实例(等价于另一个进程)在 Update 读取来源期间移除了 skill:
// 提交前的复核把它报成带 SkillID 的 NotFound,而不是撞外键后的兜底映射。
func TestUpdateReportsSkillRemovedByAnotherInstanceAsNotFound(t *testing.T) {
	gate := newNetworkGate()
	archive := zipArchive(t, map[string]string{"demo/SKILL.md": skillDocument("demo", "old")})
	server := serveGatedArchive(t, gate, func() []byte { return archive })
	path := filepath.Join(t.TempDir(), "library.db")
	first, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	second, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	demo := fetchURLSkill(t, first, server.URL+"/skills.zip", "demo")

	gate.arm()
	var wg sync.WaitGroup
	var updateErr error
	wg.Go(func() { _, updateErr = first.Update(t.Context(), demo.ID) })
	gate.awaitEntered(t)
	if err := second.RemoveSkill(t.Context(), demo.ID, library.RemoveSkillOptions{}); err != nil {
		t.Fatal(err)
	}
	gate.open()
	wg.Wait()
	requireErrorDetails(t, updateErr, library.NotFound, library.ErrorDetails{SkillID: demo.ID})
}

func TestUpdateFailsWithConflictWhenAnotherUpdateCommitsFirst(t *testing.T) {
	gate := newNetworkGate()
	var archive atomic.Pointer[[]byte]
	old := zipArchive(t, map[string]string{"demo/SKILL.md": skillDocument("demo", "old")})
	archive.Store(&old)
	server := serveGatedArchive(t, gate, func() []byte { return *archive.Load() })
	lib := openTestLibrary(t)
	demo := fetchURLSkill(t, lib, server.URL+"/skills.zip", "demo")

	updated := zipArchive(t, map[string]string{"demo/SKILL.md": skillDocument("demo", "new")})
	archive.Store(&updated)
	gate.arm()
	var wg sync.WaitGroup
	var slowErr error
	wg.Go(func() { _, slowErr = lib.Update(t.Context(), demo.ID) })
	gate.awaitEntered(t)
	// 慢的那次卡在远端时,第二次更新不受拦截,先看到并提交同样的新内容。
	gate.disarm()
	mustFinishBeforeRelease(t, gate, "second Update", func(ctx context.Context) error {
		result, err := lib.Update(ctx, demo.ID)
		if err == nil && !result.Changed {
			return fmt.Errorf("second Update = %#v, want Changed", result)
		}
		return err
	})
	gate.open()
	wg.Wait()

	requireErrorDetails(t, slowErr, library.Conflict, library.ErrorDetails{SkillID: demo.ID})
	versions, err := lib.ListVersions(t.Context(), demo.ID)
	if err != nil || len(versions) != 2 {
		t.Fatalf("versions = %v, %v; want initial + one update", versions, err)
	}
	current, err := lib.GetSkill(t.Context(), demo.ID)
	if err != nil || string(skillFiles(t, lib, current)[0].Content) != skillDocument("demo", "new") {
		t.Fatalf("current skill = %#v, %v; want the committed update", current, err)
	}
}

func TestCheckUpdatesNetworkPhaseDoesNotBlockInstall(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	gate := newNetworkGate()
	archive := zipArchive(t, map[string]string{"demo/SKILL.md": skillDocument("demo", "old")})
	server := serveGatedArchive(t, gate, func() []byte { return archive })
	lib := openTestLibrary(t)
	demo := fetchURLSkill(t, lib, server.URL+"/skills.zip", "demo")
	other := fetchNamedSkill(t, lib, "other")

	gate.arm()
	var wg sync.WaitGroup
	var checks []library.UpdateCheck
	var batchErr error
	wg.Go(func() { checks, batchErr = lib.CheckUpdates(t.Context(), []library.SkillID{demo.ID}) })
	gate.awaitEntered(t)

	mustFinishBeforeRelease(t, gate, "Install", func(ctx context.Context) error {
		return lib.Install(ctx, other.ID, library.GlobalScope(library.AgentCodex), library.InstallOptions{})
	})

	gate.open()
	wg.Wait()
	if batchErr != nil || len(checks) != 1 || checks[0].Available || checks[0].Error != nil {
		t.Fatalf("checks after release = %#v, %v; want one unchanged result", checks, batchErr)
	}
}

func TestFetchMaterializationDoesNotBlockOtherPreviewsOrInstall(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := writeGitHubRepo(t, map[string]repoFile{
		"first/SKILL.md":  {content: []byte(skillDocument("first", "first body")), mode: 0o644},
		"first/notes.md":  {content: []byte("first notes"), mode: 0o644},
		"second/SKILL.md": {content: []byte(skillDocument("second", "second body")), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)
	// 只拦 first/notes.md 这个 blob:它只在入库物化时才取,入库预览(各候选的 SKILL.md)不受影响。
	gate := newNetworkGate()
	api.gateBlob(gate, fx.blobSHA("first/notes.md"))

	lib := openTestLibrary(t)
	local := fetchNamedSkill(t, lib, "local")
	preview, err := lib.PreviewFetch(t.Context(), gitHubRequest())
	if err != nil {
		t.Fatal(err)
	}

	gate.arm()
	var wg sync.WaitGroup
	var fetched []library.Skill
	var fetchErr error
	wg.Go(func() { fetched, fetchErr = lib.Fetch(t.Context(), preview.ID, []string{"first"}) })
	gate.awaitEntered(t)

	mustFinishBeforeRelease(t, gate, "second Fetch on the same handle", func(ctx context.Context) error {
		_, err := lib.Fetch(ctx, preview.ID, []string{"second"})
		if !library.IsCode(err, library.FailedPrecondition) {
			return fmt.Errorf("busy handle fetch = %v, want FailedPrecondition", err)
		}
		return nil
	})
	mustFinishBeforeRelease(t, gate, "PreviewFetch of another Source", func(ctx context.Context) error {
		// 同一仓库钉到 commit 是另一个 Source;它的入库预览只取各候选的 SKILL.md,不经受拦的 blob。
		pinned := gitHubRequest()
		pinned.GitHub.Ref = fx.commit
		other, err := lib.PreviewFetch(ctx, pinned)
		if err != nil {
			return err
		}
		if other.ID == "" || len(other.Candidates) != 2 {
			return fmt.Errorf("other PreviewFetch = %#v, want handle and both candidates", other)
		}
		return nil
	})
	mustFinishBeforeRelease(t, gate, "Install", func(ctx context.Context) error {
		return lib.Install(ctx, local.ID, library.GlobalScope(library.AgentCodex), library.InstallOptions{})
	})

	gate.open()
	wg.Wait()
	if fetchErr != nil || len(fetched) != 1 || fetched[0].Name != "first" {
		t.Fatalf("Fetch after release = %#v, %v", fetched, fetchErr)
	}
	if _, err := lib.Fetch(t.Context(), preview.ID, []string{"second"}); !library.IsCode(err, library.NotFound) {
		t.Fatalf("consumed handle reused: %v; want NotFound", err)
	}
}

func TestCloseDoesNotWaitForFetchMaterialization(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"first/SKILL.md": {content: []byte(skillDocument("first", "first body")), mode: 0o644},
		"first/notes.md": {content: []byte("first notes"), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)
	gate := newNetworkGate()
	api.gateBlob(gate, fx.blobSHA("first/notes.md"))

	lib, err := library.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	preview, err := lib.PreviewFetch(t.Context(), gitHubRequest())
	if err != nil {
		t.Fatal(err)
	}
	gate.arm()
	var wg sync.WaitGroup
	var fetchErr error
	wg.Go(func() { _, fetchErr = lib.Fetch(t.Context(), preview.ID, candidateNames(preview)) })
	gate.awaitEntered(t)
	mustFinishBeforeRelease(t, gate, "Close", func(context.Context) error { return lib.Close() })
	gate.open()
	wg.Wait()
	if fetchErr == nil {
		t.Fatal("Fetch succeeded against a closed Library")
	}
}

func TestSyncPushAndPlanNetworkPhasesDoNotBlockLibrary(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stub := newS3Stub(t)
	lib := openTestLibrary(t)
	local := fetchNamedSkill(t, lib, "local")
	plan, err := lib.PlanSyncPush(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	pushGate := newNetworkGate()
	pushGate.arm()
	stub.gate, stub.gateMethod = pushGate, http.MethodPut
	var wg sync.WaitGroup
	var pushErr error
	wg.Go(func() { _, pushErr = lib.SyncPush(t.Context(), plan.Remote) })
	pushGate.awaitEntered(t)
	mustFinishBeforeRelease(t, pushGate, "Fetch during push upload", func(ctx context.Context) error {
		source := filepath.Join(t.TempDir(), "during-push")
		writeLocalSkill(t, source, "# during push\n")
		_, err := fetchLocalDir(ctx, lib, source)
		return err
	})
	mustFinishBeforeRelease(t, pushGate, "Install during push upload", func(ctx context.Context) error {
		return lib.Install(ctx, local.ID, library.GlobalScope(library.AgentCodex), library.InstallOptions{})
	})
	pushGate.open()
	wg.Wait()
	if pushErr != nil {
		t.Fatalf("SyncPush after release: %v", pushErr)
	}

	headGate := newNetworkGate()
	headGate.arm()
	stub.gate, stub.gateMethod = headGate, http.MethodHead
	var planErr error
	wg.Go(func() { _, planErr = lib.PlanSyncPush(t.Context()) })
	headGate.awaitEntered(t)
	mustFinishBeforeRelease(t, headGate, "Uninstall during push plan", func(ctx context.Context) error {
		return lib.Uninstall(ctx, local.ID, library.GlobalScope(library.AgentCodex))
	})
	headGate.open()
	wg.Wait()
	if planErr != nil {
		t.Fatalf("PlanSyncPush after release: %v", planErr)
	}
}
