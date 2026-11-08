package main

/*
#include <stdlib.h>
#include <unistd.h>
*/
import "C"

import "unsafe"

// caseSensitive 用 pathconf(3) 的 _PC_CASE_SENSITIVE 判断 path 所在的卷是否区分大小写;macOS 默认的卷不区分。判断不了时当作区分。
func caseSensitive(path string) bool {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	return C.pathconf(cpath, C._PC_CASE_SENSITIVE) != 0
}
