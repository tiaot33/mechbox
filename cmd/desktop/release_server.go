//go:build server

package main

import (
	"context"
	"errors"

	"github.com/wailsapp/wails/v3/pkg/updater"
)

func releaseTarget() installTarget { return installTarget{manual: ManualPackage} }

func installStaged(context.Context, *updater.Updater, string, func()) error {
	return errors.New("server mode does not install new versions")
}
