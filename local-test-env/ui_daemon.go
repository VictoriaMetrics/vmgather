package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	uiDaemonLabel      = "com.victoriametrics.vmgather"
	uiDaemonLogOutName = "vmgather.launchd.log"
	uiDaemonLogErrName = "vmgather.launchd.err.log"
)

func runUIDaemon(args []string) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("ui-daemon is supported on macOS only (runtime=%s)", runtime.GOOS)
	}

	action := "install"
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		action = strings.ToLower(strings.TrimSpace(args[0]))
	}

	repoRoot, err := findRepoRoot()
	if err != nil {
		return err
	}

	addr := getEnvOrDefault("VMGATHER_UI_ADDR", "localhost:8080")
	host, port, err := splitHostPortOrDefaultHost(addr, "localhost")
	if err != nil {
		return fmt.Errorf("invalid VMGATHER_UI_ADDR=%q: %w", addr, err)
	}

	plistPath, err := uiDaemonPlistPath()
	if err != nil {
		return err
	}

	switch action {
	case "install", "up":
		binPath := filepath.Join(repoRoot, "vmgather")
		if err := ensureVMGatherBinary(repoRoot, binPath); err != nil {
			return err
		}

		if err := writeUIDaemonPlist(repoRoot, binPath, addr, plistPath); err != nil {
			return err
		}

		if err := launchctlBootout(plistPath); err != nil {
			return err
		}
		// Now that we stopped a potentially running daemon, validate that nothing else is using the port.
		if !portAvailable(host, port) {
			return fmt.Errorf("refusing to start daemon: %s is already in use", net.JoinHostPort(host, strconv.Itoa(port)))
		}
		if err := launchctlBootstrap(plistPath); err != nil {
			return err
		}
		_ = launchctlEnable()
		_ = launchctlKickstart()

		if err := waitHTTPUp(host, port, 5*time.Second); err != nil {
			return fmt.Errorf("daemon installed but HTTP check failed: %w; see %s and %s", err,
				filepath.Join(repoRoot, uiDaemonLogErrName), filepath.Join(repoRoot, uiDaemonLogOutName))
		}

		fmt.Printf("[OK] vmgather UI daemon is running at http://%s\n", addr)
		fmt.Printf("[INFO] launchd label: %s\n", uiDaemonLabel)
		fmt.Printf("[INFO] logs: %s (stderr), %s (stdout)\n",
			filepath.Join(repoRoot, uiDaemonLogErrName), filepath.Join(repoRoot, uiDaemonLogOutName))
		return nil

	case "uninstall", "down":
		_ = launchctlBootout(plistPath)
		_ = os.Remove(plistPath)
		fmt.Println("[OK] vmgather UI daemon removed")
		return nil

	case "restart":
		if err := launchctlKickstart(); err != nil {
			return err
		}
		fmt.Println("[OK] vmgather UI daemon restarted")
		return nil

	case "status":
		return launchctlPrint()

	case "logs":
		return tailUIDaemonLogs(repoRoot, 200)

	default:
		return fmt.Errorf("unknown ui-daemon action %q; expected install|uninstall|status|restart|logs", action)
	}
}

func findRepoRoot() (string, error) {
	// Prefer walking from the executable location (stable even if cwd changes).
	if exe, err := os.Executable(); err == nil {
		if root := walkUpForRepo(filepath.Dir(exe)); root != "" {
			return root, nil
		}
	}
	// Fallback to walking from cwd.
	if wd, err := os.Getwd(); err == nil {
		if root := walkUpForRepo(wd); root != "" {
			return root, nil
		}
	}
	return "", errors.New("cannot locate repo root (expected go.mod and cmd/vmgather/main.go)")
}

func walkUpForRepo(startDir string) string {
	dir := startDir
	for i := 0; i < 8; i++ {
		if fileExists(filepath.Join(dir, "go.mod")) && fileExists(filepath.Join(dir, "cmd", "vmgather", "main.go")) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func splitHostPortOrDefaultHost(addr string, defaultHost string) (string, int, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		// If user passed ":8080", treat it as localhost.
		if strings.HasPrefix(addr, ":") {
			host = defaultHost
			portStr = strings.TrimPrefix(addr, ":")
		} else {
			return "", 0, err
		}
	}
	if strings.TrimSpace(host) == "" {
		host = defaultHost
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", 0, err
	}
	if port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("port out of range: %d", port)
	}
	return host, port, nil
}

func ensureVMGatherBinary(repoRoot, binPath string) error {
	if st, err := os.Stat(binPath); err == nil && (st.Mode()&0o111) != 0 {
		return nil
	}
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/vmgather")
	cmd.Dir = repoRoot
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to build vmgather binary: %w", err)
	}
	return nil
}

func uiDaemonPlistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", uiDaemonLabel+".plist"), nil
}

func writeUIDaemonPlist(repoRoot, binPath, addr, plistPath string) error {
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return err
	}
	outPath := filepath.Join(repoRoot, uiDaemonLogOutName)
	errPath := filepath.Join(repoRoot, uiDaemonLogErrName)

	// Keep the plist minimal and explicit.
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>%s</string>

  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>-no-browser</string>
    <string>-addr</string>
    <string>%s</string>
  </array>

  <key>WorkingDirectory</key>
  <string>%s</string>

  <key>RunAtLoad</key>
  <true/>

  <key>KeepAlive</key>
  <true/>

  <key>ThrottleInterval</key>
  <integer>5</integer>

  <key>StandardOutPath</key>
  <string>%s</string>
  <key>StandardErrorPath</key>
  <string>%s</string>
</dict>
</plist>
`, uiDaemonLabel, binPath, addr, repoRoot, outPath, errPath)

	return os.WriteFile(plistPath, []byte(plist), 0o644)
}

func launchctlDomain() string {
	return fmt.Sprintf("gui/%d", os.Getuid())
}

func launchctlBootout(plistPath string) error {
	cmd := exec.Command("launchctl", "bootout", launchctlDomain(), plistPath)
	// bootout may fail if the job isn't loaded; that is OK.
	_ = cmd.Run()
	return nil
}

func launchctlBootstrap(plistPath string) error {
	cmd := exec.Command("launchctl", "bootstrap", launchctlDomain(), plistPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func launchctlEnable() error {
	cmd := exec.Command("launchctl", "enable", launchctlDomain()+"/"+uiDaemonLabel)
	return cmd.Run()
}

func launchctlKickstart() error {
	cmd := exec.Command("launchctl", "kickstart", "-k", launchctlDomain()+"/"+uiDaemonLabel)
	return cmd.Run()
}

func launchctlPrint() error {
	cmd := exec.Command("launchctl", "print", launchctlDomain()+"/"+uiDaemonLabel)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func tailUIDaemonLogs(repoRoot string, lines int) error {
	stderrPath := filepath.Join(repoRoot, uiDaemonLogErrName)
	stdoutPath := filepath.Join(repoRoot, uiDaemonLogOutName)
	if lines <= 0 {
		lines = 200
	}

	fmt.Printf("==> %s <==\n", stderrPath)
	_ = tailFile(stderrPath, lines, os.Stdout)
	fmt.Println()

	fmt.Printf("==> %s <==\n", stdoutPath)
	_ = tailFile(stdoutPath, lines, os.Stdout)
	return nil
}

func tailFile(path string, lines int, w io.Writer) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// Small, simple tail: read up to the last ~512KB and split.
	const maxRead = 512 * 1024
	st, err := f.Stat()
	if err != nil {
		return err
	}
	var start int64
	if st.Size() > maxRead {
		start = st.Size() - maxRead
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return err
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	s := string(b)
	parts := strings.Split(s, "\n")
	if len(parts) > lines+1 {
		parts = parts[len(parts)-(lines+1):]
	}
	for _, line := range parts {
		fmt.Fprintln(w, line)
	}
	return nil
}

func waitHTTPUp(host string, port int, timeout time.Duration) error {
	// For local daemon usage, force IPv4 loopback if user asked for localhost-ish.
	checkHost := host
	if host == "" || strings.EqualFold(host, "localhost") || host == "127.0.0.1" || host == "::1" {
		checkHost = "127.0.0.1"
	}

	deadline := time.Now().Add(timeout)
	url := fmt.Sprintf("http://%s:%d/", checkHost, port)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	for time.Now().Before(deadline) {
		resp, err := client.Get(url) //nolint:gosec // local loopback check
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 500 {
				return nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for %s", url)
}
