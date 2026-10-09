package library_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

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

func inspectSkillScopes(t *testing.T, lib *library.Library) []library.SkillScopeInspection {
	t.Helper()
	got, err := lib.InspectSkillScopes(t.Context(), library.SkillScopeFilter{})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func requireInspectionStatuses(t *testing.T, lib *library.Library, want ...library.SkillScopeStatus) {
	t.Helper()
	got := inspectSkillScopes(t, lib)
	if len(got) != len(want) {
		t.Fatalf("InspectSkillScopes() = %#v, want %d statuses %v", got, len(want), want)
	}
	for i := range want {
		if got[i].Status != want[i] {
			t.Fatalf("InspectSkillScopes()[%d].Status = %q, want %q (full %#v)", i, got[i].Status, want[i], got)
		}
	}
}

// requireInspection 断言唯一一处安装的状态与和磁盘一致的版本。
func requireInspection(t *testing.T, lib *library.Library, status library.SkillScopeStatus, versionID library.VersionID) {
	t.Helper()
	got := inspectSkillScopes(t, lib)
	if len(got) != 1 || got[0].Status != status || got[0].VersionID != versionID {
		t.Fatalf("InspectSkillScopes() = %#v, want one %q at version %d", got, status, versionID)
	}
}

func snapshotTree(t *testing.T, roots ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			out[path] = fmt.Sprintf("%s %q", info.Mode(), content)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func reinstall(t *testing.T, lib *library.Library, requests ...library.ReinstallRequest) []library.ReinstallResult {
	t.Helper()
	results, err := lib.Reinstall(t.Context(), requests)
	if err != nil {
		t.Fatal(err)
	}
	return results
}

// reinstallAt 是重新安装某个技能装在某个 Scope 的那一处、不同意覆盖的请求。
func reinstallAt(id library.SkillID, scope library.Scope) library.ReinstallRequest {
	return library.ReinstallRequest{SkillID: id, Scope: scope}
}

// inspectedRequests 按界面「全部勾选」的方式,把 filter 范围内巡检出的每一处转成重新安装请求。
func inspectedRequests(t *testing.T, lib *library.Library, filter library.SkillScopeFilter) []library.ReinstallRequest {
	t.Helper()
	inspections, err := lib.InspectSkillScopes(t.Context(), filter)
	if err != nil {
		t.Fatal(err)
	}
	var requests []library.ReinstallRequest
	for _, inspection := range inspections {
		requests = append(requests, reinstallAt(inspection.SkillID, inspection.Scope))
	}
	return requests
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

func TestInspectSkillScopesCurrentStaleModifiedAndInstallAgain(t *testing.T) {
	ctx := t.Context()
	source := filepath.Join(t.TempDir(), "example-skill")
	writeLocalSkill(t, source, "# v1\n")
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	skill, err := fetchLocalDir(ctx, lib, source)
	if err != nil {
		t.Fatal(err)
	}
	v1 := skill.VersionID
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.ScopeDirectory(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	requireInspection(t, lib, library.SkillScopeCurrent, v1)

	writeLocalSkill(t, source, "# v2\n")
	if result, err := lib.Update(ctx, skill.ID); err != nil || !result.Changed {
		t.Fatalf("Update = %#v, %v", result, err)
	}
	updated, err := lib.GetSkill(ctx, skill.ID)
	if err != nil {
		t.Fatal(err)
	}
	v2 := updated.VersionID
	// 落后时给出和磁盘一致的那个旧版本。
	requireInspection(t, lib, library.SkillScopeStale, v1)

	// 再次安装只补记选择,文件夹不动,仍是 stale;卸载后再次安装才放入当前版本。
	if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	requireInspection(t, lib, library.SkillScopeStale, v1)
	if err := lib.Uninstall(ctx, skill.ID, scope); err != nil {
		t.Fatal(err)
	}
	if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	requireInspection(t, lib, library.SkillScopeCurrent, v2)

	writeFile(t, filepath.Join(directory, skill.Name, "SKILL.md"), []byte(skillDocument(skill.Name, "# v1\n")), 0o644)
	requireInspection(t, lib, library.SkillScopeStale, v1)

	writeFile(t, filepath.Join(directory, skill.Name, "SKILL.md"), []byte("# neither\n"), 0o644)
	requireInspection(t, lib, library.SkillScopeModified, 0)
}

func TestInspectSkillScopesReportsModifiedForExtraFileAndExecutableBit(t *testing.T) {
	ctx := t.Context()
	source := filepath.Join(t.TempDir(), "example-skill")
	writeLocalSkill(t, source, "# Example\n")
	writeFile(t, filepath.Join(source, "scripts", "run.sh"), []byte("#!/bin/sh\n"), 0o755)
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	skill, err := fetchLocalDir(ctx, lib, source)
	if err != nil {
		t.Fatal(err)
	}
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.ScopeDirectory(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(directory, skill.Name)

	writeFile(t, filepath.Join(target, "extra.txt"), []byte("extra"), 0o644)
	requireInspectionStatuses(t, lib, library.SkillScopeModified)
	if err := os.Remove(filepath.Join(target, "extra.txt")); err != nil {
		t.Fatal(err)
	}
	requireInspectionStatuses(t, lib, library.SkillScopeCurrent)

	script := filepath.Join(target, "scripts", "run.sh")
	if err := os.Chmod(script, 0o644); err != nil {
		t.Fatal(err)
	}
	requireInspectionStatuses(t, lib, library.SkillScopeModified)
}

// 技能文件夹不在了、整个 Agent 目录不在了,对用户都是「文件夹不在了」,同为 missing。
func TestInspectSkillScopesReportsMissingWhenFolderOrAgentDirectoryRemoved(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	skill := fetchExampleSkill(t, lib, "example")
	claude := library.GlobalScope(library.AgentClaudeCode)
	codex := library.GlobalScope(library.AgentCodex)
	for _, scope := range []library.Scope{claude, codex} {
		if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	claudeDirectory, err := lib.ScopeDirectory(ctx, claude)
	if err != nil {
		t.Fatal(err)
	}
	codexDirectory, err := lib.ScopeDirectory(ctx, codex)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(claudeDirectory); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(codexDirectory, skill.Name)); err != nil {
		t.Fatal(err)
	}
	for _, inspection := range inspectSkillScopes(t, lib) {
		if inspection.Status != library.SkillScopeMissing || inspection.VersionID != 0 {
			t.Fatalf("InspectSkillScopes() = %#v, want missing without version", inspection)
		}
	}
	requireInspectionStatuses(t, lib, library.SkillScopeMissing, library.SkillScopeMissing)
}

// 项目不在这台机器上:作用域根目录不存在是 root_missing,与文件夹不在了区分开。
func TestInspectSkillScopesReportsRootMissingWhenProjectRootGone(t *testing.T) {
	for name, replace := range map[string]func(t *testing.T, root string){
		"deleted": func(t *testing.T, root string) {
			if err := os.RemoveAll(root); err != nil {
				t.Fatal(err)
			}
		},
		"replaced by file": func(t *testing.T, root string) {
			if err := os.RemoveAll(root); err != nil {
				t.Fatal(err)
			}
			writeFile(t, root, []byte("not a directory"), 0o644)
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			t.Setenv("HOME", t.TempDir())
			lib := openTestLibrary(t)
			projectRoot := filepath.Join(t.TempDir(), "project")
			if err := os.Mkdir(projectRoot, 0o755); err != nil {
				t.Fatal(err)
			}
			project, err := lib.RegisterProject(ctx, projectRoot)
			if err != nil {
				t.Fatal(err)
			}
			skill := fetchExampleSkill(t, lib, "example")
			scope := library.ProjectScope(library.AgentCodex, project.ID)
			if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
				t.Fatal(err)
			}
			replace(t, project.Path)
			requireInspection(t, lib, library.SkillScopeRootMissing, 0)
		})
	}
}

func TestInspectSkillScopesInspectsGroupSkillScopes(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	source := filepath.Join(t.TempDir(), "group-skill")
	writeLocalSkill(t, source, "# Group\n")
	skill, err := fetchLocalDir(ctx, lib, source)
	if err != nil {
		t.Fatal(err)
	}
	group, err := lib.CreateGroup(ctx, "shipping")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.AddSkillToGroup(ctx, group.ID, skill.ID); err != nil {
		t.Fatal(err)
	}
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.ScopeDirectory(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.InstallGroup(ctx, group.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	got := inspectSkillScopes(t, lib)
	want := []library.SkillScopeInspection{{
		SkillScope: library.SkillScope{
			SkillID: skill.ID,
			Scope:   scope,
			Folder:  filepath.Join(directory, skill.Name),
			Groups:  []library.GroupID{group.ID},
		},
		Status:    library.SkillScopeCurrent,
		VersionID: skill.VersionID,
	}}
	if len(got) == 1 {
		got[0].SkillScope.CreatedAt = time.Time{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("InspectSkillScopes() = %#v, want %#v", got, want)
	}
}

func TestInspectSkillScopesIsReadOnlyAndMatchesListSkillScopes(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	firstSource := filepath.Join(t.TempDir(), "first")
	secondSource := filepath.Join(t.TempDir(), "second")
	writeLocalSkill(t, firstSource, "# First\n")
	writeLocalSkill(t, secondSource, "# Second\n")
	first, err := fetchLocalDir(ctx, lib, firstSource)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fetchLocalDir(ctx, lib, secondSource)
	if err != nil {
		t.Fatal(err)
	}
	scopes := []library.Scope{
		library.GlobalScope(library.AgentClaudeCode),
		library.GlobalScope(library.AgentCodex),
	}
	var directories []string
	for _, skill := range []library.Skill{first, second} {
		for _, scope := range scopes {
			directory, err := lib.ScopeDirectory(ctx, scope)
			if err != nil {
				t.Fatal(err)
			}
			directories = append(directories, directory)
			if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
				t.Fatal(err)
			}
		}
	}
	before, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{})
	if err != nil {
		t.Fatal(err)
	}
	skillsBefore, err := lib.ListSkills(ctx)
	if err != nil {
		t.Fatal(err)
	}
	diskBefore := snapshotTree(t, directories...)

	got := inspectSkillScopes(t, lib)

	after, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{})
	if err != nil {
		t.Fatal(err)
	}
	skillsAfter, err := lib.ListSkills(ctx)
	if err != nil {
		t.Fatal(err)
	}
	diskAfter := snapshotTree(t, directories...)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("ListSkillScopes changed: %#v, want %#v", after, before)
	}
	if !reflect.DeepEqual(skillsAfter, skillsBefore) {
		t.Fatalf("ListSkills changed: %#v, want %#v", skillsAfter, skillsBefore)
	}
	if !reflect.DeepEqual(diskAfter, diskBefore) {
		t.Fatalf("disk changed: %#v, want %#v", diskAfter, diskBefore)
	}
	if len(got) != len(before) {
		t.Fatalf("InspectSkillScopes len=%d, ListSkillScopes len=%d: %#v", len(got), len(before), got)
	}
	for i := range before {
		if !reflect.DeepEqual(got[i].SkillScope, before[i]) {
			t.Fatalf("InspectSkillScopes()[%d].SkillScope = %#v, want %#v", i, got[i].SkillScope, before[i])
		}
		if got[i].Status != library.SkillScopeCurrent {
			t.Fatalf("InspectSkillScopes()[%d].Status = %q, want current", i, got[i].Status)
		}
	}
}

// 同一个筛选条件下,ListSkillScopes 与 InspectSkillScopes 列出的是同样几处,都是全库结果的子集。
func TestSkillScopeFilterNarrowsListAndInspection(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, firstSource := fetchLocalSkill(t, lib, "first", "# first v1\n")
	second := fetchNamedSkill(t, lib, "second")
	idle := fetchNamedSkill(t, lib, "idle")
	codex := library.GlobalScope(library.AgentCodex)
	claude := library.GlobalScope(library.AgentClaudeCode)
	projectScope := library.ProjectScope(library.AgentCodex, project.ID)
	for _, scope := range []library.Scope{codex, claude, projectScope} {
		if err := lib.Install(ctx, first.ID, scope, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	for _, scope := range []library.Scope{codex, projectScope} {
		if err := lib.Install(ctx, second.ID, scope, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	updateLocalSkill(t, lib, first.ID, firstSource, "# first v2\n")
	full := inspectSkillScopes(t, lib)
	if len(full) != 5 {
		t.Fatalf("InspectSkillScopes() = %#v, want 5", full)
	}

	subset := func(match func(library.SkillScopeInspection) bool) []library.SkillScopeInspection {
		var want []library.SkillScopeInspection
		for _, inspection := range full {
			if match(inspection) {
				want = append(want, inspection)
			}
		}
		return want
	}
	tests := []struct {
		name   string
		filter library.SkillScopeFilter
		want   []library.SkillScopeInspection
	}{
		{name: "skill", filter: library.SkillScopeFilter{SkillID: first.ID},
			want: subset(func(i library.SkillScopeInspection) bool { return i.SkillID == first.ID })},
		{name: "scope", filter: scopeFilter(projectScope),
			want: subset(func(i library.SkillScopeInspection) bool { return i.Scope == projectScope })},
		{name: "both", filter: library.SkillScopeFilter{SkillID: second.ID, Scope: &codex},
			want: subset(func(i library.SkillScopeInspection) bool { return i.SkillID == second.ID && i.Scope == codex })},
		{name: "no skill scopes", filter: library.SkillScopeFilter{SkillID: idle.ID}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := lib.InspectSkillScopes(ctx, test.filter)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(test.want) || (len(got) > 0 && !reflect.DeepEqual(got, test.want)) {
				t.Fatalf("InspectSkillScopes(%s) = %#v, want %#v", test.name, got, test.want)
			}
			listed, err := lib.ListSkillScopes(ctx, test.filter)
			if err != nil {
				t.Fatal(err)
			}
			var want []library.SkillScope
			for _, inspection := range test.want {
				want = append(want, inspection.SkillScope)
			}
			if !reflect.DeepEqual(listed, want) {
				t.Fatalf("ListSkillScopes(%s) = %#v, want %#v", test.name, listed, want)
			}
		})
	}

	unknownAgent := library.GlobalScope("unknown-agent")
	invalid := library.Scope{Kind: "nowhere", Agent: library.AgentCodex}
	for name, test := range map[string]struct {
		filter library.SkillScopeFilter
		code   library.ErrorCode
	}{
		"unknown skill": {filter: library.SkillScopeFilter{SkillID: 999}, code: library.NotFound},
		"unknown agent": {filter: library.SkillScopeFilter{Scope: &unknownAgent}, code: library.NotFound},
		"invalid scope": {filter: library.SkillScopeFilter{Scope: &invalid}, code: library.InvalidArgument},
	} {
		_, err := lib.InspectSkillScopes(ctx, test.filter)
		if !library.IsCode(err, test.code) {
			t.Fatalf("InspectSkillScopes(%s) error = %v, want %s", name, err, test.code)
		}
		_, err = lib.ListSkillScopes(ctx, test.filter)
		if !library.IsCode(err, test.code) {
			t.Fatalf("ListSkillScopes(%s) error = %v, want %s", name, err, test.code)
		}
	}
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

	got := reinstall(t, lib, inspectedRequests(t, lib, scopeFilter(scope))...)

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

// 只换列出的几处,顺序与请求一致;没列出的地方即使落后也不动。
func TestReinstallOnlyTheListedPlaces(t *testing.T) {
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

	got := reinstall(t, lib, reinstallAt(updated.ID, projectScope), reinstallAt(updated.ID, claude))
	requireReinstallResults(t, got, []library.ReinstallResult{
		reinstalledResult(updated.ID, projectScope), reinstalledResult(updated.ID, claude),
	})
	requireInspectionStatuses(t, lib,
		library.SkillScopeCurrent, library.SkillScopeStale, library.SkillScopeCurrent,
		library.SkillScopeStale, library.SkillScopeStale)

	// 本来就是当前版本的报 current,不动。
	got = reinstall(t, lib, reinstallAt(updated.ID, codex), reinstallAt(updated.ID, claude), reinstallAt(other.ID, codex))
	requireReinstallResults(t, got, []library.ReinstallResult{
		reinstalledResult(updated.ID, codex),
		{SkillID: updated.ID, Scope: claude, Outcome: library.ReinstallCurrent},
		reinstalledResult(other.ID, codex),
	})
	requireInspectionStatuses(t, lib,
		library.SkillScopeCurrent, library.SkillScopeCurrent, library.SkillScopeCurrent,
		library.SkillScopeStale, library.SkillScopeCurrent)

	if got := reinstall(t, lib); len(got) != 0 || got == nil {
		t.Fatalf("Reinstall() with no requests = %#v, want empty", got)
	}
}

// 改过的地方只在这一处同意覆盖时才换;同一次调用里,同意的那处换掉,没同意的那处保持原样并报 AlreadyExists。
func TestReinstallOverwritesModifiedOnlyWhereAgreed(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	kept, _ := fetchLocalSkill(t, lib, "kept", "# kept v1\n")
	overwritten, _ := fetchLocalSkill(t, lib, "overwritten", "# overwritten v1\n")
	stale, staleSource := fetchLocalSkill(t, lib, "stale", "# stale v1\n")
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.ScopeDirectory(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []library.SkillID{kept.ID, overwritten.ID, stale.ID} {
		if err := lib.Install(ctx, id, scope, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	updateLocalSkill(t, lib, stale.ID, staleSource, "# stale v2\n")
	keptFolder := filepath.Join(directory, kept.Name)
	overwrittenFolder := filepath.Join(directory, overwritten.Name)
	for _, folder := range []string{keptFolder, overwrittenFolder} {
		writeFile(t, filepath.Join(folder, "SKILL.md"), []byte("# my edits\n"), 0o644)
		writeFile(t, filepath.Join(folder, "notes.txt"), []byte("mine"), 0o644)
	}
	requireInspectionStatuses(t, lib, library.SkillScopeModified, library.SkillScopeModified, library.SkillScopeStale)
	diskBefore := snapshotTree(t, keptFolder)

	got := reinstall(t, lib,
		reinstallAt(kept.ID, scope),
		library.ReinstallRequest{SkillID: overwritten.ID, Scope: scope, Overwrite: true},
		reinstallAt(stale.ID, scope),
	)

	if len(got) != 3 || got[0].SkillID != kept.ID || got[0].Scope != scope || got[0].Outcome != library.ReinstallFailed {
		t.Fatalf("Reinstall() = %#v, want kept failed first", got)
	}
	requireErrorDetails(t, got[0].Error, library.AlreadyExists, library.ErrorDetails{Path: keptFolder})
	requireReinstallResults(t, got[1:], []library.ReinstallResult{reinstalledResult(overwritten.ID, scope), reinstalledResult(stale.ID, scope)})
	if diskAfter := snapshotTree(t, keptFolder); !reflect.DeepEqual(diskAfter, diskBefore) {
		t.Fatalf("modified folder without consent changed: %#v, want %#v", diskAfter, diskBefore)
	}
	requireInspectionStatuses(t, lib, library.SkillScopeModified, library.SkillScopeCurrent, library.SkillScopeCurrent)
	if _, err := os.Lstat(filepath.Join(overwrittenFolder, "notes.txt")); !errors.Is(err, os.ErrNotExist) {
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
	// 目录只读:暂存无法创建,这一处在放入文件夹时失败。
	if err := os.Chmod(lockedDirectory, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(lockedDirectory, 0o755) })

	got := reinstall(t, lib, reinstallAt(skill.ID, locked), reinstallAt(skill.ID, writable))

	if len(got) != 2 || got[0].Scope != locked || got[0].Outcome != library.ReinstallFailed || got[0].Error == nil {
		t.Fatalf("Reinstall() = %#v, want locked failed with error first", got)
	}
	requireReinstallResults(t, got[1:], []library.ReinstallResult{reinstalledResult(skill.ID, writable)})
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

func TestReinstallPutsBackMissingFolderAndAgentDirectory(t *testing.T) {
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
	requireInspectionStatuses(t, lib, library.SkillScopeMissing, library.SkillScopeMissing)

	got := reinstall(t, lib, inspectedRequests(t, lib, library.SkillScopeFilter{SkillID: skill.ID})...)

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
	requireInspectionStatuses(t, lib, library.SkillScopeMissing, library.SkillScopeRootMissing)

	got := reinstall(t, lib, inspectedRequests(t, lib, library.SkillScopeFilter{SkillID: skill.ID})...)

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
	requireInspectionStatuses(t, lib, library.SkillScopeCurrent, library.SkillScopeRootMissing)
}

// 请求里的某一处已经不装在那里(巡检之后被卸载、组安装时被跳过),或技能、Agent、项目已不在库里:
// 这一处报 NotFound、磁盘不动,其余照常。
func TestReinstallReportsPlacesNoLongerInstalled(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	installed, installedSource := fetchLocalSkill(t, lib, "installed", "# installed v1\n")
	uninstalled := fetchNamedSkill(t, lib, "uninstalled")
	skipped := fetchNamedSkill(t, lib, "skipped")
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.ScopeDirectory(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	// 目标里有不是从库里装的同名文件夹,组安装跳过该成员,它没有装在这里。
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
	if err := lib.Install(ctx, uninstalled.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	updateLocalSkill(t, lib, installed.ID, installedSource, "# installed v2\n")
	requests := inspectedRequests(t, lib, scopeFilter(scope))
	if err := lib.Uninstall(ctx, uninstalled.ID, scope); err != nil {
		t.Fatal(err)
	}
	unregistered := library.ProjectScope(library.AgentCodex, 999)
	unknownAgent := library.GlobalScope("unknown-agent")
	requests = append(requests,
		library.ReinstallRequest{SkillID: skipped.ID, Scope: scope, Overwrite: true},
		reinstallAt(999, scope),
		reinstallAt(installed.ID, unregistered),
		reinstallAt(installed.ID, unknownAgent),
	)

	got := reinstall(t, lib, requests...)

	want := []library.SkillID{installed.ID, uninstalled.ID, skipped.ID, 999, installed.ID, installed.ID}
	if len(got) != len(want) {
		t.Fatalf("Reinstall() = %#v, want %d results", got, len(want))
	}
	requireReinstallResults(t, got[:1], []library.ReinstallResult{reinstalledResult(installed.ID, scope)})
	for i, result := range got[1:] {
		if result.SkillID != want[i+1] || result.Scope != requests[i+1].Scope || result.Outcome != library.ReinstallFailed {
			t.Fatalf("Reinstall()[%d] = %#v, want failed", i+1, result)
		}
		requireLibraryError(t, result.Error, library.NotFound)
	}
	requireSkillFile(t, directory, skipped.Name, "# foreign\n")
	if _, err := os.Lstat(filepath.Join(directory, uninstalled.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("uninstalled folder was put back: %v", err)
	}
}

// 只有请求本身不合法时整次失败,一处都不动。
func TestReinstallRejectsInvalidRequests(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	skill, source := fetchLocalSkill(t, lib, "example", "# example v1\n")
	scope := library.GlobalScope(library.AgentCodex)
	if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	updateLocalSkill(t, lib, skill.ID, source, "# example v2\n")
	valid := reinstallAt(skill.ID, scope)
	tests := []struct {
		name    string
		request library.ReinstallRequest
	}{
		{name: "global scope with project", request: reinstallAt(skill.ID, library.Scope{Kind: library.ScopeGlobal, Agent: library.AgentCodex, ProjectID: 1})},
		{name: "project scope without project", request: reinstallAt(skill.ID, library.Scope{Kind: library.ScopeProject, Agent: library.AgentCodex})},
		{name: "unknown scope kind", request: reinstallAt(skill.ID, library.Scope{Kind: "nowhere", Agent: library.AgentCodex})},
		{name: "listed twice", request: library.ReinstallRequest{SkillID: skill.ID, Scope: scope, Overwrite: true}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := lib.Reinstall(ctx, []library.ReinstallRequest{valid, test.request})
			requireLibraryError(t, err, library.InvalidArgument)
			if len(got) != 0 {
				t.Fatalf("Reinstall() = %#v, want no results", got)
			}
			requireInspectionStatuses(t, lib, library.SkillScopeStale)
		})
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
	requests := []library.ReinstallRequest{reinstallAt(first.ID, scope), reinstallAt(second.ID, scope)}

	// 第一处换完(新内容已就位且备份已清理)后取消。
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
	got, err := lib.Reinstall(cancelAfterFirst, requests)
	requireLibraryError(t, err, library.Canceled)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Reinstall() error = %v, want context.Canceled in chain", err)
	}
	requireReinstallResults(t, got, []library.ReinstallResult{reinstalledResult(first.ID, scope)})
	requireInspectionStatuses(t, lib, library.SkillScopeCurrent, library.SkillScopeStale)

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	got, err = lib.Reinstall(canceled, requests)
	requireLibraryError(t, err, library.Canceled)
	if len(got) != 0 {
		t.Fatalf("Reinstall(canceled) = %#v, want no results", got)
	}
	requireInspectionStatuses(t, lib, library.SkillScopeCurrent, library.SkillScopeStale)
}
