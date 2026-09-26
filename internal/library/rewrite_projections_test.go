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

// installLocalSkill 从独立的本地 Source 安装 skill,并返回 Source 目录,供之后改内容再 Update。
func installLocalSkill(t *testing.T, lib library.Library, name, body string) (library.Skill, string) {
	t.Helper()
	source := filepath.Join(t.TempDir(), name)
	writeFile(t, filepath.Join(source, "SKILL.md"), []byte(body), 0o644)
	skill, err := lib.Install(t.Context(), library.LocalSource{Path: source})
	if err != nil {
		t.Fatal(err)
	}
	return skill, source
}

// updateLocalSkill 改写本地 Source 的 SKILL.md 并 Update,使已有投影变为 stale。
func updateLocalSkill(t *testing.T, lib library.Library, id library.SkillID, source, body string) {
	t.Helper()
	writeFile(t, filepath.Join(source, "SKILL.md"), []byte(body), 0o644)
	if result, err := lib.Update(t.Context(), id); err != nil || !result.Changed {
		t.Fatalf("Update(%d) = %#v, %v", id, result, err)
	}
}

func rewriteProjections(t *testing.T, lib library.Library, filter library.ProjectionFilter, opts library.RewriteOptions) []library.RewriteResult {
	t.Helper()
	results, err := lib.RewriteProjections(t.Context(), filter, opts)
	if err != nil {
		t.Fatal(err)
	}
	return results
}

func requireRewriteResults(t *testing.T, got, want []library.RewriteResult) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RewriteProjections() = %#v, want %#v", got, want)
	}
}

func rewritten(id library.SkillID, scope library.Scope) library.RewriteResult {
	return library.RewriteResult{SkillID: id, Scope: scope, Outcome: library.Rewritten}
}

func scopeFilter(scope library.Scope) library.ProjectionFilter {
	return library.ProjectionFilter{Scope: &scope}
}

func listProjections(t *testing.T, lib library.Library) []library.Projection {
	t.Helper()
	projections, err := lib.ListProjections(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return projections
}

func TestRewriteProjectionsKeepsActivations(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	groupOnly, groupOnlySource := installLocalSkill(t, lib, "group-only", "# group-only v1\n")
	directOnly, directOnlySource := installLocalSkill(t, lib, "direct-only", "# direct-only v1\n")
	both, bothSource := installLocalSkill(t, lib, "both", "# both v1\n")
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.Resolve(ctx, scope)
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
	if _, err := lib.ActivateGroup(ctx, group.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []library.SkillID{directOnly.ID, both.ID} {
		if err := lib.Activate(ctx, id, scope, library.ActivateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	updateLocalSkill(t, lib, groupOnly.ID, groupOnlySource, "# group-only v2\n")
	updateLocalSkill(t, lib, directOnly.ID, directOnlySource, "# direct-only v2\n")
	updateLocalSkill(t, lib, both.ID, bothSource, "# both v2\n")
	requireInspectionStatuses(t, lib, library.ProjectionStale, library.ProjectionStale, library.ProjectionStale)
	before := listProjections(t, lib)

	got := rewriteProjections(t, lib, scopeFilter(scope), library.RewriteOptions{})

	requireRewriteResults(t, got, []library.RewriteResult{
		rewritten(groupOnly.ID, scope), rewritten(directOnly.ID, scope), rewritten(both.ID, scope),
	})
	if after := listProjections(t, lib); !reflect.DeepEqual(after, before) {
		t.Fatalf("ListProjections after rewrite = %#v, want %#v", after, before)
	}
	requireInspectionStatuses(t, lib, library.ProjectionCurrent, library.ProjectionCurrent, library.ProjectionCurrent)
	requireProjection(t, directory, groupOnly.Name, "# group-only v2\n")
	requireProjection(t, directory, directOnly.Name, "# direct-only v2\n")
	requireProjection(t, directory, both.Name, "# both v2\n")
	requireNoStagedEntries(t, directory)

	// 停用组激活的效果与重写前一致:只由组激活撑着的投影被移除,有直接激活的保留。
	if err := lib.DeactivateGroup(ctx, group.ID, scope); err != nil {
		t.Fatal(err)
	}
	want := []library.Projection{before[1], before[2]}
	want[1].Groups = nil
	if after := listProjections(t, lib); !reflect.DeepEqual(after, want) {
		t.Fatalf("ListProjections after DeactivateGroup = %#v, want %#v", after, want)
	}
	if _, err := os.Lstat(filepath.Join(directory, groupOnly.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("group-only projection remains after DeactivateGroup: %v", err)
	}
}

func TestRewriteProjectionsBySkillAndByScope(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	updated, updatedSource := installLocalSkill(t, lib, "updated", "# updated v1\n")
	other, otherSource := installLocalSkill(t, lib, "other", "# other v1\n")
	codex := library.GlobalScope(library.AgentCodex)
	claude := library.GlobalScope(library.AgentClaudeCode)
	projectScope := library.ProjectScope(library.AgentCodex, project.ID)
	for _, scope := range []library.Scope{codex, claude, projectScope} {
		if err := lib.Activate(ctx, updated.ID, scope, library.ActivateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	for _, scope := range []library.Scope{codex, claude} {
		if err := lib.Activate(ctx, other.ID, scope, library.ActivateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	updateLocalSkill(t, lib, updated.ID, updatedSource, "# updated v2\n")
	updateLocalSkill(t, lib, other.ID, otherSource, "# other v2\n")

	// 只给技能:它在所有作用域的投影都跟上,顺序同 ListProjections;另一个技能不受影响。
	got := rewriteProjections(t, lib, library.ProjectionFilter{SkillID: updated.ID}, library.RewriteOptions{})
	requireRewriteResults(t, got, []library.RewriteResult{
		rewritten(updated.ID, claude), rewritten(updated.ID, codex), rewritten(updated.ID, projectScope),
	})
	requireInspectionStatuses(t, lib,
		library.ProjectionCurrent, library.ProjectionCurrent, library.ProjectionCurrent,
		library.ProjectionStale, library.ProjectionStale)

	// 只给作用域:只影响该作用域;已是当前版本的报 current 且不写盘。
	got = rewriteProjections(t, lib, scopeFilter(codex), library.RewriteOptions{})
	requireRewriteResults(t, got, []library.RewriteResult{
		{SkillID: updated.ID, Scope: codex, Outcome: library.RewriteCurrent},
		rewritten(other.ID, codex),
	})
	requireInspectionStatuses(t, lib,
		library.ProjectionCurrent, library.ProjectionCurrent, library.ProjectionCurrent,
		library.ProjectionStale, library.ProjectionCurrent)

	// 两者都给:单条重写。
	got = rewriteProjections(t, lib, library.ProjectionFilter{SkillID: other.ID, Scope: &claude}, library.RewriteOptions{})
	requireRewriteResults(t, got, []library.RewriteResult{rewritten(other.ID, claude)})
	requireInspectionStatuses(t, lib,
		library.ProjectionCurrent, library.ProjectionCurrent, library.ProjectionCurrent,
		library.ProjectionCurrent, library.ProjectionCurrent)
}

func TestRewriteProjectionsRefusesModifiedUnlessOverwrite(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	edited, _ := installLocalSkill(t, lib, "edited", "# edited v1\n")
	stale, staleSource := installLocalSkill(t, lib, "stale", "# stale v1\n")
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.Resolve(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []library.SkillID{edited.ID, stale.ID} {
		if err := lib.Activate(ctx, id, scope, library.ActivateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	updateLocalSkill(t, lib, stale.ID, staleSource, "# stale v2\n")
	folder := filepath.Join(directory, edited.Name)
	writeFile(t, filepath.Join(folder, "SKILL.md"), []byte("# my edits\n"), 0o644)
	writeFile(t, filepath.Join(folder, "notes.txt"), []byte("mine"), 0o644)
	diskBefore := snapshotTree(t, folder)

	got, err := lib.RewriteProjections(ctx, scopeFilter(scope), library.RewriteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].SkillID != edited.ID || got[0].Outcome != library.RewriteFailed || got[0].Scope != scope {
		t.Fatalf("RewriteProjections()[0] = %#v, want failed for edited", got)
	}
	requireErrorDetails(t, got[0].Error, library.FailedPrecondition, library.ErrorDetails{Path: folder})
	requireRewriteResults(t, got[1:], []library.RewriteResult{rewritten(stale.ID, scope)})
	if diskAfter := snapshotTree(t, folder); !reflect.DeepEqual(diskAfter, diskBefore) {
		t.Fatalf("modified projection changed: %#v, want %#v", diskAfter, diskBefore)
	}
	requireInspectionStatuses(t, lib, library.ProjectionModified, library.ProjectionCurrent)

	got = rewriteProjections(t, lib, library.ProjectionFilter{SkillID: edited.ID}, library.RewriteOptions{OnConflict: library.Overwrite})
	requireRewriteResults(t, got, []library.RewriteResult{rewritten(edited.ID, scope)})
	requireInspectionStatuses(t, lib, library.ProjectionCurrent, library.ProjectionCurrent)
	if _, err := os.Lstat(filepath.Join(folder, "notes.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("overwrite kept stray file: %v", err)
	}
	requireNoStagedEntries(t, directory)
}

func TestRewriteProjectionsRecreatesMissingFolderAndDirectory(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	skill := installExampleSkill(t, lib, "example")
	codex := library.GlobalScope(library.AgentCodex)
	claude := library.GlobalScope(library.AgentClaudeCode)
	codexDirectory, err := lib.Resolve(ctx, codex)
	if err != nil {
		t.Fatal(err)
	}
	claudeDirectory, err := lib.Resolve(ctx, claude)
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []library.Scope{codex, claude} {
		if err := lib.Activate(ctx, skill.ID, scope, library.ActivateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.RemoveAll(filepath.Join(codexDirectory, skill.Name)); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(claudeDirectory); err != nil {
		t.Fatal(err)
	}
	requireInspectionStatuses(t, lib, library.ProjectionDirectoryMissing, library.ProjectionMissing)

	got := rewriteProjections(t, lib, library.ProjectionFilter{SkillID: skill.ID}, library.RewriteOptions{})

	requireRewriteResults(t, got, []library.RewriteResult{rewritten(skill.ID, claude), rewritten(skill.ID, codex)})
	requireInspectionStatuses(t, lib, library.ProjectionCurrent, library.ProjectionCurrent)
	requireProjection(t, claudeDirectory, skill.Name, "# example\n")
	requireProjection(t, codexDirectory, skill.Name, "# example\n")
}

func TestRewriteProjectionsFailsWithoutCreatingMissingProjectRoot(t *testing.T) {
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
	skill := installNamedSkill(t, lib, "example")
	global := library.GlobalScope(library.AgentCodex)
	projectScope := library.ProjectScope(library.AgentCodex, project.ID)
	for _, scope := range []library.Scope{global, projectScope} {
		if err := lib.Activate(ctx, skill.ID, scope, library.ActivateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	globalDirectory, err := lib.Resolve(ctx, global)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(globalDirectory, skill.Name)); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(project.Path); err != nil {
		t.Fatal(err)
	}

	got := rewriteProjections(t, lib, library.ProjectionFilter{SkillID: skill.ID}, library.RewriteOptions{})

	if len(got) != 2 {
		t.Fatalf("RewriteProjections() = %#v, want 2 results", got)
	}
	requireRewriteResults(t, got[:1], []library.RewriteResult{rewritten(skill.ID, global)})
	if got[1].SkillID != skill.ID || got[1].Scope != projectScope || got[1].Outcome != library.RewriteFailed {
		t.Fatalf("RewriteProjections()[1] = %#v, want failed for project scope", got[1])
	}
	requireErrorDetails(t, got[1].Error, library.FailedPrecondition, library.ErrorDetails{Path: project.Path, ProjectID: project.ID})
	if _, err := os.Lstat(project.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("project root was created: %v", err)
	}
	requireInspectionStatuses(t, lib, library.ProjectionCurrent, library.ProjectionDirectoryMissing)
}

func TestRewriteProjectionsIgnoresMembersSkippedByGroupActivation(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	projected, projectedSource := installLocalSkill(t, lib, "projected", "# projected v1\n")
	skipped := installNamedSkill(t, lib, "skipped")
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.Resolve(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	// 目标里有未记录文件夹,组激活跳过该成员,它没有投影记录。
	writeFile(t, filepath.Join(directory, skipped.Name, "SKILL.md"), []byte("# foreign\n"), 0o644)
	group, err := lib.CreateGroup(ctx, "g")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []library.SkillID{projected.ID, skipped.ID} {
		if _, err := lib.AddSkillToGroup(ctx, group.ID, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := lib.ActivateGroup(ctx, group.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	updateLocalSkill(t, lib, projected.ID, projectedSource, "# projected v2\n")

	got := rewriteProjections(t, lib, scopeFilter(scope), library.RewriteOptions{OnConflict: library.Overwrite})
	requireRewriteResults(t, got, []library.RewriteResult{rewritten(projected.ID, scope)})
	requireProjection(t, directory, skipped.Name, "# foreign\n")

	// 按技能筛选一个没有投影的已知技能:空结果,不报错。
	if got := rewriteProjections(t, lib, library.ProjectionFilter{SkillID: skipped.ID}, library.RewriteOptions{}); len(got) != 0 {
		t.Fatalf("RewriteProjections(skipped) = %#v, want empty", got)
	}
}

func TestRewriteProjectionsRejectsInvalidFilters(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	skill := installNamedSkill(t, lib, "example")
	scope := library.GlobalScope(library.AgentCodex)
	if err := lib.Activate(ctx, skill.ID, scope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	unknownAgent := library.GlobalScope("unknown-agent")
	unregistered := library.ProjectScope(library.AgentCodex, 999)
	globalWithProject := library.Scope{Kind: library.ScopeGlobal, Agent: library.AgentCodex, ProjectID: 1}
	idle := library.GlobalScope(library.AgentClaudeCode)
	tests := []struct {
		name   string
		filter library.ProjectionFilter
		opts   library.RewriteOptions
		code   library.ErrorCode
	}{
		{name: "no condition", code: library.InvalidArgument},
		{name: "unknown skill", filter: library.ProjectionFilter{SkillID: 999}, code: library.NotFound},
		{name: "invalid scope", filter: library.ProjectionFilter{Scope: &globalWithProject}, code: library.InvalidArgument},
		{name: "unknown agent", filter: library.ProjectionFilter{Scope: &unknownAgent}, code: library.NotFound},
		{name: "unregistered project", filter: library.ProjectionFilter{Scope: &unregistered}, code: library.NotFound},
		{name: "invalid policy", filter: library.ProjectionFilter{SkillID: skill.ID}, opts: library.RewriteOptions{OnConflict: "merge"}, code: library.InvalidArgument},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := lib.RewriteProjections(ctx, test.filter, test.opts)
			requireLibraryError(t, err, test.code)
			if len(got) != 0 {
				t.Fatalf("RewriteProjections() = %#v, want no results", got)
			}
		})
	}
	if got := rewriteProjections(t, lib, scopeFilter(idle), library.RewriteOptions{}); len(got) != 0 {
		t.Fatalf("RewriteProjections(idle scope) = %#v, want empty", got)
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

func TestRewriteProjectionsCancelReturnsCompletedItems(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	first, firstSource := installLocalSkill(t, lib, "first", "# first v1\n")
	second, secondSource := installLocalSkill(t, lib, "second", "# second v1\n")
	scope := library.GlobalScope(library.AgentCodex)
	directory, err := lib.Resolve(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []library.SkillID{first.ID, second.ID} {
		if err := lib.Activate(ctx, id, scope, library.ActivateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	updateLocalSkill(t, lib, first.ID, firstSource, "# first v2\n")
	updateLocalSkill(t, lib, second.ID, secondSource, "# second v2\n")

	// 第一条提交完成(新内容已就位且备份已清理)后取消。
	cancelAfterFirst := cancelWhen{Context: ctx, cond: func() bool {
		content, err := os.ReadFile(filepath.Join(directory, first.Name, "SKILL.md"))
		if err != nil || string(content) != "# first v2\n" {
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
	got, err := lib.RewriteProjections(cancelAfterFirst, scopeFilter(scope), library.RewriteOptions{})
	requireLibraryError(t, err, library.Canceled)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RewriteProjections() error = %v, want context.Canceled in chain", err)
	}
	requireRewriteResults(t, got, []library.RewriteResult{rewritten(first.ID, scope)})
	requireInspectionStatuses(t, lib, library.ProjectionCurrent, library.ProjectionStale)

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	got, err = lib.RewriteProjections(canceled, scopeFilter(scope), library.RewriteOptions{})
	requireLibraryError(t, err, library.Canceled)
	if len(got) != 0 {
		t.Fatalf("RewriteProjections(canceled) = %#v, want no results", got)
	}
	requireInspectionStatuses(t, lib, library.ProjectionCurrent, library.ProjectionStale)
}

func TestInspectProjectionsByFilterIsSubsetOfFullInspection(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, firstSource := installLocalSkill(t, lib, "first", "# first v1\n")
	second := installNamedSkill(t, lib, "second")
	idle := installNamedSkill(t, lib, "idle")
	codex := library.GlobalScope(library.AgentCodex)
	claude := library.GlobalScope(library.AgentClaudeCode)
	projectScope := library.ProjectScope(library.AgentCodex, project.ID)
	for _, scope := range []library.Scope{codex, claude, projectScope} {
		if err := lib.Activate(ctx, first.ID, scope, library.ActivateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	for _, scope := range []library.Scope{codex, projectScope} {
		if err := lib.Activate(ctx, second.ID, scope, library.ActivateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	updateLocalSkill(t, lib, first.ID, firstSource, "# first v2\n")
	full := inspectProjections(t, lib)
	if len(full) != 5 {
		t.Fatalf("InspectProjections() = %#v, want 5", full)
	}

	subset := func(match func(library.ProjectionInspection) bool) []library.ProjectionInspection {
		var want []library.ProjectionInspection
		for _, inspection := range full {
			if match(inspection) {
				want = append(want, inspection)
			}
		}
		return want
	}
	tests := []struct {
		name   string
		filter library.ProjectionFilter
		want   []library.ProjectionInspection
	}{
		{name: "skill", filter: library.ProjectionFilter{SkillID: first.ID},
			want: subset(func(i library.ProjectionInspection) bool { return i.SkillID == first.ID })},
		{name: "scope", filter: scopeFilter(projectScope),
			want: subset(func(i library.ProjectionInspection) bool { return i.Scope == projectScope })},
		{name: "both", filter: library.ProjectionFilter{SkillID: second.ID, Scope: &codex},
			want: subset(func(i library.ProjectionInspection) bool { return i.SkillID == second.ID && i.Scope == codex })},
		{name: "no projections", filter: library.ProjectionFilter{SkillID: idle.ID}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := lib.InspectProjections(ctx, test.filter)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(test.want) || (len(got) > 0 && !reflect.DeepEqual(got, test.want)) {
				t.Fatalf("InspectProjections(%s) = %#v, want %#v", test.name, got, test.want)
			}
		})
	}

	unknownAgent := library.GlobalScope("unknown-agent")
	for name, filter := range map[string]library.ProjectionFilter{
		"unknown skill": {SkillID: 999},
		"unknown agent": {Scope: &unknownAgent},
	} {
		_, err := lib.InspectProjections(ctx, filter)
		if !library.IsCode(err, library.NotFound) {
			t.Fatalf("InspectProjections(%s) error = %v, want not_found", name, err)
		}
	}
	invalid := library.Scope{Kind: "nowhere", Agent: library.AgentCodex}
	_, err = lib.InspectProjections(ctx, library.ProjectionFilter{Scope: &invalid})
	requireLibraryError(t, err, library.InvalidArgument)
}
