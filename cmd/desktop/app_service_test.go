package main

import (
	"archive/zip"
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/tiaot33/skills-tui/internal/library"
)

func skillFrom(source library.SourceType, check *library.UpdateCheck, fetched time.Time) library.Skill {
	return library.Skill{Source: library.Source{Type: source}, LastUpdateCheck: check, FetchedAt: fetched}
}

func TestBadgeCountSkipsLocalSources(t *testing.T) {
	available := &library.UpdateCheck{Status: library.UpdateAvailable}
	skills := []library.Skill{
		skillFrom(library.SourceGitHub, available, time.Time{}),
		skillFrom(library.SourceURL, available, time.Time{}),
		skillFrom(library.SourceLocal, available, time.Time{}),
		skillFrom(library.SourceGitHub, &library.UpdateCheck{Status: library.UpdateCurrent}, time.Time{}),
		skillFrom(library.SourceGitHub, &library.UpdateCheck{Status: library.UpdateDeleted}, time.Time{}),
		skillFrom(library.SourceURL, nil, time.Time{}),
	}
	if got := badgeCount(skills); got != 2 {
		t.Fatalf("badgeCount = %d; want 2 (local-directory skills and skills without a new version not counted)", got)
	}
}

func TestOldestConfirmedUsesCheckTimeOrFetchTime(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	hoursAgo := func(h int) time.Time { return now.Add(-time.Duration(h) * time.Hour) }
	skills := []library.Skill{
		skillFrom(library.SourceGitHub, &library.UpdateCheck{CheckedAt: hoursAgo(3), Status: library.UpdateCurrent}, hoursAgo(100)),
		skillFrom(library.SourceURL, nil, hoursAgo(5)),
		skillFrom(library.SourceLocal, nil, hoursAgo(500)),
	}
	if got := oldestConfirmed(skills, now); !got.Equal(hoursAgo(5)) {
		t.Fatalf("oldestConfirmed = %v; want the never-checked skill's fetch time %v", got, hoursAgo(5))
	}
	if got := oldestConfirmed(skills[2:], now); !got.Equal(now) {
		t.Fatalf("oldestConfirmed with only local skills = %v; want now", got)
	}
}

func startLibrary(t *testing.T) *LibraryService {
	t.Helper()
	service := NewLibraryService(filepath.Join(t.TempDir(), "library.db"))
	if err := service.ServiceStartup(t.Context(), application.ServiceOptions{}); err != nil {
		t.Fatalf("ServiceStartup: %v", err)
	}
	t.Cleanup(func() { _ = service.ServiceShutdown() })
	return service
}

func fetchSkill(t *testing.T, lib *library.Library, req library.PreviewFetchRequest, name string) library.Skill {
	t.Helper()
	preview, err := lib.PreviewFetch(t.Context(), req)
	if err != nil {
		t.Fatalf("PreviewFetch: %v", err)
	}
	skills, err := lib.Fetch(t.Context(), preview.ID, []string{name})
	if err != nil || len(skills) != 1 {
		t.Fatalf("Fetch = %v, %v", skills, err)
	}
	return skills[0]
}

func TestAppServiceKeepsSettingsAcrossRestart(t *testing.T) {
	lib := startLibrary(t)
	path := filepath.Join(t.TempDir(), settingsFileName)
	first := startApp(t, path, lib)
	if got := first.Settings(); got != defaultSettings() {
		t.Fatalf("first start = %+v; want defaults", got)
	}
	want := Settings{AutoCheck: false, CheckInterval: CheckWeekly, StayInMenuBar: false, DockBadge: false}
	if _, err := first.SaveSettings(want); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	if err := first.ServiceShutdown(); err != nil {
		t.Fatalf("ServiceShutdown: %v", err)
	}

	second := NewAppService(path, lib)
	second.keychain = first.keychain
	if err := second.ServiceStartup(t.Context(), application.ServiceOptions{}); err != nil {
		t.Fatalf("ServiceStartup after restart: %v", err)
	}
	t.Cleanup(func() { _ = second.ServiceShutdown() })
	if got := second.Settings(); got != want {
		t.Fatalf("after restart = %+v; want %+v", got, want)
	}
}

func TestAutoCheckDue(t *testing.T) {
	lib := startLibrary(t)
	var archive bytes.Buffer
	w := zip.NewWriter(&archive)
	f, _ := w.Create("remote/SKILL.md")
	_, _ = f.Write([]byte("---\nname: remote\ndescription: remote\n---\n"))
	_ = w.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(archive.Bytes()) }))
	t.Cleanup(server.Close)
	remote := fetchSkill(t, lib.lib, library.PreviewFetchRequest{URL: server.URL + "/remote.zip"}, "remote")
	local := filepath.Join(t.TempDir(), "mine", "loc")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, "SKILL.md"), []byte("---\nname: loc\ndescription: loc\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fetchSkill(t, lib.lib, library.PreviewFetchRequest{Local: filepath.Dir(local)}, "loc")
	fetched := remote.FetchedAt
	only := []library.CollectionID{remote.CollectionID}

	a := NewAppService(filepath.Join(t.TempDir(), settingsFileName), lib)
	due := func(settings Settings, opened bool, last time.Time, now time.Time) []library.CollectionID {
		a.settings, a.opened, a.lastAttempt = settings, opened, last
		return a.due(t.Context(), now)
	}
	daily := defaultSettings()
	weekly := daily
	weekly.CheckInterval = CheckWeekly
	onOpen := daily
	onOpen.CheckInterval = CheckOnOpen
	off := daily
	off.AutoCheck = false
	hour := time.Hour

	cases := []struct {
		name     string
		settings Settings
		opened   bool
		last     time.Time
		now      time.Time
		want     []library.CollectionID
	}{
		{"每天：入库不到一天不查", daily, true, time.Time{}, fetched.Add(23 * hour), nil},
		{"每天：入库满一天查非本地目录来源的集合", daily, false, time.Time{}, fetched.Add(25 * hour), only},
		{"每周：满一天不查", weekly, false, time.Time{}, fetched.Add(25 * hour), nil},
		{"每周：满一周查", weekly, false, time.Time{}, fetched.Add(8 * 24 * hour), only},
		{"上次自动检查没多久时不重试", daily, false, fetched.Add(24 * hour), fetched.Add(25 * hour), nil},
		{"关掉自动检查不查", off, true, time.Time{}, fetched.Add(30 * 24 * hour), nil},
		{"只在打开时：打开后查", onOpen, true, time.Time{}, fetched, only},
		{"只在打开时：没打开不查", onOpen, false, time.Time{}, fetched.Add(30 * 24 * hour), nil},
	}
	for _, c := range cases {
		got := due(c.settings, c.opened, c.last, c.now)
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: due = %v; want %v", c.name, got, c.want)
		}
	}
	due(onOpen, true, time.Time{}, fetched)
	if got := a.due(t.Context(), fetched); got != nil {
		t.Errorf("只在打开时：打开后只查一次，再看 due = %v", got)
	}
}
