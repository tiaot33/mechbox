//go:build !darwin && !windows

package proxy

import "os"

func readSystemProxy() SystemConfig {
	return parseEnvSystemProxy(os.Getenv)
}
