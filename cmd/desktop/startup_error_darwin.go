//go:build !server

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

static void showCriticalAlert(const char *title, const char *message) {
	@autoreleasepool {
		[NSApplication sharedApplication];
		[NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];
		[NSApp activateIgnoringOtherApps:YES];
		NSAlert *alert = [[[NSAlert alloc] init] autorelease];
		alert.alertStyle = NSAlertStyleCritical;
		alert.messageText = [NSString stringWithUTF8String:title];
		alert.informativeText = [NSString stringWithUTF8String:message];
		[alert runModal];
	}
}
*/
import "C"

import "unsafe"

// showStartupError 用系统的提示框告诉用户启动失败：从 Finder / Dock 打开时 stderr 没人看得到。
// Wails 在 init 里把主 goroutine 锁在主线程上，main 里调用时就在主线程，AppKit 要求如此。
func showStartupError(err error) {
	title, message := startupErrorText(err)
	cTitle := C.CString(title)
	defer C.free(unsafe.Pointer(cTitle))
	cMessage := C.CString(message)
	defer C.free(unsafe.Pointer(cMessage))
	C.showCriticalAlert(cTitle, cMessage)
}
