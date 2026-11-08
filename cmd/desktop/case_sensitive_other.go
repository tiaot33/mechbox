//go:build !darwin

package main

import "runtime"

// caseSensitive 判断 path 所在的卷是否区分大小写:Windows 的卷不区分,Linux 的区分。
func caseSensitive(string) bool {
	return runtime.GOOS != "windows"
}
