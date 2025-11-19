package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	importer "github.com/VictoriaMetrics/support/internal/importer/server"
)

const version = "0.1.0"

func main() {
	addr := flag.String("addr", "0.0.0.0:8081", "HTTP server address")
	noBrowser := flag.Bool("no-browser", false, "Do not open browser on start")
	flag.Parse()

	srv := importer.NewServer(version)
	httpServer := &http.Server{Addr: *addr, Handler: srv.Router()}

	go func() {
		log.Printf("VMImport %s listening on http://%s", version, *addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	if !*noBrowser {
		time.Sleep(500 * time.Millisecond)
		openBrowser(fmt.Sprintf("http://%s", *addr))
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
