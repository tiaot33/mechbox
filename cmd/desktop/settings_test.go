package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSettingsDefaultWhenFileMissing(t *testing.T) {
	got, err := loadSettings(filepath.Join(t.TempDir(), settingsFileName))
	if err != nil {
		t.Fatalf("loadSettings: %v", err)
	}
	if got != defaultSettings() {
		t.Fatalf("loadSettings = %+v; want defaults %+v", got, defaultSettings())
	}
	want := Settings{AutoCheck: true, CheckInterval: CheckDaily, StayInMenuBar: true, DockBadge: true}
	if got != want {
		t.Fatalf("defaults = %+v; want %+v", got, want)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", settingsFileName)
	want := Settings{AutoCheck: false, CheckInterval: CheckWeekly, StayInMenuBar: false, DockBadge: false}
	if err := saveSettings(path, want); err != nil {
		t.Fatalf("saveSettings: %v", err)
	}
	got, err := loadSettings(path)
	if err != nil || got != want {
		t.Fatalf("loadSettings = %+v, %v; want %+v", got, err, want)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("settings directory has %d entries; want only %s", len(entries), settingsFileName)
	}
}

func TestSettingsFillMissingFieldsWithDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), settingsFileName)
	if err := os.WriteFile(path, []byte(`{"dock_badge": false, "check_interval": "sometimes"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := loadSettings(path)
	if err != nil {
		t.Fatalf("loadSettings: %v", err)
	}
	want := defaultSettings()
	want.DockBadge = false
	if got != want {
		t.Fatalf("loadSettings = %+v; want %+v", got, want)
	}
}

func TestSettingsCorruptFileFallsBackToDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), settingsFileName)
	if err := os.WriteFile(path, []byte(`{`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := loadSettings(path)
	if err == nil || got != defaultSettings() {
		t.Fatalf("loadSettings = %+v, %v; want defaults and an error", got, err)
	}
}

func TestSaveSettingsRejectsUnknownInterval(t *testing.T) {
	path := filepath.Join(t.TempDir(), settingsFileName)
	service := NewAppService(path, nil)
	if _, err := service.SaveSettings(Settings{CheckInterval: "sometimes"}); err == nil {
		t.Fatal("SaveSettings accepted an unknown interval")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("settings file written for a rejected change: %v", err)
	}
}

func TestAppVersionComesFromBuildConfig(t *testing.T) {
	if v := appVersion(); v == "" {
		t.Fatal("appVersion is empty; want info.version from build/config.yml")
	}
}
