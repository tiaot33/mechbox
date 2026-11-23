package main

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/tiaot33/mechbox/internal/library"
)

func skillFrom(source library.SourceType, check *library.UpdateCheck, fetched time.Time) library.Skill {
	return library.Skill{Source: library.Source{Type: source}, LastUpdateCheck: check, FetchedAt: fetched}
}

func TestBadgeCountFollowsPending(t *testing.T) {
	skills := []library.Skill{
		skillFrom(library.SourceGitHub, &library.UpdateCheck{Status: library.UpdateAvailable, Pending: true}, time.Time{}),
		skillFrom(library.SourceGitHub, &library.UpdateCheck{Status: library.UpdateDeleted, RenamedTo: "pdf-kit", Pending: true}, time.Time{}),
		skillFrom(library.SourceGitHub, &library.UpdateCheck{Status: library.UpdateAvailable, Skipped: true}, time.Time{}),
		skillFrom(library.SourceLocal, &library.UpdateCheck{Status: library.UpdateAvailable}, time.Time{}),
		skillFrom(library.SourceGitHub, &library.UpdateCheck{Status: library.UpdateCurrent}, time.Time{}),
		skillFrom(library.SourceURL, nil, time.Time{}),
	}
	if got := badgeCount(skills); got != 2 {
		t.Fatalf("badgeCount = %d; want 2 (only skills the library marks pending)", got)
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

func TestAutoCheckDue(t *testing.T) {
	s := newTestServices(t, tempLibrary(t))
	lib := s.lib
	var archive bytes.Buffer
	w := zip.NewWriter(&archive)
	f, _ := w.Create("remote/SKILL.md")
	_, _ = f.Write([]byte("---\nname: remote\ndescription: remote\n---\n"))
	_ = w.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(archive.Bytes()) }))
	t.Cleanup(server.Close)
	remote := fetchSkill(t, lib.lib, library.PreviewFetchRequest{Input: server.URL + "/remote.zip"}, "remote")
	fetchLocalSkill(t, lib.lib, "loc")
	fetched := remote.FetchedAt
	only := []library.CollectionID{remote.CollectionID}

	c := s.app.checks
	due := func(settings Settings, opened bool, last time.Time, now time.Time) []library.CollectionID {
		c.settings.mu.Lock()
		c.settings.settings = settings
		c.settings.mu.Unlock()
		c.mu.Lock()
		c.opened, c.lastAttempt = opened, last
		c.mu.Unlock()
		return c.due(t.Context(), now)
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
	if got := c.due(t.Context(), fetched); got != nil {
		t.Errorf("只在打开时：打开后只查一次，再看 due = %v", got)
	}
}

// 自动检查完让前端重读(updates-checked);退出时取消了的检查不发。
func TestAutoCheckTellsFrontendWhenChecked(t *testing.T) {
	s := newTestServices(t, tempLibrary(t))
	fetchLocalSkill(t, s.lib.lib, "loc")
	collections, err := s.lib.lib.ListCollections(t.Context())
	if err != nil || len(collections) != 1 {
		t.Fatalf("ListCollections = %v, %v", collections, err)
	}
	ids := []library.CollectionID{collections[0].ID}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	s.app.checks.check(ctx, ids)
	s.app.checks.check(t.Context(), ids)
	s.events.waitFor(t, updatesCheckedEvent)
	if got := s.events.count(updatesCheckedEvent); got != 1 {
		t.Fatalf("updates-checked emitted %d times; want once, not for the cancelled check", got)
	}
	if s.app.checks.lastAttempt.IsZero() {
		t.Fatal("lastAttempt not recorded")
	}
}

// 从菜单栏或 Dock 重新打开窗口算作打开了一次:「只在打开时」随之检查，不等下一分钟。
func TestReopenedWindowCountsAsOpening(t *testing.T) {
	s := newTestServices(t, tempLibrary(t))
	c := s.app.checks
	c.mu.Lock()
	c.opened = false
	c.mu.Unlock()
	c.reopened()
	select {
	case <-c.wake:
	default:
		t.Fatal("reopened did not wake the check loop")
	}
	c.mu.Lock()
	opened := c.opened
	c.mu.Unlock()
	if !opened {
		t.Fatal("reopened did not count as opening")
	}
}
