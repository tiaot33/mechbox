//go:build !darwin

package main

// 角标只在 macOS 的 Dock 上显示。
func setBadge(int) {}
