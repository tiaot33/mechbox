package main

import (
	"runtime"

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
type nativeMenus struct {
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
	m.labeled = append(m.labeled, labeledItem{item, key})
	return item.SetLabel(nativeLabels[m.language][key])
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
	if language == m.language {
		return
	}
	m.language = language
	for _, l := range m.labeled {
		l.item.SetLabel(nativeLabels[language][l.key])
	}
	if m.appMenu != nil {
		m.appMenu.Update()
	}
}

func (m *nativeMenus) attach(app *application.App) {
	if runtime.GOOS == "darwin" {
		m.appMenu = m.macMenu()
		app.Menu.SetApplicationMenu(m.appMenu)
	}
}
