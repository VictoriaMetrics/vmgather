package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	importer "github.com/VictoriaMetrics/vmgather/internal/importer/server"
)

// Overridable at build time via: -ldflags "-X main.version=<value>"
var version = "dev"

func main() {
	addr := flag.String("addr", "0.0.0.0:8081", "HTTP server address")
	noBrowser := flag.Bool("no-browser", false, "Do not open browser on start")
	flag.Parse()

	finalAddr, err := ensureAvailablePort(*addr)
	if err != nil {
		log.Fatalf("Failed to find available port: %v", err)
	}
	if finalAddr != *addr {
		log.Printf("Port %s was busy, using %s instead", *addr, finalAddr)
	}

	srv := importer.NewServer(version)
	httpServer := &http.Server{
		Addr:              finalAddr,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 5 * time.Second,
		// Keep body reads unbounded to avoid aborting large uploads mid-transfer.
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Printf("VMImport %s listening on http://%s", version, finalAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	if !*noBrowser {
		time.Sleep(500 * time.Millisecond)
		openBrowser(fmt.Sprintf("http://%s", finalAddr))
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(ctx)
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		log.Printf("failed to open browser: %v", err)
	}
}

func ensureAvailablePort(addr string) (string, error) {
	listener, err := net.Listen("tcp", addr)
	if err == nil {
		_ = listener.Close()
		return addr, nil
	}

	log.Printf("Port %s is busy, finding available port...", addr)

	host, _, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		host = "0.0.0.0"
	}

	listener, err = net.Listen("tcp", host+":0")
	if err != nil {
		return "", fmt.Errorf("failed to grab ephemeral port: %w", err)
	}
	assigned := listener.Addr().String()
	_ = listener.Close()

	_, port, err := net.SplitHostPort(assigned)
	if err != nil {
		return "", fmt.Errorf("failed to parse assigned port: %w", err)
	}
	return net.JoinHostPort(host, port), nil
}
