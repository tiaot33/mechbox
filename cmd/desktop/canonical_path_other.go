//go:build !darwin

package main

import "path/filepath"

// canonicalPath 解析符号链接。Windows 上 filepath.EvalSymlinks 已把每一段换成磁盘上的真实大小写;Linux 的卷区分大小写，不用换。路径必须已存在。
func canonicalPath(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}
