package services

import (
	"net"
	"net/http"
	"net/http/httptest"
)

func newIPv4Server(handler http.Handler) *httptest.Server {
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
