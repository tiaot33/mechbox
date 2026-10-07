//go:build !unix && !windows

package github

import "os/exec"

func cancelGitProcess(command *exec.Cmd) {}
