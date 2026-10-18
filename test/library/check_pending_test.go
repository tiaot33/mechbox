package library_test

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/tiaot33/mechbox/internal/library"
)

// 计入角标

func TestPendingCountsChangesAndDeletionsButNotLocalSources(t *testing.T) {
	ctx := t.Context()
	lib := openTestLibrary(t)
	local, source := fetchLocalSkill(t, lib, "local", "v1")
	writeLocalSkill(t, source, "v2")
	if check := checkedSkill(t, lib, local.ID); check.Status != library.UpdateAvailable || check.Pending || check.Revision != "" {
		t.Fatalf("local check = %#v; want available but not pending", check)
	}

	archive := zipArchive(t, map[string]string{"a/SKILL.md": skillDocument("a", "v1"), "b/SKILL.md": skillDocument("b", "v1")})
	server := newArchiveServer(t, archive, `"v1"`)
	preview, err := lib.PreviewFetch(ctx, server.request())
	if err != nil {
		t.Fatal(err)
	}
	skills := fetchSkills(t, lib, preview, []string{"a", "b"})
	// 来源没变(304)时,结论也带上来源版本。
	if check := checkedSkill(t, lib, skills[0].ID); check.Status != library.UpdateCurrent || check.Pending || check.Revision != preview.Revision {
		t.Fatalf("unchanged url check = %#v; want current at %s", check, preview.Revision)
	}
	changed := zipArchive(t, map[string]string{"a/SKILL.md": skillDocument("a", "v2")})
	server.serve(changed, `"v2"`)
	sum := sha256.Sum256(changed)
	revision := hex.EncodeToString(sum[:])
	if check := checkedSkill(t, lib, skills[0].ID); check.Status != library.UpdateAvailable || !check.Pending || check.Skipped || check.Revision != revision {
		t.Fatalf("changed url check = %#v; want pending at %s", check, revision)
	}
	if check := checkedSkill(t, lib, skills[1].ID); check.Status != library.UpdateDeleted || !check.Pending || check.Revision != revision {
		t.Fatalf("deleted url check = %#v; want pending", check)
	}
}

func TestLateCheckKeepsNewerConclusionSkipAndNewSkills(t *testing.T) {
	ctx := t.Context()
	lib := openTestLibrary(t)
	gate := newNetworkGate()
	var mu sync.Mutex
	archive := zipArchive(t, map[string]string{"a/SKILL.md": skillDocument("a", "v1")})
	serve := func(files map[string]string) {
		mu.Lock()
		defer mu.Unlock()
		archive = zipArchive(t, files)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		snapshot := archive
		mu.Unlock()
		gate.pass()
		_, _ = w.Write(snapshot)
	}))
	t.Cleanup(server.Close)
	a := fetchURLSkill(t, lib, server.URL, "a")
	serve(map[string]string{"a/SKILL.md": skillDocument("a", "v2")})
	gate.arm()
	var wg sync.WaitGroup
	var slowErr error
	wg.Go(func() { _, slowErr = lib.CheckUpdates(ctx, []library.CollectionID{a.CollectionID}) })
	gate.awaitEntered(t)
	gate.disarm()
	serve(map[string]string{"a/SKILL.md": skillDocument("a", "v3"), "c/SKILL.md": skillDocument("c", "v1")})
	plan, err := lib.PlanUpdate(ctx, a.CollectionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.SkipUpdate(ctx, library.SkipRequest{Plan: plan.ID, Skills: []library.SkillID{a.ID}}); err != nil {
		t.Fatal(err)
	}
	requireNewSkills(t, lib, a.CollectionID, []string{"c"})
	gate.open()
	wg.Wait()
	if slowErr != nil {
		t.Fatal(slowErr)
	}
	if skill, err := lib.GetSkill(ctx, a.ID); err != nil || !skill.LastUpdateCheck.Skipped || skill.LastUpdateCheck.Pending {
		t.Fatalf("after the late check = %#v, %v; want the newer skipped conclusion", skill.LastUpdateCheck, err)
	}
	requireNewSkills(t, lib, a.CollectionID, []string{"c"})
}

func TestCheckStartedBeforeKeepDoesNotUndoIt(t *testing.T) {
	ctx := t.Context()
	lib := openTestLibrary(t)
	gate := newNetworkGate()
	var mu sync.Mutex
	files := map[string]string{"a/SKILL.md": skillDocument("a", "v1"), "b/SKILL.md": skillDocument("b", "v1")}
	server := serveGatedArchive(t, gate, func() []byte {
		mu.Lock()
		defer mu.Unlock()
		return zipArchive(t, files)
	})
	preview, err := lib.PreviewFetch(ctx, library.PreviewFetchRequest{Input: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	a := fetchSkills(t, lib, preview, []string{"a", "b"})[0]
	gate.arm()
	var wg sync.WaitGroup
	wg.Go(func() { _, _ = lib.CheckUpdates(ctx, []library.CollectionID{a.CollectionID}) })
	gate.awaitEntered(t)
	gate.disarm()
	mu.Lock()
	files = map[string]string{"b/SKILL.md": skillDocument("b", "v1")}
	mu.Unlock()
	if _, err := lib.PlanUpdate(ctx, a.CollectionID); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.KeepDeletedSkill(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	gate.open()
	wg.Wait()
	if skill, err := lib.GetSkill(ctx, a.ID); err != nil || !skill.KeptDeleted || skill.LastUpdateCheck.Status != library.UpdateDeleted || skill.LastUpdateCheck.Pending {
		t.Fatalf("after a check that started before keeping = %#v, %v", skill, err)
	}
}

func TestCheckOverwritingAFailureStillClearsAnEarlierKeep(t *testing.T) {
	ctx := t.Context()
	lib := openTestLibrary(t)
	gate := newNetworkGate()
	var mu sync.Mutex
	files := map[string]string{"a/SKILL.md": skillDocument("a", "v1"), "b/SKILL.md": skillDocument("b", "v1")}
	failing := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		snapshot, fail := zipArchive(t, files), failing
		mu.Unlock()
		if fail {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		gate.pass()
		_, _ = w.Write(snapshot)
	}))
	t.Cleanup(server.Close)
	preview, err := lib.PreviewFetch(ctx, library.PreviewFetchRequest{Input: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	a := fetchSkills(t, lib, preview, []string{"a", "b"})[0]
	mu.Lock()
	files = map[string]string{"b/SKILL.md": skillDocument("b", "v1")}
	mu.Unlock()
	checkedSkill(t, lib, a.ID)
	if _, err := lib.KeepDeletedSkill(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	// 来源里又有了 a:之后开始的检查即使覆盖的是一次检查失败,也清掉之前设下的标记。
	mu.Lock()
	files = map[string]string{"a/SKILL.md": skillDocument("a", "v1"), "b/SKILL.md": skillDocument("b", "v1")}
	mu.Unlock()
	gate.arm()
	var wg sync.WaitGroup
	wg.Go(func() { _, _ = lib.CheckUpdates(ctx, []library.CollectionID{a.CollectionID}) })
	gate.awaitEntered(t)
	gate.disarm()
	mu.Lock()
	failing = true
	mu.Unlock()
	if status, err := checkSkillUpdate(t, lib, ctx, a.ID); status != library.UpdateFailed || err == nil {
		t.Fatalf("failing check = %v, %v", status, err)
	}
	gate.open()
	wg.Wait()
	if skill, err := lib.GetSkill(ctx, a.ID); err != nil || skill.KeptDeleted || skill.LastUpdateCheck.Status != library.UpdateCurrent {
		t.Fatalf("after the source restored a = %#v, %v; want the keep mark cleared", skill, err)
	}
}
