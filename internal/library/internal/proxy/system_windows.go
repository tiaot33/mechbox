//go:build windows

package proxy

import "golang.org/x/sys/windows/registry"

const internetSettingsKey = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

func readSystemProxy() SystemConfig {
	key, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsKey, registry.QUERY_VALUE)
	if err != nil {
		return SystemConfig{}
	}
	defer key.Close()
	enabled, _, err := key.GetIntegerValue("ProxyEnable")
	if err != nil || enabled == 0 {
		return SystemConfig{}
	}
	server, _, err := key.GetStringValue("ProxyServer")
	if err != nil || server == "" {
		return SystemConfig{}
	}
	cfg := parseWindowsProxyServer(server)
	if override, _, err := key.GetStringValue("ProxyOverride"); err == nil && override != "" {
		cfg.Bypass = parseWindowsProxyOverride(override)
	}
	return cfg
}
