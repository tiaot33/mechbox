package library_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tiaot33/skills-tui/internal/library"
)

// 更新

func TestUpdateCarriesSelectedSkillsToOneSourceRevision(t *testing.T) {
	ctx := t.Context()
	lib := openTestLibrary(t)
	server := newArchiveServer(t, zipArchive(t, map[string]string{
		"first/SKILL.md":  skillDocument("first", "old"),
		"second/SKILL.md": skillDocument("second", "old"),
		"third/SKILL.md":  skillDocument("third", "old"),
	}), "")
	var requests atomic.Int32
	transport := http.DefaultClient.Transport
	http.DefaultClient.Transport = fetchTransport(func(r *http.Request) (*http.Response, error) {
		requests.Add(1)
		return transport.RoundTrip(r)
	})
	skills := fetchArchive(t, lib, server.request())
	first, second, third := skills[0], skills[1], skills[2]
	server.serve(zipArchive(t, map[string]string{
		"first/SKILL.md":  skillDocument("first", "new"),
		"second/SKILL.md": skillDocument("second", "new"),
		"third/SKILL.md":  skillDocument("third", "new"),
	}), "")
	request := mustPlanUpdate(t, lib, second.ID, first.ID, second.ID)
	requests.Store(0)
	result, err := lib.Update(ctx, request)
	if err != nil || len(result.Skills) != 2 || result.Skills[0].Skill.ID != second.ID || result.Skills[1].Skill.ID != first.ID {
		t.Fatalf("Update = %#v, %v", result, err)
	}
	if requests.Load() != 1 {
		t.Fatalf("source read %d times; want once", requests.Load())
	}
	if result.Source != first.Source {
		t.Fatalf("result source = %#v; want %#v", result.Source, first.Source)
	}
	for i, before := range []library.Skill{second, first} {
		updated := result.Skills[i]
		if !updated.Changed || updated.PreviousVersionID != before.VersionID || updated.Skill.VersionID == before.VersionID {
			t.Fatalf("updated %s = %#v", before.Name, updated)
		}
		if updated.Skill.Revision == before.Revision || updated.Skill.Revision != result.Skills[0].Skill.Revision {
			t.Fatalf("revision of %s = %q; want the same new source revision", before.Name, updated.Skill.Revision)
		}
		// 入库后从未检查过的技能,更新后的结论也是已是最新。
		if check := updated.Skill.LastUpdateCheck; check == nil || check.Status != library.UpdateCurrent {
			t.Fatalf("conclusion of %s = %+v", before.Name, check)
		}
		// 更新前后的版本直接拿来比较。
		diff, err := lib.DiffVersions(ctx, updated.PreviousVersionID, updated.Skill.VersionID)
		if err != nil || !reflect.DeepEqual(diff.Changed, []string{"SKILL.md"}) {
			t.Fatalf("diff of %s = %#v, %v", before.Name, diff, err)
		}
		if previous, err := lib.GetVersion(ctx, updated.PreviousVersionID); err != nil || previous.Revision != before.Revision {
			t.Fatalf("previous version of %s = %#v, %v", before.Name, previous, err)
		}
	}
	// 有变化而没勾的技能不动,只留着更新计划写下的结论。
	got, err := lib.GetSkill(ctx, third.ID)
	if err != nil || got.LastUpdateCheck == nil || got.LastUpdateCheck.Status != library.UpdateAvailable {
		t.Fatalf("unselected skill = %#v, %v", got, err)
	}
	got.LastUpdateCheck = nil
	if !reflect.DeepEqual(got, third) {
		t.Fatalf("unselected skill = %#v; want %#v", got, third)
	}
}

func TestUpdateIsAllOrNothing(t *testing.T) {
	ctx := t.Context()
	lib := openTestLibrary(t)
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a", "SKILL.md"), []byte(skillDocument("first", "v1")), 0o644)
	writeFile(t, filepath.Join(root, "b", "SKILL.md"), []byte(skillDocument("second", "v1")), 0o644)
	preview, err := lib.PreviewFetch(ctx, localRequest(root))
	if err != nil {
		t.Fatal(err)
	}
	fetched, err := lib.Fetch(ctx, preview.ID, candidateNames(preview))
	if err != nil {
		t.Fatal(err)
	}
	first, second := fetched[0], fetched[1]
	writeFile(t, filepath.Join(root, "a", "SKILL.md"), []byte(skillDocument("first", "v2")), 0o644)
	if err := os.RemoveAll(filepath.Join(root, "b")); err != nil {
		t.Fatal(err)
	}
	request := mustPlanUpdate(t, lib, first.ID, second.ID)
	before, err := lib.ListSkills(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = lib.Update(ctx, request)
	requireErrorDetails(t, err, library.NotFound, library.ErrorDetails{SkillID: second.ID, Name: second.Name})
	after, err := lib.ListSkills(ctx)
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("after failed update = %#v, %v; want %#v", after, err, before)
	}
	if versions, err := lib.ListVersions(ctx, first.ID); err != nil || len(versions) != 1 {
		t.Fatalf("first versions = %#v, %v", versions, err)
	}
}

// 事务里已写下前一个技能、后一个技能才发现读取期间被改过或被移除时,整批回滚:前一个技能的版本和结论都不变。
func TestUpdateRollsBackEarlierSkillsWhenALaterOneChangedDuringSourceRead(t *testing.T) {
	for _, race := range []string{"update", "remove"} {
		t.Run(race, func(t *testing.T) {
			ctx := t.Context()
			gate := newNetworkGate()
			var archive atomic.Pointer[[]byte]
			old := zipArchive(t, map[string]string{"first/SKILL.md": skillDocument("first", "old"), "second/SKILL.md": skillDocument("second", "old")})
			archive.Store(&old)
			server := serveGatedArchive(t, gate, func() []byte { return *archive.Load() })
			lib := openTestLibrary(t)
			preview, err := lib.PreviewFetch(ctx, library.PreviewFetchRequest{Input: server.URL + "/skills.zip"})
			if err != nil {
				t.Fatal(err)
			}
			fetched := fetchSkills(t, lib, preview, []string{"first", "second"})
			first, second := fetched[0], fetched[1]
			if _, err := lib.CheckUpdates(ctx, []library.CollectionID{first.CollectionID}); err != nil {
				t.Fatal(err)
			}
			before, err := lib.GetSkill(ctx, first.ID)
			if err != nil || before.LastUpdateCheck == nil {
				t.Fatalf("first before = %#v, %v", before, err)
			}
			newer := zipArchive(t, map[string]string{"first/SKILL.md": skillDocument("first", "new"), "second/SKILL.md": skillDocument("second", "new")})
			archive.Store(&newer)
			request := mustPlanUpdate(t, lib, first.ID, second.ID)
			before, err = lib.GetSkill(ctx, first.ID)
			if err != nil {
				t.Fatal(err)
			}
			gate.arm()
			var wg sync.WaitGroup
			var updateErr error
			wg.Go(func() { _, updateErr = lib.Update(ctx, request) })
			gate.awaitEntered(t)
			gate.disarm()
			mustFinishBeforeRelease(t, gate, race, func(ctx context.Context) error {
				if race == "remove" {
					return lib.RemoveSkill(ctx, second.ID, library.RemoveSkillOptions{})
				}
				request, err := planSingleUpdate(ctx, lib, second.ID)
				if err != nil {
					return err
				}
				_, err = lib.Update(ctx, request)
				return err
			})
			gate.open()
			wg.Wait()
			want := library.Conflict
			if race == "remove" {
				want = library.NotFound
			}
			requireErrorDetails(t, updateErr, want, library.ErrorDetails{SkillID: second.ID})
			if after, err := lib.GetSkill(ctx, first.ID); err != nil || after.VersionID != before.VersionID || after.Revision != before.Revision {
				t.Fatalf("first after rollback = %#v, %v; want %#v", after, err, before)
			}
			if versions, err := lib.ListVersions(ctx, first.ID); err != nil || len(versions) != 1 {
				t.Fatalf("first versions = %#v, %v; want only the fetched one", versions, err)
			}
			if race == "update" {
				if versions, err := lib.ListVersions(ctx, second.ID); err != nil || len(versions) != 2 {
					t.Fatalf("second versions = %#v, %v; want the racing update kept", versions, err)
				}
			}
		})
	}
}

// 同一 URL 集合里的技能记着不同的 validator:按第一个技能的 validator 得到 304 后,来源在补读整包前又发布了新内容。
// 检查与更新都按补读到的那一份判断,不把 304 的结论套在第一个技能上,也不给旧内容记上新的来源版本。
func TestURLCollectionIsJudgedAgainstOneArchiveWhenValidatorsDiffer(t *testing.T) {
	ctx := t.Context()
	lib := openTestLibrary(t)
	server := newArchiveServer(t, zipArchive(t, map[string]string{"first/SKILL.md": skillDocument("first", "v1"), "second/SKILL.md": skillDocument("second", "v1")}), `"v1"`)
	skills := fetchArchive(t, lib, server.request())
	first, second := skills[0], skills[1]
	// 两个都变了、只更新 first:second 仍记着 v1 的 validator。
	server.serve(zipArchive(t, map[string]string{"first/SKILL.md": skillDocument("first", "v2"), "second/SKILL.md": skillDocument("second", "v2")}), `"v2"`)
	if _, err := updateSkill(t, lib, ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	v3 := zipArchive(t, map[string]string{"first/SKILL.md": skillDocument("first", "v3"), "second/SKILL.md": skillDocument("second", "v3")})
	http.DefaultClient.Transport = fetchTransport(func(r *http.Request) (*http.Response, error) {
		header := make(http.Header)
		if r.Header.Get("If-None-Match") == `"v2"` {
			header.Set("ETag", `"v2"`)
			return &http.Response{StatusCode: http.StatusNotModified, Header: header, Body: http.NoBody}, nil
		}
		header.Set("ETag", `"v3"`)
		return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(bytes.NewReader(v3))}, nil
	})
	checks, err := lib.CheckUpdates(ctx, []library.CollectionID{first.CollectionID})
	if err != nil || len(checks) != 1 || checks[0].Error != nil || len(checks[0].Skills) != 2 {
		t.Fatalf("CheckUpdates = %#v, %v", checks, err)
	}
	for _, skill := range checks[0].Skills {
		if skill.LastUpdateCheck.Status != library.UpdateAvailable {
			t.Fatalf("check of %s = %+v; want available", skill.Name, skill.LastUpdateCheck)
		}
	}
	result, err := lib.Update(ctx, mustPlanUpdate(t, lib, first.ID, second.ID))
	if err != nil || len(result.Skills) != 2 {
		t.Fatalf("Update = %#v, %v", result, err)
	}
	for _, updated := range result.Skills {
		if !updated.Changed || string(skillFiles(t, lib, updated.Skill)[0].Content) != skillDocument(updated.Skill.Name, "v3") {
			t.Fatalf("updated %s = %#v; want v3", updated.Skill.Name, updated)
		}
		if updated.Skill.Revision != result.Skills[0].Skill.Revision {
			t.Fatalf("revisions = %q, %q; want one source revision", result.Skills[0].Skill.Revision, updated.Skill.Revision)
		}
	}
	for _, skill := range []library.Skill{first, second} {
		if status, err := checkSkillUpdate(t, lib, ctx, skill.ID); err != nil || status != library.UpdateCurrent {
			t.Fatalf("check of %s after update = %v, %v", skill.Name, status, err)
		}
	}
}

func TestUpdateRejectsInvalidRequests(t *testing.T) {
	ctx := t.Context()
	lib := openTestLibrary(t)
	skill := fetchNamedSkill(t, lib, "one")
	other := fetchNamedSkill(t, lib, "other")
	plan := mustPlanUpdate(t, lib, skill.ID).Plan
	global := library.GlobalScope(library.AgentCodex)
	for _, tc := range []struct {
		name string
		req  library.UpdateRequest
		code library.ErrorCode
		want library.ErrorDetails
	}{
		{"unknown plan", library.UpdateRequest{Plan: "unknown", Skills: []library.SkillID{skill.ID}}, library.NotFound, library.ErrorDetails{}},
		{"no skills", library.UpdateRequest{Plan: plan}, library.InvalidArgument, library.ErrorDetails{Field: "skills"}},
		{"missing skill", library.UpdateRequest{Plan: plan, Skills: []library.SkillID{skill.ID, 9999}}, library.InvalidArgument, library.ErrorDetails{Field: "skills", SkillID: 9999}},
		{"skill from another collection", library.UpdateRequest{Plan: plan, Skills: []library.SkillID{skill.ID, other.ID}}, library.InvalidArgument, library.ErrorDetails{Field: "skills", SkillID: other.ID}},
		{"install of an unselected skill", library.UpdateRequest{Plan: plan, Skills: []library.SkillID{skill.ID}, Installs: []library.ReinstallRequest{{SkillID: other.ID, Scope: global}}}, library.InvalidArgument, library.ErrorDetails{Field: "installs", SkillID: other.ID}},
		{"install listed twice", library.UpdateRequest{Plan: plan, Skills: []library.SkillID{skill.ID}, Installs: []library.ReinstallRequest{{SkillID: skill.ID, Scope: global}, {SkillID: skill.ID, Scope: global}}}, library.InvalidArgument, library.ErrorDetails{Field: "installs"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := lib.Update(ctx, tc.req)
			typed := requireLibraryError(t, err, tc.code)
			if tc.code != library.NotFound && typed.Details != tc.want {
				t.Fatalf("details = %#v; want %#v", typed.Details, tc.want)
			}
		})
	}
}

func TestUpdateMarksSelectedSkillsCurrent(t *testing.T) {
	for _, name := range []string{"changed", "unchanged", "never checked"} {
		t.Run(name, func(t *testing.T) {
			lib := openTestLibrary(t)
			skill, source := fetchLocalSkill(t, lib, "example", "old")
			writeLocalSkill(t, source, "new")
			var checkedAt time.Time
			if name != "never checked" {
				if status, err := checkSkillUpdate(t, lib, t.Context(), skill.ID); err != nil || status != library.UpdateAvailable {
					t.Fatalf("check = %v, %v", status, err)
				}
				checkedAt = updateCheckState(t, lib, skill.ID).CheckedAt
			}
			if name == "unchanged" {
				writeLocalSkill(t, source, "old")
			}
			result, err := updateSkill(t, lib, t.Context(), skill.ID)
			if err != nil || result.Changed != (name != "unchanged") {
				t.Fatalf("update = %+v, %v", result, err)
			}
			got := updateCheckState(t, lib, skill.ID)
			if got == nil || got.Status != library.UpdateCurrent || got.CheckedAt.Before(checkedAt) || !reflect.DeepEqual(got, result.Skill.LastUpdateCheck) {
				t.Fatalf("after update = %+v", got)
			}
		})
	}
}

// 按更新计划更新并同时重新安装

func TestUpdateReinstallsCheckedPlacesOnly(t *testing.T) {
	ctx := t.Context()
	t.Setenv("HOME", t.TempDir())
	lib := openTestLibrary(t)
	skill, source := fetchLocalSkill(t, lib, "pdf", "v1")
	project, err := lib.RegisterProject(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	claude := library.GlobalScope(library.AgentClaudeCode)
	codex := library.GlobalScope(library.AgentCodex)
	edited := library.ProjectScope(library.AgentClaudeCode, project.ID)
	directories := map[library.Scope]string{}
	for _, scope := range []library.Scope{claude, codex, edited} {
		if err := lib.Install(ctx, skill.ID, scope, library.InstallOptions{}); err != nil {
			t.Fatal(err)
		}
		if directories[scope], err = lib.ScopeDirectory(ctx, scope); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(directories[edited], "pdf", "SKILL.md"), []byte("edited by hand"), 0o644)
	writeLocalSkill(t, source, "v2")
	plan, err := lib.PlanUpdate(ctx, skill.CollectionID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := lib.Update(ctx, library.UpdateRequest{
		Plan:     plan.ID,
		Skills:   []library.SkillID{skill.ID},
		Installs: []library.ReinstallRequest{{SkillID: skill.ID, Scope: claude}, {SkillID: skill.ID, Scope: edited}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Record == 0 || len(result.Skills) != 1 || !result.Skills[0].Changed {
		t.Fatalf("Update = %#v", result)
	}
	if len(result.Installs) != 2 || result.Installs[0].Outcome != library.Reinstalled || result.Installs[1].Outcome != library.ReinstallFailed || result.Installs[1].Error.Code != library.AlreadyExists {
		t.Fatalf("installs = %#v; want the checked place reinstalled and the edited one refused", result.Installs)
	}
	requireInstalledFolder(t, directories[claude], "pdf", "v2")
	requireInstalledFolder(t, directories[codex], "pdf", "v1")
	requireSkillFile(t, directories[edited], "pdf", "edited by hand")
	if _, err := lib.Update(ctx, library.UpdateRequest{Plan: plan.ID, Skills: []library.SkillID{skill.ID}}); !library.IsCode(err, library.NotFound) {
		t.Fatalf("reusing the plan = %v; want NotFound", err)
	}
}

func TestUpdateBringsTheWholeCollectionToOneRevision(t *testing.T) {
	ctx := t.Context()
	lib := openTestLibrary(t)
	server := newArchiveServer(t, zipArchive(t, map[string]string{"a/SKILL.md": skillDocument("a", "v1"), "b/SKILL.md": skillDocument("b", "v1"), "c/SKILL.md": skillDocument("c", "v1")}), "")
	skills := fetchArchive(t, lib, server.request())
	server.serve(zipArchive(t, map[string]string{"a/SKILL.md": skillDocument("a", "v2"), "b/SKILL.md": skillDocument("b", "v2"), "c/SKILL.md": skillDocument("c", "v1")}), "")
	plan, err := lib.PlanUpdate(ctx, skills[0].CollectionID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := lib.Update(ctx, library.UpdateRequest{Plan: plan.ID, Skills: []library.SkillID{skills[0].ID, skills[1].ID}})
	if err != nil || result.Revision != plan.Revision {
		t.Fatalf("Update = %#v, %v", result, err)
	}
	for _, skill := range skills {
		got, err := lib.GetSkill(ctx, skill.ID)
		if err != nil || got.Revision != plan.Revision || got.LastUpdateCheck.Status != library.UpdateCurrent {
			t.Fatalf("%s after updating the collection = %#v, %v; want revision %s", skill.Name, got, err, plan.Revision)
		}
	}
	if versions, err := lib.ListVersions(ctx, skills[2].ID); err != nil || len(versions) != 1 {
		t.Fatalf("unchanged skill versions = %#v, %v; want no new version", versions, err)
	}
}

func TestUpdateConflictsOnlyWhenACheckedSkillChangedAgain(t *testing.T) {
	ctx := t.Context()
	lib := openTestLibrary(t)
	server := newArchiveServer(t, zipArchive(t, map[string]string{"a/SKILL.md": skillDocument("a", "v1"), "b/SKILL.md": skillDocument("b", "v1")}), "")
	skills := fetchArchive(t, lib, server.request())
	a, b := skills[0], skills[1]
	server.serve(zipArchive(t, map[string]string{"a/SKILL.md": skillDocument("a", "v2"), "b/SKILL.md": skillDocument("b", "v1")}), "")
	plan, err := lib.PlanUpdate(ctx, a.CollectionID)
	if err != nil {
		t.Fatal(err)
	}
	server.serve(zipArchive(t, map[string]string{"a/SKILL.md": skillDocument("a", "v3"), "b/SKILL.md": skillDocument("b", "v1")}), "")
	_, err = lib.Update(ctx, library.UpdateRequest{Plan: plan.ID, Skills: []library.SkillID{a.ID}})
	requireErrorDetails(t, err, library.Conflict, library.ErrorDetails{SkillID: a.ID})
	if versions, err := lib.ListVersions(ctx, a.ID); err != nil || len(versions) != 1 {
		t.Fatalf("versions after conflict = %#v, %v", versions, err)
	}
	// 只有同一来源里别的技能变了:照常更新,换到来源现在的来源版本。
	plan, err = lib.PlanUpdate(ctx, a.CollectionID)
	if err != nil {
		t.Fatal(err)
	}
	server.serve(zipArchive(t, map[string]string{"a/SKILL.md": skillDocument("a", "v3"), "b/SKILL.md": skillDocument("b", "v2")}), "")
	result, err := lib.Update(ctx, library.UpdateRequest{Plan: plan.ID, Skills: []library.SkillID{a.ID}})
	if err != nil || result.Revision == plan.Revision || string(skillFiles(t, lib, result.Skills[0].Skill)[0].Content) != skillDocument("a", "v3") {
		t.Fatalf("Update when only b changed = %#v, %v; want a at v3 on the newer revision", result, err)
	}
	if got, err := lib.GetSkill(ctx, b.ID); err != nil || got.VersionID != b.VersionID {
		t.Fatalf("b = %#v, %v; want untouched", got, err)
	}
}

func TestUpdateRefusesToWriteIntoAPulledLibrary(t *testing.T) {
	ctx := t.Context()
	newS3Stub(t)
	lib := openTestLibrary(t)
	skill, source := fetchLocalSkill(t, lib, "a", "v1")
	writeLocalSkill(t, source, "v2")
	request := mustPlanUpdate(t, lib, skill.ID)
	push, err := lib.PlanSyncPush(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var expected library.RemoteIdentity
	if push.Remote != nil {
		expected = push.Remote.Identity
	}
	if _, err := lib.SyncPush(ctx, expected); err != nil {
		t.Fatal(err)
	}
	pull, err := lib.PlanSyncPull(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.SyncPull(ctx, pull.Remote.Identity); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Update(ctx, request); !library.IsCode(err, library.Conflict) {
		t.Fatalf("Update after the library was replaced = %v; want Conflict", err)
	}
	if versions, err := lib.ListVersions(ctx, skill.ID); err != nil || len(versions) != 1 {
		t.Fatalf("versions = %#v, %v; want nothing written", versions, err)
	}
}

// 继续保留

func TestKeptDeletedSkillStopsCountingAndSurvivesRestart(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "library.db")
	lib, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	server := newArchiveServer(t, zipArchive(t, map[string]string{"a/SKILL.md": skillDocument("a", "v1"), "b/SKILL.md": skillDocument("b", "v1")}), "")
	preview, err := lib.PreviewFetch(ctx, server.request())
	if err != nil {
		t.Fatal(err)
	}
	skills := fetchSkills(t, lib, preview, []string{"a", "b"})
	a := skills[0]
	if _, err := lib.KeepDeletedSkill(ctx, 9999); !library.IsCode(err, library.NotFound) {
		t.Fatalf("keep a missing skill = %v; want NotFound", err)
	}
	server.serve(zipArchive(t, map[string]string{"b/SKILL.md": skillDocument("b", "v1")}), "")
	if check := checkedSkill(t, lib, a.ID); check.Status != library.UpdateDeleted || !check.Pending {
		t.Fatalf("deleted = %#v; want pending", check)
	}
	kept, err := lib.KeepDeletedSkill(ctx, a.ID)
	if err != nil || !kept.KeptDeleted || kept.LastUpdateCheck.Pending {
		t.Fatalf("KeepDeletedSkill = %#v, %v", kept, err)
	}
	if err := lib.Close(); err != nil {
		t.Fatal(err)
	}
	lib = openTestLibraryAt(t, path)
	if skill, err := lib.GetSkill(ctx, a.ID); err != nil || !skill.KeptDeleted || skill.LastUpdateCheck.Pending {
		t.Fatalf("after restart = %#v, %v", skill, err)
	}
	if check := checkedSkill(t, lib, a.ID); check.Status != library.UpdateDeleted || check.Pending {
		t.Fatalf("rechecked kept skill = %#v; want deleted, not pending", check)
	}
	// 来源里又有了这个技能,标记自动失效。
	server.serve(zipArchive(t, map[string]string{"a/SKILL.md": skillDocument("a", "v2"), "b/SKILL.md": skillDocument("b", "v1")}), "")
	if check := checkedSkill(t, lib, a.ID); check.Status != library.UpdateAvailable || !check.Pending {
		t.Fatalf("restored = %#v; want pending", check)
	}
	if skill, err := lib.GetSkill(ctx, a.ID); err != nil || skill.KeptDeleted {
		t.Fatalf("mark after restore = %#v, %v; want cleared", skill, err)
	}
}

func TestKeepDeletedSkillFoundByUpdatePlan(t *testing.T) {
	ctx := t.Context()
	lib := openTestLibrary(t)
	root := t.TempDir()
	writeLocalSkill(t, filepath.Join(root, "a"), "v1")
	writeLocalSkill(t, filepath.Join(root, "b"), "v1")
	preview, err := lib.PreviewFetch(ctx, localRequest(root))
	if err != nil {
		t.Fatal(err)
	}
	a := fetchSkills(t, lib, preview, []string{"a", "b"})[0]
	if err := os.RemoveAll(filepath.Join(root, "a")); err != nil {
		t.Fatal(err)
	}
	plan, err := lib.PlanUpdate(ctx, a.CollectionID)
	if err != nil || plan.Skills[0].Change != library.PlannedDeleted {
		t.Fatalf("plan = %#v, %v; want a deleted", plan.Skills, err)
	}
	if kept, err := lib.KeepDeletedSkill(ctx, a.ID); err != nil || !kept.KeptDeleted {
		t.Fatalf("KeepDeletedSkill after the plan = %#v, %v", kept, err)
	}
}
