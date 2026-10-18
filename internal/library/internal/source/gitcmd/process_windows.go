package gitcmd

import (
	"os/exec"
	"strconv"
)

func KillOnCancel(command *exec.Cmd) {
	command.Cancel = func() error {
		return exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(command.Process.Pid)).Run()
	}
}
