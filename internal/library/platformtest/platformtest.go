package platformtest

import (
	"testing"

	"github.com/tiaot33/mechbox/internal/library/internal/agentdir"
	"github.com/tiaot33/mechbox/internal/library/internal/proxy"
	"github.com/tiaot33/mechbox/internal/library/internal/wsl"
)

func EmulateWSL(t testing.TB, shell string) {
	t.Helper()
	t.Setenv("PLATFORMTEST", "1")
	t.Cleanup(wsl.Emulate(shell))
}

func WithoutFileModes(t testing.TB) {
	t.Helper()
	t.Setenv("PLATFORMTEST", "1")
	t.Cleanup(agentdir.WithoutModes())
}

func EmulateSystemProxy(t testing.TB, httpProxy, httpsProxy, socksProxy string, bypass []string) {
	t.Helper()
	t.Setenv("PLATFORMTEST", "1")
	cfg := proxy.SystemConfig{HTTP: httpProxy, HTTPS: httpsProxy, SOCKS: socksProxy, Bypass: append([]string(nil), bypass...)}
	t.Cleanup(proxy.Emulate(func() proxy.SystemConfig { return cfg }))
}

func DisableSystemProxy() func() {
	return proxy.Emulate(func() proxy.SystemConfig { return proxy.SystemConfig{} })
}
