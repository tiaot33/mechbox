//go:build !windows

package agentdir

func nativeModes() bool { return !withoutModes.Load() }
