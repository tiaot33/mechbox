//go:build !server

package trash

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
	"fmt"
	"unsafe"
)

func Discard(path string) error {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	message := C.trashItem(cpath)
	if message == nil {
		return nil
	}
	defer C.free(unsafe.Pointer(message))
	return fmt.Errorf("move %s to trash: %s", path, C.GoString(message))
}
