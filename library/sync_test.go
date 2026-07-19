package library_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tiaot33/skills-tui/library"
)

func TestSyncPushThenPullReplacesLibrary(t *testing.T) {
	var stored []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
			http.Error(w, "unsigned", http.StatusForbidden)
			return
		}
		switch r.Method {
		case http.MethodPut:
			stored, _ = io.ReadAll(r.Body)
		case http.MethodGet:
			_, _ = w.Write(stored)
		default:
			http.Error(w, "method", http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("SKILLS_S3_ENDPOINT", server.URL)
	t.Setenv("SKILLS_S3_BUCKET", "test-bucket")
	t.Setenv("SKILLS_S3_KEY", "backups/library.db")
	t.Setenv("AWS_REGION", "test-region")
	t.Setenv("AWS_ACCESS_KEY_ID", "test-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret")

	ctx := context.Background()
	lib, err := library.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
	originalSource := filepath.Join(t.TempDir(), "original")
	writeFile(t, filepath.Join(originalSource, "SKILL.md"), []byte("original\n"), 0o644)
	original, err := lib.Install(ctx, library.LocalSource{Path: originalSource})
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.SyncPush(ctx); err != nil {
		t.Fatal(err)
	}
	if len(stored) == 0 {
		t.Fatal("SyncPush uploaded no database")
	}

	if err := lib.RemoveSkill(ctx, original.ID, library.RemoveSkillOptions{}); err != nil {
		t.Fatal(err)
	}
	extraSource := filepath.Join(t.TempDir(), "extra")
	writeFile(t, filepath.Join(extraSource, "SKILL.md"), []byte("extra\n"), 0o644)
	if _, err := lib.Install(ctx, library.LocalSource{Path: extraSource}); err != nil {
		t.Fatal(err)
	}
	if err := lib.SyncPull(ctx); err != nil {
		t.Fatal(err)
	}

	skills, err := lib.ListSkills(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].Name != original.Name || string(skills[0].Files[0].Content) != "original\n" {
		t.Fatalf("skills after pull = %#v", skills)
	}
	stored = []byte("not sqlite")
	if err := lib.SyncPull(ctx); err == nil {
		t.Fatal("SyncPull accepted an invalid database")
	}
	if _, err := lib.GetSkill(ctx, original.ID); err != nil {
		t.Fatalf("invalid pull corrupted local library: %v", err)
	}
}

func TestSyncMapsS3Failures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "denied", http.StatusForbidden)
	}))
	t.Setenv("SKILLS_S3_ENDPOINT", server.URL)
	t.Setenv("SKILLS_S3_BUCKET", "test-bucket")
	t.Setenv("AWS_REGION", "test-region")
	t.Setenv("AWS_ACCESS_KEY_ID", "test-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret")
	lib, err := library.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
	if err := lib.SyncPush(context.Background()); !library.IsCode(err, library.Permission) {
		t.Fatalf("permission error = %v", err)
	}
	server.Close()
	if err := lib.SyncPush(context.Background()); !library.IsCode(err, library.SourceUnreachable) {
		t.Fatalf("unreachable error = %v", err)
	}
}
