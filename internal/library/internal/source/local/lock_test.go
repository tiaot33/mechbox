package local

import (
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tiaot33/skills-tui/internal/library/internal/skillfile"
)

func TestTreeHashMatchesGit(t *testing.T) {
	files := []skillfile.File{
		{Path: "SKILL.md", Content: []byte("---\nname: pdf\n---\n"), Mode: skillfile.ModeRegular},
		{Path: "a-b.md", Content: []byte("dash"), Mode: skillfile.ModeRegular},
		{Path: "a/nested.md", Content: []byte("nested"), Mode: skillfile.ModeRegular},
		{Path: "a/deep/x.txt", Content: []byte{0, 1, 2}, Mode: skillfile.ModeRegular},
		{Path: "scripts/run.sh", Content: []byte("#!/bin/sh\n"), Mode: skillfile.ModeExecutable},
	}
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", repo}, args...)...)
		command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
		output, err := command.Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(output))
	}
	git("init", "-q")
	for _, file := range files {
		path := filepath.Join(repo, "skill", filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if file.Mode == skillfile.ModeExecutable {
			mode = 0o755
		}
		if err := os.WriteFile(path, file.Content, mode); err != nil {
			t.Fatal(err)
		}
	}
	git("add", ".")
	want := git("rev-parse", git("write-tree")+":skill")
	if got := hex.EncodeToString(treeHash(files)); got != want {
		t.Fatalf("treeHash() = %s, want %s", got, want)
	}
	if !(LockEntry{FolderHash: strings.ToUpper(want)}).Matches(files) {
		t.Fatal("Matches() = false for the folder's tree SHA")
	}
}
