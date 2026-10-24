package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/tiaot33/mechbox/internal/library"
)

// uninstallFixture 是在临时 HOME 里打开的技能库与应用设置服务：数据库和 settings.json 在同一个目录下(与真实布局一样),
// 钥匙串里存着一个令牌;移到废纸篓换成直接删除并记下删了哪些，不碰真实的废纸篓。
type uninstallFixture struct {
	lib       *LibraryService
	app       *AppService
	store     *memoryStore
	dir       string
	discarded []string
}

func startServices(t *testing.T) *uninstallFixture {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	f := &uninstallFixture{dir: filepath.Join(t.TempDir(), appDirName)}
	f.lib = NewLibraryService(filepath.Join(f.dir, "library.db"))
	if err := f.lib.ServiceStartup(t.Context(), application.ServiceOptions{}); err != nil {
		t.Fatalf("ServiceStartup: %v", err)
	}
	t.Cleanup(func() { _ = f.lib.ServiceShutdown() })
	f.store = &memoryStore{value: []byte(`{"github_token":"ghp_test"}`)}
	f.app = startWithStore(t, filepath.Join(f.dir, settingsFileName), f.lib, f.store)
	f.app.discard = func(path string) error {
		f.discarded = append(f.discarded, path)
		return os.RemoveAll(path)
	}
	// 先保存一次设置，让 settings.json 落盘。
	if _, err := f.app.SaveSettings(t.Context(), defaultSettings()); err != nil {
		t.Fatalf("SaveSettings: %v", err)
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

// quit 按 Wails 退出的顺序关闭两个服务再调 afterShutdown;lib.lib 置空，让 Cleanup 不再关一次。
func (f *uninstallFixture) quit(t *testing.T) {
	t.Helper()
	if err := f.app.ServiceShutdown(); err != nil {
		t.Fatalf("AppService shutdown: %v", err)
	}
	if err := f.lib.ServiceShutdown(); err != nil {
		t.Fatalf("LibraryService shutdown: %v", err)
	}
	f.lib.lib = nil
	f.app.afterShutdown()
}

func (f *uninstallFixture) uninstall(t *testing.T, opts UninstallOptions) UninstallResult {
	t.Helper()
	result, err := f.app.UninstallApp(t.Context(), opts)
	if err != nil {
		t.Fatalf("UninstallApp(%+v): %v", opts, err)
	}
	return result
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func TestUninstallAppKeepsInstalledFoldersByDefault(t *testing.T) {
	f := startServices(t)
	folder := f.install(t, "notes")

	if result := f.uninstall(t, UninstallOptions{}); !result.Uninstalled {
		t.Fatalf("UninstallApp = %+v; want uninstalled", result)
	}
	if f.store.value != nil {
		t.Fatalf("keychain item = %s; want deleted", f.store.value)
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
	f := startServices(t)
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
	f := startServices(t)
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

func TestUninstallAppStopsWhenKeychainRefusesUntilSkipped(t *testing.T) {
	f := startServices(t)
	folder := f.install(t, "notes")
	f.store.fail = map[string][]error{"delete": {errDenied, errDenied}}

	result := f.uninstall(t, UninstallOptions{Folders: FoldersTrash})
	if result.Uninstalled || result.KeychainError == "" {
		t.Fatalf("UninstallApp = %+v; want the keychain error and not uninstalled", result)
	}
	if !exists(folder) || f.store.secrets(t).GitHubToken != "ghp_test" {
		t.Fatalf("nothing should change when the keychain refuses; folder kept %v, keychain %s", exists(folder), f.store.value)
	}

	if result := f.uninstall(t, UninstallOptions{Folders: FoldersTrash, SkipKeychain: true}); !result.Uninstalled {
		t.Fatalf("UninstallApp with SkipKeychain = %+v; want uninstalled", result)
	}
	if exists(folder) {
		t.Fatal("folder should be trashed after skipping the keychain")
	}
}

func TestFailedUninstallKeepsDataAfterQuit(t *testing.T) {
	f := startServices(t)
	tools := f.install(t, "tools")
	f.app.discard = func(string) error { return errDenied }

	if _, err := f.app.UninstallApp(t.Context(), UninstallOptions{Folders: FoldersTrash}); !errors.Is(err, errDenied) {
		t.Fatalf("UninstallApp = %v; want the folder error", err)
	}
	f.quit(t)
	for _, path := range []string{filepath.Join(f.dir, "library.db"), filepath.Join(f.dir, settingsFileName), tools} {
		if !exists(path) {
			t.Errorf("%s gone after a failed uninstall; want kept", path)
		}
	}
}

func TestUninstallAppRetrySkipsFoldersAlreadyTrashed(t *testing.T) {
	f := startServices(t)
	notes, tools := f.install(t, "notes"), f.install(t, "tools")
	trash := f.app.discard
	fail := true
	f.app.discard = func(path string) error {
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

func TestUninstallAppRefusesToSaveCredentials(t *testing.T) {
	f := startServices(t)
	folder := f.install(t, "notes")
	var saveErr error
	trash := f.app.discard
	f.app.discard = func(path string) error {
		_, saveErr = f.app.SaveGitHubToken("ghp_late")
		return trash(path)
	}

	f.uninstall(t, UninstallOptions{Folders: FoldersTrash})
	if exists(folder) {
		t.Fatal("folder should be trashed")
	}
	if saveErr == nil || f.store.value != nil {
		t.Fatalf("SaveGitHubToken during uninstall = %v, keychain %s; want refused and the item gone", saveErr, f.store.value)
	}
}

func TestRemoveAppDataLeavesOtherFilesAlone(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"library.db", "library.db-wal", settingsFileName, "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := removeAppData(filepath.Join(dir, "library.db"), filepath.Join(dir, settingsFileName)); err != nil {
		t.Fatalf("removeAppData: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "notes.txt" {
		t.Fatalf("left in directory: %v, %v; want only notes.txt", entries, err)
	}
}
