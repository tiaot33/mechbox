//go:build server

package main

import (
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"

	"github.com/tiaot33/mechbox/internal/library"
)

func openDesktopLibrary(path string) (*library.Library, error) {
	hosts := map[string]string{}
	for _, pair := range strings.Fields(os.Getenv("E2E_HOSTS")) {
		if host, target, ok := strings.Cut(pair, "="); ok {
			hosts[host] = target
		}
	}
	if len(hosts) == 0 {
		return library.Open(path)
	}
	var proxyFn atomic.Pointer[func(*http.Request) (*url.URL, error)]
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = func(req *http.Request) (*url.URL, error) {
		if fn := proxyFn.Load(); fn != nil {
			return (*fn)(req)
		}
		return http.ProxyFromEnvironment(req)
	}
	client := &http.Client{Transport: redirectHosts{hosts: hosts, next: transport}}
	lib, err := library.Open(path, library.WithHTTPClient(client))
	if err != nil {
		return nil, err
	}
	fn := library.ProxyFunc(lib)
	proxyFn.Store(&fn)
	return lib, nil
}

type redirectHosts struct {
	hosts map[string]string
	next  http.RoundTripper
}

func (r redirectHosts) RoundTrip(req *http.Request) (*http.Response, error) {
	target, ok := r.hosts[req.URL.Host]
	if !ok {
		return r.next.RoundTrip(req)
	}
	req = req.Clone(req.Context())
	req.URL.Scheme = "http"
	req.URL.Host = target
	req.Host = target
	return r.next.RoundTrip(req)
}
