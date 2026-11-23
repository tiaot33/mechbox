//go:build !server

package system

import "os/exec"

// ShowStartupError 用 zenity 或 kdialog 告诉用户启动失败：从应用菜单打开时 stderr 没人看得到。两个都没装时只留 stderr 那一行。
func ShowStartupError(title, message string) {
	if path, lookErr := exec.LookPath("zenity"); lookErr == nil {
		exec.Command(path, "--error", "--no-markup", "--title", title, "--text", message).Run()
		return
	}
	if path, lookErr := exec.LookPath("kdialog"); lookErr == nil {
		exec.Command(path, "--title", title, "--error", message).Run()
	}
}
