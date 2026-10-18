package library_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tiaot33/skills-tui/internal/library"
)

// s3Stub 是一个内存 S3 打桩,记录当前对象内容并按内容哈希给出 ETag。
// 它兑现条件写(If-Match / If-None-Match),据此模拟并发写者与前置条件失败(412);
// 像 S3 一样保存推送时的元数据、在 HEAD / GET 上返回 Last-Modified 与元数据,并拒绝未签名的 x-amz-* 头;
// 按 secrets 里该 Access Key ID 的 Secret Access Key 校验 SigV4 签名,不认识的 Access Key ID 或签名不符返回 403。
type s3Stub struct {
	stored     []byte
	etag       string
	modified   time.Time
	pushedFrom string
	// afterHead 若非 nil,在一次 HEAD 响应发出后触发一次(随即清空),
	// 用来在"HEAD 查询"与"随后 PUT"之间注入一次远端改动,复现 TOCTOU 窗口。
	afterHead func()
	// gate 若非 nil,gateMethod 方法的请求在处理前先过闸;用来把 push / plan 停在网络上。
	gate       *networkGate
	gateMethod string
	// endpoint 是打桩服务的地址;secrets 是认得的 Access Key ID 与对应的 Secret Access Key;
	// requests 按顺序记下每个签名正确的请求用了哪组凭证、发到哪个路径。
	endpoint string
	secrets  map[string]string
	requests []s3Request
}

// s3Request 是打桩记下的一次请求:Access Key ID、签名里的区域、Session Token 与请求路径。
type s3Request struct {
	accessKey, region, sessionToken, path string
}

// verifySigV4 按 secrets 重算 SigV4 签名,签名与请求相符时返回这次请求用的凭证。
func verifySigV4(r *http.Request, secrets map[string]string) (s3Request, bool) {
	fields := map[string]string{}
	for field := range strings.SplitSeq(strings.TrimPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 "), ", ") {
		name, value, _ := strings.Cut(field, "=")
		fields[name] = value
	}
	scope := strings.Split(fields["Credential"], "/")
	if len(scope) != 5 {
		return s3Request{}, false
	}
	secret, ok := secrets[scope[0]]
	if !ok {
		return s3Request{}, false
	}
	body, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(body))
	payloadSum := sha256.Sum256(body)
	payloadHash := hex.EncodeToString(payloadSum[:])
	if r.Header.Get("X-Amz-Content-Sha256") != payloadHash {
		return s3Request{}, false
	}
	var headers strings.Builder
	for name := range strings.SplitSeq(fields["SignedHeaders"], ";") {
		value := r.Header.Get(name)
		if name == "host" {
			value = r.Host
		}
		headers.WriteString(name + ":" + strings.TrimSpace(value) + "\n")
	}
	canonical := r.Method + "\n" + r.URL.EscapedPath() + "\n\n" + headers.String() + "\n" + fields["SignedHeaders"] + "\n" + payloadHash
	canonicalSum := sha256.Sum256([]byte(canonical))
	toSign := "AWS4-HMAC-SHA256\n" + r.Header.Get("X-Amz-Date") + "\n" + strings.Join(scope[1:], "/") + "\n" + hex.EncodeToString(canonicalSum[:])
	key := []byte("AWS4" + secret)
	for _, part := range append(scope[1:], toSign) {
		mac := hmac.New(sha256.New, key)
		mac.Write([]byte(part))
		key = mac.Sum(nil)
	}
	if !hmac.Equal([]byte(hex.EncodeToString(key)), []byte(fields["Signature"])) {
		return s3Request{}, false
	}
	return s3Request{accessKey: scope[0], region: scope[2], sessionToken: r.Header.Get("X-Amz-Security-Token"), path: r.URL.EscapedPath()}, true
}

// takeRequests 返回并清空已记下的请求。
func (s *s3Stub) takeRequests() []s3Request {
	taken := s.requests
	s.requests = nil
	return taken
}

func (s *s3Stub) set(data []byte) {
	s.stored = data
	sum := sha256.Sum256(data)
	s.etag = `"` + hex.EncodeToString(sum[:]) + `"`
	s.modified = time.Now().UTC().Truncate(time.Second)
	s.pushedFrom = ""
}

// edit 用 SQL 改写 S3 上的库文件,构造另一台机器或另一个应用版本推送的库。
func (s *s3Stub) edit(t *testing.T, statement string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "remote.db")
	writeFile(t, path, s.stored, 0o644)
	editLibraryFixture(t, path, statement)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s.set(data)
}

func newS3Stub(t *testing.T) *s3Stub {
	t.Helper()
	stub := &s3Stub{secrets: map[string]string{"test-key": "test-secret"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization := r.Header.Get("Authorization")
		if !strings.HasPrefix(authorization, "AWS4-HMAC-SHA256 ") {
			http.Error(w, "unsigned", http.StatusForbidden)
			return
		}
		for name := range r.Header {
			if name = strings.ToLower(name); strings.HasPrefix(name, "x-amz-") && !strings.Contains(authorization, name) {
				http.Error(w, "unsigned header "+name, http.StatusForbidden)
				return
			}
		}
		request, ok := verifySigV4(r, stub.secrets)
		if !ok {
			http.Error(w, "signature does not match", http.StatusForbidden)
			return
		}
		stub.requests = append(stub.requests, request)
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
			stub.pushedFrom = r.Header.Get("X-Amz-Meta-Pushed-From")
			w.Header().Set("ETag", stub.etag)
		case http.MethodGet, http.MethodHead:
			if len(stub.stored) == 0 {
				http.Error(w, "missing", http.StatusNotFound)
				return
			}
			w.Header().Set("ETag", stub.etag)
			w.Header().Set("Last-Modified", stub.modified.Format(http.TimeFormat))
			if stub.pushedFrom != "" {
				w.Header().Set("X-Amz-Meta-Pushed-From", stub.pushedFrom)
			}
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
	stub.endpoint = server.URL
	t.Setenv("SKILLS_S3_ENDPOINT", server.URL)
	t.Setenv("SKILLS_S3_BUCKET", "test-bucket")
	t.Setenv("SKILLS_S3_KEY", "backups/test$file+name.db")
	t.Setenv("AWS_REGION", "test-region")
	t.Setenv("AWS_ACCESS_KEY_ID", "test-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret")
	return stub
}

func thisMachine(t *testing.T) string {
	t.Helper()
	name, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSuffix(name, ".local")
}

// 推送的同步计划告诉用户会覆盖 S3 上的哪一份库:谁、什么时候推送的;只查询,不上传。
// 带着计划里的标识推送成功,返回刚推送的那份库。
func TestSyncPushPlanShowsWhichRemoteLibraryWillBeOverwritten(t *testing.T) {
	stub := newS3Stub(t)
	lib := openTestLibrary(t)
	ctx := t.Context()

	first, err := lib.PlanSyncPush(ctx)
	if err != nil || first.Remote != nil {
		t.Fatalf("PlanSyncPush() without remote = %#v, %v; want no remote library", first, err)
	}
	if len(stub.stored) != 0 {
		t.Fatal("push plan uploaded a database")
	}
	pushed, err := lib.SyncPush(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if string(pushed.Identity) != stub.etag || pushed.PushedFrom != thisMachine(t) || time.Since(pushed.PushedAt) > time.Minute {
		t.Fatalf("SyncPush() = %#v, want remote %q pushed from %q just now", pushed, stub.etag, thisMachine(t))
	}

	before := bytes.Clone(stub.stored)
	if _, err := lib.CreateGroup(ctx, "new-local-group"); err != nil {
		t.Fatal(err)
	}
	plan, err := lib.PlanSyncPush(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := &library.RemoteLibrary{Identity: pushed.Identity, PushedAt: stub.modified, PushedFrom: thisMachine(t)}
	if !reflect.DeepEqual(plan.Remote, want) || !bytes.Equal(before, stub.stored) {
		t.Fatalf("PlanSyncPush() = %#v, want %#v and remote untouched", plan.Remote, want)
	}
	overwritten, err := lib.SyncPush(ctx, plan.Remote.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if overwritten.Identity == plan.Remote.Identity || bytes.Equal(before, stub.stored) {
		t.Fatal("matching push did not update remote")
	}
	if err := lib.SyncPull(ctx, overwritten.Identity); err != nil {
		t.Fatal(err)
	}
	groups, err := lib.ListGroups(ctx)
	if err != nil || len(groups) != 1 || groups[0].Name != "new-local-group" {
		t.Fatalf("pushed library groups = %#v, %v", groups, err)
	}
}

func TestSyncPushConflictsWhenRemoteChangesWithinWindow(t *testing.T) {
	stub := newS3Stub(t)
	ctx := t.Context()
	lib := openTestLibrary(t)
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
	if _, err := lib.SyncPush(ctx, plan.Remote.Identity); !library.IsCode(err, library.Conflict) {
		t.Fatalf("SyncPush() racing a concurrent remote write = %v, want Conflict", err)
	}
	if !bytes.Equal(stub.stored, intruder) {
		t.Fatalf("SyncPush overwrote the concurrently-written remote: stored = %q", stub.stored)
	}
}

func TestSyncRejectsStalePlanAfterRemoteCreationChangeOrDeletion(t *testing.T) {
	stub := newS3Stub(t)
	lib := openTestLibrary(t)
	ctx := t.Context()
	plan, err := lib.PlanSyncPush(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Remote != nil {
		t.Fatalf("PlanSyncPush() = %#v, want no remote library", plan)
	}
	stub.set([]byte("another writer"))
	if _, err := lib.SyncPush(ctx, ""); !library.IsCode(err, library.Conflict) {
		t.Fatalf("push after creation = %v, want Conflict", err)
	}
	plan, err = lib.PlanSyncPush(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stub.set([]byte("changed writer"))
	before := bytes.Clone(stub.stored)
	if _, err := lib.SyncPush(ctx, plan.Remote.Identity); !library.IsCode(err, library.Conflict) {
		t.Fatalf("push after change = %v, want Conflict", err)
	}
	if !bytes.Equal(before, stub.stored) {
		t.Fatal("stale push changed remote")
	}
	stub.stored, stub.etag = nil, ""
	if _, err := lib.SyncPush(ctx, plan.Remote.Identity); !library.IsCode(err, library.Conflict) {
		t.Fatalf("push after deletion = %v, want Conflict", err)
	}
	if err := lib.SyncPull(ctx, plan.Remote.Identity); !library.IsCode(err, library.Conflict) {
		t.Fatalf("pull after deletion = %v, want Conflict", err)
	}
}

// 新机器第一次打开同步页:S3 上还没有库是正常状态,拉回的同步计划说明不能拉回,不返回错误;
// 硬要拉回则是 NotFound。
func TestSyncPullPlanWithoutRemoteLibrary(t *testing.T) {
	newS3Stub(t)
	lib := openTestLibrary(t)
	plan, err := lib.PlanSyncPull(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if want := (library.SyncPullPlan{CannotPull: library.CannotPullNoRemote}); !reflect.DeepEqual(plan, want) {
		t.Fatalf("PlanSyncPull() without remote = %#v, want %#v", plan, want)
	}
	if err := lib.SyncPull(t.Context(), ""); !library.IsCode(err, library.NotFound) {
		t.Fatalf("SyncPull() without remote = %v, want NotFound", err)
	}
}

// S3 上的库不能拉回时,拉回的同步计划说明原因:不是本应用的库、来自较新或较旧版本的应用;
// 执行拉回返回 FailedPrecondition,本机的库不变。计划之后远端又变了,拉回返回 Conflict。
func TestSyncPullPlanExplainsWhyRemoteCannotBePulled(t *testing.T) {
	stub := newS3Stub(t)
	ctx := t.Context()
	lib := openTestLibrary(t)
	source := filepath.Join(t.TempDir(), "keeper")
	writeLocalSkill(t, source, "keeper\n")
	skill, err := fetchLocalDir(ctx, lib, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.SyncPush(ctx, ""); err != nil {
		t.Fatal(err)
	}
	pushed := bytes.Clone(stub.stored)

	plan, err := lib.PlanSyncPull(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stub.set([]byte("different remote object"))
	if err := lib.SyncPull(ctx, plan.Remote.Identity); !library.IsCode(err, library.Conflict) {
		t.Fatalf("SyncPull() after remote change = %v, want Conflict", err)
	}
	if _, err := lib.GetSkill(ctx, skill.ID); err != nil {
		t.Fatalf("conflicting pull corrupted local library: %v", err)
	}

	for _, tc := range []struct {
		name   string
		remote func()
		want   library.CannotPullReason
	}{
		{"not a database", func() { stub.set([]byte("different remote object")) }, library.CannotPullNotALibrary},
		{"another application", func() { stub.set(pushed); stub.edit(t, `PRAGMA application_id = 7`) }, library.CannotPullNotALibrary},
		{"newer application", func() { stub.set(pushed); stub.edit(t, `PRAGMA user_version = 9999`) }, library.CannotPullNewerApp},
		{"older application", func() { stub.set(pushed); stub.edit(t, `PRAGMA user_version = 1`) }, library.CannotPullOlderApp},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.remote()
			plan, err := lib.PlanSyncPull(ctx)
			if err != nil {
				t.Fatal(err)
			}
			want := library.SyncPullPlan{Remote: &library.RemoteLibrary{Identity: library.RemoteIdentity(stub.etag), PushedAt: stub.modified}, CannotPull: tc.want}
			if !reflect.DeepEqual(plan, want) {
				t.Fatalf("PlanSyncPull() = %#v, want %#v", plan, want)
			}
			if err := lib.SyncPull(ctx, plan.Remote.Identity); !library.IsCode(err, library.FailedPrecondition) {
				t.Fatalf("SyncPull() = %v, want FailedPrecondition", err)
			}
			if _, err := lib.GetSkill(ctx, skill.ID); err != nil {
				t.Fatalf("refused pull corrupted local library: %v", err)
			}
		})
	}
}

func TestPullPlanReturnsTemporaryFileFailure(t *testing.T) {
	newS3Stub(t)
	lib := openTestLibrary(t)
	if _, err := lib.SyncPush(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	// 在远端有效时制造本地暂存失败,不能把它伪装成不能拉回。
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

// 换机器:旧机器推送,新机器先看拉回的同步计划(里面有什么、哪些项目不在这台机器上),再拉回。
// 拉回替换整个库但不动 Agent 目录;项目路径原样保留,用户把不在这台机器上的项目改到本机位置后,
// 安装记录按本机的用户主目录与新项目位置算出目录。
func TestSyncPullReplacesLibraryAndKeepsProjectsForRelocation(t *testing.T) {
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
	if _, err := updateSkill(t, lib, ctx, original.ID); err != nil {
		t.Fatal(err)
	}
	group, err := lib.CreateGroup(ctx, "synced-group")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.AddSkillToGroup(ctx, group.ID, original.ID); err != nil {
		t.Fatal(err)
	}
	oldProjectPath := filepath.Join(t.TempDir(), "ai-workflow-engine")
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
	if _, err := lib.SyncPush(ctx, ""); err != nil {
		t.Fatal(err)
	}

	// 换到新机器:本机库另有内容,旧项目目录与旧主目录都不在。
	if err := lib.RemoveSkill(ctx, original.ID, library.RemoveSkillOptions{}); err != nil {
		t.Fatal(err)
	}
	extraSource := filepath.Join(t.TempDir(), "extra")
	writeLocalSkill(t, extraSource, "extra\n")
	if _, err := fetchLocalDir(ctx, lib, extraSource); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(oldProjectPath); err != nil {
		t.Fatal(err)
	}
	newHome := filepath.Join(t.TempDir(), "new-home")
	t.Setenv("HOME", newHome)

	pullPlan, err := lib.PlanSyncPull(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantPlan := library.SyncPullPlan{
		Remote:   &library.RemoteLibrary{Identity: library.RemoteIdentity(stub.etag), PushedAt: stub.modified, PushedFrom: thisMachine(t)},
		CanPull:  true,
		Skills:   1,
		Groups:   1,
		Projects: []library.RegisteredProject{{ID: project.ID, Name: "ai-workflow-engine", Path: project.Path, Missing: true}},
	}
	if !reflect.DeepEqual(pullPlan, wantPlan) {
		t.Fatalf("PlanSyncPull() = %#v, want %#v", pullPlan, wantPlan)
	}
	beforePull, err := lib.ListSkills(ctx)
	if err != nil || len(beforePull) != 1 || beforePull[0].Name != "extra" {
		t.Fatalf("pull plan replaced local library: %#v, %v", beforePull, err)
	}
	if err := lib.SyncPull(ctx, pullPlan.Remote.Identity); err != nil {
		t.Fatal(err)
	}

	skills, err := lib.ListSkills(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].Name != original.Name {
		t.Fatalf("skills after pull = %#v", skills)
	}
	full, err := lib.GetSkill(ctx, original.ID)
	if err != nil || string(skillFiles(t, lib, full)[0].Content) != skillDocument(original.Name, "updated\n") {
		t.Fatalf("GetSkill after pull = %#v, %v", full, err)
	}
	versions, err := lib.ListVersions(ctx, original.ID)
	if err != nil || len(versions) != 2 {
		t.Fatalf("versions after pull = %#v, %v", versions, err)
	}
	group.Members = []library.SkillID{original.ID}
	requireGroups(t, lib, []library.Group{group})
	// 拉回的库保留集合的 ID。
	requireCollections(t, lib, []library.Collection{{ID: original.CollectionID, Source: original.Source, Skills: []library.SkillID{original.ID}}})
	projects, err := lib.ListRegisteredProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(projects, wantPlan.Projects) {
		t.Fatalf("projects after pull = %#v, want %#v", projects, wantPlan.Projects)
	}

	newProjectPath := filepath.Join(t.TempDir(), "ai-workflow-engine")
	if err := os.MkdirAll(newProjectPath, 0o755); err != nil {
		t.Fatal(err)
	}
	relocated, err := lib.RelocateProject(ctx, project.ID, newProjectPath)
	if err != nil {
		t.Fatal(err)
	}
	newProjectPath = relocated.Path
	// 拉回不在磁盘上创建任何东西:新主目录与新项目里的 Agent 目录都还不存在,用户重新安装后才有。
	for _, path := range []string{newHome, filepath.Join(newProjectPath, ".agents")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("pull created %s: %v", path, err)
		}
	}
	// SkillScope 只记 Scope 身份:全局按本机 $HOME、项目按改过的位置算出目录,不带上一台机器的绝对路径。
	skillScopes, err := lib.ListSkillScopes(ctx, library.SkillScopeFilter{})
	if err != nil {
		t.Fatal(err)
	}
	wantSkillScopes := []library.SkillScope{
		{SkillID: original.ID, Scope: globalScope, Folder: filepath.Join(newHome, ".agents", "skills", original.Name), Direct: true},
		{SkillID: original.ID, Scope: projectScope, Folder: filepath.Join(newProjectPath, ".agents", "skills", original.Name), Groups: []library.GroupID{group.ID}},
	}
	if !reflect.DeepEqual(withoutCreatedAt(skillScopes), wantSkillScopes) {
		t.Fatalf("skill scopes after pull = %#v, want %#v", skillScopes, wantSkillScopes)
	}
	for _, want := range wantSkillScopes {
		if dir, err := lib.ScopeDirectory(ctx, want.Scope); err != nil || dir != filepath.Dir(want.Folder) {
			t.Fatalf("ScopeDirectory(%#v) after pull = %q, %v; want %q", want.Scope, dir, err, filepath.Dir(want.Folder))
		}
	}
}

// 拉回的库里内置 Agent 缺行或名称、目录规则来自另一个版本的应用时,拉回后与 Open 一样以本机应用为准。
func TestSyncPullRewritesBuiltinAgentsForThisApp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	stub := newS3Stub(t)
	lib := openTestLibrary(t)
	ctx := t.Context()
	if _, err := lib.SyncPush(ctx, ""); err != nil {
		t.Fatal(err)
	}
	stub.edit(t, `DELETE FROM agents WHERE code = 'claude-code'; UPDATE agents SET name = 'Old Codex', global_path = '.codex/skills' WHERE code = 'codex'`)
	plan, err := lib.PlanSyncPull(ctx)
	if err != nil || !plan.CanPull {
		t.Fatalf("PlanSyncPull() = %#v, %v", plan, err)
	}
	if err := lib.SyncPull(ctx, plan.Remote.Identity); err != nil {
		t.Fatal(err)
	}
	agents, err := lib.ListAgents(ctx)
	if err != nil || !reflect.DeepEqual(agents, seededAgents(home)) {
		t.Fatalf("ListAgents() after pull = %#v, %v; want %#v", agents, err, seededAgents(home))
	}
	if dir, err := lib.ScopeDirectory(ctx, library.GlobalScope(library.AgentCodex)); err != nil || dir != filepath.Join(home, ".agents", "skills") {
		t.Fatalf("ScopeDirectory(codex global) after pull = %q, %v", dir, err)
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
	if err := lib.SyncPull(t.Context(), ""); !library.IsCode(err, library.NotConfigured) {
		t.Fatalf("missing config pull error = %v, want NotConfigured", err)
	}
}

func TestAllSyncOperationsReportMissingConfiguration(t *testing.T) {
	newS3Stub(t)
	lib := openTestLibrary(t)
	for _, key := range []string{"SKILLS_S3_BUCKET", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, "")
			_, pushPlanErr := lib.PlanSyncPush(t.Context())
			_, pullPlanErr := lib.PlanSyncPull(t.Context())
			_, pushErr := lib.SyncPush(t.Context(), "")
			pullErr := lib.SyncPull(t.Context(), "")
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
	lib := openTestLibrary(t)
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
	pullErr := lib.SyncPull(ctx, "")
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

// 调用方传入同步设置后,不设置任何环境变量也能推送和拉回:请求发到传入的服务地址、存储桶与对象名,
// 用传入的 Access Key ID、Secret Access Key、区域签名,并带上传入的 Session Token。
func TestSyncPushAndPullWithPassedSettings(t *testing.T) {
	stub := newS3Stub(t)
	stub.secrets["settings-key"] = "settings-secret"
	for _, key := range []string{"SKILLS_S3_ENDPOINT", "SKILLS_S3_BUCKET", "SKILLS_S3_KEY", "AWS_REGION", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN"} {
		t.Setenv(key, "")
	}
	lib := openTestLibrary(t)
	lib.SetSyncSettings(&library.SyncSettings{
		Endpoint:        stub.endpoint,
		Bucket:          "settings-bucket",
		Key:             "settings.db",
		Region:          "settings-region",
		AccessKeyID:     "settings-key",
		SecretAccessKey: "settings-secret",
		SessionToken:    "settings-session",
	})
	ctx := t.Context()
	group, err := lib.CreateGroup(ctx, "pushed")
	if err != nil {
		t.Fatal(err)
	}
	pushPlan, err := lib.PlanSyncPush(ctx)
	if err != nil || pushPlan.Remote != nil {
		t.Fatalf("push plan = %#v, %v", pushPlan, err)
	}
	if _, err := lib.SyncPush(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if err := lib.DeleteGroup(ctx, group.ID, library.DeleteGroupOptions{}); err != nil {
		t.Fatal(err)
	}
	pullPlan, err := lib.PlanSyncPull(ctx)
	if err != nil || !pullPlan.CanPull || pullPlan.Groups != 1 {
		t.Fatalf("pull plan = %#v, %v", pullPlan, err)
	}
	if err := lib.SyncPull(ctx, pullPlan.Remote.Identity); err != nil {
		t.Fatal(err)
	}
	if groups, err := lib.ListGroups(ctx); err != nil || len(groups) != 1 || groups[0].Name != "pushed" {
		t.Fatalf("groups after pull = %#v, %v", groups, err)
	}
	want := s3Request{accessKey: "settings-key", region: "settings-region", sessionToken: "settings-session", path: "/settings-bucket/settings.db"}
	requests := stub.takeRequests()
	if len(requests) == 0 || slices.ContainsFunc(requests, func(r s3Request) bool { return r != want }) {
		t.Fatalf("requests = %+v, want passed settings only", requests)
	}
}

// 同步设置整组取用:传入了就只用传入的那一组,缺的项不从环境变量补;在同一个 Library 上改设置,
// 下一次同步就用新值;传 nil 回到环境变量。
func TestSyncSettingsAreTakenAsAWholeAndTakeEffectImmediately(t *testing.T) {
	stub := newS3Stub(t)
	stub.secrets["settings-key"] = "settings-secret"
	stub.secrets["rotated-key"] = "rotated-secret"
	t.Setenv("AWS_SESSION_TOKEN", "environment-session")
	lib := openTestLibrary(t)
	planPush := func() s3Request {
		t.Helper()
		if _, err := lib.PlanSyncPush(t.Context()); err != nil {
			t.Fatal(err)
		}
		requests := stub.takeRequests()
		if len(requests) != 1 {
			t.Fatalf("requests = %+v, want one", requests)
		}
		return requests[0]
	}
	environment := s3Request{accessKey: "test-key", region: "test-region", sessionToken: "environment-session", path: "/test-bucket/backups/test%24file%2Bname.db"}
	if got := planPush(); got != environment {
		t.Fatalf("environment request = %+v, want %+v", got, environment)
	}

	lib.SetSyncSettings(&library.SyncSettings{Bucket: "settings-bucket"})
	if _, err := lib.PlanSyncPush(t.Context()); !library.IsCode(err, library.NotConfigured) {
		t.Fatalf("partial settings = %v, want NotConfigured without falling back to environment", err)
	}
	if requests := stub.takeRequests(); len(requests) != 0 {
		t.Fatalf("partial settings sent %+v", requests)
	}

	// 传入的那组没填区域、对象名与 Session Token:按原有规则取默认区域与对象名,不带 Session Token,不用环境变量里的。
	for key, secret := range map[string]string{"settings-key": "settings-secret", "rotated-key": "rotated-secret"} {
		lib.SetSyncSettings(&library.SyncSettings{Endpoint: stub.endpoint, Bucket: "settings-bucket", AccessKeyID: key, SecretAccessKey: secret})
		want := s3Request{accessKey: key, region: "us-east-1", path: "/settings-bucket/library.db"}
		if got := planPush(); got != want {
			t.Fatalf("request = %+v, want %+v", got, want)
		}
	}

	lib.SetSyncSettings(&library.SyncSettings{Endpoint: stub.endpoint, Bucket: "settings-bucket", AccessKeyID: "settings-key", SecretAccessKey: "wrong-secret"})
	if _, err := lib.PlanSyncPush(t.Context()); !library.IsCode(err, library.Permission) {
		t.Fatalf("wrong secret = %v, want Permission", err)
	}

	lib.SetSyncSettings(nil)
	if got := planPush(); got != environment {
		t.Fatalf("request after clearing settings = %+v, want environment %+v", got, environment)
	}
}
