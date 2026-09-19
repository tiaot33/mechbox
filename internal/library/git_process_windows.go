package library

import (
	"os/exec"
	"strconv"
)

// cancelGitProcess 使用系统命令终止 Git 子进程树。
func cancelGitProcess(command *exec.Cmd) {
	command.Cancel = func() error {
		return exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(command.Process.Pid)).Run()
	}
}
