//go:build !unix && !windows

package git

import "os/exec"

func KillOnCancel(command *exec.Cmd) {}
