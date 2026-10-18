package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/tiaot33/skills-tui/internal/library"
)

// startApp 启动一个应用设置服务;钥匙串条目换成本测试专用的，测试结束时删掉，不碰真实条目。
func startApp(t *testing.T, path string, lib *LibraryService) *AppService {
	t.Helper()
	a := NewAppService(path, lib)
	a.keychain = keychain{service: bundleID + ".test", account: t.Name()}
	t.Cleanup(func() { _ = a.keychain.delete() })
	if err := a.ServiceStartup(t.Context(), application.ServiceOptions{}); err != nil {
		t.Fatalf("ServiceStartup: %v", err)
	}
	t.Cleanup(func() { _ = a.ServiceShutdown() })
	return a
}

func clearEnv(t *testing.T) {
	for _, name := range []string{
		"GITHUB_TOKEN", "GH_TOKEN", "SKILLS_S3_ENDPOINT", "SKILLS_S3_BUCKET", "SKILLS_S3_KEY",
		"AWS_REGION", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN",
	} {
		t.Setenv(name, "")
	}
}

// s3Server 记下收到的请求的 Authorization 头，对象一律不存在。
func s3Server(t *testing.T) (*httptest.Server, func() []string) {
	var mu sync.Mutex
	var auth []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auth = append(auth, r.Header.Get("Authorization"))
		mu.Unlock()
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)
	return server, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), auth...)
	}
}

func TestCredentialsKeptAcrossRestartAndGivenToLibrary(t *testing.T) {
	clearEnv(t)
	server, requests := s3Server(t)
	lib := startLibrary(t)
	path := filepath.Join(t.TempDir(), settingsFileName)
	first := startApp(t, path, lib)
	if got, err := first.Credentials(); err != nil || got != (Credentials{}) {
		t.Fatalf("first start = %+v, %v; want nothing filled", got, err)
	}
	if got := lib.Config(); got.GitHubToken || got.Sync {
		t.Fatalf("Config before filling = %+v; want nothing configured", got)
	}
	if _, err := lib.PlanSyncPush(t.Context()); library.ErrorOf(err).Code != library.NotConfigured {
		t.Fatalf("PlanSyncPush before filling = %v; want NotConfigured", err)
	}

	if _, err := first.SaveGitHubToken(" ghp_secret \n"); err != nil {
		t.Fatalf("SaveGitHubToken: %v", err)
	}
	sync := library.SyncSettings{
		Endpoint: server.URL, Bucket: "bucket", Key: "team.db", Region: "eu-west-1",
		AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "s3-secret", SessionToken: "s3-session",
	}
	saved, err := first.SaveSyncSettings(sync)
	if err != nil {
		t.Fatalf("SaveSyncSettings: %v", err)
	}
	want := Credentials{GitHubToken: "ghp_secret", Sync: sync}
	if saved != want {
		t.Fatalf("SaveSyncSettings = %+v; want %+v", saved, want)
	}
	if got := lib.Config(); !got.GitHubToken || !got.Sync {
		t.Fatalf("Config after filling = %+v; want both configured", got)
	}
	if _, err := lib.PlanSyncPush(t.Context()); err != nil {
		t.Fatalf("PlanSyncPush with the settings page's sync settings: %v", err)
	}
	if got := requests(); len(got) == 0 || !strings.Contains(got[0], "Credential=AKIDEXAMPLE/") {
		t.Fatalf("S3 requests = %q; want them signed with the settings page's access key", got)
	}

	// 令牌和密钥只在钥匙串里;settings.json 里只有其余几项。
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"ghp_secret", "s3-secret", "s3-session"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("settings.json contains %q:\n%s", secret, data)
		}
	}
	loaded, err := loadSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	if wantSync := (syncFields{Endpoint: server.URL, Bucket: "bucket", Key: "team.db", Region: "eu-west-1", AccessKeyID: "AKIDEXAMPLE"}); loaded.Sync != wantSync {
		t.Fatalf("settings.json sync = %+v; want %+v", loaded.Sync, wantSync)
	}
	item, ok, err := first.keychain.get()
	if err != nil || !ok {
		t.Fatalf("keychain item = %v, %v", ok, err)
	}
	var stored secrets
	if err := json.Unmarshal(item, &stored); err != nil {
		t.Fatal(err)
	}
	if stored != (secrets{GitHubToken: "ghp_secret", SecretAccessKey: "s3-secret", SessionToken: "s3-session"}) {
		t.Fatalf("keychain item = %+v", stored)
	}

	// 改其他设置不会丢掉同步设置。
	toggled := defaultSettings()
	toggled.DockBadge = false
	if _, err := first.SaveSettings(toggled); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	if err := first.ServiceShutdown(); err != nil {
		t.Fatalf("ServiceShutdown: %v", err)
	}

	// 重启：新的技能库实例从设置里拿回令牌与同步设置。
	restarted := startLibrary(t)
	second := NewAppService(path, restarted)
	second.keychain = first.keychain
	if err := second.ServiceStartup(t.Context(), application.ServiceOptions{}); err != nil {
		t.Fatalf("ServiceStartup after restart: %v", err)
	}
	t.Cleanup(func() { _ = second.ServiceShutdown() })
	if got, err := second.Credentials(); err != nil || got != want {
		t.Fatalf("after restart = %+v, %v; want %+v", got, err, want)
	}
	if got := second.Settings(); !reflect.DeepEqual(got, toggled) {
		t.Fatalf("settings after restart = %+v; want %+v", got, toggled)
	}
	if got := restarted.Config(); !got.GitHubToken || !got.Sync {
		t.Fatalf("Config after restart = %+v; want both configured", got)
	}
	before := len(requests())
	if _, err := restarted.PlanSyncPush(t.Context()); err != nil || len(requests()) == before {
		t.Fatalf("PlanSyncPush after restart = %v; want it to reach the settings page's endpoint", err)
	}
}

func TestClearedCredentialsFallBackToEnvironment(t *testing.T) {
	clearEnv(t)
	t.Setenv("GH_TOKEN", "from-env")
	t.Setenv("SKILLS_S3_BUCKET", "env-bucket")
	t.Setenv("AWS_ACCESS_KEY_ID", "env-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "env-secret")
	lib := startLibrary(t)
	path := filepath.Join(t.TempDir(), settingsFileName)
	a := startApp(t, path, lib)
	if got := lib.Config(); !got.GitHubToken || !got.Sync {
		t.Fatalf("Config with only environment variables = %+v; want both configured", got)
	}

	// 同步设置整组取用：设置页只填了存储桶时，不从环境变量补密钥。
	if _, err := a.SaveSyncSettings(library.SyncSettings{Bucket: "page-bucket"}); err != nil {
		t.Fatalf("SaveSyncSettings: %v", err)
	}
	if got := lib.Config(); got.Sync {
		t.Fatalf("Config with a partial sync group = %+v; want sync not configured", got)
	}
	if _, err := lib.PlanSyncPush(t.Context()); library.ErrorOf(err).Code != library.NotConfigured {
		t.Fatalf("PlanSyncPush with a partial sync group = %v; want NotConfigured", err)
	}

	if _, err := a.SaveGitHubToken("page-token"); err != nil {
		t.Fatalf("SaveGitHubToken: %v", err)
	}
	if _, err := a.SaveGitHubToken(""); err != nil {
		t.Fatalf("SaveGitHubToken clear: %v", err)
	}
	cleared, err := a.SaveSyncSettings(library.SyncSettings{Bucket: "  "})
	if err != nil {
		t.Fatalf("SaveSyncSettings clear: %v", err)
	}
	if cleared != (Credentials{}) {
		t.Fatalf("after clearing = %+v; want nothing filled", cleared)
	}
	if got := lib.Config(); !got.GitHubToken || !got.Sync {
		t.Fatalf("Config after clearing = %+v; want environment variables used again", got)
	}
	if _, ok, err := a.keychain.get(); ok || err != nil {
		t.Fatalf("keychain item after clearing = %v, %v; want removed", ok, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"sync"`) {
		t.Fatalf("settings.json still has sync settings:\n%s", data)
	}
}

// memoryStore 是测试用的钥匙串条目;fail 里按操作排队的错误依次返回，用来模拟拒绝授权或钥匙串锁定。
type memoryStore struct {
	value []byte
	fail  map[string][]error
}

func (m *memoryStore) next(op string) error {
	if len(m.fail[op]) == 0 {
		return nil
	}
	err := m.fail[op][0]
	m.fail[op] = m.fail[op][1:]
	return err
}

func (m *memoryStore) get() ([]byte, bool, error) {
	if err := m.next("get"); err != nil {
		return nil, false, err
	}
	return m.value, m.value != nil, nil
}

func (m *memoryStore) set(value []byte) error {
	if err := m.next("set"); err != nil {
		return err
	}
	m.value = value
	return nil
}

func (m *memoryStore) delete() error {
	if err := m.next("delete"); err != nil {
		return err
	}
	m.value = nil
	return nil
}

func (m *memoryStore) secrets(t *testing.T) secrets {
	t.Helper()
	var stored secrets
	if m.value != nil {
		if err := json.Unmarshal(m.value, &stored); err != nil {
			t.Fatal(err)
		}
	}
	return stored
}

func startWithStore(t *testing.T, path string, lib *LibraryService, store *memoryStore) *AppService {
	t.Helper()
	a := NewAppService(path, lib)
	a.keychain = store
	if err := a.ServiceStartup(t.Context(), application.ServiceOptions{}); err != nil {
		t.Fatalf("ServiceStartup: %v", err)
	}
	t.Cleanup(func() { _ = a.ServiceShutdown() })
	return a
}

var errDenied = errors.New("user denied keychain access")

func TestDeniedKeychainAtStartupDoesNotLoseSecrets(t *testing.T) {
	clearEnv(t)
	lib := startLibrary(t)
	path := filepath.Join(t.TempDir(), settingsFileName)
	old := secrets{GitHubToken: "ghp_old", SecretAccessKey: "s3-secret", SessionToken: "s3-session"}
	if err := saveSettings(path, settingsFile{Settings: defaultSettings(), Sync: syncFields{Bucket: "bucket", AccessKeyID: "AKID"}}); err != nil {
		t.Fatal(err)
	}
	store := &memoryStore{value: must(json.Marshal(old)), fail: map[string][]error{"get": {errDenied, errDenied, errDenied}}}

	// 启动时拒绝：令牌回到环境变量，同步缺密钥。
	a := startWithStore(t, path, lib, store)
	if got := lib.Config(); got.GitHubToken || got.Sync {
		t.Fatalf("Config after denied startup = %+v; want nothing configured", got)
	}
	// 打开设置页时再次拒绝：读不到就报错，也不能保存，钥匙串里的内容不动。
	if _, err := a.Credentials(); !errors.Is(err, errDenied) {
		t.Fatalf("Credentials while denied = %v; want the keychain error", err)
	}
	if _, err := a.SaveSyncSettings(library.SyncSettings{Bucket: "bucket", AccessKeyID: "AKID", Region: "eu-west-1"}); !errors.Is(err, errDenied) {
		t.Fatalf("SaveSyncSettings while denied = %v; want the keychain error", err)
	}
	if got := store.secrets(t); got != old {
		t.Fatalf("keychain after a denied save = %+v; want %+v", got, old)
	}

	// 用户允许后，设置页拿到完整内容，技能库也用上;只改区域不会清掉密钥。
	got, err := a.Credentials()
	if err != nil {
		t.Fatalf("Credentials after allowing: %v", err)
	}
	if got.GitHubToken != "ghp_old" || got.Sync.SecretAccessKey != "s3-secret" || got.Sync.SessionToken != "s3-session" {
		t.Fatalf("Credentials after allowing = %+v; want the stored secrets", got)
	}
	if c := lib.Config(); !c.GitHubToken || !c.Sync {
		t.Fatalf("Config after allowing = %+v; want both configured", c)
	}
	got.Sync.Region = "eu-west-1"
	if _, err := a.SaveSyncSettings(got.Sync); err != nil {
		t.Fatalf("SaveSyncSettings: %v", err)
	}
	if stored := store.secrets(t); stored != old {
		t.Fatalf("keychain after changing the region = %+v; want %+v", stored, old)
	}
}

func TestFailedSettingsWriteRestoresKeychain(t *testing.T) {
	clearEnv(t)
	lib := startLibrary(t)
	path := filepath.Join(t.TempDir(), settingsFileName)
	before := library.SyncSettings{Bucket: "old-bucket", AccessKeyID: "OLDKEY", SecretAccessKey: "old-secret"}
	changed := library.SyncSettings{Bucket: "new-bucket", AccessKeyID: "NEWKEY", SecretAccessKey: "new-secret"}

	for _, c := range []struct {
		name       string
		restore    error
		wantSecret string
	}{
		{"撤销成功：钥匙串回到原来的密钥", nil, "old-secret"},
		{"撤销也失败：报出两个错误，缓存与技能库跟钥匙串里实际的一致", errDenied, "new-secret"},
	} {
		t.Run(c.name, func(t *testing.T) {
			store := &memoryStore{}
			a := startWithStore(t, path, lib, store)
			if _, err := a.SaveSyncSettings(before); err != nil {
				t.Fatalf("SaveSyncSettings: %v", err)
			}
			// settings.json 换成非空目录，写入时改名失败。
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(path, "blocker"), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(path) })
			store.fail = map[string][]error{"set": {nil, c.restore}}

			_, err := a.SaveSyncSettings(changed)
			if err == nil {
				t.Fatal("SaveSyncSettings succeeded although settings.json could not be written")
			}
			if c.restore != nil && !errors.Is(err, errDenied) {
				t.Fatalf("SaveSyncSettings error = %v; want the failed restore reported too", err)
			}
			if got := store.secrets(t).SecretAccessKey; got != c.wantSecret {
				t.Fatalf("keychain secret = %q; want %q", got, c.wantSecret)
			}
			got, err := a.Credentials()
			if err != nil || got.Sync.SecretAccessKey != c.wantSecret || got.Sync.Bucket != "old-bucket" {
				t.Fatalf("Credentials = %+v, %v; want old settings.json fields with secret %q", got, err, c.wantSecret)
			}
		})
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
