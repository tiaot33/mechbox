//go:build !server

package system

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework AppKit
#import <AppKit/AppKit.h>

static int reduceTransparency(void) {
	return [[NSWorkspace sharedWorkspace] accessibilityDisplayShouldReduceTransparency] ? 1 : 0;
}
*/
import "C"

func ReducesTransparency() bool {
	return C.reduceTransparency() != 0
}
