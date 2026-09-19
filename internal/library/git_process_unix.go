//go:build unix

package library

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// cancelGitProcess 同时终止 Git 与其 HTTP/SSH 子进程,避免取消后仍占用暂存目录或连接。
func cancelGitProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}
