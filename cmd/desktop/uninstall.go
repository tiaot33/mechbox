package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

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

// UninstallApp 卸载 Mechbox：按选项把装出去的技能文件夹移到废纸篓
// (先现查各处状态：已经不在的、在不在这台机器上的项目里的跳过，选了保留改过的时跳过用户改过的),然后退出;
// 退出时技能库关上以后，再删除数据库(连同存在里面的设置、令牌与同步设置)与系统给应用留下的数据，
// 并把应用移到废纸篓(移不动时在 Finder 中选中它)。
// 移文件夹中途失败时返回错误、不退出，数据库还在，已经移走的那几处之后巡检为文件夹不在了，重试时跳过。S3 上的备份不动。
func (a *AppService) UninstallApp(ctx context.Context, opts UninstallOptions) (UninstallResult, error) {
	if !opts.Folders.valid() {
		return UninstallResult{}, fmt.Errorf("unknown folders option %q", opts.Folders)
	}
	a.mu.Lock()
	if a.uninstalling {
		a.mu.Unlock()
		return UninstallResult{}, errors.New("uninstall already in progress")
	}
	a.uninstalling = true
	a.mu.Unlock()
	if opts.Folders != "" && opts.Folders != FoldersKeep {
		if err := a.trashFolders(ctx, opts.Folders == FoldersKeepModified); err != nil {
			a.stopUninstalling()
			return UninstallResult{}, err
		}
	}
	a.mu.Lock()
	a.uninstalled = true
	a.mu.Unlock()
	if a.app != nil {
		// 在另一个 goroutine 里退出，这次调用先返回给前端。
		go a.app.Quit()
	}
	return UninstallResult{Uninstalled: true}, nil
}

func (a *AppService) stopUninstalling() {
	a.mu.Lock()
	a.uninstalling = false
	a.mu.Unlock()
}

func (a *AppService) trashFolders(ctx context.Context, keepModified bool) error {
	places, err := a.library.lib.InspectSkillScopes(ctx, library.SkillScopeFilter{})
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
		if err := a.discard(place.Folder); err != nil {
			return err
		}
		done[place.Folder] = true
	}
	return nil
}

func (a *AppService) afterShutdown() {
	a.mu.Lock()
	uninstalled := a.uninstalled
	a.mu.Unlock()
	if !uninstalled {
		return
	}
	a.removeOnce.Do(func() {
		if err := errors.Join(removeAppData(a.library.path), removeFromSystem()); err != nil {
			fmt.Fprintln(os.Stderr, "uninstall:", err)
		}
	})
}

func removeAppData(libraryPath string) error {
	var errs []error
	for _, path := range []string{libraryPath, libraryPath + "-wal", libraryPath + "-shm", libraryPath + "-journal"} {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	_ = os.Remove(filepath.Dir(libraryPath))
	return errors.Join(errs...)
}
