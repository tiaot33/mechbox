package library_test

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tiaot33/skills-tui/library"
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

func TestGitHubInstallFetchDoesNotRefetchPreviewBlobs(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"first/SKILL.md":    {content: []byte(skillDocument("first", "first body")), mode: 0o644},
		"first/scripts/run": {content: []byte{0, 255, 1}, mode: 0o755},
		"first/data.bin":    {content: []byte{0, 1, 2, 255}, mode: 0o644},
		"second/SKILL.md":   {content: []byte(skillDocument("second", "second body")), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)
	lib := openTestLibrary(t)
	fetch, err := lib.Fetch(t.Context(), gitHubSpec())
	if err != nil {
		t.Fatal(err)
	}
	api.Reset()
	installed, err := lib.InstallFetch(t.Context(), fetch.ID, []string{"first"})
	if err != nil || len(installed) != 1 {
		t.Fatalf("InstallFetch = %#v, %v", installed, err)
	}
	commits, trees, _ := gitHubAPIKindCounts(t, api)
	if commits != 0 || trees != 0 {
		t.Fatalf("InstallFetch commits=%d trees=%d, want blobs only: %#v", commits, trees, api.snapshot())
	}
	assertGitHubBlobPaths(t, api, "first/data.bin", "first/scripts/run")
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
