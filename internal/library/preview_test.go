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

func mockPreviewArchive(t *testing.T) library.PreviewFetchRequest {
	t.Helper()
	archive := zipArchive(t, map[string]string{"first/SKILL.md": skillDocument("first", "first"), "second/SKILL.md": skillDocument("second", "second")})
	client := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: fetchTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(archive)), Header: make(http.Header)}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = client })
	return library.PreviewFetchRequest{URL: "https://source.example/skills.zip"}
}

func openLibraryAt(t *testing.T, path string) *library.Library {
	t.Helper()
	lib, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
	return lib
}

func TestPreviewIsReadOnlyAndInstanceScoped(t *testing.T) {
	spec := mockPreviewArchive(t)
	path := filepath.Join(t.TempDir(), "library.db")
	lib := openLibraryAt(t, path)
	ctx := t.Context()
	preview, err := lib.PreviewFetch(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	want := []library.Candidate{{Name: "first", Path: "first"}, {Name: "second", Path: "second"}}
	// Ref 只对 GitHub 有意义;URL 来源只有钉住内容的哈希。
	if preview.ID == "" || preview.Ref != "" || preview.Revision == "" || !reflect.DeepEqual(preview.Candidates, want) {
		t.Fatalf("PreviewFetch = %#v; want handle, content revision and candidates %#v", preview, want)
	}
	abandoned, err := lib.PreviewFetch(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	// URL 入库必须仅依赖入库预览时下载的内容,不能再次访问远端。
	http.DefaultClient = &http.Client{Transport: fetchTransport(func(*http.Request) (*http.Response, error) {
		t.Error("fetch contacted remote source")
		return nil, errors.New("offline")
	})}
	preview.Candidates[0].Name = "changed by caller"
	if skills, err := lib.ListSkills(ctx); err != nil || len(skills) != 0 {
		t.Fatalf("ListSkills = %v, %v", skills, err)
	}
	if collections, err := lib.ListCollections(ctx); err != nil || len(collections) != 0 {
		t.Fatalf("ListCollections = %v, %v", collections, err)
	}
	other := openLibraryAt(t, path)
	if _, err := other.Fetch(ctx, preview.ID, candidateNames(preview)); !library.IsCode(err, library.NotFound) {
		t.Fatalf("other instance accepted handle: %v", err)
	}
	for _, id := range []library.FetchPreviewID{"", "unknown"} {
		if _, err := lib.Fetch(ctx, id, []string{"first"}); !library.IsCode(err, library.NotFound) {
			t.Fatalf("unknown handle: %v", err)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := lib.Fetch(cancelled, preview.ID, []string{"first"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled fetch: %v", err)
	}
	fetched, err := getFetchedSkills(ctx, lib, preview, []string{"first"})
	if err != nil || len(fetched) != 1 || fetched[0].Name != "first" || string(fetched[0].Files[0].Content) != skillDocument("first", "first") {
		t.Fatalf("fetch after preview mutation and cancellation = %#v, %v", fetched, err)
	}
	if err := lib.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Fetch(ctx, abandoned.ID, candidateNames(abandoned)); !library.IsCode(err, library.NotFound) {
		t.Fatalf("closed instance accepted handle: %v", err)
	}
	if _, err := lib.PreviewFetch(ctx, spec); err == nil {
		t.Fatal("closed instance accepted PreviewFetch")
	}
	reopened := openLibraryAt(t, path)
	if _, err := reopened.Fetch(ctx, abandoned.ID, candidateNames(abandoned)); !library.IsCode(err, library.NotFound) {
		t.Fatalf("reopened instance accepted handle: %v", err)
	}
}

func TestPreviewHandleLifetime(t *testing.T) {
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
				spec := mockPreviewArchive(t)
				lib := openLibraryAt(t, ":memory:")
				preview, err := lib.PreviewFetch(t.Context(), spec)
				if err != nil {
					t.Fatal(err)
				}
				time.Sleep(test.wait)
				synctest.Wait()
				fetched, err := lib.Fetch(t.Context(), preview.ID, candidateNames(preview))
				if test.expired {
					if !library.IsCode(err, library.NotFound) || len(fetched) != 0 {
						t.Fatalf("expired handle fetch = %#v, %v", fetched, err)
					}
				} else if err != nil || len(fetched) != 2 {
					t.Fatalf("valid handle fetch = %#v, %v", fetched, err)
				}
			})
		})
	}
}

func TestCloseDuringPreviewDoesNotRetainIt(t *testing.T) {
	spec := mockPreviewArchive(t)
	transport := http.DefaultClient.Transport
	started, resume := make(chan struct{}), make(chan struct{})
	http.DefaultClient.Transport = fetchTransport(func(r *http.Request) (*http.Response, error) {
		close(started)
		<-resume
		return transport.RoundTrip(r)
	})
	lib := openLibraryAt(t, ":memory:")
	finished := make(chan error, 1)
	go func() {
		_, err := lib.PreviewFetch(t.Context(), spec)
		finished <- err
	}()
	<-started
	closeErr := lib.Close()
	close(resume)
	if err := <-finished; err == nil {
		t.Error("PreviewFetch succeeded after Close")
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
}

func TestPreviewsAreNotSynced(t *testing.T) {
	spec := mockPreviewArchive(t)
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
			header.Set("ETag", `"preview-sync"`)
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
	lib := openLibraryAt(t, filepath.Join(t.TempDir(), "library.db"))
	preview, err := lib.PreviewFetch(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := lib.PlanSyncPush(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.SyncPush(t.Context(), plan.Remote); err != nil {
		t.Fatal(err)
	}
	other := openLibraryAt(t, filepath.Join(t.TempDir(), "other.db"))
	plan, err = other.PlanSyncPull(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.SyncPull(t.Context(), plan.Remote); err != nil {
		t.Fatal(err)
	}
	if _, err := other.Fetch(t.Context(), preview.ID, candidateNames(preview)); !library.IsCode(err, library.NotFound) {
		t.Fatalf("synced instance accepted handle: %v", err)
	}
	if skills, err := other.ListSkills(t.Context()); err != nil || len(skills) != 0 {
		t.Fatalf("synced skills = %v, %v", skills, err)
	}
	if _, err := lib.Fetch(t.Context(), preview.ID, candidateNames(preview)); err != nil {
		t.Fatalf("SyncPush invalidated local handle: %v", err)
	}
}

func TestPreviewInvalidArgumentsHaveTypedErrors(t *testing.T) {
	lib := openLibraryAt(t, filepath.Join(t.TempDir(), "library.db"))
	gitHub := &library.GitHubInput{Input: "owner/repo", Ref: "main"}
	for _, tc := range []struct {
		name  string
		req   library.PreviewFetchRequest
		field string
	}{
		{name: "empty", req: library.PreviewFetchRequest{}},
		{name: "github and url", req: library.PreviewFetchRequest{GitHub: gitHub, URL: "https://source.example/skills.zip"}, field: "url"},
		{name: "url and local", req: library.PreviewFetchRequest{URL: "https://source.example/skills.zip", Local: t.TempDir()}, field: "local"},
		{name: "github and local", req: library.PreviewFetchRequest{GitHub: gitHub, Local: t.TempDir()}, field: "local"},
		{name: "empty github input", req: library.PreviewFetchRequest{GitHub: &library.GitHubInput{}}},
		{name: "github without owner", req: library.PreviewFetchRequest{GitHub: &library.GitHubInput{Input: "repo", Ref: "main"}}},
		{name: "invalid url", req: library.PreviewFetchRequest{URL: "://bad"}, field: "url"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := lib.PreviewFetch(t.Context(), tc.req)
			if typed := requireLibraryError(t, err, library.InvalidArgument); typed.Details.Field != tc.field {
				t.Fatalf("PreviewFetch(%#v) field = %q; want %q", tc.req, typed.Details.Field, tc.field)
			}
		})
	}
}

func TestPreviewArchiveDownloadTimesOutAndHonorsCancel(t *testing.T) {
	archive := zipArchive(t, map[string]string{"demo/SKILL.md": skillDocument("demo", "body")})
	for _, spec := range []library.PreviewFetchRequest{
		{URL: "https://source.example/skills.zip"},
	} {
		t.Run("url", func(t *testing.T) {
			lib := openLibraryAt(t, ":memory:")
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
			if _, err := lib.PreviewFetch(t.Context(), spec); err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithCancel(t.Context())
			http.DefaultClient.Transport = fetchTransport(func(r *http.Request) (*http.Response, error) {
				cancel()
				<-r.Context().Done()
				return nil, r.Context().Err()
			})
			if _, err := lib.PreviewFetch(ctx, spec); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel = %v", err)
			}

			synctest.Test(t, func(t *testing.T) {
				http.DefaultClient.Transport = fetchTransport(func(r *http.Request) (*http.Response, error) {
					<-r.Context().Done()
					return nil, r.Context().Err()
				})
				done := make(chan error, 1)
				go func() {
					_, err := lib.PreviewFetch(t.Context(), spec)
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

func TestPreviewMapsErrorsAndPreservesCancellation(t *testing.T) {
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
			_, err = lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{URL: server.URL})
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
	_, err = lib.PreviewFetch(ctx, library.PreviewFetchRequest{URL: "http://127.0.0.1/skills.zip"})
	if !library.IsCode(err, library.Canceled) || !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want Canceled wrapping context.Canceled", err)
	}
}

func TestCandidateFetchedIsQueriedWhenPreviewing(t *testing.T) {
	spec := mockPreviewArchive(t)
	lib := openTestLibrary(t)
	ctx := t.Context()
	preview, err := lib.PreviewFetch(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "first")
	writeLocalSkill(t, source, "# Local")
	if _, err := fetchLocalDir(ctx, lib, source); err != nil {
		t.Fatal(err)
	}
	// 同名但来自另一来源的技能不算已入库。
	different, err := lib.PreviewFetch(ctx, spec)
	if err != nil || different.Candidates[0].Fetched != 0 {
		t.Fatalf("different source = %#v, %v", different, err)
	}
	fetched, err := lib.Fetch(ctx, different.ID, []string{"first"})
	if err != nil {
		t.Fatal(err)
	}
	again, err := lib.PreviewFetch(ctx, spec)
	if err != nil || again.Candidates[0].Fetched != fetched[0].ID || again.Candidates[1].Fetched != 0 {
		t.Fatalf("fetched = %#v, %v", again, err)
	}
	// 入库预览建立时查询:之前打开的入库预览里仍是未入库,入库时按重复整批失败。
	if preview.Candidates[0].Fetched != 0 {
		t.Fatalf("earlier preview changed = %#v", preview.Candidates)
	}
	_, err = lib.Fetch(ctx, preview.ID, []string{"first", "second"})
	requireErrorDetails(t, err, library.AlreadyExists, library.ErrorDetails{SkillID: fetched[0].ID, Name: "first"})
	if err := lib.RemoveSkill(ctx, fetched[0].ID, library.RemoveSkillOptions{}); err != nil {
		t.Fatal(err)
	}
	removed, err := lib.PreviewFetch(ctx, spec)
	if err != nil || removed.Candidates[0].Fetched != 0 {
		t.Fatalf("removed = %#v, %v", removed, err)
	}
	data, err := json.Marshal(removed.Candidates[0])
	if err != nil || bytes.Contains(data, []byte(`"fetched"`)) {
		t.Fatalf("zero fetched JSON = %s, %v", data, err)
	}
}
