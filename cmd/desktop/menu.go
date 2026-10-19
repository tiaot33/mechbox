package main

import (
	"runtime"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// nativeLabels 是 Go 这边画的菜单文字：菜单栏图标的菜单与 macOS 顶部菜单栏，按界面语言。
var nativeLabels = map[Language]map[string]string{
	LanguageZh: {
		"open": "打开 Mechbox", "quit": "退出 Mechbox",
		"about": "关于 Mechbox", "services": "服务", "hide": "隐藏 Mechbox", "hideOthers": "隐藏其他", "showAll": "全部显示",
		"file": "文件", "close": "关闭窗口",
		"edit": "编辑", "undo": "撤销", "redo": "重做", "cut": "剪切", "copy": "拷贝", "paste": "粘贴",
		"pasteAndMatchStyle": "粘贴并匹配样式", "delete": "删除", "selectAll": "全选",
		"speech": "语音", "startSpeaking": "开始朗读", "stopSpeaking": "停止朗读",
		"view": "显示", "reload": "重新载入", "forceReload": "强制重新载入", "devTools": "开发者工具",
		"actualSize": "实际大小", "zoomIn": "放大", "zoomOut": "缩小", "fullScreen": "切换全屏幕",
		"window": "窗口", "minimise": "最小化", "zoom": "缩放", "front": "前置全部窗口",
	},
	LanguageEn: {
		"open": "Open Mechbox", "quit": "Quit Mechbox",
		"about": "About Mechbox", "services": "Services", "hide": "Hide Mechbox", "hideOthers": "Hide Others", "showAll": "Show All",
		"file": "File", "close": "Close Window",
		"edit": "Edit", "undo": "Undo", "redo": "Redo", "cut": "Cut", "copy": "Copy", "paste": "Paste",
		"pasteAndMatchStyle": "Paste and Match Style", "delete": "Delete", "selectAll": "Select All",
		"speech": "Speech", "startSpeaking": "Start Speaking", "stopSpeaking": "Stop Speaking",
		"view": "View", "reload": "Reload", "forceReload": "Force Reload", "devTools": "Developer Tools",
		"actualSize": "Actual Size", "zoomIn": "Zoom In", "zoomOut": "Zoom Out", "fullScreen": "Toggle Full Screen",
		"window": "Window", "minimise": "Minimize", "zoom": "Zoom", "front": "Bring All to Front",
	},
}

// nativeMenus 记下 Go 这边画的菜单项，界面语言变了时逐项换文字。
// 换文字要等主线程，所以只在 mu 下记录和取出菜单项，换文字时不持 mu;几次换语言由 applyMu 排队。
// 顶部菜单栏在 attach 时建好，那时还没读设置、文字是空的，ServiceStartup 定下语言后换上(都早于原生菜单画出来)。
type nativeMenus struct {
	applyMu  sync.Mutex
	mu       sync.Mutex
	language Language
	labeled  []labeledItem
	appMenu  *application.Menu
}

type labeledItem struct {
	item *application.MenuItem
	key  string
}

func (m *nativeMenus) label(item *application.MenuItem, key string) *application.MenuItem {
	if item == nil {
		return nil
	}
	m.mu.Lock()
	m.labeled = append(m.labeled, labeledItem{item, key})
	language := m.language
	m.mu.Unlock()
	// 写文字期间换了语言时，setLanguage 可能已经写过新文字、又被这里的旧文字盖掉，所以写完再对一次。
	for {
		item.SetLabel(nativeLabels[language][key])
		m.mu.Lock()
		current := m.language
		m.mu.Unlock()
		if current == language {
			return item
		}
		language = current
	}
}

func (m *nativeMenus) role(role application.Role, key string) *application.MenuItem {
	return m.label(application.NewRole(role), key)
}

func (m *nativeMenus) submenu(key string, items ...*application.MenuItem) *application.MenuItem {
	var kept []*application.MenuItem
	for _, item := range items {
		if item != nil {
			kept = append(kept, item)
		}
	}
	return m.label(application.NewSubmenu("", application.NewMenuFromItems(kept[0], kept[1:]...)), key)
}

// macMenu 是 macOS 顶部菜单栏：Wails 默认菜单里的各项，文字按界面语言。
// 默认菜单里"帮助"只有一项把窗口导航到 wails.io 的"Learn More",这里不要。
func (m *nativeMenus) macMenu() *application.Menu {
	sep := application.NewMenuItemSeparator
	app := application.NewSubmenu("Mechbox", application.NewMenuFromItems(
		m.role(application.About, "about"), sep(),
		m.role(application.ServicesMenu, "services"), sep(),
		m.role(application.Hide, "hide"), m.role(application.HideOthers, "hideOthers"), m.role(application.ShowAll, "showAll"), sep(),
		m.role(application.Quit, "quit"),
	))
	window := m.submenu("window",
		m.role(application.Minimise, "minimise"), m.role(application.Zoom, "zoom"), sep(),
		m.role(application.BringAllToFront, "front"),
	).SetRole(application.WindowMenu)
	return application.NewMenuFromItems(app,
		m.submenu("file", m.role(application.CloseWindow, "close")),
		m.submenu("edit",
			m.role(application.Undo, "undo"), m.role(application.Redo, "redo"), sep(),
			m.role(application.Cut, "cut"), m.role(application.Copy, "copy"), m.role(application.Paste, "paste"),
			m.role(application.PasteAndMatchStyle, "pasteAndMatchStyle"), m.role(application.Delete, "delete"),
			m.role(application.SelectAll, "selectAll"), sep(),
			m.submenu("speech", m.role(application.StartSpeaking, "startSpeaking"), m.role(application.StopSpeaking, "stopSpeaking")),
		),
		m.submenu("view",
			m.role(application.Reload, "reload"), m.role(application.ForceReload, "forceReload"), m.role(application.OpenDevTools, "devTools"), sep(),
			m.role(application.ResetZoom, "actualSize"), m.role(application.ZoomIn, "zoomIn"), m.role(application.ZoomOut, "zoomOut"), sep(),
			m.role(application.ToggleFullscreen, "fullScreen"),
		),
		window,
	)
}

// setLanguage 换掉已经画出来的菜单项文字;顶部菜单栏整个重建一遍，菜单栏上的标题才会跟着变。
func (m *nativeMenus) setLanguage(language Language) {
	m.applyMu.Lock()
	defer m.applyMu.Unlock()
	m.mu.Lock()
	if language == m.language {
		m.mu.Unlock()
		return
	}
	m.language = language
	labeled, appMenu := append([]labeledItem(nil), m.labeled...), m.appMenu
	m.mu.Unlock()
	for _, l := range labeled {
		l.item.SetLabel(nativeLabels[language][l.key])
	}
	if appMenu != nil {
		appMenu.Update()
	}
}

func (m *nativeMenus) attach(app *application.App) {
	if runtime.GOOS == "darwin" {
		menu := m.macMenu()
		m.mu.Lock()
		m.appMenu = menu
		m.mu.Unlock()
		app.Menu.SetApplicationMenu(menu)
	}
}
