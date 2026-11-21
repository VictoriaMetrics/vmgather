package services

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/VictoriaMetrics/vmgather/internal/domain"
	"github.com/VictoriaMetrics/vmgather/internal/infrastructure/vm"
)

// Real version labels from a lab (vmsingle/vmagent v1.155.0, VictoriaLogs v1.53.0, operator v0.75.2).
// The old MetricsQL regex "(.+?)\-.*" mapped both victoria-metrics and victoria-logs to "victoria".
func TestComponentFromVersion(t *testing.T) {
	cases := map[string]string{
		"victoria-metrics-20261023-161715-tags-v1.155.0-0-g3fec2f7241":           "victoria-metrics",
		"victoria-logs-20261001-105813-tags-v1.53.0-0-g915d91904b":               "victoria-logs",
		"vmagent-20261023-161803-tags-v1.155.0-0-g3fec2f7241":                    "vmagent",
		"vmstorage-20261023-161803-tags-v1.155.0-cluster-0-g3fec2f7241":          "vmstorage",
		"config-reloader-20261028-075425-config-reloader-v0.75.2":                "config-reloader",
		"operator-20261028-074017-v0.75.2":                                       "operator",
		"victoria-metrics-20240101-000000-tags-v1.97.1-enterprise-0-g1234567890": "victoria-metrics",
		"vmstorage-v1.95.1": "vmstorage", // no build date: old behavior
		"v1.53.0":           "",          // no component at all
		"":                  "",
	}
	for version, want := range cases {
		if got := componentFromVersion(version); got != want {
			t.Errorf("componentFromVersion(%q) = %q, want %q", version, got, want)
		}
	}
}

// During a rolling upgrade one job reports two versions; it must be listed once.
func TestDiscoverComponents_DedupesJobsAcrossVersions(t *testing.T) {
	srv := newIPv4Server(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		result := []map[string]interface{}{}
		switch {
		case strings.HasPrefix(query, "group by (job, version)"):
			for _, v := range []string{"vmagent-20261023-161803-tags-v1.155.0-0-gaaaa", "vmagent-20261101-101010-tags-v1.156.0-0-gbbbb"} {
				result = append(result, map[string]interface{}{"metric": map[string]string{"job": "vmagent", "version": v}})
			}
		case strings.HasPrefix(query, "count"):
			result = append(result, map[string]interface{}{"metric": map[string]string{"job": "vmagent"}, "value": []interface{}{float64(time.Now().Unix()), "1"}})
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "success", "data": map[string]interface{}{"resultType": "vector", "result": result}})
	}))
	defer srv.Close()

	svc := &vmServiceImpl{clientFactory: vm.NewClient}
	comps, err := svc.DiscoverComponents(context.Background(), domain.VMConnection{URL: srv.URL}, domain.TimeRange{Start: time.Now().Add(-time.Hour), End: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if len(comps) != 1 || len(comps[0].Jobs) != 1 {
		t.Fatalf("want 1 component with 1 job, got %+v", comps)
	}
}

// CLI oneshot (-export-stdout) uses ExportToWriter; cluster mode without jobs must fail before any request.
func TestExportToWriter_ClusterWithoutJobsIsRejected(t *testing.T) {
	srv := newIPv4Server(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s", r.URL.Path)
	}))
	defer srv.Close()

	cfg := domain.ExportConfig{Connection: domain.VMConnection{URL: srv.URL}, TimeRange: domain.TimeRange{Start: time.Now().Add(-time.Minute), End: time.Now()}}
	var buf bytes.Buffer
	if _, err := ExportToWriter(context.Background(), cfg, &buf); err == nil {
		t.Fatal("expected an error")
	}
}

// Discovery must cover the whole range (a component that stopped before the end is still exported) and
// must not accept a partial answer from a cluster with an unavailable vmstorage.
func TestDiscoverVMJobs_CoversRangeAndDeniesPartialResponses(t *testing.T) {
	var query, deny string
	srv := newIPv4Server(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query, deny = r.URL.Query().Get("query"), r.URL.Query().Get("deny_partial_response")
		result := []map[string]interface{}{
			{"metric": map[string]string{"job": "vmstorage-a", "version": "vmstorage-20261023-161803-tags-v1.155.0-cluster-0-gaaaa"}},
			{"metric": map[string]string{"job": "vmselect-a", "version": "vmselect-20261023-161803-tags-v1.155.0-cluster-0-gaaaa"}},
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "success", "data": map[string]interface{}{"resultType": "vector", "result": result}})
	}))
	defer srv.Close()

	end := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	comps, err := DiscoverVMJobs(context.Background(), domain.VMConnection{URL: srv.URL}, domain.TimeRange{Start: end.Add(-time.Hour), End: end})
	if err != nil {
		t.Fatal(err)
	}
	if want := `group by (job, version) (last_over_time(vm_app_version{version!=""}[3600s]))`; query != want {
		t.Fatalf("query = %q; want %q", query, want)
	}
	if deny != "1" {
		t.Fatalf("deny_partial_response = %q; want 1", deny)
	}
	if len(comps) != 2 || comps[0].Component != "vmselect" || comps[1].Component != "vmstorage" {
		t.Fatalf("components = %+v", comps)
	}

	// No components is not an error for snapshot mode: the window is simply empty.
	empty := newIPv4Server(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	defer empty.Close()
	comps, err = DiscoverVMJobs(context.Background(), domain.VMConnection{URL: empty.URL}, domain.TimeRange{Start: end.Add(-time.Hour), End: end})
	if err != nil || len(comps) != 0 {
		t.Fatalf("empty discovery: comps=%v err=%v", comps, err)
	}
}
