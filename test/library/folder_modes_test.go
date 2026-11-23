//go:build !windows

package library_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tiaot33/mechbox/internal/library"
)

// 经应用安装、更新、回滚写出的技能文件夹，最外层和里面的子目录权限一样(默认 umask 下都是 755),
// 和用 npx skills、git clone 或手动拷贝放进 Agent 目录的一致，同一台机器上的其他用户也读得到。
// 改 Agent 目录时迁移过去的文件夹保留原来的权限。

func requireFolderModeLikeSubfolder(t *testing.T, folder string) {
	t.Helper()
	outer, err := os.Stat(folder)
	if err != nil {
		t.Fatal(err)
	}
	inner, err := os.Stat(filepath.Join(folder, "scripts"))
	if err != nil {
		t.Fatal(err)
	}
	if outer.Mode().Perm() != inner.Mode().Perm() || outer.Mode().Perm()&0o055 != 0o055 {
		t.Fatalf("%s mode = %v, scripts = %v; want the folder as open as its subfolders (755 under umask 022)", folder, outer.Mode().Perm(), inner.Mode().Perm())
	}
}

func requireFolderMode(t *testing.T, folder string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(folder)
	if err != nil || info.Mode().Perm() != want {
		t.Fatalf("%s = %v, %v; want mode %v", folder, info, err, want)
	}
}

// installUpdateRollback 从 source 入库一个带 scripts/ 的技能，装到 Claude Code 全局，改来源后更新并连带重新安装那一处，
// 再回滚那次更新;每一步都核对 folder 的最外层权限。
func installUpdateRollback(t *testing.T, lib *library.Library, source, sourceInput, folder string) {
	t.Helper()
	ctx := t.Context()
	writeLocalSkill(t, source, "v1")
	writeFile(t, filepath.Join(source, "scripts", "run.sh"), []byte("#!/bin/sh\n"), 0o755)
	skill, err := fetchLocalDir(ctx, lib, sourceInput)
	if err != nil {
		t.Fatal(err)
	}
	claude := library.GlobalScope(library.AgentClaudeCode)
	if err := lib.Install(ctx, skill.ID, claude, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	requireFolderModeLikeSubfolder(t, folder)

	writeLocalSkill(t, source, "v2")
	request := mustPlanUpdate(t, lib, skill.ID)
	request.Installs = []library.ReinstallRequest{{SkillID: skill.ID, Scope: claude}}
	result, err := lib.Update(ctx, request)
	if err != nil || len(result.Installs) != 1 || result.Installs[0].Outcome != library.Reinstalled {
		t.Fatalf("Update = %#v, %v", result, err)
	}
	requireFolderModeLikeSubfolder(t, folder)

	rollback, err := lib.Rollback(ctx, result.Record)
	if err != nil || len(rollback.Installs) != 1 || rollback.Installs[0].Outcome != library.RollbackReinstalled {
		t.Fatalf("Rollback = %#v, %v", rollback, err)
	}
	requireFolderModeLikeSubfolder(t, folder)
	requireNoStagedEntries(t, filepath.Dir(folder))
}

func TestInstallUpdateRollbackWriteFoldersAsOpenAsTheirSubfolders(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	setTestHome(t, home)
	lib := openTestLibrary(t)
	source := filepath.Join(t.TempDir(), "runner")
	installUpdateRollback(t, lib, source, source, filepath.Join(scopeDirectory(t, lib, library.GlobalScope(library.AgentClaudeCode)), "runner"))
}

func TestInstallUpdateRollbackWithoutFileModesWriteFoldersAsOpenAsTheirSubfolders(t *testing.T) {
	setTestHome(t, t.TempDir())
	lib := openTestLibrary(t)
	withoutFileModes(t)
	source := filepath.Join(t.TempDir(), "runner")
	installUpdateRollback(t, lib, source, source, filepath.Join(scopeDirectory(t, lib, library.GlobalScope(library.AgentClaudeCode)), "runner"))
}

func TestWSLDistroInstallUpdateRollbackWriteFoldersAsOpenAsTheirSubfolders(t *testing.T) {
	lib, home := openWSLLibrary(t)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	source, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source = filepath.Join(source, "runner")
	installUpdateRollback(t, lib, source, wslPath(t, source), filepath.Join(home, ".claude/skills/runner"))
}

// 旧版装出的 700 文件夹和用户自己设成 750 的文件夹，迁移到新目录后都还是原来的权限。
func TestEditAgentMigrationKeepsFolderModes(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	lib, agents, cursor := cursorHome(t)
	global := library.GlobalScope("cursor")
	modes := map[string]os.FileMode{"private": 0o700, "group": 0o750}
	for name, mode := range modes {
		folder := writeUnmanagedSkill(t, cursor, name, name)
		if err := os.Chmod(folder, mode); err != nil {
			t.Fatal(err)
		}
		if _, err := lib.FetchUnmanaged(ctx, library.FetchUnmanagedRequest{Scope: global, Directory: cursor, Name: name}); err != nil {
			t.Fatal(err)
		}
	}
	result, err := lib.EditAgent(ctx, "cursor", library.AgentInput{
		Name: "Cursor", GlobalDirectories: []string{agents}, ProjectDirectories: []string{".agents/skills"}, DetectDirectories: []string{"~/.cursor"},
	}, library.EditAgentOptions{Migrate: true})
	if err != nil || len(result.Migrations) != len(modes) {
		t.Fatalf("EditAgent = %#v, %v; want both folders migrated", result, err)
	}
	for name, mode := range modes {
		requireNoInstalledFolder(t, cursor, name)
		requireFolderMode(t, filepath.Join(agents, name), mode)
	}
}
