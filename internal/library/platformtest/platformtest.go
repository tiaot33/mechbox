package platformtest

import (
	"testing"

	"github.com/tiaot33/mechbox/internal/library/internal/agentdir"
	"github.com/tiaot33/mechbox/internal/library/internal/wsl"
)

func EmulateWSL(t testing.TB, shell string) {
	t.Helper()
	t.Cleanup(wsl.Emulate(shell))
}

func WithoutFileModes(t testing.TB) {
	t.Helper()
	t.Cleanup(agentdir.WithoutModes())
}
