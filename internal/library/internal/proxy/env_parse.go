package proxy

import "strings"

func parseEnvSystemProxy(getenv func(string) string) SystemConfig {
	var cfg SystemConfig
	cfg.HTTP = envProxy(getenv, "HTTP_PROXY", "http_proxy")
	cfg.HTTPS = envProxy(getenv, "HTTPS_PROXY", "https_proxy")
	if all := envProxy(getenv, "ALL_PROXY", "all_proxy"); all != "" {
		lower := strings.ToLower(all)
		if strings.HasPrefix(lower, "socks5://") || strings.HasPrefix(lower, "socks5h://") {
			cfg.SOCKS = all
		} else {
			if cfg.HTTP == "" {
				cfg.HTTP = all
			}
			if cfg.HTTPS == "" {
				cfg.HTTPS = all
			}
		}
	}
	noProxy := getenv("NO_PROXY")
	if noProxy == "" {
		noProxy = getenv("no_proxy")
	}
	if noProxy != "" {
		for item := range strings.SplitSeq(noProxy, ",") {
			if trimmed := strings.TrimSpace(item); trimmed != "" {
				cfg.Bypass = append(cfg.Bypass, trimmed)
			}
		}
	}
	return cfg
}

func envProxy(getenv func(string) string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(getenv(key)); value != "" {
			return buildHostPortURL("http", value, 0)
		}
	}
	return ""
}
