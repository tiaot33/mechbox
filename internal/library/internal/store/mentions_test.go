package store

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// 最早支持升级的旧数据库升级后有存判断结论的表:当前版本的结论能写能读,写给不存在的版本报 ErrNotFound。
func TestUpgradedLibraryStoresMentionJudgments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")
	writeOldestLibrary(t, path)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var version int64
	if err := s.db.QueryRowContext(t.Context(), `SELECT current_version_id FROM skills WHERE current_version_id IS NOT NULL LIMIT 1`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMentionJudgments(t.Context(), version, map[string]bool{"review": true, "spec": false}, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, err := s.MentionJudgments(t.Context())
	if want := map[int64]map[string]bool{version: {"review": true, "spec": false}}; err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("MentionJudgments = %v, %v; want %v", got, err, want)
	}
	if err := s.SetMentionJudgments(t.Context(), 99999, map[string]bool{"review": true}, time.Now()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetMentionJudgments on a missing version = %v; want ErrNotFound", err)
	}
}
