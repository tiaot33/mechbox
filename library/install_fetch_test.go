package library_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tiaot33/skills-tui/library"
)

func TestInstallFetchFailureKeepsHandleAndLibraryUnchanged(t *testing.T) {
	repository := installFetchRepository(t)
	lib, err := library.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
	ctx := t.Context()
	fetch, err := lib.Fetch(ctx, library.SourceSpec{Type: library.SourceGitHub, Locator: library.SourceLocator{Repository: repository, Branch: "main"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(repository); err != nil {
		t.Fatal(err)
	}
	if installed, err := lib.InstallFetch(ctx, fetch.ID, []string{"first", "missing"}); err == nil || len(installed) != 0 {
		t.Fatalf("unknown selection = %v, %v; want no results and error", installed, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if installed, err := lib.InstallFetch(canceled, fetch.ID, nil); !errors.Is(err, context.Canceled) || len(installed) != 0 {
		t.Fatalf("canceled install = %v, %v; want no results and cancellation", installed, err)
	}
	collections, err := lib.ListCollections(ctx)
	if err != nil || len(collections) != 0 {
		t.Fatalf("failed attempts left collections: %v, %v", collections, err)
	}
	installed, err := lib.InstallFetch(ctx, fetch.ID, nil)
	if err != nil || len(installed) != 2 || installed[0].Name != "first" || installed[1].Name != "second" {
		t.Fatalf("offline retry with empty filter = %v, %v; want both Skills", installed, err)
	}
	if _, err := lib.InstallFetch(ctx, fetch.ID, []string{"second"}); !library.IsCode(err, library.NotFound) {
		t.Fatalf("reused handle after success: %v; want NotFound", err)
	}
}

func TestInstallFetchConcurrentCallsConsumeHandleOnce(t *testing.T) {
	repository := installFetchRepository(t)
	// 每轮使用独立的库,两个调用选择不同候选,避免唯一约束掩盖重复消费。
	for range 10 {
		lib, err := library.Open(filepath.Join(t.TempDir(), "library.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = lib.Close() })
		ctx := t.Context()
		fetch, err := lib.Fetch(ctx, library.SourceSpec{Type: library.SourceGitHub, Locator: library.SourceLocator{Repository: repository, Branch: "main"}})
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		results := make(chan error, 2)
		for _, name := range fetch.Skills {
			go func() {
				<-start
				_, err := lib.InstallFetch(ctx, fetch.ID, []string{name})
				results <- err
			}()
		}
		close(start)
		succeeded := 0
		for range 2 {
			if err := <-results; err == nil {
				succeeded++
			} else if !library.IsCode(err, library.NotFound) {
				t.Errorf("concurrent install error = %v, want NotFound", err)
			}
		}
		if succeeded != 1 {
			t.Fatalf("successful consumers = %d, want 1", succeeded)
		}
		skills, err := lib.ListSkills(ctx)
		if err != nil || len(skills) != 1 {
			t.Fatalf("concurrent installs left %v, %v; want one Skill", skills, err)
		}
	}
}

func installFetchRepository(t *testing.T) string {
	t.Helper()
	repository := t.TempDir()
	runGit(t, repository, "init", "-b", "main")
	writeFile(t, filepath.Join(repository, "first", "SKILL.md"), []byte("first"), 0o644)
	writeFile(t, filepath.Join(repository, "second", "SKILL.md"), []byte("second"), 0o644)
	commitGit(t, repository, "initial")
	return repository
}
