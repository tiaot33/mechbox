package library_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"github.com/tiaot33/skills-tui/library"
)

type fetchTransport func(*http.Request) (*http.Response, error)

func (f fetchTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func mockFetchArchive(t *testing.T) library.SourceSpec {
	t.Helper()
	archive := zipArchive(t, map[string]string{"first/SKILL.md": skillDocument("first", "first"), "second/SKILL.md": skillDocument("second", "second")})
	client := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: fetchTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(archive)), Header: make(http.Header)}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = client })
	return library.SourceSpec{Type: library.SourceURL, Locator: library.SourceLocator{URL: "https://source.example/skills.zip"}}
}

func openFetchLibrary(t *testing.T, path string) library.Library {
	t.Helper()
	lib, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
	return lib
}

func TestFetchListingIsReadOnlyAndSessionScoped(t *testing.T) {
	spec := mockFetchArchive(t)
	path := filepath.Join(t.TempDir(), "library.db")
	lib := openFetchLibrary(t, path)
	ctx := t.Context()
	fetch, err := lib.Fetch(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	// 列表必须仅依赖暂存,不能再次访问远端。
	http.DefaultClient = &http.Client{Transport: fetchTransport(func(*http.Request) (*http.Response, error) {
		t.Error("listing contacted remote source")
		return nil, errors.New("offline")
	})}
	fetch.Skills[0].Name = "changed by caller"
	for range 2 {
		names, err := lib.ListFetchSkills(ctx, fetch.ID)
		if err != nil || !reflect.DeepEqual(names.Skills, []library.FetchSkill{{Name: "first", Path: "first"}, {Name: "second", Path: "second"}}) {
			t.Fatalf("ListFetchSkills = %v, %v", names, err)
		}
		names.Skills[0].Name = "changed by caller"
	}
	if skills, err := lib.ListSkills(ctx); err != nil || len(skills) != 0 {
		t.Fatalf("ListSkills = %v, %v", skills, err)
	}
	if collections, err := lib.ListCollections(ctx); err != nil || len(collections) != 0 {
		t.Fatalf("ListCollections = %v, %v", collections, err)
	}
	other := openFetchLibrary(t, path)
	if _, err := other.ListFetchSkills(ctx, fetch.ID); !library.IsCode(err, library.NotFound) {
		t.Fatalf("other instance accepted handle: %v", err)
	}
	for _, id := range []library.FetchID{"", "unknown"} {
		if _, err := lib.ListFetchSkills(ctx, id); !library.IsCode(err, library.NotFound) {
			t.Fatalf("unknown handle: %v", err)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := lib.ListFetchSkills(cancelled, fetch.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled listing: %v", err)
	}
	if err := lib.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.ListFetchSkills(ctx, fetch.ID); !library.IsCode(err, library.NotFound) {
		t.Fatalf("closed instance accepted handle: %v", err)
	}
	if _, err := lib.Fetch(ctx, spec); err == nil {
		t.Fatal("closed instance accepted Fetch")
	}
	reopened := openFetchLibrary(t, path)
	if _, err := reopened.ListFetchSkills(ctx, fetch.ID); !library.IsCode(err, library.NotFound) {
		t.Fatalf("reopened instance accepted handle: %v", err)
	}
}

func TestAbandonedFetchExpiresWithoutAnotherFetch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		spec := mockFetchArchive(t)
		lib := openFetchLibrary(t, ":memory:")
		fetch, err := lib.Fetch(t.Context(), spec)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(29 * time.Minute)
		if _, err := lib.ListFetchSkills(t.Context(), fetch.ID); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Minute)
		synctest.Wait()
		if _, err := lib.ListFetchSkills(t.Context(), fetch.ID); !library.IsCode(err, library.NotFound) {
			t.Fatalf("expired handle: %v", err)
		}
	})
}

func TestCloseDuringFetchDoesNotRetainSession(t *testing.T) {
	spec := mockFetchArchive(t)
	transport := http.DefaultClient.Transport
	started, resume := make(chan struct{}), make(chan struct{})
	http.DefaultClient.Transport = fetchTransport(func(r *http.Request) (*http.Response, error) {
		close(started)
		<-resume
		return transport.RoundTrip(r)
	})
	lib := openFetchLibrary(t, ":memory:")
	finished := make(chan error, 1)
	go func() {
		_, err := lib.Fetch(t.Context(), spec)
		finished <- err
	}()
	<-started
	closeErr := lib.Close()
	close(resume)
	if err := <-finished; err == nil {
		t.Error("Fetch succeeded after Close")
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
}

func TestFetchSessionsAreNotSynced(t *testing.T) {
	spec := mockFetchArchive(t)
	archiveTransport := http.DefaultClient.Transport
	var stored []byte
	http.DefaultClient.Transport = fetchTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "source.example" {
			return archiveTransport.RoundTrip(r)
		}
		if r.Method == http.MethodPut {
			var err error
			stored, err = io.ReadAll(r.Body)
			if err != nil {
				return nil, err
			}
		}
		header := make(http.Header)
		if stored != nil {
			header.Set("ETag", `"fetch-sync"`)
		}
		status := http.StatusOK
		if stored == nil && r.Method != http.MethodPut {
			status = http.StatusNotFound
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(stored)), Header: header}, nil
	})
	t.Setenv("SKILLS_S3_ENDPOINT", "https://s3.example")
	t.Setenv("SKILLS_S3_BUCKET", "test-bucket")
	t.Setenv("SKILLS_S3_KEY", "library.db")
	t.Setenv("AWS_REGION", "test-region")
	t.Setenv("AWS_ACCESS_KEY_ID", "test-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret")
	lib := openFetchLibrary(t, filepath.Join(t.TempDir(), "library.db"))
	fetch, err := lib.Fetch(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := lib.PreviewSyncPush(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.SyncPush(t.Context(), preview.Remote); err != nil {
		t.Fatal(err)
	}
	other := openFetchLibrary(t, filepath.Join(t.TempDir(), "other.db"))
	preview, err = other.PreviewSyncPull(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.SyncPull(t.Context(), preview.Remote); err != nil {
		t.Fatal(err)
	}
	if _, err := other.ListFetchSkills(t.Context(), fetch.ID); !library.IsCode(err, library.NotFound) {
		t.Fatalf("synced instance accepted handle: %v", err)
	}
	if skills, err := other.ListSkills(t.Context()); err != nil || len(skills) != 0 {
		t.Fatalf("synced skills = %v, %v", skills, err)
	}
	if _, err := lib.ListFetchSkills(t.Context(), fetch.ID); err != nil {
		t.Fatalf("SyncPush invalidated local handle: %v", err)
	}
}

func TestFetchInvalidArgumentsHaveTypedErrors(t *testing.T) {
	lib := openFetchLibrary(t, filepath.Join(t.TempDir(), "library.db"))
	for _, spec := range []library.SourceSpec{
		{Type: library.SourceLocal},
		{Type: "unsupported"},
		{Type: library.SourceGitHub, Locator: library.SourceLocator{Repository: "repo"}},
		{Type: library.SourceGitHub, Locator: library.SourceLocator{Branch: "main"}},
		{Type: library.SourceURL},
		{Type: library.SourceStore, Locator: library.SourceLocator{PackageID: "package"}},
		{Type: library.SourceStore, Locator: library.SourceLocator{Version: "v1"}},
	} {
		if _, err := lib.Fetch(t.Context(), spec); !library.IsCode(err, library.InvalidArgument) {
			t.Errorf("Fetch(%#v) = %v; want InvalidArgument", spec, err)
		}
	}
}

func TestFetchArchiveDownloadTimesOutAndHonorsCancel(t *testing.T) {
	t.Setenv("SKILLS_STORE_URL", "https://store.example/skills")
	archive := zipArchive(t, map[string]string{"demo/SKILL.md": skillDocument("demo", "body")})
	for _, spec := range []library.SourceSpec{
		{Type: library.SourceURL, Locator: library.SourceLocator{URL: "https://source.example/skills.zip"}},
		{Type: library.SourceStore, Locator: library.SourceLocator{PackageID: "owner/package", Version: "1.0.0"}},
	} {
		t.Run(string(spec.Type), func(t *testing.T) {
			lib := openFetchLibrary(t, ":memory:")
			client := http.DefaultClient
			t.Cleanup(func() { http.DefaultClient = client })

			http.DefaultClient = &http.Client{Transport: fetchTransport(func(r *http.Request) (*http.Response, error) {
				deadline, ok := r.Context().Deadline()
				if !ok {
					t.Error("download lacks timeout")
				} else if remaining := time.Until(deadline); remaining > 30*time.Second {
					t.Errorf("download timeout = %v, want at most 30s", remaining)
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(archive)), Header: make(http.Header)}, nil
			})}
			if _, err := lib.Fetch(t.Context(), spec); err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithCancel(t.Context())
			http.DefaultClient.Transport = fetchTransport(func(r *http.Request) (*http.Response, error) {
				cancel()
				<-r.Context().Done()
				return nil, r.Context().Err()
			})
			if _, err := lib.Fetch(ctx, spec); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel = %v", err)
			}

			synctest.Test(t, func(t *testing.T) {
				http.DefaultClient.Transport = fetchTransport(func(r *http.Request) (*http.Response, error) {
					<-r.Context().Done()
					return nil, r.Context().Err()
				})
				done := make(chan error, 1)
				go func() {
					_, err := lib.Fetch(t.Context(), spec)
					done <- err
				}()
				synctest.Wait()
				time.Sleep(30 * time.Second)
				if err := <-done; !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("timeout = %v", err)
				}
			})
		})
	}
}
