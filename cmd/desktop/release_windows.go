//go:build !server

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/wailsapp/wails/v3/pkg/updater"
	"golang.org/x/sys/windows"
)

func releaseTarget() installTarget {
	return installTarget{asset: "-windows-" + runtime.GOARCH + "-setup.exe"}
}

func installLog(stateDir string) string {
	return filepath.Join(stateDir, "update.log")
}

func installStaged(_ context.Context, _ *updater.Updater, staged, log string, quit func()) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	_ = os.Remove(log)
	script := installerScript(os.Getpid(), staged, filepath.Dir(staged), filepath.Dir(exe), exe, log)
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-WindowStyle", "Hidden", "-EncodedCommand", encodePowerShell(script))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP}
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()
	quit()
	return nil
}
