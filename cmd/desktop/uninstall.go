package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/tiaot33/mechbox/cmd/desktop/internal/system"
	"github.com/tiaot33/mechbox/cmd/desktop/internal/trash"
	"github.com/tiaot33/mechbox/internal/library"
)

// UninstallFolders 是卸载 Mechbox 时，库装到各作用域的技能文件夹怎么处理。
type UninstallFolders string

const (
	FoldersKeep         UninstallFolders = "keep"          // 全部留在原处,Agent 照常使用，之后算作 Agent 目录里不是库装的技能(默认，空字符串同义)
	FoldersKeepModified UninstallFolders = "keep_modified" // 移到废纸篓，但用户改过的那几处留在原处
	FoldersTrash        UninstallFolders = "trash"         // 全部移到废纸篓
)

func (f UninstallFolders) valid() bool {
	return f == "" || f == FoldersKeep || f == FoldersKeepModified || f == FoldersTrash
}

// UninstallOptions 是卸载 Mechbox 的选项。
type UninstallOptions struct {
	Folders UninstallFolders `json:"folders"`
}

// UninstallResult 是卸载 Mechbox 的结果。
type UninstallResult struct {
	// Uninstalled 为 true 时卸载已经确认，应用随即退出。
	Uninstalled bool `json:"uninstalled"`
}

// uninstaller 是卸载 Mechbox:按选项移走装出去的技能文件夹，然后退出;退出时技能库关上以后，再删除数据库与系统给应用留下的数据。
type uninstaller struct {
	lib     *library.Library
	path    string
	discard func(path string) error
	quit    func()

	mu           sync.Mutex
	uninstalling bool
	uninstalled  bool
	removeOnce   sync.Once
}

func newUninstaller(lib *library.Library, path string, quit func()) *uninstaller {
	return &uninstaller{lib: lib, path: path, discard: trash.Discard, quit: quit}
}

// uninstall 见 AppService.UninstallApp。
func (u *uninstaller) uninstall(ctx context.Context, opts UninstallOptions) (UninstallResult, error) {
	if !opts.Folders.valid() {
		return UninstallResult{}, fmt.Errorf("unknown folders option %q", opts.Folders)
	}
	u.mu.Lock()
	if u.uninstalling {
		u.mu.Unlock()
		return UninstallResult{}, errors.New("uninstall already in progress")
	}
	u.uninstalling = true
	u.mu.Unlock()
	if opts.Folders != "" && opts.Folders != FoldersKeep {
		if err := u.trashFolders(ctx, opts.Folders == FoldersKeepModified); err != nil {
			u.mu.Lock()
			u.uninstalling = false
			u.mu.Unlock()
			return UninstallResult{}, err
		}
	}
	u.mu.Lock()
	u.uninstalled = true
	u.mu.Unlock()
	u.quit()
	return UninstallResult{Uninstalled: true}, nil
}

func (u *uninstaller) trashFolders(ctx context.Context, keepModified bool) error {
	places, err := u.lib.InspectSkillScopes(ctx, library.SkillScopeFilter{})
	if err != nil {
		return err
	}
	done := map[string]bool{}
	for _, place := range places {
		switch {
		case done[place.Folder], place.Status == library.SkillScopeRootMissing, place.Status == library.SkillScopeMissing:
			continue
		case keepModified && place.Status == library.SkillScopeModified:
			continue
		}
		if err := u.discard(place.Folder); err != nil {
			return err
		}
		done[place.Folder] = true
	}
	return nil
}

// afterShutdown 在各服务关闭、技能库关上以后删数据库与系统给应用留下的数据;只在卸载确认了时做，只做一遍。
func (u *uninstaller) afterShutdown() {
	u.mu.Lock()
	uninstalled := u.uninstalled
	u.mu.Unlock()
	if !uninstalled {
		return
	}
	u.removeOnce.Do(func() {
		if err := errors.Join(removeAppData(u.path), system.RemoveFromSystem(bundleID)); err != nil {
			fmt.Fprintln(os.Stderr, "uninstall:", err)
		}
	})
}

func removeAppData(libraryPath string) error {
	var errs []error
	paths := []string{libraryPath, libraryPath + "-wal", libraryPath + "-shm", libraryPath + "-journal"}
	backups, err := upgradeBackups(libraryPath)
	if err != nil {
		errs = append(errs, err)
	}
	for _, path := range append(paths, backups...) {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	_ = os.Remove(filepath.Dir(libraryPath))
	return errors.Join(errs...)
}

func upgradeBackups(libraryPath string) ([]string, error) {
	dir, name := filepath.Split(libraryPath)
	ext := filepath.Ext(name)
	pattern := regexp.MustCompile(`^` + regexp.QuoteMeta(strings.TrimSuffix(name, ext)) + `-v[0-9]+` + regexp.QuoteMeta(ext) + `(-wal|-shm|-journal|\..+\.partial)?$`)
	entries, err := os.ReadDir(filepath.Clean(dir))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	var backups []string
	for _, entry := range entries {
		if pattern.MatchString(entry.Name()) {
			backups = append(backups, filepath.Join(dir, entry.Name()))
		}
	}
	return backups, nil
}
