//go:build !windows

package agentdir

import (
	"errors"
	"io/fs"
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

func umaskedMode(t *testing.T, mode os.FileMode) os.FileMode {
	t.Helper()
	probe := filepath.Join(t.TempDir(), "probe")
	if err := os.Mkdir(probe, mode); err != nil {
		t.Fatal(err)
	}
	return perm(t, probe)
}

func forEachSystem(t *testing.T, test func(t *testing.T, at func(string) string, open os.FileMode)) {
	t.Run("native", func(t *testing.T) { test(t, func(path string) string { return path }, umaskedMode(t, 0o755)) })
	t.Run("distro", func(t *testing.T) { test(t, unc, 0o755) })
}

func TestStagedFoldersAreAsOpenAsTheirSubfolders(t *testing.T) {
	files := []skillfile.File{
		{Path: "SKILL.md", Content: []byte("v1"), Mode: skillfile.ModeRegular},
		{Path: "scripts/run.sh", Content: []byte("#!/bin/sh\n"), Mode: skillfile.ModeExecutable},
	}
	forEachSystem(t, func(t *testing.T, at func(string) string, open os.FileMode) {
		ctx := t.Context()
		skills := filepath.Join(tempDir(t), ".claude/skills")
		folder := filepath.Join(skills, "alpha")
		requireOpen := func(step string) {
			t.Helper()
			if outer, inner := perm(t, folder), perm(t, filepath.Join(folder, "scripts")); outer != inner || outer != open {
				t.Fatalf("%s: folder mode = %v, scripts = %v; want both %v", step, outer, inner, open)
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
			forEachSystem(t, func(t *testing.T, at func(string) string, _ os.FileMode) {
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

func TestStageDirectoriesStartWithTheRequestedMode(t *testing.T) {
	forEachSystem(t, func(t *testing.T, at func(string) string, open os.FileMode) {
		directory := filepath.Join(tempDir(t), ".claude/skills")
		for mode, want := range map[fs.FileMode]fs.FileMode{0o700: 0o700, 0o755: open} {
			stage := filepath.Join(directory, ".alpha-"+mode.String())
			if err := on(at(directory)).mkdirStage(at(directory), at(stage), mode); err != nil {
				t.Fatal(err)
			}
			if got := perm(t, stage); got != want {
				t.Fatalf("stage for %v = %v; want %v", mode, got, want)
			}
		}
	})
}

func TestCopyKeepsItsStagePrivateUntilTheCopyFinishes(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads unreadable files")
	}
	ctx := t.Context()
	home := tempDir(t)
	source := filepath.Join(home, ".agents/skills/private")
	write(t, filepath.Join(source, "SKILL.md"), "x", 0o644)
	write(t, filepath.Join(source, "unreadable"), "x", 0o000)
	if err := os.Chmod(source, 0o700); err != nil {
		t.Fatal(err)
	}
	skills := filepath.Join(home, ".claude/skills")

	change := Change{}
	if err := change.Copy(ctx, source, skills, "private"); err == nil {
		t.Fatal("copy of an unreadable file succeeded")
	}
	if got := perm(t, change.placements[0].stage); got != 0o700 {
		t.Fatalf("half-copied stage mode = %v; want 0700 so the copied files stay private", got)
	}
	if err := change.Rollback(); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(skills); len(entries) != 0 {
		t.Fatalf("leftovers: %v", entries)
	}
}

func TestStageRejectsFolderNamesOutsideTheDirectory(t *testing.T) {
	forEachSystem(t, func(t *testing.T, at func(string) string, _ os.FileMode) {
		root := tempDir(t)
		skills := filepath.Join(root, ".claude/skills")
		for _, name := range []string{"", ".", "..", "../escape", "a/b", `a\b`} {
			change := Change{}
			if err := change.Stage(t.Context(), at(skills), name, nil); err == nil {
				t.Fatalf("Stage(%q) succeeded", name)
			}
		}
		if _, err := os.Stat(skills); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("rejected names still created %s: %v", skills, err)
		}
		if entries, _ := os.ReadDir(filepath.Join(root, ".claude")); len(entries) != 0 {
			t.Fatalf("rejected names left %v", entries)
		}
	})
}
