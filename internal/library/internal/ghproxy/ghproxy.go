package ghproxy

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
)

const prefix = "https://gh-proxy.org/"

var hosts = map[string]bool{
	"github.com":                 true,
	"api.github.com":             true,
	"raw.githubusercontent.com":  true,
	"codeload.github.com":        true,
	"gist.github.com":            true,
	"gist.githubusercontent.com": true,
}

type Switch struct {
	on atomic.Bool
}

func (s *Switch) Set(on bool) {
	s.on.Store(on)
}

func (s *Switch) On() bool {
	return s != nil && s.on.Load()
}

func (s *Switch) URL(location string) (string, bool) {
	if !s.On() {
		return "", false
	}
	u, err := url.Parse(location)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || !hosts[strings.ToLower(u.Host)] {
		return "", false
	}
	return prefix + location, true
}

func (s *Switch) Client(client *http.Client) *http.Client {
	if client == nil {
		client = http.DefaultClient
	}
	wrapped := *client
	wrapped.Transport = s.Transport(client.Transport)
	return &wrapped
}

func (s *Switch) Transport(next http.RoundTripper) http.RoundTripper {
	if next == nil {
		next = http.DefaultTransport
	}
	return transport{mirror: s, next: next}
}

type transport struct {
	mirror *Switch
	next   http.RoundTripper
}

func (t transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if (req.Method != http.MethodGet && req.Method != http.MethodHead) || req.Response != nil || req.Header.Get("Authorization") != "" {
		return t.next.RoundTrip(req)
	}
	location, ok := t.mirror.URL(req.URL.String())
	if !ok {
		return t.next.RoundTrip(req)
	}
	target, err := url.Parse(location)
	if err != nil {
		return t.next.RoundTrip(req)
	}
	mirrored := req.Clone(req.Context())
	mirrored.URL, mirrored.Host = target, ""
	response, err := t.next.RoundTrip(mirrored)
	if err == nil && !unavailable(response.StatusCode) {
		return response, nil
	}
	if err == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		_ = response.Body.Close()
	}
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	return t.next.RoundTrip(req)
}

func unavailable(status int) bool {
	return status >= http.StatusInternalServerError || status == http.StatusForbidden || status == http.StatusTooManyRequests
}
