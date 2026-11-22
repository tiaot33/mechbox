package proxy

import "sync"

var (
	emulateMu sync.RWMutex
	emulated  func() SystemConfig
)

func Emulate(fn func() SystemConfig) func() {
	emulateMu.Lock()
	previous := emulated
	emulated = fn
	emulateMu.Unlock()
	return func() {
		emulateMu.Lock()
		emulated = previous
		emulateMu.Unlock()
	}
}

func emulator() func() SystemConfig {
	emulateMu.RLock()
	defer emulateMu.RUnlock()
	return emulated
}

func SystemFresh() SystemConfig {
	if fn := emulator(); fn != nil {
		return fn()
	}
	return readSystemProxy()
}
