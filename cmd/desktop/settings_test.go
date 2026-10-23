package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tiaot33/mechbox/internal/library"
)

func TestSettingsDefaultWhenFileMissing(t *testing.T) {
	file, err := loadSettings(filepath.Join(t.TempDir(), settingsFileName))
	if err != nil {
		t.Fatalf("loadSettings: %v", err)
	}
	if !reflect.DeepEqual(file, settingsFile{Settings: defaultSettings()}) {
		t.Fatalf("loadSettings = %+v; want defaults %+v", file, defaultSettings())
	}
	want := Settings{AutoCheck: true, CheckInterval: CheckDaily, StayInMenuBar: true, DockBadge: true}
	if !reflect.DeepEqual(file.Settings, want) {
		t.Fatalf("defaults = %+v; want %+v", file.Settings, want)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", settingsFileName)
	want := settingsFile{
		Settings: Settings{
			AutoCheck: false, CheckInterval: CheckWeekly, StayInMenuBar: false, DockBadge: false, Language: LanguageEn,
			AgentsShown: map[library.AgentCode]bool{library.AgentCodex: false, "custom-0a1b2c3d4e5f": true},
		},
		Sync: syncFields{Endpoint: "https://s3.example.com", Bucket: "b", Key: "k.db", Region: "r", AccessKeyID: "AKID"},
	}
	if err := saveSettings(path, want); err != nil {
		t.Fatalf("saveSettings: %v", err)
	}
	got, err := loadSettings(path)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("loadSettings = %+v, %v; want %+v", got, err, want)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("settings directory has %d entries; want only %s", len(entries), settingsFileName)
	}
}

func TestSettingsFillMissingFieldsWithDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), settingsFileName)
	if err := os.WriteFile(path, []byte(`{"dock_badge": false, "check_interval": "sometimes", "language": "fr"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := loadSettings(path)
	if err != nil {
		t.Fatalf("loadSettings: %v", err)
	}
	want := settingsFile{Settings: defaultSettings()}
	want.DockBadge = false
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("loadSettings = %+v; want %+v", got, want)
	}
}

func TestSettingsCorruptFileFallsBackToDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), settingsFileName)
	if err := os.WriteFile(path, []byte(`{`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := loadSettings(path)
	if err == nil || !reflect.DeepEqual(got, settingsFile{Settings: defaultSettings()}) {
		t.Fatalf("loadSettings = %+v, %v; want defaults and an error", got, err)
	}
}

func TestSaveSettingsRejectsUnknownInterval(t *testing.T) {
	path := filepath.Join(t.TempDir(), settingsFileName)
	service := NewAppService(path, nil)
	if _, err := service.SaveSettings(t.Context(), Settings{CheckInterval: "sometimes"}); err == nil {
		t.Fatal("SaveSettings accepted an unknown interval")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("settings file written for a rejected change: %v", err)
	}
}

func TestSaveSettingsRejectsUnknownLanguage(t *testing.T) {
	path := filepath.Join(t.TempDir(), settingsFileName)
	service := NewAppService(path, nil)
	if _, err := service.SaveSettings(t.Context(), Settings{CheckInterval: CheckDaily, Language: "fr"}); err == nil {
		t.Fatal("SaveSettings accepted an unknown language")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("settings file written for a rejected change: %v", err)
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
	path := filepath.Join(t.TempDir(), settingsFileName)
	app := startApp(t, path, lib)

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
	lib := startLibrary(t)
	path := filepath.Join(t.TempDir(), settingsFileName)
	if err := saveSettings(path, settingsFile{Settings: Settings{CheckInterval: CheckDaily, WSLDistro: "late-distro"}, FirstOpenDone: true}); err != nil {
		t.Fatal(err)
	}
	app := startApp(t, path, lib)
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
