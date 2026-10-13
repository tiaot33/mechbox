package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tiaot33/skills-tui/internal/library"
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
			AutoCheck: false, CheckInterval: CheckWeekly, StayInMenuBar: false, DockBadge: false,
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
	if err := os.WriteFile(path, []byte(`{"dock_badge": false, "check_interval": "sometimes"}`), 0o644); err != nil {
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
