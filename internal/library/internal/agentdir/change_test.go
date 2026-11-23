//go:build !windows

package agentdir

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tiaot33/mechbox/internal/library/internal/skillfile"
)

func perm(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func forEachSystem(t *testing.T, test func(t *testing.T, at func(string) string)) {
	t.Run("native", func(t *testing.T) { test(t, func(path string) string { return path }) })
	t.Run("distro", func(t *testing.T) { test(t, unc) })
}

func TestStagedFoldersAreAsOpenAsTheirSubfolders(t *testing.T) {
	files := []skillfile.File{
		{Path: "SKILL.md", Content: []byte("v1"), Mode: skillfile.ModeRegular},
		{Path: "scripts/run.sh", Content: []byte("#!/bin/sh\n"), Mode: skillfile.ModeExecutable},
	}
	forEachSystem(t, func(t *testing.T, at func(string) string) {
		ctx := t.Context()
		skills := filepath.Join(tempDir(t), ".claude/skills")
		folder := filepath.Join(skills, "alpha")
		requireOpen := func(step string) {
			t.Helper()
			if outer, inner := perm(t, folder), perm(t, filepath.Join(folder, "scripts")); outer != inner || outer&0o055 != 0o055 {
				t.Fatalf("%s: folder mode = %v, scripts = %v; want the folder as open as its subfolders (0755 under umask 022)", step, outer, inner)
			}
		}

		change := Change{}
		if err := change.Stage(ctx, at(skills), "alpha", files); err != nil {
			t.Fatal(err)
		}
		if err := change.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		change.Done()
		requireOpen("install")

		if err := os.Chmod(folder, 0o700); err != nil {
			t.Fatal(err)
		}
		change = Change{}
		if err := change.Stage(ctx, at(skills), "alpha", files); err != nil {
			t.Fatal(err)
		}
		if err := change.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		change.Done()
		requireOpen("replace")

		if err := os.Chmod(folder, 0o750); err != nil {
			t.Fatal(err)
		}
		change = Change{}
		if err := change.Stage(ctx, at(skills), "alpha", files); err != nil {
			t.Fatal(err)
		}
		if err := change.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if err := change.Rollback(); err != nil {
			t.Fatal(err)
		}
		if got := perm(t, folder); got != 0o750 {
			t.Fatalf("undone replacement left mode %v; want the original 0750 back", got)
		}
		if entries, _ := os.ReadDir(skills); len(entries) != 1 {
			t.Fatalf("leftovers: %v", entries)
		}
	})
}

func TestCopiedFoldersKeepTheirModes(t *testing.T) {
	for _, mode := range []os.FileMode{0o700, 0o750, 0o755} {
		t.Run(mode.String(), func(t *testing.T) {
			forEachSystem(t, func(t *testing.T, at func(string) string) {
				ctx := t.Context()
				home := tempDir(t)
				source := filepath.Join(home, ".agents/skills/shared")
				write(t, filepath.Join(source, "SKILL.md"), "x", 0o644)
				write(t, filepath.Join(source, "scripts/run.sh"), "#!/bin/sh\n", 0o755)
				if err := os.Chmod(source, mode); err != nil {
					t.Fatal(err)
				}
				skills := filepath.Join(home, ".claude/skills")

				change := Change{}
				if err := change.Copy(ctx, at(source), at(skills), "copied"); err != nil {
					t.Fatal(err)
				}
				if err := change.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				change.Done()
				if got := perm(t, filepath.Join(skills, "copied")); got != mode {
					t.Fatalf("copied folder mode = %v; want %v", got, mode)
				}

				link := filepath.Join(skills, "linked")
				symlink(t, source, link)
				change = Change{}
				if err := change.Detach(ctx, at(link)); err != nil {
					t.Fatal(err)
				}
				if err := change.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				change.Done()
				if info, err := os.Lstat(link); err != nil || !info.IsDir() || perm(t, link) != mode {
					t.Fatalf("detached folder = %v, %v; want a folder with mode %v", info, err, mode)
				}
			})
		})
	}
}
