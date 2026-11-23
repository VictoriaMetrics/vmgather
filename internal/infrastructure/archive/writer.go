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
	// Keep archive filename safe across all platforms (not only on the current runtime OS).
	// Windows forbids these characters in filenames.
	if strings.ContainsAny(exportID, `<>:"|?*`) {
		return fmt.Errorf("export ID contains invalid filename characters")
	}
	for _, r := range exportID {
		if r < 32 || r == 127 {
			return fmt.Errorf("export ID contains control characters")
		}
	}
	return nil
}

// ArchiveMetadata is written to metadata.json in the archive.
type ArchiveMetadata struct {
	ExportID             string             `json:"export_id"`
	ExportDate           time.Time          `json:"export_date"`
	TimeRange            domain.TimeRange   `json:"time_range"`
	Components           []string           `json:"components"`
	Jobs                 []string           `json:"jobs"`
	MetricsCount         int                `json:"metrics_count"`
	Obfuscated           bool               `json:"obfuscated"`
	MDXFiltered          bool               `json:"mdx_filtered,omitempty"`
	AdaptiveMode         string             `json:"adaptive_mode,omitempty"`
	MetricStepSeconds    int                `json:"metric_step_seconds,omitempty"`
	MaxMetricStepSeconds int                `json:"max_metric_step_seconds,omitempty"`
	Sampled              bool               `json:"sampled,omitempty"`
	AdaptiveDecisions    []AdaptiveDecision `json:"adaptive_decisions,omitempty"`
	VMGatherVersion      string             `json:"vmgather_version"`
}

type AdaptiveDecision struct {
	Strategy    string           `json:"strategy"`
	ErrorKind   string           `json:"error_kind,omitempty"`
	TimeRange   domain.TimeRange `json:"time_range"`
	StepSeconds int              `json:"step_seconds,omitempty"`
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
func (w *Writer) addMetadataToArchive(zipWriter *zip.Writer, metadata ArchiveMetadata) error {
	writer, err := zipWriter.Create("metadata.json")
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(metadata)
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
		readme += "Values of the selected labels are replaced with their SHA-256 hex digests,\n"
		readme += "the same as vmagent -remoteWrite.obfuscateLabels.\n"
	}
	if metadata.MDXFiltered {
		readme += "\nOnly MDX series are included (the same filter as vmagent -remoteWrite.mdx.enable).\n"
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
