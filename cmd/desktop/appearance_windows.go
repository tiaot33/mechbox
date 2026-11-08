//go:build !server

package main

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/w32"
)

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
