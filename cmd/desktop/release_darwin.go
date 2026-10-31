//go:build !server

package main

import (
	"context"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/updater"
)

func releaseTarget() installTarget {
	bundle := appBundle()
	if bundle == "" || !writableDir(filepath.Dir(bundle)) {
		return installTarget{manual: ManualLocation}
	}
	return installTarget{asset: "-macos-universal.zip"}
}

func installStaged(ctx context.Context, u *updater.Updater, _ string, _ func()) error {
	return u.Restart(ctx)
}
