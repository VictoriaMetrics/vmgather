package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	binaryName = "vmexporter"
	distDir    = "dist"
)

var version = getVersion()

// Platform represents a target build platform
type Platform struct {
	GOOS   string
	GOARCH string
	Ext    string // .exe for windows, empty for others
	Alias  string // User-friendly name (optional)
}

// BuildResult contains build output information
type BuildResult struct {
	Platform   Platform
	OutputPath string
	Size       int64
	SHA256     string
	BuildTime  time.Duration
	Error      error
}

var platforms = []Platform{
	// Linux
	{GOOS: "linux", GOARCH: "amd64", Ext: ""},
	{GOOS: "linux", GOARCH: "arm64", Ext: ""},
	{GOOS: "linux", GOARCH: "386", Ext: ""},
	
	// macOS
	{GOOS: "darwin", GOARCH: "amd64", Ext: "", Alias: "macos-intel"},
	{GOOS: "darwin", GOARCH: "arm64", Ext: "", Alias: "macos-apple-silicon"},
	
	// Windows
	{GOOS: "windows", GOARCH: "amd64", Ext: ".exe"},
	{GOOS: "windows", GOARCH: "arm64", Ext: ".exe"},
	{GOOS: "windows", GOARCH: "386", Ext: ".exe"},
}

func main() {
	fmt.Printf("🚀 VMExporter Build System v%s\n", version)
	fmt.Printf("📦 Building for %d platforms...\n\n", len(platforms))

	// Create dist directory
	if err := os.RemoveAll(distDir); err != nil && !os.IsNotExist(err) {
		fatal("Failed to clean dist directory: %v", err)
	}
	if err := os.MkdirAll(distDir, 0755); err != nil {
		fatal("Failed to create dist directory: %v", err)
	}

	// Build for all platforms
	results := make([]BuildResult, 0, len(platforms))
	successCount := 0

	for _, platform := range platforms {
		result := buildPlatform(platform)
		results = append(results, result)

		if result.Error == nil {
			successCount++
			fmt.Printf("✅ %s/%s: %s (%.2f MB, %s)\n",
				platform.GOOS,
				platform.GOARCH,
				result.OutputPath,
				float64(result.Size)/1024/1024,
				result.BuildTime.Round(time.Millisecond),
			)
		} else {
			fmt.Printf("❌ %s/%s: %v\n", platform.GOOS, platform.GOARCH, result.Error)
		}
	}

	// Generate checksums file
	if err := generateChecksums(results); err != nil {
		fatal("Failed to generate checksums: %v", err)
	}

	// Print summary
	fmt.Printf("\n📊 Build Summary:\n")
	fmt.Printf("   Success: %d/%d\n", successCount, len(platforms))
	fmt.Printf("   Output:  %s/\n", distDir)
	fmt.Printf("   Files:   %s\n", strings.Join(listDistFiles(), ", "))

	if successCount < len(platforms) {
		os.Exit(1)
	}
}

// buildPlatform builds binary for specified platform
func buildPlatform(platform Platform) BuildResult {
	start := time.Now()

	// Generate output filename
	filename := fmt.Sprintf("%s-v%s-%s-%s%s",
		binaryName,
		version,
		platform.GOOS,
		platform.GOARCH,
		platform.Ext,
	)
	outputPath := filepath.Join(distDir, filename)

	// Prepare build command
	cmd := exec.Command("go", "build",
		"-o", outputPath,
		"-ldflags", fmt.Sprintf("-s -w -X main.version=%s", version),
		"./cmd/vmexporter",
	)

	// Set environment for cross-compilation
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env,
		fmt.Sprintf("GOOS=%s", platform.GOOS),
		fmt.Sprintf("GOARCH=%s", platform.GOARCH),
		"CGO_ENABLED=0", // Disable CGO for static binaries
	)

	// Execute build
	output, err := cmd.CombinedOutput()
	if err != nil {
		return BuildResult{
			Platform:  platform,
			Error:     fmt.Errorf("build failed: %w\n%s", err, string(output)),
			BuildTime: time.Since(start),
		}
	}

	// Get file info
	info, err := os.Stat(outputPath)
	if err != nil {
		return BuildResult{
			Platform:  platform,
			Error:     fmt.Errorf("stat failed: %w", err),
			BuildTime: time.Since(start),
		}
	}

	// Calculate SHA256
	hash, err := calculateSHA256(outputPath)
	if err != nil {
		return BuildResult{
			Platform:   platform,
			OutputPath: outputPath,
			Size:       info.Size(),
			Error:      fmt.Errorf("hash failed: %w", err),
			BuildTime:  time.Since(start),
		}
	}

	// Create alias copy if specified (e.g., macos-apple-silicon)
	if platform.Alias != "" {
		aliasFilename := fmt.Sprintf("%s-v%s-%s%s",
			binaryName,
			version,
			platform.Alias,
			platform.Ext,
		)
		aliasPath := filepath.Join(distDir, aliasFilename)
		
		// Copy file
		if err := copyFile(outputPath, aliasPath); err == nil {
			fmt.Printf("   📋 Created alias: %s\n", aliasFilename)
		}
	}

	return BuildResult{
		Platform:   platform,
		OutputPath: outputPath,
		Size:       info.Size(),
		SHA256:     hash,
		BuildTime:  time.Since(start),
	}
}

// calculateSHA256 calculates SHA256 hash of a file
func calculateSHA256(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

// generateChecksums generates checksums.txt file
func generateChecksums(results []BuildResult) error {
	checksumPath := filepath.Join(distDir, "checksums.txt")
	file, err := os.Create(checksumPath)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	_, _ = fmt.Fprintf(file, "# VMExporter v%s - SHA256 Checksums\n", version)
	_, _ = fmt.Fprintf(file, "# Generated: %s\n\n", time.Now().Format(time.RFC3339))

	for _, result := range results {
		if result.Error == nil {
			filename := filepath.Base(result.OutputPath)
			_, _ = fmt.Fprintf(file, "%s  %s\n", result.SHA256, filename)
		}
	}

	fmt.Printf("✅ Generated checksums.txt\n")
	return nil
}

// listDistFiles lists all files in dist directory
func listDistFiles() []string {
	entries, err := os.ReadDir(distDir)
	if err != nil {
		return []string{}
	}

	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			files = append(files, entry.Name())
		}
	}
	return files
}

// fatal prints error and exits
func fatal(format string, args ...interface{}) {
	_, _ = fmt.Fprintf(os.Stderr, "❌ ERROR: "+format+"\n", args...)
	os.Exit(1)
}

// GetCurrentPlatform returns current OS/ARCH as string
func GetCurrentPlatform() string {
	return fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)
}

// getVersion returns version from VERSION env var or default
func getVersion() string {
	if v := os.Getenv("VERSION"); v != "" {
		return strings.TrimPrefix(v, "v")
	}
	return "1.0.0"
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = sourceFile.Close() }()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() { _ = destFile.Close() }()

	_, err = io.Copy(destFile, sourceFile)
	return err
}

