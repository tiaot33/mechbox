package library_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tiaot33/skills-tui/internal/library"
)

func TestUpdateClearsAvailableCheck(t *testing.T) {
	for _, name := range []string{"changed", "unchanged"} {
		changed := name == "changed"
		t.Run(name, func(t *testing.T) {
			lib := openTestLibrary(t)
			skill, source := fetchLocalSkill(t, lib, "example", "old")
			writeLocalSkill(t, source, "new")
			if available, err := checkSingleUpdate(t, lib, t.Context(), skill.ID); err != nil || !available {
				t.Fatalf("check = %v, %v", available, err)
			}
			before := updateCheckState(t, lib, skill.ID)
			if !changed {
				writeLocalSkill(t, source, "old")
			}
			result, err := lib.Update(t.Context(), skill.ID)
			if err != nil || result.Changed != changed {
				t.Fatalf("update = %+v, %v", result, err)
			}
			got := updateCheckState(t, lib, skill.ID)
			if got == nil || got.Status != library.UpdateCurrent || got.CheckedAt.Before(before.CheckedAt) {
				t.Fatalf("after update = %+v", got)
			}
		})
	}
}

func TestUpdateChecksReplaceFailuresAndRemovalStartsUnchecked(t *testing.T) {
	lib := openTestLibrary(t)
	skill, source := fetchLocalSkill(t, lib, "example", "old")
	for _, status := range []library.UpdateCheckStatus{library.UpdateCurrent, library.UpdateAvailable, library.UpdateFailed, library.UpdateCurrent} {
		before := time.Now()
		switch status {
		case library.UpdateAvailable:
			writeLocalSkill(t, source, "new")
		case library.UpdateFailed:
			if err := os.Rename(source, source+"-offline"); err != nil {
				t.Fatal(err)
			}
		case library.UpdateCurrent:
			writeLocalSkill(t, source, "old")
		}
		_, err := checkSingleUpdate(t, lib, t.Context(), skill.ID)
		if (err != nil) != (status == library.UpdateFailed) {
			t.Fatalf("check error = %v for %s", err, status)
		}
		got := updateCheckState(t, lib, skill.ID)
		if got == nil || got.Status != status || got.CheckedAt.Before(before) {
			t.Fatalf("state = %+v, want %s", got, status)
		}
	}
	if err := lib.RemoveSkill(t.Context(), skill.ID, library.RemoveSkillOptions{}); err != nil {
		t.Fatal(err)
	}
	if skills, err := lib.ListSkills(t.Context()); err != nil || len(skills) != 0 {
		t.Fatalf("after removal = %+v, %v", skills, err)
	}
	skill, err := fetchLocal(t.Context(), lib, source)
	if err != nil {
		t.Fatal(err)
	}
	if got := updateCheckState(t, lib, skill.ID); got != nil {
		t.Fatalf("reinstalled check = %+v", got)
	}
}

func TestUpdateCheckStateSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")
	lib, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
	unchecked := fetchNamedSkill(t, lib, "unchecked")
	current := fetchNamedSkill(t, lib, "current")
	failed, missing := fetchLocalSkill(t, lib, "failed", "old")
	if err := os.Rename(missing, missing+"-offline"); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.CheckUpdates(t.Context(), []library.SkillID{current.ID, failed.ID}); err != nil {
		t.Fatal(err)
	}
	currentState := updateCheckState(t, lib, current.ID)
	failedState := updateCheckState(t, lib, failed.ID)
	if currentState == nil || currentState.Status != library.UpdateCurrent || failedState == nil || failedState.Status != library.UpdateFailed {
		t.Fatalf("current = %+v, failed = %+v", currentState, failedState)
	}
	skill, source := fetchLocalSkill(t, lib, "example", "old")
	if got := updateCheckState(t, lib, skill.ID); got != nil {
		t.Fatalf("unchecked = %+v", got)
	}
	writeLocalSkill(t, source, "new")
	start := time.Now()
	available, err := checkSingleUpdate(t, lib, t.Context(), skill.ID)
	if err != nil || !available {
		t.Fatalf("check = %v, %v", available, err)
	}
	want := updateCheckState(t, lib, skill.ID)
	if want == nil || want.Status != library.UpdateAvailable || want.CheckedAt.Before(start) || want.CheckedAt.After(time.Now()) {
		t.Fatalf("state = %+v", want)
	}
	if err := lib.Close(); err != nil {
		t.Fatal(err)
	}
	lib, err = library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := updateCheckState(t, lib, skill.ID); !reflect.DeepEqual(got, want) {
		t.Fatalf("reopened = %+v, want %+v", got, want)
	}
	for id, state := range map[library.SkillID]*library.UpdateCheckState{unchecked.ID: nil, current.ID: currentState, failed.ID: failedState} {
		if got := updateCheckState(t, lib, id); !reflect.DeepEqual(got, state) {
			t.Fatalf("reopened %d = %+v, want %+v", id, got, state)
		}
	}
	group, err := lib.CreateGroup(t.Context(), "checks")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.AddSkillToGroup(t.Context(), group.ID, skill.ID); err != nil {
		t.Fatal(err)
	}
	members, err := lib.ListGroupSkills(t.Context(), group.ID)
	if err != nil || len(members) != 1 || !reflect.DeepEqual(members[0].LastUpdateCheck, want) {
		t.Fatalf("group checks = %+v, %v", members, err)
	}
	collections, err := lib.ListCollections(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, collection := range collections {
		for _, summary := range collection.Skills {
			if got := updateCheckState(t, lib, summary.ID); !reflect.DeepEqual(summary.LastUpdateCheck, got) {
				t.Fatalf("collection check = %+v, want %+v", summary.LastUpdateCheck, got)
			}
		}
	}
}

func TestInFlightUpdateCheckDoesNotOverwriteUpdatedSkill(t *testing.T) {
	for _, name := range []string{"changed", "unchanged"} {
		changed := name == "changed"
		t.Run(name, func(t *testing.T) {
			lib := openTestLibrary(t)
			gate := newNetworkGate()
			old := zipArchive(t, map[string]string{"demo/SKILL.md": skillDocument("demo", "old")})
			var archive atomic.Pointer[[]byte]
			archive.Store(&old)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				snapshot := archive.Load()
				gate.pass()
				_, _ = w.Write(*snapshot)
			}))
			t.Cleanup(server.Close)
			skill := fetchURLSkill(t, lib, server.URL, "demo")
			if _, err := checkSingleUpdate(t, lib, t.Context(), skill.ID); err != nil {
				t.Fatal(err)
			}
			latest := zipArchive(t, map[string]string{"demo/SKILL.md": skillDocument("demo", "new")})
			archive.Store(&latest)
			gate.arm()
			var wg sync.WaitGroup
			var checks []library.UpdateCheck
			var checkErr error
			wg.Go(func() { checks, checkErr = lib.CheckUpdates(t.Context(), []library.SkillID{skill.ID}) })
			gate.awaitEntered(t)
			gate.disarm()
			if !changed {
				archive.Store(&old)
			}
			_, err := lib.Update(t.Context(), skill.ID)
			gate.open()
			wg.Wait()
			if err != nil {
				t.Fatal(err)
			}
			if checkErr != nil || len(checks) != 1 || checks[0].Error == nil || !library.IsCode(checks[0].Error, library.Conflict) {
				t.Fatalf("racing check = %+v, %v", checks, checkErr)
			}
			if got := updateCheckState(t, lib, skill.ID); got == nil || got.Status != library.UpdateCurrent {
				t.Fatalf("updated state = %+v", got)
			}
		})
	}
}

func TestOverlappingCheckSuccessReplacesConcurrentFailure(t *testing.T) {
	lib := openTestLibrary(t)
	gate := newNetworkGate()
	var failing atomic.Bool
	archive := zipArchive(t, map[string]string{"demo/SKILL.md": skillDocument("demo", "old")})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failing.Load() {
			http.Error(w, "offline", http.StatusServiceUnavailable)
			return
		}
		snapshot := archive
		gate.pass()
		_, _ = w.Write(snapshot)
	}))
	t.Cleanup(server.Close)
	skill := fetchURLSkill(t, lib, server.URL, "demo")
	archive = zipArchive(t, map[string]string{"demo/SKILL.md": skillDocument("demo", "new")})
	gate.arm()
	var wg sync.WaitGroup
	var checks []library.UpdateCheck
	var checkErr error
	wg.Go(func() { checks, checkErr = lib.CheckUpdates(t.Context(), []library.SkillID{skill.ID}) })
	gate.awaitEntered(t)
	gate.disarm()
	failing.Store(true)
	if _, err := checkSingleUpdate(t, lib, t.Context(), skill.ID); err == nil {
		t.Fatal("concurrent check should fail")
	}
	gate.open()
	wg.Wait()
	if checkErr != nil || len(checks) != 1 || checks[0].Error != nil || !checks[0].Available {
		t.Fatalf("overlapping check = %+v, %v", checks, checkErr)
	}
	if got := updateCheckState(t, lib, skill.ID); got == nil || got.Status != library.UpdateAvailable {
		t.Fatalf("state = %+v", got)
	}
}
