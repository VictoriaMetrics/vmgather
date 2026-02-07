package domain

import "time"

// TimeRange represents a time interval for metrics export
type TimeRange struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// AuthType defines the authentication method
type AuthType string

const (
	AuthTypeNone   AuthType = "none"
	AuthTypeBasic  AuthType = "basic"
	AuthTypeBearer AuthType = "bearer"
	AuthTypeHeader AuthType = "header"
)

// ExportMode defines the high-level collection mode
type ExportMode string

const (
	ExportModeCluster ExportMode = "cluster"
	ExportModeCustom  ExportMode = "custom"
)

// QueryMode defines the query style for custom mode
type QueryMode string

const (
	QueryModeSelector  QueryMode = "selector"
	QueryModeMetricsQL QueryMode = "metricsql"
)

// AuthConfig contains authentication settings
type AuthConfig struct {
	Type        AuthType `json:"type"`
	Username    string   `json:"username,omitempty"`
	Password    string   `json:"password,omitempty"`
	Token       string   `json:"token,omitempty"`
	HeaderName  string   `json:"header_name,omitempty"`
	HeaderValue string   `json:"header_value,omitempty"`
}

// VMConnection represents connection settings to VictoriaMetrics
type VMConnection struct {
	URL           string     `json:"url"`
	ApiBasePath   string     `json:"api_base_path,omitempty"`  // e.g., "/select/0/prometheus" or "/1011/prometheus"
	TenantId      string     `json:"tenant_id,omitempty"`      // e.g., "0" or "1011"
	IsMultitenant bool       `json:"is_multitenant,omitempty"` // true for /select/multitenant endpoints
	FullApiUrl    string     `json:"full_api_url,omitempty"`   // Complete URL with base path
	Auth          AuthConfig `json:"auth"`
	SkipTLSVerify bool       `json:"skip_tls_verify"`
	Debug         bool       `json:"debug,omitempty"`
}

// VMComponent represents a discovered VictoriaMetrics component
type VMComponent struct {
	Component            string         `json:"component"`
	Jobs                 []string       `json:"jobs"`
	InstanceCount        int            `json:"instance_count"`
	MetricsCountEstimate int            `json:"metrics_count_estimate"`
	JobMetrics           map[string]int `json:"job_metrics,omitempty"`
}

// SelectorJob represents a job discovered by selector-based discovery
type SelectorJob struct {
	Job                  string `json:"job"`
	InstanceCount        int    `json:"instance_count"`
	MetricsCountEstimate int    `json:"metrics_count_estimate,omitempty"`
}

// BatchSettings controls batching for long-running exports
type BatchSettings struct {
	Enabled            bool   `json:"enabled"`
	Strategy           string `json:"strategy,omitempty"` // e.g. "auto"
	CustomIntervalSecs int    `json:"custom_interval_seconds,omitempty"`
}

// MetricSample represents a sample metric for preview
type MetricSample struct {
	MetricName string            `json:"metric_name"`
	Labels     map[string]string `json:"labels"`
	Value      float64           `json:"value"`
	Timestamp  int64             `json:"timestamp"`
}

// ObfuscationConfig contains obfuscation settings
type ObfuscationConfig struct {
	Enabled           bool     `json:"enabled"`
	ObfuscateInstance bool     `json:"obfuscate_instance"`
	ObfuscateJob      bool     `json:"obfuscate_job"`
	PreserveStructure bool     `json:"preserve_structure"`
	CustomLabels      []string `json:"custom_labels,omitempty"` // Additional labels to obfuscate (pod, namespace, etc.)
	DropLabels        []string `json:"drop_labels,omitempty"`   // Labels removed from export
}

// OutputSettings defines export output configuration
type OutputSettings struct {
	Format      string `json:"format"`      // "jsonl"
	Compression string `json:"compression"` // "gzip"
	ArchiveName string `json:"archive_name"`
}

// ExportConfig contains full export configuration
type ExportConfig struct {
	Connection        VMConnection      `json:"connection"`
	TimeRange         TimeRange         `json:"time_range"`
	Components        []string          `json:"components"`
	Jobs              []string          `json:"jobs"`
	Mode              ExportMode        `json:"mode,omitempty"`
	QueryType         QueryMode         `json:"query_type,omitempty"`
	Query             string            `json:"query,omitempty"`
	Obfuscation       ObfuscationConfig `json:"obfuscation"`
	Batching          BatchSettings     `json:"batching"`
	StagingDir        string            `json:"staging_dir,omitempty"`
	StagingFile       string            `json:"staging_file,omitempty"`
	ResumeFromBatch   int               `json:"resume_from_batch,omitempty"`
	MetricStepSeconds int               `json:"metric_step_seconds,omitempty"`
	OutputSettings    OutputSettings    `json:"output_settings"`
}

// ExportResult represents the result of an export operation
type ExportResult struct {
	ExportID           string    `json:"export_id"`
	ArchivePath        string    `json:"archive_path"`
	ArchiveName        string    `json:"archive_name"`
	ArchiveSizeBytes   int64     `json:"archive_size_bytes"`
	MetricsExported    int       `json:"metrics_exported"`
	TimeRange          TimeRange `json:"time_range"`
	ObfuscationApplied bool      `json:"obfuscation_applied"`
	SHA256             string    `json:"sha256"`
}
