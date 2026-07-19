package library_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tiaot33/skills-tui/library"
)

func TestSyncPushThenPullReplacesLibrary(t *testing.T) {
	var stored []byte
	var escapedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		escapedPath = r.URL.EscapedPath()
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
	t.Setenv("SKILLS_S3_KEY", "backups/test$file+name.db")
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
	writeFile(t, filepath.Join(originalSource, "SKILL.md"), []byte("updated\n"), 0o644)
	if _, err := lib.Update(ctx, original.ID); err != nil {
		t.Fatal(err)
	}
	group, err := lib.CreateGroup(ctx, "synced-group")
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.AddSkillToGroup(ctx, group.ID, original.ID); err != nil {
		t.Fatal(err)
	}
	oldProjectPath := filepath.Join(t.TempDir(), "old-project")
	if err := os.MkdirAll(oldProjectPath, 0o755); err != nil {
		t.Fatal(err)
	}
	project, err := lib.RegisterProject(ctx, oldProjectPath)
	if err != nil {
		t.Fatal(err)
	}
	projectScope := library.Scope{Agent: library.AgentCodex, Path: filepath.Join(project.Path, ".codex", "skills")}
	if err := lib.ActivateGroup(ctx, group.ID, projectScope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	oldHome := filepath.Join(t.TempDir(), "old-home")
	t.Setenv("HOME", oldHome)
	globalScope := library.Scope{Agent: library.AgentCodex, Path: filepath.Join(oldHome, ".codex", "skills")}
	if err := lib.Activate(ctx, original.ID, globalScope, library.ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := lib.SyncPush(ctx); err != nil {
		t.Fatal(err)
	}
	if len(stored) == 0 {
		t.Fatal("SyncPush uploaded no database")
	}
	if escapedPath != "/test-bucket/backups/test%24file%2Bname.db" {
		t.Fatalf("S3 escaped path = %q", escapedPath)
	}

	if err := lib.RemoveSkill(ctx, original.ID, library.RemoveSkillOptions{}); err != nil {
		t.Fatal(err)
	}
	extraSource := filepath.Join(t.TempDir(), "extra")
	writeFile(t, filepath.Join(extraSource, "SKILL.md"), []byte("extra\n"), 0o644)
	if _, err := lib.Install(ctx, library.LocalSource{Path: extraSource}); err != nil {
		t.Fatal(err)
	}
	newProjectPath := filepath.Join(t.TempDir(), "new-project")
	newHome := filepath.Join(t.TempDir(), "new-home")
	t.Setenv("HOME", newHome)
	mapping, _ := json.Marshal(map[string]string{project.Path: newProjectPath})
	t.Setenv("SKILLS_PROJECT_PATH_MAP", string(mapping))
	if err := lib.SyncPull(ctx); err != nil {
		t.Fatal(err)
	}

	skills, err := lib.ListSkills(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].Name != original.Name || string(skills[0].Files[0].Content) != "updated\n" {
		t.Fatalf("skills after pull = %#v", skills)
	}
	versions, err := lib.ListVersions(ctx, original.ID)
	if err != nil || len(versions) != 2 {
		t.Fatalf("versions after pull = %#v, %v", versions, err)
	}
	groupSkills, err := lib.ListGroupSkills(ctx, group.ID)
	if err != nil || len(groupSkills) != 1 || groupSkills[0].ID != original.ID {
		t.Fatalf("group after pull = %#v, %v", groupSkills, err)
	}
	projects, err := lib.ListRegisteredProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].Path != newProjectPath {
		t.Fatalf("projects after pull = %#v", projects)
	}
	activations, err := lib.ListActivations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	activationPaths := map[string]bool{}
	for _, activation := range activations {
		activationPaths[activation.Scope.Path] = true
	}
	if len(activations) != 2 || !activationPaths[filepath.Join(newProjectPath, ".codex", "skills")] || !activationPaths[filepath.Join(newHome, ".codex", "skills")] {
		t.Fatalf("activations after pull = %#v", activations)
	}
	stored = []byte("not sqlite")
	if err := lib.SyncPull(ctx); err == nil {
		t.Fatal("SyncPull accepted an invalid database")
	}
	if _, err := lib.GetSkill(ctx, original.ID); err != nil {
		t.Fatalf("invalid pull corrupted local library: %v", err)
	}
	fakePath := filepath.Join(t.TempDir(), "fake.db")
	fake, err := sql.Open("sqlite3", fakePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fake.Exec(`PRAGMA application_id = 1397443650; PRAGMA user_version = 1; CREATE TABLE skills (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if err := fake.Close(); err != nil {
		t.Fatal(err)
	}
	stored, err = os.ReadFile(fakePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.SyncPull(ctx); err == nil {
		t.Fatal("SyncPull accepted a foreign SQLite database")
	}
	if _, err := lib.GetSkill(ctx, original.ID); err != nil {
		t.Fatalf("foreign pull corrupted local library: %v", err)
	}
}

func TestOpenRejectsForeignDatabaseWithoutMutatingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "foreign.db")
	foreign, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := foreign.Exec(`CREATE TABLE notes (body TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err := foreign.Close(); err != nil {
		t.Fatal(err)
	}
	if lib, err := library.Open(path); err == nil {
		_ = lib.Close()
		t.Fatal("Open accepted a foreign database")
	}
	foreign, err = sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer foreign.Close()
	var applicationID, notes, libraryTables int
	if err := foreign.QueryRow(`PRAGMA application_id`).Scan(&applicationID); err != nil {
		t.Fatal(err)
	}
	if err := foreign.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'notes'`).Scan(&notes); err != nil {
		t.Fatal(err)
	}
	if err := foreign.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name IN ('skills', 'sources')`).Scan(&libraryTables); err != nil {
		t.Fatal(err)
	}
	if applicationID != 0 || notes != 1 || libraryTables != 0 {
		t.Fatalf("foreign database mutated: application_id=%d notes=%d library_tables=%d", applicationID, notes, libraryTables)
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
