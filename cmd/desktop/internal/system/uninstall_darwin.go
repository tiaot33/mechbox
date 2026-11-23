//go:build !server

package system

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/tiaot33/mechbox/cmd/desktop/internal/trash"
)

func RemoveFromSystem(bundleID string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	var errs []error
	for _, path := range []string{
		filepath.Join(home, "Library", "WebKit", bundleID),
		filepath.Join(home, "Library", "Caches", bundleID),
		filepath.Join(home, "Library", "HTTPStorages", bundleID),
		filepath.Join(home, "Library", "Saved Application State", bundleID+".savedState"),
		filepath.Join(home, "Library", "Preferences", bundleID+".plist"),
	} {
		errs = append(errs, os.RemoveAll(path))
	}
	if bundle := AppBundle(); bundle != "" {
		if err := trash.Discard(bundle); err != nil {
			errs = append(errs, err, Reveal(bundle))
		}
	}
	return errors.Join(errs...)
}

func AppBundle() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	bundle, ok := strings.CutSuffix(filepath.Dir(exe), "/Contents/MacOS")
	if !ok || filepath.Ext(bundle) != ".app" {
		return ""
	}
	return bundle
}
