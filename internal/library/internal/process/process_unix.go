//go:build unix

package process

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func Hide(command *exec.Cmd) {}

func KillTreeOnCancel(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}
