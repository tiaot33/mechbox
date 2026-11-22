package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestValidateURLAndParse(t *testing.T) {
	t.Parallel()
	valid := map[string]string{
		"http://127.0.0.1:7890":               "http://127.0.0.1:7890",
		"  HTTP://127.0.0.1:7890/  ":          "http://127.0.0.1:7890",
		"https://proxy.example.com:8443":      "https://proxy.example.com:8443",
		"socks5://127.0.0.1:1080":             "socks5://127.0.0.1:1080",
		"SOCKS5H://user:pass@127.0.0.1:1080":  "socks5h://user:pass@127.0.0.1:1080",
		"http://user:p%40ss@proxy.local:3128": "http://user:p%40ss@proxy.local:3128",
		"http://[::1]:7890":                   "http://[::1]:7890",
	}
	for input, want := range valid {
		got, err := ValidateURL(input)
		if err != nil {
			t.Errorf("ValidateURL(%q) error = %v; want %q", input, err, want)
			continue
		}
		if got != want {
			t.Errorf("ValidateURL(%q) = %q; want %q", input, got, want)
		}
	}
	invalid := []string{
		"",
		"   ",
		"127.0.0.1:7890",
		"ftp://127.0.0.1:21",
		"socks4://127.0.0.1:1080",
		"http://",
		"http://:7890",
		"http://127.0.0.1:",
		"http://127.0.0.1:99999",
		"http://127.0.0.1:0",
		"http://127.0.0.1:7890/path",
		"http://127.0.0.1:7890?",
		"http://127.0.0.1:7890?a=1",
		"http://127.0.0.1:7890#frag",
		"http://alice:topSecret99@127.0.0.1:abc",
	}
	for _, input := range invalid {
		got, err := ValidateURL(input)
		if err == nil {
			t.Errorf("ValidateURL(%q) = %q, nil; want error", input, got)
			continue
		}
		if strings.Contains(err.Error(), "topSecret99") {
			t.Errorf("ValidateURL(%q) leaked password in error %q", input, err.Error())
		}
	}
}

func TestRedactURL(t *testing.T) {
	t.Parallel()
	if got := RedactURL("http://127.0.0.1:7890"); got != "http://127.0.0.1:7890" {
		t.Fatalf("RedactURL without auth = %q", got)
	}
	if got := RedactURL("http://alice:secret@127.0.0.1:7890"); got != "http://alice:xxxxx@127.0.0.1:7890" {
		t.Fatalf("RedactURL with auth = %q", got)
	}
	if got := RedactURL("http://alice:topSecret99@127.0.0.1:abc"); got != "" {
		t.Fatalf("RedactURL malformed = %q; want empty so password is never leaked", got)
	}
	if got := RedactURL(""); got != "" {
		t.Fatalf("RedactURL empty = %q", got)
	}
}

func TestParseWindowsProxyServer(t *testing.T) {
	t.Parallel()
	got := parseWindowsProxyServer("127.0.0.1:7890")
	want := SystemConfig{HTTP: "http://127.0.0.1:7890", HTTPS: "http://127.0.0.1:7890"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("global = %+v; want %+v", got, want)
	}

	got = parseWindowsProxyServer("http=127.0.0.1:7890;https=127.0.0.1:7891;socks=127.0.0.1:1080;ftp=127.0.0.1:2121")
	want = SystemConfig{HTTP: "http://127.0.0.1:7890", HTTPS: "http://127.0.0.1:7891", SOCKS: "socks5://127.0.0.1:1080"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("per-protocol = %+v; want %+v", got, want)
	}

	got = parseWindowsProxyServer("socks=socks://127.0.0.1:1080")
	want = SystemConfig{SOCKS: "socks5://127.0.0.1:1080"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("socks prefix = %+v; want %+v", got, want)
	}

	bypass := parseWindowsProxyOverride("localhost; 127.*; *.local ;<local>;")
	wantBypass := []string{"localhost", "127.*", "*.local", "<local>"}
	if !reflect.DeepEqual(bypass, wantBypass) {
		t.Fatalf("override = %v; want %v", bypass, wantBypass)
	}
}

func TestParseEnvSystemProxy(t *testing.T) {
	t.Parallel()
	lookup := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	got := parseEnvSystemProxy(lookup(map[string]string{
		"http_proxy":  "127.0.0.1:7890",
		"HTTPS_PROXY": "http://127.0.0.1:7891",
		"ALL_PROXY":   "socks5://127.0.0.1:1080",
		"no_proxy":    "corp.example, .local , 10.0.0.0/8",
	}))
	want := SystemConfig{
		HTTP:   "http://127.0.0.1:7890",
		HTTPS:  "http://127.0.0.1:7891",
		SOCKS:  "socks5://127.0.0.1:1080",
		Bypass: []string{"corp.example", ".local", "10.0.0.0/8"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("env proxy = %+v; want %+v", got, want)
	}

	got = parseEnvSystemProxy(lookup(map[string]string{"all_proxy": "http://127.0.0.1:8080"}))
	want = SystemConfig{HTTP: "http://127.0.0.1:8080", HTTPS: "http://127.0.0.1:8080"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("http all_proxy = %+v; want %+v", got, want)
	}
}

func TestBypassLoopbackAndNoProxy(t *testing.T) {
	t.Parallel()
	for _, host := range []string{"localhost", "api.localhost", "127.0.0.1", "::1", "::ffff:127.0.0.1"} {
		if !isLoopbackHost(host) {
			t.Errorf("isLoopbackHost(%q) = false; want true", host)
		}
	}

	rules := []string{"*.wild.example", ".dot.example", "corp.example", "169.254/16", "10.0.0.0/8", "192.168.*", "api.internal:8443", "fd00::1", "<local>"}
	should := [][2]string{
		{"printer.wild.example", "80"},
		{"git.dot.example", "443"},
		{"corp.example", "443"},
		{"git.corp.example", "443"},
		{"169.254.12.34", "80"},
		{"10.1.2.3", "443"},
		{"192.168.1.5", "80"},
		{"api.internal", "8443"},
		{"fd00:0:0:0:0:0:0:1", "443"},
		{"intranet", "80"},
	}
	for _, tc := range should {
		if !shouldBypass(tc[0], tc[1], rules) {
			t.Errorf("shouldBypass(%q, %q) = false; want true", tc[0], tc[1])
		}
	}
	shouldNot := [][2]string{
		{"wild.example", "80"},
		{"dot.example", "443"},
		{"github.com", "443"},
		{"api.internal", "443"},
		{"notcorp.example", "443"},
		{"172.16.0.1", "80"},
	}
	for _, tc := range shouldNot {
		if shouldBypass(tc[0], tc[1], rules) {
			t.Errorf("shouldBypass(%q, %q) = true; want false", tc[0], tc[1])
		}
	}
	noProxy := formatNoProxy(rules)
	for _, want := range []string{"localhost", "127.0.0.1", "::1", ".wild.example", ".dot.example", "corp.example", "169.254.0.0/16", "10.0.0.0/8", "api.internal:8443"} {
		if !slices.Contains(strings.Split(noProxy, ","), want) {
			t.Errorf("formatNoProxy = %q; missing %q", noProxy, want)
		}
	}
}

func TestProxyForApplyEnvAndGitProxy(t *testing.T) {
	var currentSys SystemConfig
	currentSys = SystemConfig{
		HTTP:   "http://127.0.0.1:7890",
		HTTPS:  "http://127.0.0.1:7891",
		SOCKS:  "socks5://127.0.0.1:1080",
		Bypass: []string{"*.local", "192.168.*", "<local>"},
	}
	restore := Emulate(func() SystemConfig { return currentSys })
	t.Cleanup(restore)

	state := New(Settings{Mode: ModeSystem})
	defer state.Close()

	reqHTTPS, _ := http.NewRequest(http.MethodGet, "https://github.com/a/b", nil)
	reqHTTP, _ := http.NewRequest(http.MethodGet, "http://example.com/a", nil)
	reqBypass, _ := http.NewRequest(http.MethodGet, "https://box.local/a", nil)
	reqLoopback, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:11434/v1", nil)

	assertProxy := func(req *http.Request, want string) {
		t.Helper()
		got, err := state.ProxyFor(req)
		if err != nil {
			t.Fatalf("ProxyFor(%s) error = %v", req.URL, err)
		}
		gotStr := ""
		if got != nil {
			gotStr = got.String()
		}
		if gotStr != want {
			t.Fatalf("ProxyFor(%s) = %q; want %q", req.URL, gotStr, want)
		}
	}

	assertProxy(reqHTTPS, "http://127.0.0.1:7891")
	assertProxy(reqHTTP, "http://127.0.0.1:7890")
	assertProxy(reqBypass, "")
	assertProxy(reqLoopback, "")

	if v, ok := state.GitProxy("https://github.com/org/repo.git"); !ok || v != "http://127.0.0.1:7891" {
		t.Fatalf("GitProxy https = %q, %v", v, ok)
	}
	if v, ok := state.GitProxy("http://192.168.1.10/org/repo.git"); !ok || v != "" {
		t.Fatalf("GitProxy bypassed 192.168.* = %q, %v; want empty override", v, ok)
	}
	if v, ok := state.GitProxy("http://intranet/org/repo.git"); !ok || v != "" {
		t.Fatalf("GitProxy bypassed <local> = %q, %v; want empty override", v, ok)
	}

	env := state.ApplyEnv([]string{"PATH=/usr/bin", "HTTPS_PROXY=http://old:1", "https_proxy=http://old:1"})
	envMap := toEnvMap(env)
	if envMap["HTTP_PROXY"] != "http://127.0.0.1:7890" || envMap["HTTPS_PROXY"] != "http://127.0.0.1:7891" || envMap["ALL_PROXY"] != "socks5h://127.0.0.1:1080" {
		t.Fatalf("system env = %v", envMap)
	}

	currentSys = SystemConfig{}
	env = state.ApplyEnv([]string{"PATH=/usr/bin", "HTTPS_PROXY=http://stale:9999", "http_proxy=http://stale:9999"})
	envMap = toEnvMap(env)
	if envMap["HTTPS_PROXY"] != "" || envMap["http_proxy"] != "" || envMap["PATH"] != "/usr/bin" {
		t.Fatalf("empty system proxy should strip stale env vars: %v", envMap)
	}
	if v, ok := state.GitProxy("https://github.com/org/repo.git"); !ok || v != "" {
		t.Fatalf("GitProxy with empty system proxy = %q, %v; want empty override", v, ok)
	}

	state.Set(Settings{Mode: ModeManual, URL: "socks5://alice:pw@127.0.0.1:1080"})
	assertProxy(reqHTTPS, "socks5://alice:pw@127.0.0.1:1080")
	assertProxy(reqBypass, "socks5://alice:pw@127.0.0.1:1080")
	assertProxy(reqLoopback, "")
	if v, ok := state.GitProxy("https://github.com/org/repo.git"); !ok || v != "socks5h://alice:pw@127.0.0.1:1080" {
		t.Fatalf("GitProxy manual = %q, %v", v, ok)
	}

	env = state.ApplyEnv([]string{"PATH=/usr/bin", "http_proxy=http://old:1"})
	envMap = toEnvMap(env)
	for _, key := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy"} {
		if envMap[key] != "socks5h://alice:pw@127.0.0.1:1080" {
			t.Fatalf("manual env[%s] = %q; want socks5h URL", key, envMap[key])
		}
	}

	state.Set(Settings{Mode: ModeDirect})
	baseEnv := []string{"PATH=/usr/bin", "HTTPS_PROXY=http://keep:9999"}
	gotEnv := state.ApplyEnv(baseEnv)
	if !reflect.DeepEqual(gotEnv, baseEnv) {
		t.Fatalf("direct mode ApplyEnv = %v; want unchanged", gotEnv)
	}
	if _, ok := state.GitProxy("https://github.com/org/repo.git"); ok {
		t.Fatal("direct mode GitProxy should not override git config")
	}
}

func toEnvMap(env []string) map[string]string {
	m := map[string]string{}
	for _, pair := range env {
		k, v, _ := strings.Cut(pair, "=")
		m[k] = v
	}
	return m
}

func TestStateClientRoutesThroughHTTPProxy(t *testing.T) {
	t.Parallel()
	var gotAuth, gotTarget string
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Proxy-Authorization")
		gotTarget = r.URL.String()
		_, _ = io.WriteString(w, "via-proxy")
	}))
	defer proxyServer.Close()

	u, err := url.Parse(proxyServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword("bob", "s3cret")

	state := New(Settings{Mode: ModeManual, URL: u.String()})
	defer state.Close()

	resp, err := state.Client().Get("http://target.example/hello")
	if err != nil {
		t.Fatalf("GET via proxy: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "via-proxy" {
		t.Fatalf("body = %q; want via-proxy", body)
	}
	if gotTarget != "http://target.example/hello" {
		t.Fatalf("proxy target = %q", gotTarget)
	}
	if !strings.HasPrefix(gotAuth, "Basic ") {
		t.Fatalf("Proxy-Authorization = %q; want Basic auth", gotAuth)
	}
}
