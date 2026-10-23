package wsl

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestDirFS(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for name, mode := range map[string]os.FileMode{"SKILL.md": 0o644, "scripts/run.sh": 0o755, ".hidden/notes.txt": 0o644, "技能/说明.md": 0o644} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(name), mode); err != nil {
			t.Fatal(err)
		}
	}
	eachShell(t, func(t *testing.T) {
		fsys := DirFS(context.Background(), "test", root)
		if err := fstest.TestFS(fsys, "SKILL.md", "scripts/run.sh", ".hidden/notes.txt", "技能/说明.md"); err != nil {
			t.Fatal(err)
		}
		info, err := fs.Stat(fsys, "scripts/run.sh")
		if err != nil || info.Mode().Perm()&0o111 == 0 {
			t.Fatalf("run.sh = %v %v", info, err)
		}
		if _, err := fs.ReadFile(fsys, "missing"); !os.IsNotExist(err) {
			t.Fatalf("missing: %v", err)
		}
	})
}
