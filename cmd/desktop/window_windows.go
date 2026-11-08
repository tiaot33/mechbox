//go:build !server

package main

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/w32"
)

// setPlatformWindowOptions 设置 Windows 上的窗口外观。窗口不要系统标题栏，和 macOS 一样由前端的顶栏当标题栏:
// 拖动区、最小化 / 最大化 / 关闭按钮与双击最大化都在前端(见 frontend 的 titlebar.tsx、lib/window.ts);系统的阴影、圆角照旧。
// 调整大小只能从窗口内侧边缘的几像素开始(Wails 前端运行时处理,最大化时由前端关掉),窗口外侧没有系统的调整区。
// 窗口的深浅色不交给 Wails 跟随系统(那样选了浅色或深色时，系统一切换又被改回去),由 setNativeAppearance 按设置给出。
// 支持系统背景材质(Windows 11 22H2 起，与 Wails 设置材质时的判断相同)时窗口背后用 Mica,前端的半透明底色铺在上面，对应 macOS 的毛玻璃;
// 更早的版本窗口用不透明底，与 index.css 里 window-backdrop 最底下那层同色，前端的半透明底色铺在上面(默认的黑底会透出来，整窗发灰)。
func setPlatformWindowOptions(options *application.WebviewWindowOptions) {
	options.Frameless = true
	options.Windows.Theme = application.Light
	if w32.SupportsBackdropTypes() {
		options.BackgroundType = application.BackgroundTypeTranslucent
		options.Windows.BackdropType = application.Mica
		return
	}
	options.BackgroundColour = windowColour(false)
}

// windowColour 是没有 Mica 时窗口的不透明底色，浅色、深色各一份，与 index.css 里 window-backdrop 最底下那层同色。
func windowColour(dark bool) application.RGBA {
	if dark {
		return application.NewRGB(26, 26, 30)
	}
	return application.NewRGB(236, 239, 245)
}

// setNativeAppearance 按外观设窗口的深浅色(Mica 与标题栏跟着变)和没有 Mica 时的不透明底色;跟随系统时看系统的「应用模式」。
// 窗口还没建出来时(启动时)只记下底色，深浅色等窗口显示出来再设(见 AppService.attach)。
func setNativeAppearance(window *application.WebviewWindow, appearance Appearance) {
	if window == nil {
		return
	}
	dark := appearance == AppearanceDark || appearance == AppearanceSystem && w32.IsCurrentlyDarkMode()
	if !w32.SupportsBackdropTypes() {
		window.SetBackgroundColour(windowColour(dark))
	}
	hwnd := uintptr(window.NativeWindow())
	if hwnd == 0 {
		return
	}
	application.InvokeAsync(func() {
		if dark && w32.AllowDarkModeForWindow != nil {
			w32.AllowDarkModeForWindow(w32.HWND(hwnd), true)
		}
		w32.SetTheme(hwnd, dark)
	})
}
