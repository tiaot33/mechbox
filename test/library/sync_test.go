package library_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tiaot33/mechbox/internal/library"
)

// s3Object 是打桩里的一个对象:内容、按内容哈希给出的 ETag 与推送时带的 sha256 元数据。
type s3Object struct {
	data   []byte
	etag   string
	sha256 string
}

// s3Stub 是一个内存 S3 打桩,按 "<存储桶>/<对象名>" 保存对象。
// 支持 PUT(兑现 If-None-Match: *)、GET / HEAD(兑现 If-Match,返回 sha256 元数据)、DELETE,
// 以及按前缀分页列出(ListObjectsV2);拒绝未签名的 x-amz-* 头;
// 按 secrets 里该 Access Key ID 的 Secret Access Key 校验 SigV4 签名(含查询串),不认识的 Access Key ID 或签名不符返回 403。
type s3Stub struct {
	mu       sync.Mutex
	settings library.SyncSettings
	objects  map[string]s3Object
	// pageSize 是列出时每页的对象数;omitETag 让 HEAD / GET 不给 ETag;denyDelete 让 DELETE 返回 403。
	pageSize   int
	omitETag   bool
	denyDelete bool
	// gate 若非 nil,gateMethod 方法的请求在处理前先过闸;用来把推送停在网络上。
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

const stubBucket = "test-bucket"

// awsEncode 按 SigV4 的规则编码查询串里的名字与值:只保留 A-Z a-z 0-9 - _ . ~。
func awsEncode(value string) string {
	var encoded strings.Builder
	for i := range len(value) {
		c := value[i]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.IndexByte("-_.~", c) >= 0 {
			encoded.WriteByte(c)
			continue
		}
		encoded.WriteString("%" + strings.ToUpper(hex.EncodeToString([]byte{c})))
	}
	return encoded.String()
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
	query := r.URL.Query()
	var params []string
	for _, name := range slices.Sorted(maps.Keys(query)) {
		for _, value := range query[name] {
			params = append(params, awsEncode(name)+"="+awsEncode(value))
		}
	}
	canonical := r.Method + "\n" + r.URL.EscapedPath() + "\n" + strings.Join(params, "&") + "\n" + headers.String() + "\n" + fields["SignedHeaders"] + "\n" + payloadHash
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
	s.mu.Lock()
	defer s.mu.Unlock()
	taken := s.requests
	s.requests = nil
	return taken
}

// store 写入一个对象(name 含存储桶)。
func (s *s3Stub) store(name string, data []byte, digest string) s3Object {
	sum := sha256.Sum256(data)
	object := s3Object{data: data, etag: `"` + hex.EncodeToString(sum[:]) + `"`, sha256: digest}
	s.objects[name] = object
	return object
}

// put 在默认存储桶里写入一个对象,构造别的设备推送的备份或不相干的对象。
func (s *s3Stub) put(key string, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.store(stubBucket+"/"+key, data, "")
}

// data 返回默认存储桶里一个对象的内容。
func (s *s3Stub) data(t *testing.T, key string) []byte {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	object, ok := s.objects[stubBucket+"/"+key]
	if !ok {
		t.Fatalf("no S3 object %q", key)
	}
	return object.data
}

// remove 删掉默认存储桶里的一个对象。
func (s *s3Stub) remove(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, stubBucket+"/"+key)
}

// keys 列出默认存储桶里以 prefix 开头的对象名,按名字排序。
func (s *s3Stub) keys(prefix string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var keys []string
	for name := range s.objects {
		if key, ok := strings.CutPrefix(name, stubBucket+"/"); ok && strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	return keys
}

// contains 报告打桩里有没有哪个对象的内容含有 marker。
func (s *s3Stub) contains(marker string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, object := range s.objects {
		if bytes.Contains(object.data, []byte(marker)) {
			return true
		}
	}
	return false
}

// edit 用 SQL 改写默认存储桶里的一份备份,构造另一个应用版本推送的库。
func (s *s3Stub) edit(t *testing.T, key, statement string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "remote.db")
	writeFile(t, path, s.data(t, key), 0o644)
	editLibraryFixture(t, path, statement)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s.put(key, data)
}

type listResult struct {
	XMLName               xml.Name      `xml:"ListBucketResult"`
	IsTruncated           bool          `xml:"IsTruncated"`
	NextContinuationToken string        `xml:"NextContinuationToken,omitempty"`
	Contents              []listContent `xml:"Contents"`
}

type listContent struct {
	Key          string `xml:"Key"`
	LastModified string `xml:"LastModified"`
	ETag         string `xml:"ETag"`
	Size         int    `xml:"Size"`
}

func (s *s3Stub) list(w http.ResponseWriter, bucket string, query map[string][]string) {
	prefix := bucket + "/" + firstValue(query["prefix"])
	var names []string
	for name := range s.objects {
		if strings.HasPrefix(name, prefix) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	start, _ := strconv.Atoi(firstValue(query["continuation-token"]))
	end := min(start+s.pageSize, len(names))
	result := listResult{}
	for _, name := range names[start:end] {
		object := s.objects[name]
		result.Contents = append(result.Contents, listContent{
			Key: strings.TrimPrefix(name, bucket+"/"), LastModified: time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
			ETag: object.etag, Size: len(object.data),
		})
	}
	if end < len(names) {
		result.IsTruncated, result.NextContinuationToken = true, strconv.Itoa(end)
	}
	body, _ := xml.Marshal(result)
	_, _ = w.Write(body)
}

func firstValue(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func newS3Stub(t *testing.T) *s3Stub {
	t.Helper()
	stub := &s3Stub{secrets: map[string]string{"test-key": "test-secret"}, objects: map[string]s3Object{}, pageSize: 1000}
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
		stub.mu.Lock()
		stub.requests = append(stub.requests, request)
		gate := stub.gate
		if gate == nil || r.Method != stub.gateMethod {
			gate = nil
		}
		stub.mu.Unlock()
		if gate != nil {
			gate.pass()
		}
		stub.mu.Lock()
		defer stub.mu.Unlock()
		name := strings.TrimPrefix(r.URL.Path, "/")
		query := r.URL.Query()
		switch {
		case r.Method == http.MethodGet && query.Get("list-type") == "2":
			stub.list(w, name, query)
		case r.Method == http.MethodPut:
			data, _ := io.ReadAll(r.Body)
			if _, exists := stub.objects[name]; exists && r.Header.Get("If-None-Match") == "*" {
				http.Error(w, "exists", http.StatusPreconditionFailed)
				return
			}
			object := stub.store(name, data, r.Header.Get("X-Amz-Meta-Sha256"))
			w.Header().Set("ETag", object.etag)
		case r.Method == http.MethodGet || r.Method == http.MethodHead:
			object, exists := stub.objects[name]
			if !exists {
				http.Error(w, "missing", http.StatusNotFound)
				return
			}
			if match := r.Header.Get("If-Match"); match != "" && match != object.etag {
				http.Error(w, "changed", http.StatusPreconditionFailed)
				return
			}
			if !stub.omitETag {
				w.Header().Set("ETag", object.etag)
			}
			if object.sha256 != "" {
				w.Header().Set("X-Amz-Meta-Sha256", object.sha256)
			}
			if r.Method == http.MethodGet {
				_, _ = w.Write(object.data)
			}
		case r.Method == http.MethodDelete:
			if stub.denyDelete {
				http.Error(w, "denied", http.StatusForbidden)
				return
			}
			delete(stub.objects, name)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "method", http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(server.Close)
	stub.endpoint = server.URL
	stub.settings = library.SyncSettings{
		Endpoint: server.URL, Bucket: stubBucket, Folder: "backups/test$folder+name",
		Region: "test-region", AccessKeyID: "test-key", SecretAccessKey: "test-secret",
	}
	return stub
}

// open 打开一个新的测试库,同步设置指向这个桩。
func (s *s3Stub) open(t *testing.T) *library.Library {
	t.Helper()
	lib := openTestLibrary(t)
	setSyncSettings(t, lib, s.settings)
	return lib
}

// folder 返回打桩默认设置里放备份的文件夹,带结尾的 /。
func (s *s3Stub) folder() string {
	return s.settings.Folder + "/"
}

func pushBackup(t *testing.T, lib *library.Library) library.SyncPushResult {
	t.Helper()
	result, err := lib.SyncPush(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func syncDevice(t *testing.T, lib *library.Library) string {
	t.Helper()
	status, err := lib.SyncStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return status.Device
}

func listBackups(t *testing.T, lib *library.Library) []library.SyncBackup {
	t.Helper()
	backups, err := lib.ListSyncBackups(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return backups
}

// pullLatest 像用户在同步页上那样拉回:列出备份,选最新的一份,看拉回计划,确认拉回。
func pullLatest(t *testing.T, lib *library.Library) library.SyncPullPlan {
	t.Helper()
	backups := listBackups(t, lib)
	if len(backups) == 0 {
		t.Fatal("no backups on S3")
	}
	plan, err := lib.PlanSyncPull(t.Context(), backups[0].ID)
	if err != nil || !plan.CanPull {
		t.Fatalf("PlanSyncPull() = %#v, %v", plan, err)
	}
	if err := lib.SyncPull(t.Context(), backups[0].ID); err != nil {
		t.Fatal(err)
	}
	return plan
}

var deviceName = regexp.MustCompile(`^[A-Za-z0-9._-]+-[0-9a-f]{6}$`)

// 推送在 <文件夹>/<设备目录>/ 下新建一份备份,不覆盖已有的;本机的库没变时不新建。
// 设备目录是主机名加随机后缀,两台主机名相同的机器也各用各的目录。
func TestSyncPushCreatesBackupsInThisDeviceFolder(t *testing.T) {
	stub := newS3Stub(t)
	lib := stub.open(t)
	ctx := t.Context()
	device := syncDevice(t, lib)
	if !deviceName.MatchString(device) {
		t.Fatalf("device = %q, want host name plus a random suffix", device)
	}
	if other := syncDevice(t, stub.open(t)); other == device {
		t.Fatalf("two libraries on one host share device folder %q", device)
	}

	first := pushBackup(t, lib)
	keys := stub.keys(stub.folder())
	firstData := bytes.Clone(stub.data(t, keys[0]))
	if len(keys) != 1 || !regexp.MustCompile(`^`+regexp.QuoteMeta(stub.folder()+device)+`/library-\d{8}T\d{6}\.\d{3}Z\.db$`).MatchString(keys[0]) {
		t.Fatalf("objects after push = %q, want one backup in %s%s/", keys, stub.folder(), device)
	}
	if first.Unchanged || first.Backup.Device != device || !first.Backup.ThisDevice || time.Since(first.Backup.PushedAt) > time.Minute {
		t.Fatalf("SyncPush() = %#v, want a new backup from this device just now", first)
	}
	if backups := listBackups(t, lib); !reflect.DeepEqual(backups, []library.SyncBackup{first.Backup}) {
		t.Fatalf("ListSyncBackups() = %#v, want the pushed backup %#v", backups, first.Backup)
	}

	again := pushBackup(t, lib)
	if !again.Unchanged || again.Backup != first.Backup || len(stub.keys(stub.folder())) != 1 {
		t.Fatalf("push without changes = %#v, objects %q; want no new backup", again, stub.keys(stub.folder()))
	}

	if _, err := lib.CreateGroup(ctx, "new-local-group"); err != nil {
		t.Fatal(err)
	}
	changed := pushBackup(t, lib)
	if changed.Unchanged || changed.Backup == first.Backup || len(stub.keys(stub.folder())) != 2 {
		t.Fatalf("push after change = %#v, objects %q; want a second backup", changed, stub.keys(stub.folder()))
	}
	if !bytes.Equal(stub.data(t, keys[0]), firstData) {
		t.Fatal("first backup was overwritten")
	}
	if err := lib.DeleteGroup(ctx, mustGroupID(t, lib, "new-local-group"), library.DeleteGroupOptions{}); err != nil {
		t.Fatal(err)
	}
	pullLatest(t, lib)
	groups, err := lib.ListGroups(ctx)
	if err != nil || len(groups) != 1 || groups[0].Name != "new-local-group" {
		t.Fatalf("groups after pulling the latest backup = %#v, %v", groups, err)
	}
}

func mustGroupID(t *testing.T, lib *library.Library, name string) library.GroupID {
	t.Helper()
	groups, err := lib.ListGroups(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range groups {
		if group.Name == name {
			return group.ID
		}
	}
	t.Fatalf("no group %q", name)
	return 0
}

// 推送后删掉这台机器超出保留份数的最旧备份,别的设备的备份不动;调小份数后,下一次推送(即使没变化)就删到新份数。
// 没填份数时保留 10 份。
func TestSyncPushKeepsConfiguredNumberOfBackups(t *testing.T) {
	stub := newS3Stub(t)
	settings := stub.settings
	settings.Keep = 3
	lib := openTestLibrary(t)
	setSyncSettings(t, lib, settings)
	ctx := t.Context()
	device := syncDevice(t, lib)
	otherDevice := stub.folder() + "Other-Mac-0a0b0c/"
	for i := range 4 {
		stub.put(otherDevice+"library-2026010"+strconv.Itoa(i+1)+"T000000.000Z.db", []byte("other device"))
	}

	var pushed []library.SyncBackup
	for i := range 5 {
		if _, err := lib.CreateGroup(ctx, "group-"+strconv.Itoa(i)); err != nil {
			t.Fatal(err)
		}
		pushed = append(pushed, pushBackup(t, lib).Backup)
	}
	own := stub.keys(stub.folder() + device + "/")
	if len(own) != 3 || len(stub.keys(otherDevice)) != 4 {
		t.Fatalf("objects = %q and %q, want this device's newest 3 and all 4 of the other device", own, stub.keys(otherDevice))
	}
	backups := listBackups(t, lib)
	var mine []library.SyncBackup
	for _, backup := range backups {
		if backup.ThisDevice {
			mine = append(mine, backup)
		}
	}
	if want := []library.SyncBackup{pushed[4], pushed[3], pushed[2]}; !reflect.DeepEqual(mine, want) {
		t.Fatalf("this device's backups = %#v, want the newest three %#v", mine, want)
	}

	settings.Keep = 1
	setSyncSettings(t, lib, settings)
	if result := pushBackup(t, lib); !result.Unchanged {
		t.Fatalf("push without changes = %#v, want Unchanged", result)
	}
	if own := stub.keys(stub.folder() + device + "/"); len(own) != 1 || !strings.HasSuffix(string(pushed[4].ID), own[0]) {
		t.Fatalf("objects after lowering keep = %q, want only the newest", own)
	}

	settings.Keep = 0
	setSyncSettings(t, lib, settings)
	for i := range library.DefaultSyncKeep + 1 {
		if _, err := lib.CreateGroup(ctx, "more-"+strconv.Itoa(i)); err != nil {
			t.Fatal(err)
		}
		pushBackup(t, lib)
	}
	if own := stub.keys(stub.folder() + device + "/"); len(own) != library.DefaultSyncKeep {
		t.Fatalf("objects with default keep = %d, want %d", len(own), library.DefaultSyncKeep)
	}
}

// 备份已经传上去、删旧备份被拒时,推送返回错误,但这份备份留在 S3 上,上次推送的时间照样更新;
// 能删以后,下一次推送把超出的删掉。
func TestSyncPushReportsFailedRemovalAfterUploading(t *testing.T) {
	stub := newS3Stub(t)
	settings := stub.settings
	settings.Keep = 1
	lib := openTestLibrary(t)
	setSyncSettings(t, lib, settings)
	ctx := t.Context()
	device := syncDevice(t, lib)
	pushBackup(t, lib)
	if _, err := lib.CreateGroup(ctx, "after-first"); err != nil {
		t.Fatal(err)
	}
	stub.denyDelete = true
	before := time.Now()
	if _, err := lib.SyncPush(ctx); !library.IsCode(err, library.Permission) {
		t.Fatalf("SyncPush() with removal denied = %v, want Permission", err)
	}
	if own := stub.keys(stub.folder() + device + "/"); len(own) != 2 {
		t.Fatalf("objects = %q, want the new backup uploaded and the old one kept", own)
	}
	if status, err := lib.SyncStatus(ctx); err != nil || status.LastPushedAt.Before(before) {
		t.Fatalf("SyncStatus() = %#v, %v; want LastPushedAt updated", status, err)
	}
	stub.denyDelete = false
	if result := pushBackup(t, lib); !result.Unchanged {
		t.Fatalf("retry = %#v, want Unchanged", result)
	}
	if own := stub.keys(stub.folder() + device + "/"); len(own) != 1 {
		t.Fatalf("objects after retry = %q, want one", own)
	}
}

// 列出备份时列出文件夹里所有设备的备份,最新推送的在前,标出哪些是这台机器的;
// 文件夹里不是备份的对象、别的文件夹里的对象都不列出;分页的列出结果合在一起。
func TestListSyncBackupsAcrossDevicesNewestFirst(t *testing.T) {
	stub := newS3Stub(t)
	stub.pageSize = 2
	lib := stub.open(t)
	if backups := listBackups(t, lib); backups == nil || len(backups) != 0 {
		t.Fatalf("ListSyncBackups() before any push = %#v, want empty list", backups)
	}
	device := syncDevice(t, lib)
	stub.put(stub.folder()+"Old-PC-111111/library-20250101T000000.000Z.db", []byte("old"))
	stub.put(stub.folder()+"notes.txt", []byte("not a backup"))
	stub.put(stub.folder()+"Old-PC-111111/readme.md", []byte("not a backup"))
	stub.put(stub.folder()+"Old-PC-111111/nested/library-20250101T000000.000Z.db", []byte("too deep"))
	stub.put("elsewhere/Other-222222/library-20250101T000000.000Z.db", []byte("other folder"))
	mine := pushBackup(t, lib).Backup
	stub.put(stub.folder()+"Laptop-333333/library-20991231T000000.000Z.db", []byte("newest"))

	backups := listBackups(t, lib)
	var devices []string
	for _, backup := range backups {
		devices = append(devices, backup.Device)
	}
	if want := []string{"Laptop-333333", device, "Old-PC-111111"}; !slices.Equal(devices, want) {
		t.Fatalf("devices = %q, want newest first %q", devices, want)
	}
	if backups[1] != mine || backups[0].ThisDevice || backups[2].ThisDevice || !backups[0].PushedAt.After(backups[1].PushedAt) {
		t.Fatalf("ListSyncBackups() = %#v", backups)
	}

	settings := stub.settings
	settings.Folder = ""
	setSyncSettings(t, lib, settings)
	stub.put("Root-444444/library-20250101T000000.000Z.db", []byte("at bucket root"))
	if backups := listBackups(t, lib); len(backups) != 1 || backups[0].Device != "Root-444444" {
		t.Fatalf("ListSyncBackups() at bucket root = %#v", backups)
	}
}

// 拉回计划之后那份备份被删掉(推送它的机器按份数删了)或改写,拉回返回 Conflict,本机的库不变;
// 传的不是列出的标识返回 InvalidArgument。
func TestSyncPullRejectsBackupRemovedOrChangedSincePlan(t *testing.T) {
	stub := newS3Stub(t)
	ctx := t.Context()
	lib := stub.open(t)
	local := fetchNamedSkill(t, lib, "keeper")
	pushBackup(t, lib)
	backup := listBackups(t, lib)[0]
	key := stub.keys(stub.folder())[0]

	stub.put(key, []byte("rewritten"))
	if _, err := lib.PlanSyncPull(ctx, backup.ID); !library.IsCode(err, library.Conflict) {
		t.Fatalf("PlanSyncPull() after rewrite = %v, want Conflict", err)
	}
	if err := lib.SyncPull(ctx, backup.ID); !library.IsCode(err, library.Conflict) {
		t.Fatalf("SyncPull() after rewrite = %v, want Conflict", err)
	}
	stub.remove(key)
	if _, err := lib.PlanSyncPull(ctx, backup.ID); !library.IsCode(err, library.Conflict) {
		t.Fatalf("PlanSyncPull() after removal = %v, want Conflict", err)
	}
	if err := lib.SyncPull(ctx, backup.ID); !library.IsCode(err, library.Conflict) {
		t.Fatalf("SyncPull() after removal = %v, want Conflict", err)
	}
	for _, id := range []library.SyncBackupID{"", "no-space", `"etag" `} {
		if _, err := lib.PlanSyncPull(ctx, id); !library.IsCode(err, library.InvalidArgument) {
			t.Errorf("PlanSyncPull(%q) = %v, want InvalidArgument", id, err)
		}
		if err := lib.SyncPull(ctx, id); !library.IsCode(err, library.InvalidArgument) {
			t.Errorf("SyncPull(%q) = %v, want InvalidArgument", id, err)
		}
	}
	if _, err := lib.GetSkill(ctx, local.ID); err != nil {
		t.Fatalf("refused pull changed local library: %v", err)
	}
}

// 那份备份不能拉回时,拉回的同步计划说明原因:不是本应用的库、来自较新或较旧版本的应用;
// 执行拉回返回 FailedPrecondition,本机的库不变。
func TestSyncPullPlanExplainsWhyBackupCannotBePulled(t *testing.T) {
	stub := newS3Stub(t)
	ctx := t.Context()
	lib := stub.open(t)
	skill := fetchNamedSkill(t, lib, "keeper")
	pushBackup(t, lib)
	key := stub.keys(stub.folder())[0]
	pushed := bytes.Clone(stub.data(t, key))

	for _, tc := range []struct {
		name   string
		remote func()
		want   library.CannotPullReason
	}{
		{"not a database", func() { stub.put(key, []byte("different remote object")) }, library.CannotPullNotALibrary},
		{"another application", func() { stub.put(key, pushed); stub.edit(t, key, `PRAGMA application_id = 7`) }, library.CannotPullNotALibrary},
		{"newer application", func() { stub.put(key, pushed); stub.edit(t, key, `PRAGMA user_version = 9999`) }, library.CannotPullNewerApp},
		{"older application", func() { stub.put(key, pushed); stub.edit(t, key, `PRAGMA user_version = 1`) }, library.CannotPullOlderApp},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.remote()
			backup := listBackups(t, lib)[0]
			plan, err := lib.PlanSyncPull(ctx, backup.ID)
			if err != nil {
				t.Fatal(err)
			}
			if want := (library.SyncPullPlan{CannotPull: tc.want}); !reflect.DeepEqual(plan, want) {
				t.Fatalf("PlanSyncPull() = %#v, want %#v", plan, want)
			}
			if err := lib.SyncPull(ctx, backup.ID); !library.IsCode(err, library.FailedPrecondition) {
				t.Fatalf("SyncPull() = %v, want FailedPrecondition", err)
			}
			if _, err := lib.GetSkill(ctx, skill.ID); err != nil {
				t.Fatalf("refused pull corrupted local library: %v", err)
			}
		})
	}
}

func TestPullPlanReturnsTemporaryFileFailure(t *testing.T) {
	stub := newS3Stub(t)
	lib := stub.open(t)
	pushBackup(t, lib)
	backup := listBackups(t, lib)[0]
	// 在远端有效时制造本地暂存失败,不能把它伪装成不能拉回。
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
	if plan, err := lib.PlanSyncPull(t.Context(), backup.ID); err == nil {
		t.Fatalf("temporary file failure became successful plan: %#v", plan)
	}
}

func TestPullPlanPreservesCancellationDuringValidation(t *testing.T) {
	stub := newS3Stub(t)
	lib := stub.open(t)
	pushBackup(t, lib)
	backup := listBackups(t, lib)[0]
	temp := t.TempDir()
	t.Setenv("TMPDIR", temp)
	base, cancel := context.WithCancel(t.Context())
	defer cancel()
	// 下载完成并写入暂存库后才取消,确保覆盖校验阶段而非 HTTP 请求阶段。
	fault := cancelAfterPlacement{Context: base, cancel: cancel, changed: func() bool {
		entries, _ := os.ReadDir(temp)
		return len(entries) > 0
	}}
	if plan, err := lib.PlanSyncPull(fault, backup.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("validation cancellation = %#v, %v; want context.Canceled", plan, err)
	}
	if entries, err := os.ReadDir(temp); err != nil || len(entries) != 0 {
		t.Fatalf("temporary database not cleaned up: %v, %v", entries, err)
	}
}

// 换机器:旧机器推送,新机器选这份备份,先看拉回的同步计划(里面有什么、哪些项目不在这台机器上),再拉回。
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
	lib := stub.open(t)
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
	pushBackup(t, lib)

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

	backup := listBackups(t, lib)[0]
	pullPlan, err := lib.PlanSyncPull(ctx, backup.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantPlan := library.SyncPullPlan{
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
	if err := lib.SyncPull(ctx, backup.ID); err != nil {
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
	lib := stub.open(t)
	pushBackup(t, lib)
	stub.edit(t, stub.keys(stub.folder())[0], `DELETE FROM agents WHERE code = 'claude-code'; UPDATE agents SET name = 'Old Codex', global_path = '.codex/skills' WHERE code = 'codex'`)
	pullLatest(t, lib)
	agents, err := lib.ListAgents(t.Context())
	if err != nil || !reflect.DeepEqual(agents, seededAgents(home)) {
		t.Fatalf("ListAgents() after pull = %#v, %v; want %#v", agents, err, seededAgents(home))
	}
	if dir, err := lib.ScopeDirectory(t.Context(), library.GlobalScope(library.AgentCodex)); err != nil || dir != filepath.Join(home, ".agents", "skills") {
		t.Fatalf("ScopeDirectory(codex global) after pull = %q, %v", dir, err)
	}
}

func TestSyncMapsFailuresAndMissingConfiguration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "denied", http.StatusForbidden)
	}))
	settings := library.SyncSettings{Endpoint: server.URL, Bucket: "test-bucket", Region: "test-region", AccessKeyID: "test-key", SecretAccessKey: "test-secret"}
	lib := openTestLibrary(t)
	setSyncSettings(t, lib, settings)
	if _, err := lib.SyncPush(t.Context()); !library.IsCode(err, library.Permission) {
		t.Fatalf("push permission error = %v", err)
	}
	if _, err := lib.ListSyncBackups(t.Context()); !library.IsCode(err, library.Permission) {
		t.Fatalf("list permission error = %v", err)
	}
	server.Close()
	if _, err := lib.SyncPush(t.Context()); !library.IsCode(err, library.SourceUnreachable) {
		t.Fatalf("unreachable error = %v", err)
	}
	if _, err := lib.PlanSyncPull(t.Context(), `"etag" key`); !library.IsCode(err, library.SourceUnreachable) {
		t.Fatalf("unreachable pull plan error = %v", err)
	}
}

func TestAllSyncOperationsReportMissingConfiguration(t *testing.T) {
	stub := newS3Stub(t)
	lib := openTestLibrary(t)
	for name, drop := range map[string]func(*library.SyncSettings){
		"nothing set":       func(s *library.SyncSettings) { *s = library.SyncSettings{} },
		"bucket":            func(s *library.SyncSettings) { s.Bucket = "" },
		"access key id":     func(s *library.SyncSettings) { s.AccessKeyID = "" },
		"secret access key": func(s *library.SyncSettings) { s.SecretAccessKey = "" },
	} {
		t.Run(name, func(t *testing.T) {
			settings := stub.settings
			drop(&settings)
			setSyncSettings(t, lib, settings)
			_, pushErr := lib.SyncPush(t.Context())
			_, listErr := lib.ListSyncBackups(t.Context())
			_, pullPlanErr := lib.PlanSyncPull(t.Context(), `"etag" key`)
			pullErr := lib.SyncPull(t.Context(), `"etag" key`)
			for name, err := range map[string]error{"push": pushErr, "list": listErr, "pull plan": pullPlanErr, "pull": pullErr} {
				if !library.IsCode(err, library.NotConfigured) {
					t.Errorf("%s = %v, want NotConfigured", name, err)
				}
			}
			if requests := stub.takeRequests(); len(requests) != 0 {
				t.Errorf("requests without configuration = %+v", requests)
			}
		})
	}
}

// S3 的响应里没有 ETag 时认不出是哪一份:推送不新建备份、拉回不替换本机的库,都返回 SourceUnreachable。
func TestSyncRejectsRemoteWithoutIdentity(t *testing.T) {
	stub := newS3Stub(t)
	lib := stub.open(t)
	ctx := t.Context()
	if _, err := lib.CreateGroup(ctx, "local-group"); err != nil {
		t.Fatal(err)
	}
	pushBackup(t, lib)
	backup := listBackups(t, lib)[0]
	if _, err := lib.CreateGroup(ctx, "another-group"); err != nil {
		t.Fatal(err)
	}
	stub.omitETag = true
	_, pushErr := lib.SyncPush(ctx)
	_, pullPlanErr := lib.PlanSyncPull(ctx, backup.ID)
	pullErr := lib.SyncPull(ctx, backup.ID)
	for name, err := range map[string]error{"push": pushErr, "pull plan": pullPlanErr, "pull": pullErr} {
		if !library.IsCode(err, library.SourceUnreachable) {
			t.Errorf("%s without remote identity = %v, want SourceUnreachable", name, err)
		}
	}
	if keys := stub.keys(stub.folder()); len(keys) != 1 {
		t.Fatalf("objects = %q, want no new backup", keys)
	}
	groups, err := lib.ListGroups(ctx)
	if err != nil || len(groups) != 2 {
		t.Fatalf("local library changed: %#v, %v", groups, err)
	}
}

// 存在数据库里的同步设置用于推送、列出与拉回:请求发到设置的服务地址、存储桶与文件夹,
// 用设置的 Access Key ID、Secret Access Key、区域签名,并带上设置的 Session Token。环境变量不读。
func TestSyncPushAndPullWithStoredSettings(t *testing.T) {
	stub := newS3Stub(t)
	stub.secrets["settings-key"] = "settings-secret"
	t.Setenv("SKILLS_S3_BUCKET", "environment-bucket")
	t.Setenv("AWS_ACCESS_KEY_ID", "test-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret")
	t.Setenv("AWS_SESSION_TOKEN", "environment-session")
	lib := openTestLibrary(t)
	setSyncSettings(t, lib, library.SyncSettings{
		Endpoint:        stub.endpoint,
		Bucket:          "settings-bucket",
		Folder:          "team/mechbox",
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
	pushBackup(t, lib)
	if err := lib.DeleteGroup(ctx, group.ID, library.DeleteGroupOptions{}); err != nil {
		t.Fatal(err)
	}
	if plan := pullLatest(t, lib); plan.Groups != 1 {
		t.Fatalf("pull plan = %#v", plan)
	}
	if groups, err := lib.ListGroups(ctx); err != nil || len(groups) != 1 || groups[0].Name != "pushed" {
		t.Fatalf("groups after pull = %#v, %v", groups, err)
	}
	device := syncDevice(t, lib)
	requests := stub.takeRequests()
	if len(requests) == 0 || slices.ContainsFunc(requests, func(r s3Request) bool {
		return r.accessKey != "settings-key" || r.region != "settings-region" || r.sessionToken != "settings-session" ||
			r.path != "/settings-bucket" && !strings.HasPrefix(r.path, "/settings-bucket/team/mechbox/"+device+"/library-")
	}) {
		t.Fatalf("requests = %+v, want stored settings only", requests)
	}
}

// 设置的 GitHub 令牌存在数据库里,随推送、拉回走:拉回的一侧换成推送那份库里的令牌,访问 GitHub 时用它;
// 拉回一份没有令牌的库,本机的令牌随之变成没填。
func TestGitHubTokenTravelsWithSync(t *testing.T) {
	fx := inspectGitHubRepo(t, writeGitHubRepo(t, map[string]repoFile{
		"demo/SKILL.md": {content: []byte(skillDocument("demo", "body")), mode: 0o644},
	}))
	api := serveGitHubAPI(t, fx)
	api.anonymousStatus = http.StatusNotFound
	blockGitHubClone(t)
	stub := newS3Stub(t)
	gitHub := http.DefaultClient.Transport
	http.DefaultClient = &http.Client{Transport: fetchTransport(func(r *http.Request) (*http.Response, error) {
		if "http://"+r.URL.Host == stub.endpoint {
			return http.DefaultTransport.RoundTrip(r)
		}
		return gitHub.RoundTrip(r)
	})}
	ctx := t.Context()

	pushing := stub.open(t)
	setGitHubToken(t, pushing, "  synced-token\n")
	pushBackup(t, pushing)
	pulling := stub.open(t)
	setGitHubToken(t, pulling, "local-token")
	tokenIs := func(want string) {
		t.Helper()
		if token, err := pulling.GitHubToken(ctx); err != nil || token != want {
			t.Fatalf("GitHubToken = %q, %v; want %q", token, err, want)
		}
	}
	previewWith := func(token string) {
		t.Helper()
		api.mu.Lock()
		api.acceptedToken = token
		api.mu.Unlock()
		preview, err := pulling.PreviewFetch(ctx, gitHubRequest())
		if err != nil || len(preview.Candidates) != 1 {
			t.Fatalf("preview = %#v, %v", preview, err)
		}
		got := api.takeAuthorizations()
		if len(got) < 2 || got[0] != "" || slices.ContainsFunc(got[1:], func(a string) bool { return a != "Bearer "+token }) {
			t.Fatalf("authorizations = %q, want anonymous then %q", got, token)
		}
	}

	pullLatest(t, pulling)
	tokenIs("synced-token")
	previewWith("synced-token")

	setGitHubToken(t, pulling, "local-token")
	setGitHubToken(t, pushing, "")
	pushBackup(t, pushing)
	pullLatest(t, pulling)
	tokenIs("")
}

// 清空或换掉的令牌不会留在推送出去的库里:删掉的内容在数据库文件里清零,S3 上的对象里找不到旧令牌原文。
func TestClearedGitHubTokenDoesNotLeaveInPushedLibrary(t *testing.T) {
	stub := newS3Stub(t)
	lib := stub.open(t)
	setGitHubToken(t, lib, "ghp_REPLACEDMARKER0123456789")
	setGitHubToken(t, lib, "ghp_CLEAREDMARKER")
	setGitHubToken(t, lib, "")
	pushBackup(t, lib)
	for _, old := range []string{"ghp_REPLACEDMARKER", "ghp_CLEAREDMARKER"} {
		if stub.contains(old) {
			t.Fatalf("pushed library still contains %q", old)
		}
	}
}

// 同步设置存在数据库里:重新打开同一个数据库仍是它;在同一个 Library 上改设置,下一次同步就用新值;
// 缺存储桶或密钥时不从环境变量补;全部清空后同步报 NotConfigured。
func TestSyncSettingsAreStoredAndTakeEffectImmediately(t *testing.T) {
	stub := newS3Stub(t)
	stub.secrets["settings-key"] = "settings-secret"
	stub.secrets["rotated-key"] = "rotated-secret"
	t.Setenv("SKILLS_S3_BUCKET", "environment-bucket")
	t.Setenv("AWS_ACCESS_KEY_ID", "test-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret")
	t.Setenv("AWS_SESSION_TOKEN", "environment-session")
	path := filepath.Join(t.TempDir(), "library.db")
	first := openTestLibraryAt(t, path)
	setSyncSettings(t, first, library.SyncSettings{
		Endpoint: " " + stub.endpoint + "\n", Bucket: "settings-bucket", Folder: " /team//mechbox/ ",
		AccessKeyID: "settings-key", SecretAccessKey: "settings-secret", Keep: 5, AutoPush: library.SyncDaily,
	})
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	lib := openTestLibraryAt(t, path)
	stored := library.SyncSettings{
		Endpoint: stub.endpoint, Bucket: "settings-bucket", Folder: "team/mechbox",
		AccessKeyID: "settings-key", SecretAccessKey: "settings-secret", Keep: 5, AutoPush: library.SyncDaily,
	}
	if got, err := lib.SyncSettings(t.Context()); err != nil || got != stored {
		t.Fatalf("SyncSettings after reopen = %+v, %v; want trimmed %+v", got, err, stored)
	}
	list := func() s3Request {
		t.Helper()
		if _, err := lib.ListSyncBackups(t.Context()); err != nil {
			t.Fatal(err)
		}
		requests := stub.takeRequests()
		if len(requests) != 1 {
			t.Fatalf("requests = %+v, want one", requests)
		}
		return requests[0]
	}

	// 没填区域与 Session Token:取默认区域,不带 Session Token,不用环境变量里的。
	for key, secret := range map[string]string{"settings-key": "settings-secret", "rotated-key": "rotated-secret"} {
		setSyncSettings(t, lib, library.SyncSettings{Endpoint: stub.endpoint, Bucket: "settings-bucket", AccessKeyID: key, SecretAccessKey: secret})
		want := s3Request{accessKey: key, region: "us-east-1", path: "/settings-bucket"}
		if got := list(); got != want {
			t.Fatalf("request = %+v, want %+v", got, want)
		}
	}

	setSyncSettings(t, lib, library.SyncSettings{Bucket: "settings-bucket"})
	if _, err := lib.ListSyncBackups(t.Context()); !library.IsCode(err, library.NotConfigured) {
		t.Fatalf("partial settings = %v, want NotConfigured without falling back to environment", err)
	}
	if requests := stub.takeRequests(); len(requests) != 0 {
		t.Fatalf("partial settings sent %+v", requests)
	}

	setSyncSettings(t, lib, library.SyncSettings{Endpoint: stub.endpoint, Bucket: "settings-bucket", AccessKeyID: "settings-key", SecretAccessKey: "wrong-secret"})
	if _, err := lib.ListSyncBackups(t.Context()); !library.IsCode(err, library.Permission) {
		t.Fatalf("wrong secret = %v, want Permission", err)
	}

	setSyncSettings(t, lib, library.SyncSettings{})
	if got, err := lib.SyncSettings(t.Context()); err != nil || got != (library.SyncSettings{}) {
		t.Fatalf("SyncSettings after clearing = %+v, %v", got, err)
	}
	if _, err := lib.SyncPush(t.Context()); !library.IsCode(err, library.NotConfigured) {
		t.Fatalf("after clearing = %v, want NotConfigured", err)
	}
}

// 保留份数为负、自动推送的间隔不认识时不存,按字段返回 InvalidArgument。
func TestSetSyncSettingsRejectsInvalidKeepAndInterval(t *testing.T) {
	lib := openTestLibrary(t)
	saved := library.SyncSettings{Bucket: "bucket", Keep: 2, AutoPush: library.SyncHourly}
	setSyncSettings(t, lib, saved)
	for field, settings := range map[string]library.SyncSettings{
		"keep":      {Bucket: "other", Keep: -1},
		"auto_push": {Bucket: "other", AutoPush: "sometimes"},
	} {
		err := lib.SetSyncSettings(t.Context(), settings)
		if failure := library.ErrorOf(err); failure == nil || failure.Code != library.InvalidArgument || failure.Details.Field != field {
			t.Errorf("SetSyncSettings(%+v) = %v, want InvalidArgument on %s", settings, err, field)
		}
	}
	if got, err := lib.SyncSettings(t.Context()); err != nil || got != saved {
		t.Fatalf("SyncSettings after rejected saves = %+v, %v; want %+v", got, err, saved)
	}
	for interval, want := range map[library.SyncInterval]time.Duration{library.SyncHourly: time.Hour, library.SyncDaily: 24 * time.Hour, library.SyncWeekly: 7 * 24 * time.Hour} {
		if got, ok := interval.Period(); !ok || got != want {
			t.Errorf("%s.Period() = %v, %v; want %v", interval, got, ok, want)
		}
	}
	if _, ok := library.SyncInterval("").Period(); ok {
		t.Error("empty interval has a period")
	}
}

// 同步设置与同步状态只属于这台机器:推送出去的库里没有 S3 密钥,拉回时保留本机的同步设置、设备目录与上次推送时间;
// 应用的设置随推送、拉回走,拉回后换成那份库里的。设备目录重新打开数据库后不变。
func TestSyncSettingsStayOnThisMachineAndAppSettingsTravel(t *testing.T) {
	stub := newS3Stub(t)
	ctx := t.Context()
	pushingSettings := stub.settings
	pushingSettings.SecretAccessKey, pushingSettings.SessionToken = "PUSHINGSECRETMARKER", "PUSHINGSESSIONMARKER"
	stub.secrets["test-key"] = pushingSettings.SecretAccessKey
	pushing := openTestLibrary(t)
	setSyncSettings(t, pushing, pushingSettings)
	setAppSettings(t, pushing, map[string]string{"language": "en", "auto_check": "false"})
	pushingDevice := syncDevice(t, pushing)
	pushBackup(t, pushing)
	for _, secret := range []string{"PUSHINGSECRETMARKER", "PUSHINGSESSIONMARKER", pushingDevice} {
		if stub.contains(secret) {
			t.Fatalf("pushed library contains %q", secret)
		}
	}

	pullingSettings := pushingSettings
	pullingSettings.SessionToken, pullingSettings.Keep, pullingSettings.AutoPush = "pulling-session", 4, library.SyncWeekly
	path := filepath.Join(t.TempDir(), "pulling.db")
	pulling := openTestLibraryAt(t, path)
	setSyncSettings(t, pulling, pullingSettings)
	setAppSettings(t, pulling, map[string]string{"language": "zh", "dock_badge": "false"})
	if status, err := pulling.SyncStatus(ctx); err != nil || !status.LastPushedAt.IsZero() {
		t.Fatalf("SyncStatus() before any push = %#v, %v", status, err)
	}
	pullingDevice := syncDevice(t, pulling)
	before := time.Now()
	pushBackup(t, pulling)
	pushed, err := pulling.SyncStatus(ctx)
	if err != nil || pushed.LastPushedAt.Before(before) {
		t.Fatalf("SyncStatus() after push = %#v, %v", pushed, err)
	}

	backups := listBackups(t, pulling)
	i := slices.IndexFunc(backups, func(b library.SyncBackup) bool { return b.Device == pushingDevice })
	if err := pulling.SyncPull(ctx, backups[i].ID); err != nil {
		t.Fatal(err)
	}
	if got, err := pulling.SyncSettings(ctx); err != nil || got != pullingSettings {
		t.Fatalf("SyncSettings after pull = %+v, %v; want this machine's %+v", got, err, pullingSettings)
	}
	if got, err := pulling.SyncStatus(ctx); err != nil || got.Device != pullingDevice || !got.LastPushedAt.Equal(pushed.LastPushedAt) {
		t.Fatalf("SyncStatus after pull = %+v, %v; want this machine's %+v", got, err, pushed)
	}
	if got, err := pulling.AppSettings(ctx); err != nil || !maps.Equal(got, map[string]string{"language": "en", "auto_check": "false"}) {
		t.Fatalf("AppSettings after pull = %v, %v; want pushed ones", got, err)
	}
	if err := pulling.Close(); err != nil {
		t.Fatal(err)
	}
	if device := syncDevice(t, openTestLibraryAt(t, path)); device != pullingDevice {
		t.Fatalf("device after reopen = %q, want %q", device, pullingDevice)
	}
}
