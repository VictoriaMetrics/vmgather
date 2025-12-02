package archive

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/VictoriaMetrics/support/internal/domain"
)

// TestNewWriter tests writer creation
func TestNewWriter(t *testing.T) {
	writer := NewWriter("/tmp/test")

	if writer == nil {
		t.Fatal("expected non-nil writer")
	}

	if writer.outputDir != "/tmp/test" {
		t.Errorf("outputDir = %v, want /tmp/test", writer.outputDir)
	}
}

// TestWriter_CreateArchive_Success tests successful archive creation
func TestWriter_CreateArchive_Success(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "vmgather-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	writer := NewWriter(tmpDir)

	// Sample metrics data
	metricsData := `{"metric":{"__name__":"vm_app_version"},"values":[1],"timestamps":[1699728000000]}
{"metric":{"__name__":"go_goroutines"},"values":[42],"timestamps":[1699728000000]}`

	metricsReader := strings.NewReader(metricsData)

	// Metadata
	metadata := ArchiveMetadata{
		ExportID:   "test-export-123",
		ExportDate: time.Now(),
		TimeRange: domain.TimeRange{
			Start: time.Now().Add(-1 * time.Hour),
			End:   time.Now(),
		},
		Components:        []string{"vmstorage", "vmselect"},
		Jobs:              []string{"vmstorage-prod", "vmselect-prod"},
		MetricsCount:      2,
		Obfuscated:        false,
		VMGatherVersion: "1.0.0",
	}

	// Create archive
	archivePath, sha256sum, err := writer.CreateArchive("test-export-123", metricsReader, metadata)
	if err != nil {
		t.Fatalf("CreateArchive failed: %v", err)
	}

	// Verify archive exists
	if _, err := os.Stat(archivePath); os.IsNotExist(err) {
		t.Errorf("archive file not created: %s", archivePath)
	}

	// Verify SHA256 is non-empty
	if sha256sum == "" {
		t.Error("SHA256 is empty")
	}

	// Verify archive name format
	if !strings.HasPrefix(filepath.Base(archivePath), "vmexport_test-export-123_") {
		t.Errorf("unexpected archive name: %s", filepath.Base(archivePath))
	}

	if !strings.HasSuffix(archivePath, ".zip") {
		t.Errorf("archive doesn't have .zip extension: %s", archivePath)
	}
}

// TestWriter_CreateArchive_ContainsRequiredFiles tests archive contents
func TestWriter_CreateArchive_ContainsRequiredFiles(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "vmgather-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	writer := NewWriter(tmpDir)

	metricsData := `{"metric":{"__name__":"test"},"values":[1],"timestamps":[1]}`
	metadata := ArchiveMetadata{
		ExportID:          "test",
		ExportDate:        time.Now(),
		TimeRange:         domain.TimeRange{Start: time.Now(), End: time.Now()},
		Components:        []string{"vmstorage"},
		Jobs:              []string{"vmstorage-prod"},
		MetricsCount:      1,
		VMGatherVersion: "1.0.0",
	}

	archivePath, _, err := writer.CreateArchive("test", strings.NewReader(metricsData), metadata)
	if err != nil {
		t.Fatalf("CreateArchive failed: %v", err)
	}

	// Open and verify ZIP contents
	zipReader, err := zip.OpenReader(archivePath)
	if err != nil {
		t.Fatalf("failed to open archive: %v", err)
	}
	defer zipReader.Close()

	// Check for required files
	requiredFiles := []string{"metrics.jsonl", "metadata.json", "README.txt"}
	foundFiles := make(map[string]bool)

	for _, file := range zipReader.File {
		foundFiles[file.Name] = true
	}

	for _, required := range requiredFiles {
		if !foundFiles[required] {
			t.Errorf("required file missing: %s", required)
		}
	}
}

// TestWriter_CreateArchive_MetricsContent tests metrics file content
func TestWriter_CreateArchive_MetricsContent(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "vmgather-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	writer := NewWriter(tmpDir)

	expectedMetrics := `{"metric":{"__name__":"vm_app_version"},"values":[1],"timestamps":[1699728000000]}`
	metadata := ArchiveMetadata{
		ExportID:          "test",
		ExportDate:        time.Now(),
		TimeRange:         domain.TimeRange{Start: time.Now(), End: time.Now()},
		Components:        []string{"vmstorage"},
		Jobs:              []string{"vmstorage-prod"},
		MetricsCount:      1,
		VMGatherVersion: "1.0.0",
	}

	archivePath, _, err := writer.CreateArchive("test", strings.NewReader(expectedMetrics), metadata)
	if err != nil {
		t.Fatalf("CreateArchive failed: %v", err)
	}

	// Read metrics from archive
	zipReader, err := zip.OpenReader(archivePath)
	if err != nil {
		t.Fatalf("failed to open archive: %v", err)
	}
	defer zipReader.Close()

	// Find metrics.jsonl
	var metricsFile *zip.File
	for _, file := range zipReader.File {
		if file.Name == "metrics.jsonl" {
			metricsFile = file
			break
		}
	}

	if metricsFile == nil {
		t.Fatal("metrics.jsonl not found in archive")
	}

	// Read content
	reader, err := metricsFile.Open()
	if err != nil {
		t.Fatalf("failed to open metrics file: %v", err)
	}
	defer reader.Close()

	content, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("failed to read metrics: %v", err)
	}

	if string(content) != expectedMetrics {
		t.Errorf("metrics content mismatch:\nwant: %s\ngot:  %s", expectedMetrics, string(content))
	}
}

// TestWriter_CreateArchive_MetadataContent tests metadata file content
func TestWriter_CreateArchive_MetadataContent(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "vmgather-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	writer := NewWriter(tmpDir)

	metricsData := `{"metric":{"__name__":"test"},"values":[1],"timestamps":[1]}`
	metadata := ArchiveMetadata{
		ExportID:          "test-export-456",
		ExportDate:        time.Now(),
		TimeRange:         domain.TimeRange{Start: time.Now(), End: time.Now()},
		Components:        []string{"vmstorage", "vmselect"},
		Jobs:              []string{"vmstorage-prod", "vmselect-prod"},
		MetricsCount:      100,
		Obfuscated:        true,
		InstanceMap:       map[string]string{"10.0.1.5:8482": "192.0.2.1:8482"},
		JobMap:            map[string]string{"vmstorage-prod": "vm_component_vmstorage_1"},
		VMGatherVersion: "1.0.0",
	}

	archivePath, _, err := writer.CreateArchive("test", strings.NewReader(metricsData), metadata)
	if err != nil {
		t.Fatalf("CreateArchive failed: %v", err)
	}

	// Read metadata from archive
	zipReader, err := zip.OpenReader(archivePath)
	if err != nil {
		t.Fatalf("failed to open archive: %v", err)
	}
	defer zipReader.Close()

	// Find metadata.json
	var metadataFile *zip.File
	for _, file := range zipReader.File {
		if file.Name == "metadata.json" {
			metadataFile = file
			break
		}
	}

	if metadataFile == nil {
		t.Fatal("metadata.json not found in archive")
	}

	// Read and parse
	reader, err := metadataFile.Open()
	if err != nil {
		t.Fatalf("failed to open metadata file: %v", err)
	}
	defer reader.Close()

	var parsedMetadata ArchiveMetadata
	if err := json.NewDecoder(reader).Decode(&parsedMetadata); err != nil {
		t.Fatalf("failed to parse metadata: %v", err)
	}

	// Verify key fields
	if parsedMetadata.ExportID != metadata.ExportID {
		t.Errorf("ExportID = %v, want %v", parsedMetadata.ExportID, metadata.ExportID)
	}

	if parsedMetadata.MetricsCount != metadata.MetricsCount {
		t.Errorf("MetricsCount = %v, want %v", parsedMetadata.MetricsCount, metadata.MetricsCount)
	}

	if !parsedMetadata.Obfuscated {
		t.Error("Obfuscated flag not set")
	}

	// Per issue #10: mapping should not be included in archive
	// Verify that InstanceMap and JobMap are NOT in the archive metadata
	if len(parsedMetadata.InstanceMap) != 0 {
		t.Errorf("InstanceMap should not be in archive, but found %d entries", len(parsedMetadata.InstanceMap))
	}

	if len(parsedMetadata.JobMap) != 0 {
		t.Errorf("JobMap should not be in archive, but found %d entries", len(parsedMetadata.JobMap))
	}
}

// TestWriter_CreateArchive_MappingExcluded tests that obfuscation maps are excluded from archive
// This test verifies the fix for issue #10
func TestWriter_CreateArchive_MappingExcluded(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "vmgather-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	writer := NewWriter(tmpDir)

	metricsData := `{"metric":{"__name__":"test"},"values":[1],"timestamps":[1]}`
	metadata := ArchiveMetadata{
		ExportID:          "test-mapping-excluded",
		ExportDate:        time.Now(),
		TimeRange:         domain.TimeRange{Start: time.Now(), End: time.Now()},
		Components:        []string{"vmstorage"},
		Jobs:              []string{"vmstorage-prod"},
		MetricsCount:      1,
		Obfuscated:        true,
		InstanceMap:       map[string]string{"10.0.1.5:8482": "777.777.1.1:8482", "10.0.1.6:8482": "777.777.1.2:8482"},
		JobMap:            map[string]string{"vmstorage-prod": "vm_component_vmstorage_1", "vmselect-prod": "vm_component_vmselect_1"},
		VMGatherVersion: "1.0.0",
	}

	archivePath, _, err := writer.CreateArchive("test", strings.NewReader(metricsData), metadata)
	if err != nil {
		t.Fatalf("CreateArchive failed: %v", err)
	}

	// Read metadata from archive
	zipReader, err := zip.OpenReader(archivePath)
	if err != nil {
		t.Fatalf("failed to open archive: %v", err)
	}
	defer zipReader.Close()

	// Find metadata.json
	var metadataFile *zip.File
	for _, file := range zipReader.File {
		if file.Name == "metadata.json" {
			metadataFile = file
			break
		}
	}

	if metadataFile == nil {
		t.Fatal("metadata.json not found in archive")
	}

	// Read and parse
	reader, err := metadataFile.Open()
	if err != nil {
		t.Fatalf("failed to open metadata file: %v", err)
	}
	defer reader.Close()

	// Parse as public metadata (without maps)
	var parsedMetadata struct {
		ExportID          string            `json:"export_id"`
		ExportDate        string            `json:"export_date"`
		TimeRange         domain.TimeRange   `json:"time_range"`
		Components        []string          `json:"components"`
		Jobs              []string          `json:"jobs"`
		MetricsCount      int               `json:"metrics_count"`
		Obfuscated        bool              `json:"obfuscated"`
		InstanceMap       map[string]string `json:"instance_map,omitempty"`
		JobMap            map[string]string `json:"job_map,omitempty"`
		VMGatherVersion string            `json:"vmgather_version"`
	}

	if err := json.NewDecoder(reader).Decode(&parsedMetadata); err != nil {
		t.Fatalf("failed to parse metadata: %v", err)
	}

	// Verify that obfuscation maps are NOT present in archive
	if parsedMetadata.InstanceMap != nil && len(parsedMetadata.InstanceMap) > 0 {
		t.Errorf("InstanceMap should not be in archive metadata, but found: %v", parsedMetadata.InstanceMap)
	}

	if parsedMetadata.JobMap != nil && len(parsedMetadata.JobMap) > 0 {
		t.Errorf("JobMap should not be in archive metadata, but found: %v", parsedMetadata.JobMap)
	}

	// Verify other fields are present
	if parsedMetadata.ExportID != metadata.ExportID {
		t.Errorf("ExportID = %v, want %v", parsedMetadata.ExportID, metadata.ExportID)
	}

	if !parsedMetadata.Obfuscated {
		t.Error("Obfuscated flag should be true")
	}

	if parsedMetadata.MetricsCount != metadata.MetricsCount {
		t.Errorf("MetricsCount = %v, want %v", parsedMetadata.MetricsCount, metadata.MetricsCount)
	}
}

// TestWriter_CreateArchive_ReadmeContent tests README generation
func TestWriter_CreateArchive_ReadmeContent(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "vmgather-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	writer := NewWriter(tmpDir)

	metricsData := `{"metric":{"__name__":"test"},"values":[1],"timestamps":[1]}`
	metadata := ArchiveMetadata{
		ExportID:          "test",
		ExportDate:        time.Now(),
		TimeRange:         domain.TimeRange{Start: time.Now(), End: time.Now()},
		Components:        []string{"vmstorage"},
		Jobs:              []string{"vmstorage-prod"},
		MetricsCount:      150,
		Obfuscated:        true,
		VMGatherVersion: "1.0.0",
	}

	archivePath, _, err := writer.CreateArchive("test", strings.NewReader(metricsData), metadata)
	if err != nil {
		t.Fatalf("CreateArchive failed: %v", err)
	}

	// Read README from archive
	zipReader, err := zip.OpenReader(archivePath)
	if err != nil {
		t.Fatalf("failed to open archive: %v", err)
	}
	defer zipReader.Close()

	// Find README.txt
	var readmeFile *zip.File
	for _, file := range zipReader.File {
		if file.Name == "README.txt" {
			readmeFile = file
			break
		}
	}

	if readmeFile == nil {
		t.Fatal("README.txt not found in archive")
	}

	// Read content
	reader, err := readmeFile.Open()
	if err != nil {
		t.Fatalf("failed to open README file: %v", err)
	}
	defer reader.Close()

	content, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("failed to read README: %v", err)
	}

	readme := string(content)

	// Verify key content
	if !strings.Contains(readme, "VictoriaMetrics Metrics Export") {
		t.Error("README missing title")
	}

	if !strings.Contains(readme, "test") {
		t.Error("README missing export ID")
	}

	if !strings.Contains(readme, "vmstorage") {
		t.Error("README missing component name")
	}

	if !strings.Contains(readme, "Total Metrics: 150") {
		t.Error("README missing metrics count")
	}

	if !strings.Contains(readme, "OBFUSCATION APPLIED") {
		t.Error("README missing obfuscation warning")
	}
}

// TestWriter_CalculateSHA256 tests SHA256 calculation
func TestWriter_CalculateSHA256(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "vmgather-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Create test file
	testFile := filepath.Join(tmpDir, "test.txt")
	testContent := []byte("test content")
	if err := os.WriteFile(testFile, testContent, 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	writer := NewWriter(tmpDir)

	// Calculate SHA256
	sha256sum, err := writer.calculateSHA256(testFile)
	if err != nil {
		t.Fatalf("calculateSHA256 failed: %v", err)
	}

	// Verify it's a valid hex string
	if len(sha256sum) != 64 {
		t.Errorf("SHA256 length = %d, want 64", len(sha256sum))
	}

	// Calculate twice - should be same
	sha256sum2, err := writer.calculateSHA256(testFile)
	if err != nil {
		t.Fatalf("calculateSHA256 failed on second call: %v", err)
	}

	if sha256sum != sha256sum2 {
		t.Error("SHA256 not deterministic")
	}
}

// TestWriter_GetArchiveSize tests size retrieval
func TestWriter_GetArchiveSize(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "vmgather-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	writer := NewWriter(tmpDir)

	metricsData := `{"metric":{"__name__":"test"},"values":[1],"timestamps":[1]}`
	metadata := ArchiveMetadata{
		ExportID:          "test",
		ExportDate:        time.Now(),
		TimeRange:         domain.TimeRange{Start: time.Now(), End: time.Now()},
		Components:        []string{"vmstorage"},
		Jobs:              []string{"vmstorage-prod"},
		MetricsCount:      1,
		VMGatherVersion: "1.0.0",
	}

	archivePath, _, err := writer.CreateArchive("test", strings.NewReader(metricsData), metadata)
	if err != nil {
		t.Fatalf("CreateArchive failed: %v", err)
	}

	// Get size
	size, err := writer.GetArchiveSize(archivePath)
	if err != nil {
		t.Fatalf("GetArchiveSize failed: %v", err)
	}

	// Size should be > 0
	if size <= 0 {
		t.Errorf("archive size = %d, want > 0", size)
	}

	// Should be reasonable size (not too large)
	if size > 10*1024*1024 { // 10MB
		t.Errorf("archive size = %d, unexpectedly large", size)
	}
}

// TestWriter_CreateArchive_CreatesOutputDir tests directory creation
func TestWriter_CreateArchive_CreatesOutputDir(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "vmgather-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Use non-existent subdirectory
	outputDir := filepath.Join(tmpDir, "nested", "output")
	writer := NewWriter(outputDir)

	metricsData := `{"metric":{"__name__":"test"},"values":[1],"timestamps":[1]}`
	metadata := ArchiveMetadata{
		ExportID:          "test",
		ExportDate:        time.Now(),
		TimeRange:         domain.TimeRange{Start: time.Now(), End: time.Now()},
		Components:        []string{"vmstorage"},
		Jobs:              []string{"vmstorage-prod"},
		MetricsCount:      1,
		VMGatherVersion: "1.0.0",
	}

	_, _, err = writer.CreateArchive("test", strings.NewReader(metricsData), metadata)
	if err != nil {
		t.Fatalf("CreateArchive failed: %v", err)
	}

	// Verify directory was created
	if _, err := os.Stat(outputDir); os.IsNotExist(err) {
		t.Error("output directory not created")
	}
}

// TestWriter_CreateArchive_EmptyMetrics tests handling of empty metrics
func TestWriter_CreateArchive_EmptyMetrics(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "vmgather-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	writer := NewWriter(tmpDir)

	// Empty metrics
	metricsData := bytes.NewReader([]byte{})
	metadata := ArchiveMetadata{
		ExportID:          "test",
		ExportDate:        time.Now(),
		TimeRange:         domain.TimeRange{Start: time.Now(), End: time.Now()},
		Components:        []string{"vmstorage"},
		Jobs:              []string{"vmstorage-prod"},
		MetricsCount:      0,
		VMGatherVersion: "1.0.0",
	}

	archivePath, _, err := writer.CreateArchive("test", metricsData, metadata)
	if err != nil {
		t.Fatalf("CreateArchive failed with empty metrics: %v", err)
	}

	// Archive should still be created
	if _, err := os.Stat(archivePath); os.IsNotExist(err) {
		t.Error("archive not created for empty metrics")
	}
}

