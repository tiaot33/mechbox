package release

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/wailsapp/wails/v3/pkg/updater"

	"github.com/tiaot33/mechbox/internal/library"
)

type stubUpdaterHost struct{}

func (stubUpdaterHost) Emit(string, ...any) bool                              { return true }
func (stubUpdaterHost) OnEvent(string, func(any)) func()                      { return func() {} }
func (stubUpdaterHost) OpenWindow(updater.WindowOptions) updater.WindowHandle { return nil }
func (stubUpdaterHost) Quit()                                                 {}

// fakeGitHub 是一个只有最新版本附带的 release.json、安装包与 SHA256SUMS.txt 的 GitHub。
type fakeGitHub struct {
	server    *httptest.Server
	tag       string
	assets    map[string][]byte
	checksums map[string][]byte // 写进 SHA256SUMS.txt 的内容，nil 时按 assets 算
	status    int
}

func newFakeGitHub(t *testing.T, tag string, assets map[string][]byte) *fakeGitHub {
	t.Helper()
	g := &fakeGitHub{tag: tag, assets: assets}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /releases/latest/download/"+releaseManifest, func(w http.ResponseWriter, r *http.Request) {
		if g.status != 0 {
			w.WriteHeader(g.status)
			return
		}
		type asset struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
			Size int64  `json:"size"`
		}
		list := []asset{{Name: checksumsAsset, URL: g.server.URL + "/download/" + checksumsAsset}}
		for name, data := range g.assets {
			list = append(list, asset{Name: name, URL: g.server.URL + "/download/" + name, Size: int64(len(data))})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name":     g.tag,
			"body":         "## 安装\r\n\r\n手动安装的说明\r\n\r\n" + generatedNotes + "\r\n* 更新说明",
			"html_url":     g.server.URL + "/releases/tag/" + g.tag,
			"published_at": "2026-11-01T00:00:00Z",
			"assets":       list,
		})
	})
	serveAsset := func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if name == checksumsAsset {
			for file, data := range g.assets {
				sum := sha256.Sum256(data)
				if fake, ok := g.checksums[file]; ok {
					sum = sha256.Sum256(fake)
				}
				fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), file)
			}
			return
		}
		data, ok := g.assets[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}
	mux.HandleFunc("GET /download/{name}", serveAsset)
	// 安装包和校验值不按 release.json 里写的地址读，读的是这个版本在 GitHub 上的固定地址。
	mux.HandleFunc("GET /releases/download/{tag}/{name}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("tag") != g.tag {
			http.NotFound(w, r)
			return
		}
		serveAsset(w, r)
	})
	g.server = httptest.NewServer(mux)
	t.Cleanup(g.server.Close)
	return g
}

func (g *fakeGitHub) releases(t *testing.T, current string, target installTarget) *Releases {
	return newReleases(g.server.URL+"/releases/latest/download/"+releaseManifest, current, t.TempDir(), updater.New(stubUpdaterHost{}), func() installTarget { return target }, nil, nil)
}

const testAsset = "Mechbox-1.2.0-macos-universal.zip"

func errorCode(err error) library.ErrorCode {
	if err == nil {
		return ""
	}
	return library.ErrorOf(err).Code
}

func TestCheckReleaseFindsNewerVersion(t *testing.T) {
	g := newFakeGitHub(t, "v1.2.0", map[string][]byte{testAsset: []byte("new app"), "Mechbox-1.2.0-windows-amd64-setup.exe": []byte("setup")})
	r := g.releases(t, "1.1.0", installTarget{asset: "-macos-universal.zip"})
	latest, err := r.Check(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if latest == nil || latest.Version != "1.2.0" || latest.Notes != generatedNotes+"\r\n* 更新说明" || latest.Manual != "" || !strings.HasSuffix(latest.PageURL, "/releases/tag/v1.2.0") {
		t.Fatalf("latest = %+v", latest)
	}
	if latest, _ := r.Status(); latest == nil || latest.Version != "1.2.0" {
		t.Fatalf("status latest = %+v", latest)
	}
}

func TestCheckReleaseUpToDate(t *testing.T) {
	g := newFakeGitHub(t, "v1.2.0", map[string][]byte{testAsset: []byte("new app")})
	for _, current := range []string{"1.2.0", "1.3.0"} {
		r := g.releases(t, current, installTarget{asset: "-macos-universal.zip"})
		latest, err := r.Check(t.Context())
		if err != nil || latest != nil {
			t.Fatalf("current %s: latest = %+v, err = %v", current, latest, err)
		}
		if latest, _ := r.Status(); latest != nil {
			t.Fatalf("current %s: status latest = %+v", current, latest)
		}
	}
}

func TestCheckReleaseReportsUnreachableGitHub(t *testing.T) {
	g := newFakeGitHub(t, "v1.2.0", nil)
	g.status = http.StatusForbidden
	r := g.releases(t, "1.1.0", installTarget{asset: "-macos-universal.zip"})
	if _, err := r.Check(t.Context()); errorCode(err) != library.SourceUnreachable || !strings.Contains(err.Error(), "403") {
		t.Fatalf("err = %v", err)
	}
	g.server.Close()
	if _, err := r.Check(t.Context()); errorCode(err) != library.SourceUnreachable {
		t.Fatalf("closed server: err = %v", err)
	}
}

func TestCheckReleaseWithoutInstallableAsset(t *testing.T) {
	g := newFakeGitHub(t, "v1.2.0", map[string][]byte{"Mechbox-1.2.0-macos-universal.dmg": []byte("dmg")})
	cases := []struct {
		target installTarget
		want   ManualReason
	}{
		{installTarget{asset: "-macos-universal.zip"}, ManualAsset},
		{installTarget{manual: ManualPackage}, ManualPackage},
		{installTarget{manual: ManualLocation}, ManualLocation},
	}
	for _, c := range cases {
		r := g.releases(t, "1.1.0", c.target)
		latest, err := r.Check(t.Context())
		if err != nil || latest == nil || latest.Manual != c.want {
			t.Fatalf("%+v: latest = %+v, err = %v", c.target, latest, err)
		}
		if err := r.Install(t.Context(), "1.2.0", func() {}); errorCode(err) != library.FailedPrecondition {
			t.Fatalf("%+v: install err = %v", c.target, err)
		}
	}
}

func TestInstallReleaseDownloadsVerifiesAndApplies(t *testing.T) {
	g := newFakeGitHub(t, "v1.2.0", map[string][]byte{"Mechbox-1.2.0-linux-amd64.AppImage": []byte("new image")})
	r := g.releases(t, "1.1.0", installTarget{asset: "-linux-amd64.AppImage"})
	var staged []byte
	quit := false
	r.apply = func(_ context.Context, _ *updater.Updater, path, _ string, q func()) error {
		var err error
		staged, err = os.ReadFile(path)
		q()
		return errors.Join(err, os.RemoveAll(filepath.Dir(path)))
	}
	if err := r.Install(t.Context(), "1.2.0", func() {}); errorCode(err) != library.FailedPrecondition {
		t.Fatalf("install before check: err = %v", err)
	}
	if _, err := r.Check(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := r.Install(t.Context(), "1.2.0", func() { quit = true }); err != nil {
		t.Fatal(err)
	}
	if string(staged) != "new image" || !quit {
		t.Fatalf("staged = %q, quit = %v", staged, quit)
	}
	if pending, err := os.ReadFile(filepath.Join(r.stateDir, pendingFile)); err != nil || !strings.Contains(string(pending), `"version":"1.2.0"`) {
		t.Fatalf("pending = %s, err = %v", pending, err)
	}
	if err := r.Install(t.Context(), "1.2.0", func() {}); errorCode(err) != library.Busy {
		t.Fatalf("second install: err = %v", err)
	}
}

func TestInstallReleaseRejectsChecksumMismatch(t *testing.T) {
	g := newFakeGitHub(t, "v1.2.0", map[string][]byte{"Mechbox-1.2.0-linux-amd64.AppImage": []byte("tampered")})
	g.checksums = map[string][]byte{"Mechbox-1.2.0-linux-amd64.AppImage": []byte("published")}
	r := g.releases(t, "1.1.0", installTarget{asset: "-linux-amd64.AppImage"})
	applied := false
	r.apply = func(context.Context, *updater.Updater, string, string, func()) error {
		applied = true
		return nil
	}
	if _, err := r.Check(t.Context()); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := r.Install(t.Context(), "1.2.0", func() {}); errorCode(err) != library.SourceInvalid {
			t.Fatalf("err = %v", err)
		}
	}
	if applied {
		t.Fatal("applied an installer that failed verification")
	}
}

func TestReleasesDisabledWithoutFeed(t *testing.T) {
	r := newReleases("", "1.0.0", t.TempDir(), updater.New(stubUpdaterHost{}), func() installTarget { return installTarget{} }, nil, nil)
	if r != nil {
		t.Fatalf("releases = %+v; want nil when there is no feed", r)
	}
	if _, err := r.Check(t.Context()); errorCode(err) != library.FailedPrecondition {
		t.Fatalf("err = %v", err)
	}
}

func TestNewerVersion(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"1.2.0", "1.1.9", true},
		{"1.10.0", "1.9.0", true},
		{"2.0.0", "1.99.99", true},
		{"1.2.0", "1.2.0", false},
		{"1.1.0", "1.2.0", false},
		{"1.2.0", "1.2.0-beta.1", true},
		{"1.2.0-beta.2", "1.2.0-beta.1", true},
		{"1.2.0-beta.1", "1.2.0", false},
		{"v1.2.0", "1.1.0", true},
		{"latest", "1.1.0", false},
		{"1.2.0", "", false},
	}
	for _, c := range cases {
		if got := newerVersion(c.latest, c.current); got != c.want {
			t.Errorf("newerVersion(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}

func TestParseChecksum(t *testing.T) {
	sum := sha256.Sum256([]byte("x"))
	text := "0000000000000000000000000000000000000000000000000000000000000000  other.zip\n" + hex.EncodeToString(sum[:]) + " *Mechbox.zip\n"
	got, err := parseChecksum(strings.NewReader(text), "Mechbox.zip")
	if err != nil || hex.EncodeToString(got) != hex.EncodeToString(sum[:]) {
		t.Fatalf("got %x, err %v", got, err)
	}
	if got, err := parseChecksum(strings.NewReader(text), "missing.zip"); got != nil || err != nil {
		t.Fatalf("missing: got %x, err %v", got, err)
	}
	if _, err := parseChecksum(strings.NewReader("zz  Mechbox.zip\n"), "Mechbox.zip"); errorCode(err) != library.SourceInvalid {
		t.Fatalf("malformed: err %v", err)
	}
}

func TestReplaceFileSwapsInPlaceAndMakesExecutable(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "Mechbox.AppImage")
	staged := filepath.Join(t.TempDir(), "new.AppImage")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staged, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := replaceFile(staged, target); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "new" {
		t.Fatalf("target = %q, err = %v", data, err)
	}
	if info, _ := os.Stat(target); info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v", info.Mode())
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("left files behind: %v", entries)
	}
}

func TestInstallerScriptRunsSetupSilentlyThenRelaunches(t *testing.T) {
	script := installerScript(42, `C:\Temp\wails-update-1\Mechbox-1.2.0-windows-amd64-setup.exe`, `C:\Temp\wails-update-1`, `C:\Program Files\tiaot33\Mechbox`, `C:\Program Files\tiaot33\Mechbox\mechbox.exe`, `C:\Users\a\mechbox\update.log`)
	for _, want := range []string{
		"Wait-Process -Id 42",
		`Start-Process -FilePath 'C:\Temp\wails-update-1\Mechbox-1.2.0-windows-amd64-setup.exe' -ArgumentList '/S /D=C:\Program Files\tiaot33\Mechbox' -Verb RunAs -Wait -PassThru`,
		`$($p.ExitCode)`,
		`Set-Content -LiteralPath 'C:\Users\a\mechbox\update.log' -Value $result`,
		`Remove-Item -LiteralPath 'C:\Temp\wails-update-1' -Recurse -Force`,
		`Start-Process -FilePath 'C:\Program Files\tiaot33\Mechbox\mechbox.exe'`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script lacks %q:\n%s", want, script)
		}
	}
	if quoted := installerScript(1, `C:\a'b\setup.exe`, `C:\a'b`, `C:\x`, `C:\x\m.exe`, `C:\x\update.log`); !strings.Contains(quoted, `'C:\a''b\setup.exe'`) {
		t.Errorf("single quote not escaped:\n%s", quoted)
	}
	raw, err := base64.StdEncoding.DecodeString(encodePowerShell("Start-Process 'ä'"))
	if err != nil || len(raw)%2 != 0 {
		t.Fatalf("raw = %v, err = %v", raw, err)
	}
	units := make([]uint16, len(raw)/2)
	for i := range units {
		units[i] = uint16(raw[2*i]) | uint16(raw[2*i+1])<<8
	}
	if got := string(utf16.Decode(units)); got != "Start-Process 'ä'" {
		t.Fatalf("decoded = %q", got)
	}
}

func TestPendingInstallReportsVersionThatDidNotTakeEffect(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "update.log")
	if err := os.WriteFile(log, []byte("helper start\nparent did not exit within timeout — aborting swap\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	write := func(version string) {
		t.Helper()
		data, _ := json.Marshal(pendingInstall{Version: version, Log: log})
		if err := os.WriteFile(filepath.Join(dir, pendingFile), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("1.2.0")
	failed := takePending(dir, "1.1.0")
	if failed == nil || failed.Version != "1.2.0" || failed.Reason != "parent did not exit within timeout — aborting swap" {
		t.Fatalf("failed = %+v", failed)
	}
	if _, err := os.Stat(filepath.Join(dir, pendingFile)); !os.IsNotExist(err) {
		t.Fatalf("pending file kept: %v", err)
	}
	write("1.2.0")
	if failed := takePending(dir, "1.2.0"); failed != nil {
		t.Fatalf("installed version reported as failed: %+v", failed)
	}
	if failed := takePending(dir, "1.1.0"); failed != nil {
		t.Fatalf("no pending file: %+v", failed)
	}
}

func TestInstallReleaseForgetsPendingWhenApplyFails(t *testing.T) {
	g := newFakeGitHub(t, "v1.2.0", map[string][]byte{"Mechbox-1.2.0-linux-amd64.AppImage": []byte("new image")})
	r := g.releases(t, "1.1.0", installTarget{asset: "-linux-amd64.AppImage"})
	r.apply = func(_ context.Context, _ *updater.Updater, path, _ string, _ func()) error {
		return errors.Join(errors.New("cannot replace"), os.RemoveAll(filepath.Dir(path)))
	}
	if _, err := r.Check(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := r.Install(t.Context(), "1.2.0", func() {}); errorCode(err) != library.Internal {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(r.stateDir, pendingFile)); !os.IsNotExist(err) {
		t.Fatalf("pending file kept after failed apply: %v", err)
	}
	if _, failed := r.Status(); failed != nil {
		t.Fatalf("status failed = %+v", failed)
	}
}

func TestInstallReleaseCanBeCanceledDuringDownload(t *testing.T) {
	g := newFakeGitHub(t, "v1.2.0", map[string][]byte{"Mechbox-1.2.0-linux-amd64.AppImage": []byte("new image")})
	r := g.releases(t, "1.1.0", installTarget{asset: "-linux-amd64.AppImage"})
	if _, err := r.Check(t.Context()); err != nil {
		t.Fatal(err)
	}
	before := partialDownloads()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := r.Install(ctx, "1.2.0", func() {}); errorCode(err) != library.Canceled {
		t.Fatalf("err = %v", err)
	}
	for dir := range partialDownloads() {
		if !before[dir] {
			t.Fatalf("canceled download left %s behind", dir)
		}
	}
	r.apply = func(_ context.Context, _ *updater.Updater, path, _ string, _ func()) error {
		return os.RemoveAll(filepath.Dir(path))
	}
	if err := r.Install(t.Context(), "1.2.0", func() {}); err != nil {
		t.Fatalf("retry after cancel: %v", err)
	}
}

func TestInstallReleaseOnlyInstallsTheVersionShown(t *testing.T) {
	g := newFakeGitHub(t, "v1.2.0", map[string][]byte{"Mechbox-1.2.0-linux-amd64.AppImage": []byte("new image")})
	r := g.releases(t, "1.1.0", installTarget{asset: "-linux-amd64.AppImage"})
	applied := false
	r.apply = func(context.Context, *updater.Updater, string, string, func()) error {
		applied = true
		return nil
	}
	if _, err := r.Check(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := r.Install(t.Context(), "1.1.5", func() {}); errorCode(err) != library.FailedPrecondition || !strings.Contains(err.Error(), "1.2.0") {
		t.Fatalf("install an older dialog's version: err = %v", err)
	}
	if applied {
		t.Fatal("installed a version the user didn't confirm")
	}
	if err := r.Install(t.Context(), "1.2.0", func() {}); err != nil || !applied {
		t.Fatalf("install the shown version: err = %v, applied = %v", err, applied)
	}
}

func TestReleaseDue(t *testing.T) {
	start := time.Date(2026, 11, 5, 9, 0, 0, 50_000_000, time.UTC)
	cases := []struct {
		name    string
		now     time.Time
		last    time.Time
		checked bool
		want    bool
	}{
		{"never checked", start, time.Time{}, false, true},
		{"failed, within the hour", start.Add(59 * time.Minute), start, false, false},
		{"failed, an hour later", start.Add(time.Hour), start, false, true},
		{"checked, within the day", start.Add(23 * time.Hour), start, true, false},
		{"checked, a day later", start.Add(24 * time.Hour), start, true, true},
	}
	for _, c := range cases {
		if got := releaseDue(c.now, c.last, c.checked); got != c.want {
			t.Errorf("%s: releaseDue = %v, want %v", c.name, got, c.want)
		}
	}
}

// 后台检查新版本：一开始就检查一次并把结果交给 checked;取消后停下。
func TestWatchChecksAtOnceAndStopsWhenCancelled(t *testing.T) {
	g := newFakeGitHub(t, "v1.2.0", map[string][]byte{testAsset: []byte("new app")})
	r := g.releases(t, "1.1.0", installTarget{asset: "-macos-universal.zip"})
	ctx, cancel := context.WithCancel(t.Context())
	checked := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.Watch(ctx, func(err error) { checked <- err })
	}()
	select {
	case err := <-checked:
		if err != nil {
			t.Fatalf("first background check = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Watch did not check at once")
	}
	if latest, _ := r.Status(); latest == nil || latest.Version != "1.2.0" {
		t.Fatalf("latest after background check = %+v", latest)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Watch kept running after cancel")
	}
}

func TestReleaseCheckAndDownloadUseLibraryProxy(t *testing.T) {
	const assetName = "Mechbox-2.0.0-linux-amd64.AppImage"
	assetData := []byte("proxied release installer")
	sum := sha256.Sum256(assetData)
	manifest, _ := json.Marshal(githubRelease{
		TagName: "v2.0.0",
		Body:    "## What's Changed\n- proxy support",
		HTMLURL: "https://github.com/tiaot33/mechbox/releases/tag/v2.0.0",
		Assets: []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
			Size int64  `json:"size"`
		}{
			{Name: assetName, URL: "http://releases.proxy-test.example/download/" + assetName, Size: int64(len(assetData))},
			{Name: checksumsAsset, URL: "http://releases.proxy-test.example/download/" + checksumsAsset},
		},
	})
	var proxiedPaths []string
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxiedPaths = append(proxiedPaths, r.URL.Path)
		switch r.URL.Path {
		case "/releases/latest/download/" + releaseManifest:
			_, _ = w.Write(manifest)
		case "/releases/download/v2.0.0/" + checksumsAsset:
			fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), assetName)
		case "/releases/download/v2.0.0/" + assetName:
			_, _ = w.Write(assetData)
		default:
			http.NotFound(w, r)
		}
	}))
	defer proxyServer.Close()

	lib, err := library.Open(filepath.Join(t.TempDir(), "library.db"), library.WithHomeDirectory(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	if err := lib.SetProxySettings(t.Context(), library.ProxySettings{Mode: library.ProxyManual, URL: proxyServer.URL}); err != nil {
		t.Fatal(err)
	}

	r := newReleases("http://releases.proxy-test.example/releases/latest/download/"+releaseManifest, "1.0.0", t.TempDir(), updater.New(stubUpdaterHost{}), func() installTarget { return installTarget{asset: "-linux-amd64.AppImage"} }, library.ProxyFunc(lib), nil)
	applied := false
	r.apply = func(context.Context, *updater.Updater, string, string, func()) error {
		applied = true
		return nil
	}

	got, err := r.Check(t.Context())
	if err != nil || got == nil || got.Version != "2.0.0" {
		t.Fatalf("check via proxy = %+v, %v", got, err)
	}
	if err := r.Install(t.Context(), "2.0.0", func() {}); err != nil || !applied {
		t.Fatalf("install via proxy = %v, applied = %v", err, applied)
	}
	if len(proxiedPaths) != 3 {
		t.Fatalf("proxied requests = %v; want manifest, checksums, and asset", proxiedPaths)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// 打开 GitHub 镜像加速时，新版本信息与安装包经镜像，校验值直连这个版本在 GitHub 上的固定地址：
// 镜像改了安装包、在 release.json 里换上自己的校验值地址，也装不上。
func TestReleaseMirrorCannotReplaceInstaller(t *testing.T) {
	const assetName = "Mechbox-1.2.0-linux-amd64.AppImage"
	for _, honest := range []bool{true, false} {
		t.Run(fmt.Sprintf("honest=%v", honest), func(t *testing.T) {
			g := newFakeGitHub(t, "v1.2.0", map[string][]byte{assetName: []byte("genuine image")})
			forged := []byte("forged image")
			forgedSum := sha256.Sum256(forged)
			var mirrored []string
			mirror := func(next http.RoundTripper) http.RoundTripper {
				return roundTripFunc(func(r *http.Request) (*http.Response, error) {
					mirrored = append(mirrored, r.URL.Path)
					if honest {
						return next.RoundTrip(r)
					}
					recorder := httptest.NewRecorder()
					switch r.URL.Path {
					case "/releases/latest/download/" + releaseManifest:
						_ = json.NewEncoder(recorder).Encode(map[string]any{
							"tag_name": "v1.2.0",
							"html_url": "https://phishing.example/mechbox",
							"assets": []map[string]any{
								{"name": assetName, "browser_download_url": g.server.URL + "/forged/" + assetName, "size": len(forged)},
								{"name": checksumsAsset, "browser_download_url": g.server.URL + "/forged/" + checksumsAsset},
							},
						})
					case "/releases/download/v1.2.0/" + assetName, "/forged/" + assetName:
						_, _ = recorder.Write(forged)
					case "/forged/" + checksumsAsset:
						fmt.Fprintf(recorder, "%s  %s\n", hex.EncodeToString(forgedSum[:]), assetName)
					default:
						recorder.WriteHeader(http.StatusNotFound)
					}
					return recorder.Result(), nil
				})
			}
			r := newReleases(g.server.URL+"/releases/latest/download/"+releaseManifest, "1.1.0", t.TempDir(), updater.New(stubUpdaterHost{}), func() installTarget { return installTarget{asset: "-linux-amd64.AppImage"} }, nil, mirror)
			var staged []byte
			r.apply = func(_ context.Context, _ *updater.Updater, path, _ string, _ func()) error {
				var err error
				staged, err = os.ReadFile(path)
				return errors.Join(err, os.RemoveAll(filepath.Dir(path)))
			}
			latest, err := r.Check(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if latest.PageURL != g.server.URL+"/releases/tag/v1.2.0" {
				t.Fatalf("page URL = %q; want the release page built from the tag", latest.PageURL)
			}
			err = r.Install(t.Context(), "1.2.0", func() {})
			if honest && (err != nil || string(staged) != "genuine image") {
				t.Fatalf("install through honest mirror = %v, staged %q", err, staged)
			}
			if !honest && (errorCode(err) != library.SourceInvalid || staged != nil) {
				t.Fatalf("install through forging mirror = %v, staged %q; want digest mismatch", err, staged)
			}
			want := []string{"/releases/latest/download/" + releaseManifest, "/releases/download/v1.2.0/" + assetName}
			if strings.Join(mirrored, " ") != strings.Join(want, " ") {
				t.Fatalf("through mirror %q; want manifest and installer only, checksums direct", mirrored)
			}
		})
	}
}

func TestCheckReleaseRejectsOversizedManifest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"tag_name":"v9.0.0","body":"%s"}`, strings.Repeat("x", maxManifestSize))
	}))
	defer server.Close()
	r := newReleases(server.URL+"/releases/latest/download/"+releaseManifest, "1.0.0", t.TempDir(), updater.New(stubUpdaterHost{}), func() installTarget { return installTarget{asset: "-linux-amd64.AppImage"} }, nil, nil)
	if _, err := r.Check(t.Context()); errorCode(err) != library.SourceInvalid {
		t.Fatalf("check with an oversized release.json = %v; want SourceInvalid", err)
	}
}
