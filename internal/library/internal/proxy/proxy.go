package proxy

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Mode string

const (
	ModeSystem Mode = "system"
	ModeManual Mode = "manual"
	ModeDirect Mode = "direct"
)

var (
	ErrInvalidMode = errors.New("unknown proxy mode")
	ErrInvalidURL  = errors.New("invalid proxy url")
)

type Settings struct {
	Mode Mode
	URL  string
}

type SystemConfig struct {
	HTTP   string
	HTTPS  string
	SOCKS  string
	Bypass []string
}

const systemCacheTTL = time.Second

func ParseMode(raw string) (Mode, bool) {
	switch Mode(strings.TrimSpace(raw)) {
	case "", ModeSystem:
		return ModeSystem, true
	case ModeManual:
		return ModeManual, true
	case ModeDirect:
		return ModeDirect, true
	default:
		return "", false
	}
}

func Parse(rawMode, rawURL string) (Settings, error) {
	mode, ok := ParseMode(rawMode)
	if !ok {
		return Settings{}, ErrInvalidMode
	}
	trimmed := strings.TrimSpace(rawURL)
	if mode == ModeManual || trimmed != "" {
		normalized, err := ValidateURL(trimmed)
		if err != nil {
			return Settings{}, err
		}
		return Settings{Mode: mode, URL: normalized}, nil
	}
	return Settings{Mode: mode}, nil
}

func ValidateURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ErrInvalidURL
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", ErrInvalidURL
	}
	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return "", ErrInvalidURL
	}
	if u.Host == "" || u.Hostname() == "" || strings.HasSuffix(u.Host, ":") {
		return "", ErrInvalidURL
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", ErrInvalidURL
		}
	}
	if u.Path != "" && u.Path != "/" {
		return "", ErrInvalidURL
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", ErrInvalidURL
	}
	u.Scheme = scheme
	u.Path = ""
	u.RawPath = ""
	return u.String(), nil
}

func RedactURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Redacted()
}

type State struct {
	mu        sync.RWMutex
	settings  Settings
	manualURL *url.URL
	transport *http.Transport
	client    *http.Client

	sysMu     sync.Mutex
	sysAt     time.Time
	sysCached SystemConfig
}

func New(settings Settings) *State {
	state := &State{}
	transport := cloneDefaultTransport()
	transport.Proxy = state.ProxyFor
	state.transport = transport
	state.client = &http.Client{Transport: transport}
	state.apply(settings)
	return state
}

func cloneDefaultTransport() *http.Transport {
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		return base.Clone()
	}
	return &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
}

func (s *State) apply(settings Settings) {
	mode, ok := ParseMode(string(settings.Mode))
	if !ok {
		mode = ModeSystem
	}
	var parsed *url.URL
	if raw := strings.TrimSpace(settings.URL); raw != "" {
		if normalized, err := ValidateURL(raw); err == nil {
			if u, err := url.Parse(normalized); err == nil {
				parsed = u
				settings.URL = normalized
			}
		}
	}
	settings.Mode = mode
	s.mu.Lock()
	s.settings = settings
	s.manualURL = parsed
	s.mu.Unlock()
	s.sysMu.Lock()
	s.sysAt = time.Time{}
	s.sysMu.Unlock()
}

func (s *State) Set(settings Settings) {
	s.apply(settings)
	s.transport.CloseIdleConnections()
}

func (s *State) Client() *http.Client {
	return s.client
}

func (s *State) Close() {
	s.transport.CloseIdleConnections()
}

func (s *State) current() (Settings, *url.URL) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings, s.manualURL
}

func (s *State) systemConfig() SystemConfig {
	if fn := emulator(); fn != nil {
		return fn()
	}
	s.sysMu.Lock()
	defer s.sysMu.Unlock()
	if !s.sysAt.IsZero() && time.Since(s.sysAt) < systemCacheTTL {
		return s.sysCached
	}
	cfg := readSystemProxy()
	s.sysCached = cfg
	s.sysAt = time.Now()
	return cfg
}

func (s *State) ProxyFor(req *http.Request) (*url.URL, error) {
	settings, manualURL := s.current()
	switch settings.Mode {
	case ModeDirect:
		return http.ProxyFromEnvironment(req)
	case ModeManual:
		if manualURL == nil || req == nil || req.URL == nil {
			return nil, nil
		}
		if isLoopbackHost(req.URL.Hostname()) {
			return nil, nil
		}
		return manualURL, nil
	default:
		if req == nil || req.URL == nil || isLoopbackHost(req.URL.Hostname()) {
			return nil, nil
		}
		sys := s.systemConfig()
		if shouldBypass(req.URL.Hostname(), effectivePort(req.URL), sys.Bypass) {
			return nil, nil
		}
		picked := pickSystemProxy(req.URL.Scheme, sys)
		if picked == "" {
			return nil, nil
		}
		return url.Parse(picked)
	}
}

func (s *State) GitProxy(remote string) (string, bool) {
	settings, manualURL := s.current()
	switch settings.Mode {
	case ModeDirect:
		return "", false
	case ModeManual:
		if manualURL == nil {
			return "", false
		}
		if u, ok := parseHTTPRemote(remote); ok && isLoopbackHost(u.Hostname()) {
			return "", true
		}
		return subprocessProxyURL(manualURL), true
	default:
		sys := s.systemConfig()
		if u, ok := parseHTTPRemote(remote); ok {
			if isLoopbackHost(u.Hostname()) || shouldBypass(u.Hostname(), effectivePort(u), sys.Bypass) {
				return "", true
			}
			return subprocessProxyString(pickSystemProxy(u.Scheme, sys)), true
		}
		picked := sys.HTTPS
		if picked == "" {
			picked = sys.HTTP
		}
		if picked == "" {
			picked = sys.SOCKS
		}
		return subprocessProxyString(picked), true
	}
}

func parseHTTPRemote(remote string) (*url.URL, bool) {
	if remote == "" {
		return nil, false
	}
	u, err := url.Parse(remote)
	if err != nil {
		return nil, false
	}
	scheme := strings.ToLower(u.Scheme)
	if (scheme != "http" && scheme != "https") || u.Hostname() == "" {
		return nil, false
	}
	return u, true
}

func pickSystemProxy(scheme string, sys SystemConfig) string {
	switch strings.ToLower(scheme) {
	case "https":
		if sys.HTTPS != "" {
			return sys.HTTPS
		}
		return sys.SOCKS
	case "http":
		if sys.HTTP != "" {
			return sys.HTTP
		}
		return sys.SOCKS
	default:
		return sys.SOCKS
	}
}

func (s *State) ApplyEnv(env []string) []string {
	settings, manualURL := s.current()
	switch settings.Mode {
	case ModeDirect:
		return env
	case ModeManual:
		if manualURL == nil {
			return env
		}
		target := subprocessProxyURL(manualURL)
		noProxy := formatNoProxy(nil)
		out, _ := stripProxyEnv(env)
		out = append(out,
			"HTTP_PROXY="+target,
			"http_proxy="+target,
			"HTTPS_PROXY="+target,
			"https_proxy="+target,
			"ALL_PROXY="+target,
			"all_proxy="+target,
			"NO_PROXY="+noProxy,
			"no_proxy="+noProxy,
		)
		return out
	default:
		sys := s.systemConfig()
		httpP := subprocessProxyString(sys.HTTP)
		httpsP := subprocessProxyString(sys.HTTPS)
		socksP := subprocessProxyString(sys.SOCKS)
		if httpP == "" {
			httpP = socksP
		}
		if httpsP == "" {
			httpsP = socksP
		}
		out, _ := stripProxyEnv(env)
		if httpP == "" && httpsP == "" && socksP == "" {
			return out
		}
		if httpP != "" {
			out = append(out, "HTTP_PROXY="+httpP, "http_proxy="+httpP)
		}
		if httpsP != "" {
			out = append(out, "HTTPS_PROXY="+httpsP, "https_proxy="+httpsP)
		}
		if socksP != "" {
			out = append(out, "ALL_PROXY="+socksP, "all_proxy="+socksP)
		}
		noProxy := formatNoProxy(sys.Bypass)
		out = append(out, "NO_PROXY="+noProxy, "no_proxy="+noProxy)
		return out
	}
}

func subprocessProxyURL(u *url.URL) string {
	cloned := *u
	if cloned.Scheme == "socks5" {
		cloned.Scheme = "socks5h"
	}
	return cloned.String()
}

func subprocessProxyString(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return subprocessProxyURL(u)
}

func stripProxyEnv(env []string) ([]string, bool) {
	stripped := false
	out := make([]string, 0, len(env)+8)
	for _, pair := range env {
		name, _, ok := strings.Cut(pair, "=")
		if ok && isProxyEnvKey(name) {
			stripped = true
			continue
		}
		out = append(out, pair)
	}
	return out, stripped
}

func isProxyEnvKey(name string) bool {
	if runtime.GOOS == "windows" {
		name = strings.ToLower(name)
	}
	switch name {
	case "http_proxy", "HTTP_PROXY",
		"https_proxy", "HTTPS_PROXY",
		"all_proxy", "ALL_PROXY",
		"no_proxy", "NO_PROXY":
		return true
	default:
		return false
	}
}

func buildHostPortURL(scheme, host string, port int) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if strings.Contains(host, "://") {
		if normalized, err := ValidateURL(host); err == nil {
			return normalized
		}
		return ""
	}
	if port > 0 && port <= 65535 {
		if _, _, err := net.SplitHostPort(host); err != nil {
			host = net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(port))
		}
	}
	normalized, err := ValidateURL(scheme + "://" + host)
	if err != nil {
		return ""
	}
	return normalized
}

func effectivePort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}

func isLoopbackHost(host string) bool {
	if host == "" {
		return false
	}
	lower := strings.ToLower(host)
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") {
		return true
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && addr.Unmap().IsLoopback()
}

func shouldBypass(host, port string, bypass []string) bool {
	if host == "" {
		return false
	}
	lowerHost := strings.ToLower(host)
	hostIP, ipErr := netip.ParseAddr(lowerHost)
	if ipErr == nil {
		hostIP = hostIP.Unmap()
	}
	for _, raw := range bypass {
		rule := strings.ToLower(strings.TrimSpace(raw))
		if rule == "" {
			continue
		}
		if rule == "*" {
			return true
		}
		if rule == "<local>" {
			if !strings.Contains(lowerHost, ".") {
				return true
			}
			continue
		}
		if prefix, ok := parseCIDRPrefix(rule); ok {
			if ipErr == nil && prefix.Contains(hostIP) {
				return true
			}
			continue
		}
		ruleHost, rulePort := splitRuleHostPort(rule)
		if rulePort != "" && rulePort != port {
			continue
		}
		if ruleIP, err := netip.ParseAddr(ruleHost); err == nil {
			if ipErr == nil && hostIP == ruleIP.Unmap() {
				return true
			}
			continue
		}
		if suffix, ok := strings.CutPrefix(ruleHost, "*."); ok && suffix != "" && !strings.Contains(suffix, "*") {
			if strings.HasSuffix(lowerHost, "."+suffix) {
				return true
			}
			continue
		}
		if suffix, ok := strings.CutPrefix(ruleHost, "."); ok && suffix != "" && !strings.Contains(suffix, "*") {
			if strings.HasSuffix(lowerHost, "."+suffix) {
				return true
			}
			continue
		}
		if strings.Contains(ruleHost, "*") {
			if wildcardMatch(ruleHost, lowerHost) {
				return true
			}
			continue
		}
		if lowerHost == ruleHost || strings.HasSuffix(lowerHost, "."+ruleHost) {
			return true
		}
	}
	return false
}

func splitRuleHostPort(rule string) (string, string) {
	if strings.HasPrefix(rule, "[") {
		host, port, err := net.SplitHostPort(rule)
		if err == nil {
			return host, port
		}
		return strings.Trim(rule, "[]"), ""
	}
	if strings.Count(rule, ":") == 1 {
		host, port, err := net.SplitHostPort(rule)
		if err == nil && port != "" {
			return host, port
		}
	}
	return rule, ""
}

func parseCIDRPrefix(rule string) (netip.Prefix, bool) {
	if !strings.Contains(rule, "/") {
		return netip.Prefix{}, false
	}
	if prefix, err := netip.ParsePrefix(rule); err == nil {
		return prefix, true
	}
	base, bits, ok := strings.Cut(rule, "/")
	if !ok || strings.Contains(base, ":") {
		return netip.Prefix{}, false
	}
	parts := strings.Split(base, ".")
	if len(parts) < 1 || len(parts) >= 4 {
		return netip.Prefix{}, false
	}
	for len(parts) < 4 {
		parts = append(parts, "0")
	}
	prefix, err := netip.ParsePrefix(strings.Join(parts, ".") + "/" + bits)
	return prefix, err == nil
}

func wildcardMatch(pattern, s string) bool {
	p, t := 0, 0
	star, match := -1, 0
	for t < len(s) {
		if p < len(pattern) && pattern[p] == s[t] {
			p++
			t++
		} else if p < len(pattern) && pattern[p] == '*' {
			star = p
			match = t
			p++
		} else if star != -1 {
			p = star + 1
			match++
			t = match
		} else {
			return false
		}
	}
	for p < len(pattern) && pattern[p] == '*' {
		p++
	}
	return p == len(pattern)
}

func formatNoProxy(bypass []string) string {
	entries := []string{"localhost", "127.0.0.1", "::1"}
	add := func(item string) {
		if item == "" || slices.Contains(entries, item) {
			return
		}
		entries = append(entries, item)
	}
	for _, raw := range bypass {
		rule := strings.TrimSpace(raw)
		if rule == "" || strings.EqualFold(rule, "<local>") {
			continue
		}
		if rule == "*" {
			add("*")
			continue
		}
		if prefix, ok := parseCIDRPrefix(rule); ok {
			add(prefix.String())
			continue
		}
		if suffix, ok := strings.CutPrefix(rule, "*."); ok && !strings.Contains(suffix, "*") {
			add("." + suffix)
			continue
		}
		if strings.Contains(rule, "*") {
			continue
		}
		add(rule)
	}
	return strings.Join(entries, ",")
}
