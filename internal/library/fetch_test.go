package library_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"github.com/tiaot33/skills-tui/internal/library"
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

func TestFetchPreviewIsReadOnlyAndSessionScoped(t *testing.T) {
	spec := mockFetchArchive(t)
	path := filepath.Join(t.TempDir(), "library.db")
	lib := openFetchLibrary(t, path)
	ctx := t.Context()
	fetch, err := lib.Fetch(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	want := []library.FetchSkill{{Name: "first", Path: "first"}, {Name: "second", Path: "second"}}
	if fetch.ID == "" || fetch.Ref == "" || !reflect.DeepEqual(fetch.Skills, want) {
		t.Fatalf("Fetch = %#v; want handle, remote ref and candidates %#v", fetch, want)
	}
	abandoned, err := lib.Fetch(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	// URL 安装必须仅依赖暂存,不能再次访问远端。
	http.DefaultClient = &http.Client{Transport: fetchTransport(func(*http.Request) (*http.Response, error) {
		t.Error("install contacted remote source")
		return nil, errors.New("offline")
	})}
	fetch.Skills[0].Name = "changed by caller"
	if skills, err := lib.ListSkills(ctx); err != nil || len(skills) != 0 {
		t.Fatalf("ListSkills = %v, %v", skills, err)
	}
	if collections, err := lib.ListCollections(ctx); err != nil || len(collections) != 0 {
		t.Fatalf("ListCollections = %v, %v", collections, err)
	}
	other := openFetchLibrary(t, path)
	if _, err := other.InstallFetch(ctx, fetch.ID, nil); !library.IsCode(err, library.NotFound) {
		t.Fatalf("other instance accepted handle: %v", err)
	}
	for _, id := range []library.FetchID{"", "unknown"} {
		if _, err := lib.InstallFetch(ctx, id, nil); !library.IsCode(err, library.NotFound) {
			t.Fatalf("unknown handle: %v", err)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := lib.InstallFetch(cancelled, fetch.ID, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled install: %v", err)
	}
	installed, err := lib.InstallFetch(ctx, fetch.ID, []string{"first"})
	if err != nil || len(installed) != 1 || installed[0].Name != "first" || string(installed[0].Files[0].Content) != skillDocument("first", "first") {
		t.Fatalf("install after preview mutation and cancellation = %#v, %v", installed, err)
	}
	if err := lib.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.InstallFetch(ctx, abandoned.ID, nil); !library.IsCode(err, library.NotFound) {
		t.Fatalf("closed instance accepted handle: %v", err)
	}
	if _, err := lib.Fetch(ctx, spec); err == nil {
		t.Fatal("closed instance accepted Fetch")
	}
	reopened := openFetchLibrary(t, path)
	if _, err := reopened.InstallFetch(ctx, abandoned.ID, nil); !library.IsCode(err, library.NotFound) {
		t.Fatalf("reopened instance accepted handle: %v", err)
	}
}

func TestFetchHandleLifetime(t *testing.T) {
	for _, test := range []struct {
		name    string
		wait    time.Duration
		expired bool
	}{
		{name: "before expiry", wait: 29 * time.Minute},
		{name: "abandoned handle expires", wait: 31 * time.Minute, expired: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				spec := mockFetchArchive(t)
				lib := openFetchLibrary(t, ":memory:")
				fetch, err := lib.Fetch(t.Context(), spec)
				if err != nil {
					t.Fatal(err)
				}
				time.Sleep(test.wait)
				synctest.Wait()
				installed, err := lib.InstallFetch(t.Context(), fetch.ID, nil)
				if test.expired {
					if !library.IsCode(err, library.NotFound) || len(installed) != 0 {
						t.Fatalf("expired handle install = %#v, %v", installed, err)
					}
				} else if err != nil || len(installed) != 2 {
					t.Fatalf("valid handle install = %#v, %v", installed, err)
				}
			})
		})
	}
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
	if _, err := other.InstallFetch(t.Context(), fetch.ID, nil); !library.IsCode(err, library.NotFound) {
		t.Fatalf("synced instance accepted handle: %v", err)
	}
	if skills, err := other.ListSkills(t.Context()); err != nil || len(skills) != 0 {
		t.Fatalf("synced skills = %v, %v", skills, err)
	}
	if _, err := lib.InstallFetch(t.Context(), fetch.ID, nil); err != nil {
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

func TestFetchStoreRequiresConfiguredBaseURL(t *testing.T) {
	t.Setenv("SKILLS_STORE_URL", "")
	lib := openTestLibrary(t)
	client := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: fetchTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("unconfigured store contacted network")
		return nil, nil
	})}
	t.Cleanup(func() { http.DefaultClient = client })
	fetch, err := lib.Fetch(t.Context(), library.SourceSpec{
		Type:    library.SourceStore,
		Locator: library.SourceLocator{PackageID: "owner/package", Version: "1.0.0"},
	})
	if !library.IsCode(err, library.NotConfigured) || fetch.ID != "" {
		t.Fatalf("unconfigured store = %#v, %v", fetch, err)
	}
}

func TestFetchMapsErrorsAndPreservesCancellation(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		code   library.ErrorCode
	}{
		{name: "permission", status: http.StatusForbidden, code: library.Permission},
		{name: "unreachable", status: http.StatusServiceUnavailable, code: library.SourceUnreachable},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
			}))
			defer server.Close()
			lib, err := library.Open(filepath.Join(t.TempDir(), "library.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer lib.Close()
			_, err = lib.Fetch(t.Context(), library.SourceSpec{
				Type: library.SourceURL, Locator: library.SourceLocator{URL: server.URL},
			})
			if !library.IsCode(err, test.code) {
				t.Fatalf("error = %v, want code %s", err, test.code)
			}
		})
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	lib, err := library.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	_, err = lib.Fetch(ctx, library.SourceSpec{
		Type: library.SourceURL, Locator: library.SourceLocator{URL: "http://127.0.0.1/skills.zip"},
	})
	if !library.IsCode(err, library.Canceled) || !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want Canceled wrapping context.Canceled", err)
	}
}

func TestFetchInstalledIsQueriedOnEveryListing(t *testing.T) {
	spec := mockFetchArchive(t)
	lib := openTestLibrary(t)
	ctx := t.Context()
	preview, err := lib.Fetch(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "first")
	writeFile(t, filepath.Join(source, "SKILL.md"), []byte("# Local"), 0o644)
	if _, err := lib.Install(ctx, library.LocalSource{Path: source}); err != nil {
		t.Fatal(err)
	}
	candidates, err := lib.ListFetchSkills(ctx, preview.ID)
	if err != nil || candidates.Skills[0].Installed != 0 {
		t.Fatalf("different source = %#v, %v", candidates, err)
	}
	other, err := lib.Fetch(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	installed, err := lib.InstallFetch(ctx, other.ID, []string{"first"})
	if err != nil {
		t.Fatal(err)
	}
	for _, fresh := range []bool{false, true} {
		if fresh {
			again, err := lib.Fetch(ctx, spec)
			if err != nil {
				t.Fatal(err)
			}
			candidates = again.FetchCandidates
		} else {
			candidates, err = lib.ListFetchSkills(ctx, preview.ID)
			if err != nil {
				t.Fatal(err)
			}
		}
		if candidates.Skills[0].Installed != installed[0].ID || candidates.Skills[1].Installed != 0 {
			t.Fatalf("installed = %#v", candidates)
		}
	}
	_, err = lib.InstallFetch(ctx, preview.ID, []string{"first"})
	requireErrorDetails(t, err, library.AlreadyExists, library.ErrorDetails{SkillID: installed[0].ID, Name: "first"})
	if err := lib.RemoveSkill(ctx, installed[0].ID, library.RemoveSkillOptions{}); err != nil {
		t.Fatal(err)
	}
	candidates, err = lib.ListFetchSkills(ctx, preview.ID)
	if err != nil || candidates.Skills[0].Installed != 0 {
		t.Fatalf("removed = %#v, %v", candidates, err)
	}
	data, err := json.Marshal(candidates.Skills[0])
	if err != nil || bytes.Contains(data, []byte(`"installed"`)) {
		t.Fatalf("zero installed JSON = %s, %v", data, err)
	}
	if _, err := lib.ListFetchSkills(ctx, "unknown"); !library.IsCode(err, library.NotFound) {
		t.Fatalf("unknown fetch = %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := lib.ListFetchSkills(canceled, preview.ID); !library.IsCode(err, library.Canceled) {
		t.Fatalf("canceled list = %v", err)
	}
}
