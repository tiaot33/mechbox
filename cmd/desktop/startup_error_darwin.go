//go:build !server

package main

import "os/exec"

// showStartupError 用系统的提示框告诉用户启动失败：从 Finder / Dock 打开时 stderr 没人看得到。
// 提示框由另起的 osascript 进程弹出：本进程里 Wails 已经接管了 NSApp 的通知，服务没启动成功时没人读它的事件，
// 在本进程里跑模态框会让主线程卡在发事件上，点不动也退不出。
func showStartupError(err error, libraryPath string) {
	title, message := startupErrorText(err, libraryPath)
	exec.Command("/usr/bin/osascript",
		"-e", "on run argv",
		"-e", "activate",
		"-e", "display alert (item 1 of argv) message (item 2 of argv) as critical",
		"-e", "end run",
		title, message,
	).Run()
}
