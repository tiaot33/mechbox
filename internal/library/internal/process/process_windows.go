package process

import (
	"os/exec"
	"strconv"
	"syscall"
)

const createNoWindow = 0x08000000

func Hide(command *exec.Cmd) {
	if command.SysProcAttr == nil {
		command.SysProcAttr = &syscall.SysProcAttr{}
	}
	command.SysProcAttr.HideWindow = true
	command.SysProcAttr.CreationFlags |= createNoWindow
}

func KillTreeOnCancel(command *exec.Cmd) {
	command.Cancel = func() error {
		kill := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(command.Process.Pid))
		Hide(kill)
		return kill.Run()
	}
}
