package main

/*
#include <stdlib.h>
*/
import "C"

import "unsafe"

// canonicalPath 用 realpath(3) 解析符号链接，并把每一段换成磁盘上的真实大小写:
// macOS 默认的卷不区分大小写，同一个文件换一种大小写写法仍得到同一个结果。路径必须已存在。
func canonicalPath(path string) (string, error) {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	resolved, err := C.realpath(cpath, nil)
	if resolved == nil {
		return "", err
	}
	defer C.free(unsafe.Pointer(resolved))
	return C.GoString(resolved), nil
}
