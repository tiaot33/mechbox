//go:build !server

package main

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

const createNoWindow = 0x08000000

func trashInDistro(distro, linux string) error {
	command := exec.Command("wsl.exe", "--distribution", distro, "--cd", "~", "--exec", "/bin/sh", "-c", distroTrashScript, "sh", linux, trashInfo(linux, time.Now()))
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("move %s to trash in %s: %w: %s", linux, distro, err, strings.TrimSpace(string(output)))
	}
	return nil
}
