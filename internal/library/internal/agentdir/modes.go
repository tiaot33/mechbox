package agentdir

import "sync/atomic"

var withoutModes atomic.Bool

func WithoutModes() (restore func()) {
	previous := withoutModes.Swap(true)
	return func() { withoutModes.Store(previous) }
}
