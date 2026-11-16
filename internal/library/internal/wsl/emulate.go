package wsl

import "sync/atomic"

var emulated atomic.Pointer[string]

func Emulate(shell string) (restore func()) {
	previous := emulated.Swap(&shell)
	return func() { emulated.Store(previous) }
}

func emulatedShell() string {
	if shell := emulated.Load(); shell != nil {
		return *shell
	}
	return ""
}
