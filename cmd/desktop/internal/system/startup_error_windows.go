//go:build !server

package system

import "golang.org/x/sys/windows"

// ShowStartupError 用系统的消息框告诉用户启动失败：从开始菜单或桌面打开时 stderr 没人看得到。
func ShowStartupError(title, message string) {
	titlePtr, titleErr := windows.UTF16PtrFromString(title)
	messagePtr, messageErr := windows.UTF16PtrFromString(message)
	if titleErr != nil || messageErr != nil {
		return
	}
	windows.MessageBox(0, messagePtr, titlePtr, windows.MB_OK|windows.MB_ICONERROR|windows.MB_SETFOREGROUND)
}
