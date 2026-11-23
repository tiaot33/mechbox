package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/tiaot33/mechbox/internal/library"
)

// uninstallFixture 是在临时 HOME 里打开的技能库与两个绑定服务，数据库在应用目录下(与真实布局一样),
// 存着设置与同步设置;移到废纸篓换成直接删除并记下删了哪些，不碰真实的废纸篓。
type uninstallFixture struct {
	*testServices
	dir       string
	discarded []string
}

func startUninstall(t *testing.T) *uninstallFixture {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	f := &uninstallFixture{dir: filepath.Join(t.TempDir(), appDirName)}
	f.testServices = startServices(t, filepath.Join(f.dir, "library.db"))
	f.app.removal.discard = func(path string) error {
		f.discarded = append(f.discarded, path)
		return os.RemoveAll(path)
	}
	if _, err := f.app.SaveSettings(t.Context(), defaultSettings()); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	if err := f.lib.lib.SetSyncSettings(t.Context(), library.SyncSettings{Bucket: "b", AccessKeyID: "AKID", SecretAccessKey: "s3-secret"}); err != nil {
		t.Fatalf("SetSyncSettings: %v", err)
	}
	return f
}

// install 入库一个本地技能并装到 Claude Code 全局，返回装出去的文件夹。
func (f *uninstallFixture) install(t *testing.T, name string) string {
	t.Helper()
	skill := fetchLocalSkill(t, f.lib.lib, name)
	scope := library.GlobalScope(library.AgentClaudeCode)
	if err := f.lib.lib.Install(t.Context(), skill.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	dir, err := f.lib.lib.ScopeDirectory(t.Context(), scope)
	if err != nil {
		t.Fatalf("ScopeDirectory: %v", err)
	}
	return filepath.Join(dir, name)
}

// quit 按 Wails 退出的顺序关闭两个服务，再做 PostShutdown 里的 afterShutdown。
func (f *uninstallFixture) quit(t *testing.T) {
	t.Helper()
	f.stop(t)
	f.app.removal.afterShutdown()
}

func (f *uninstallFixture) uninstall(t *testing.T, opts UninstallOptions) UninstallResult {
	t.Helper()
	result, err := f.app.UninstallApp(t.Context(), opts)
	if err != nil {
		t.Fatalf("UninstallApp(%+v): %v", opts, err)
	}
	return result
}

var errDenied = errors.New("permission denied")

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func TestUninstallAppKeepsInstalledFoldersByDefault(t *testing.T) {
	f := startUninstall(t)
	folder := f.install(t, "notes")

	if result := f.uninstall(t, UninstallOptions{}); !result.Uninstalled || f.quits.Load() != 1 {
		t.Fatalf("UninstallApp = %+v, quit %d times; want uninstalled and the app quitting", result, f.quits.Load())
	}
	// 退出前数据库还在，退出时技能库关上以后才删。
	if !exists(filepath.Join(f.dir, "library.db")) {
		t.Fatal("library deleted before shutdown")
	}
	f.quit(t)

	if exists(f.dir) {
		t.Fatal("app directory still there after uninstall")
	}
	if !exists(filepath.Join(folder, "SKILL.md")) || len(f.discarded) != 0 {
		t.Fatalf("installed folder should stay for the agent; discarded %v", f.discarded)
	}
}

func TestUninstallAppTrashesInstalledFoldersWhenAsked(t *testing.T) {
	f := startUninstall(t)
	notes, tools := f.install(t, "notes"), f.install(t, "tools")
	if err := os.WriteFile(filepath.Join(tools, "SKILL.md"), []byte("edited by hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	f.uninstall(t, UninstallOptions{Folders: FoldersTrash})
	slices.Sort(f.discarded)
	if want := []string{notes, tools}; !slices.Equal(f.discarded, want) {
		t.Fatalf("discarded %v; want %v", f.discarded, want)
	}
	f.quit(t)
	if exists(f.dir) {
		t.Fatal("app directory still there after uninstall")
	}
}

func TestUninstallAppKeepsModifiedFoldersWhenAsked(t *testing.T) {
	f := startUninstall(t)
	notes, tools := f.install(t, "notes"), f.install(t, "tools")
	if err := os.WriteFile(filepath.Join(tools, "SKILL.md"), []byte("edited by hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	f.uninstall(t, UninstallOptions{Folders: FoldersKeepModified})
	if !slices.Equal(f.discarded, []string{notes}) {
		t.Fatalf("discarded %v; want only the unmodified %s", f.discarded, notes)
	}
	if !exists(filepath.Join(tools, "SKILL.md")) {
		t.Fatal("modified folder should stay")
	}
}

func TestFailedUninstallKeepsDataAfterQuit(t *testing.T) {
	f := startUninstall(t)
	tools := f.install(t, "tools")
	f.app.removal.discard = func(string) error { return errDenied }

	if _, err := f.app.UninstallApp(t.Context(), UninstallOptions{Folders: FoldersTrash}); !errors.Is(err, errDenied) || f.quits.Load() != 0 {
		t.Fatalf("UninstallApp = %v, quit %d times; want the folder error and the app staying open", err, f.quits.Load())
	}
	f.quit(t)
	for _, path := range []string{filepath.Join(f.dir, "library.db"), tools} {
		if !exists(path) {
			t.Errorf("%s gone after a failed uninstall; want kept", path)
		}
	}
}

func TestUninstallAppRetrySkipsFoldersAlreadyTrashed(t *testing.T) {
	f := startUninstall(t)
	notes, tools := f.install(t, "notes"), f.install(t, "tools")
	trash := f.app.removal.discard
	fail := true
	f.app.removal.discard = func(path string) error {
		if path == tools && fail {
			return errDenied
		}
		return trash(path)
	}

	if _, err := f.app.UninstallApp(t.Context(), UninstallOptions{Folders: FoldersTrash}); !errors.Is(err, errDenied) {
		t.Fatalf("UninstallApp = %v; want the folder error", err)
	}
	if skills, err := f.lib.lib.ListSkills(t.Context()); err != nil || len(skills) != 2 {
		t.Fatalf("library after failed uninstall = %v, %v; want both skills kept", skills, err)
	}
	fail = false
	f.discarded = nil
	if result := f.uninstall(t, UninstallOptions{Folders: FoldersTrash}); !result.Uninstalled {
		t.Fatalf("retry = %+v; want uninstalled", result)
	}
	if !slices.Equal(f.discarded, []string{tools}) {
		t.Fatalf("retry discarded %v; want only %s (%s was already trashed)", f.discarded, tools, notes)
	}
}

func TestRemoveAppDataLeavesOtherFilesAlone(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"library.db", "library.db-wal", "notes.txt", "library-vacation.db", "library-v27.db.bak", "other-v27.db"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := removeAppData(filepath.Join(dir, "library.db")); err != nil {
		t.Fatalf("removeAppData: %v", err)
	}
	entries, err := os.ReadDir(dir)
	var left []string
	for _, entry := range entries {
		left = append(left, entry.Name())
	}
	if want := []string{"library-v27.db.bak", "library-vacation.db", "notes.txt", "other-v27.db"}; err != nil || !slices.Equal(left, want) {
		t.Fatalf("left in directory: %v, %v; want %v", left, err, want)
	}
}

// 升级前留下的备份就是整个技能库(含明文令牌与密钥),卸载时和数据库一起删掉,目录也随之删除。
func TestRemoveAppDataRemovesUpgradeBackups(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "mechbox")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"library.db", "library.db-wal", "library-v27.db", "library-v28.db", "library-v28.db-wal", "library-v29.db.123456.partial"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := removeAppData(filepath.Join(dir, "library.db")); err != nil {
		t.Fatalf("removeAppData: %v", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		entries, _ := os.ReadDir(dir)
		t.Fatalf("app data directory still there with %v: %v", entries, err)
	}
}
