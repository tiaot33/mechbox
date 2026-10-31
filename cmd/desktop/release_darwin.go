//go:build !server

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/wailsapp/wails/v3/pkg/updater"
)

func releaseTarget() installTarget {
	bundle := appBundle()
	if bundle == "" || !writableDir(filepath.Dir(bundle)) || !sameVolume(filepath.Dir(bundle), os.TempDir()) {
		return installTarget{manual: ManualLocation}
	}
	return installTarget{asset: "-macos-universal.zip"}
}

func sameVolume(a, b string) bool {
	var sa, sb syscall.Stat_t
	if syscall.Stat(a, &sa) != nil || syscall.Stat(b, &sb) != nil {
		return false
	}
	return sa.Dev == sb.Dev
}

func installLog(string) string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("wails-update-%d.log", os.Getpid()))
}

func installStaged(ctx context.Context, u *updater.Updater, _, _ string, _ func()) error {
	return u.Restart(ctx)
}
