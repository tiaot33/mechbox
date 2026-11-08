//go:build !server

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework AppKit
#import <AppKit/AppKit.h>

static void setAppAppearance(int mode) {
	dispatch_async(dispatch_get_main_queue(), ^{
		NSAppearance *appearance = nil;
		if (mode == 1) {
			appearance = [NSAppearance appearanceNamed:NSAppearanceNameAqua];
		} else if (mode == 2) {
			appearance = [NSAppearance appearanceNamed:NSAppearanceNameDarkAqua];
		}
		[[NSApplication sharedApplication] setAppearance:appearance];
	});
}
*/
import "C"

import "github.com/wailsapp/wails/v3/pkg/application"

// setNativeAppearance 设整个 app 的外观，窗口背后的毛玻璃、WKWebView 的 prefers-color-scheme 与菜单都跟着变;
// 跟随系统时设为 nil,交给系统。在主线程上异步执行，启动时 app 还没开始运行也可以调用。
func setNativeAppearance(_ *application.WebviewWindow, appearance Appearance) {
	mode := 0
	switch appearance {
	case AppearanceLight:
		mode = 1
	case AppearanceDark:
		mode = 2
	}
	C.setAppAppearance(C.int(mode))
}
