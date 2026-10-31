package main

import (
	"context"
	_ "embed"
	"fmt"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/tiaot33/mechbox/internal/library"
)

const (
	updatesCheckedEvent   = "updates-checked"
	settingsReloadedEvent = "settings-reloaded"
	syncPushedEvent       = "sync-pushed"
)

// autoPushRetry 是自动推送失败后，最早多久再试一次。
const autoPushRetry = time.Hour

// AppService 是应用设置的绑定服务，并按设置在后台做事：自动检查更新、自动推送、Dock 角标、关闭窗口后留在菜单栏。
// 自动检查只读(检查更新不改技能，也不碰装出去的文件夹),结果不发系统通知，只体现在角标和侧栏计数上。
// 自动推送按同步设置里的间隔把库推送成 S3 上这台机器的一份新备份，从不自动拉回;推送出的问题由 PushProblem 给出。
type AppService struct {
	library *LibraryService
	app     *application.App
	window  *application.WebviewWindow

	mu           sync.Mutex
	settings     Settings
	firstOpen    bool
	hidden       bool
	opened       bool
	uninstalling bool
	uninstalled  bool
	lastAttempt  time.Time
	distroError  string

	lastPushAttempt time.Time
	pushProblem     *PushProblem

	removeOnce sync.Once
	discard    func(path string) error

	saveMu sync.Mutex

	nativeMu sync.Mutex
	tray     *application.SystemTray
	menus    nativeMenus

	badgeMu sync.Mutex
	wake    chan struct{}
	stop    context.CancelFunc
	done    chan struct{}
}

// NewAppService 创建应用设置服务;app 与 window 在 main 里创建窗口后填入。
func NewAppService(lib *LibraryService) *AppService {
	a := &AppService{library: lib, discard: discard, wake: make(chan struct{}, 1)}
	if lib != nil {
		lib.afterPull = a.reloadSettings
		lib.afterSyncSettings = a.syncSettingsChanged
		lib.afterSyncPush = a.pushed
	}
	return a
}

// ServiceStartup 从技能库读出设置，换到存着的 WSL 发行版(各 Agent 的全局目录所在),并开始后台检查;
// 它排在 LibraryService 之后启动，技能库已经打开。选了发行版而打不开时，技能库留在 Windows 本机的全局目录，
// 原因由 WSLDistroError 给出。设置读不出时用默认值，并且不当作第一次打开。
func (a *AppService) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	values, err := a.library.lib.AppSettings(ctx)
	a.warn(err, "settings unreadable, using defaults")
	settings, done := settingsFrom(values)
	distro, distroErr := a.library.lib.WSLDistro(ctx)
	a.warn(distroErr, "WSL distro setting unreadable")
	settings.WSLDistro = distro
	a.settings, a.firstOpen = settings, err == nil && !done
	if distro != "" {
		if err := a.library.lib.SetWSLDistro(ctx, distro); err != nil {
			a.distroError = library.ErrorOf(err).Message
		}
	}
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
// Windows 上是系统设置里排第一的显示语言，其他平台与 server 模式读 LC_ALL / LC_MESSAGES / LANG),首选中文(含繁体)时是中文，其余是英文。
func (a *AppService) Language() Language {
	return a.Settings().Language.resolve()
}

// SaveSettings 保存设置并立即生效。前端每次传完整的设置，一次一次依次保存。
// 改了全局目录所在的 WSL 发行版时先交给技能库，打不开就不保存、返回错误，技能库仍用原来的全局目录。
func (a *AppService) SaveSettings(ctx context.Context, settings Settings) (Settings, error) {
	if !settings.CheckInterval.valid() {
		return Settings{}, fmt.Errorf("unknown check interval %q", settings.CheckInterval)
	}
	if !settings.Language.valid() {
		return Settings{}, fmt.Errorf("unknown language %q", settings.Language)
	}
	previous := a.Settings().WSLDistro
	switched := settings.WSLDistro != previous
	if switched {
		if err := a.library.lib.SetWSLDistro(ctx, settings.WSLDistro); err != nil {
			return Settings{}, err
		}
	}
	err := a.saveSettings(ctx, settings, nil)
	if err == nil && switched {
		a.mu.Lock()
		a.distroError = ""
		a.mu.Unlock()
	}
	if err != nil {
		if switched {
			_ = a.library.lib.SetWSLDistro(context.WithoutCancel(ctx), previous)
		}
		return Settings{}, err
	}
	a.menus.setLanguage(settings.Language.resolve())
	a.poke()
	a.refreshBadge(context.Background())
	return settings, nil
}

// WSLDistroError 在设置里选的 WSL 发行版启动时没能打开时给出原因,此时技能库用的是 Windows 本机的全局目录;
// 打开了、没选发行版或之后用 RetryWSLDistro 打开了时为空。前端启动时据此提示，让用户重试或改回 Windows 本机。
func (a *AppService) WSLDistroError() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.distroError
}

// RetryWSLDistro 再打开一次设置里选的 WSL 发行版，打开了就把各 Agent 的全局目录换过去;仍打不开时返回错误。
func (a *AppService) RetryWSLDistro(ctx context.Context) error {
	if err := a.library.lib.SetWSLDistro(ctx, a.Settings().WSLDistro); err != nil {
		a.mu.Lock()
		a.distroError = library.ErrorOf(err).Message
		a.mu.Unlock()
		return err
	}
	a.mu.Lock()
	a.distroError = ""
	a.mu.Unlock()
	return nil
}

// FirstOpen 在这个数据库第一次被 app 打开时返回 true,并在技能库的应用设置里记下已经打开过,之后(含重启)都返回 false。
// 前端据此把用户带到各 Agent 全局目录里不是库装的技能;换到新机器也一样。记不下来时照样返回 true,下次启动会再带一次。
// 设置读不出时返回 false,不拿默认值把它覆盖掉。拉回后保留这台机器已经打开过的记录(见 reloadSettings)。
func (a *AppService) FirstOpen() bool {
	a.mu.Lock()
	first := a.firstOpen
	a.firstOpen = false
	settings := a.settings
	a.mu.Unlock()
	if !first {
		return false
	}
	done := true
	a.warn(a.saveSettings(context.Background(), settings, &done), "settings unwritable, first open not recorded")
	return true
}

// saveSettings 把设置写进技能库的应用设置，写成了才换掉 a.settings;firstOpenDone 为 nil 时按现在的记录。
// 写入依次进行，免得较早的一次后写、盖掉较新的设置。
func (a *AppService) saveSettings(ctx context.Context, settings Settings, firstOpenDone *bool) error {
	a.saveMu.Lock()
	defer a.saveMu.Unlock()
	a.mu.Lock()
	done := !a.firstOpen
	a.mu.Unlock()
	if firstOpenDone != nil {
		done = *firstOpenDone
	}
	if err := a.library.lib.SetAppSettings(ctx, settings.appValues(done)); err != nil {
		return err
	}
	a.mu.Lock()
	a.settings = settings
	a.mu.Unlock()
	return nil
}

// reloadSettings 在拉回之后重新读出设置并立即生效：拉回的库带着推送那台机器的设置。WSL 发行版只属于这台机器，不变;
// 这台机器已经打开过 app 时，把这条记录写回去，免得拉回一份没有它的库后下次启动又当作第一次打开。
// 换完发 settings-reloaded 事件，开着的设置页据此重读，免得之后改一项时拿旧值整组盖掉拉回来的设置。
func (a *AppService) reloadSettings(ctx context.Context) {
	values, err := a.library.lib.AppSettings(ctx)
	if err != nil {
		a.warn(err, "settings unreadable after pull")
		return
	}
	settings, done := settingsFrom(values)
	a.mu.Lock()
	settings.WSLDistro = a.settings.WSLDistro
	keep := !a.firstOpen && !done
	a.settings = settings
	a.mu.Unlock()
	if keep {
		a.warn(a.saveSettings(ctx, settings, &keep), "settings unwritable, first open not kept after pull")
	}
	a.menus.setLanguage(settings.Language.resolve())
	a.poke()
	a.refreshBadge(ctx)
	if a.app != nil {
		go a.app.Event.Emit(settingsReloadedEvent)
	}
}

func (a *AppService) warn(err error, message string) {
	if err != nil && a.app != nil {
		a.app.Logger.Warn(message, "error", err)
	}
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
		if a.pushDue(ctx, time.Now()) {
			a.autoPush(ctx)
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
	var err error
	a.library.inBackground(ctx, func() { _, err = a.library.lib.CheckUpdates(ctx, collections) })
	if err != nil && ctx.Err() == nil {
		a.app.Logger.Warn("automatic update check", "error", err)
	}
	if ctx.Err() != nil {
		return
	}
	a.refreshBadge(ctx)
	go a.app.Event.Emit(updatesCheckedEvent)
}

// PushProblem 是上一次推送(自动或手动)出的问题:推送失败,或备份已经传上去、删超出份数的旧备份失败(Uploaded)。
type PushProblem struct {
	At       time.Time      `json:"at"`
	Error    *library.Error `json:"error"`
	Uploaded bool           `json:"uploaded,omitempty"`
}

// PushProblem 返回上一次推送出的问题;上一次推送完全成功、或之后改了同步设置时为 nil。
// 设置页、左栏与同步弹窗据此提示，免得用户以为一直在备份。只记在内存里，重启后由下一次推送重新得出。
func (a *AppService) PushProblem() *PushProblem {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.pushProblem
}

func (a *AppService) syncSettingsChanged() {
	a.mu.Lock()
	a.lastPushAttempt, a.pushProblem = time.Time{}, nil
	a.mu.Unlock()
	a.poke()
}

func (a *AppService) pushDue(ctx context.Context, now time.Time) bool {
	settings, err := a.library.lib.SyncSettings(ctx)
	if err != nil {
		return false
	}
	period, timed := settings.AutoPush.Period()
	if !timed || !syncConfigured(settings) {
		return false
	}
	status, err := a.library.lib.SyncStatus(ctx)
	if err != nil || now.Sub(status.LastPushedAt) < period {
		return false
	}
	a.mu.Lock()
	last := a.lastPushAttempt
	a.mu.Unlock()
	return now.Sub(last) >= min(period, autoPushRetry)
}

func (a *AppService) autoPush(ctx context.Context) {
	a.mu.Lock()
	a.lastPushAttempt = time.Now().Round(0)
	a.mu.Unlock()
	result, err := a.library.lib.SyncPush(ctx)
	if ctx.Err() != nil {
		return
	}
	a.pushed(result, err)
	if a.app != nil {
		a.warn(err, "automatic sync push")
		go a.app.Event.Emit(syncPushedEvent)
	}
}

func (a *AppService) pushed(result library.SyncPushResult, err error) {
	var problem *PushProblem
	switch {
	case err != nil:
		problem = &PushProblem{At: time.Now().Round(0), Error: library.ErrorOf(err)}
	case result.RemoveError != nil:
		problem = &PushProblem{At: time.Now().Round(0), Error: result.RemoveError, Uploaded: true}
	}
	a.mu.Lock()
	a.pushProblem = problem
	a.mu.Unlock()
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

//go:embed build/tray-template.png
var trayTemplateIcon []byte

//go:embed build/tray.png
var trayIcon []byte

func (a *AppService) newTray() *application.SystemTray {
	menu := a.app.NewMenu()
	a.menus.label(menu.Add(""), "open").OnClick(func(*application.Context) { a.showWindow() })
	menu.AddSeparator()
	a.menus.label(menu.Add(""), "quit").OnClick(func(*application.Context) { a.app.Quit() })
	tray := a.app.SystemTray.New()
	if runtime.GOOS == "darwin" {
		tray.SetTemplateIcon(trayTemplateIcon)
	} else {
		tray.SetIcon(trayIcon)
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
