package services

import (
	"testing"
	"time"

	"github.com/VictoriaMetrics/vmgather/internal/domain"
)

func TestApplyExportDefaults_PreservesDropLabels(t *testing.T) {
	cfg := domain.ExportConfig{
		TimeRange: domain.TimeRange{
			Start: time.Now().Add(-time.Hour),
			End:   time.Now(),
		},
		Obfuscation: domain.ObfuscationConfig{
			Enabled:    false,
			DropLabels: []string{"env", "job"},
		},
	}

	ApplyExportDefaults(&cfg)

	if cfg.MetricStepSeconds == 0 {
		t.Fatalf("expected metric step to be set")
	}
	if len(cfg.Obfuscation.DropLabels) != 2 {
		t.Fatalf("expected drop labels to be preserved, got %v", cfg.Obfuscation.DropLabels)
	}
}
