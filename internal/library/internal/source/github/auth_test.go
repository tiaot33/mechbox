package github

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// 从访达启动的应用 PATH 里没有 Homebrew:PATH 找不到 gh 时按常见安装位置的顺序找,都没有就当作没有 gh。
func TestFindGHFallsBackToCommonLocations(t *testing.T) {
	dir := t.TempDir()
	homebrew := filepath.Join(dir, "opt", "homebrew", "bin", "gh")
	usrLocal := filepath.Join(dir, "usr", "local", "bin", "gh")
	notExecutable := filepath.Join(dir, "not-executable", "gh")
	for path, mode := range map[string]os.FileMode{homebrew: 0o755, usrLocal: 0o755, notExecutable: 0o644} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), mode); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", t.TempDir())
	missing := filepath.Join(dir, "missing", "gh")

	for _, tc := range []struct {
		name       string
		configured string
		locations  []string
		want       string
	}{
		{"first existing location", "", []string{missing, notExecutable, homebrew, usrLocal}, homebrew},
		{"none found", "", []string{missing, notExecutable}, ""},
		{"configured wins", usrLocal, []string{homebrew}, usrLocal},
		{"configured missing is not replaced", missing, []string{homebrew}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := findGH(tc.configured, tc.locations); got != tc.want {
				t.Fatalf("findGH = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGHLocationsIncludeHomebrew(t *testing.T) {
	for _, want := range []string{"/opt/homebrew/bin/gh", "/usr/local/bin/gh"} {
		if !slices.Contains(ghLocations, want) {
			t.Fatalf("ghLocations = %q, missing %s", ghLocations, want)
		}
	}
}
