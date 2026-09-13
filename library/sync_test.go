package library_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tiaot33/skills-tui/library"
)

// s3Stub 是一个内存 S3 打桩,记录当前对象内容并按内容哈希给出 ETag。
// 它兑现条件写(If-Match / If-None-Match),据此模拟并发写者与前置条件失败(412)。
type s3Stub struct {
	stored []byte
	etag   string
	// afterHead 若非 nil,在一次 HEAD 响应发出后触发一次(随即清空),
	// 用来在"预检 HEAD"与"随后 PUT"之间注入一次远端改动,复现 TOCTOU 窗口。
	afterHead func()
}

func (s *s3Stub) set(data []byte) {
	s.stored = data
	sum := sha256.Sum256(data)
	s.etag = `"` + hex.EncodeToString(sum[:]) + `"`
}

func newS3Stub(t *testing.T) *s3Stub {
	t.Helper()
	stub := &s3Stub{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
			http.Error(w, "unsigned", http.StatusForbidden)
			return
		}
		switch r.Method {
		case http.MethodPut:
			data, _ := io.ReadAll(r.Body)
			if r.Header.Get("If-None-Match") == "*" && len(stub.stored) > 0 {
				http.Error(w, "exists", http.StatusPreconditionFailed)
				return
			}
			if match := r.Header.Get("If-Match"); match != "" && match != stub.etag {
				http.Error(w, "changed", http.StatusPreconditionFailed)
				return
			}
			stub.set(data)
			w.Header().Set("ETag", stub.etag)
		case http.MethodGet, http.MethodHead:
			if len(stub.stored) == 0 {
				http.Error(w, "missing", http.StatusNotFound)
				return
			}
			w.Header().Set("ETag", stub.etag)
			if r.Method == http.MethodGet {
				_, _ = w.Write(stub.stored)
			} else if stub.afterHead != nil {
				hook := stub.afterHead
				stub.afterHead = nil
				hook()
			}
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
	return stub
}

func TestSyncPreviewAndPushPullRoundTrip(t *testing.T) {
	stub := newS3Stub(t)
	ctx := t.Context()
	lib := openTestLibrary(t)
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

	// 推送前预览:远端尚无对象。
	pushPreview, err := lib.PreviewSyncPush(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if pushPreview.RemoteExists || pushPreview.Remote != "" {
		t.Fatalf("PreviewSyncPush() = %#v, want no remote object", pushPreview)
	}
	if len(stub.stored) != 0 {
		t.Fatal("push preview uploaded a database")
	}
	pushResult, err := lib.SyncPush(ctx, pushPreview.Remote)
	if err != nil {
		t.Fatal(err)
	}
	if len(stub.stored) == 0 {
		t.Fatal("SyncPush uploaded no database")
	}
	if pushResult.Remote == "" || string(pushResult.Remote) != stub.etag {
		t.Fatalf("SyncPush() result = %#v, want new remote identity %q", pushResult, stub.etag)
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

	// 拉取前预览:报告远端身份与 schema 可接受,并带投影可能过时的警告。
	pullPreview, err := lib.PreviewSyncPull(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !pullPreview.RemoteExists || string(pullPreview.Remote) != stub.etag || !pullPreview.SchemaOK {
		t.Fatalf("PreviewSyncPull() = %#v, want existing remote with acceptable schema", pullPreview)
	}
	if len(pullPreview.Warnings) == 0 {
		t.Fatal("PreviewSyncPull() carried no projection warning")
	}
	beforePull, err := lib.ListSkills(ctx)
	if err != nil || len(beforePull) != 1 || beforePull[0].Name != "extra" {
		t.Fatalf("pull preview replaced local library: %#v, %v", beforePull, err)
	}
	pullResult, err := lib.SyncPull(ctx, pullPreview.Remote)
	if err != nil {
		t.Fatal(err)
	}
	if string(pullResult.Remote) != stub.etag || len(pullResult.Warnings) == 0 {
		t.Fatalf("SyncPull() result = %#v, want remote identity and projection warning", pullResult)
	}

	for _, path := range []string{newProjectPath, newHome} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("pull rebuilt projection at %s: %v", path, err)
		}
	}
	skills, err := lib.ListSkills(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].Name != original.Name {
		t.Fatalf("skills after pull = %#v", skills)
	}
	full, err := lib.GetSkill(ctx, original.ID)
	if err != nil || string(full.Files[0].Content) != "updated\n" {
		t.Fatalf("GetSkill after pull = %#v, %v", full, err)
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
}

func TestSyncPullRefusesChangedOrInvalidRemote(t *testing.T) {
	stub := newS3Stub(t)
	ctx := t.Context()
	lib, err := library.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
	source := filepath.Join(t.TempDir(), "keeper")
	writeFile(t, filepath.Join(source, "SKILL.md"), []byte("keeper\n"), 0o644)
	skill, err := lib.Install(ctx, library.LocalSource{Path: source})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.SyncPush(ctx, ""); err != nil {
		t.Fatal(err)
	}

	// 预览拿到当前身份,随后远端在预览之后发生变化。
	preview, err := lib.PreviewSyncPull(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stub.set([]byte("different remote object"))
	if _, err := lib.SyncPull(ctx, preview.Remote); !library.IsCode(err, library.Conflict) {
		t.Fatalf("SyncPull() after remote change = %v, want Conflict", err)
	}
	if _, err := lib.GetSkill(ctx, skill.ID); err != nil {
		t.Fatalf("conflicting pull corrupted local library: %v", err)
	}

	// 身份匹配但内容不是合法库:预览标注 schema 不可接受,执行被拒且本地不受损。
	badPreview, err := lib.PreviewSyncPull(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if badPreview.SchemaOK {
		t.Fatalf("PreviewSyncPull() = %#v, want schema not acceptable", badPreview)
	}
	if _, err := lib.SyncPull(ctx, badPreview.Remote); err == nil {
		t.Fatal("SyncPull accepted an invalid database")
	}
	if _, err := lib.GetSkill(ctx, skill.ID); err != nil {
		t.Fatalf("invalid pull corrupted local library: %v", err)
	}

	// 身份是别的库文件(schema 身份不符)也应被拒。
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
	foreign, err := os.ReadFile(fakePath)
	if err != nil {
		t.Fatal(err)
	}
	stub.set(foreign)
	foreignPreview, err := lib.PreviewSyncPull(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.SyncPull(ctx, foreignPreview.Remote); err == nil {
		t.Fatal("SyncPull accepted a foreign SQLite database")
	}
	if _, err := lib.GetSkill(ctx, skill.ID); err != nil {
		t.Fatalf("foreign pull corrupted local library: %v", err)
	}
}

func TestSyncPushConflictsWhenRemoteChangesWithinWindow(t *testing.T) {
	stub := newS3Stub(t)
	ctx := t.Context()
	lib, err := library.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
	source := filepath.Join(t.TempDir(), "keeper")
	writeFile(t, filepath.Join(source, "SKILL.md"), []byte("keeper\n"), 0o644)
	if _, err := lib.Install(ctx, library.LocalSource{Path: source}); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.SyncPush(ctx, ""); err != nil {
		t.Fatal(err)
	}
	preview, err := lib.PreviewSyncPush(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// 预检 HEAD 仍看到预览时的身份(通过),但紧接着(PUT 之前)远端被并发写者改动。
	// 条件写(If-Match)必须捕获该窗口内的变化,拒绝覆盖。
	intruder := []byte("concurrent remote write")
	stub.afterHead = func() { stub.set(intruder) }
	if _, err := lib.SyncPush(ctx, preview.Remote); !library.IsCode(err, library.Conflict) {
		t.Fatalf("SyncPush() racing a concurrent remote write = %v, want Conflict", err)
	}
	if !bytes.Equal(stub.stored, intruder) {
		t.Fatalf("SyncPush overwrote the concurrently-written remote: stored = %q", stub.stored)
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

func TestSyncMapsFailuresAndMissingConfiguration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "denied", http.StatusForbidden)
	}))
	t.Setenv("SKILLS_S3_ENDPOINT", server.URL)
	t.Setenv("SKILLS_S3_BUCKET", "test-bucket")
	t.Setenv("AWS_REGION", "test-region")
	t.Setenv("AWS_ACCESS_KEY_ID", "test-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret")
	lib := openTestLibrary(t)
	if _, err := lib.SyncPush(t.Context(), ""); !library.IsCode(err, library.Permission) {
		t.Fatalf("permission error = %v", err)
	}
	server.Close()
	if _, err := lib.SyncPush(t.Context(), ""); !library.IsCode(err, library.SourceUnreachable) {
		t.Fatalf("unreachable error = %v", err)
	}

	// 缺少必要配置时报 NotConfigured,而非裸的 Internal。
	t.Setenv("SKILLS_S3_BUCKET", "")
	if _, err := lib.PreviewSyncPush(t.Context()); !library.IsCode(err, library.NotConfigured) {
		t.Fatalf("missing config error = %v, want NotConfigured", err)
	}
	if _, err := lib.SyncPull(t.Context(), ""); !library.IsCode(err, library.NotConfigured) {
		t.Fatalf("missing config pull error = %v, want NotConfigured", err)
	}
}

func TestSyncPushPreviewPreservesRemoteAndMatchingOverwriteSucceeds(t *testing.T) {
	stub := newS3Stub(t)
	lib, err := library.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
	ctx := t.Context()
	if _, err := lib.SyncPush(ctx, ""); err != nil {
		t.Fatal(err)
	}
	before := bytes.Clone(stub.stored)
	if _, err := lib.CreateGroup(ctx, "new-local-group"); err != nil {
		t.Fatal(err)
	}
	preview, err := lib.PreviewSyncPush(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !preview.RemoteExists || string(preview.Remote) != stub.etag || !bytes.Equal(before, stub.stored) {
		t.Fatalf("push preview changed remote or returned wrong identity: %#v", preview)
	}
	result, err := lib.SyncPush(ctx, preview.Remote)
	if err != nil {
		t.Fatal(err)
	}
	if result.Remote == preview.Remote || bytes.Equal(before, stub.stored) {
		t.Fatal("matching push did not update remote")
	}
	if _, err := lib.SyncPull(ctx, result.Remote); err != nil {
		t.Fatal(err)
	}
	groups, err := lib.ListGroups(ctx)
	if err != nil || len(groups) != 1 || groups[0].Name != "new-local-group" {
		t.Fatalf("pushed library groups = %#v, %v", groups, err)
	}
}

func TestSyncRejectsStalePreviewAfterRemoteCreationChangeOrDeletion(t *testing.T) {
	stub := newS3Stub(t)
	lib, err := library.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
	ctx := t.Context()
	preview, err := lib.PreviewSyncPush(ctx)
	if err != nil {
		t.Fatal(err)
	}
	intruder := []byte("another writer")
	stub.set(intruder)
	if _, err := lib.SyncPush(ctx, preview.Remote); !library.IsCode(err, library.Conflict) {
		t.Fatalf("push after creation = %v, want Conflict", err)
	}
	preview, err = lib.PreviewSyncPush(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stub.set([]byte("changed writer"))
	before := bytes.Clone(stub.stored)
	if _, err := lib.SyncPush(ctx, preview.Remote); !library.IsCode(err, library.Conflict) {
		t.Fatalf("push after change = %v, want Conflict", err)
	}
	if !bytes.Equal(before, stub.stored) {
		t.Fatal("stale push changed remote")
	}
	stub.stored, stub.etag = nil, ""
	if _, err := lib.SyncPush(ctx, preview.Remote); !library.IsCode(err, library.Conflict) {
		t.Fatalf("push after deletion = %v, want Conflict", err)
	}
	if _, err := lib.SyncPull(ctx, preview.Remote); !library.IsCode(err, library.Conflict) {
		t.Fatalf("pull after deletion = %v, want Conflict", err)
	}
}

func TestAllSyncOperationsReportMissingConfiguration(t *testing.T) {
	newS3Stub(t)
	lib, err := library.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
	for _, key := range []string{"SKILLS_S3_BUCKET", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, "")
			_, pushPreviewErr := lib.PreviewSyncPush(t.Context())
			_, pullPreviewErr := lib.PreviewSyncPull(t.Context())
			_, pushErr := lib.SyncPush(t.Context(), "")
			_, pullErr := lib.SyncPull(t.Context(), "")
			for name, err := range map[string]error{"push preview": pushPreviewErr, "pull preview": pullPreviewErr, "push": pushErr, "pull": pullErr} {
				if !library.IsCode(err, library.NotConfigured) {
					t.Errorf("%s = %v, want NotConfigured", name, err)
				}
			}
		})
	}
}

func TestSyncRejectsExistingRemoteWithoutIdentity(t *testing.T) {
	stub := newS3Stub(t)
	lib, err := library.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
	ctx := t.Context()
	if _, err := lib.CreateGroup(ctx, "local-group"); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.SyncPush(ctx, ""); err != nil {
		t.Fatal(err)
	}
	before := bytes.Clone(stub.stored)
	stub.etag = ""
	_, pushPreviewErr := lib.PreviewSyncPush(ctx)
	_, pullPreviewErr := lib.PreviewSyncPull(ctx)
	_, pushErr := lib.SyncPush(ctx, "")
	_, pullErr := lib.SyncPull(ctx, "")
	for name, err := range map[string]error{"push preview": pushPreviewErr, "pull preview": pullPreviewErr, "push": pushErr, "pull": pullErr} {
		if !library.IsCode(err, library.SourceUnreachable) {
			t.Errorf("%s without remote identity = %v, want SourceUnreachable", name, err)
		}
	}
	if !bytes.Equal(before, stub.stored) {
		t.Fatal("unidentified remote was overwritten")
	}
	groups, err := lib.ListGroups(ctx)
	if err != nil || len(groups) != 1 || groups[0].Name != "local-group" {
		t.Fatalf("local library changed: %#v, %v", groups, err)
	}
}

func TestPullPreviewReturnsTemporaryFileFailure(t *testing.T) {
	newS3Stub(t)
	lib := openTestLibrary(t)
	if _, err := lib.SyncPush(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	// 在远端有效时制造本地暂存失败,不能把它伪装成 schema 不可接受。
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
	if preview, err := lib.PreviewSyncPull(t.Context()); err == nil {
		t.Fatalf("temporary file failure became successful preview: %#v", preview)
	}
}

func TestPullPreviewPreservesCancellationDuringValidation(t *testing.T) {
	newS3Stub(t)
	lib := openTestLibrary(t)
	if _, err := lib.SyncPush(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	temp := t.TempDir()
	t.Setenv("TMPDIR", temp)
	base, cancel := context.WithCancel(t.Context())
	defer cancel()
	// 下载完成并写入暂存库后才取消,确保覆盖校验阶段而非 HTTP 请求阶段。
	fault := cancelAfterProjection{Context: base, cancel: cancel, changed: func() bool {
		entries, _ := os.ReadDir(temp)
		return len(entries) > 0
	}}
	if preview, err := lib.PreviewSyncPull(fault); !errors.Is(err, context.Canceled) {
		t.Fatalf("validation cancellation = %#v, %v; want context.Canceled", preview, err)
	}
	if entries, err := os.ReadDir(temp); err != nil || len(entries) != 0 {
		t.Fatalf("temporary database not cleaned up: %v, %v", entries, err)
	}
}
