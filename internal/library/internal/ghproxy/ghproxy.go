package ghproxy

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

const prefix = "https://gh-proxy.org/"

var headerWait = 10 * time.Second

var publicQuery = map[string]bool{"recursive": true, "per_page": true, "page": true}

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
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return "", false
	}
	for key := range query {
		if !publicQuery[key] {
			return "", false
		}
	}
	return prefix + location, true
}

func Attempt(ctx context.Context) (context.Context, context.CancelFunc) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, time.Until(deadline)/2)
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
	if (req.Method != http.MethodGet && req.Method != http.MethodHead) || req.Response != nil || req.Header.Get("Authorization") != "" || req.Header.Get("Cookie") != "" {
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
	wait := headerWait
	if deadline, ok := req.Context().Deadline(); ok {
		wait = min(wait, time.Until(deadline)/2)
	}
	ctx, cancel := context.WithCancel(req.Context())
	timer := time.AfterFunc(wait, cancel)
	mirrored := req.Clone(ctx)
	mirrored.URL, mirrored.Host = target, ""
	response, err := t.next.RoundTrip(mirrored)
	inTime := timer.Stop()
	if err == nil && response.Body == nil {
		response.Body = http.NoBody
	}
	if err == nil && inTime && !unavailable(response.StatusCode) {
		response.Body = cancelOnClose{ReadCloser: response.Body, cancel: cancel}
		return response, nil
	}
	cancel()
	if err == nil {
		_ = response.Body.Close()
	}
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	return t.next.RoundTrip(req)
}

type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b cancelOnClose) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

func unavailable(status int) bool {
	return status >= http.StatusInternalServerError || status == http.StatusForbidden || status == http.StatusTooManyRequests
}
