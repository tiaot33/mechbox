//go:build !unix && !windows

package library

import "os/exec"

func cancelGitProcess(command *exec.Cmd) {}
