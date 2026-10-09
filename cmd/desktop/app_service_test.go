package main

import (
	"testing"
	"time"

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
