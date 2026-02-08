package archive

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/VictoriaMetrics/vmgather/internal/domain"
)

// Writer handles archive creation for export data
type Writer struct {
	outputDir string
}

func (w *Writer) OutputDir() string {
	return w.outputDir
}

// NewWriter creates a new archive writer
func NewWriter(outputDir string) *Writer {
	return &Writer{
		outputDir: outputDir,
	}
}

func validateExportID(exportID string) error {
	if strings.TrimSpace(exportID) == "" {
		return fmt.Errorf("export ID cannot be empty")
	}
	// Prevent path traversal / accidental subdirs via export ID.
	if strings.ContainsAny(exportID, `/\`) {
		return fmt.Errorf("export ID must not contain path separators")
	}
	if runtime.GOOS == "windows" && isWindowsReservedName(exportID) {
		return fmt.Errorf("export ID is a reserved name on Windows")
	}
	return nil
}

func isWindowsReservedName(name string) bool {
	// Windows treats these names as reserved even with extensions, and ignores
	// trailing dots/spaces in path components.
	trimmed := strings.TrimRight(name, ". ")
	upper := strings.ToUpper(trimmed)
	switch upper {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(upper) == 4 {
		prefix := upper[:3]
		suffix := upper[3]
		if (prefix == "COM" || prefix == "LPT") && suffix >= '1' && suffix <= '9' {
			return true
		}
	}
	return false
}

// ArchiveMetadata contains metadata about the export
// Note: InstanceMap and JobMap are intentionally excluded from archive metadata
// per issue #10 - mapping should not be included in the archive sent to customers
type ArchiveMetadata struct {
	ExportID        string            `json:"export_id"`
	ExportDate      time.Time         `json:"export_date"`
	TimeRange       domain.TimeRange  `json:"time_range"`
	Components      []string          `json:"components"`
	Jobs            []string          `json:"jobs"`
	MetricsCount    int               `json:"metrics_count"`
	Obfuscated      bool              `json:"obfuscated"`
	InstanceMap     map[string]string `json:"instance_map,omitempty"` // Internal use only, not included in archive
	JobMap          map[string]string `json:"job_map,omitempty"`      // Internal use only, not included in archive
	VMGatherVersion string            `json:"vmgather_version"`
}

// archiveMetadataPublic is the public version of metadata without obfuscation maps
// This is what gets included in the archive sent to customers
type archiveMetadataPublic struct {
	ExportID        string           `json:"export_id"`
	ExportDate      time.Time        `json:"export_date"`
	TimeRange       domain.TimeRange `json:"time_range"`
	Components      []string         `json:"components"`
	Jobs            []string         `json:"jobs"`
	MetricsCount    int              `json:"metrics_count"`
	Obfuscated      bool             `json:"obfuscated"`
	VMGatherVersion string           `json:"vmgather_version"`
}

// CreateArchive creates a ZIP archive with metrics data
// Returns archive path, SHA256 checksum, and error
func (w *Writer) CreateArchive(
	exportID string,
	metricsReader io.Reader,
	metadata ArchiveMetadata,
) (archivePath string, sha256sum string, err error) {
	if err := validateExportID(exportID); err != nil {
		return "", "", err
	}

	// Generate archive filename
	timestamp := time.Now().Format("20060102_150405")
	archiveName := fmt.Sprintf("vmexport_%s_%s.zip", exportID, timestamp)
	archivePath = filepath.Join(w.outputDir, archiveName)

	// Create output directory if not exists
	if err := os.MkdirAll(w.outputDir, 0755); err != nil {
		return "", "", fmt.Errorf("failed to create output directory: %w", err)
	}

	// Create archive file
	archiveFile, err := os.Create(archivePath)
	if err != nil {
		return "", "", fmt.Errorf("failed to create archive file: %w", err)
	}
	defer func() { _ = archiveFile.Close() }()

	// Create ZIP writer
	zipWriter := zip.NewWriter(archiveFile)
	defer func() { _ = zipWriter.Close() }()

	// Add metrics data
	if err := w.addMetricsToArchive(zipWriter, metricsReader); err != nil {
		return "", "", fmt.Errorf("failed to add metrics: %w", err)
	}

	// Add metadata
	if err := w.addMetadataToArchive(zipWriter, metadata); err != nil {
		return "", "", fmt.Errorf("failed to add metadata: %w", err)
	}

	// Add README
	if err := w.addReadmeToArchive(zipWriter, metadata); err != nil {
		return "", "", fmt.Errorf("failed to add README: %w", err)
	}

	// Close ZIP writer to flush all data
	if err := zipWriter.Close(); err != nil {
		return "", "", fmt.Errorf("failed to close zip writer: %w", err)
	}

	// Calculate SHA256
	sha256sum, err = w.calculateSHA256(archivePath)
	if err != nil {
		return "", "", fmt.Errorf("failed to calculate SHA256: %w", err)
	}

	return archivePath, sha256sum, nil
}

// addMetricsToArchive adds metrics JSONL data to archive
func (w *Writer) addMetricsToArchive(zipWriter *zip.Writer, metricsReader io.Reader) error {
	writer, err := zipWriter.Create("metrics.jsonl")
	if err != nil {
		return err
	}

	_, err = io.Copy(writer, metricsReader)
	return err
}

// addMetadataToArchive adds metadata JSON to archive
// Note: Obfuscation maps (InstanceMap, JobMap) are excluded from archive per issue #10
func (w *Writer) addMetadataToArchive(zipWriter *zip.Writer, metadata ArchiveMetadata) error {
	writer, err := zipWriter.Create("metadata.json")
	if err != nil {
		return err
	}

	// Create public metadata without obfuscation maps
	publicMetadata := archiveMetadataPublic{
		ExportID:        metadata.ExportID,
		ExportDate:      metadata.ExportDate,
		TimeRange:       metadata.TimeRange,
		Components:      metadata.Components,
		Jobs:            metadata.Jobs,
		MetricsCount:    metadata.MetricsCount,
		Obfuscated:      metadata.Obfuscated,
		VMGatherVersion: metadata.VMGatherVersion,
	}

	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(publicMetadata)
}

// addReadmeToArchive adds human-readable README to archive
func (w *Writer) addReadmeToArchive(zipWriter *zip.Writer, metadata ArchiveMetadata) error {
	writer, err := zipWriter.Create("README.txt")
	if err != nil {
		return err
	}

	readme := w.generateReadme(metadata)
	_, err = writer.Write([]byte(readme))
	return err
}

// generateReadme generates human-readable README content
func (w *Writer) generateReadme(metadata ArchiveMetadata) string {
	readme := fmt.Sprintf(`VictoriaMetrics Metrics Export
================================

Export ID: %s
Export Date: %s
Time Range: %s to %s

Components Exported:
`, metadata.ExportID, metadata.ExportDate.Format(time.RFC3339),
		metadata.TimeRange.Start.Format(time.RFC3339),
		metadata.TimeRange.End.Format(time.RFC3339))

	for _, comp := range metadata.Components {
		readme += fmt.Sprintf("  - %s\n", comp)
	}

	readme += fmt.Sprintf("\nTotal Metrics: %d\n", metadata.MetricsCount)

	if metadata.Obfuscated {
		readme += "\n[WARN] OBFUSCATION APPLIED\n"
		readme += "Instance IPs and job names have been obfuscated for privacy.\n"
	}

	readme += "\nFiles in this archive:\n"
	readme += "  - metrics.jsonl: Exported metrics in JSONL format\n"
	readme += "  - metadata.json: Export metadata\n"
	readme += "  - README.txt: This file\n"

	readme += "\nFor support inquiries, send this archive to VictoriaMetrics Support Team.\n"
	readme += fmt.Sprintf("Generated by vmgather v%s\n", metadata.VMGatherVersion)

	return readme
}

// calculateSHA256 calculates SHA256 checksum of a file
func (w *Writer) calculateSHA256(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

// GetArchiveSize returns the size of an archive file in bytes
func (w *Writer) GetArchiveSize(archivePath string) (int64, error) {
	info, err := os.Stat(archivePath)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}
