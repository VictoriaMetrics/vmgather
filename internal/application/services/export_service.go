package services

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/VictoriaMetrics/vmgather/internal/domain"
	"github.com/VictoriaMetrics/vmgather/internal/infrastructure/archive"
	"github.com/VictoriaMetrics/vmgather/internal/infrastructure/obfuscation"
	"github.com/VictoriaMetrics/vmgather/internal/infrastructure/vm"
)

const defaultBatchTimeout = 2 * time.Minute

// ExportService interface for full export operations
type ExportService interface {
	// ExecuteExport performs full export with optional obfuscation
	ExecuteExport(ctx context.Context, config domain.ExportConfig) (*domain.ExportResult, error)
}

// exportServiceImpl implements ExportService
type exportServiceImpl struct {
	clientFactory   func(domain.VMConnection) *vm.Client
	archiveWriter   *archive.Writer
	vmGatherVersion string
}

// NewExportService creates a new export service
func NewExportService(outputDir, version string) ExportService {
	if version == "" {
		version = "dev"
	}
	return &exportServiceImpl{
		clientFactory:   vm.NewClient,
		archiveWriter:   archive.NewWriter(outputDir),
		vmGatherVersion: version,
	}
}

// ExportToWriter streams exported metrics into the provided writer.
// Intended for CLI oneshot mode; writes JSONL metrics without creating an archive.
func ExportToWriter(ctx context.Context, config domain.ExportConfig, writer io.Writer) (int, error) {
	service := &exportServiceImpl{
		clientFactory:   vm.NewClient,
		archiveWriter:   archive.NewWriter(os.TempDir()),
		vmGatherVersion: "dev",
	}
	return service.exportToWriter(ctx, config, writer)
}

// ExecuteExport performs full metrics export with optional obfuscation
func (s *exportServiceImpl) ExecuteExport(ctx context.Context, config domain.ExportConfig) (*domain.ExportResult, error) {
	// Generate export ID
	exportID := s.generateExportID()

	// Step 1: Prepare staging file for incremental writes
	stagingDir := config.StagingDir
	if stagingDir == "" {
		stagingDir = filepath.Join(s.archiveWriter.OutputDir(), "staging")
	}
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to prepare staging directory: %w", err)
	}
	if config.StagingFile == "" {
		config.StagingFile = filepath.Join(stagingDir, fmt.Sprintf("%s.partial.jsonl", exportID))
	}
	flags := os.O_CREATE | os.O_WRONLY
	if config.ResumeFromBatch > 0 {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	stagingHandle, err := os.OpenFile(config.StagingFile, flags, 0o640)
	if err != nil {
		return nil, fmt.Errorf("failed to create staging file: %w", err)
	}
	defer func() { _ = stagingHandle.Close() }()
	stagingWriter := bufio.NewWriter(stagingHandle)
	defer func() {
		_ = stagingWriter.Flush()
		_ = stagingHandle.Close()
	}()

	// Step 2: Export metrics from VictoriaMetrics in batches
	client := s.clientFactory(config.Connection)
	selector, useQueryRange := s.buildExportQuery(config)
	batchWindows := CalculateBatchWindows(config.TimeRange, config.Batching)
	metricsCount := 0
	var obfuscator *obfuscation.Obfuscator
	if config.Obfuscation.Enabled {
		obfuscator = obfuscation.NewObfuscator()
	}

	startIdx := config.ResumeFromBatch
	if startIdx < 0 || startIdx >= len(batchWindows) {
		startIdx = 0
	}

	for batchIndex := startIdx; batchIndex < len(batchWindows); batchIndex++ {
		window := batchWindows[batchIndex]
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		fmt.Printf("Processing batch %d/%d (%s - %s)\n",
			batchIndex+1, len(batchWindows), window.Start.Format(time.RFC3339), window.End.Format(time.RFC3339))
		batchStart := time.Now()

		batchCtx, cancelBatch := context.WithTimeout(ctx, defaultBatchTimeout)
		exportReader, err := s.fetchBatch(batchCtx, client, selector, window, config.MetricStepSeconds, useQueryRange)
		if err != nil {
			cancelBatch()
			return nil, err
		}

		batchCount, err := s.processMetricsIntoWriter(exportReader, config.Obfuscation, obfuscator, stagingWriter)
		_ = exportReader.Close()
		cancelBatch()
		if err != nil {
			fmt.Printf("[ERROR] Metrics processing failed for batch %d: %v\n", batchIndex+1, err)
			return nil, fmt.Errorf("metrics processing failed: %w", err)
		}
		if err := stagingWriter.Flush(); err != nil {
			return nil, fmt.Errorf("failed to flush staging file: %w", err)
		}

		metricsCount += batchCount
		batchDuration := time.Since(batchStart)
		fmt.Printf("[OK] Batch %d processed in %v (%d metrics)\n", batchIndex+1, batchDuration, batchCount)

		ReportBatchProgress(ctx, BatchProgress{
			BatchIndex:   batchIndex + 1,
			TotalBatches: len(batchWindows),
			TimeRange:    window,
			Metrics:      batchCount,
			Duration:     batchDuration,
		})
	}

	obfuscationMaps := make(map[string]map[string]string)
	if obfuscator != nil {
		instanceMap, jobMap := obfuscator.GetMappings()
		obfuscationMaps["instance"] = instanceMap
		obfuscationMaps["job"] = jobMap
	}

	// Step 3: Create archive
	fmt.Printf("Creating archive...\n")
	metadata := s.buildArchiveMetadata(exportID, config, metricsCount, obfuscationMaps)
	processedReader, err := os.Open(config.StagingFile)
	if err != nil {
		return nil, fmt.Errorf("failed to open staging file for archive: %w", err)
	}
	defer func() {
		_ = processedReader.Close()
	}()

	archiveStartTime := time.Now()
	archivePath, sha256sum, err := s.archiveWriter.CreateArchive(exportID, processedReader, metadata)
	if err != nil {
		fmt.Printf("[ERROR] Archive creation failed: %v\n", err)
		return nil, fmt.Errorf("archive creation failed: %w", err)
	}
	fmt.Printf("[OK] Archive created in %v\n", time.Since(archiveStartTime))

	// Step 4: Get archive size
	archiveSize, err := s.archiveWriter.GetArchiveSize(archivePath)
	if err != nil {
		fmt.Printf("[ERROR] Failed to get archive size: %v\n", err)
		return nil, fmt.Errorf("failed to get archive size: %w", err)
	}
	fmt.Printf("Archive size: %.2f MB\n", float64(archiveSize)/(1024*1024))
	fmt.Printf("SHA256: %s\n", sha256sum)

	if config.ResumeFromBatch == 0 {
		if err := os.Remove(config.StagingFile); err != nil {
			log.Printf("[WARN] Failed to remove staging file %s: %v", config.StagingFile, err)
		}
	}

	// Build result
	result := &domain.ExportResult{
		ExportID:           exportID,
		ArchivePath:        archivePath,
		ArchiveName:        filepath.Base(archivePath),
		ArchiveSizeBytes:   archiveSize,
		MetricsExported:    metricsCount,
		TimeRange:          config.TimeRange,
		ObfuscationApplied: config.Obfuscation.Enabled,
		SHA256:             sha256sum,
	}

	return result, nil
}

func (s *exportServiceImpl) exportToWriter(ctx context.Context, config domain.ExportConfig, writer io.Writer) (int, error) {
	client := s.clientFactory(config.Connection)
	selector, useQueryRange := s.buildExportQuery(config)
	batchWindows := CalculateBatchWindows(config.TimeRange, config.Batching)
	metricsCount := 0
	var obfuscator *obfuscation.Obfuscator
	if config.Obfuscation.Enabled {
		obfuscator = obfuscation.NewObfuscator()
	}

	buffered := bufio.NewWriter(writer)
	for _, window := range batchWindows {
		batchCtx, cancelBatch := context.WithTimeout(ctx, defaultBatchTimeout)
		exportReader, err := s.fetchBatch(batchCtx, client, selector, window, config.MetricStepSeconds, useQueryRange)
		if err != nil {
			cancelBatch()
			return 0, err
		}

		count, err := s.processMetricsIntoWriter(exportReader, config.Obfuscation, obfuscator, buffered)
		cancelBatch()
		if closeErr := exportReader.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
		if err != nil {
			return 0, err
		}
		metricsCount += count
	}

	if err := buffered.Flush(); err != nil {
		return 0, fmt.Errorf("flush error: %w", err)
	}
	return metricsCount, nil
}

// processMetrics processes exported metrics with optional obfuscation
// Returns processed reader, metrics count, and obfuscation maps
// nolint:unused // kept for advanced tests that need direct access to the processor
func (s *exportServiceImpl) processMetrics(
	reader io.Reader,
	obfConfig domain.ObfuscationConfig,
) (io.Reader, int, map[string]map[string]string, error) {
	var processedMetrics bytes.Buffer
	var obfuscator *obfuscation.Obfuscator
	if obfConfig.Enabled {
		obfuscator = obfuscation.NewObfuscator()
	}

	metricsCount, err := s.processMetricsIntoWriter(reader, obfConfig, obfuscator, &processedMetrics)
	if err != nil {
		return nil, 0, nil, err
	}

	obfuscationMaps := make(map[string]map[string]string)
	if obfuscator != nil {
		instanceMap, jobMap := obfuscator.GetMappings()
		obfuscationMaps["instance"] = instanceMap
		obfuscationMaps["job"] = jobMap
	}

	return &processedMetrics, metricsCount, obfuscationMaps, nil
}

// processMetricsIntoWriter decodes metrics stream, applies obfuscation (if enabled) and appends JSONL lines into the provided writer.
func (s *exportServiceImpl) processMetricsIntoWriter(
	reader io.Reader,
	obfConfig domain.ObfuscationConfig,
	obfuscator *obfuscation.Obfuscator,
	writer io.Writer,
) (int, error) {
	decoder := vm.NewExportDecoder(reader)
	metricsCount := 0

	for {
		metric, err := decoder.Decode()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, fmt.Errorf("decode error: %w", err)
		}

		if len(obfConfig.DropLabels) > 0 {
			for _, label := range obfConfig.DropLabels {
				delete(metric.Metric, label)
			}
		}

		if obfConfig.Enabled {
			if obfuscator == nil {
				obfuscator = obfuscation.NewObfuscator()
			}
			s.applyObfuscation(metric, obfuscator, obfConfig)
		}

		data, err := json.Marshal(metric)
		if err != nil {
			return 0, fmt.Errorf("marshal error: %w", err)
		}

		if _, err := writer.Write(data); err != nil {
			return 0, fmt.Errorf("write error: %w", err)
		}
		if _, err := writer.Write([]byte{'\n'}); err != nil {
			return 0, fmt.Errorf("write error: %w", err)
		}
		metricsCount++
	}

	return metricsCount, nil
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

	return buildJobFilterSelector(jobs)
}

func (s *exportServiceImpl) buildExportQuery(config domain.ExportConfig) (string, bool) {
	if config.Mode == domain.ExportModeCustom && config.Query != "" {
		switch config.QueryType {
		case domain.QueryModeSelector:
			selector := config.Query
			if len(config.Jobs) > 0 {
				filter := buildJobFilterSelector(config.Jobs)
				selector = fmt.Sprintf("(%s) and on(job) %s", selector, filter)
				return selector, true
			}
			return selector, false
		case domain.QueryModeMetricsQL:
			return config.Query, true
		default:
			return config.Query, false
		}
	}

	return s.buildSelector(config.Jobs), false
}

// buildArchiveMetadata builds archive metadata from export config
func (s *exportServiceImpl) buildArchiveMetadata(
	exportID string,
	config domain.ExportConfig,
	metricsCount int,
	obfuscationMaps map[string]map[string]string,
) archive.ArchiveMetadata {
	metadata := archive.ArchiveMetadata{
		ExportID:        exportID,
		ExportDate:      time.Now().UTC(),
		TimeRange:       config.TimeRange,
		Components:      uniqueStrings(config.Components),
		Jobs:            uniqueStrings(config.Jobs),
		MetricsCount:    metricsCount,
		Obfuscated:      config.Obfuscation.Enabled,
		VMGatherVersion: s.vmGatherVersion,
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

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" {
			continue
		}
		if _, exists := seen[v]; exists {
			continue
		}
		seen[v] = struct{}{}
		result = append(result, v)
	}
	return result
}

func determineQueryRangeStep(tr domain.TimeRange, overrideSeconds int) time.Duration {
	if overrideSeconds > 0 {
		step := time.Duration(overrideSeconds) * time.Second
		if step < minBatchInterval {
			return minBatchInterval
		}
		if step > maxBatchInterval {
			return maxBatchInterval
		}
		return step
	}
	return recommendedIntervalForDuration(tr.End.Sub(tr.Start))
}

// exportViaQueryRange exports metrics using query_range as fallback when /api/v1/export is not available
// This method queries all series matching the selector and reconstructs export format
// It uses streaming and time chunking to avoid OOM on large time ranges
func (s *exportServiceImpl) exportViaQueryRange(ctx context.Context, client *vm.Client, selector string, timeRange domain.TimeRange, overrideSeconds int) (io.ReadCloser, error) {
	step := determineQueryRangeStep(timeRange, overrideSeconds)

	// Create a pipe to stream results
	pr, pw := io.Pipe()

	go func() {
		encoder := json.NewEncoder(pw)

		// Chunk size: 1 hour (balance between request count and memory usage)
		chunkSize := 1 * time.Hour

		currentStart := timeRange.Start
		totalPoints := 0

		fmt.Printf("Starting streaming query_range fallback (chunk size: %v)\n", chunkSize)

		for currentStart.Before(timeRange.End) {
			// Check context cancellation
			if ctx.Err() != nil {
				_ = pw.CloseWithError(ctx.Err())
				return
			}

			currentEnd := currentStart.Add(chunkSize)
			if currentEnd.After(timeRange.End) {
				currentEnd = timeRange.End
			}

			// Execute query_range for this chunk
			// We use a separate context for the request to ensure we can cancel it
			reqCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			result, err := client.QueryRange(reqCtx, selector, currentStart, currentEnd, step)
			cancel()

			if err != nil {
				fmt.Printf("[FAIL] Query_range failed for chunk %s-%s: %v\n",
					currentStart.Format(time.RFC3339), currentEnd.Format(time.RFC3339), err)
				_ = pw.CloseWithError(fmt.Errorf("query_range chunk failed: %w", err))
				return
			}

			// Convert and stream results
			for _, series := range result.Data.Result {
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
					valueNum, err := strconv.ParseFloat(valueStr, 64)
					if err != nil {
						continue
					}

					// Build export line
					exportLine := map[string]interface{}{
						"metric":     series.Metric,
						"values":     []interface{}{valueNum},
						"timestamps": []interface{}{int64(timestamp * 1000)},
					}

					if err := encoder.Encode(exportLine); err != nil {
						_ = pw.CloseWithError(fmt.Errorf("encode error: %w", err))
						return
					}
					totalPoints++
				}
			}

			// Move to next chunk
			// Add a small overlap or just next step?
			// QueryRange is inclusive of start and end?
			// Prometheus /query_range is usually inclusive.
			// If we do [0, 60] and [60, 120], we might get duplicate point at 60.
			// But step aligns points.
			// Safest is to start next chunk at currentEnd + step?
			// Or just rely on deduplication downstream?
			// Actually, standard practice is [start, end).
			// But Prom API takes start/end.
			// Let's just use currentEnd as next start, duplicates are better than gaps,
			// and usually downstream can handle it or we accept minor overlap.
			currentStart = currentEnd
		}

		fmt.Printf("[OK] Streaming completed. Total points: %d\n", totalPoints)
		_ = pw.Close()
	}()

	return pr, nil
}

func (s *exportServiceImpl) fetchBatch(ctx context.Context, client *vm.Client, selector string, tr domain.TimeRange, metricStepSeconds int, forceQueryRange bool) (io.ReadCloser, error) {
	fmt.Printf("Attempting export for batch: %s -> %s\n", tr.Start.Format(time.RFC3339), tr.End.Format(time.RFC3339))
	if forceQueryRange {
		fmt.Printf("[INFO] Using query_range export for custom query\n")
		return s.exportViaQueryRange(ctx, client, selector, tr, metricStepSeconds)
	}
	reader, err := client.Export(ctx, selector, tr.Start, tr.End)
	if err != nil && s.isMissingRouteError(err) {
		fmt.Printf("[WARN] Export API not available for current batch, falling back to query_range\n")
		return s.exportViaQueryRange(ctx, client, selector, tr, metricStepSeconds)
	}
	if err != nil {
		return nil, fmt.Errorf("export failed: %w", err)
	}
	return reader, nil
}

// generateExportID generates a unique export ID
func (s *exportServiceImpl) generateExportID() string {
	timestamp := time.Now().Unix()
	return fmt.Sprintf("export-%d", timestamp)
}
