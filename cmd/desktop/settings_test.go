package main

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/tiaot33/mechbox/internal/library"
)

func TestSettingsDefaultWhenNothingStored(t *testing.T) {
	got, done := settingsFrom(nil)
	want := Settings{AutoCheck: true, CheckInterval: CheckDaily, StayInMenuBar: true, DockBadge: true}
	if !reflect.DeepEqual(got, want) || done {
		t.Fatalf("settingsFrom(nil) = %+v, %v; want defaults %+v and not opened", got, done, want)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	want := Settings{
		AutoCheck: false, CheckInterval: CheckWeekly, StayInMenuBar: false, DockBadge: false, Language: LanguageEn,
		AgentsShown: map[library.AgentCode]bool{library.AgentCodex: false, "custom-0a1b2c3d4e5f": true},
	}
	for _, opened := range []bool{false, true} {
		got, done := settingsFrom(want.appValues(opened))
		if !reflect.DeepEqual(got, want) || done != opened {
			t.Fatalf("settingsFrom(appValues(%v)) = %+v, %v; want %+v", opened, got, done, want)
		}
	}
	if v := defaultSettings().appValues(false)["language"]; v != "" {
		t.Fatalf("following the system language stores language %q; want nothing", v)
	}
}

func TestSettingsFillUnreadableValuesWithDefaults(t *testing.T) {
	got, _ := settingsFrom(map[string]string{"dock_badge": "false", "auto_check": "maybe", "check_interval": "sometimes", "language": "fr", "agents_shown": "{"})
	want := defaultSettings()
	want.DockBadge = false
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("settingsFrom = %+v; want %+v", got, want)
	}
}

func TestSaveSettingsRejectsUnknownValues(t *testing.T) {
	lib := startLibrary(t)
	service := startApp(t, lib)
	for _, settings := range []Settings{{CheckInterval: "sometimes"}, {CheckInterval: CheckDaily, Language: "fr"}} {
		if _, err := service.SaveSettings(t.Context(), settings); err == nil {
			t.Fatalf("SaveSettings(%+v) accepted an unknown value", settings)
		}
	}
	if values, err := lib.lib.AppSettings(t.Context()); err != nil || len(values) != 0 {
		t.Fatalf("app settings = %v, %v; want nothing written for rejected changes", values, err)
	}
}

func TestLanguageFollowsSettingThenSystem(t *testing.T) {
	for tag, want := range map[string]Language{
		"zh-Hans-CN": LanguageZh, "zh-Hant-TW": LanguageZh, "zh_CN.UTF-8": LanguageZh, "ZH": LanguageZh,
		"en-US": LanguageEn, "en_US.UTF-8": LanguageEn, "ja-JP": LanguageEn, "zhx": LanguageEn, "C": LanguageEn, "": LanguageEn,
	} {
		if got := languageOf(tag); got != want {
			t.Errorf("languageOf(%q) = %q; want %q", tag, got, want)
		}
	}
	for _, l := range []Language{LanguageZh, LanguageEn} {
		if got := l.resolve(); got != l {
			t.Errorf("%q.resolve() = %q; want the setting itself", l, got)
		}
	}
	if got := LanguageSystem.resolve(); got != LanguageZh && got != LanguageEn {
		t.Errorf("LanguageSystem.resolve() = %q; want zh or en", got)
	}
}

func TestAppVersionComesFromBuildConfig(t *testing.T) {
	if v := appVersion(); v == "" {
		t.Fatal("appVersion is empty; want info.version from build/config.yml")
	}
}

// MECHBOX_TEST_WSL_SHELL 让「WSL 发行版」就是本机的 sh;每个发行版的 sh 启动一次、一直用下去,所以各处用不同的名字。
func TestWSLDistroSetting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MECHBOX_TEST_WSL_SHELL", "sh")
	lib := startLibrary(t)
	app := startApp(t, lib)

	settings := app.Settings()
	settings.WSLDistro = "saved-distro"
	if _, err := app.SaveSettings(t.Context(), settings); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	if got := lib.lib.HomeDirectory(); !strings.HasPrefix(got, `\\wsl.localhost\saved-distro\`) {
		t.Fatalf("HomeDirectory = %q", got)
	}

	t.Setenv("MECHBOX_TEST_WSL_SHELL", "/nonexistent/sh")
	settings.WSLDistro = "broken-distro"
	if _, err := app.SaveSettings(t.Context(), settings); !library.IsCode(err, library.FailedPrecondition) {
		t.Fatalf("SaveSettings with a broken distro = %v", err)
	}
	if got := app.Settings().WSLDistro; got != "saved-distro" {
		t.Fatalf("WSLDistro after a failed switch = %q", got)
	}
	settings.WSLDistro = ""
	if _, err := app.SaveSettings(t.Context(), settings); err != nil {
		t.Fatalf("SaveSettings back to Windows: %v", err)
	}
	if got := lib.lib.HomeDirectory(); got != home {
		t.Fatalf("HomeDirectory back on Windows = %q", got)
	}
}

func TestWSLDistroUnavailableAtStartup(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MECHBOX_TEST_WSL_SHELL", "/nonexistent/sh")
	path := filepath.Join(t.TempDir(), "library.db")
	created := NewLibraryService(path)
	if err := created.ServiceStartup(t.Context(), application.ServiceOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := created.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO config(key, value) VALUES ('wsl_distro', 'late-distro')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	lib := NewLibraryService(path)
	if err := lib.ServiceStartup(t.Context(), application.ServiceOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.ServiceShutdown() })
	app := startApp(t, lib)
	if got := app.Settings().WSLDistro; got != "late-distro" {
		t.Fatalf("WSLDistro = %q; want the stored one", got)
	}
	if app.WSLDistroError() == "" {
		t.Fatal("no error for a distro that can't be opened")
	}
	home := lib.lib.HomeDirectory()
	if strings.HasPrefix(home, `\\`) {
		t.Fatalf("HomeDirectory = %q; want the Windows home until the distro opens", home)
	}
	if err := app.RetryWSLDistro(t.Context()); err == nil || app.WSLDistroError() == "" {
		t.Fatalf("RetryWSLDistro = %v, error %q", err, app.WSLDistroError())
	}

	t.Setenv("MECHBOX_TEST_WSL_SHELL", "sh")
	if err := app.RetryWSLDistro(t.Context()); err != nil {
		t.Fatalf("RetryWSLDistro: %v", err)
	}
	if app.WSLDistroError() != "" || !strings.HasPrefix(lib.lib.HomeDirectory(), `\\wsl.localhost\late-distro\`) {
		t.Fatalf("after retry: error %q, home %q", app.WSLDistroError(), lib.lib.HomeDirectory())
	}
}
