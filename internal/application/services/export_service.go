package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/VictoriaMetrics/support/internal/domain"
	"github.com/VictoriaMetrics/support/internal/infrastructure/archive"
	"github.com/VictoriaMetrics/support/internal/infrastructure/obfuscation"
	"github.com/VictoriaMetrics/support/internal/infrastructure/vm"
)

// ExportService interface for full export operations
type ExportService interface {
	// ExecuteExport performs full export with optional obfuscation
	ExecuteExport(ctx context.Context, config domain.ExportConfig) (*domain.ExportResult, error)
}

// exportServiceImpl implements ExportService
type exportServiceImpl struct {
	clientFactory  func(domain.VMConnection) *vm.Client
	archiveWriter  *archive.Writer
	vmExporterVersion string
}

// NewExportService creates a new export service
func NewExportService(outputDir string) ExportService {
	return &exportServiceImpl{
		clientFactory:  vm.NewClient,
		archiveWriter:  archive.NewWriter(outputDir),
		vmExporterVersion: "1.0.0",
	}
}

// ExecuteExport performs full metrics export with optional obfuscation
func (s *exportServiceImpl) ExecuteExport(ctx context.Context, config domain.ExportConfig) (*domain.ExportResult, error) {
	// Generate export ID
	exportID := s.generateExportID()

	// Step 1: Export metrics from VictoriaMetrics
	client := s.clientFactory(config.Connection)
	selector := s.buildSelector(config.Jobs)

	// Try direct export first
	fmt.Printf("📤 Attempting direct export via /api/v1/export...\n")
	exportReader, err := client.Export(ctx, selector, config.TimeRange.Start, config.TimeRange.End)
	
	// Check if export failed due to missing route (export API not available)
	if err != nil && s.isMissingRouteError(err) {
		fmt.Printf("⚠️  Export API not available (error: %v)\n", err)
		fmt.Println("⚠️  Falling back to query_range method")
		
		// Fallback to query_range
		exportReader, err = s.exportViaQueryRange(ctx, client, selector, config.TimeRange)
		if err != nil {
			fmt.Printf("❌ Query_range fallback failed: %v\n", err)
			return nil, fmt.Errorf("export via query_range fallback failed: %w", err)
		}
		fmt.Println("✅ Fallback export data ready")
	} else if err != nil {
		fmt.Printf("❌ Direct export failed: %v\n", err)
		return nil, fmt.Errorf("export failed: %w", err)
	} else {
		fmt.Println("✅ Direct export successful")
	}
	defer func() { _ = exportReader.Close() }()

	// Step 2: Process metrics (with optional obfuscation)
	fmt.Printf("🔄 Processing metrics (obfuscation: %v)...\n", config.Obfuscation.Enabled)
	processStartTime := time.Now()
	processedReader, metricsCount, obfuscationMaps, err := s.processMetrics(exportReader, config.Obfuscation)
	if err != nil {
		fmt.Printf("❌ Metrics processing failed: %v\n", err)
		return nil, fmt.Errorf("metrics processing failed: %w", err)
	}
	fmt.Printf("✅ Metrics processed in %v: %d metrics\n", time.Since(processStartTime), metricsCount)

	// Step 3: Create archive
	fmt.Printf("📦 Creating archive...\n")
	metadata := s.buildArchiveMetadata(exportID, config, metricsCount, obfuscationMaps)

	archiveStartTime := time.Now()
	archivePath, sha256sum, err := s.archiveWriter.CreateArchive(exportID, processedReader, metadata)
	if err != nil {
		fmt.Printf("❌ Archive creation failed: %v\n", err)
		return nil, fmt.Errorf("archive creation failed: %w", err)
	}
	fmt.Printf("✅ Archive created in %v\n", time.Since(archiveStartTime))

	// Step 4: Get archive size
	archiveSize, err := s.archiveWriter.GetArchiveSize(archivePath)
	if err != nil {
		fmt.Printf("❌ Failed to get archive size: %v\n", err)
		return nil, fmt.Errorf("failed to get archive size: %w", err)
	}
	fmt.Printf("📊 Archive size: %.2f MB\n", float64(archiveSize)/(1024*1024))
	fmt.Printf("🔐 SHA256: %s\n", sha256sum)

	// Build result
	result := &domain.ExportResult{
		ExportID:           exportID,
		ArchivePath:        archivePath,
		ArchiveSizeBytes:   archiveSize,
		MetricsExported:    metricsCount,
		TimeRange:          config.TimeRange,
		ObfuscationApplied: config.Obfuscation.Enabled,
		SHA256:             sha256sum,
	}

	return result, nil
}

// processMetrics processes exported metrics with optional obfuscation
// Returns processed reader, metrics count, and obfuscation maps
func (s *exportServiceImpl) processMetrics(
	reader io.Reader,
	obfConfig domain.ObfuscationConfig,
) (io.Reader, int, map[string]map[string]string, error) {
	decoder := vm.NewExportDecoder(reader)
	var processedMetrics bytes.Buffer
	metricsCount := 0

	// Initialize obfuscator if needed
	var obfuscator *obfuscation.Obfuscator
	if obfConfig.Enabled {
		obfuscator = obfuscation.NewObfuscator()
	}

	// Process each metric
	for {
		metric, err := decoder.Decode()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, 0, nil, fmt.Errorf("decode error: %w", err)
		}

		// Apply obfuscation if enabled
		if obfuscator != nil {
			s.applyObfuscation(metric, obfuscator, obfConfig)
		}

		// Write processed metric as JSONL
		data, err := json.Marshal(metric)
		if err != nil {
			return nil, 0, nil, fmt.Errorf("marshal error: %w", err)
		}

		processedMetrics.Write(data)
		processedMetrics.WriteByte('\n')
		metricsCount++
	}

	// Build obfuscation maps
	obfuscationMaps := make(map[string]map[string]string)
	if obfuscator != nil {
		instanceMap, jobMap := obfuscator.GetMappings()
		obfuscationMaps["instance"] = instanceMap
		obfuscationMaps["job"] = jobMap
	}

	return &processedMetrics, metricsCount, obfuscationMaps, nil
}

// applyObfuscation applies obfuscation to a metric
func (s *exportServiceImpl) applyObfuscation(
	metric *vm.ExportedMetric,
	obfuscator *obfuscation.Obfuscator,
	config domain.ObfuscationConfig,
) {
	if metric.Metric == nil {
		return
	}

	// Obfuscate instance label
	if config.ObfuscateInstance {
		if instance, exists := metric.Metric["instance"]; exists {
			metric.Metric["instance"] = obfuscator.ObfuscateInstance(instance)
		}
	}

	// Obfuscate job label
	if config.ObfuscateJob {
		if job, exists := metric.Metric["job"]; exists {
			// Try to determine component from metric name or other labels
			component := s.guessComponent(metric.Metric)
			metric.Metric["job"] = obfuscator.ObfuscateJob(job, component)
		}
	}

	// Obfuscate custom labels (pod, namespace, etc.)
	for _, labelName := range config.CustomLabels {
		if value, exists := metric.Metric[labelName]; exists {
			metric.Metric[labelName] = obfuscator.ObfuscateCustomLabel(labelName, value)
		}
	}
}

// guessComponent attempts to determine component type from metric labels
// Falls back to "unknown" if cannot be determined
func (s *exportServiceImpl) guessComponent(labels map[string]string) string {
	// Try component label first (most reliable)
	if comp, exists := labels["component"]; exists {
		return comp
	}

	// Try vm_component label (if present from discovery query)
	if comp, exists := labels["vm_component"]; exists {
		return comp
	}

	// Try to guess from metric name
	metricName := labels["__name__"]
	if metricName == "" {
		return "unknown"
	}

	// Common VictoriaMetrics metric prefixes
	// Check specific components first (longer prefixes)
	if len(metricName) >= 10 && metricName[0:10] == "vmstorage_" {
		return "vmstorage"
	}
	if len(metricName) >= 9 && metricName[0:9] == "vmselect_" {
		return "vmselect"
	}
	if len(metricName) >= 9 && metricName[0:9] == "vminsert_" {
		return "vminsert"
	}
	if len(metricName) >= 8 && metricName[0:8] == "vmagent_" {
		return "vmagent"
	}
	if len(metricName) >= 8 && metricName[0:8] == "vmalert_" {
		return "vmalert"
	}

	// Fallback: use job name as component
	if job, exists := labels["job"]; exists {
		return job
	}

	return "unknown"
}

// buildSelector builds PromQL selector from job list
func (s *exportServiceImpl) buildSelector(jobs []string) string {
	if len(jobs) == 0 {
		return "{__name__!=\"\"}" // All metrics
	}

	// Build job selector: {job=~"job1|job2|job3"}
	jobRegex := ""
	for i, job := range jobs {
		if i > 0 {
			jobRegex += "|"
		}
		jobRegex += job
	}

	return fmt.Sprintf(`{job=~"%s"}`, jobRegex)
}

// buildArchiveMetadata builds archive metadata from export config
func (s *exportServiceImpl) buildArchiveMetadata(
	exportID string,
	config domain.ExportConfig,
	metricsCount int,
	obfuscationMaps map[string]map[string]string,
) archive.ArchiveMetadata {
	metadata := archive.ArchiveMetadata{
		ExportID:          exportID,
		ExportDate:        time.Now(),
		TimeRange:         config.TimeRange,
		Components:        config.Components,
		Jobs:              config.Jobs,
		MetricsCount:      metricsCount,
		Obfuscated:        config.Obfuscation.Enabled,
		VMExporterVersion: s.vmExporterVersion,
	}

	// Add obfuscation maps if present
	if instanceMap, exists := obfuscationMaps["instance"]; exists {
		metadata.InstanceMap = instanceMap
	}
	if jobMap, exists := obfuscationMaps["job"]; exists {
		metadata.JobMap = jobMap
	}

	return metadata
}

// isMissingRouteError checks if error is due to missing export route
func (s *exportServiceImpl) isMissingRouteError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := err.Error()
	return containsAny(errMsg, []string{
		"missing route",
		"404",
		"not found",
		"unsupported path",
	})
}

// containsAny checks if string contains any of the substrings (case-insensitive)
func containsAny(s string, substrs []string) bool {
	s = toLower(s)
	for _, substr := range substrs {
		if contains(s, toLower(substr)) {
			return true
		}
	}
	return false
}

// Helper functions for string operations
func toLower(s string) string {
	return strings.ToLower(s)
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

// exportViaQueryRange exports metrics using query_range as fallback when /api/v1/export is not available
// This method queries all series matching the selector and reconstructs export format
func (s *exportServiceImpl) exportViaQueryRange(ctx context.Context, client *vm.Client, selector string, timeRange domain.TimeRange) (io.ReadCloser, error) {
	// Calculate appropriate step based on time range
	// For short ranges (< 1 hour): 15s step
	// For medium ranges (1-24 hours): 1m step
	// For long ranges (> 24 hours): 5m step
	duration := timeRange.End.Sub(timeRange.Start)
	var step time.Duration
	switch {
	case duration < time.Hour:
		step = 15 * time.Second
	case duration < 24*time.Hour:
		step = 1 * time.Minute
	default:
		step = 5 * time.Minute
	}

	fmt.Printf("🔄 Starting query_range fallback:\n")
	fmt.Printf("   Time range: %s to %s (duration: %v)\n", timeRange.Start.Format(time.RFC3339), timeRange.End.Format(time.RFC3339), duration)
	fmt.Printf("   Step: %v\n", step)
	fmt.Printf("   Selector: %s\n", selector)
	fmt.Printf("   Executing query_range request...\n")

	// Execute query_range
	startTime := time.Now()
	result, err := client.QueryRange(ctx, selector, timeRange.Start, timeRange.End, step)
	if err != nil {
		fmt.Printf("❌ Query_range failed after %v: %v\n", time.Since(startTime), err)
		return nil, fmt.Errorf("query_range failed: %w", err)
	}

	fmt.Printf("✅ Query_range completed in %v\n", time.Since(startTime))
	fmt.Printf("   Series returned: %d\n", len(result.Data.Result))
	
	// Calculate total data points
	totalDataPoints := 0
	for _, series := range result.Data.Result {
		totalDataPoints += len(series.Values)
	}
	fmt.Printf("   Total data points: %d\n", totalDataPoints)
	fmt.Printf("🔄 Converting to export format (JSONL)...\n")

	// Convert query_range result to export format (JSONL)
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	
	convertStartTime := time.Now()
	processedPoints := 0
	lastProgressReport := time.Now()

	for seriesIdx, series := range result.Data.Result {
		// Each series becomes multiple export lines (one per data point)
		for _, value := range series.Values {
			if len(value) < 2 {
				continue
			}

			timestamp, ok := value[0].(float64)
			if !ok {
				continue
			}

			valueStr, ok := value[1].(string)
			if !ok {
				continue
			}

			// Build export line in VictoriaMetrics export format
			exportLine := map[string]interface{}{
				"metric": series.Metric,
				"values": []interface{}{valueStr},
				"timestamps": []interface{}{int64(timestamp * 1000)}, // Convert to milliseconds
			}

			if err := encoder.Encode(exportLine); err != nil {
				return nil, fmt.Errorf("failed to encode export line: %w", err)
			}
			
			processedPoints++
			
			// Report progress every 5 seconds or every 10000 points
			if time.Since(lastProgressReport) > 5*time.Second || processedPoints%10000 == 0 {
				progress := float64(processedPoints) / float64(totalDataPoints) * 100
				fmt.Printf("   Progress: %d/%d points (%.1f%%) - Series %d/%d\n", 
					processedPoints, totalDataPoints, progress, seriesIdx+1, len(result.Data.Result))
				lastProgressReport = time.Now()
			}
		}
	}

	fmt.Printf("✅ Conversion completed in %v\n", time.Since(convertStartTime))
	fmt.Printf("   Total points processed: %d\n", processedPoints)
	fmt.Printf("   Export data size: %.2f MB\n", float64(buf.Len())/(1024*1024))

	// Return as ReadCloser
	return io.NopCloser(&buf), nil
}

// generateExportID generates a unique export ID
func (s *exportServiceImpl) generateExportID() string {
	timestamp := time.Now().Unix()
	return fmt.Sprintf("export-%d", timestamp)
}

