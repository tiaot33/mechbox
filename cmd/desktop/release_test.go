package main

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
	"unicode/utf16"

	"github.com/wailsapp/wails/v3/pkg/updater"

	"github.com/tiaot33/mechbox/internal/library"
)

type stubUpdaterHost struct{}

func (stubUpdaterHost) Emit(string, ...any) bool                              { return true }
func (stubUpdaterHost) OnEvent(string, func(any)) func()                      { return func() {} }
func (stubUpdaterHost) OpenWindow(updater.WindowOptions) updater.WindowHandle { return nil }
func (stubUpdaterHost) Quit()                                                 {}

// fakeGitHub 是一个只有 releases/latest、安装包与 SHA256SUMS.txt 的 GitHub。
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
	mux.HandleFunc("GET /releases/latest", func(w http.ResponseWriter, r *http.Request) {
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
			"body":         "## 更新说明",
			"html_url":     g.server.URL + "/releases/tag/" + g.tag,
			"published_at": "2026-11-01T00:00:00Z",
			"assets":       list,
		})
	})
	mux.HandleFunc("GET /download/{name}", func(w http.ResponseWriter, r *http.Request) {
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
	})
	g.server = httptest.NewServer(mux)
	t.Cleanup(g.server.Close)
	return g
}

func (g *fakeGitHub) releases(current string, target installTarget) *releases {
	return newReleases(g.server.URL+"/releases/latest", current, updater.New(stubUpdaterHost{}), func() installTarget { return target })
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
	r := g.releases("1.1.0", installTarget{asset: "-macos-universal.zip"})
	latest, err := r.check(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if latest == nil || latest.Version != "1.2.0" || latest.Notes != "## 更新说明" || latest.Manual != "" || !strings.HasSuffix(latest.PageURL, "/releases/tag/v1.2.0") {
		t.Fatalf("latest = %+v", latest)
	}
	if status := r.status(); !status.Checks || status.Latest == nil || status.Latest.Version != "1.2.0" {
		t.Fatalf("status = %+v", status)
	}
}

func TestCheckReleaseUpToDate(t *testing.T) {
	g := newFakeGitHub(t, "v1.2.0", map[string][]byte{testAsset: []byte("new app")})
	for _, current := range []string{"1.2.0", "1.3.0"} {
		r := g.releases(current, installTarget{asset: "-macos-universal.zip"})
		latest, err := r.check(t.Context())
		if err != nil || latest != nil {
			t.Fatalf("current %s: latest = %+v, err = %v", current, latest, err)
		}
		if status := r.status(); !status.Checks || status.Latest != nil {
			t.Fatalf("current %s: status = %+v", current, status)
		}
	}
}

func TestCheckReleaseReportsUnreachableGitHub(t *testing.T) {
	g := newFakeGitHub(t, "v1.2.0", nil)
	g.status = http.StatusForbidden
	r := g.releases("1.1.0", installTarget{asset: "-macos-universal.zip"})
	if _, err := r.check(t.Context()); errorCode(err) != library.SourceUnreachable || !strings.Contains(err.Error(), "403") {
		t.Fatalf("err = %v", err)
	}
	g.server.Close()
	if _, err := r.check(t.Context()); errorCode(err) != library.SourceUnreachable {
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
		r := g.releases("1.1.0", c.target)
		latest, err := r.check(t.Context())
		if err != nil || latest == nil || latest.Manual != c.want {
			t.Fatalf("%+v: latest = %+v, err = %v", c.target, latest, err)
		}
		if err := r.install(t.Context(), func() {}); errorCode(err) != library.FailedPrecondition {
			t.Fatalf("%+v: install err = %v", c.target, err)
		}
	}
}

func TestInstallReleaseDownloadsVerifiesAndApplies(t *testing.T) {
	g := newFakeGitHub(t, "v1.2.0", map[string][]byte{"Mechbox-1.2.0-linux-amd64.AppImage": []byte("new image")})
	r := g.releases("1.1.0", installTarget{asset: "-linux-amd64.AppImage"})
	var staged []byte
	quit := false
	r.apply = func(_ context.Context, _ *updater.Updater, path string, q func()) error {
		var err error
		staged, err = os.ReadFile(path)
		q()
		return errors.Join(err, os.RemoveAll(filepath.Dir(path)))
	}
	if err := r.install(t.Context(), func() {}); errorCode(err) != library.FailedPrecondition {
		t.Fatalf("install before check: err = %v", err)
	}
	if _, err := r.check(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := r.install(t.Context(), func() { quit = true }); err != nil {
		t.Fatal(err)
	}
	if string(staged) != "new image" || !quit {
		t.Fatalf("staged = %q, quit = %v", staged, quit)
	}
	if err := r.install(t.Context(), func() {}); errorCode(err) != library.Busy {
		t.Fatalf("second install: err = %v", err)
	}
}

func TestInstallReleaseRejectsChecksumMismatch(t *testing.T) {
	g := newFakeGitHub(t, "v1.2.0", map[string][]byte{"Mechbox-1.2.0-linux-amd64.AppImage": []byte("tampered")})
	g.checksums = map[string][]byte{"Mechbox-1.2.0-linux-amd64.AppImage": []byte("published")}
	r := g.releases("1.1.0", installTarget{asset: "-linux-amd64.AppImage"})
	applied := false
	r.apply = func(context.Context, *updater.Updater, string, func()) error {
		applied = true
		return nil
	}
	if _, err := r.check(t.Context()); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := r.install(t.Context(), func() {}); errorCode(err) != library.SourceInvalid {
			t.Fatalf("err = %v", err)
		}
	}
	if applied {
		t.Fatal("applied an installer that failed verification")
	}
}

func TestReleasesDisabledWithoutFeed(t *testing.T) {
	r := newReleases("", "1.0.0", updater.New(stubUpdaterHost{}), func() installTarget { return installTarget{} })
	if status := r.status(); status.Checks {
		t.Fatalf("status = %+v", status)
	}
	if _, err := r.check(t.Context()); errorCode(err) != library.FailedPrecondition {
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
	script := installerScript(42, `C:\Temp\wails-update-1\Mechbox-1.2.0-windows-amd64-setup.exe`, `C:\Temp\wails-update-1`, `C:\Program Files\tiaot33\Mechbox`, `C:\Program Files\tiaot33\Mechbox\mechbox.exe`)
	for _, want := range []string{
		"Wait-Process -Id 42",
		`Start-Process -FilePath 'C:\Temp\wails-update-1\Mechbox-1.2.0-windows-amd64-setup.exe' -ArgumentList '/S /D=C:\Program Files\tiaot33\Mechbox' -Verb RunAs -Wait`,
		`Remove-Item -LiteralPath 'C:\Temp\wails-update-1' -Recurse -Force`,
		`Start-Process -FilePath 'C:\Program Files\tiaot33\Mechbox\mechbox.exe'`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script lacks %q:\n%s", want, script)
		}
	}
	if quoted := installerScript(1, `C:\a'b\setup.exe`, `C:\a'b`, `C:\x`, `C:\x\m.exe`); !strings.Contains(quoted, `'C:\a''b\setup.exe'`) {
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
