package main

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/icons"

	"github.com/tiaot33/skills-tui/internal/library"
)

// updatesCheckedEvent 在自动检查更新完成后发给前端，前端据此重读快照。
const updatesCheckedEvent = "updates-checked"

// AppService 是应用设置的绑定服务，并按设置在后台做事：自动检查更新、Dock 角标、关闭窗口后留在菜单栏。
// 自动检查只读(检查更新不改技能，也不碰装出去的文件夹),结果不发系统通知，只体现在角标和侧栏计数上。
type AppService struct {
	path    string
	library *LibraryService
	app     *application.App
	window  *application.WebviewWindow

	mu          sync.Mutex
	settings    Settings
	hidden      bool                    // 窗口已关闭、应用留在菜单栏
	tray        *application.SystemTray // 窗口关闭期间菜单栏上的入口
	opened      bool                    // 应用启动或窗口重新打开后，「只在打开时」还没检查
	lastAttempt time.Time               // 上次自动检查的时间，检查失败时避免每分钟重试

	badgeMu sync.Mutex
	wake    chan struct{}
	stop    context.CancelFunc
	done    chan struct{}
}

// NewAppService 创建应用设置服务;app 与 window 在 main 里创建窗口后填入。
func NewAppService(path string, lib *LibraryService) *AppService {
	return &AppService{path: path, library: lib, wake: make(chan struct{}, 1)}
}

// ServiceStartup 读取设置并开始后台检查;它排在 LibraryService 之后启动，技能库已经打开。
func (a *AppService) ServiceStartup(context.Context, application.ServiceOptions) error {
	settings, err := loadSettings(a.path)
	if err != nil {
		a.app.Logger.Warn("settings unreadable, using defaults", "error", err)
	}
	a.settings = settings
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

// SaveSettings 保存设置并立即生效。
func (a *AppService) SaveSettings(settings Settings) (Settings, error) {
	if !settings.CheckInterval.valid() {
		return Settings{}, fmt.Errorf("unknown check interval %q", settings.CheckInterval)
	}
	a.mu.Lock()
	err := saveSettings(a.path, settings)
	if err == nil {
		a.settings = settings
	}
	a.mu.Unlock()
	if err != nil {
		return Settings{}, err
	}
	a.poke()
	a.refreshBadge(context.Background())
	return settings, nil
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

// badgeCount 数有新版本的技能，本地目录来源的不计入(它们不检查更新，只在用户自己重新读取时变)。
func badgeCount(skills []library.Skill) int {
	n := 0
	for _, skill := range skills {
		if skill.Source.Type != library.SourceLocal && skill.LastUpdateCheck != nil &&
			skill.LastUpdateCheck.Status == library.UpdateAvailable {
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

// loop 每分钟看一次是否到了检查的时间;设置改变或窗口重新打开时立刻再看。
// 按时间检查以技能库里记下的检查时间为准，所以重启应用不会重新计时，手动检查过也算。
func (a *AppService) loop(ctx context.Context) {
	defer close(a.done)
	a.refreshBadge(ctx)
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		if collections := a.due(ctx); len(collections) > 0 {
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

// due 返回到了该检查的集合(本地目录来源不检查更新);不到时间时为空。
func (a *AppService) due(ctx context.Context) []library.CollectionID {
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
	since := oldestConfirmed(skills, time.Now())
	if last.After(since) {
		since = last
	}
	if time.Since(since) < period {
		return nil
	}
	return ids
}

// oldestConfirmed 返回最久没确认过来源的那个技能上次确认的时间，它决定是否到了检查的时间;
// 从未检查过的技能从入库那一刻算起，本地目录来源的不算。没有这样的技能时是 now。
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

// check 检查这些集合有没有新版本，结论随技能保存;不发系统通知，只更新角标并让前端重读。
func (a *AppService) check(ctx context.Context, collections []library.CollectionID) {
	a.mu.Lock()
	// 去掉单调时钟读数，按挂钟计时：电脑睡眠的时间也算进间隔。
	a.lastAttempt = time.Now().Round(0)
	a.mu.Unlock()
	if _, err := a.library.lib.CheckUpdates(ctx, collections); err != nil && ctx.Err() == nil {
		a.app.Logger.Warn("automatic update check", "error", err)
	}
	if ctx.Err() != nil {
		return
	}
	a.refreshBadge(ctx)
	// 发事件要等主线程;不在这里等，免得应用退出时(主线程正等着这个 goroutine 结束)互相卡住。
	go a.app.Event.Emit(updatesCheckedEvent)
}

// windowClosing 在用户关闭主窗口时调用：留在菜单栏时取消关闭、隐藏窗口，并在菜单栏放一个入口;
// 否则照常关闭，最后一个窗口关闭后应用退出。退出应用时窗口照常关闭。
func (a *AppService) windowClosing(event *application.WindowEvent) {
	if a.app.Context().Err() != nil || !a.Settings().StayInMenuBar {
		return
	}
	event.Cancel()
	a.window.Hide()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.hidden = true
	if a.tray == nil {
		a.tray = a.newTray()
	}
}

func (a *AppService) newTray() *application.SystemTray {
	menu := a.app.NewMenu()
	menu.Add("打开技能管理器").OnClick(func(*application.Context) { a.showWindow() })
	menu.AddSeparator()
	menu.Add("退出技能管理器").OnClick(func(*application.Context) { a.app.Quit() })
	tray := a.app.SystemTray.New()
	if runtime.GOOS == "darwin" {
		tray.SetTemplateIcon(icons.SystrayMacTemplate)
	} else {
		tray.SetIcon(icons.SystrayLight)
	}
	tray.SetTooltip("技能管理器")
	tray.SetMenu(menu)
	return tray
}

// showWindow 把主窗口带到前台;窗口原先关在菜单栏时去掉菜单栏入口，并算作一次打开。
func (a *AppService) showWindow() {
	if a.window.IsMinimised() {
		a.window.UnMinimise()
	}
	a.window.Show()
	a.window.Focus()
	a.mu.Lock()
	reopened, tray := a.hidden, a.tray
	a.hidden, a.tray = false, nil
	if reopened {
		a.opened = true
	}
	a.mu.Unlock()
	if tray != nil {
		tray.Destroy()
	}
	if reopened {
		a.poke()
	}
}

// attach 接上主窗口：关闭窗口按设置留在菜单栏;窗口关着时点 Dock 图标重新打开它。
func (a *AppService) attach(app *application.App, window *application.WebviewWindow) {
	a.app, a.window = app, window
	window.RegisterHook(events.Common.WindowClosing, a.windowClosing)
	app.Event.OnApplicationEvent(events.Mac.ApplicationShouldHandleReopen, func(event *application.ApplicationEvent) {
		if !event.Context().HasVisibleWindows() {
			a.showWindow()
		}
	})
}
