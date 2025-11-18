package vm

import (
	"net"
	"net/http"
	"net/http/httptest"
)

// newIPv4TestServer creates an httptest.Server bound explicitly to 127.0.0.1.
// The default httptest server may bind to IPv6 (::1), which can be disallowed
// in sandboxed environments. This helper keeps tests deterministic.
func newIPv4TestServer(handler http.Handler) *httptest.Server {
	server := &httptest.Server{
		Config: &http.Server{
			Handler: handler,
		},
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	server.Listener = ln
	server.Start()
	return server
}
