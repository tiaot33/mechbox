//go:build desktop

package main

// 不经 Wails CLI 直接 go build 时,链接器缺 UTType 符号;CLI 平时会自动补这个框架。

// #cgo LDFLAGS: -framework UniformTypeIdentifiers
import "C"
