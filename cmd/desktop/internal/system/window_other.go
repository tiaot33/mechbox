//go:build !windows || server

package system

import "github.com/wailsapp/wails/v3/pkg/application"

// SetWindowOptions 在 Windows 以外的平台不改窗口选项:macOS 的外观在 main.go 的 Mac 选项里。
func SetWindowOptions(*application.WebviewWindowOptions) {}
