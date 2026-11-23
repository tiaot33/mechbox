package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/tiaot33/mechbox/internal/library"
)

// testEvents 记下发给前端的事件，代替 application.App 的 Event。
type testEvents struct {
	mu    sync.Mutex
	names []string
}

func (e *testEvents) Emit(name string, _ ...any) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.names = append(e.names, name)
	return true
}

func (e *testEvents) count(name string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(slices.DeleteFunc(slices.Clone(e.names), func(n string) bool { return n != name }))
}

// waitFor 等到 name 发出过(事件在另一个 goroutine 里发)。
func (e *testEvents) waitFor(t *testing.T, name string) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); e.count(name) == 0; {
		if time.Now().After(deadline) {
			t.Fatalf("event %q not emitted", name)
		}
		time.Sleep(time.Millisecond)
	}
}

// testServices 是 newServices 接好的一套(和 run 一样，只是没有 app、窗口与新版本);quits 数 app 被要求退出了几次。
type testServices struct {
	lib     *LibraryService
	app     *AppService
	events  *testEvents
	quits   atomic.Int32
	started bool
	stopped bool
}

func tempLibrary(t *testing.T) string {
	return filepath.Join(t.TempDir(), "library.db")
}

// newTestServices 在 path 处打开技能库并接好两个绑定服务，还不启动 AppService;测试结束时按 Wails 的顺序关掉。
func newTestServices(t *testing.T, path string) *testServices {
	t.Helper()
	lib, err := openLibrary(path)
	if err != nil {
		t.Fatalf("openLibrary: %v", err)
	}
	s := &testServices{events: &testEvents{}}
	s.lib, s.app = newServices(path, lib, s.events, slog.New(slog.DiscardHandler), func() { s.quits.Add(1) })
	t.Cleanup(func() { s.stop(t) })
	return s
}

func (s *testServices) start(t *testing.T) *testServices {
	t.Helper()
	if err := s.app.ServiceStartup(t.Context(), application.ServiceOptions{}); err != nil {
		t.Fatalf("ServiceStartup: %v", err)
	}
	s.started = true
	return s
}

// stop 按 Wails 退出的顺序关掉两个服务(AppService 先、LibraryService 后),只关一次。
func (s *testServices) stop(t *testing.T) {
	t.Helper()
	if s.stopped {
		return
	}
	s.stopped = true
	if s.started {
		if err := s.app.ServiceShutdown(); err != nil {
			t.Errorf("AppService shutdown: %v", err)
		}
	}
	if err := s.lib.ServiceShutdown(); err != nil {
		t.Errorf("LibraryService shutdown: %v", err)
	}
}

func startServices(t *testing.T, path string) *testServices {
	t.Helper()
	return newTestServices(t, path).start(t)
}

// startLibrary 打开一个临时的技能库，返回接好的 LibraryService。
func startLibrary(t *testing.T) *LibraryService {
	t.Helper()
	return newTestServices(t, tempLibrary(t)).lib
}

func fetchSkill(t *testing.T, lib *library.Library, req library.PreviewFetchRequest, name string) library.Skill {
	t.Helper()
	preview, err := lib.PreviewFetch(t.Context(), req)
	if err != nil {
		t.Fatalf("PreviewFetch: %v", err)
	}
	skills, err := lib.Fetch(t.Context(), preview.ID, []string{name})
	if err != nil || len(skills) != 1 {
		t.Fatalf("Fetch = %v, %v", skills, err)
	}
	return skills[0]
}

// fetchLocalSkill 在临时目录里写一个只有 SKILL.md 的本地技能并入库。
func fetchLocalSkill(t *testing.T, lib *library.Library, name string) library.Skill {
	t.Helper()
	src := filepath.Join(t.TempDir(), "mine", name)
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: "+name+"\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return fetchSkill(t, lib, library.PreviewFetchRequest{Input: filepath.Dir(src)}, name)
}

// 设置存在技能库里:重启(同一个数据库重新打开)后仍是保存的那一组。
func TestAppServiceKeepsSettingsAcrossRestart(t *testing.T) {
	path := tempLibrary(t)
	first := startServices(t, path)
	if got := first.app.Settings(); !reflect.DeepEqual(got, defaultSettings()) {
		t.Fatalf("first start = %+v; want defaults", got)
	}
	want := Settings{AutoCheck: false, CheckInterval: CheckWeekly, StayInMenuBar: false, DockBadge: false, Language: LanguageEn, FoldCollections: true, FoldBelow: 2, AgentsShown: map[library.AgentCode]bool{library.AgentClaudeCode: false, "custom-0a1b2c3d4e5f": true}}
	if _, err := first.app.SaveSettings(t.Context(), want); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	first.stop(t)

	if got := startServices(t, path).app.Settings(); !reflect.DeepEqual(got, want) {
		t.Fatalf("after restart = %+v; want %+v", got, want)
	}
}

func TestFirstOpenIsTrueOnlyOnceOnThisMachine(t *testing.T) {
	path := tempLibrary(t)
	s := startServices(t, path)
	first := s.app
	if !first.FirstOpen() {
		t.Fatal("FirstOpen() on a new machine = false; want true")
	}
	if first.FirstOpen() {
		t.Fatal("FirstOpen() a second time = true; want false")
	}
	// 之后保存设置也不会把它抹掉。
	if _, err := first.SaveSettings(t.Context(), defaultSettings()); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	s.stop(t)
	if startServices(t, path).app.FirstOpen() {
		t.Fatal("FirstOpen() after restart = true; want false")
	}

	// 换到新机器(新的数据库)又是第一次。
	if !startServices(t, tempLibrary(t)).app.FirstOpen() {
		t.Fatal("FirstOpen() on another machine = false; want true")
	}
}

// 从旧版本升级上来的数据库(第一次打开在升级前就做过了)升级后第一次打开时提示一次按检测结果添加 Agent:只提示一次,重启、拉回后都不再提示;
// 新数据库在第一次打开的流程里已经按检测结果添加过,不再提示。
func TestAgentPoolIsOfferedOnceAfterUpgrade(t *testing.T) {
	path := tempLibrary(t)
	s := newTestServices(t, path)
	lib := s.lib
	if err := lib.lib.SetAppSettings(t.Context(), map[string]string{firstOpenDoneKey: "true"}); err != nil {
		t.Fatal(err)
	}
	upgraded := s.start(t).app
	if upgraded.FirstOpen() {
		t.Fatal("FirstOpen() on an upgraded library = true; want false")
	}
	// 没记下之前(如读候选池失败)一直待提示,不会被白白用掉。
	if !upgraded.AgentPoolPending() || !upgraded.AgentPoolPending() {
		t.Fatal("AgentPoolPending() after upgrade = false; want true until the offer is recorded")
	}
	upgraded.AgentPoolOffered()
	if upgraded.AgentPoolPending() {
		t.Fatal("AgentPoolPending() after AgentPoolOffered = true; want false")
	}
	if err := lib.lib.SetAppSettings(t.Context(), map[string]string{firstOpenDoneKey: "true", "language": "en"}); err != nil {
		t.Fatal(err)
	}
	upgraded.settings.reload(t.Context())
	if values, err := lib.lib.AppSettings(t.Context()); err != nil || values[agentPoolOfferedKey] != "true" || values["language"] != "en" {
		t.Fatalf("app settings after pull = %v, %v; want the offer kept", values, err)
	}
	s.stop(t)
	if startServices(t, path).app.AgentPoolPending() {
		t.Fatal("AgentPoolPending() after restart = true; want false")
	}

	fresh := tempLibrary(t)
	s = startServices(t, fresh)
	first := s.app
	if !first.FirstOpen() || first.AgentPoolPending() {
		t.Fatal("a new library: want FirstOpen() and no agent pool offer")
	}
	s.stop(t)
	if startServices(t, fresh).app.AgentPoolPending() {
		t.Fatal("AgentPoolPending() after first open and restart = true; want false")
	}
}

// 设置里选的 WSL 发行版启动时没能打开时,检测的是 Windows 本机,先不提示添加 Agent,也不记下;发行版打开后再提示。
func TestAgentPoolWaitsForTheChosenDistro(t *testing.T) {
	s := newTestServices(t, tempLibrary(t))
	if err := s.lib.lib.SetAppSettings(t.Context(), map[string]string{firstOpenDoneKey: "true"}); err != nil {
		t.Fatal(err)
	}
	app := s.start(t).app
	app.settings.mu.Lock()
	app.settings.distroError = "distro unavailable"
	app.settings.mu.Unlock()
	if app.AgentPoolPending() {
		t.Fatal("AgentPoolPending() while the chosen distro is unavailable = true; want false")
	}
	app.settings.mu.Lock()
	app.settings.distroError = ""
	app.settings.mu.Unlock()
	if !app.AgentPoolPending() {
		t.Fatal("AgentPoolPending() once the distro opened = false; want true")
	}
}

// 设置读不出时用默认值，不当作第一次打开，也不拿默认值覆盖库里的设置。
func TestFirstOpenLeavesUnreadableSettingsAlone(t *testing.T) {
	s := newTestServices(t, tempLibrary(t))
	lib, app := s.lib, s.app
	if err := lib.lib.SetAppSettings(t.Context(), map[string]string{"language": "en"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := app.ServiceStartup(ctx, application.ServiceOptions{}); err != nil {
		t.Fatalf("ServiceStartup: %v", err)
	}
	s.started = true
	if got := app.Settings(); !reflect.DeepEqual(got, defaultSettings()) {
		t.Fatalf("Settings with unreadable library = %+v; want defaults", got)
	}
	if app.FirstOpen() {
		t.Fatal("FirstOpen() with unreadable settings = true; want false")
	}
	if values, err := lib.lib.AppSettings(t.Context()); err != nil || values["language"] != "en" || values[firstOpenDoneKey] != "" {
		t.Fatalf("app settings = %v, %v; want them left alone", values, err)
	}
}

// 拉回后设置换成拉回的那份库里的，立即生效;这台机器已经打开过的记录保留，WSL 发行版不变。
func TestSettingsFollowPulledLibrary(t *testing.T) {
	s := startServices(t, tempLibrary(t))
	lib, app := s.lib, s.app
	if !app.FirstOpen() {
		t.Fatal("FirstOpen() = false; want true")
	}
	var applied []Settings
	app.settings.onChange(func(_ context.Context, settings Settings) { applied = append(applied, settings) })
	if err := lib.lib.SetAppSettings(t.Context(), map[string]string{"language": "en", "dock_badge": "false"}); err != nil {
		t.Fatal(err)
	}
	app.settings.reload(t.Context())
	want := defaultSettings()
	want.Language, want.DockBadge = LanguageEn, false
	if got := app.Settings(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Settings after pull = %+v; want %+v", got, want)
	}
	if len(applied) != 1 || !reflect.DeepEqual(applied[0], want) {
		t.Fatalf("settings applied after pull = %+v; want the pulled ones once", applied)
	}
	s.events.waitFor(t, settingsReloadedEvent)
	if values, err := lib.lib.AppSettings(t.Context()); err != nil || values[firstOpenDoneKey] != "true" {
		t.Fatalf("app settings after pull = %v, %v; want first open kept", values, err)
	}
}
