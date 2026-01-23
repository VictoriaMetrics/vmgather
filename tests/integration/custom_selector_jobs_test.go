//go:build integration && realdiscovery

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/VictoriaMetrics/vmgather/internal/application/services"
	"github.com/VictoriaMetrics/vmgather/internal/domain"
)

func TestCustomSelectorJobsIncludeTestData(t *testing.T) {
	vmSvc := services.NewVMService()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	tr := domain.TimeRange{
		Start: time.Now().Add(-15 * time.Minute),
		End:   time.Now(),
	}
	conn := domain.VMConnection{
		URL:         vmSingleNoAuthURL(),
		ApiBasePath: "",
		Auth:        domain.AuthConfig{Type: domain.AuthTypeNone},
	}

	components, err := vmSvc.DiscoverComponents(ctx, conn, tr)
	if err != nil {
		t.Fatalf("component discovery failed: %v", err)
	}
	for _, comp := range components {
		for _, job := range comp.Jobs {
			if job == "test1" || job == "test2" {
				t.Fatalf("unexpected test job %q in cluster metrics discovery", job)
			}
		}
	}

	jobs, err := vmSvc.DiscoverSelectorJobs(ctx, conn, `{job="test1"}`, tr)
	if err != nil {
		t.Fatalf("selector discovery failed: %v", err)
	}
	found := false
	for _, job := range jobs {
		if job.Job == "test1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected test1 to be discoverable via selector")
	}
}
