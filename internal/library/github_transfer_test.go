package library_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/tiaot33/skills-tui/internal/library"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGitHubRawPreviewUsesOnlyTwoRESTRequests(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"first/SKILL.md":  {content: []byte(skillDocument("first", "one")), mode: 0o644},
		"second/SKILL.md": {content: []byte(skillDocument("second", "two")), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)
	var raw atomic.Int32
	base := http.DefaultClient.Transport
	http.DefaultClient.Transport = fetchTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "raw.githubusercontent.com" {
			return base.RoundTrip(r)
		}
		prefix := "/owner/repo/" + fx.commit + "/"
		p, ok := strings.CutPrefix(r.URL.Path, prefix)
		if !ok {
			t.Errorf("unpinned raw URL: %s", r.URL)
		}
		raw.Add(1)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(fx.blobs[fx.blobSHA(p)])))}, nil
	})
	lib := openTestLibrary(t)
	got, err := lib.PreviewSource(t.Context(), gitHubSpec())
	if err != nil || len(got.Skills) != 2 {
		t.Fatalf("PreviewSource = %#v, %v", got, err)
	}
	commits, trees, blobs := gitHubAPIKindCounts(t, api)
	if commits != 1 || trees != 1 || blobs != 0 || raw.Load() != 2 {
		t.Fatalf("requests: commit=%d tree=%d blob=%d raw=%d", commits, trees, blobs, raw.Load())
	}
}

func TestGitHubTarballFetchAndMissingFileFallback(t *testing.T) {
	for _, missing := range []string{"", "first/file00"} {
		t.Run("missing="+missing, func(t *testing.T) {
			files := map[string]repoFile{
				"first/SKILL.md":  {content: []byte(skillDocument("first", "one")), mode: 0o644},
				"second/SKILL.md": {content: []byte(skillDocument("second", "two")), mode: 0o644},
			}
			for i := range 12 {
				files[fmt.Sprintf("first/file%02d", i)] = repoFile{content: []byte(fmt.Sprintf("file %d", i)), mode: 0o755}
			}
			repo := writeGitHubRepo(t, files)
			fx := inspectGitHubRepo(t, repo)
			api := serveGitHubAPI(t, fx)
			blockGitHubClone(t)
			archive := gitHubTarball(t, fx, missing, nil)
			var downloads atomic.Int32
			base := http.DefaultClient.Transport
			http.DefaultClient.Transport = fetchTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "codeload.github.com" {
					return base.RoundTrip(r)
				}
				if r.URL.Path != "/owner/repo/tar.gz/"+fx.commit {
					t.Errorf("unpinned tarball: %s", r.URL)
				}
				downloads.Add(1)
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(archive))}, nil
			})
			lib := openTestLibrary(t)
			preview, err := lib.PreviewSource(t.Context(), gitHubSpec())
			if err != nil {
				t.Fatal(err)
			}
			api.Reset()
			fetched, err := lib.FetchFromPreview(t.Context(), preview.ID, nil)
			if err != nil || len(fetched) != 2 {
				t.Fatalf("FetchFromPreview = %#v, %v", fetched, err)
			}
			if downloads.Load() != 1 {
				t.Fatalf("tarballs = %d, want 1", downloads.Load())
			}
			want := []string{}
			if missing != "" {
				want = append(want, missing)
			}
			assertGitHubBlobPaths(t, api, want...)
			for _, skill := range fetched {
				for _, f := range skill.Files {
					original := files[skill.Name+"/"+f.Path]
					if !bytes.Equal(f.Content, original.content) {
						t.Errorf("wrong content: %s/%s", skill.Name, f.Path)
					}
					if f.Path != "SKILL.md" && f.Mode != library.ModeExecutable {
						t.Errorf("mode not from tree: %s", f.Path)
					}
				}
			}
		})
	}
}

// gitHubTarball 故意不带可执行位,验证入库文件的权限来自 tree;extra 在正常文件之前写入额外条目。
func gitHubTarball(t *testing.T, fx gitHubFixture, missing string, extra func(*tar.Writer)) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "pax_global_header", Typeflag: tar.TypeXGlobalHeader, PAXRecords: map[string]string{"comment": fx.commit}}); err != nil {
		t.Fatal(err)
	}
	if extra != nil {
		extra(tw)
	}
	for _, e := range fx.entries {
		if e.Type != "blob" || e.Path == missing {
			continue
		}
		b := fx.blobs[e.SHA]
		if err := tw.WriteHeader(&tar.Header{Name: "repo-sha/" + e.Path, Size: int64(len(b)), Mode: 0o644}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestGitHubCLIUsesBatchedGraphQLAndOneTarball(t *testing.T) {
	files := map[string]repoFile{
		"first/SKILL.md":  {content: []byte(skillDocument("first", "one")), mode: 0o644},
		"second/SKILL.md": {content: []byte(skillDocument("second", "two")), mode: 0o644},
	}
	for i := range 12 {
		files[fmt.Sprintf("first/file%02d", i)] = repoFile{content: []byte(fmt.Sprintf("file %d", i)), mode: 0o644}
	}
	repo := writeGitHubRepo(t, files)
	fx := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, fx)
	api.anonymousStatus = http.StatusTooManyRequests
	api.rateLimitReset = strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
	blockGitHubClone(t)
	serveGitHubCLI(t, fx)
	oldGH, err := exec.LookPath("gh")
	if err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "calls")
	archive := filepath.Join(t.TempDir(), "archive")
	writeFile(t, archive, gitHubTarball(t, fx, "", nil), 0o600)
	t.Setenv("TRANSFER_OLD_GH", oldGH)
	t.Setenv("TRANSFER_LOG", log)
	t.Setenv("TRANSFER_ARCHIVE", archive)
	payload := map[string]any{}
	for i, p := range []string{"first/SKILL.md", "second/SKILL.md"} {
		b := fx.blobs[fx.blobSHA(p)]
		payload[fmt.Sprintf("b%d", i)] = map[string]any{"text": string(b), "isBinary": false, "isTruncated": false, "byteSize": len(b)}
	}
	body, _ := json.Marshal(map[string]any{"data": map[string]any{"repository": payload}})
	stubGitHubCLI(t, `echo "$2" >> "$TRANSFER_LOG"
[ -z "$GITHUB_TOKEN$GH_TOKEN" ] || exit 1
[ "$GH_PROMPT_DISABLED" = 1 ] || exit 1
case "$2" in
 graphql) cat <<'JSON'
`+string(body)+`
JSON
;;
 */tarball/*) cat "$TRANSFER_ARCHIVE" ;;
 *) exec "$TRANSFER_OLD_GH" "$@" ;;
esac
`)
	lib := openTestLibrary(t)
	preview, err := lib.PreviewSource(t.Context(), gitHubSpec())
	if err != nil || len(preview.Skills) != 2 {
		t.Fatalf("PreviewSource = %#v, %v", preview, err)
	}
	calls, _ := os.ReadFile(log)
	if strings.Count(string(calls), "graphql") != 1 || strings.Contains(string(calls), "/git/blobs/") {
		t.Fatalf("preview CLI calls: %s", calls)
	}
	if err := os.WriteFile(log, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	fetched, err := lib.FetchFromPreview(t.Context(), preview.ID, nil)
	if err != nil || len(fetched) != 2 {
		t.Fatalf("install = %#v, %v", fetched, err)
	}
	calls, _ = os.ReadFile(log)
	if strings.Count(string(calls), "/tarball/") != 1 || strings.Contains(string(calls), "/git/blobs/") {
		t.Fatalf("install CLI calls: %s", calls)
	}
}

func TestGitHubRawMismatchFallsBackAndMemoIsReused(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md": {content: []byte(skillDocument("demo", "correct")), mode: 0o644},
	})
	fx := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)
	var raw atomic.Int32
	base := http.DefaultClient.Transport
	http.DefaultClient.Transport = fetchTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "raw.githubusercontent.com" {
			return base.RoundTrip(r)
		}
		raw.Add(1)
		return transferResponse([]byte(skillDocument("wrong", "bad SHA"))), nil
	})
	lib := openTestLibrary(t)
	for range 2 {
		preview, err := lib.PreviewSource(t.Context(), gitHubSpec())
		if err != nil || len(preview.Skills) != 1 || preview.Skills[0].Name != "demo" {
			t.Fatalf("PreviewSource = %#v, %v", preview, err)
		}
	}
	if raw.Load() != 1 {
		t.Fatalf("memo did not avoid raw: %d", raw.Load())
	}
	assertGitHubBlobPaths(t, api, "demo/SKILL.md")
}

func TestGitHubSmallOrOversizedRepositoryUsesRawFetch(t *testing.T) {
	for _, scenario := range []string{"small", "mostly-unrelated", "too-many-bytes", "unknown-size"} {
		t.Run(scenario, func(t *testing.T) {
			count := 1
			if scenario != "small" {
				count = 12
			}
			files := map[string]repoFile{"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644}}
			for i := range count {
				files[fmt.Sprintf("demo/file%02d", i)] = repoFile{content: []byte(fmt.Sprintf("data %d", i)), mode: 0o644}
			}
			repo := writeGitHubRepo(t, files)
			fx := inspectGitHubRepo(t, repo)
			switch scenario {
			case "mostly-unrelated":
				// 整仓超过小仓阈值,待取字节远小于整仓:不值得下载归档。
				fx.entries = append(fx.entries, gitHubTreeEntry{Path: "unrelated/big", Type: "blob", Mode: "100644", SHA: fx.blobSHA("demo/file00"), Size: 5 << 20})
			case "too-many-bytes":
				fx.entries = append(fx.entries, gitHubTreeEntry{Path: "unrelated/huge", Type: "blob", Mode: "100644", SHA: fx.blobSHA("demo/file00"), Size: 65 << 20})
			}
			api := serveGitHubAPI(t, fx)
			blockGitHubClone(t)
			var raw, archives atomic.Int32
			base := http.DefaultClient.Transport
			http.DefaultClient.Transport = fetchTransport(func(r *http.Request) (*http.Response, error) {
				if strings.Contains(r.URL.Path, "/tarball/") || r.URL.Host == "codeload.github.com" {
					archives.Add(1)
				}
				if r.URL.Host == "raw.githubusercontent.com" {
					p := strings.TrimPrefix(r.URL.Path, "/owner/repo/"+fx.commit+"/")
					raw.Add(1)
					return transferResponse(fx.blobs[fx.blobSHA(p)]), nil
				}
				if scenario == "unknown-size" && strings.Contains(r.URL.Path, "/git/trees/") {
					entries := []map[string]any{}
					for _, e := range fx.entries {
						entries = append(entries, map[string]any{"path": e.Path, "type": e.Type, "mode": e.Mode, "sha": e.SHA})
					}
					return jsonResponse(200, map[string]any{"tree": entries}), nil
				}
				return base.RoundTrip(r)
			})
			lib := openTestLibrary(t)
			preview, err := lib.PreviewSource(t.Context(), gitHubSpec())
			if err != nil {
				t.Fatal(err)
			}
			raw.Store(0)
			api.Reset()
			got, err := lib.FetchFromPreview(t.Context(), preview.ID, []string{"demo"})
			if err != nil || len(got) != 1 || len(got[0].Files) != count+1 {
				t.Fatalf("install = %#v, %v", got, err)
			}
			if raw.Load() != int32(count) || archives.Load() != 0 {
				t.Fatalf("raw=%d archives=%d", raw.Load(), archives.Load())
			}
			assertGitHubBlobPaths(t, api)
		})
	}
}

func TestGitHubRawPrivateTokenAndRedirectDropsAuthorization(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644}})
	fx := inspectGitHubRepo(t, repo)
	serveGitHubAPI(t, fx)
	blockGitHubClone(t)
	t.Setenv("GITHUB_TOKEN", "private-test-token")
	base := http.DefaultClient.Transport
	var anonymous, token, redirected atomic.Int32
	http.DefaultClient.Transport = fetchTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "raw.githubusercontent.com" {
			if r.Header.Get("Authorization") == "" {
				anonymous.Add(1)
				return jsonResponse(404, nil), nil
			}
			if r.Header.Get("Authorization") != "Bearer private-test-token" {
				t.Error("wrong token header")
			}
			token.Add(1)
			response := jsonResponse(302, nil)
			response.Header.Set("Location", "https://child.raw.githubusercontent.com/file")
			return response, nil
		}
		if r.URL.Host == "child.raw.githubusercontent.com" {
			if r.Header.Get("Authorization") != "" {
				t.Error("redirect leaked token")
			}
			redirected.Add(1)
			return transferResponse(fx.blobs[fx.blobSHA("demo/SKILL.md")]), nil
		}
		return base.RoundTrip(r)
	})
	lib := openTestLibrary(t)
	preview, err := lib.PreviewSource(t.Context(), gitHubSpec())
	if err != nil || len(preview.Skills) != 1 {
		t.Fatalf("PreviewSource = %#v, %v", preview, err)
	}
	if anonymous.Load() != 1 || token.Load() != 1 || redirected.Load() != 1 {
		t.Fatalf("anon=%d token=%d redirects=%d", anonymous.Load(), token.Load(), redirected.Load())
	}
}

func transferResponse(body []byte) *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body))}
}

func TestGitHubGraphQLFallbackAndBatchLimit(t *testing.T) {
	for _, scenario := range []string{"null", "empty", "bad-sha", "large-batch"} {
		t.Run(scenario, func(t *testing.T) {
			count := 2
			if scenario == "large-batch" {
				count = 101
			}
			files := map[string]repoFile{}
			for i := range count {
				name := fmt.Sprintf("skill%03d", i)
				files[name+"/SKILL.md"] = repoFile{content: []byte(skillDocument(name, "body")), mode: 0o644}
			}
			repo := writeGitHubRepo(t, files)
			fx := inspectGitHubRepo(t, repo)
			api := serveGitHubAPI(t, fx)
			api.anonymousStatus = 404
			blockGitHubClone(t)
			serveGitHubCLI(t, fx)
			oldGH, err := exec.LookPath("gh")
			if err != nil {
				t.Fatal(err)
			}
			log := filepath.Join(t.TempDir(), "calls")
			t.Setenv("TRANSFER_OLD_GH", oldGH)
			t.Setenv("TRANSFER_LOG", log)
			objects := map[string]any{}
			for i := range count {
				content := fx.blobs[fx.blobSHA(fmt.Sprintf("skill%03d/SKILL.md", i))]
				blob := map[string]any{"text": string(content)}
				if i == 0 {
					switch scenario {
					case "null":
						blob["text"] = nil
					case "empty":
						blob["text"] = ""
					case "bad-sha":
						blob["text"] = strings.Repeat("x", len(content))
					}
				}
				objects[fmt.Sprintf("b%d", i)] = blob
			}
			body, _ := json.Marshal(map[string]any{"data": map[string]any{"repository": objects}})
			stubGitHubCLI(t, `echo "$*" >> "$TRANSFER_LOG"
if [ "$2" = graphql ]; then
cat <<'JSON'
`+string(body)+`
JSON
else
exec "$TRANSFER_OLD_GH" "$@"
fi
`)
			lib := openTestLibrary(t)
			preview, err := lib.PreviewSource(t.Context(), gitHubSpec())
			if err != nil || len(preview.Skills) != count {
				t.Fatalf("PreviewSource candidates=%d, err=%v", len(preview.Skills), err)
			}
			calls, _ := os.ReadFile(log)
			wantBatches, wantBlobs := 1, 1
			if scenario == "large-batch" {
				wantBatches, wantBlobs = 2, 0
			}
			if strings.Count(string(calls), "api graphql") != wantBatches || strings.Count(string(calls), "/git/blobs/") != wantBlobs {
				t.Fatalf("CLI calls: %s", calls)
			}
			for line := range strings.SplitSeq(string(calls), "\n") {
				if strings.Contains(line, "api graphql") && strings.Count(line, "object(oid:") > 100 {
					t.Error("GraphQL batch exceeds limit")
				}
			}
		})
	}
}

func TestGitHubTarballSkipsUnrelatedEntriesAndEnforcesLimits(t *testing.T) {
	for _, scenario := range []string{"unrelated-entries", "symlink-at-wanted-path", "expanded-limit", "download-limit"} {
		t.Run(scenario, func(t *testing.T) {
			files := map[string]repoFile{"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644}}
			for i := range 12 {
				files[fmt.Sprintf("demo/file%02d", i)] = repoFile{content: []byte(fmt.Sprintf("data %d", i)), mode: 0o644}
			}
			repo := writeGitHubRepo(t, files)
			fx := inspectGitHubRepo(t, repo)
			api := serveGitHubAPI(t, fx)
			blockGitHubClone(t)
			writeHeader := func(tw *tar.Writer, header *tar.Header) {
				if err := tw.WriteHeader(header); err != nil {
					t.Fatal(err)
				}
			}
			var archive []byte
			var wantBlobs, allFiles []string
			for i := range 12 {
				allFiles = append(allFiles, fmt.Sprintf("demo/file%02d", i))
			}
			switch scenario {
			case "unrelated-entries":
				// 归档不落盘、正文按 SHA 校验,无关的异常条目不应让整个归档作废。
				archive = gitHubTarball(t, fx, "", func(tw *tar.Writer) {
					writeHeader(tw, &tar.Header{Name: "repo-sha/../../escape", Mode: 0o644})
					writeHeader(tw, &tar.Header{Name: "repo-sha/link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"})
				})
			case "symlink-at-wanted-path":
				archive = gitHubTarball(t, fx, "demo/file00", func(tw *tar.Writer) {
					writeHeader(tw, &tar.Header{Name: "repo-sha/demo/file00", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"})
				})
				wantBlobs = []string{"demo/file00"}
			case "expanded-limit":
				// 压缩后很小、展开超限的条目排在最前,读到上限即停止,全部文件逐个回退。
				archive = gitHubTarball(t, fx, "", func(tw *tar.Writer) {
					writeHeader(tw, &tar.Header{Name: "repo-sha/zeros", Mode: 0o644, Size: 65 << 20})
					if _, err := io.Copy(tw, io.LimitReader(zeroTransferReader{}, 65<<20)); err != nil {
						t.Fatal(err)
					}
				})
				wantBlobs = allFiles
			case "download-limit":
				wantBlobs = allFiles
			}
			var archives atomic.Int32
			base := http.DefaultClient.Transport
			http.DefaultClient.Transport = fetchTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "codeload.github.com" {
					return base.RoundTrip(r)
				}
				archives.Add(1)
				if scenario == "download-limit" {
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(io.LimitReader(zeroTransferReader{}, (64<<20)+1))}, nil
				}
				return transferResponse(archive), nil
			})
			lib := openTestLibrary(t)
			preview, err := lib.PreviewSource(t.Context(), gitHubSpec())
			if err != nil {
				t.Fatal(err)
			}
			api.Reset()
			fetched, err := lib.FetchFromPreview(t.Context(), preview.ID, nil)
			if err != nil || len(fetched) != 1 || len(fetched[0].Files) != 13 {
				t.Fatalf("FetchFromPreview = %#v, %v", fetched, err)
			}
			for _, f := range fetched[0].Files {
				if !bytes.Equal(f.Content, files["demo/"+f.Path].content) {
					t.Errorf("wrong content: %s", f.Path)
				}
			}
			if archives.Load() != 1 {
				t.Fatalf("archives = %d, want 1", archives.Load())
			}
			assertGitHubBlobPaths(t, api, wantBlobs...)
		})
	}
}

func TestGitHubLargeRepositoryUsesTarballWhenMostBytesAreWanted(t *testing.T) {
	files := map[string]repoFile{"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644}}
	for i := range 12 {
		files[fmt.Sprintf("demo/file%02d", i)] = repoFile{content: []byte(fmt.Sprintf("data %d", i)), mode: 0o644}
	}
	repo := writeGitHubRepo(t, files)
	fx := inspectGitHubRepo(t, repo)
	// 只调大 tree 声明的大小:待取约 12MB,整仓约 32MB,超过小仓阈值但占比足够。
	for i, e := range fx.entries {
		if strings.HasPrefix(e.Path, "demo/file") {
			fx.entries[i].Size = 1 << 20
		}
	}
	fx.entries = append(fx.entries, gitHubTreeEntry{Path: "unrelated/big", Type: "blob", Mode: "100644", SHA: fx.blobSHA("demo/file00"), Size: 20 << 20})
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)
	archive := gitHubTarball(t, fx, "unrelated/big", nil)
	var archives atomic.Int32
	base := http.DefaultClient.Transport
	http.DefaultClient.Transport = fetchTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "codeload.github.com" {
			return base.RoundTrip(r)
		}
		archives.Add(1)
		return transferResponse(archive), nil
	})
	lib := openTestLibrary(t)
	preview, err := lib.PreviewSource(t.Context(), gitHubSpec())
	if err != nil {
		t.Fatal(err)
	}
	api.Reset()
	fetched, err := lib.FetchFromPreview(t.Context(), preview.ID, nil)
	if err != nil || len(fetched) != 1 || len(fetched[0].Files) != 13 {
		t.Fatalf("FetchFromPreview = %#v, %v", fetched, err)
	}
	if archives.Load() != 1 {
		t.Fatalf("archives = %d, want 1", archives.Load())
	}
	assertGitHubBlobPaths(t, api)
}

type zeroTransferReader struct{}

func (zeroTransferReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestGitHubRawCancellationStopsAllRequests(t *testing.T) {
	files := map[string]repoFile{}
	for i := range 16 {
		name := fmt.Sprintf("skill%02d", i)
		files[name+"/SKILL.md"] = repoFile{content: []byte(skillDocument(name, "body")), mode: 0o644}
	}
	repo := writeGitHubRepo(t, files)
	api := serveGitHubAPI(t, inspectGitHubRepo(t, repo))
	blockGitHubClone(t)
	base := http.DefaultClient.Transport
	started := make(chan struct{}, 16)
	var active, peak atomic.Int32
	http.DefaultClient.Transport = fetchTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "raw.githubusercontent.com" {
			return base.RoundTrip(r)
		}
		n := active.Add(1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		defer active.Add(-1)
		started <- struct{}{}
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	lib := openTestLibrary(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := lib.PreviewSource(ctx, gitHubSpec()); done <- err }()
	for range 8 {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("raw batch did not start")
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("PreviewSource = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not stop raw requests")
	}
	if active.Load() != 0 || peak.Load() > 8 {
		t.Fatalf("active=%d peak=%d", active.Load(), peak.Load())
	}
	assertGitHubBlobPaths(t, api)
}

func TestGitHubTransferCancellationStopsCLIAndDownloads(t *testing.T) {
	for _, phase := range []string{"graphql", "tarball", "http-tarball"} {
		t.Run(phase, func(t *testing.T) {
			files := map[string]repoFile{"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644}}
			for i := range 12 {
				files[fmt.Sprintf("demo/file%02d", i)] = repoFile{content: []byte(fmt.Sprintf("data %d", i)), mode: 0o644}
			}
			repo := writeGitHubRepo(t, files)
			fx := inspectGitHubRepo(t, repo)
			api := serveGitHubAPI(t, fx)
			blockGitHubClone(t)
			lib := openTestLibrary(t)
			var preview library.Preview
			if phase != "graphql" {
				var err error
				preview, err = lib.PreviewSource(t.Context(), gitHubSpec())
				if err != nil {
					t.Fatal(err)
				}
			}
			ready := filepath.Join(t.TempDir(), "ready")
			escaped := filepath.Join(t.TempDir(), "escaped")
			if phase != "http-tarball" {
				api.anonymousStatus = 404
				serveGitHubCLI(t, fx)
				oldGH, err := exec.LookPath("gh")
				if err != nil {
					t.Fatal(err)
				}
				t.Setenv("TRANSFER_OLD_GH", oldGH)
				t.Setenv("TRANSFER_READY", ready)
				t.Setenv("TRANSFER_ESCAPED", escaped)
				stubGitHubCLI(t, `case "$2" in
 graphql|*/tarball/*)
 sh -c 'echo ready > "$TRANSFER_READY"; sleep 1; echo escaped > "$TRANSFER_ESCAPED"' &
 wait
 ;;
 *) exec "$TRANSFER_OLD_GH" "$@" ;;
esac
`)
			}
			base := http.DefaultClient.Transport
			http.DefaultClient.Transport = fetchTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "codeload.github.com" {
					if phase != "http-tarball" {
						return jsonResponse(404, nil), nil
					}
					if err := os.WriteFile(ready, nil, 0o600); err != nil {
						return nil, err
					}
					<-r.Context().Done()
					return nil, r.Context().Err()
				}
				return base.RoundTrip(r)
			})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				if phase == "graphql" {
					_, err := lib.PreviewSource(ctx, gitHubSpec())
					done <- err
				} else {
					_, err := lib.FetchFromPreview(ctx, preview.ID, nil)
					done <- err
				}
			}()
			deadline := time.Now().Add(3 * time.Second)
			for {
				if _, err := os.Stat(ready); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("transfer did not start")
				}
				time.Sleep(10 * time.Millisecond)
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancel error = %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("transfer did not stop")
			}
			if phase != "http-tarball" {
				time.Sleep(1100 * time.Millisecond)
				if _, err := os.Stat(escaped); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("CLI child survived: %v", err)
				}
			}
			if skills, err := lib.ListSkills(t.Context()); err != nil || len(skills) != 0 {
				t.Fatalf("canceled transfer wrote skills: %#v, %v", skills, err)
			}
		})
	}
}

func TestGitHubRawUpdateReusesUnchangedLargeStoredFile(t *testing.T) {
	files := map[string]repoFile{
		"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644},
		"demo/large":    {content: bytes.Repeat([]byte("x"), 300<<10), mode: 0o644},
		"demo/change":   {content: []byte("old"), mode: 0o644},
	}
	repo := writeGitHubRepo(t, files)
	first := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, first)
	blockGitHubClone(t)
	lib := openTestLibrary(t)
	preview, err := lib.PreviewSource(t.Context(), gitHubSpec())
	if err != nil {
		t.Fatal(err)
	}
	fetched, err := lib.FetchFromPreview(t.Context(), preview.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo, "demo/change"), []byte("new"), 0o644)
	commitGit(t, repo, "change one file")
	next := inspectGitHubRepo(t, repo)
	api.Swap(next)
	api.Reset()
	var raw atomic.Int32
	base := http.DefaultClient.Transport
	http.DefaultClient.Transport = fetchTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "raw.githubusercontent.com" {
			return base.RoundTrip(r)
		}
		p := strings.TrimPrefix(r.URL.Path, "/owner/repo/"+next.commit+"/")
		if p != "demo/change" {
			t.Errorf("redownloaded unchanged file: %s", p)
		}
		raw.Add(1)
		return transferResponse(next.blobs[next.blobSHA(p)]), nil
	})
	result, err := lib.Update(t.Context(), fetched[0].ID)
	if err != nil || !result.Changed || len(result.Skill.Files) != 3 {
		t.Fatalf("Update = %#v, %v", result, err)
	}
	if raw.Load() != 1 {
		t.Errorf("raw downloads = %d, want 1", raw.Load())
	}
	assertGitHubBlobPaths(t, api)
}

func TestGitHubEmptyGraphQLDoesNotRememberCLIAuthentication(t *testing.T) {
	repo := writeGitHubRepo(t, map[string]repoFile{"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644}})
	fx := inspectGitHubRepo(t, repo)
	api := serveGitHubAPI(t, fx)
	blockGitHubClone(t)
	serveGitHubCLI(t, fx)
	oldGH, err := exec.LookPath("gh")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TRANSFER_OLD_GH", oldGH)
	stubGitHubCLI(t, `if [ "$2" = graphql ]; then
 printf '%s\n' '{"data":{"repository":{}},"errors":[{"message":"unavailable"}]}'
 exit 1
fi
exec "$TRANSFER_OLD_GH" "$@"
`)
	base := http.DefaultClient.Transport
	http.DefaultClient.Transport = fetchTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "raw.githubusercontent.com" {
			return jsonResponse(404, nil), nil
		}
		return base.RoundTrip(r)
	})
	lib := openTestLibrary(t)
	if _, err := lib.PreviewSource(t.Context(), gitHubSpec()); err != nil {
		t.Fatal(err)
	}
	api.Reset()
	if _, err := lib.PreviewSource(t.Context(), gitHubSpec()); err != nil {
		t.Fatal(err)
	}
	commits, _, _ := gitHubAPIKindCounts(t, api)
	if commits != 1 {
		t.Fatalf("failed GraphQL remembered CLI authentication: HTTP requests=%#v", api.snapshot())
	}
}
