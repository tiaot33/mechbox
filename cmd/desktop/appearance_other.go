//go:build (!darwin && !windows) || server

package main

import "github.com/wailsapp/wails/v3/pkg/application"

// setNativeAppearance 在 macOS、Windows 以外的平台与 server 模式下不做什么：窗口背后没有系统材质，界面的配色由前端切换。
func setNativeAppearance(*application.WebviewWindow, Appearance) {}
