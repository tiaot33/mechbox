package proxy

import "strings"

func parseWindowsProxyServer(server string) SystemConfig {
	server = strings.TrimSpace(server)
	if server == "" {
		return SystemConfig{}
	}
	var cfg SystemConfig
	if !strings.Contains(server, "=") {
		if strings.HasPrefix(strings.ToLower(server), "socks5://") || strings.HasPrefix(strings.ToLower(server), "socks5h://") {
			cfg.SOCKS = buildHostPortURL("socks5", server, 0)
			return cfg
		}
		if rest, ok := strings.CutPrefix(strings.ToLower(server), "socks://"); ok {
			cfg.SOCKS = buildHostPortURL("socks5", server[len(server)-len(rest):], 0)
			return cfg
		}
		addr := buildHostPortURL("http", server, 0)
		cfg.HTTP = addr
		cfg.HTTPS = addr
		return cfg
	}
	for part := range strings.SplitSeq(server, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		scheme, target, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		scheme = strings.ToLower(strings.TrimSpace(scheme))
		target = strings.TrimSpace(target)
		switch scheme {
		case "http":
			cfg.HTTP = buildHostPortURL("http", target, 0)
		case "https":
			cfg.HTTPS = buildHostPortURL("http", target, 0)
		case "socks", "socks5", "socks5h":
			if rest, ok := strings.CutPrefix(strings.ToLower(target), "socks://"); ok {
				target = target[len(target)-len(rest):]
			}
			cfg.SOCKS = buildHostPortURL("socks5", target, 0)
		}
	}
	return cfg
}

func parseWindowsProxyOverride(override string) []string {
	var bypass []string
	for item := range strings.SplitSeq(override, ";") {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			bypass = append(bypass, trimmed)
		}
	}
	return bypass
}
