//go:build linux || darwin

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

// skillFolder 在 dir 下建一个带 SKILL.md 的文件夹，返回它的真实路径(macOS 的临时目录经过符号链接)。
func skillFolder(t *testing.T, dir, name string) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(dir, name)
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "SKILL.md"), []byte("# "+name), 0o644); err != nil {
		t.Fatal(err)
	}
	return folder
}

func readTrashInfo(t *testing.T, trash, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(trash, "info", name+".trashinfo"))
	if err != nil {
		t.Fatalf("read trashinfo: %v", err)
	}
	return string(data)
}

func TestFreedesktopTrashMovesFolderIntoHomeTrash(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	folder := skillFolder(t, t.TempDir(), "my notes")

	if err := moveToFreedesktopTrash(folder); err != nil {
		t.Fatalf("moveToFreedesktopTrash: %v", err)
	}
	trash := filepath.Join(data, "Trash")
	if _, err := os.Lstat(folder); !os.IsNotExist(err) {
		t.Fatalf("folder should be gone from %s; Lstat: %v", folder, err)
	}
	if _, err := os.Stat(filepath.Join(trash, "files", "my notes", "SKILL.md")); err != nil {
		t.Fatalf("folder should be in the trash: %v", err)
	}
	want := regexp.MustCompile(`^\[Trash Info\]\nPath=` + regexp.QuoteMeta(filepath.Dir(folder)) + `/my%20notes\nDeletionDate=\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\n$`)
	if info := readTrashInfo(t, trash, "my notes"); !want.MatchString(info) {
		t.Fatalf("trashinfo = %q; want absolute, escaped Path and local DeletionDate", info)
	}
}

func TestFreedesktopTrashKeepsBothFoldersWithTheSameName(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	first := skillFolder(t, t.TempDir(), "notes")
	second := skillFolder(t, t.TempDir(), "notes")

	for _, folder := range []string{first, second} {
		if err := moveToFreedesktopTrash(folder); err != nil {
			t.Fatalf("moveToFreedesktopTrash(%s): %v", folder, err)
		}
	}
	trash := filepath.Join(data, "Trash")
	for name, folder := range map[string]string{"notes": first, "notes.1": second} {
		if _, err := os.Stat(filepath.Join(trash, "files", name, "SKILL.md")); err != nil {
			t.Fatalf("%s should be in the trash as %s: %v", folder, name, err)
		}
		if want := "Path=" + folder + "\n"; !regexp.MustCompile(regexp.QuoteMeta(want)).MatchString(readTrashInfo(t, trash, name)) {
			t.Fatalf("trashinfo for %s should record %s", name, folder)
		}
	}
}

func TestFreedesktopTrashSkipsNameLeftInFilesWithoutInfo(t *testing.T) {
	trash := filepath.Join(t.TempDir(), "Trash")
	if err := prepareTrash(trash); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(trash, "files", "notes"), 0o700); err != nil {
		t.Fatal(err)
	}
	folder := skillFolder(t, t.TempDir(), "notes")

	if err := trashInto(trash, "", folder); err != nil {
		t.Fatalf("trashInto: %v", err)
	}
	if _, err := os.Stat(filepath.Join(trash, "files", "notes.1", "SKILL.md")); err != nil {
		t.Fatalf("folder should be trashed as notes.1: %v", err)
	}
	if _, err := os.Stat(filepath.Join(trash, "info", "notes.trashinfo")); !os.IsNotExist(err) {
		t.Fatalf("no trashinfo should be left for the existing notes; Stat: %v", err)
	}
}

func TestFreedesktopTrashLeavesFolderAndNoInfoWhenMoveFails(t *testing.T) {
	trash := filepath.Join(t.TempDir(), "Trash")
	if err := prepareTrash(trash); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "notes")

	if err := trashInto(trash, "", missing); err == nil {
		t.Fatal("trashInto should fail for a folder that isn't there")
	}
	if entries, _ := os.ReadDir(filepath.Join(trash, "info")); len(entries) != 0 {
		t.Fatalf("info should stay empty after a failed move; got %v", entries)
	}
}

func TestFreedesktopTopdirTrashRecordsPathRelativeToTop(t *testing.T) {
	top := t.TempDir()
	folder := skillFolder(t, top, filepath.Join("work", "proj", ".claude", "skills", "notes"))
	top = filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(folder)))))

	trash, err := topdirTrash(top, os.Getuid())
	if err != nil {
		t.Fatalf("topdirTrash: %v", err)
	}
	if want := filepath.Join(top, ".Trash-"+strconv.Itoa(os.Getuid())); trash != want {
		t.Fatalf("trash = %s; want %s", trash, want)
	}
	if info, err := os.Stat(trash); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("trash should be created with mode 0700; Stat: %v, %v", info, err)
	}
	if err := trashInto(trash, top, folder); err != nil {
		t.Fatalf("trashInto: %v", err)
	}
	if info := readTrashInfo(t, trash, "notes"); !regexp.MustCompile(`\nPath=work/proj/\.claude/skills/notes\n`).MatchString(info) {
		t.Fatalf("trashinfo = %q; want Path relative to the top directory", info)
	}
}

func TestFreedesktopTopdirTrashUsesStickySharedTrash(t *testing.T) {
	top := t.TempDir()
	shared := filepath.Join(top, ".Trash")
	if err := os.Mkdir(shared, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(shared, 0o777|os.ModeSticky); err != nil {
		t.Fatal(err)
	}

	trash, err := topdirTrash(top, os.Getuid())
	if err != nil {
		t.Fatalf("topdirTrash: %v", err)
	}
	if want := filepath.Join(shared, strconv.Itoa(os.Getuid())); trash != want {
		t.Fatalf("trash = %s; want %s", trash, want)
	}
}

func TestFreedesktopTopdirTrashIgnoresUnsafeSharedTrash(t *testing.T) {
	for name, setup := range map[string]func(t *testing.T, shared string){
		"not sticky": func(t *testing.T, shared string) {
			if err := os.Mkdir(shared, 0o777); err != nil {
				t.Fatal(err)
			}
		},
		"symlink": func(t *testing.T, shared string) {
			target := t.TempDir()
			if err := os.Chmod(target, 0o777|os.ModeSticky); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, shared); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			top := t.TempDir()
			setup(t, filepath.Join(top, ".Trash"))

			trash, err := topdirTrash(top, os.Getuid())
			if err != nil {
				t.Fatalf("topdirTrash: %v", err)
			}
			if want := filepath.Join(top, ".Trash-"+strconv.Itoa(os.Getuid())); trash != want {
				t.Fatalf("trash = %s; want %s", trash, want)
			}
		})
	}
}

func TestFreedesktopTopdirTrashRefusesSymlinkedUserTrash(t *testing.T) {
	top := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(top, ".Trash-"+strconv.Itoa(os.Getuid()))); err != nil {
		t.Fatal(err)
	}

	if _, err := topdirTrash(top, os.Getuid()); err == nil {
		t.Fatal("topdirTrash should refuse a .Trash-$uid that is a symlink")
	}
}

func TestFreedesktopTrashMountTopStopsAtFileSystemRoot(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}

	top, err := mountTop(dir, device(info))
	if err != nil {
		t.Fatalf("mountTop: %v", err)
	}
	topInfo, err := os.Stat(top)
	if err != nil {
		t.Fatal(err)
	}
	if device(topInfo) != device(info) {
		t.Fatalf("mountTop %s is on another file system than %s", top, dir)
	}
	if parent := filepath.Dir(top); parent != top {
		parentInfo, err := os.Stat(parent)
		if err != nil {
			t.Fatal(err)
		}
		if device(parentInfo) == device(info) {
			t.Fatalf("mountTop %s should be the top of its file system; %s is on the same one", top, parent)
		}
	}
}
