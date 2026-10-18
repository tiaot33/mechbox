//go:build !unix && !windows

package gitcmd

import "os/exec"

func KillOnCancel(command *exec.Cmd) {}
