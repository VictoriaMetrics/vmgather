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

	"github.com/VictoriaMetrics/vmgather/internal/server"
)

const (
	version = "1.4.1"
)

func main() {
	// Parse flags
	addr := flag.String("addr", "localhost:8080", "HTTP server address")
	outputDir := flag.String("output", "./exports", "Export output directory")
	noBrowser := flag.Bool("no-browser", false, "Don't open browser automatically")
	debug := flag.Bool("debug", false, "Enable debug logging")
	flag.Parse()

	log.Printf("vmgather v%s starting...", version)

	// Try to find available port if default is busy
	finalAddr, err := ensureAvailablePort(*addr)
	if err != nil {
		log.Fatalf("Failed to find available port: %v", err)
	}
	if finalAddr != *addr {
		log.Printf("Port %s was busy, using %s instead", *addr, finalAddr)
	}

	// Create HTTP server
	srv := server.NewServer(*outputDir, version, *debug)
	httpServer := &http.Server{
		Addr:    finalAddr,
		Handler: srv.Router(),
	}

	// Start server in goroutine
	go func() {
		log.Printf("Server listening on http://%s", finalAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	// Open browser automatically
	if !*noBrowser {
		time.Sleep(500 * time.Millisecond) // Wait for server to start
		openBrowser(fmt.Sprintf("http://%s", finalAddr))
	}

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")

	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}

	log.Println("Server stopped")
}

// ensureAvailablePort checks if the given address is available
// If not, tries to find an ephemeral port automatically
func ensureAvailablePort(addr string) (string, error) {
	// Try the requested address first
	listener, err := net.Listen("tcp", addr)
	if err == nil {
		// Port is available, close it and return
		_ = listener.Close()
		return addr, nil
	}

	// Port is busy, find an ephemeral port
	log.Printf("Port %s is busy, finding available port...", addr)

	// Parse host from original address
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = "localhost" // fallback
	}

	// Let OS choose ephemeral port by using :0
	listener, err = net.Listen("tcp", host+":0")
	if err != nil {
		return "", fmt.Errorf("failed to find available port: %w", err)
	}

	// Get the assigned port
	assignedAddr := listener.Addr().String()
	_ = listener.Close()

	// Extract port number
	_, port, err := net.SplitHostPort(assignedAddr)
	if err != nil {
		return "", fmt.Errorf("failed to parse assigned address: %w", err)
	}

	// Return host:port format
	finalAddr := net.JoinHostPort(host, port)
	return finalAddr, nil
}

// openBrowser opens the default browser to the given URL
func openBrowser(url string) {
	var err error

	switch runtime.GOOS {
	case "linux":
		err = exec.Command("xdg-open", url).Start()
	case "windows":
		err = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		err = exec.Command("open", url).Start()
	default:
		err = fmt.Errorf("unsupported platform")
	}

	if err != nil {
		log.Printf("Failed to open browser: %v", err)
		log.Printf("Please open manually: %s", url)
	}
}
