package library

import (
	"os/exec"
	"strconv"
)

func cancelGitProcess(command *exec.Cmd) {
	command.Cancel = func() error {
		return exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(command.Process.Pid)).Run()
	}
}
