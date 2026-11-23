//go:build !server

package system

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation
#include <stdlib.h>
#include <string.h>
#import <Foundation/Foundation.h>

static char *preferredLanguage(void) {
	@autoreleasepool {
		NSString *first = [[NSLocale preferredLanguages] firstObject];
		return first ? strdup([first UTF8String]) : NULL;
	}
}
*/
import "C"

import "unsafe"

// PreferredLanguage 是系统设置里排第一的首选语言。不用 WebView 的 navigator.language:它跟随 app bundle 声明的语言。
func PreferredLanguage() string {
	tag := C.preferredLanguage()
	if tag == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(tag))
	return C.GoString(tag)
}
