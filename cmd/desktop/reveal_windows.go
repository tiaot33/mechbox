//go:build !server

package main

import (
	"errors"
	"os/exec"
	"path/filepath"
	"syscall"
)

func reveal(path string) error {
	line, err := explorerSelect(filepath.Clean(path))
	if err != nil {
		return err
	}
	cmd := exec.Command("explorer.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: line}
	var exit *exec.ExitError
	if err := cmd.Run(); err != nil && !errors.As(err, &exit) {
		return err
	}
	return nil
}
