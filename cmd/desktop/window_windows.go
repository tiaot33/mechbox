//go:build !server

package main

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/w32"
)

// setPlatformWindowOptions 设置 Windows 上的窗口外观。窗口不要系统标题栏，和 macOS 一样由前端的顶栏当标题栏:
// 拖动区、最小化 / 最大化 / 关闭按钮与双击最大化都在前端(见 frontend 的 titlebar.tsx、lib/window.ts);系统的阴影、圆角照旧。
// 调整大小只能从窗口内侧边缘的几像素开始(Wails 前端运行时处理,最大化时由前端关掉),窗口外侧没有系统的调整区。
// 界面只有浅色一套，背景材质固定为浅色，不跟随系统的深色模式。
// 支持系统背景材质(Windows 11 22H2 起，与 Wails 设置材质时的判断相同)时窗口背后用 Mica,前端的半透明底色铺在上面，对应 macOS 的毛玻璃;
// 更早的版本窗口用不透明的浅色底，与 index.css 里 window-backdrop 最底下那层同色，前端的半透明底色铺在上面仍是浅色
// (默认的黑底会透出来，整窗发灰)。
func setPlatformWindowOptions(options *application.WebviewWindowOptions) {
	options.Frameless = true
	options.Windows.Theme = application.Light
	if w32.SupportsBackdropTypes() {
		options.BackgroundType = application.BackgroundTypeTranslucent
		options.Windows.BackdropType = application.Mica
		return
	}
	options.BackgroundColour = application.NewRGB(236, 239, 245)
}
