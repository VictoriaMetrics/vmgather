package httpguard

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSameOrigin(t *testing.T) {
	cases := []struct {
		name     string
		method   string
		host     string
		origin   string
		fetch    string
		rejected bool
	}{
		{name: "cross-origin POST", method: http.MethodPost, host: "localhost:8080", origin: "https://evil.example", rejected: true},
		{name: "null origin POST", method: http.MethodPost, host: "localhost:8080", origin: "null", rejected: true},
		{name: "same-origin POST", method: http.MethodPost, host: "localhost:8080", origin: "http://localhost:8080"},
		{name: "other port is another origin", method: http.MethodPost, host: "localhost:8080", origin: "http://localhost:3000", rejected: true},
		{name: "IPv6 loopback same origin", method: http.MethodPost, host: "[::1]:8080", origin: "http://[::1]:8080"},
		{name: "default port omitted on both sides", method: http.MethodPost, host: "vmgather.internal", origin: "http://vmgather.internal"},
		{name: "Sec-Fetch-Site cross-site without Origin", method: http.MethodPost, host: "localhost:8080", fetch: "cross-site", rejected: true},
		{name: "Sec-Fetch-Site same-site is still another origin", method: http.MethodPost, host: "localhost:8080", fetch: "same-site", rejected: true},
		{name: "Sec-Fetch-Site same-origin wins over Origin", method: http.MethodPost, host: "localhost:8080", origin: "https://evil.example", fetch: "same-origin"},
		{name: "Sec-Fetch-Site none (typed URL, bookmark)", method: http.MethodPost, host: "localhost:8080", fetch: "none"},
		{name: "no browser headers (curl)", method: http.MethodPost, host: "localhost:8080"},
		{name: "cross-origin GET is safe", method: http.MethodGet, host: "localhost:8080", origin: "https://evil.example", fetch: "cross-site"},
		{name: "cross-origin DELETE", method: http.MethodDelete, host: "localhost:8080", origin: "https://evil.example", rejected: true},
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "http://"+tc.host+"/api/x", nil)
			req.Host = tc.host
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.fetch != "" {
				req.Header.Set("Sec-Fetch-Site", tc.fetch)
			}
			rr := httptest.NewRecorder()
			SameOrigin(next).ServeHTTP(rr, req)
			if got := rr.Code == http.StatusForbidden; got != tc.rejected {
				t.Fatalf("rejected=%v, want %v (status %d)", got, tc.rejected, rr.Code)
			}
			if tc.rejected && rr.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("rejection is not JSON: %q", rr.Body.String())
			}
		})
	}
}

// DNS rebinding: a page from rebind.example whose name later resolves to 127.0.0.1 talks to the local UI as
// "same origin" and can read responses (directory listings, bundles). Requests from the local machine must use
// a local host name or an IP address.
func TestLocalHostOnly(t *testing.T) {
	cases := []struct {
		name, remote, host string
		rejected           bool
	}{
		{name: "rebinding name from loopback", remote: "127.0.0.1:5000", host: "rebind.example:8080", rejected: true},
		{name: "rebinding name from IPv6 loopback", remote: "[::1]:5000", host: "rebind.example", rejected: true},
		{name: "localhost", remote: "127.0.0.1:5000", host: "localhost:8080"},
		{name: "subdomain of localhost", remote: "127.0.0.1:5000", host: "app.localhost:8080"},
		{name: "IPv4 literal", remote: "127.0.0.1:5000", host: "127.0.0.1:8080"},
		{name: "IPv6 literal", remote: "[::1]:5000", host: "[::1]:8080"},
		{name: "LAN IP", remote: "127.0.0.1:5000", host: "192.168.1.10:8080"},
		{name: "remote client by host name (ingress, Docker)", remote: "10.1.2.3:5000", host: "vmgather.internal"},
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://x/api/fs/list", nil)
			req.Host = tc.host
			req.RemoteAddr = tc.remote
			rr := httptest.NewRecorder()
			LocalHostOnly(next).ServeHTTP(rr, req)
			if got := rr.Code == http.StatusForbidden; got != tc.rejected {
				t.Fatalf("rejected=%v, want %v", got, tc.rejected)
			}
		})
	}
}

// A reverse proxy on the same machine forwards with its own Host name. The operator can allow that name.
func TestLocalHostOnly_AllowedHosts(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	// An entry with a port allows the host name on any port: requests are matched without the port.
	h := LocalHostOnly(next, "vmgather.example", "Metrics.Internal", "proxy.local:8443")
	for host, rejected := range map[string]bool{
		"vmgather.example:443": false, "metrics.internal": false, "rebind.example": true,
		"proxy.local:8443": false, "proxy.local": false, "proxy.local.evil:8443": true,
	} {
		req := httptest.NewRequest(http.MethodGet, "http://x/", nil)
		req.Host, req.RemoteAddr = host, "127.0.0.1:5000"
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if got := rr.Code == http.StatusForbidden; got != rejected {
			t.Errorf("host %s: rejected=%v, want %v", host, got, rejected)
		}
	}
}
