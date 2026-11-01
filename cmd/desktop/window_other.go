//go:build !windows || server

package main

import "github.com/wailsapp/wails/v3/pkg/application"

// setPlatformWindowOptions 在 Windows 以外的平台不改窗口选项:macOS 的外观在 main.go 的 Mac 选项里。
func setPlatformWindowOptions(*application.WebviewWindowOptions) {}
