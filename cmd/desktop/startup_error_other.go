//go:build (!darwin && !windows && !linux) || server

package main

// showStartupError 在 server 模式下什么也不做：启动失败的原因在 stderr 里。
func showStartupError(error) {}
