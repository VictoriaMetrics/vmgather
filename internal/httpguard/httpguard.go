// Package httpguard protects the local web UIs from cross-site request forgery and DNS rebinding.
package httpguard

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// SameOrigin rejects state-changing browser requests sent from another origin. Without it any web
// page open in the user's browser can POST to vmgather or vmimporter on localhost: "simple" requests
// (text/plain, multipart/form-data) need no CORS preflight.
//
// It is net/http.CrossOriginProtection with a JSON error for the UIs. GET, HEAD and OPTIONS always pass.
// If Sec-Fetch-Site is present, only "same-origin" and "none" pass. Otherwise, if Origin is present, its
// host must equal the request Host. Requests without both headers (curl, scripts) pass.
//
// trustedHosts (-allowed-hosts) are also accepted as the Origin host when the browser sends no
// Sec-Fetch-Site (plain HTTP sites): a reverse proxy that rewrites Host would fail every POST otherwise.
func SameOrigin(next http.Handler, trustedHosts ...string) http.Handler {
	p := http.NewCrossOriginProtection()
	trusted := hostSet(trustedHosts)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := p.Check(r); err != nil && !isTrustedOrigin(r, trusted) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"cross-origin request rejected"}` + "\n"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isTrustedOrigin reports whether a request without Sec-Fetch-Site comes from a page on a trusted host.
// A browser that sends Sec-Fetch-Site has decided itself, so its "cross-site" is never overridden.
func isTrustedOrigin(r *http.Request, trusted map[string]struct{}) bool {
	if len(trusted) == 0 || r.Header.Get("Sec-Fetch-Site") != "" {
		return false
	}
	u, err := url.Parse(r.Header.Get("Origin"))
	if err != nil || u.Host == "" {
		return false
	}
	_, ok := trusted[hostName(u.Host)]
	return ok
}

// LocalHostOnly blocks DNS rebinding. A page from an attacker's domain whose name later resolves to
// 127.0.0.1 reaches the local UI as "same origin" and can read every response (directory listings,
// bundles). Such requests come from the local machine with the attacker's name in Host. Requests from the
// local machine must therefore use "localhost", a "*.localhost" name, an IP address or one of allowedHosts
// (for a reverse proxy on the same machine that forwards its own Host name). Requests from other machines
// are not checked, so it does not cover Docker port mapping or a rebinding to a LAN address of a server that
// listens on 0.0.0.0; SameOrigin still applies there.
func LocalHostOnly(next http.Handler, allowedHosts ...string) http.Handler {
	allowed := hostSet(allowedHosts)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isLoopbackClient(r.RemoteAddr) && !isLocalHostName(r.Host) && !isAllowedHost(r.Host, allowed) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"open this UI via localhost or an IP address"}` + "\n"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// hostSet normalizes host names with hostName: "proxy.local:8443" allows proxy.local on any port.
func hostSet(hosts []string) map[string]struct{} {
	set := make(map[string]struct{}, len(hosts))
	for _, h := range hosts {
		if h = hostName(strings.TrimSpace(h)); h != "" {
			set[h] = struct{}{}
		}
	}
	return set
}

func isLoopbackClient(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isLocalHostName(hostport string) bool {
	host := hostName(hostport)
	return host == "localhost" || strings.HasSuffix(host, ".localhost") || net.ParseIP(host) != nil
}

func isAllowedHost(hostport string, allowed map[string]struct{}) bool {
	_, ok := allowed[hostName(hostport)]
	return ok
}

// hostName returns the lower-case host part of a Host header value, without port and IPv6 brackets.
func hostName(hostport string) string {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	return strings.ToLower(strings.Trim(host, "[]"))
}

// ParseHostList splits a comma-separated -allowed-hosts flag value.
func ParseHostList(value string) []string {
	var hosts []string
	for _, h := range strings.Split(value, ",") {
		if h = strings.TrimSpace(h); h != "" {
			hosts = append(hosts, h)
		}
	}
	return hosts
}
