//go:build !server

package main

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/sys/windows"
)

// micaBuild 是支持系统背景材质(Mica)的第一个 Windows 版本:Windows 11 22H2。
const micaBuild = 22621

// setPlatformWindowOptions 设置 Windows 上的窗口外观。界面只有浅色一套，标题栏与背景材质固定为浅色，不跟随系统的深色模式。
// 支持 Mica 时窗口背后用 Mica,前端的半透明底色铺在上面，对应 macOS 的毛玻璃;
// 更早的版本没有 Mica,窗口用不透明的浅色底，前端的半透明底色铺在上面仍是浅色(默认的黑底会透出来，整窗发灰)。
func setPlatformWindowOptions(options *application.WebviewWindowOptions) {
	options.Windows.Theme = application.Light
	if windows.RtlGetVersion().BuildNumber >= micaBuild {
		options.BackgroundType = application.BackgroundTypeTranslucent
		options.Windows.BackdropType = application.Mica
		return
	}
	options.BackgroundColour = application.NewRGB(236, 239, 245)
}
