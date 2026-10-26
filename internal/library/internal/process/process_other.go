//go:build !unix && !windows

package process

import "os/exec"

func Hide(command *exec.Cmd) {}

func KillTreeOnCancel(command *exec.Cmd) {}
