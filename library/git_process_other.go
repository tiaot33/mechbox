//go:build !unix && !windows

package library

import "os/exec"

// cancelGitProcess 在其他平台沿用 exec.CommandContext 的进程取消行为。
func cancelGitProcess(command *exec.Cmd) {}
