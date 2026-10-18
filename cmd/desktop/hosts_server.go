//go:build server

package main

import (
	"net/http"
	"os"
	"strings"
)

func init() {
	hosts := map[string]string{}
	for _, pair := range strings.Fields(os.Getenv("E2E_HOSTS")) {
		if host, target, ok := strings.Cut(pair, "="); ok {
			hosts[host] = target
		}
	}
	if len(hosts) > 0 {
		http.DefaultTransport = redirectHosts{hosts: hosts, next: http.DefaultTransport}
	}
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
