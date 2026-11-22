package ghproxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type recorder struct {
	mu     sync.Mutex
	seen   []string
	mirror func(*http.Request) (*http.Response, error)
}

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.seen = append(r.seen, req.URL.String())
	r.mu.Unlock()
	if req.URL.Host == "gh-proxy.org" && r.mirror != nil {
		return r.mirror(req)
	}
	return respond(http.StatusOK), nil
}

func respond(status int) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("body")), Header: http.Header{}}
}

func send(t *testing.T, transport http.RoundTripper, method, location string, header http.Header, redirected bool) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, location, nil)
	if err != nil {
		t.Fatal(err)
	}
	for key, values := range header {
		req.Header[key] = values
	}
	if redirected {
		req.Response = respond(http.StatusFound)
	}
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp
}

func TestURLRewritesOnlyGitHubHostsWhenOn(t *testing.T) {
	var mirror Switch
	if _, ok := mirror.URL("https://api.github.com/repos/o/r"); ok {
		t.Fatal("rewrote while off")
	}
	if _, ok := (*Switch)(nil).URL("https://api.github.com/repos/o/r"); ok {
		t.Fatal("nil switch rewrote")
	}
	mirror.Set(true)
	for _, location := range []string{
		"https://github.com/o/r.git",
		"https://api.github.com/repos/o/r/commits/main",
		"https://raw.githubusercontent.com/o/r/main/SKILL.md",
		"https://codeload.github.com/o/r/tar.gz/main",
		"https://gist.github.com/o/abc",
		"https://GIST.githubusercontent.com/o/abc/raw",
	} {
		if got, ok := mirror.URL(location); !ok || got != "https://gh-proxy.org/"+location {
			t.Errorf("URL(%q) = %q, %v", location, got, ok)
		}
	}
	for _, location := range []string{
		"http://github.com/o/r",
		"https://gitlab.com/o/r",
		"https://user:secret@github.com/o/r.git",
		"https://github.com:8443/o/r",
		"https://example.com/https://github.com/o/r",
		"git@github.com:o/r.git",
	} {
		if got, ok := mirror.URL(location); ok {
			t.Errorf("URL(%q) = %q; want unchanged", location, got)
		}
	}
}

func TestTransportSendsOnlyAnonymousFirstRequestsToMirror(t *testing.T) {
	next := &recorder{}
	var mirror Switch
	mirror.Set(true)
	transport := mirror.Transport(next)
	send(t, transport, http.MethodGet, "https://api.github.com/repos/o/r", nil, false)
	send(t, transport, http.MethodGet, "https://api.github.com/repos/o/r", http.Header{"Authorization": {"Bearer secret"}}, false)
	send(t, transport, http.MethodGet, "https://codeload.github.com/o/r/tar.gz/main?token=temporary", nil, true)
	send(t, transport, http.MethodPost, "https://api.github.com/graphql", nil, false)
	send(t, transport, http.MethodGet, "https://gitlab.com/o/r", nil, false)
	want := []string{
		"https://gh-proxy.org/https://api.github.com/repos/o/r",
		"https://api.github.com/repos/o/r",
		"https://codeload.github.com/o/r/tar.gz/main?token=temporary",
		"https://api.github.com/graphql",
		"https://gitlab.com/o/r",
	}
	if strings.Join(next.seen, "\n") != strings.Join(want, "\n") {
		t.Fatalf("sent\n%s\nwant\n%s", strings.Join(next.seen, "\n"), strings.Join(want, "\n"))
	}
	mirror.Set(false)
	next.seen = nil
	send(t, transport, http.MethodGet, "https://api.github.com/repos/o/r", nil, false)
	if len(next.seen) != 1 || next.seen[0] != "https://api.github.com/repos/o/r" {
		t.Fatalf("after turning off sent %v", next.seen)
	}
}

func TestTransportFallsBackToDirectWhenMirrorUnavailable(t *testing.T) {
	cases := []struct {
		name     string
		mirror   func(*http.Request) (*http.Response, error)
		status   int
		fallback bool
	}{
		{"network error", func(*http.Request) (*http.Response, error) { return nil, errors.New("connection refused") }, http.StatusOK, true},
		{"bad gateway", func(*http.Request) (*http.Response, error) { return respond(http.StatusBadGateway), nil }, http.StatusOK, true},
		{"blocked", func(*http.Request) (*http.Response, error) { return respond(http.StatusForbidden), nil }, http.StatusOK, true},
		{"throttled", func(*http.Request) (*http.Response, error) { return respond(http.StatusTooManyRequests), nil }, http.StatusOK, true},
		{"not found", func(*http.Request) (*http.Response, error) { return respond(http.StatusNotFound), nil }, http.StatusNotFound, false},
		{"ok", func(*http.Request) (*http.Response, error) { return respond(http.StatusOK), nil }, http.StatusOK, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			next := &recorder{mirror: tc.mirror}
			var mirror Switch
			mirror.Set(true)
			resp := send(t, mirror.Transport(next), http.MethodGet, "https://raw.githubusercontent.com/o/r/main/SKILL.md", nil, false)
			want := []string{"https://gh-proxy.org/https://raw.githubusercontent.com/o/r/main/SKILL.md"}
			if tc.fallback {
				want = append(want, "https://raw.githubusercontent.com/o/r/main/SKILL.md")
			}
			if strings.Join(next.seen, "\n") != strings.Join(want, "\n") || resp.StatusCode != tc.status {
				t.Fatalf("sent %v, status %d; want %v, %d", next.seen, resp.StatusCode, want, tc.status)
			}
		})
	}
}

func TestURLKeepsLinksWithUnknownQueryDirect(t *testing.T) {
	var mirror Switch
	mirror.Set(true)
	for _, location := range []string{
		"https://raw.githubusercontent.com/o/private/main/SKILL.md?token=GHSAT0secret",
		"https://codeload.github.com/o/r/tar.gz/main?token=secret",
		"https://api.github.com/repos/o/r/git/trees/sha?recursive=1&access_token=secret",
		"https://github.com/o/r/archive/main.zip?%zz",
	} {
		if got, ok := mirror.URL(location); ok {
			t.Errorf("URL(%q) = %q; want direct", location, got)
		}
	}
	for _, location := range []string{
		"https://api.github.com/repos/o/r/git/trees/sha?recursive=1",
		"https://api.github.com/repos/o/r/compare/a...b?per_page=100&page=2",
	} {
		if _, ok := mirror.URL(location); !ok {
			t.Errorf("URL(%q) not mirrored", location)
		}
	}
}

func TestTransportSendsCookieRequestsDirect(t *testing.T) {
	next := &recorder{}
	var mirror Switch
	mirror.Set(true)
	send(t, mirror.Transport(next), http.MethodGet, "https://raw.githubusercontent.com/o/r/main/SKILL.md", http.Header{"Cookie": {"session=secret"}}, false)
	if len(next.seen) != 1 || next.seen[0] != "https://raw.githubusercontent.com/o/r/main/SKILL.md" {
		t.Fatalf("sent %v; want direct only", next.seen)
	}
}

func TestTransportFallsBackWhenMirrorStallsOrHasNoBody(t *testing.T) {
	previous := headerWait
	headerWait = 50 * time.Millisecond
	t.Cleanup(func() { headerWait = previous })
	cases := map[string]func(*http.Request) (*http.Response, error){
		"stalled": func(r *http.Request) (*http.Response, error) {
			<-r.Context().Done()
			return nil, r.Context().Err()
		},
		"nil body": func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusForbidden, Header: http.Header{}}, nil
		},
	}
	for name, mirrorResponse := range cases {
		t.Run(name, func(t *testing.T) {
			next := &recorder{mirror: mirrorResponse}
			var mirror Switch
			mirror.Set(true)
			resp := send(t, mirror.Transport(next), http.MethodGet, "https://raw.githubusercontent.com/o/r/main/SKILL.md", nil, false)
			if resp.StatusCode != http.StatusOK || len(next.seen) != 2 || next.seen[1] != "https://raw.githubusercontent.com/o/r/main/SKILL.md" {
				t.Fatalf("sent %v, status %d; want the mirror then direct", next.seen, resp.StatusCode)
			}
		})
	}
}

func TestAttemptLeavesHalfTheDeadline(t *testing.T) {
	parent, cancel := context.WithTimeout(t.Context(), time.Hour)
	defer cancel()
	attempt, cancelAttempt := Attempt(parent)
	defer cancelAttempt()
	deadline, ok := attempt.Deadline()
	if remaining := time.Until(deadline); !ok || remaining > 31*time.Minute || remaining < 29*time.Minute {
		t.Fatalf("attempt deadline in %v, %v; want about half an hour", remaining, ok)
	}
	unbounded, cancelUnbounded := Attempt(t.Context())
	defer cancelUnbounded()
	if _, ok := unbounded.Deadline(); ok {
		t.Fatal("attempt without a parent deadline got one")
	}
}
