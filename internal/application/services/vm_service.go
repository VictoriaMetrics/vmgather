package services

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/VictoriaMetrics/vmgather/internal/domain"
	"github.com/VictoriaMetrics/vmgather/internal/infrastructure/vm"
)

// VMService interface for VictoriaMetrics operations
type VMService interface {
	// ValidateConnection validates connection to VictoriaMetrics
	ValidateConnection(ctx context.Context, conn domain.VMConnection) error

	// DiscoverComponents discovers VM components in the cluster
	DiscoverComponents(ctx context.Context, conn domain.VMConnection, tr domain.TimeRange) ([]domain.VMComponent, error)

	// GetSample retrieves sample metrics for preview
	GetSample(ctx context.Context, config domain.ExportConfig, limit int) ([]domain.MetricSample, error)

	// EstimateExportSize estimates total series count for export
	EstimateExportSize(ctx context.Context, conn domain.VMConnection, jobs []string, tr domain.TimeRange) (int, error)

	// CheckExportAPI checks if /api/v1/export endpoint is available
	CheckExportAPI(ctx context.Context, conn domain.VMConnection) bool
}

// vmServiceImpl implements VMService
type vmServiceImpl struct {
	clientFactory func(domain.VMConnection) *vm.Client
}

// NewVMService creates a new VM service
func NewVMService() VMService {
	return &vmServiceImpl{
		clientFactory: vm.NewClient,
	}
}

// ValidateConnection validates connection to VictoriaMetrics by executing a simple query
func (s *vmServiceImpl) ValidateConnection(ctx context.Context, conn domain.VMConnection) error {
	client := s.clientFactory(conn)

	// Try to query vm_app_version metric - present in all VM components
	query := "vm_app_version"
	now := time.Now()

	result, err := client.Query(ctx, query, now)
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}

	// Check if we got any results
	if len(result.Data.Result) == 0 {
		return fmt.Errorf("no VM components found - is this a VictoriaMetrics instance?")
	}

	return nil
}

// DiscoverComponents discovers VictoriaMetrics components using vm_app_version metric
func (s *vmServiceImpl) DiscoverComponents(ctx context.Context, conn domain.VMConnection, tr domain.TimeRange) ([]domain.VMComponent, error) {
	client := s.clientFactory(conn)

	// Discovery query: extract component name from version label
	// Example: version="vmstorage-v1.95.1" -> component="vmstorage"
	query := `group by (job, vm_component) (label_replace(vm_app_version{version!=""}, "vm_component", "$1", "version", "(.+?)\\-.*"))`

	result, err := client.Query(ctx, query, tr.End)
	if err != nil {
		return nil, fmt.Errorf("discovery query failed: %w", err)
	}

	if len(result.Data.Result) == 0 {
		return nil, fmt.Errorf("no VM components discovered")
	}

	// Group by component
	componentMap := make(map[string]*domain.VMComponent)

	for _, r := range result.Data.Result {
		component := r.Metric["vm_component"]
		job := r.Metric["job"]

		if component == "" || job == "" {
			continue
		}

		if comp, exists := componentMap[component]; exists {
			comp.Jobs = append(comp.Jobs, job)
		} else {
			componentMap[component] = &domain.VMComponent{
				Component: component,
				Jobs:      []string{job},
			}
		}
	}

	// Convert map to slice and estimate metrics count
	components := make([]domain.VMComponent, 0, len(componentMap))

	for _, comp := range componentMap {
		// Estimate metrics count for this component
		count, err := s.estimateComponentMetrics(ctx, client, comp.Jobs, tr)
		if err != nil {
			// Log error but don't fail - just set -1
			comp.MetricsCountEstimate = -1
		} else {
			comp.MetricsCountEstimate = count
		}

		// Count instances
		comp.InstanceCount, _ = s.countInstances(ctx, client, comp.Jobs, tr)

		// Estimate per-job metrics if possible
		jobMetrics := s.estimateJobMetrics(ctx, client, comp.Jobs, tr)
		if len(jobMetrics) > 0 {
			comp.JobMetrics = jobMetrics
		}

		components = append(components, *comp)
	}

	return components, nil
}

// estimateComponentMetrics estimates the number of metrics for given jobs
func (s *vmServiceImpl) estimateComponentMetrics(ctx context.Context, client *vm.Client, jobs []string, tr domain.TimeRange) (int, error) {
	if len(jobs) == 0 {
		return 0, nil
	}

	// Build job selector: job=~"job1|job2|job3"
	jobRegex := strings.Join(jobs, "|")
	selector := fmt.Sprintf(`{job=~"%s"}`, jobRegex)

	// Count unique series
	query := fmt.Sprintf("count(%s)", selector)

	result, err := client.Query(ctx, query, tr.End)
	if err != nil {
		return 0, err
	}

	if len(result.Data.Result) == 0 {
		return 0, nil
	}

	if len(result.Data.Result[0].Value) < 2 {
		return 0, nil
	}

	if count, ok := parseCountValue(result.Data.Result[0].Value[1]); ok {
		return count, nil
	}

	return 0, nil
}

// countInstances counts unique instances for given jobs
func (s *vmServiceImpl) countInstances(ctx context.Context, client *vm.Client, jobs []string, tr domain.TimeRange) (int, error) {
	if len(jobs) == 0 {
		return 0, nil
	}

	jobRegex := strings.Join(jobs, "|")
	query := fmt.Sprintf(`count(count by (instance) ({job=~"%s"}))`, jobRegex)

	result, err := client.Query(ctx, query, tr.End)
	if err != nil {
		return 0, err
	}

	if len(result.Data.Result) == 0 {
		return 0, nil
	}

	if len(result.Data.Result[0].Value) < 2 {
		return 0, nil
	}

	if count, ok := parseCountValue(result.Data.Result[0].Value[1]); ok {
		return count, nil
	}

	return 0, nil
}

// estimateJobMetrics returns per-job series counts if available
func (s *vmServiceImpl) estimateJobMetrics(ctx context.Context, client *vm.Client, jobs []string, tr domain.TimeRange) map[string]int {
	jobCounts := make(map[string]int)

	if len(jobs) == 0 {
		return jobCounts
	}

	jobRegex := strings.Join(jobs, "|")
	query := fmt.Sprintf(`count by (job) ({job=~"%s"})`, jobRegex)

	result, err := client.Query(ctx, query, tr.End)
	if err != nil || len(result.Data.Result) == 0 {
		return jobCounts
	}

	for _, series := range result.Data.Result {
		job := series.Metric["job"]
		if job == "" || len(series.Value) < 2 {
			continue
		}

		if count, ok := parseCountValue(series.Value[1]); ok {
			jobCounts[job] = count
		}
	}

	return jobCounts
}

// parseCountValue extracts an integer series count from Prometheus API values
func parseCountValue(value interface{}) (int, bool) {
	switch v := value.(type) {
	case string:
		var count int
		if _, err := fmt.Sscanf(v, "%d", &count); err == nil {
			return count, true
		}
	case float64:
		return int(v), true
	}
	return 0, false
}

// GetSample retrieves sample metrics for preview
// Uses instant query with topk() for fast sampling (optimized for performance)
func (s *vmServiceImpl) GetSample(ctx context.Context, config domain.ExportConfig, limit int) ([]domain.MetricSample, error) {
	client := s.clientFactory(config.Connection)

	// Build candidate queries (avoid heavy selector when jobs aren't provided)
	queries := s.buildSampleQueries(config.Jobs, limit)
	var lastErr error

	for _, query := range queries {
		// Execute instant query at current time
		result, err := client.Query(ctx, query, time.Now())
		if err != nil {
			lastErr = err
			continue
		}

		if result.Status != "success" {
			lastErr = fmt.Errorf("query returned non-success status: %s", result.Status)
			continue
		}

		// Parse results into MetricSample format
		samples := make([]domain.MetricSample, 0, len(result.Data.Result))

		for _, r := range result.Data.Result {
			sample := domain.MetricSample{
				MetricName: r.Metric["__name__"],
				Labels:     r.Metric,
			}

			// Extract value from result
			if len(r.Value) >= 2 {
				// Value is [timestamp, value_string]
				if valStr, ok := r.Value[1].(string); ok {
					_, _ = fmt.Sscanf(valStr, "%f", &sample.Value)
				} else if val, ok := r.Value[1].(float64); ok {
					sample.Value = val
				}
			}

			// Extract timestamp
			if len(r.Value) >= 1 {
				if ts, ok := r.Value[0].(float64); ok {
					sample.Timestamp = int64(ts * 1000) // Convert to milliseconds
				}
			}

			samples = append(samples, sample)
		}

		if len(samples) > 0 {
			return samples, nil
		}
		lastErr = fmt.Errorf("no sample metrics found for query %q", query)
	}

	if lastErr != nil {
		return nil, fmt.Errorf("sample query failed: %w", lastErr)
	}
	return nil, fmt.Errorf("sample query failed: no queries executed")
}

// EstimateExportSize estimates total series count for export
func (s *vmServiceImpl) EstimateExportSize(ctx context.Context, conn domain.VMConnection, jobs []string, tr domain.TimeRange) (int, error) {
	client := s.clientFactory(conn)
	return s.estimateComponentMetrics(ctx, client, jobs, tr)
}

func (s *vmServiceImpl) buildSampleQueries(jobs []string, limit int) []string {
	if limit <= 0 {
		limit = 10
	}
	if len(jobs) == 0 {
		// Avoid heavy scan over all series; vm_app_version is guaranteed by discovery.
		return []string{
			fmt.Sprintf("topk(%d, vm_app_version)", limit),
		}
	}

	selector := s.buildSampleSelector(jobs)
	return []string{fmt.Sprintf("topk(%d, %s)", limit, selector)}
}

func (s *vmServiceImpl) buildSampleSelector(jobs []string) string {
	if len(jobs) == 0 {
		return "vm_app_version"
	}
	parts := make([]string, 0, len(jobs))
	for _, job := range jobs {
		parts = append(parts, fmt.Sprintf(`job=%q`, job))
	}
	return fmt.Sprintf("{%s}", strings.Join(parts, " or "))
}

// CheckExportAPI checks if /api/v1/export endpoint is available
// Returns true if export API works, false if it returns "missing route" or other errors
func (s *vmServiceImpl) CheckExportAPI(ctx context.Context, conn domain.VMConnection) bool {
	client := s.clientFactory(conn)

	// Try a minimal export request to check if endpoint exists
	// Use a very short time range and simple match to minimize data transfer
	start := time.Now().Add(-1 * time.Minute)
	end := time.Now()

	// Try to export a single metric (up is commonly available)
	selector := "up"

	_, err := client.Export(ctx, selector, start, end)

	if err != nil {
		errMsg := strings.ToLower(err.Error())

		// Check for "missing route" error - this means export API is not configured
		if strings.Contains(errMsg, "missing route") {
			return false
		}

		// Check for 404 - endpoint not found
		if strings.Contains(errMsg, "404") || strings.Contains(errMsg, "not found") {
			return false
		}

		// Other errors (auth, timeout, etc.) don't necessarily mean export is unavailable
		// The endpoint exists, just failed for other reasons
		// We'll consider this as "export available but failed"
		return true
	}

	// Export succeeded - API is available
	return true
}
