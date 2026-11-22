package library_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tiaot33/mechbox/internal/library"
	"github.com/tiaot33/mechbox/internal/library/platformtest"
)

func TestProxySettingsRoundTripAndValidation(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "library.db")
	lib := openTestLibraryAt(t, path)

	got, err := lib.ProxySettings(t.Context())
	if err != nil {
		t.Fatalf("ProxySettings: %v", err)
	}
	if got.Mode != library.ProxySystem || got.URL != "" {
		t.Fatalf("default ProxySettings = %+v; want system and empty URL", got)
	}

	requireErrorDetails(t, lib.SetProxySettings(t.Context(), library.ProxySettings{Mode: "bogus"}),
		library.InvalidArgument, library.ErrorDetails{Field: "mode"})
	requireErrorDetails(t, lib.SetProxySettings(t.Context(), library.ProxySettings{Mode: library.ProxyManual}),
		library.InvalidArgument, library.ErrorDetails{Field: "url"})
	requireErrorDetails(t, lib.SetProxySettings(t.Context(), library.ProxySettings{Mode: library.ProxyManual, URL: "127.0.0.1:7890"}),
		library.InvalidArgument, library.ErrorDetails{Field: "url"})
	requireErrorDetails(t, lib.SetProxySettings(t.Context(), library.ProxySettings{Mode: library.ProxyDirect, URL: "ftp://127.0.0.1:21"}),
		library.InvalidArgument, library.ErrorDetails{Field: "url"})
	badSecretErr := requireErrorDetails(t, lib.SetProxySettings(t.Context(), library.ProxySettings{Mode: library.ProxyManual, URL: "http://alice:superSecretPw@127.0.0.1:abc"}),
		library.InvalidArgument, library.ErrorDetails{Field: "url"})
	if strings.Contains(badSecretErr.Message, "superSecretPw") {
		t.Fatalf("invalid proxy url error leaked password: %q", badSecretErr.Message)
	}

	if err := lib.SetProxySettings(t.Context(), library.ProxySettings{
		Mode: library.ProxyManual,
		URL:  "  HTTP://alice:secret@127.0.0.1:7890/ ",
	}); err != nil {
		t.Fatalf("SetProxySettings manual: %v", err)
	}
	got, err = lib.ProxySettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != library.ProxyManual || got.URL != "http://alice:secret@127.0.0.1:7890" {
		t.Fatalf("manual ProxySettings = %+v", got)
	}

	if err := lib.SetProxySettings(t.Context(), library.ProxySettings{Mode: library.ProxyDirect, URL: got.URL}); err != nil {
		t.Fatalf("SetProxySettings direct: %v", err)
	}
	reopened := openTestLibraryAt(t, path)
	got, err = reopened.ProxySettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != library.ProxyDirect || got.URL != "http://alice:secret@127.0.0.1:7890" {
		t.Fatalf("reopened ProxySettings = %+v; want direct with kept manual URL", got)
	}
}

func TestSystemProxyRedactsPassword(t *testing.T) {
	platformtest.EmulateSystemProxy(t, "http://u:p@127.0.0.1:7890", "http://127.0.0.1:7890", "socks5://bob:pw@127.0.0.1:1080", nil)
	lib := openTestLibrary(t)
	got, err := lib.SystemProxy(t.Context())
	if err != nil {
		t.Fatalf("SystemProxy: %v", err)
	}
	if got.HTTP != "http://u:xxxxx@127.0.0.1:7890" || got.HTTPS != "http://127.0.0.1:7890" || got.SOCKS != "socks5://bob:xxxxx@127.0.0.1:1080" {
		t.Fatalf("SystemProxy = %+v", got)
	}
}

func TestProxyRoutesHTTPReopenAndDirect(t *testing.T) {
	archive := zipArchive(t, map[string]string{
		"demo/SKILL.md": "---\nname: demo\ndescription: proxied skill\n---\n# demo\n",
	})
	var mu sync.Mutex
	var hits []string
	var lastAuth string
	serveProxy := func(label string) *httptest.Server {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			hits = append(hits, label+":"+r.URL.String())
			lastAuth = r.Header.Get("Proxy-Authorization")
			mu.Unlock()
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(archive)
		}))
		t.Cleanup(s.Close)
		return s
	}

	sysProxy := serveProxy("sys")
	manualProxy := serveProxy("manual")

	platformtest.EmulateSystemProxy(t, sysProxy.URL, sysProxy.URL, "", nil)

	path := filepath.Join(t.TempDir(), "library.db")
	home := t.TempDir()
	lib, err := library.Open(path, library.WithHomeDirectory(home))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	preview, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: "http://skills.proxy-test.example/demo.zip"})
	if err != nil {
		_ = lib.Close()
		t.Fatalf("PreviewFetch via system proxy: %v", err)
	}
	if len(preview.Candidates) != 1 || preview.Candidates[0].Name != "demo" {
		_ = lib.Close()
		t.Fatalf("candidates = %+v", preview.Candidates)
	}

	u, _ := url.Parse(manualProxy.URL)
	u.User = url.UserPassword("alice", "pass123")
	if err := lib.SetProxySettings(t.Context(), library.ProxySettings{Mode: library.ProxyManual, URL: u.String()}); err != nil {
		_ = lib.Close()
		t.Fatalf("SetProxySettings manual: %v", err)
	}
	if _, err := lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: "http://skills.proxy-test.example/demo2.zip"}); err != nil {
		_ = lib.Close()
		t.Fatalf("PreviewFetch via manual proxy: %v", err)
	}
	if err := lib.Close(); err != nil {
		t.Fatal(err)
	}

	// 重开数据库后仍按落库的手动代理联网
	reopened, err := library.Open(path, library.WithHomeDirectory(home))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if _, err := reopened.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: "http://skills.proxy-test.example/demo3.zip"}); err != nil {
		t.Fatalf("PreviewFetch after reopen: %v", err)
	}

	// 切到不用代理：不再走模拟的系统代理或手动代理
	if err := reopened.SetProxySettings(t.Context(), library.ProxySettings{Mode: library.ProxyDirect, URL: u.String()}); err != nil {
		t.Fatalf("SetProxySettings direct: %v", err)
	}
	directServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(archive)
	}))
	t.Cleanup(directServer.Close)
	if _, err := reopened.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: directServer.URL + "/direct.zip"}); err != nil {
		t.Fatalf("PreviewFetch direct: %v", err)
	}

	mu.Lock()
	gotHits := append([]string(nil), hits...)
	gotAuth := lastAuth
	mu.Unlock()
	wantHits := []string{
		"sys:http://skills.proxy-test.example/demo.zip",
		"manual:http://skills.proxy-test.example/demo2.zip",
		"manual:http://skills.proxy-test.example/demo3.zip",
	}
	if len(gotHits) != len(wantHits) {
		t.Fatalf("proxy hits = %v; want %v", gotHits, wantHits)
	}
	for i := range wantHits {
		if gotHits[i] != wantHits[i] {
			t.Fatalf("proxy hits[%d] = %q; want %q", i, gotHits[i], wantHits[i])
		}
	}
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("alice:pass123"))
	if gotAuth != wantAuth {
		t.Fatalf("Proxy-Authorization = %q; want %q", gotAuth, wantAuth)
	}
}

func TestSetProxySettingsDoesNotBlockOnHangingNetworkRequest(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	release := make(chan struct{})
	stubHTTP(t, func(r *http.Request) (*http.Response, error) {
		select {
		case <-started:
		default:
			close(started)
		}
		select {
		case <-release:
			return nil, r.Context().Err()
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	})
	lib := openTestLibrary(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: "https://hanging.example/skills.zip"})
	}()
	<-started

	setDone := make(chan error, 1)
	go func() {
		setDone <- lib.SetProxySettings(t.Context(), library.ProxySettings{Mode: library.ProxyManual, URL: "http://127.0.0.1:7890"})
	}()
	select {
	case err := <-setDone:
		if err != nil {
			t.Fatalf("SetProxySettings during hanging request: %v", err)
		}
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("SetProxySettings blocked while a network request held the shared lock")
	}
	close(release)
	<-done
}

func TestProxyPropagatesToGitAndGHSubprocesses(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stub script")
	}
	bin := t.TempDir()
	outFile := filepath.Join(t.TempDir(), "env.json")
	gitScript := `#!/bin/sh
args=""
for a in "$@"; do
  args="$args|$a"
done
cat > "$CAPTURE_FILE" <<EOF
{"http":"$HTTP_PROXY","http_l":"$http_proxy","https":"$HTTPS_PROXY","https_l":"$https_proxy","all":"$ALL_PROXY","no":"$NO_PROXY","args":"$args"}
EOF
exit 1
`
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(gitScript), 0o755); err != nil {
		t.Fatal(err)
	}
	ghFile := filepath.Join(t.TempDir(), "gh-env.json")
	ghScript := `#!/bin/sh
cat > "$GH_CAPTURE_FILE" <<EOF
{"https":"$HTTPS_PROXY","https_l":"$https_proxy","all":"$ALL_PROXY"}
EOF
exit 1
`
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(ghScript), 0o755); err != nil {
		t.Fatal(err)
	}

	platformtest.EmulateSystemProxy(t, "http://127.0.0.1:7890", "http://127.0.0.1:7891", "socks5://127.0.0.1:1080", []string{"*.local", "192.168.*", "<local>"})
	lib := openTestLibrary(t)
	setTestEnv(t, "PATH", bin+string(os.PathListSeparator)+testEnv(t, "PATH"))
	setTestEnv(t, "CAPTURE_FILE", outFile)
	setTestEnv(t, "GH_CAPTURE_FILE", ghFile)

	readGitCaptured := func(remote string) map[string]string {
		t.Helper()
		_ = os.Remove(outFile)
		_, _ = lib.ListSourceRefs(t.Context(), remote)
		data, err := os.ReadFile(outFile)
		if err != nil {
			t.Fatalf("read captured git env: %v", err)
		}
		var got map[string]string
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("unmarshal captured git env %q: %v", data, err)
		}
		return got
	}

	got := readGitCaptured("https://git.example.com/org/repo.git")
	if got["http"] != "http://127.0.0.1:7890" || got["https"] != "http://127.0.0.1:7891" || got["all"] != "socks5h://127.0.0.1:1080" || !strings.Contains(got["no"], ".local") {
		t.Fatalf("system mode git env = %v", got)
	}
	if !strings.Contains(got["args"], "|-c|http.proxy=http://127.0.0.1:7891|") || !strings.Contains(got["args"], "|-c|http.https://git.example.com/org/repo.git.proxy=http://127.0.0.1:7891|") {
		t.Fatalf("system mode git args missing -c http.proxy override: %q", got["args"])
	}

	// 被系统 bypass 规则命中时（192.168.*），用 -c http.proxy= 覆盖可能存在的 ~/.gitconfig 代理
	got = readGitCaptured("http://192.168.1.50/org/repo.git")
	if !strings.Contains(got["args"], "|-c|http.proxy=|") || !strings.Contains(got["args"], "|-c|http.http://192.168.1.50/org/repo.git.proxy=|") {
		t.Fatalf("bypassed git args missing empty -c http.proxy= override: %q", got["args"])
	}

	if err := lib.SetProxySettings(t.Context(), library.ProxySettings{Mode: library.ProxyManual, URL: "socks5://u:p@127.0.0.1:1081"}); err != nil {
		t.Fatal(err)
	}
	got = readGitCaptured("https://git.example.com/org/repo.git")
	for _, k := range []string{"http", "http_l", "https", "https_l", "all"} {
		if got[k] != "socks5h://u:p@127.0.0.1:1081" {
			t.Fatalf("manual mode git env[%s] = %q", k, got[k])
		}
	}
	if !strings.Contains(got["args"], "|-c|http.proxy=socks5h://u:p@127.0.0.1:1081|") {
		t.Fatalf("manual mode git args missing -c http.proxy: %q", got["args"])
	}

	// gh 回退路径也拿到同一组代理环境变量（传完整 commit SHA 跳过 Locate 里的 git ls-remote，直接进入 GitHub API -> 404 -> gh 回退）
	stubHTTP(t, func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusNotFound, Header: http.Header{}, Body: http.NoBody}, nil
	})
	_ = os.Remove(ghFile)
	_, _ = lib.PreviewFetch(t.Context(), library.PreviewFetchRequest{Input: "owner/repo", Ref: strings.Repeat("a", 40)})
	if data, err := os.ReadFile(ghFile); err != nil {
		t.Fatalf("expected gh fallback to run and capture env: %v", err)
	} else {
		var ghGot map[string]string
		if err := json.Unmarshal(data, &ghGot); err != nil {
			t.Fatalf("unmarshal gh env %q: %v", data, err)
		}
		if ghGot["https"] != "socks5h://u:p@127.0.0.1:1081" || ghGot["https_l"] != "socks5h://u:p@127.0.0.1:1081" {
			t.Fatalf("manual mode gh env = %v", ghGot)
		}
	}

	// 不用代理：不传 -c http.proxy，环境变量保持调用方原样
	if err := lib.SetProxySettings(t.Context(), library.ProxySettings{Mode: library.ProxyDirect}); err != nil {
		t.Fatal(err)
	}
	setTestEnv(t, "HTTPS_PROXY", "http://from-caller:8080")
	got = readGitCaptured("https://git.example.com/org/repo.git")
	if got["https"] != "http://from-caller:8080" || got["all"] != "" {
		t.Fatalf("direct mode git env = %v; want caller env untouched", got)
	}
	if strings.Contains(got["args"], "http.proxy=") {
		t.Fatalf("direct mode git args should not override http.proxy: %q", got["args"])
	}
}

func TestProxySettingsStayLocalAcrossSync(t *testing.T) {
	t.Parallel()
	s3Server := newS3Stub(t)
	syncCfg := s3Server.settings

	machineA := openTestLibrary(t)
	setSyncSettings(t, machineA, syncCfg)
	if err := machineA.SetProxySettings(t.Context(), library.ProxySettings{Mode: library.ProxyManual, URL: "http://alice:proxySecret88@proxy-a:7890"}); err != nil {
		t.Fatal(err)
	}
	if _, err := machineA.SyncPush(t.Context()); err != nil {
		t.Fatalf("SyncPush: %v", err)
	}
	s3Server.mu.Lock()
	for key, obj := range s3Server.objects {
		if bytes.Contains(obj.data, []byte("proxySecret88")) || bytes.Contains(obj.data, []byte("proxy-a")) {
			s3Server.mu.Unlock()
			t.Fatalf("pushed S3 backup %s leaked proxy settings/password", key)
		}
	}
	s3Server.mu.Unlock()

	machineB := openTestLibrary(t)
	setSyncSettings(t, machineB, syncCfg)
	if err := machineB.SetProxySettings(t.Context(), library.ProxySettings{Mode: library.ProxyManual, URL: "socks5://proxy-b:1080"}); err != nil {
		t.Fatal(err)
	}
	backups, err := machineB.ListSyncBackups(t.Context())
	if err != nil || len(backups) != 1 {
		t.Fatalf("ListSyncBackups = %+v, %v", backups, err)
	}
	if err := machineB.SyncPull(t.Context(), backups[0].ID); err != nil {
		t.Fatalf("SyncPull: %v", err)
	}
	gotB, err := machineB.ProxySettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if gotB.Mode != library.ProxyManual || gotB.URL != "socks5://proxy-b:1080" {
		t.Fatalf("machine B ProxySettings after pull = %+v; want machine B's own proxy kept", gotB)
	}

	machineC := openTestLibrary(t)
	setSyncSettings(t, machineC, syncCfg)
	if err := machineC.SyncPull(t.Context(), backups[0].ID); err != nil {
		t.Fatalf("SyncPull on fresh machine C: %v", err)
	}
	gotC, err := machineC.ProxySettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if gotC.Mode != library.ProxySystem || gotC.URL != "" {
		t.Fatalf("machine C ProxySettings after pull = %+v; want default system proxy (machine A's proxy stripped from push)", gotC)
	}
}
