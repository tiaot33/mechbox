package main

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/icons"

	"github.com/tiaot33/mechbox/internal/library"
)

const updatesCheckedEvent = "updates-checked"

// AppService 是应用设置的绑定服务，并按设置在后台做事：自动检查更新、Dock 角标、关闭窗口后留在菜单栏。
// 自动检查只读(检查更新不改技能，也不碰装出去的文件夹),结果不发系统通知，只体现在角标和侧栏计数上。
type AppService struct {
	path    string
	library *LibraryService
	app     *application.App
	window  *application.WebviewWindow

	mu           sync.Mutex
	settings     Settings
	sync         syncFields
	firstOpen    bool
	hidden       bool
	opened       bool
	uninstalling bool
	uninstalled  bool
	lastAttempt  time.Time

	removeOnce sync.Once
	discard    func(path string) error

	credMu      sync.Mutex
	keychain    secretStore
	secrets     secrets
	secretsRead bool

	nativeMu sync.Mutex
	tray     *application.SystemTray
	menus    nativeMenus

	badgeMu sync.Mutex
	wake    chan struct{}
	stop    context.CancelFunc
	done    chan struct{}
}

// NewAppService 创建应用设置服务;app 与 window 在 main 里创建窗口后填入。
func NewAppService(path string, lib *LibraryService) *AppService {
	return &AppService{path: path, library: lib, keychain: defaultKeychain(), discard: discard, wake: make(chan struct{}, 1)}
}

// ServiceStartup 读取设置，把设置页里填的 GitHub 令牌与同步设置交给技能库，并开始后台检查;
// 它排在 LibraryService 之后启动，技能库已经打开。
func (a *AppService) ServiceStartup(context.Context, application.ServiceOptions) error {
	file, err := loadSettings(a.path)
	if err != nil && a.app != nil {
		a.app.Logger.Warn("settings unreadable, using defaults", "error", err)
	}
	a.settings, a.sync, a.firstOpen = file.Settings, file.Sync, err == nil && !file.FirstOpenDone
	a.credMu.Lock()
	if err := a.readSecrets(); err != nil && a.app != nil {
		a.app.Logger.Warn("keychain unreadable", "error", err)
	}
	a.applyCredentials(a.credentials())
	a.credMu.Unlock()
	a.menus.setLanguage(a.settings.Language.resolve())
	a.opened = true
	loop, stop := context.WithCancel(context.Background())
	a.stop = stop
	a.done = make(chan struct{})
	go a.loop(loop)
	return nil
}

// ServiceShutdown 停止后台检查;它先于 LibraryService 关闭，进行中的检查随之取消。
func (a *AppService) ServiceShutdown() error {
	if a.stop != nil {
		a.stop()
		<-a.done
	}
	return nil
}

// Settings 返回当前设置。
func (a *AppService) Settings() Settings {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.settings
}

// Language 返回界面实际用的语言(zh 或 en):设置里选了的按设置;跟随系统时看系统的首选语言(macOS 上是系统设置里的语言顺序，
// 其他平台与 server 模式读 LC_ALL / LC_MESSAGES / LANG),首选中文(含繁体)时是中文，其余是英文。
func (a *AppService) Language() Language {
	return a.Settings().Language.resolve()
}

// SaveSettings 保存设置并立即生效。前端每次传完整的设置，一次一次依次保存。
func (a *AppService) SaveSettings(settings Settings) (Settings, error) {
	if !settings.CheckInterval.valid() {
		return Settings{}, fmt.Errorf("unknown check interval %q", settings.CheckInterval)
	}
	if !settings.Language.valid() {
		return Settings{}, fmt.Errorf("unknown language %q", settings.Language)
	}
	a.mu.Lock()
	err := saveSettings(a.path, settingsFile{Settings: settings, Sync: a.sync, FirstOpenDone: !a.firstOpen})
	if err == nil {
		a.settings = settings
	}
	a.mu.Unlock()
	if err != nil {
		return Settings{}, err
	}
	a.menus.setLanguage(settings.Language.resolve())
	a.poke()
	a.refreshBadge(context.Background())
	return settings, nil
}

// FirstOpen 在这台机器上第一次打开 app 时返回 true,并在 settings.json 里记下已经打开过,之后(含重启)都返回 false。
// 前端据此把用户带到各 Agent 全局目录里不是库装的技能;换到新机器也一样。记不下来时照样返回 true,下次启动会再带一次。
// settings.json 在却读不出时返回 false,不拿默认值把它覆盖掉。
func (a *AppService) FirstOpen() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.firstOpen {
		return false
	}
	a.firstOpen = false
	if err := saveSettings(a.path, settingsFile{Settings: a.settings, Sync: a.sync, FirstOpenDone: true}); err != nil && a.app != nil {
		a.app.Logger.Warn("settings unwritable, first open not recorded", "error", err)
	}
	return true
}

// RevealInFinder 在 Finder 中选中 path(作用域里的技能文件夹等);是符号链接时选中链接本身。
// server 模式与 macOS 以外的平台什么都不做。
func (a *AppService) RevealInFinder(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("reveal in finder: path must be absolute: %q", path)
	}
	return reveal(path)
}

// RefreshBadge 按技能库现在的检查更新结论重设 Dock 角标;前端每次重读快照后调用。
func (a *AppService) RefreshBadge(ctx context.Context) {
	a.refreshBadge(ctx)
}

func (a *AppService) refreshBadge(ctx context.Context) {
	a.badgeMu.Lock()
	defer a.badgeMu.Unlock()
	n := 0
	if a.Settings().DockBadge {
		skills, err := a.library.lib.ListSkills(ctx)
		if err != nil {
			return
		}
		n = badgeCount(skills)
	}
	setBadge(n)
}

func badgeCount(skills []library.Skill) int {
	n := 0
	for _, skill := range skills {
		if skill.LastUpdateCheck != nil && skill.LastUpdateCheck.Pending {
			n++
		}
	}
	return n
}

func (a *AppService) poke() {
	select {
	case a.wake <- struct{}{}:
	default:
	}
}

func (a *AppService) loop(ctx context.Context) {
	defer close(a.done)
	a.refreshBadge(ctx)
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		if collections := a.due(ctx, time.Now()); len(collections) > 0 {
			a.check(ctx, collections)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-a.wake:
		}
	}
}

func (a *AppService) due(ctx context.Context, now time.Time) []library.CollectionID {
	a.mu.Lock()
	settings, opened, last := a.settings, a.opened, a.lastAttempt
	a.opened = false
	a.mu.Unlock()
	if !settings.AutoCheck {
		return nil
	}
	collections, err := a.library.lib.ListCollections(ctx)
	if err != nil {
		return nil
	}
	var ids []library.CollectionID
	for _, c := range collections {
		if c.Source.Type != library.SourceLocal {
			ids = append(ids, c.ID)
		}
	}
	period, timed := settings.CheckInterval.period()
	if !timed || len(ids) == 0 {
		if opened {
			return ids
		}
		return nil
	}
	skills, err := a.library.lib.ListSkills(ctx)
	if err != nil {
		return nil
	}
	since := oldestConfirmed(skills, now)
	if last.After(since) {
		since = last
	}
	if now.Sub(since) < period {
		return nil
	}
	return ids
}

func oldestConfirmed(skills []library.Skill, now time.Time) time.Time {
	oldest := now
	for _, skill := range skills {
		if skill.Source.Type == library.SourceLocal {
			continue
		}
		seen := skill.FetchedAt
		if skill.LastUpdateCheck != nil {
			seen = skill.LastUpdateCheck.CheckedAt
		}
		if seen.Before(oldest) {
			oldest = seen
		}
	}
	return oldest
}

func (a *AppService) check(ctx context.Context, collections []library.CollectionID) {
	a.mu.Lock()
	a.lastAttempt = time.Now().Round(0)
	a.mu.Unlock()
	if _, err := a.library.lib.CheckUpdates(ctx, collections); err != nil && ctx.Err() == nil {
		a.app.Logger.Warn("automatic update check", "error", err)
	}
	if ctx.Err() != nil {
		return
	}
	a.refreshBadge(ctx)
	go a.app.Event.Emit(updatesCheckedEvent)
}

func (a *AppService) windowClosing(event *application.WindowEvent) {
	if a.app.Context().Err() != nil {
		return
	}
	event.Cancel()
	if !a.Settings().StayInMenuBar {
		a.app.Quit()
		return
	}
	a.nativeMu.Lock()
	a.window.Hide()
	if a.tray == nil {
		a.tray = a.newTray()
	}
	a.tray.Show()
	a.nativeMu.Unlock()
	a.mu.Lock()
	a.hidden = true
	a.mu.Unlock()
}

func (a *AppService) newTray() *application.SystemTray {
	menu := a.app.NewMenu()
	a.menus.label(menu.Add(""), "open").OnClick(func(*application.Context) { a.showWindow() })
	menu.AddSeparator()
	a.menus.label(menu.Add(""), "quit").OnClick(func(*application.Context) { a.app.Quit() })
	tray := a.app.SystemTray.New()
	if runtime.GOOS == "darwin" {
		tray.SetTemplateIcon(icons.SystrayMacTemplate)
	} else {
		tray.SetIcon(icons.SystrayLight)
	}
	tray.SetTooltip("Mechbox")
	tray.SetMenu(menu)
	return tray
}

func (a *AppService) showWindow() {
	a.nativeMu.Lock()
	if a.window.IsMinimised() {
		a.window.UnMinimise()
	}
	a.window.Show()
	a.window.Focus()
	if a.tray != nil {
		a.tray.Hide()
	}
	a.nativeMu.Unlock()
	a.mu.Lock()
	reopened := a.hidden
	a.hidden = false
	if reopened {
		a.opened = true
	}
	a.mu.Unlock()
	if reopened {
		a.poke()
	}
}

func (a *AppService) attach(app *application.App, window *application.WebviewWindow) {
	a.app, a.window = app, window
	a.menus.attach(app)
	window.RegisterHook(events.Common.WindowClosing, a.windowClosing)
	app.Event.OnApplicationEvent(events.Mac.ApplicationShouldHandleReopen, func(event *application.ApplicationEvent) {
		if !event.Context().HasVisibleWindows() {
			a.showWindow()
		}
	})
}
