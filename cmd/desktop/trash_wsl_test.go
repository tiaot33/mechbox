package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSplitWSLPath(t *testing.T) {
	distro, linux, ok := splitWSLPath(`\\wsl.localhost\Ubuntu\home\me\.claude\skills\tdd`)
	if !ok || distro != "Ubuntu" || linux != "/home/me/.claude/skills/tdd" {
		t.Fatalf("split = %q %q %v", distro, linux, ok)
	}
	if _, _, ok := splitWSLPath(`C:\Users\me\.claude\skills\tdd`); ok {
		t.Fatal("split a Windows path")
	}
}

func TestDistroTrashScript(t *testing.T) {
	home := t.TempDir()
	folder := filepath.Join(home, ".claude/skills/tdd")
	for range 2 {
		if err := os.MkdirAll(folder, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(folder, "SKILL.md"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		command := exec.Command("sh", "-c", distroTrashScript, "sh", folder, trashInfo(folder, time.Now()))
		command.Env = append(os.Environ(), "HOME="+home, "XDG_DATA_HOME=")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, output)
		}
	}
	trash := filepath.Join(home, ".local/share/Trash")
	for _, name := range []string{"files/tdd/SKILL.md", "files/tdd.1/SKILL.md", "info/tdd.trashinfo", "info/tdd.1.trashinfo"} {
		if _, err := os.Stat(filepath.Join(trash, name)); err != nil {
			t.Error(err)
		}
	}
	info, _ := os.ReadFile(filepath.Join(trash, "info/tdd.trashinfo"))
	if !strings.HasPrefix(string(info), "[Trash Info]\nPath="+filepath.ToSlash(folder)) {
		t.Errorf("trashinfo = %q", info)
	}
	if _, err := os.Stat(folder); !os.IsNotExist(err) {
		t.Errorf("folder still there: %v", err)
	}
	command := exec.Command("sh", "-c", distroTrashScript, "sh", filepath.Join(home, "missing"), "x")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("missing folder: %v %s", err, output)
	}
}
