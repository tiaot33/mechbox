//go:build !server

package main

import (
	"errors"
	"os/exec"
	"path/filepath"
	"syscall"
)

func reveal(path string) error {
	cmd := exec.Command("explorer.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `explorer.exe /select,"` + filepath.Clean(path) + `"`}
	var exit *exec.ExitError
	if err := cmd.Run(); err != nil && !errors.As(err, &exit) {
		return err
	}
	return nil
}
