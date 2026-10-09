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

// setBadge 设 Dock 图标上的角标，0 时去掉。异步交给主线程，调用方不等它
// (应用退出时主线程正等着后台检查结束，同步等主线程会互相卡住)。
func setBadge(n int) {
	if n == 0 {
		C.setDockBadge(nil)
		return
	}
	label := C.CString(strconv.Itoa(n))
	defer C.free(unsafe.Pointer(label))
	C.setDockBadge(label)
}
