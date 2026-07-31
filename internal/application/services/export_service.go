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
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/VictoriaMetrics/vmgather/internal/domain"
	"github.com/VictoriaMetrics/vmgather/internal/infrastructure/archive"
	"github.com/VictoriaMetrics/vmgather/internal/infrastructure/obfuscation"
	"github.com/VictoriaMetrics/vmgather/internal/infrastructure/vm"
)

const defaultBatchTimeout = 2 * time.Minute

var (
	metricSelectorPattern = regexp.MustCompile(`^([a-zA-Z_:][a-zA-Z0-9_:]*)\s*\{(.*)\}$`)
	metricNamePattern     = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)
	jobMatcherPattern     = regexp.MustCompile(`(^|,)\s*job\s*(=|!=|=~|!~)`)
)

type exportAttempt struct {
	Selector      string
	TimeRange     domain.TimeRange
	UseQueryRange bool
	Depth         int
	Jobs          []string
	Strategy      string
	StepSeconds   int
}

type adaptiveExportStats struct {
	Mode               domain.ExportAdaptivityMode
	MaxStepSecondsUsed int
	Sampled            bool
	Decisions          []archive.AdaptiveDecision
}

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
	applyExportServiceDefaults(&config)

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
	if err := initializeStagingFile(config.StagingFile, config.ResumeFromBatch > 0); err != nil {
		return nil, fmt.Errorf("failed to initialize staging file: %w", err)
	}

	// Step 2: Export metrics from VictoriaMetrics in batches
	client := s.clientFactory(config.Connection)
	batchWindows := CalculateBatchWindows(config.TimeRange, config.Batching)
	metricsCount := 0
	var obfuscator *obfuscation.Obfuscator
	if config.Obfuscation.Enabled {
		obfuscator = obfuscation.NewObfuscator()
	}
	adaptiveStats := &adaptiveExportStats{
		Mode:               config.Safety.Mode,
		MaxStepSecondsUsed: config.MetricStepSeconds,
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

		attempt := s.newExportAttempt(config, window)
		retryCount := 0
		batchCount, err := s.exportWindowAdaptive(ctx, client, config, attempt, config.StagingFile, obfuscator, &retryCount, adaptiveStats)
		if err != nil {
			fmt.Printf("[ERROR] Metrics processing failed for batch %d: %v\n", batchIndex+1, err)
			return nil, fmt.Errorf("metrics processing failed: %w", err)
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
	metadata := s.buildArchiveMetadata(exportID, config, metricsCount, obfuscationMaps, adaptiveStats)
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

func initializeStagingFile(path string, preserve bool) error {
	flags := os.O_CREATE | os.O_WRONLY
	if preserve {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	handle, err := os.OpenFile(path, flags, 0o640)
	if err != nil {
		return err
	}
	return handle.Close()
}

func (s *exportServiceImpl) exportToWriter(ctx context.Context, config domain.ExportConfig, writer io.Writer) (int, error) {
	applyExportServiceDefaults(&config)

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

func applyExportServiceDefaults(config *domain.ExportConfig) {
	originalBatching := config.Batching
	ApplyExportDefaults(config)
	if !originalBatching.Enabled && originalBatching.Strategy == "" && originalBatching.CustomIntervalSecs == 0 {
		config.Batching = originalBatching
	}
}

func (s *exportServiceImpl) newExportAttempt(config domain.ExportConfig, tr domain.TimeRange) exportAttempt {
	selector, useQueryRange := s.buildExportQuery(config)
	strategy := "export"
	if useQueryRange {
		strategy = "query_range"
	}
	return exportAttempt{
		Selector:      selector,
		TimeRange:     tr,
		UseQueryRange: useQueryRange,
		Jobs:          uniqueStrings(config.Jobs),
		Strategy:      strategy,
		StepSeconds:   config.MetricStepSeconds,
	}
}

func (s *exportServiceImpl) exportWindowAdaptive(
	ctx context.Context,
	client *vm.Client,
	config domain.ExportConfig,
	attempt exportAttempt,
	mainStagingFile string,
	obfuscator *obfuscation.Obfuscator,
	retryCount *int,
	stats *adaptiveExportStats,
) (int, error) {
	log.Printf("[EXPORT][ATTEMPT] strategy=%s depth=%d use_query_range=%v step=%ds jobs=%d window=%s..%s selector=%s",
		attempt.Strategy,
		attempt.Depth,
		attempt.UseQueryRange,
		attempt.StepSeconds,
		len(attempt.Jobs),
		attempt.TimeRange.Start.Format(time.RFC3339),
		attempt.TimeRange.End.Format(time.RFC3339),
		formatSelectorForLog(attempt.Selector),
	)
	attemptFile := fmt.Sprintf("%s.attempt.%d", mainStagingFile, time.Now().UnixNano())
	count, err := s.fetchAndProcessAttempt(ctx, client, config, attempt, obfuscator, attemptFile, stats)
	if err == nil {
		log.Printf("[EXPORT][ATTEMPT][OK] strategy=%s depth=%d metrics=%d window=%s..%s",
			attempt.Strategy,
			attempt.Depth,
			count,
			attempt.TimeRange.Start.Format(time.RFC3339),
			attempt.TimeRange.End.Format(time.RFC3339),
		)
		if err := appendFile(mainStagingFile, attemptFile); err != nil {
			_ = os.Remove(attemptFile)
			return 0, err
		}
		_ = os.Remove(attemptFile)
		return count, nil
	}
	_ = os.Remove(attemptFile)

	kind := vm.ErrorKindOf(err)
	log.Printf("[EXPORT][ATTEMPT][FAIL] strategy=%s depth=%d kind=%s step=%ds jobs=%d window=%s..%s selector=%s err=%v",
		attempt.Strategy,
		attempt.Depth,
		kind,
		attempt.StepSeconds,
		len(attempt.Jobs),
		attempt.TimeRange.Start.Format(time.RFC3339),
		attempt.TimeRange.End.Format(time.RFC3339),
		formatSelectorForLog(attempt.Selector),
		err,
	)

	switch {
	case kind == vm.ErrorKindMissingRoute && !attempt.UseQueryRange:
		*retryCount++
		log.Printf("[EXPORT][ADAPTIVE] decision=query_range_fallback retries=%d kind=%s window=%s..%s selector=%s",
			*retryCount,
			kind,
			attempt.TimeRange.Start.Format(time.RFC3339),
			attempt.TimeRange.End.Format(time.RFC3339),
			formatSelectorForLog(attempt.Selector),
		)
		progress := AdaptiveRetryProgress{
			Retries:   *retryCount,
			TimeRange: attempt.TimeRange,
			ErrorKind: string(kind),
			Strategy:  "query_range",
		}
		ReportAdaptiveRetry(ctx, progress)
		recordAdaptiveDecision(stats, progress)
		next := attempt
		next.UseQueryRange = true
		next.Depth++
		next.Strategy = "query_range"
		return s.exportWindowAdaptive(ctx, client, config, next, mainStagingFile, obfuscator, retryCount, stats)
	case kind == vm.ErrorKindTooManySeries && config.Safety.AutoSplit && attempt.Depth < config.Safety.MaxSplitDepth && config.Safety.SplitByJob && len(attempt.Jobs) > 1:
		*retryCount++
		log.Printf("[EXPORT][ADAPTIVE] decision=split_by_job retries=%d kind=%s jobs=%s window=%s..%s",
			*retryCount,
			kind,
			formatJobsForLog(attempt.Jobs),
			attempt.TimeRange.Start.Format(time.RFC3339),
			attempt.TimeRange.End.Format(time.RFC3339),
		)
		progress := AdaptiveRetryProgress{
			Retries:   *retryCount,
			TimeRange: attempt.TimeRange,
			ErrorKind: string(kind),
			Strategy:  "split_by_job",
		}
		ReportAdaptiveRetry(ctx, progress)
		recordAdaptiveDecision(stats, progress)
		children := make([]exportAttempt, 0, len(attempt.Jobs))
		for _, job := range attempt.Jobs {
			child, ok := s.buildJobSplitAttempt(config, attempt, job)
			if !ok {
				return 0, enrichAdaptiveError(kind, err, attempt, config.Safety.MinWindowSeconds)
			}
			child.Depth = attempt.Depth + 1
			children = append(children, child)
		}
		return s.runSplitAttemptsAtomically(ctx, client, config, children, mainStagingFile, obfuscator, retryCount, stats)
	case kind == vm.ErrorKindQueryTimeout && attempt.UseQueryRange:
		if config.Safety.AutoSplit && attempt.Depth < config.Safety.MaxSplitDepth {
			left, right, ok := splitTimeRange(attempt.TimeRange, config.Safety.MinWindowSeconds)
			if ok {
				*retryCount++
				log.Printf("[EXPORT][ADAPTIVE] decision=split_by_time retries=%d kind=%s step=%ds window=%s..%s -> [%s..%s] + [%s..%s]",
					*retryCount,
					kind,
					attempt.StepSeconds,
					attempt.TimeRange.Start.Format(time.RFC3339),
					attempt.TimeRange.End.Format(time.RFC3339),
					left.Start.Format(time.RFC3339),
					left.End.Format(time.RFC3339),
					right.Start.Format(time.RFC3339),
					right.End.Format(time.RFC3339),
				)
				progress := AdaptiveRetryProgress{
					Retries:     *retryCount,
					TimeRange:   attempt.TimeRange,
					ErrorKind:   string(kind),
					Strategy:    "split_by_time",
					StepSeconds: attempt.StepSeconds,
				}
				ReportAdaptiveRetry(ctx, progress)
				recordAdaptiveDecision(stats, progress)
				leftAttempt := attempt
				leftAttempt.TimeRange = left
				leftAttempt.Depth = attempt.Depth + 1
				leftAttempt.Strategy = "query_range"
				rightAttempt := attempt
				rightAttempt.TimeRange = right
				rightAttempt.Depth = attempt.Depth + 1
				rightAttempt.Strategy = "query_range"

				return s.runSplitAttemptsAtomically(ctx, client, config, []exportAttempt{leftAttempt, rightAttempt}, mainStagingFile, obfuscator, retryCount, stats)
			}
		}
		if nextStep, ok := nextAutopilotStep(attempt.StepSeconds, config.Safety); ok {
			*retryCount++
			log.Printf("[EXPORT][ADAPTIVE] decision=increase_step retries=%d kind=%s from_step=%ds to_step=%ds window=%s..%s",
				*retryCount,
				kind,
				attempt.StepSeconds,
				nextStep,
				attempt.TimeRange.Start.Format(time.RFC3339),
				attempt.TimeRange.End.Format(time.RFC3339),
			)
			progress := AdaptiveRetryProgress{
				Retries:     *retryCount,
				TimeRange:   attempt.TimeRange,
				ErrorKind:   string(kind),
				Strategy:    "increase_step",
				StepSeconds: nextStep,
			}
			ReportAdaptiveRetry(ctx, progress)
			recordAdaptiveDecision(stats, progress)
			next := attempt
			next.StepSeconds = nextStep
			next.Depth = 0
			next.Strategy = "query_range"
			return s.exportWindowAdaptive(ctx, client, config, next, mainStagingFile, obfuscator, retryCount, stats)
		}
	default:
	}

	logAdaptiveFailure(config, attempt, kind, err)
	return 0, enrichAdaptiveError(kind, err, attempt, config.Safety.MinWindowSeconds)
}

func (s *exportServiceImpl) runSplitAttemptsAtomically(
	ctx context.Context,
	client *vm.Client,
	config domain.ExportConfig,
	children []exportAttempt,
	mainStagingFile string,
	obfuscator *obfuscation.Obfuscator,
	retryCount *int,
	stats *adaptiveExportStats,
) (int, error) {
	splitFile := fmt.Sprintf("%s.split.%d", mainStagingFile, time.Now().UnixNano())
	if err := initializeStagingFile(splitFile, false); err != nil {
		return 0, fmt.Errorf("failed to initialize split staging file: %w", err)
	}

	total := 0
	for _, child := range children {
		count, err := s.exportWindowAdaptive(ctx, client, config, child, splitFile, obfuscator, retryCount, stats)
		if err != nil {
			_ = os.Remove(splitFile)
			return 0, err
		}
		total += count
	}
	if err := appendFile(mainStagingFile, splitFile); err != nil {
		_ = os.Remove(splitFile)
		return 0, err
	}
	if err := os.Remove(splitFile); err != nil {
		log.Printf("[WARN] Failed to remove split staging file %s: %v", splitFile, err)
	}
	return total, nil
}

func (s *exportServiceImpl) fetchAndProcessAttempt(
	ctx context.Context,
	client *vm.Client,
	config domain.ExportConfig,
	attempt exportAttempt,
	obfuscator *obfuscation.Obfuscator,
	attemptFile string,
	stats *adaptiveExportStats,
) (int, error) {
	handle, err := os.OpenFile(attemptFile, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return 0, fmt.Errorf("failed to create attempt file: %w", err)
	}
	defer func() { _ = handle.Close() }()

	writer := bufio.NewWriter(handle)

	batchCtx, cancelBatch := context.WithTimeout(ctx, defaultBatchTimeout)
	defer cancelBatch()

	exportReader, err := s.fetchAttemptReader(batchCtx, client, attempt)
	if err != nil {
		return 0, err
	}
	count, err := s.processMetricsIntoWriter(exportReader, config.Obfuscation, obfuscator, writer)
	if closeErr := exportReader.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	if err != nil {
		return 0, err
	}
	if err := writer.Flush(); err != nil {
		return 0, fmt.Errorf("failed to flush attempt file: %w", err)
	}
	if err := handle.Close(); err != nil {
		return 0, fmt.Errorf("failed to close attempt file: %w", err)
	}
	recordAttemptSuccess(stats, attempt)
	return count, nil
}

func appendFile(destination, source string) error {
	src, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("failed to open attempt file: %w", err)
	}
	defer func() { _ = src.Close() }()

	dst, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return fmt.Errorf("failed to append staging file: %w", err)
	}

	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return fmt.Errorf("failed to append attempt output: %w", err)
	}
	if err := dst.Close(); err != nil {
		return fmt.Errorf("failed to close staging file after append: %w", err)
	}
	return nil
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
	// No transformation requested: skip the decode/re-marshal round trip and
	// stream lines through as-is. This avoids a reflection-based JSON
	// unmarshal+marshal on every metric line, which dominates CPU on large exports.
	if !obfConfig.Enabled && len(obfConfig.DropLabels) == 0 {
		count, err := vm.CopyLines(reader, writer)
		if err != nil {
			return 0, fmt.Errorf("copy error: %w", err)
		}
		return count, nil
	}

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
				if rewritten, ok := applyJobMatcherToSelector(selector, config.Jobs); ok {
					return rewritten, false
				}
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

func (s *exportServiceImpl) buildJobSplitAttempt(config domain.ExportConfig, parent exportAttempt, job string) (exportAttempt, bool) {
	child := exportAttempt{
		TimeRange:   parent.TimeRange,
		Jobs:        []string{job},
		StepSeconds: parent.StepSeconds,
	}

	switch {
	case config.Mode == domain.ExportModeCustom && config.QueryType == domain.QueryModeSelector:
		if rewritten, ok := applyJobMatcherToSelector(config.Query, []string{job}); ok {
			child.Selector = rewritten
			child.UseQueryRange = false
			child.Strategy = "export"
			return child, true
		}
		filter := buildJobFilterSelector([]string{job})
		child.Selector = fmt.Sprintf("(%s) and on(job) %s", config.Query, filter)
		child.UseQueryRange = true
		child.Strategy = "query_range"
		return child, true
	case config.Mode == domain.ExportModeCluster:
		child.Selector = s.buildSelector([]string{job})
		child.UseQueryRange = false
		child.Strategy = "export"
		return child, true
	default:
		return exportAttempt{}, false
	}
}

func applyJobMatcherToSelector(selector string, jobs []string) (string, bool) {
	trimmed := strings.TrimSpace(selector)
	if trimmed == "" || len(jobs) == 0 {
		return "", false
	}
	jobMatcher := buildJobFilterSelector(jobs)
	jobMatcher = strings.TrimPrefix(strings.TrimSuffix(jobMatcher, "}"), "{")

	if strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}") {
		inside := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "{"), "}"))
		if containsJobMatcher(inside) {
			return "", false
		}
		if inside == "" {
			return fmt.Sprintf("{%s}", jobMatcher), true
		}
		return fmt.Sprintf("{%s,%s}", inside, jobMatcher), true
	}

	if matches := metricSelectorPattern.FindStringSubmatch(trimmed); len(matches) == 3 {
		metricName := matches[1]
		inside := strings.TrimSpace(matches[2])
		if containsJobMatcher(inside) {
			return "", false
		}
		if inside == "" {
			return fmt.Sprintf("%s{%s}", metricName, jobMatcher), true
		}
		return fmt.Sprintf("%s{%s,%s}", metricName, inside, jobMatcher), true
	}

	if metricNamePattern.MatchString(trimmed) {
		return fmt.Sprintf("%s{%s}", trimmed, jobMatcher), true
	}

	return "", false
}

func containsJobMatcher(selectorBody string) bool {
	return jobMatcherPattern.MatchString(selectorBody)
}

func nextAutopilotStep(current int, safety domain.ExportSafetyConfig) (int, bool) {
	if safety.Mode != domain.ExportAdaptivityAutopilot {
		return 0, false
	}
	for _, step := range normalizeStepLadder(safety.StepLadderSeconds, safety.MaxStepSeconds) {
		if step > current {
			return step, true
		}
	}
	return 0, false
}

func recordAdaptiveDecision(stats *adaptiveExportStats, progress AdaptiveRetryProgress) {
	if stats == nil {
		return
	}
	stats.Decisions = append(stats.Decisions, archive.AdaptiveDecision{
		Strategy:    progress.Strategy,
		ErrorKind:   progress.ErrorKind,
		TimeRange:   progress.TimeRange,
		StepSeconds: progress.StepSeconds,
	})
	if progress.StepSeconds > stats.MaxStepSecondsUsed {
		stats.MaxStepSecondsUsed = progress.StepSeconds
	}
	if progress.Strategy == "increase_step" {
		stats.Sampled = true
	}
}

func recordAttemptSuccess(stats *adaptiveExportStats, attempt exportAttempt) {
	if stats == nil {
		return
	}
	if attempt.UseQueryRange {
		stats.Sampled = true
	}
	if attempt.StepSeconds > stats.MaxStepSecondsUsed {
		stats.MaxStepSecondsUsed = attempt.StepSeconds
	}
}

// buildArchiveMetadata builds archive metadata from export config
func (s *exportServiceImpl) buildArchiveMetadata(
	exportID string,
	config domain.ExportConfig,
	metricsCount int,
	obfuscationMaps map[string]map[string]string,
	adaptiveStats *adaptiveExportStats,
) archive.ArchiveMetadata {
	metadata := archive.ArchiveMetadata{
		ExportID:             exportID,
		ExportDate:           time.Now().UTC(),
		TimeRange:            config.TimeRange,
		Components:           uniqueStrings(config.Components),
		Jobs:                 uniqueStrings(config.Jobs),
		MetricsCount:         metricsCount,
		Obfuscated:           config.Obfuscation.Enabled,
		AdaptiveMode:         string(config.Safety.Mode),
		MetricStepSeconds:    config.MetricStepSeconds,
		MaxMetricStepSeconds: config.Safety.MaxStepSeconds,
		VMGatherVersion:      s.vmGatherVersion,
	}
	if adaptiveStats != nil {
		metadata.AdaptiveMode = string(adaptiveStats.Mode)
		metadata.Sampled = adaptiveStats.Sampled
		if adaptiveStats.Sampled {
			metadata.MetricStepSeconds = adaptiveStats.MaxStepSecondsUsed
		} else {
			metadata.MetricStepSeconds = 0
		}
		metadata.AdaptiveDecisions = append([]archive.AdaptiveDecision(nil), adaptiveStats.Decisions...)
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
	return vm.ErrorKindOf(err) == vm.ErrorKindMissingRoute
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

func splitTimeRange(tr domain.TimeRange, minWindowSeconds int) (domain.TimeRange, domain.TimeRange, bool) {
	minWindow := time.Duration(minWindowSeconds) * time.Second
	if minWindowSeconds <= 0 {
		minWindow = 5 * time.Second
	}
	duration := tr.End.Sub(tr.Start)
	if duration <= minWindow {
		return domain.TimeRange{}, domain.TimeRange{}, false
	}

	mid := tr.Start.Add(duration / 2)
	if !mid.After(tr.Start) || !tr.End.After(mid) {
		return domain.TimeRange{}, domain.TimeRange{}, false
	}
	return domain.TimeRange{Start: tr.Start, End: mid}, domain.TimeRange{Start: mid, End: tr.End}, true
}

func enrichAdaptiveError(kind vm.ErrorKind, err error, attempt exportAttempt, minWindowSeconds int) error {
	switch kind {
	case vm.ErrorKindQueryTimeout:
		return fmt.Errorf("query still exceeds VictoriaMetrics -search.maxQueryDuration after splitting down to %ds windows and sampling at %ds; narrow selector/query or use raw selector export: %w", minWindowSeconds, attempt.StepSeconds, err)
	case vm.ErrorKindTooManySeries:
		if len(attempt.Jobs) <= 1 {
			return fmt.Errorf("query exceeds VictoriaMetrics series limits and vmgather cannot split the request further by job; narrow selector/query or adjust VictoriaMetrics -search.maxExportSeries / -search.maxUniqueTimeseries: %w", err)
		}
		return fmt.Errorf("query exceeds VictoriaMetrics series limits; narrow selector/query or adjust VictoriaMetrics -search.maxExportSeries / -search.maxUniqueTimeseries: %w", err)
	default:
		if attempt.UseQueryRange {
			return fmt.Errorf("adaptive export failed while using query_range: %w", err)
		}
		return err
	}
}

func logAdaptiveFailure(config domain.ExportConfig, attempt exportAttempt, kind vm.ErrorKind, err error) {
	switch kind {
	case vm.ErrorKindTooManySeries:
		log.Printf("[EXPORT][LIMIT] too_many_series unrecoverable jobs=%d split_by_job=%v split_by_metric_name=%v use_query_range=%v selector=%s err=%v",
			len(attempt.Jobs),
			config.Safety.SplitByJob,
			config.Safety.SplitByMetricName,
			attempt.UseQueryRange,
			formatSelectorForLog(attempt.Selector),
			err,
		)
	case vm.ErrorKindQueryTimeout:
		log.Printf("[EXPORT][LIMIT] query_timeout unrecoverable min_window_seconds=%d step=%ds depth=%d selector=%s err=%v",
			config.Safety.MinWindowSeconds,
			attempt.StepSeconds,
			attempt.Depth,
			formatSelectorForLog(attempt.Selector),
			err,
		)
	default:
		log.Printf("[EXPORT][FAIL] unrecoverable kind=%s strategy=%s depth=%d selector=%s err=%v",
			kind,
			attempt.Strategy,
			attempt.Depth,
			formatSelectorForLog(attempt.Selector),
			err,
		)
	}
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

func (s *exportServiceImpl) fetchAttemptReader(ctx context.Context, client *vm.Client, attempt exportAttempt) (io.ReadCloser, error) {
	fmt.Printf("Attempting export for batch: %s -> %s\n", attempt.TimeRange.Start.Format(time.RFC3339), attempt.TimeRange.End.Format(time.RFC3339))
	if attempt.UseQueryRange {
		fmt.Printf("[INFO] Using query_range export for custom query at %ds step\n", attempt.StepSeconds)
		return s.exportViaQueryRange(ctx, client, attempt.Selector, attempt.TimeRange, attempt.StepSeconds)
	}
	reader, err := client.Export(ctx, attempt.Selector, attempt.TimeRange.Start, attempt.TimeRange.End)
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
