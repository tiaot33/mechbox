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
	"reflect"
	"strings"
	"testing"

	"github.com/tiaot33/skills-tui/internal/library"
)

// s3Stub 是一个内存 S3 打桩,记录当前对象内容并按内容哈希给出 ETag。
// 它兑现条件写(If-Match / If-None-Match),据此模拟并发写者与前置条件失败(412)。
type s3Stub struct {
	stored []byte
	etag   string
	// afterHead 若非 nil,在一次 HEAD 响应发出后触发一次(随即清空),
	// 用来在"HEAD 查询"与"随后 PUT"之间注入一次远端改动,复现 TOCTOU 窗口。
	afterHead func()
	// gate 若非 nil,gateMethod 方法的请求在处理前先过闸;用来把 push / plan 停在网络上。
	gate       *networkGate
	gateMethod string
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
		if stub.gate != nil && r.Method == stub.gateMethod {
			stub.gate.pass()
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

func TestSyncPlanAndPushPullRoundTrip(t *testing.T) {
	stub := newS3Stub(t)
	ctx := t.Context()
	oldHome := filepath.Join(t.TempDir(), "old-home")
	if err := os.MkdirAll(oldHome, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", oldHome)
	lib := openTestLibrary(t)
	originalSource := filepath.Join(t.TempDir(), "original")
	writeLocalSkill(t, originalSource, "original\n")
	original, err := fetchLocalDir(ctx, lib, originalSource)
	if err != nil {
		t.Fatal(err)
	}
	writeLocalSkill(t, originalSource, "updated\n")
	if _, err := lib.Update(ctx, original.ID); err != nil {
		t.Fatal(err)
	}
	group, err := lib.CreateGroup(ctx, "synced-group")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.AddSkillToGroup(ctx, group.ID, original.ID); err != nil {
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
	projectScope := library.ProjectScope(library.AgentCodex, project.ID)
	if _, err := lib.InstallGroup(ctx, group.ID, projectScope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	globalScope := library.GlobalScope(library.AgentCodex)
	if err := lib.Install(ctx, original.ID, globalScope, library.InstallOptions{}); err != nil {
		t.Fatal(err)
	}

	// 推送前的同步计划:远端尚无对象。
	pushPlan, err := lib.PlanSyncPush(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if pushPlan.RemoteExists || pushPlan.Remote != "" {
		t.Fatalf("PlanSyncPush() = %#v, want no remote object", pushPlan)
	}
	if len(stub.stored) != 0 {
		t.Fatal("push plan uploaded a database")
	}
	pushResult, err := lib.SyncPush(ctx, pushPlan.Remote)
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
	writeLocalSkill(t, extraSource, "extra\n")
	if _, err := fetchLocalDir(ctx, lib, extraSource); err != nil {
		t.Fatal(err)
	}
	newProjectPath := filepath.Join(t.TempDir(), "new-project")
	newHome := filepath.Join(t.TempDir(), "new-home")
	t.Setenv("HOME", newHome)
	mapping, _ := json.Marshal(map[string]string{project.Path: newProjectPath})
	t.Setenv("SKILLS_PROJECT_PATH_MAP", string(mapping))

	// 拉取前的同步计划:报告远端身份与 schema 可接受,并带已安装文件夹可能与新库不一致的警告。
	pullPlan, err := lib.PlanSyncPull(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !pullPlan.RemoteExists || string(pullPlan.Remote) != stub.etag || !pullPlan.SchemaOK {
		t.Fatalf("PlanSyncPull() = %#v, want existing remote with acceptable schema", pullPlan)
	}
	if len(pullPlan.Warnings) == 0 {
		t.Fatal("PlanSyncPull() carried no skill scope warning")
	}
	beforePull, err := lib.ListSkills(ctx)
	if err != nil || len(beforePull) != 1 || beforePull[0].Name != "extra" {
		t.Fatalf("pull plan replaced local library: %#v, %v", beforePull, err)
	}
	pullResult, err := lib.SyncPull(ctx, pullPlan.Remote)
	if err != nil {
		t.Fatal(err)
	}
	if string(pullResult.Remote) != stub.etag || len(pullResult.Warnings) == 0 {
		t.Fatalf("SyncPull() result = %#v, want remote identity and skill scope warning", pullResult)
	}

	for _, path := range []string{newProjectPath, newHome} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("pull rebuilt skill scope at %s: %v", path, err)
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
	if err != nil || string(full.Files[0].Content) != skillDocument(original.Name, "updated\n") {
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
	// SkillScope 只记 Scope 身份:拉库后全局按本机 $HOME、项目按映射后的根重新派生目录,
	// 不携带上一台机器的绝对路径。
	skillScopes, err := lib.ListSkillScopes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantSkillScopes := []library.SkillScope{
		{SkillID: original.ID, Scope: globalScope, Directory: filepath.Join(newHome, ".agents", "skills"), Direct: true},
		{SkillID: original.ID, Scope: projectScope, Directory: filepath.Join(newProjectPath, ".agents", "skills"), Groups: []library.GroupID{group.ID}},
	}
	if !reflect.DeepEqual(withoutCreatedAt(skillScopes), wantSkillScopes) {
		t.Fatalf("skill scopes after pull = %#v, want %#v", skillScopes, wantSkillScopes)
	}
	for _, want := range wantSkillScopes {
		if dir, err := lib.Resolve(ctx, want.Scope); err != nil || dir != want.Directory {
			t.Fatalf("Resolve(%#v) after pull = %q, %v; want %q", want.Scope, dir, err, want.Directory)
		}
	}
}

// 映射只按拉回库里记录的原路径查一次,不会把已改写的结果再拿去匹配:
// {A→B, B→C} 下 A 的项目落在 B、B 的项目落在 C,与 map 迭代顺序无关(重复 pull 给随机性多次机会)。
// 目标 C 有意不在磁盘上创建:项目路径是 advisory 值,重映射不校验目标目录存在。
func TestSyncPullRemapsEachProjectOnceByItsOriginalPath(t *testing.T) {
	newS3Stub(t)
	ctx := t.Context()
	lib := openTestLibrary(t)
	roots := make([]string, 3)
	for i := range roots {
		roots[i] = filepath.Join(t.TempDir(), "project")
	}
	pathA, pathB, pathC := roots[0], roots[1], roots[2]
	for _, root := range []string{pathA, pathB} {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	projectA, err := lib.RegisterProject(ctx, pathA)
	if err != nil {
		t.Fatal(err)
	}
	projectB, err := lib.RegisterProject(ctx, pathB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.SyncPush(ctx, ""); err != nil {
		t.Fatal(err)
	}
	mapping, _ := json.Marshal(map[string]string{projectA.Path: projectB.Path, projectB.Path: pathC})
	t.Setenv("SKILLS_PROJECT_PATH_MAP", string(mapping))

	for range 5 {
		plan, err := lib.PlanSyncPull(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := lib.SyncPull(ctx, plan.Remote); err != nil {
			t.Fatal(err)
		}
		projects, err := lib.ListRegisteredProjects(ctx)
		if err != nil {
			t.Fatal(err)
		}
		wantProjects := []library.RegisteredProject{{ID: projectA.ID, Path: projectB.Path}, {ID: projectB.ID, Path: pathC}}
		if !reflect.DeepEqual(projects, wantProjects) {
			t.Fatalf("projects after chained remap = %#v, want %#v", projects, wantProjects)
		}
		for _, want := range []struct {
			scope library.Scope
			dir   string
		}{
			{library.ProjectScope(library.AgentCodex, projectA.ID), filepath.Join(projectB.Path, ".agents", "skills")},
			{library.ProjectScope(library.AgentClaudeCode, projectB.ID), filepath.Join(pathC, ".claude", "skills")},
		} {
			if dir, err := lib.Resolve(ctx, want.scope); err != nil || dir != want.dir {
				t.Fatalf("Resolve(%#v) after pull = %q, %v; want %q", want.scope, dir, err, want.dir)
			}
		}
	}
}

// 映射成环(A↔B 互换)或把目标指向仍被其他项目占着的路径是配置错误:
// pull 返回 InvalidArgument,本地库保持拉取前状态。
func TestSyncPullRejectsCyclicProjectPathMappingWithoutTouchingLocalLibrary(t *testing.T) {
	newS3Stub(t)
	ctx := t.Context()
	lib := openTestLibrary(t)
	pathA := filepath.Join(t.TempDir(), "project")
	pathB := filepath.Join(t.TempDir(), "project")
	for _, root := range []string{pathA, pathB} {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	projectA, err := lib.RegisterProject(ctx, pathA)
	if err != nil {
		t.Fatal(err)
	}
	projectB, err := lib.RegisterProject(ctx, pathB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.SyncPush(ctx, ""); err != nil {
		t.Fatal(err)
	}
	// 拉取前本地库继续演进:成功的 pull 会把它替换掉,失败的 pull 必须原样保留。
	localSource := filepath.Join(t.TempDir(), "local-only")
	writeLocalSkill(t, localSource, "local\n")
	if _, err := fetchLocalDir(ctx, lib, localSource); err != nil {
		t.Fatal(err)
	}
	mapping, _ := json.Marshal(map[string]string{projectA.Path: projectB.Path, projectB.Path: projectA.Path})
	t.Setenv("SKILLS_PROJECT_PATH_MAP", string(mapping))

	plan, err := lib.PlanSyncPull(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.SyncPull(ctx, plan.Remote); !library.IsCode(err, library.InvalidArgument) {
		t.Fatalf("SyncPull() with cyclic mapping = %v, want InvalidArgument", err)
	}
	skills, err := lib.ListSkills(ctx)
	if err != nil || len(skills) != 1 || skills[0].Name != "local-only" {
		t.Fatalf("rejected pull replaced local library: %#v, %v", skills, err)
	}
	projects, err := lib.ListRegisteredProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := []library.RegisteredProject{projectA, projectB}; !reflect.DeepEqual(projects, want) {
		t.Fatalf("rejected pull changed projects: %#v, want %#v", projects, want)
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
	writeLocalSkill(t, source, "keeper\n")
	skill, err := fetchLocalDir(ctx, lib, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.SyncPush(ctx, ""); err != nil {
		t.Fatal(err)
	}

	// 同步计划拿到当前身份,随后远端发生变化。
	plan, err := lib.PlanSyncPull(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stub.set([]byte("different remote object"))
	if _, err := lib.SyncPull(ctx, plan.Remote); !library.IsCode(err, library.Conflict) {
		t.Fatalf("SyncPull() after remote change = %v, want Conflict", err)
	}
	if _, err := lib.GetSkill(ctx, skill.ID); err != nil {
		t.Fatalf("conflicting pull corrupted local library: %v", err)
	}

	// 身份匹配但内容不是合法库:同步计划标注 schema 不可接受,执行被拒且本地不受损。
	badPlan, err := lib.PlanSyncPull(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if badPlan.SchemaOK {
		t.Fatalf("PlanSyncPull() = %#v, want schema not acceptable", badPlan)
	}
	if _, err := lib.SyncPull(ctx, badPlan.Remote); !library.IsCode(err, library.FailedPrecondition) {
		t.Fatalf("SyncPull invalid database = %v, want FailedPrecondition", err)
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
	foreignPlan, err := lib.PlanSyncPull(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.SyncPull(ctx, foreignPlan.Remote); !library.IsCode(err, library.FailedPrecondition) {
		t.Fatalf("SyncPull foreign database = %v, want FailedPrecondition", err)
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
	writeLocalSkill(t, source, "keeper\n")
	if _, err := fetchLocalDir(ctx, lib, source); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.SyncPush(ctx, ""); err != nil {
		t.Fatal(err)
	}
	plan, err := lib.PlanSyncPush(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// HEAD 查询仍看到同步计划里的身份(通过),但紧接着(PUT 之前)远端被并发写者改动。
	// 条件写(If-Match)必须捕获该窗口内的变化,拒绝覆盖。
	intruder := []byte("concurrent remote write")
	stub.afterHead = func() { stub.set(intruder) }
	if _, err := lib.SyncPush(ctx, plan.Remote); !library.IsCode(err, library.Conflict) {
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
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := library.Open(path)
	if err == nil {
		_ = lib.Close()
	}
	requireLibraryError(t, err, library.FailedPrecondition)
	if after, err := os.ReadFile(path); err != nil || !bytes.Equal(after, before) {
		t.Fatalf("foreign database changed: %v", err)
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
	if _, err := lib.PlanSyncPush(t.Context()); !library.IsCode(err, library.NotConfigured) {
		t.Fatalf("missing config error = %v, want NotConfigured", err)
	}
	if _, err := lib.SyncPull(t.Context(), ""); !library.IsCode(err, library.NotConfigured) {
		t.Fatalf("missing config pull error = %v, want NotConfigured", err)
	}
}

func TestSyncPushPlanPreservesRemoteAndMatchingOverwriteSucceeds(t *testing.T) {
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
	plan, err := lib.PlanSyncPush(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.RemoteExists || string(plan.Remote) != stub.etag || !bytes.Equal(before, stub.stored) {
		t.Fatalf("push plan changed remote or returned wrong identity: %#v", plan)
	}
	result, err := lib.SyncPush(ctx, plan.Remote)
	if err != nil {
		t.Fatal(err)
	}
	if result.Remote == plan.Remote || bytes.Equal(before, stub.stored) {
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

func TestSyncRejectsStalePlanAfterRemoteCreationChangeOrDeletion(t *testing.T) {
	stub := newS3Stub(t)
	lib, err := library.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lib.Close() })
	ctx := t.Context()
	plan, err := lib.PlanSyncPush(ctx)
	if err != nil {
		t.Fatal(err)
	}
	intruder := []byte("another writer")
	stub.set(intruder)
	if _, err := lib.SyncPush(ctx, plan.Remote); !library.IsCode(err, library.Conflict) {
		t.Fatalf("push after creation = %v, want Conflict", err)
	}
	plan, err = lib.PlanSyncPush(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stub.set([]byte("changed writer"))
	before := bytes.Clone(stub.stored)
	if _, err := lib.SyncPush(ctx, plan.Remote); !library.IsCode(err, library.Conflict) {
		t.Fatalf("push after change = %v, want Conflict", err)
	}
	if !bytes.Equal(before, stub.stored) {
		t.Fatal("stale push changed remote")
	}
	stub.stored, stub.etag = nil, ""
	if _, err := lib.SyncPush(ctx, plan.Remote); !library.IsCode(err, library.Conflict) {
		t.Fatalf("push after deletion = %v, want Conflict", err)
	}
	if _, err := lib.SyncPull(ctx, plan.Remote); !library.IsCode(err, library.Conflict) {
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
			_, pushPlanErr := lib.PlanSyncPush(t.Context())
			_, pullPlanErr := lib.PlanSyncPull(t.Context())
			_, pushErr := lib.SyncPush(t.Context(), "")
			_, pullErr := lib.SyncPull(t.Context(), "")
			for name, err := range map[string]error{"push plan": pushPlanErr, "pull plan": pullPlanErr, "push": pushErr, "pull": pullErr} {
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
	_, pushPlanErr := lib.PlanSyncPush(ctx)
	_, pullPlanErr := lib.PlanSyncPull(ctx)
	_, pushErr := lib.SyncPush(ctx, "")
	_, pullErr := lib.SyncPull(ctx, "")
	for name, err := range map[string]error{"push plan": pushPlanErr, "pull plan": pullPlanErr, "push": pushErr, "pull": pullErr} {
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

func TestPullPlanReturnsTemporaryFileFailure(t *testing.T) {
	newS3Stub(t)
	lib := openTestLibrary(t)
	if _, err := lib.SyncPush(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	// 在远端有效时制造本地暂存失败,不能把它伪装成 schema 不可接受。
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
	if plan, err := lib.PlanSyncPull(t.Context()); err == nil {
		t.Fatalf("temporary file failure became successful plan: %#v", plan)
	}
}

func TestPullPlanPreservesCancellationDuringValidation(t *testing.T) {
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
	fault := cancelAfterPlacement{Context: base, cancel: cancel, changed: func() bool {
		entries, _ := os.ReadDir(temp)
		return len(entries) > 0
	}}
	if plan, err := lib.PlanSyncPull(fault); !errors.Is(err, context.Canceled) {
		t.Fatalf("validation cancellation = %#v, %v; want context.Canceled", plan, err)
	}
	if entries, err := os.ReadDir(temp); err != nil || len(entries) != 0 {
		t.Fatalf("temporary database not cleaned up: %v, %v", entries, err)
	}
}
