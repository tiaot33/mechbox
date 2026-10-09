package library_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tiaot33/skills-tui/internal/library"
)

// updateLocalSkill 改写本地 Source 的 SKILL.md 并 Update,使已安装的文件夹变为 stale。
func updateLocalSkill(t *testing.T, lib *library.Library, id library.SkillID, source, body string) {
	t.Helper()
	writeLocalSkill(t, source, body)
	if result, err := lib.Update(t.Context(), id); err != nil || !result.Changed {
		t.Fatalf("Update(%d) = %#v, %v", id, result, err)
	}
}

func reinstall(t *testing.T, lib *library.Library, filter library.SkillScopeFilter, opts library.ReinstallOptions) []library.ReinstallResult {
	t.Helper()
	results, err := lib.Reinstall(t.Context(), filter, opts)
	if err != nil {
		t.Fatal(err)
	}
	return results
}

func requireReinstallResults(t *testing.T, got, want []library.ReinstallResult) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Reinstall() = %#v, want %#v", got, want)
	}
}

func reinstalledResult(id library.SkillID, scope library.Scope) library.ReinstallResult {
	return library.ReinstallResult{SkillID: id, Scope: scope, Outcome: library.Reinstalled}
}

func scopeFilter(scope library.Scope) library.SkillScopeFilter {
	return library.SkillScopeFilter{Scope: &scope}
}

func TestReinstallKeepsInstalls(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	groupOnly, groupOnlySource := fetchLocalSkill(t, lib, "group-only", "# group-only v1\n")
	directOnly, directOnlySource := fetchLocalSkill(t, lib, "direct-only", "# direct-only v1\n")
	both, bothSource := fetchLocalSkill(t, lib, "both", "# both v1\n")
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.ScopeDirectory(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	group, err := lib.CreateGroup(ctx, "g")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []library.SkillID{groupOnly.ID, both.ID} {
		if _, err := lib.AddSkillToGroup(ctx, group.ID, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := lib.InstallGroup(ctx, group.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []library.SkillID{directOnly.ID, both.ID} {
		if err := lib.Install(ctx, id, scope, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	updateLocalSkill(t, lib, groupOnly.ID, groupOnlySource, "# group-only v2\n")
	updateLocalSkill(t, lib, directOnly.ID, directOnlySource, "# direct-only v2\n")
	updateLocalSkill(t, lib, both.ID, bothSource, "# both v2\n")
	requireInspectionStatuses(t, lib, library.SkillScopeStale, library.SkillScopeStale, library.SkillScopeStale)
	before := listSkillScopes(t, lib)

	got := reinstall(t, lib, scopeFilter(scope), library.ReinstallOptions{})

	requireReinstallResults(t, got, []library.ReinstallResult{
		reinstalledResult(groupOnly.ID, scope), reinstalledResult(directOnly.ID, scope), reinstalledResult(both.ID, scope),
	})
	if after := listSkillScopes(t, lib); !reflect.DeepEqual(after, before) {
		t.Fatalf("ListSkillScopes after reinstall = %#v, want %#v", after, before)
	}
	requireInspectionStatuses(t, lib, library.SkillScopeCurrent, library.SkillScopeCurrent, library.SkillScopeCurrent)
	requireInstalledFolder(t, directory, groupOnly.Name, "# group-only v2\n")
	requireInstalledFolder(t, directory, directOnly.Name, "# direct-only v2\n")
	requireInstalledFolder(t, directory, both.Name, "# both v2\n")
	requireNoStagedEntries(t, directory)

	// 卸载组安装的效果与重新安装前一致:只由组安装留着的技能被卸载,有直接安装的保留。
	if err := lib.UninstallGroup(ctx, group.ID, scope); err != nil {
		t.Fatal(err)
	}
	want := []library.SkillScope{before[1], before[2]}
	want[1].Groups = nil
	if after := listSkillScopes(t, lib); !reflect.DeepEqual(after, want) {
		t.Fatalf("ListSkillScopes after UninstallGroup = %#v, want %#v", after, want)
	}
	if _, err := os.Lstat(filepath.Join(directory, groupOnly.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("group-only skill scope remains after UninstallGroup: %v", err)
	}
}

func TestReinstallBySkillAndByScope(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	updated, updatedSource := fetchLocalSkill(t, lib, "updated", "# updated v1\n")
	other, otherSource := fetchLocalSkill(t, lib, "other", "# other v1\n")
	codex := library.GlobalScope(library.AgentCodex)
	claude := library.GlobalScope(library.AgentClaudeCode)
	projectScope := library.ProjectScope(library.AgentCodex, project.ID)
	for _, scope := range []library.Scope{codex, claude, projectScope} {
		if err := lib.Install(ctx, updated.ID, scope, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	for _, scope := range []library.Scope{codex, claude} {
		if err := lib.Install(ctx, other.ID, scope, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	updateLocalSkill(t, lib, updated.ID, updatedSource, "# updated v2\n")
	updateLocalSkill(t, lib, other.ID, otherSource, "# other v2\n")

	// 只给技能:它在所有作用域的技能文件夹都跟上,顺序同 ListSkillScopes;另一个技能不受影响。
	got := reinstall(t, lib, library.SkillScopeFilter{SkillID: updated.ID}, library.ReinstallOptions{})
	requireReinstallResults(t, got, []library.ReinstallResult{
		reinstalledResult(updated.ID, claude), reinstalledResult(updated.ID, codex), reinstalledResult(updated.ID, projectScope),
	})
	requireInspectionStatuses(t, lib,
		library.SkillScopeCurrent, library.SkillScopeCurrent, library.SkillScopeCurrent,
		library.SkillScopeStale, library.SkillScopeStale)

	// 只给作用域:只影响该作用域;已是当前版本的报 current 且不写盘。
	got = reinstall(t, lib, scopeFilter(codex), library.ReinstallOptions{})
	requireReinstallResults(t, got, []library.ReinstallResult{
		{SkillID: updated.ID, Scope: codex, Outcome: library.ReinstallCurrent},
		reinstalledResult(other.ID, codex),
	})
	requireInspectionStatuses(t, lib,
		library.SkillScopeCurrent, library.SkillScopeCurrent, library.SkillScopeCurrent,
		library.SkillScopeStale, library.SkillScopeCurrent)

	// 两者都给:单条重新安装。
	got = reinstall(t, lib, library.SkillScopeFilter{SkillID: other.ID, Scope: &claude}, library.ReinstallOptions{})
	requireReinstallResults(t, got, []library.ReinstallResult{reinstalledResult(other.ID, claude)})
	requireInspectionStatuses(t, lib,
		library.SkillScopeCurrent, library.SkillScopeCurrent, library.SkillScopeCurrent,
		library.SkillScopeCurrent, library.SkillScopeCurrent)
}

func TestReinstallRefusesModifiedUnlessOverwrite(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	edited, _ := fetchLocalSkill(t, lib, "edited", "# edited v1\n")
	stale, staleSource := fetchLocalSkill(t, lib, "stale", "# stale v1\n")
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.ScopeDirectory(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []library.SkillID{edited.ID, stale.ID} {
		if err := lib.Install(ctx, id, scope, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	updateLocalSkill(t, lib, stale.ID, staleSource, "# stale v2\n")
	folder := filepath.Join(directory, edited.Name)
	writeFile(t, filepath.Join(folder, "SKILL.md"), []byte("# my edits\n"), 0o644)
	writeFile(t, filepath.Join(folder, "notes.txt"), []byte("mine"), 0o644)
	diskBefore := snapshotTree(t, folder)

	got, err := lib.Reinstall(ctx, scopeFilter(scope), library.ReinstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].SkillID != edited.ID || got[0].Outcome != library.ReinstallFailed || got[0].Scope != scope {
		t.Fatalf("Reinstall()[0] = %#v, want failed for edited", got)
	}
	requireErrorDetails(t, got[0].Error, library.FailedPrecondition, library.ErrorDetails{Path: folder})
	requireReinstallResults(t, got[1:], []library.ReinstallResult{reinstalledResult(stale.ID, scope)})
	if diskAfter := snapshotTree(t, folder); !reflect.DeepEqual(diskAfter, diskBefore) {
		t.Fatalf("modified skill scope changed: %#v, want %#v", diskAfter, diskBefore)
	}
	requireInspectionStatuses(t, lib, library.SkillScopeModified, library.SkillScopeCurrent)

	got = reinstall(t, lib, library.SkillScopeFilter{SkillID: edited.ID}, library.ReinstallOptions{OnConflict: library.Overwrite})
	requireReinstallResults(t, got, []library.ReinstallResult{reinstalledResult(edited.ID, scope)})
	requireInspectionStatuses(t, lib, library.SkillScopeCurrent, library.SkillScopeCurrent)
	if _, err := os.Lstat(filepath.Join(folder, "notes.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("overwrite kept stray file: %v", err)
	}
	requireNoStagedEntries(t, directory)
}

func TestReinstallWriteFailureRestoresItemAndContinues(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 不受目录权限限制")
	}
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	skill, source := fetchLocalSkill(t, lib, "example", "# example v1\n")
	writable := library.GlobalScope(library.AgentCodex)
	locked := library.GlobalScope(library.AgentClaudeCode)
	for _, scope := range []library.Scope{writable, locked} {
		if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	updateLocalSkill(t, lib, skill.ID, source, "# example v2\n")
	lockedDirectory, err := lib.ScopeDirectory(ctx, locked)
	if err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(lockedDirectory, skill.Name)
	diskBefore := snapshotTree(t, folder)
	// 目录只读:暂存无法创建,该条在写盘阶段失败。
	if err := os.Chmod(lockedDirectory, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(lockedDirectory, 0o755) })

	got := reinstall(t, lib, library.SkillScopeFilter{SkillID: skill.ID}, library.ReinstallOptions{})

	if len(got) != 2 {
		t.Fatalf("Reinstall() = %#v, want 2 results", got)
	}
	for _, result := range got {
		switch result.Scope {
		case writable:
			requireReinstallResults(t, []library.ReinstallResult{result}, []library.ReinstallResult{reinstalledResult(skill.ID, writable)})
		case locked:
			if result.Outcome != library.ReinstallFailed || result.Error == nil {
				t.Fatalf("Reinstall(locked) = %#v, want failed with error", result)
			}
		default:
			t.Fatalf("unexpected scope in %#v", result)
		}
	}
	if diskAfter := snapshotTree(t, folder); !reflect.DeepEqual(diskAfter, diskBefore) {
		t.Fatalf("failed skill scope changed: %#v, want %#v", diskAfter, diskBefore)
	}
	requireNoStagedEntries(t, lockedDirectory)
	statuses := map[library.Scope]library.SkillScopeStatus{}
	for _, inspection := range inspectSkillScopes(t, lib) {
		statuses[inspection.Scope] = inspection.Status
	}
	if statuses[writable] != library.SkillScopeCurrent || statuses[locked] != library.SkillScopeStale {
		t.Fatalf("statuses = %#v, want writable current and locked stale", statuses)
	}
}

func TestReinstallRecreatesMissingFolderAndDirectory(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	skill := fetchExampleSkill(t, lib, "example")
	codex := library.GlobalScope(library.AgentCodex)
	claude := library.GlobalScope(library.AgentClaudeCode)
	codexDirectory, err := lib.ScopeDirectory(ctx, codex)
	if err != nil {
		t.Fatal(err)
	}
	claudeDirectory, err := lib.ScopeDirectory(ctx, claude)
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []library.Scope{codex, claude} {
		if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.RemoveAll(filepath.Join(codexDirectory, skill.Name)); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(claudeDirectory); err != nil {
		t.Fatal(err)
	}
	requireInspectionStatuses(t, lib, library.SkillScopeDirectoryMissing, library.SkillScopeMissing)

	got := reinstall(t, lib, library.SkillScopeFilter{SkillID: skill.ID}, library.ReinstallOptions{})

	requireReinstallResults(t, got, []library.ReinstallResult{reinstalledResult(skill.ID, claude), reinstalledResult(skill.ID, codex)})
	requireInspectionStatuses(t, lib, library.SkillScopeCurrent, library.SkillScopeCurrent)
	requireInstalledFolder(t, claudeDirectory, skill.Name, "# example\n")
	requireInstalledFolder(t, codexDirectory, skill.Name, "# example\n")
}

func TestReinstallFailsWithoutCreatingMissingProjectRoot(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	root := filepath.Join(t.TempDir(), "project")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	project, err := lib.RegisterProject(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	skill := fetchNamedSkill(t, lib, "example")
	global := library.GlobalScope(library.AgentCodex)
	projectScope := library.ProjectScope(library.AgentCodex, project.ID)
	for _, scope := range []library.Scope{global, projectScope} {
		if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	globalDirectory, err := lib.ScopeDirectory(ctx, global)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(globalDirectory, skill.Name)); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(project.Path); err != nil {
		t.Fatal(err)
	}

	got := reinstall(t, lib, library.SkillScopeFilter{SkillID: skill.ID}, library.ReinstallOptions{})

	if len(got) != 2 {
		t.Fatalf("Reinstall() = %#v, want 2 results", got)
	}
	requireReinstallResults(t, got[:1], []library.ReinstallResult{reinstalledResult(skill.ID, global)})
	if got[1].SkillID != skill.ID || got[1].Scope != projectScope || got[1].Outcome != library.ReinstallFailed {
		t.Fatalf("Reinstall()[1] = %#v, want failed for project scope", got[1])
	}
	requireErrorDetails(t, got[1].Error, library.FailedPrecondition, library.ErrorDetails{Path: project.Path, ProjectID: project.ID})
	if _, err := os.Lstat(project.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("project root was created: %v", err)
	}
	requireInspectionStatuses(t, lib, library.SkillScopeCurrent, library.SkillScopeDirectoryMissing)
}

func TestReinstallIgnoresMembersSkippedByGroupInstall(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	installed, installedSource := fetchLocalSkill(t, lib, "installed", "# installed v1\n")
	skipped := fetchNamedSkill(t, lib, "skipped")
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.ScopeDirectory(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	// 目标里有未记录文件夹,组安装跳过该成员,它没有安装记录。
	writeFile(t, filepath.Join(directory, skipped.Name, "SKILL.md"), []byte("# foreign\n"), 0o644)
	group, err := lib.CreateGroup(ctx, "g")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []library.SkillID{installed.ID, skipped.ID} {
		if _, err := lib.AddSkillToGroup(ctx, group.ID, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := lib.InstallGroup(ctx, group.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	updateLocalSkill(t, lib, installed.ID, installedSource, "# installed v2\n")

	got := reinstall(t, lib, scopeFilter(scope), library.ReinstallOptions{OnConflict: library.Overwrite})
	requireReinstallResults(t, got, []library.ReinstallResult{reinstalledResult(installed.ID, scope)})
	requireSkillFile(t, directory, skipped.Name, "# foreign\n")

	// 按技能筛选一个没有安装的已知技能:空结果,不报错。
	if got := reinstall(t, lib, library.SkillScopeFilter{SkillID: skipped.ID}, library.ReinstallOptions{}); len(got) != 0 {
		t.Fatalf("Reinstall(skipped) = %#v, want empty", got)
	}
}

func TestReinstallRejectsInvalidFilters(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	skill := fetchNamedSkill(t, lib, "example")
	scope := library.GlobalScope(library.AgentCodex)
	if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	unknownAgent := library.GlobalScope("unknown-agent")
	unregistered := library.ProjectScope(library.AgentCodex, 999)
	globalWithProject := library.Scope{Kind: library.ScopeGlobal, Agent: library.AgentCodex, ProjectID: 1}
	idle := library.GlobalScope(library.AgentClaudeCode)
	tests := []struct {
		name   string
		filter library.SkillScopeFilter
		opts   library.ReinstallOptions
		code   library.ErrorCode
	}{
		{name: "no condition", code: library.InvalidArgument},
		{name: "unknown skill", filter: library.SkillScopeFilter{SkillID: 999}, code: library.NotFound},
		{name: "invalid scope", filter: library.SkillScopeFilter{Scope: &globalWithProject}, code: library.InvalidArgument},
		{name: "unknown agent", filter: library.SkillScopeFilter{Scope: &unknownAgent}, code: library.NotFound},
		{name: "unregistered project", filter: library.SkillScopeFilter{Scope: &unregistered}, code: library.NotFound},
		{name: "invalid policy", filter: library.SkillScopeFilter{SkillID: skill.ID}, opts: library.ReinstallOptions{OnConflict: "merge"}, code: library.InvalidArgument},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := lib.Reinstall(ctx, test.filter, test.opts)
			requireLibraryError(t, err, test.code)
			if len(got) != 0 {
				t.Fatalf("Reinstall() = %#v, want no results", got)
			}
		})
	}
	if got := reinstall(t, lib, scopeFilter(idle), library.ReinstallOptions{}); len(got) != 0 {
		t.Fatalf("Reinstall(idle scope) = %#v, want empty", got)
	}
}

// cancelWhen 在 cond 成立后报告已取消;Done 不关闭,只让 Library 在条目之间的检查点看到取消。
type cancelWhen struct {
	context.Context
	cond func() bool
}

func (c cancelWhen) Err() error {
	if c.cond() {
		return context.Canceled
	}
	return c.Context.Err()
}

func TestReinstallCancelReturnsCompletedItems(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	first, firstSource := fetchLocalSkill(t, lib, "first", "# first v1\n")
	second, secondSource := fetchLocalSkill(t, lib, "second", "# second v1\n")
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.ScopeDirectory(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []library.SkillID{first.ID, second.ID} {
		if err := lib.Install(ctx, id, scope, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	updateLocalSkill(t, lib, first.ID, firstSource, "# first v2\n")
	updateLocalSkill(t, lib, second.ID, secondSource, "# second v2\n")

	// 第一条提交完成(新内容已就位且备份已清理)后取消。
	cancelAfterFirst := cancelWhen{Context: ctx, cond: func() bool {
		content, err := os.ReadFile(filepath.Join(directory, first.Name, "SKILL.md"))
		if err != nil || string(content) != skillDocument(first.Name, "# first v2\n") {
			return false
		}
		entries, err := os.ReadDir(directory)
		if err != nil {
			return false
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".") {
				return false
			}
		}
		return true
	}}
	got, err := lib.Reinstall(cancelAfterFirst, scopeFilter(scope), library.ReinstallOptions{})
	requireLibraryError(t, err, library.Canceled)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Reinstall() error = %v, want context.Canceled in chain", err)
	}
	requireReinstallResults(t, got, []library.ReinstallResult{reinstalledResult(first.ID, scope)})
	requireInspectionStatuses(t, lib, library.SkillScopeCurrent, library.SkillScopeStale)

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	got, err = lib.Reinstall(canceled, scopeFilter(scope), library.ReinstallOptions{})
	requireLibraryError(t, err, library.Canceled)
	if len(got) != 0 {
		t.Fatalf("Reinstall(canceled) = %#v, want no results", got)
	}
	requireInspectionStatuses(t, lib, library.SkillScopeCurrent, library.SkillScopeStale)
}
