//go:build !server

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"

	"github.com/wailsapp/wails/v3/pkg/updater"
)

func releaseTarget() installTarget {
	image := os.Getenv("APPIMAGE")
	switch {
	case image == "":
		return installTarget{manual: ManualPackage}
	case !writableDir(filepath.Dir(image)):
		return installTarget{manual: ManualLocation}
	}
	return installTarget{asset: "-linux-" + runtime.GOARCH + ".AppImage"}
}

func installStaged(_ context.Context, _ *updater.Updater, staged string, quit func()) error {
	image := os.Getenv("APPIMAGE")
	if err := replaceFile(staged, image); err != nil {
		return err
	}
	_ = os.RemoveAll(filepath.Dir(staged))
	cmd := exec.Command("/bin/sh", "-c", `while kill -0 "$1" 2>/dev/null; do sleep 0.2; done; exec "$2"`, "sh", strconv.Itoa(os.Getpid()), image)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()
	quit()
	return nil
}
