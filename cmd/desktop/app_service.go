package main

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/tiaot33/mechbox/cmd/desktop/internal/release"
	"github.com/tiaot33/mechbox/cmd/desktop/internal/system"
)

const (
	updatesCheckedEvent   = "updates-checked"
	settingsReloadedEvent = "settings-reloaded"
	syncPushedEvent       = "sync-pushed"
)

// emitter 发出给前端的事件，即 application.App 的 Event。
type emitter interface {
	Emit(name string, data ...any) bool
}

func warn(log *slog.Logger, err error, message string) {
	if err != nil {
		log.Warn(message, "error", err)
	}
}

// AppService 是开放给前端的应用服务：应用设置、自动推送出的问题、Mechbox 的新版本与卸载 Mechbox,
// 另外管着窗口与菜单栏图标(关闭窗口后留在菜单栏、从菜单栏或 Dock 重新打开)。
// 应用设置的读写、自动检查更新、自动推送、Dock 角标与卸载各是单独的一块，由 run 建好后交给它;启动时它让后台的几件事开始，退出时停下。
type AppService struct {
	app      *application.App
	window   *application.WebviewWindow
	settings *settingsStore
	checks   *autoCheck
	pushes   *autoPush
	badge    *dockBadge
	releases *release.Releases
	removal  *uninstaller

	nativeMu sync.Mutex
	tray     *application.SystemTray
	hidden   bool
	menus    nativeMenus

	stop  context.CancelFunc
	loops sync.WaitGroup
}

// ServiceStartup 从技能库读出设置并让它生效，然后开始自动检查更新、自动推送，以及检查 Mechbox 的新版本(这个构建检查时)。
// 它排在 LibraryService 之后启动。
func (a *AppService) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	a.settings.load(ctx)
	settings := a.settings.current()
	a.menus.setLanguage(settings.Language.resolve())
	a.applyAppearance(settings.Appearance)
	loop, stop := context.WithCancel(context.Background())
	a.stop = stop
	a.loops.Go(func() { a.checks.run(loop) })
	a.loops.Go(func() { a.pushes.run(loop) })
	if a.releases != nil {
		a.loops.Go(func() { a.releases.Watch(loop, a.releaseChecked) })
	}
	return nil
}

// ServiceShutdown 停下后台的几件事;它先于 LibraryService 关闭，进行中的检查与推送随之取消，技能库在它们停下后才关上。
func (a *AppService) ServiceShutdown() error {
	a.stop()
	a.loops.Wait()
	return nil
}

// Settings 返回当前设置。
func (a *AppService) Settings() Settings {
	return a.settings.current()
}

// Language 返回界面实际用的语言(zh 或 en):设置里选了的按设置;跟随系统时看系统的首选语言(macOS 上是系统设置里的语言顺序，
// Windows 上是系统设置里排第一的显示语言，其他平台与 server 模式读 LC_ALL / LC_MESSAGES / LANG),首选中文(含繁体)时是中文，其余是英文。
func (a *AppService) Language() Language {
	return a.Settings().Language.resolve()
}

// ReduceTransparency 返回系统是否要求减少透明度：macOS 上读系统设置里的「降低透明度」(WKWebView 不认 prefers-reduced-transparency);
// 其他平台返回 false,由前端的媒体查询判断。前端在启动和窗口重新获得焦点时问一次，为真时毛玻璃换成不透明的底。
func (a *AppService) ReduceTransparency() bool {
	return system.ReducesTransparency()
}

// SaveSettings 保存设置并立即生效。前端每次传完整的设置，一次一次依次保存。
// 改了全局目录所在的 WSL 发行版时先交给技能库，打不开就不保存、返回错误，技能库仍用原来的全局目录。
func (a *AppService) SaveSettings(ctx context.Context, settings Settings) (Settings, error) {
	return a.settings.save(ctx, settings)
}

// WSLDistroError 在设置里选的 WSL 发行版启动时没能打开时给出原因,此时技能库用的是 Windows 本机的全局目录;
// 打开了、没选发行版或之后用 RetryWSLDistro 打开了时为空。前端启动时据此提示，让用户重试或改回 Windows 本机。
func (a *AppService) WSLDistroError() string {
	return a.settings.wslDistroError()
}

// RetryWSLDistro 再打开一次设置里选的 WSL 发行版，打开了就把各 Agent 的全局目录换过去;仍打不开时返回错误。
func (a *AppService) RetryWSLDistro(ctx context.Context) error {
	return a.settings.retryWSLDistro(ctx)
}

// FirstOpen 在这个数据库第一次被 app 打开时返回 true,并在技能库的应用设置里记下已经打开过,之后(含重启)都返回 false。
// 前端据此把用户带到各 Agent 全局目录里不是库装的技能;换到新机器也一样。记不下来时照样返回 true,下次启动会再带一次。
// 设置读不出时返回 false,不拿默认值把它覆盖掉。拉回后保留这台机器已经打开过的记录(见 settingsStore.reload)。
func (a *AppService) FirstOpen() bool {
	return a.settings.takeFirstOpen()
}

// AgentPoolPending 在从旧版本升级上来的数据库(第一次打开的流程在升级前就做过了,当时还没有按检测结果添加 Agent)还没提示过
// 按检测结果添加 Agent 时返回 true;前端据此弹一次「添加 Agent」,检测到的默认勾上,读到候选池之后调用 AgentPoolOffered 记下。
// 第一次打开的流程里已经按检测结果添加过,不再提示。设置里选的 WSL 发行版启动时没能打开时也返回 false:
// 这时检测的是 Windows 本机的目录,等发行版打开了(重试或下次启动)再提示。
func (a *AppService) AgentPoolPending() bool {
	return a.settings.agentPoolPending()
}

// AgentPoolOffered 记下已经按检测结果提示过添加 Agent,之后(含重启、拉回)AgentPoolPending 都返回 false。
// 记不下来时下次启动会再提示一次。
func (a *AppService) AgentPoolOffered() {
	a.settings.agentPoolOffered()
}

// RevealInFileManager 在系统的文件管理器里显示 path(作用域里的技能文件夹等):macOS 在 Finder 中选中它,
// Windows 在文件资源管理器中选中它,是符号链接时选中链接本身;Linux 用 xdg-open 打开它所在的目录，不等文件管理器退出。server 模式什么都不做。
func (a *AppService) RevealInFileManager(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("reveal in file manager: path must be absolute: %q", path)
	}
	return system.Reveal(path)
}

// RefreshBadge 按技能库现在的检查更新结论重设 Dock 角标;前端每次重读快照后调用。
func (a *AppService) RefreshBadge(ctx context.Context) {
	a.badge.refresh(ctx)
}

// PushProblem 返回上一次推送出的问题;上一次推送完全成功、或之后改了同步设置时为 nil。
// 设置页、左栏与同步弹窗据此提示，免得用户以为一直在备份。只记在内存里，重启后由下一次推送重新得出。
func (a *AppService) PushProblem() *PushProblem {
	return a.pushes.lastProblem()
}

// UninstallApp 卸载 Mechbox：按选项把装出去的技能文件夹移到废纸篓
// (先现查各处状态：已经不在的、在不在这台机器上的项目里的跳过，选了保留改过的时跳过用户改过的),然后退出;
// 退出时技能库关上以后，再删除数据库(连同存在里面的设置、令牌与同步设置)与系统给应用留下的数据，
// 并把应用移到废纸篓(移不动时在 Finder 中选中它)。
// 移文件夹中途失败时返回错误、不退出，数据库还在，已经移走的那几处之后巡检为文件夹不在了，重试时跳过。S3 上的备份不动。
func (a *AppService) UninstallApp(ctx context.Context, opts UninstallOptions) (UninstallResult, error) {
	return a.removal.uninstall(ctx, opts)
}

// attach 装好顶部菜单栏、窗口与应用的事件，并登记设置换了以后让界面语言、外观与 Dock 角标跟着变。在 app 运行前调用一次。
func (a *AppService) attach() {
	var checkRelease func()
	if a.releases != nil {
		checkRelease = a.requestReleaseCheck
	}
	a.menus.attach(a.app, checkRelease)
	a.window.RegisterHook(events.Common.WindowClosing, a.windowClosing)
	a.watchAppearance()
	a.app.Event.OnApplicationEvent(events.Mac.ApplicationShouldHandleReopen, func(event *application.ApplicationEvent) {
		if !event.Context().HasVisibleWindows() {
			a.showWindow()
		}
	})
	a.settings.onChange(a.settingsChanged)
}

// settingsChanged 让保存或拉回的设置立即生效：菜单文字、窗口背后的系统材质、自动检查更新的间隔与 Dock 角标。
func (a *AppService) settingsChanged(ctx context.Context, settings Settings) {
	a.menus.setLanguage(settings.Language.resolve())
	a.applyAppearance(settings.Appearance)
	a.checks.poke()
	a.badge.refresh(ctx)
}

// applyAppearance 让窗口背后的系统材质跟着外观变：macOS 上是整个 app 的外观(窗口背后的毛玻璃、菜单与系统弹窗),
// Windows 上是窗口的深浅色(Mica 与标题栏)和没有 Mica 时的不透明底色。跟随系统时 macOS 交给系统;
// Windows 上由 watchAppearance 在窗口显示出来与系统切换深浅色时再调一次。界面本身的配色由前端按设置与 prefers-color-scheme 切换。
func (a *AppService) applyAppearance(appearance Appearance) {
	system.SetAppearance(a.window, appearance.native())
}

// watchAppearance 在 Windows 上补设窗口的深浅色：窗口显示出来时才有窗口句柄(启动时设不上);
// 系统切换深浅色时，跟随系统的窗口跟着变。其他平台不会发这两个事件。
func (a *AppService) watchAppearance() {
	a.window.OnWindowEvent(events.Windows.WindowShow, func(*application.WindowEvent) {
		a.applyAppearance(a.Settings().Appearance)
	})
	a.app.Event.OnApplicationEvent(events.Windows.SystemThemeChanged, func(*application.ApplicationEvent) {
		if appearance := a.Settings().Appearance; appearance == AppearanceSystem {
			a.applyAppearance(appearance)
		}
	})
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
	defer a.nativeMu.Unlock()
	a.window.Hide()
	if a.tray == nil {
		a.tray = a.newTray()
	}
	a.tray.Show()
	a.hidden = true
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
		tray.SetIcon(trayIcon).SetDarkModeIcon(trayIcon)
	}
	tray.SetTooltip("Mechbox")
	tray.SetMenu(menu)
	return tray
}

// showWindow 把窗口带到前台;窗口原来收在菜单栏里时收起菜单栏图标，并算作重新打开了一次(「只在打开时」检查更新)。
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
	reopened := a.hidden
	a.hidden = false
	a.nativeMu.Unlock()
	if reopened {
		a.checks.reopened()
	}
}
