//go:build server

package system

// ShowStartupError 在 server 模式下什么也不做：启动失败的原因在 stderr 里。
func ShowStartupError(string, string) {}
