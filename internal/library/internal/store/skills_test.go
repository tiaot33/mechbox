package store

import (
	"path/filepath"
	"testing"
	"time"
)

// 检查更新写回结论的规则:开始读来源之后别处写过结论、或技能换了版本,就不写;别处写下的是检查失败时照样写。
// 检查失败只记失败,之前读成来源的那次结论留着。
func TestConcludeCheckKeepsNewerConclusionsButOverwritesFailures(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "library.db")
	writeOldestLibrary(t, path)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	const skillID = 1
	read, err := s.GetCurrent(ctx, skillID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 16, 8, 0, 0, 0, time.UTC)
	conclude := func(versionID, serial int64, check UpdateCheck) bool {
		t.Helper()
		var written bool
		if err := s.InTx(ctx, func(tx *Tx) error {
			var err error
			written, err = tx.ConcludeCheck(ctx, skillID, versionID, serial, check)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return written
	}
	stored := func() UpdateCheck {
		t.Helper()
		var check UpdateCheck
		if err := s.InTx(ctx, func(tx *Tx) error {
			var err error
			check, err = tx.UpdateCheck(ctx, skillID)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		check.At = time.Time{}
		return check
	}
	seen := UpdateCheck{Status: "available", Revision: "rev-3"}
	if got := stored(); got != seen {
		t.Fatalf("stored before = %+v; want %+v", got, seen)
	}

	if !conclude(read.VersionID, read.CheckSerial, UpdateCheck{At: now, Failed: true}) {
		t.Fatal("failed check was not written")
	}
	failed := seen
	failed.Failed = true
	if got := stored(); got != failed {
		t.Fatalf("stored after a failed check = %+v; want %+v", got, failed)
	}
	if skill, err := s.GetSkill(ctx, skillID); err != nil || !skill.CheckFailed || !skill.CheckedAt.Equal(now) {
		t.Fatalf("skill after a failed check = %+v, %v; want it marked failed at %v", skill, err, now)
	}

	current := UpdateCheck{At: now, Status: "current", Revision: "rev-4", Found: true}
	if !conclude(read.VersionID, read.CheckSerial, current) {
		t.Fatal("a check that began before the failure did not overwrite it")
	}
	want := UpdateCheck{Status: "current", Revision: "rev-4"}
	if got := stored(); got != want {
		t.Fatalf("stored after overwriting the failure = %+v; want %+v", got, want)
	}

	if conclude(read.VersionID, read.CheckSerial, UpdateCheck{At: now, Failed: true}) {
		t.Fatal("a check that began before a newer conclusion overwrote it")
	}
	fresh, err := s.GetCurrent(ctx, skillID)
	if err != nil {
		t.Fatal(err)
	}
	if conclude(fresh.VersionID+1, fresh.CheckSerial, UpdateCheck{At: now, Failed: true}) {
		t.Fatal("a check of another version was written")
	}
	if got := stored(); got != want {
		t.Fatalf("stored at the end = %+v; want %+v", got, want)
	}
}
