//go:build (!darwin && !windows) || server

package system

import "os"

func PreferredLanguage() string {
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(name); v != "" {
			return v
		}
	}
	return ""
}
