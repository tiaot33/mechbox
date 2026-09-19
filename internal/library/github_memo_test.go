package library_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tiaot33/skills-tui/internal/library"
)

func TestGitHubRepeatFetchOmitsTreeAndBlobRequests(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"first/SKILL.md":    {content: []byte(skillDocument("first", "first body")), mode: 0o644},
		"first/scripts/run": {content: []byte{0, 255, 1}, mode: 0o755},
		"second/SKILL.md":   {content: []byte(skillDocument("second", "second body")), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)
	lib := openTestLibrary(t)
	ctx := t.Context()

	first, err := lib.Fetch(ctx, gitHubSpec())
	if err != nil {
		t.Fatal(err)
	}
	api.Reset()
	second, err := lib.Fetch(ctx, gitHubSpec())
	if err != nil {
		t.Fatal(err)
	}
	if second.Ref != first.Ref || !reflect.DeepEqual(second.Skills, first.Skills) {
		t.Fatalf("second Fetch = %#v, want candidates of %#v", second, first)
	}
	commits, trees, blobs := gitHubAPIKindCounts(t, api)
	if trees != 0 || blobs != 0 || commits != 1 {
		t.Fatalf("second Fetch commits=%d trees=%d blobs=%d, want one commit only: %#v", commits, trees, blobs, api.snapshot())
	}
}

func TestGitHubPinnedSHARepeatFetchMakesNoRequests(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)
	lib := openTestLibrary(t)
	spec := gitHubSpecAt(fx.commit)
	if _, err := lib.Fetch(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	api.Reset()
	if _, err := lib.Fetch(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	commits, trees, blobs := gitHubAPIKindCounts(t, api)
	if commits != 0 || trees != 0 || blobs != 0 {
		t.Fatalf("pinned SHA second Fetch commits=%d trees=%d blobs=%d, want zero: %#v", commits, trees, blobs, api.snapshot())
	}
}

func TestGitHubConcurrentCheckUpdatesSingleFlightsCommit(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644},
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

	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	transport := http.DefaultClient.Transport
	http.DefaultClient.Transport = fetchTransport(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/commits/") {
			once.Do(func() { close(started) })
			<-release
		}
		return transport.RoundTrip(r)
	})

	api.Reset()
	var first, second []library.UpdateCheck
	var firstErr, secondErr error
	var wg sync.WaitGroup
	wg.Go(func() { first, firstErr = lib.CheckUpdates(ctx, []library.SkillID{installed[0].ID}) })
	<-started
	wg.Go(func() { second, secondErr = lib.CheckUpdates(ctx, []library.SkillID{installed[0].ID}) })
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()
	if firstErr != nil || secondErr != nil || len(first) != 1 || len(second) != 1 || first[0].SkillID != installed[0].ID || second[0].SkillID != installed[0].ID || first[0].Error != nil || second[0].Error != nil || first[0].Available || second[0].Available {
		t.Fatalf("CheckUpdates = %v/%v, %v/%v, want both false", first, second, firstErr, secondErr)
	}
	assertGitHubCommitOnly(t, api, 1)
}

func TestGitHubConcurrentFetchSingleFlightsTreeAndBlobs(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"first/SKILL.md":  {content: []byte(skillDocument("first", "first body")), mode: 0o644},
		"second/SKILL.md": {content: []byte(skillDocument("second", "second body")), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)
	lib := openTestLibrary(t)
	ctx := t.Context()

	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	transport := http.DefaultClient.Transport
	http.DefaultClient.Transport = fetchTransport(func(r *http.Request) (*http.Response, error) {
		once.Do(func() { close(started) })
		<-release
		return transport.RoundTrip(r)
	})

	var first, second library.Fetch
	var firstErr, secondErr error
	var wg sync.WaitGroup
	wg.Go(func() { first, firstErr = lib.Fetch(ctx, gitHubSpec()) })
	<-started
	wg.Go(func() { second, secondErr = lib.Fetch(ctx, gitHubSpec()) })
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()
	if firstErr != nil || secondErr != nil {
		t.Fatalf("Fetch = %v, %v", firstErr, secondErr)
	}
	if first.Ref != second.Ref || !reflect.DeepEqual(first.Skills, second.Skills) {
		t.Fatalf("concurrent Fetch %#v vs %#v", first, second)
	}
	commits, trees, blobs := gitHubAPIKindCounts(t, api)
	if commits != 1 || trees != 1 || blobs != 2 {
		t.Fatalf("concurrent Fetch commits=%d trees=%d blobs=%d, want 1/1/2: %#v", commits, trees, blobs, api.snapshot())
	}
}

// 先到者自己的 ctx 被取消不该拖垮等待者:等待者重新出网,自己的 Fetch 照常成功。
func TestGitHubWaiterSurvivesLeaderCancellation(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)
	lib := openTestLibrary(t)

	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	transport := http.DefaultClient.Transport
	http.DefaultClient.Transport = fetchTransport(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/commits/") {
			once.Do(func() { close(started); <-release })
		}
		return transport.RoundTrip(r)
	})

	leaderCtx, cancelLeader := context.WithCancel(t.Context())
	var leaderErr, waiterErr error
	var waiter library.Fetch
	var wg sync.WaitGroup
	wg.Go(func() { _, leaderErr = lib.Fetch(leaderCtx, gitHubSpec()) })
	<-started
	wg.Go(func() { waiter, waiterErr = lib.Fetch(t.Context(), gitHubSpec()) })
	time.Sleep(20 * time.Millisecond)
	cancelLeader()
	close(release)
	wg.Wait()
	if !errors.Is(leaderErr, context.Canceled) {
		t.Fatalf("leader Fetch error = %v, want context.Canceled", leaderErr)
	}
	if waiterErr != nil || waiter.Ref != fx.commit || len(waiter.Skills) != 1 {
		t.Fatalf("waiter Fetch = %#v, %v, want a normal result", waiter, waiterErr)
	}
	// 等待者不沿用先到者的取消,自己再解析一次 commit;tree 与 blob 仍各只出一份。
	commits, trees, blobs := gitHubAPIKindCounts(t, api)
	if commits != 2 || trees != 1 || blobs != 1 {
		t.Fatalf("commits=%d trees=%d blobs=%d, want 2/1/1: %#v", commits, trees, blobs, api.snapshot())
	}
}

// 等待者自己的 ctx 被取消时立即返回,不陪先到者等到底。
func TestGitHubWaiterStopsOnItsOwnCancellation(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)
	lib := openTestLibrary(t)

	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	transport := http.DefaultClient.Transport
	http.DefaultClient.Transport = fetchTransport(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/commits/") {
			once.Do(func() { close(started); <-release })
		}
		return transport.RoundTrip(r)
	})

	var leaderErr error
	var wg sync.WaitGroup
	wg.Go(func() { _, leaderErr = lib.Fetch(t.Context(), gitHubSpec()) })
	<-started
	waiterCtx, cancelWaiter := context.WithCancel(t.Context())
	waiterDone := make(chan error, 1)
	go func() {
		_, err := lib.Fetch(waiterCtx, gitHubSpec())
		waiterDone <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancelWaiter()
	select {
	case err := <-waiterDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("waiter Fetch error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled waiter kept waiting for the leader")
	}
	close(release)
	wg.Wait()
	if leaderErr != nil {
		t.Fatalf("leader Fetch = %v", leaderErr)
	}
	// 等待者退出后没有自己再解析 commit;剩下的 tree / blob 都是先到者的。
	if commits, trees, blobs := gitHubAPIKindCounts(t, api); commits != 1 || trees != 1 || blobs != 1 {
		t.Fatalf("commits=%d trees=%d blobs=%d, want 1/1/1: %#v", commits, trees, blobs, api.snapshot())
	}
}

// 单条 blob 超过总字节上限时不写入 memo,再次访问回源。
func TestGitHubOversizedBlobIsNotMemoized(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md": {content: []byte(skillDocument("demo", string(bytes.Repeat([]byte("a"), 300*1024)))), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)
	lib := openTestLibrary(t)
	spec := gitHubSpecAt(fx.commit)
	if _, err := lib.Fetch(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	api.Reset()
	if _, err := lib.Fetch(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	commits, trees, blobs := gitHubAPIKindCounts(t, api)
	if commits != 0 || trees != 0 || blobs != 1 {
		t.Fatalf("second Fetch commits=%d trees=%d blobs=%d, want the oversized blob refetched only: %#v", commits, trees, blobs, api.snapshot())
	}
}

func TestGitHubTruncatedTreeIsNotMemoized(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	fx.truncated = true
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)
	lib := openTestLibrary(t)
	if _, err := lib.Fetch(t.Context(), gitHubSpec()); err == nil {
		t.Fatal("truncated tree with clone blocked succeeded")
	}
	api.Swap(inspectGitHubRepo(t, repo))
	api.Reset()
	if _, err := lib.Fetch(t.Context(), gitHubSpec()); err != nil {
		t.Fatal(err)
	}
	_, trees, _ := gitHubAPIKindCounts(t, api)
	if trees == 0 {
		t.Fatalf("truncated tree was memoized: %#v", api.snapshot())
	}
}

func TestGitHubIncompleteBlobIsNotMemoized(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	fx.incompleteBlob = fx.blobSHA("demo/SKILL.md")
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)
	lib := openTestLibrary(t)
	if _, err := lib.Fetch(t.Context(), gitHubSpec()); err == nil {
		t.Fatal("incomplete blob with clone blocked succeeded")
	}
	api.Swap(inspectGitHubRepo(t, repo))
	api.Reset()
	if _, err := lib.Fetch(t.Context(), gitHubSpec()); err != nil {
		t.Fatal(err)
	}
	_, _, blobs := gitHubAPIKindCounts(t, api)
	if blobs == 0 {
		t.Fatalf("incomplete blob was memoized: %#v", api.snapshot())
	}
}

func TestGitHubTreeMemoEvictsLeastRecentlyUsed(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md": {content: []byte(skillDocument("demo", "v0")), mode: 0o644},
	})
	first := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, first)
	blockGitHubClone(t)
	lib := openTestLibrary(t)
	if _, err := lib.Fetch(t.Context(), gitHubSpecAt(first.commit)); err != nil {
		t.Fatal(err)
	}
	var second gitHubFixture
	// 先写入 15 棵新 tree(加上 first 共 16,顶满),再访问 first 把它变成最近使用,
	// 然后写入第 17 棵应逐出 second 而不是 first。
	for i := range 15 {
		writeFile(t, filepath.Join(repo, "demo", "SKILL.md"), []byte(skillDocument("demo", fmt.Sprintf("v%d", i+1))), 0o644)
		commitGit(t, repo, fmt.Sprintf("v%d", i+1))
		next := inspectGitHubRepo(t, repo)
		api.Swap(next)
		if i == 0 {
			second = next
		}
		if _, err := lib.Fetch(t.Context(), gitHubSpecAt(next.commit)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := lib.Fetch(t.Context(), gitHubSpecAt(first.commit)); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo, "demo", "SKILL.md"), []byte(skillDocument("demo", "overflow")), 0o644)
	commitGit(t, repo, "overflow")
	overflow := inspectGitHubRepo(t, repo)
	api.Swap(overflow)
	if _, err := lib.Fetch(t.Context(), gitHubSpecAt(overflow.commit)); err != nil {
		t.Fatal(err)
	}
	api.Reset()
	if _, err := lib.Fetch(t.Context(), gitHubSpecAt(first.commit)); err != nil {
		t.Fatal(err)
	}
	// 钉住 SHA 的 commit 与 tree 同为 16 条上限、同一访问顺序:first 最近用过,两者都还在。
	commits, trees, _ := gitHubAPIKindCounts(t, api)
	if commits != 0 || trees != 0 {
		t.Fatalf("recently used commit / tree was evicted: commits=%d trees=%d: %#v", commits, trees, api.snapshot())
	}
	api.Reset()
	if _, err := lib.Fetch(t.Context(), gitHubSpecAt(second.commit)); err != nil {
		t.Fatal(err)
	}
	commits, trees, blobs := gitHubAPIKindCounts(t, api)
	if commits != 1 || trees != 1 || blobs != 0 {
		t.Fatalf("LRU refetch commits=%d trees=%d blobs=%d, want one commit and one tree: %#v", commits, trees, blobs, api.snapshot())
	}
}

func TestGitHubBlobMemoEvictsLeastRecentlyUsed(t *testing.T) {
	const blobSize = 80 * 1024
	repo := writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md": {content: []byte(skillDocument("demo", string(bytes.Repeat([]byte("a"), blobSize)))), mode: 0o644},
	})
	first := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, first)
	blockGitHubClone(t)
	lib := openTestLibrary(t)
	if _, err := lib.Fetch(t.Context(), gitHubSpecAt(first.commit)); err != nil {
		t.Fatal(err)
	}
	var second gitHubFixture
	// 先写入 2 个新 blob(加上 first 约 240 KiB),再访问 first 把它变成最近使用,
	// 然后第 4 个应逐出 second 而不是 first。
	for i := range 2 {
		body := string(bytes.Repeat([]byte{byte('b' + i)}, blobSize))
		writeFile(t, filepath.Join(repo, "demo", "SKILL.md"), []byte(skillDocument("demo", body)), 0o644)
		commitGit(t, repo, fmt.Sprintf("blob-%d", i))
		next := inspectGitHubRepo(t, repo)
		api.Swap(next)
		if i == 0 {
			second = next
		}
		if _, err := lib.Fetch(t.Context(), gitHubSpecAt(next.commit)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := lib.Fetch(t.Context(), gitHubSpecAt(first.commit)); err != nil {
		t.Fatal(err)
	}
	overflowBody := string(bytes.Repeat([]byte("z"), blobSize))
	writeFile(t, filepath.Join(repo, "demo", "SKILL.md"), []byte(skillDocument("demo", overflowBody)), 0o644)
	commitGit(t, repo, "overflow")
	overflow := inspectGitHubRepo(t, repo)
	api.Swap(overflow)
	if _, err := lib.Fetch(t.Context(), gitHubSpecAt(overflow.commit)); err != nil {
		t.Fatal(err)
	}
	api.Reset()
	if _, err := lib.Fetch(t.Context(), gitHubSpecAt(first.commit)); err != nil {
		t.Fatal(err)
	}
	_, _, blobs := gitHubAPIKindCounts(t, api)
	if blobs != 0 {
		t.Fatalf("recently used blob was evicted: %#v", api.snapshot())
	}
	api.Reset()
	if _, err := lib.Fetch(t.Context(), gitHubSpecAt(second.commit)); err != nil {
		t.Fatal(err)
	}
	commits, trees, blobs := gitHubAPIKindCounts(t, api)
	if blobs != 1 || commits != 0 || trees != 0 {
		t.Fatalf("LRU blob refetch commits=%d trees=%d blobs=%d, want one blob: %#v", commits, trees, blobs, api.snapshot())
	}
}

func TestGitHubMemoDoesNotSurviveCloseAndReopen(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)
	path := filepath.Join(t.TempDir(), "library.db")
	lib, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Fetch(t.Context(), gitHubSpec()); err != nil {
		t.Fatal(err)
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
	if _, err := reopened.Fetch(t.Context(), gitHubSpec()); err != nil {
		t.Fatal(err)
	}
	commits, trees, blobs := gitHubAPIKindCounts(t, api)
	if commits == 0 || trees == 0 || blobs == 0 {
		t.Fatalf("reopened Fetch commits=%d trees=%d blobs=%d, want a full refetch: %#v", commits, trees, blobs, api.snapshot())
	}
}

func gitHubSpecAt(ref string) library.SourceSpec {
	spec := gitHubSpec()
	spec.Locator.Branch = ref
	return spec
}
