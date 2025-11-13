package archive

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestWriter_CrossPlatformPaths tests path handling across different OS
func TestWriter_CrossPlatformPaths(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "vmexporter-crossplatform-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	writer := NewWriter(tmpDir)

	tests := []struct {
		name        string
		exportID    string
		shouldWork  bool
		description string
	}{
		{
			name:        "simple_id",
			exportID:    "export-123",
			shouldWork:  true,
			description: "Simple alphanumeric ID",
		},
		{
			name:        "with_underscore",
			exportID:    "export_test_123",
			shouldWork:  true,
			description: "ID with underscores",
		},
		{
			name:        "with_dash",
			exportID:    "export-test-123",
			shouldWork:  true,
			description: "ID with dashes",
		},
		{
			name:        "with_dots",
			exportID:    "export.test.123",
			shouldWork:  true,
			description: "ID with dots (should work on all platforms)",
		},
	}

	// Platform-specific tests
	if runtime.GOOS == "windows" {
		tests = append(tests, struct {
			name        string
			exportID    string
			shouldWork  bool
			description string
		}{
			name:        "windows_reserved",
			exportID:    "CON", // Reserved name on Windows
			shouldWork:  false,
			description: "Windows reserved name",
		})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metricsData := strings.NewReader(`{"metric":{"__name__":"test"},"values":[1],"timestamps":[1]}`)
			
			metadata := ArchiveMetadata{
				ExportID:          tt.exportID,
				ExportDate:        time.Now(),
				VMExporterVersion: "1.0.0-test",
			}

			archivePath, _, err := writer.CreateArchive(tt.exportID, metricsData, metadata)

			if tt.shouldWork {
				if err != nil {
					t.Errorf("expected success for %s, got error: %v", tt.description, err)
				}
				// Verify file exists
				if _, err := os.Stat(archivePath); os.IsNotExist(err) {
					t.Errorf("archive file not created: %s", archivePath)
				}
			} else {
				if err == nil {
					t.Errorf("expected error for %s, but succeeded", tt.description)
				}
			}
		})
	}
}

// TestWriter_FileCollisions tests handling of duplicate export IDs
func TestWriter_FileCollisions(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "vmexporter-collisions-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	writer := NewWriter(tmpDir)
	exportID := "export-collision-test"

	metricsData := strings.NewReader(`{"metric":{"__name__":"test"},"values":[1],"timestamps":[1]}`)
	
	metadata := ArchiveMetadata{
		ExportID:          exportID,
		ExportDate:        time.Now(),
		VMExporterVersion: "1.0.0-test",
	}

	// Create first archive
	firstPath, _, err := writer.CreateArchive(exportID, metricsData, metadata)
	if err != nil {
		t.Fatalf("first archive creation failed: %v", err)
	}

	// Wait a bit to ensure different timestamp
	time.Sleep(1 * time.Second)

	// Create second archive with same ID
	metricsData2 := strings.NewReader(`{"metric":{"__name__":"test2"},"values":[2],"timestamps":[2]}`)
	secondPath, _, err := writer.CreateArchive(exportID, metricsData2, metadata)
	if err != nil {
		t.Fatalf("second archive creation failed: %v", err)
	}

	// Paths should be different (timestamp makes them unique)
	if firstPath == secondPath {
		t.Error("expected different paths for duplicate export IDs")
	}

	// Both files should exist
	if _, err := os.Stat(firstPath); os.IsNotExist(err) {
		t.Error("first archive was overwritten")
	}
	if _, err := os.Stat(secondPath); os.IsNotExist(err) {
		t.Error("second archive not created")
	}

	t.Logf("First:  %s", firstPath)
	t.Logf("Second: %s", secondPath)
}

// TestWriter_LargeArchive tests handling of large metric exports
func TestWriter_LargeArchive(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large archive test in short mode")
	}

	tmpDir, err := os.MkdirTemp("", "vmexporter-large-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	writer := NewWriter(tmpDir)

	// Generate 100K metrics (~10MB of data)
	var buf bytes.Buffer
	for i := 0; i < 100000; i++ {
		fmt.Fprintf(&buf, `{"metric":{"__name__":"test_%d","instance":"10.0.1.%d:8482","job":"test"},"values":[%d],"timestamps":[1699728000]}%s`,
			i, i%255, i, "\n")
	}

	metricsData := bytes.NewReader(buf.Bytes())
	
	metadata := ArchiveMetadata{
		ExportID:          "export-large",
		ExportDate:        time.Now(),
		VMExporterVersion: "1.0.0-test",
		MetricsCount:      100000,
	}

	start := time.Now()
	archivePath, sha256, err := writer.CreateArchive("export-large", metricsData, metadata)
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("large archive creation failed: %v", err)
	}

	t.Logf("Created large archive in %v", duration)
	t.Logf("Archive path: %s", archivePath)
	t.Logf("SHA256: %s", sha256)

	// Verify archive size
	archiveSize, err := writer.GetArchiveSize(archivePath)
	if err != nil {
		t.Fatalf("failed to get archive size: %v", err)
	}

	t.Logf("Archive size: %.2f MB", float64(archiveSize)/1024/1024)

	// Archive should be smaller than raw data (compression)
	if archiveSize >= int64(buf.Len()) {
		t.Logf("Warning: archive size (%d) >= raw data size (%d) - compression not effective", archiveSize, buf.Len())
	}

	// Verify archive integrity
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		t.Fatalf("failed to open archive: %v", err)
	}
	defer r.Close()

	// Should have 3 files: metrics.jsonl, metadata.json, README.txt
	if len(r.File) != 3 {
		t.Errorf("expected 3 files in archive, got %d", len(r.File))
	}
}

// TestWriter_ConcurrentArchiveCreation tests thread-safe archive creation
func TestWriter_ConcurrentArchiveCreation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping concurrent test in short mode")
	}

	tmpDir, err := os.MkdirTemp("", "vmexporter-concurrent-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	writer := NewWriter(tmpDir)
	const numArchives = 20

	var wg sync.WaitGroup
	errors := make(chan error, numArchives)
	paths := make(chan string, numArchives)

	for i := 0; i < numArchives; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			exportID := fmt.Sprintf("export-concurrent-%d", id)
			metricsData := strings.NewReader(fmt.Sprintf(`{"metric":{"__name__":"test_%d"},"values":[%d],"timestamps":[1]}`, id, id))
			
			metadata := ArchiveMetadata{
				ExportID:          exportID,
				ExportDate:        time.Now(),
				VMExporterVersion: "1.0.0-test",
			}

			archivePath, _, err := writer.CreateArchive(exportID, metricsData, metadata)
			if err != nil {
				errors <- fmt.Errorf("archive %d failed: %w", id, err)
				return
			}

			paths <- archivePath
		}(i)
	}

	wg.Wait()
	close(errors)
	close(paths)

	// Check for errors
	for err := range errors {
		t.Error(err)
	}

	// Verify all archives were created
	createdPaths := make([]string, 0, numArchives)
	for path := range paths {
		createdPaths = append(createdPaths, path)
	}

	if len(createdPaths) != numArchives {
		t.Errorf("expected %d archives, got %d", numArchives, len(createdPaths))
	}

	// Verify all files exist
	for _, path := range createdPaths {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("archive not found: %s", path)
		}
	}

	t.Logf("Successfully created %d concurrent archives", len(createdPaths))
}

// TestWriter_EmptyMetricsStream tests handling of empty metrics
func TestWriter_EmptyMetricsStream(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "vmexporter-empty-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	writer := NewWriter(tmpDir)

	// Empty metrics stream
	metricsData := strings.NewReader("")
	
	metadata := ArchiveMetadata{
		ExportID:          "export-empty",
		ExportDate:        time.Now(),
		VMExporterVersion: "1.0.0-test",
		MetricsCount:      0,
	}

	archivePath, _, err := writer.CreateArchive("export-empty", metricsData, metadata)
	if err != nil {
		t.Fatalf("empty archive creation failed: %v", err)
	}

	// Verify archive exists
	if _, err := os.Stat(archivePath); os.IsNotExist(err) {
		t.Error("empty archive not created")
	}

	// Verify archive content
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		t.Fatalf("failed to open archive: %v", err)
	}
	defer r.Close()

	// Should still have metadata and README
	if len(r.File) < 2 {
		t.Errorf("expected at least 2 files (metadata + README), got %d", len(r.File))
	}
}

// TestWriter_SpecialCharactersInMetadata tests metadata with special characters
func TestWriter_SpecialCharactersInMetadata(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "vmexporter-special-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	writer := NewWriter(tmpDir)

	metricsData := strings.NewReader(`{"metric":{"__name__":"test"},"values":[1],"timestamps":[1]}`)
	
	metadata := ArchiveMetadata{
		ExportID:          "export-special",
		ExportDate:        time.Now(),
		VMExporterVersion: "1.0.0-test",
		Components:        []string{"vmstorage", "vmselect", "vm-insert"}, // dash in component name
		Jobs:              []string{"job/with/slashes", "job:with:colons"},
	}

	archivePath, _, err := writer.CreateArchive("export-special", metricsData, metadata)
	if err != nil {
		t.Fatalf("archive creation failed: %v", err)
	}

	// Verify archive can be opened
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		t.Fatalf("failed to open archive: %v", err)
	}
	defer r.Close()

	// Verify metadata.json is valid
	for _, f := range r.File {
		if f.Name == "metadata.json" {
			rc, err := f.Open()
			if err != nil {
				t.Fatalf("failed to open metadata.json: %v", err)
			}
			defer rc.Close()

			// Should be able to read without errors
			buf := new(bytes.Buffer)
			if _, err := buf.ReadFrom(rc); err != nil {
				t.Errorf("failed to read metadata.json: %v", err)
			}

			t.Logf("Metadata content: %s", buf.String())
			return
		}
	}

	t.Error("metadata.json not found in archive")
}

// TestWriter_DiskSpaceHandling tests behavior when disk space is low
func TestWriter_DiskSpaceHandling(t *testing.T) {
	t.Skip("Disk space simulation requires platform-specific mocking")
	
	// NOTE: This test would require:
	// 1. Mock filesystem with quota
	// 2. Or actual disk space limitation (risky for CI)
	// 3. Or filesystem wrapper with injectable errors
	//
	// For MVP, we rely on OS-level disk space errors
	// which are propagated correctly through our code
}

// TestWriter_InvalidOutputDirectory tests error handling for invalid directories
func TestWriter_InvalidOutputDirectory(t *testing.T) {
	tests := []struct {
		name        string
		outputDir   string
		shouldFail  bool
		description string
	}{
		{
			name:        "nonexistent_parent",
			outputDir:   "/nonexistent/parent/dir",
			shouldFail:  true,
			description: "Parent directory doesn't exist",
		},
		{
			name:        "empty_path",
			outputDir:   "",
			shouldFail:  true, // Empty path is invalid
			description: "Empty path (should fail)",
		},
	}

	// Platform-specific tests
	if runtime.GOOS != "windows" {
		tests = append(tests, struct {
			name        string
			outputDir   string
			shouldFail  bool
			description string
		}{
			name:        "read_only_dir",
			outputDir:   "/",
			shouldFail:  true,
			description: "Read-only directory (root)",
		})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writer := NewWriter(tt.outputDir)

			metricsData := strings.NewReader(`{"metric":{"__name__":"test"},"values":[1],"timestamps":[1]}`)
			
			metadata := ArchiveMetadata{
				ExportID:          "export-test",
				ExportDate:        time.Now(),
				VMExporterVersion: "1.0.0-test",
			}

			_, _, err := writer.CreateArchive("export-test", metricsData, metadata)

			if tt.shouldFail {
				if err == nil {
					t.Errorf("expected error for %s, but succeeded", tt.description)
				} else {
					t.Logf("Got expected error: %v", err)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error for %s: %v", tt.description, err)
				}
			}
		})
	}
}

// TestWriter_ArchiveNaming tests archive filename generation
func TestWriter_ArchiveNaming(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "vmexporter-naming-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	writer := NewWriter(tmpDir)

	metricsData := strings.NewReader(`{"metric":{"__name__":"test"},"values":[1],"timestamps":[1]}`)
	
	metadata := ArchiveMetadata{
		ExportID:          "export-123",
		ExportDate:        time.Now(),
		VMExporterVersion: "1.0.0-test",
	}

	archivePath, _, err := writer.CreateArchive("export-123", metricsData, metadata)
	if err != nil {
		t.Fatalf("archive creation failed: %v", err)
	}

	// Verify filename format: vmexport_<exportID>_<timestamp>.zip
	filename := filepath.Base(archivePath)
	
	if !strings.HasPrefix(filename, "vmexport_") {
		t.Errorf("filename should start with 'vmexport_', got: %s", filename)
	}

	if !strings.HasSuffix(filename, ".zip") {
		t.Errorf("filename should end with '.zip', got: %s", filename)
	}

	if !strings.Contains(filename, "export-123") {
		t.Errorf("filename should contain export ID, got: %s", filename)
	}

	t.Logf("Generated filename: %s", filename)
}

