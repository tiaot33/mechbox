package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

static void setDockBadge(const char *label) {
	NSString *text = label ? [[NSString alloc] initWithUTF8String:label] : nil;
	dispatch_async(dispatch_get_main_queue(), ^{
		[[NSApp dockTile] setBadgeLabel:text];
		[text release];
	});
}
*/
import "C"

import (
	"strconv"
	"unsafe"
)

func setBadge(n int) {
	if n == 0 {
		C.setDockBadge(nil)
		return
	}
	label := C.CString(strconv.Itoa(n))
	defer C.free(unsafe.Pointer(label))
	C.setDockBadge(label)
}
