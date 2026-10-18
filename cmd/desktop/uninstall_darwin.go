//go:build !server

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation
#import <Foundation/Foundation.h>
#include <stdlib.h>
#include <string.h>

static char *trashItem(const char *path) {
	@autoreleasepool {
		NSURL *url = [NSURL fileURLWithPath:[NSString stringWithUTF8String:path]];
		NSError *error = nil;
		if ([[NSFileManager defaultManager] trashItemAtURL:url resultingItemURL:nil error:&error]) {
			return NULL;
		}
		return strdup(error.localizedDescription.UTF8String ?: "unknown error");
	}
}
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"
)

func removeFromSystem() error {
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
	if bundle := appBundle(); bundle != "" {
		if err := trash(bundle); err != nil {
			errs = append(errs, err, reveal(bundle))
		}
	}
	return errors.Join(errs...)
}

func appBundle() string {
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

func discard(path string) error {
	return trash(path)
}

func trash(path string) error {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	message := C.trashItem(cpath)
	if message == nil {
		return nil
	}
	defer C.free(unsafe.Pointer(message))
	return fmt.Errorf("move %s to trash: %s", path, C.GoString(message))
}
