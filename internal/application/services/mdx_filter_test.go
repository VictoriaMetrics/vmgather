package services

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/VictoriaMetrics/vmgather/internal/domain"
	"github.com/VictoriaMetrics/vmgather/internal/infrastructure/obfuscation"
)

func mdxLine(labels string) string {
	return `{"metric":{` + labels + `},"values":[1],"timestamps":[1700000000000]}` + "\n"
}

// runMDX primes a fresh MDX processor with prime and processes data.
func runMDX(t *testing.T, cfg domain.ExportConfig, prime, data string) (string, *lineProcessor) {
	t.Helper()
	cfg.MDXFilter = true
	proc := newLineProcessor(cfg)
	t.Cleanup(proc.close)
	if err := proc.mdx.prime(strings.NewReader(prime)); err != nil {
		t.Fatalf("prime: %v", err)
	}
	var out bytes.Buffer
	if _, err := proc.process(strings.NewReader(data), &out); err != nil {
		t.Fatalf("process: %v", err)
	}
	return out.String(), proc
}

func TestMDXFilterKeepsOnlyVictoriaMetricsInstances(t *testing.T) {
	prime := mdxLine(`"__name__":"vm_app_version","job":"vmstorage","instance":"a:8482","version":"vmstorage-20261106-v1.156.0"`)
	data := mdxLine(`"__name__":"vm_rows","job":"vmstorage","instance":"a:8482"`) +
		// Same job, other instance without vm_app_version: not a VictoriaMetrics process.
		mdxLine(`"__name__":"node_load1","job":"vmstorage","instance":"b:9100"`) +
		// No instance label: lib/mdx always drops it.
		mdxLine(`"__name__":"vm_rows","job":"vmstorage"`) +
		// vmestimator cardinality estimates are excluded from MDX.
		mdxLine(`"__name__":"cardinality_estimate","job":"vmstorage","instance":"a:8482"`)

	out, proc := runMDX(t, domain.ExportConfig{}, prime, data)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], `"vm_rows"`) || !strings.Contains(lines[0], `"instance":"a:8482"`) {
		t.Fatalf("want only vm_rows of a:8482, got:\n%s", out)
	}
	if !strings.Contains(lines[0], `"victoriametrics_app":"true"`) {
		t.Fatalf("lib/mdx label not added: %s", lines[0])
	}
	if proc.mdx.dropped != 3 {
		t.Fatalf("dropped = %d; want 3", proc.mdx.dropped)
	}
}

// Each window is decided by its own vm_app_version series: an instance seen in an earlier window is
// forgotten. The drop count covers the whole export, not only the last window.
func TestMDXFilterPrimeReplacesInstances(t *testing.T) {
	data := mdxLine(`"__name__":"vm_rows","job":"vmstorage","instance":"a:8482"`)
	out, proc := runMDX(t, domain.ExportConfig{}, mdxLine(`"__name__":"vm_app_version","job":"vmstorage","instance":"a:8482"`), data)
	if !strings.Contains(out, `"vm_rows"`) {
		t.Fatalf("first window: want vm_rows of a:8482, got %q", out)
	}
	for window := 2; window <= 3; window++ {
		// a:8482 has no vm_app_version in the later windows.
		if err := proc.mdx.prime(strings.NewReader(mdxLine(`"__name__":"vm_app_version","job":"vmstorage","instance":"b:8482"`))); err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		if _, err := proc.process(strings.NewReader(data), &buf); err != nil {
			t.Fatal(err)
		}
		if buf.Len() != 0 {
			t.Fatalf("window %d kept a series of an instance from an earlier window: %q", window, buf.String())
		}
	}
	if proc.mdx.dropped != 2 {
		t.Fatalf("dropped = %d; want 2 (one per later window)", proc.mdx.dropped)
	}
}

// Without priming the filter knows no instance and drops everything, so ExecuteExport must prime it.
func TestMDXFilterNeedsPriming(t *testing.T) {
	out, _ := runMDX(t, domain.ExportConfig{}, "", mdxLine(`"__name__":"vm_rows","job":"vmstorage","instance":"a:8482"`))
	if out != "" {
		t.Fatalf("unprimed filter kept %q", out)
	}
}

func TestMDXFilterLabelRules(t *testing.T) {
	data := mdxLine(`"__name__":"process_cpu_seconds_total","job":"app","instance":"c:8080","victoriametrics_app":"true"`) +
		mdxLine(`"__name__":"process_cpu_seconds_total","job":"other","instance":"d:8080","victoriametrics_app":"false"`)
	out, _ := runMDX(t, domain.ExportConfig{}, "", data)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], `"instance":"c:8080"`) {
		t.Fatalf("want only the series that already has victoriametrics_app=true, got:\n%s", out)
	}
	if strings.Count(lines[0], "victoriametrics_app") != 1 {
		t.Fatalf("label duplicated: %s", lines[0])
	}

	// A vm_app_version series registers its instance even if it carries another victoriametrics_app value;
	// the kept series get the value replaced, never a second label.
	prime := mdxLine(`"__name__":"vm_app_version","job":"vmagent","instance":"e:8429","victoriametrics_app":"no"`)
	out, _ = runMDX(t, domain.ExportConfig{}, prime, mdxLine(`"__name__":"vmagent_rows","job":"vmagent","instance":"e:8429","victoriametrics_app":"no"`))
	if strings.Count(out, "victoriametrics_app") != 1 || !strings.Contains(out, `"victoriametrics_app":"true"`) {
		t.Fatalf("want one victoriametrics_app=true, got %s", out)
	}
}

func TestMDXFilterTenants(t *testing.T) {
	prime := mdxLine(`"__name__":"vm_app_version","job":"vmstorage","instance":"a:8482","vm_account_id":"1","vm_project_id":"0"`)
	data := mdxLine(`"__name__":"vm_rows","job":"vmstorage","instance":"a:8482","vm_account_id":"1","vm_project_id":"0"`) +
		mdxLine(`"__name__":"vm_rows","job":"vmstorage","instance":"a:8482","vm_account_id":"2","vm_project_id":"0"`)
	out, _ := runMDX(t, domain.ExportConfig{}, prime, data)
	if strings.Count(out, "\n") != 1 || !strings.Contains(out, `"vm_account_id":"1"`) {
		t.Fatalf("want only tenant 1, got:\n%s", out)
	}
}

// The MDX filter runs on the original labels, before obfuscation, like in vmagent.
func TestMDXFilterRunsBeforeObfuscation(t *testing.T) {
	cfg := domain.ExportConfig{Obfuscation: domain.ObfuscationConfig{Enabled: true, ObfuscateInstance: true, ObfuscateJob: true}}
	prime := mdxLine(`"__name__":"vm_app_version","job":"vmstorage","instance":"a:8482"`)
	out, _ := runMDX(t, cfg, prime, mdxLine(`"__name__":"vm_rows","job":"vmstorage","instance":"a:8482"`))
	if !strings.Contains(out, obfuscation.Value("a:8482")) || !strings.Contains(out, obfuscation.Value("vmstorage")) {
		t.Fatalf("labels not obfuscated after the filter: %s", out)
	}
	if strings.Contains(out, "a:8482") {
		t.Fatalf("real instance left: %s", out)
	}
}

func TestMDXFilterRejectsNonStringLabels(t *testing.T) {
	cfg := domain.ExportConfig{MDXFilter: true}
	proc := newLineProcessor(cfg)
	defer proc.close()
	var out bytes.Buffer
	_, err := proc.process(strings.NewReader(mdxLine(`"__name__":"vm_rows","job":1,"instance":"a"`)), &out)
	if err == nil {
		t.Fatalf("want a decode error")
	}
}

func TestValidateExportConfigMDXNeedsClusterMode(t *testing.T) {
	err := ValidateExportConfig(domain.ExportConfig{Mode: domain.ExportModeCustom, Query: `{job="x"}`, MDXFilter: true})
	if err == nil {
		t.Fatalf("want an error for the MDX filter in custom mode")
	}
}

// metadata.json must not carry real job names next to obfuscated job labels.
func TestArchiveMetadataObfuscatesJobs(t *testing.T) {
	s := &exportServiceImpl{}
	cfg := domain.ExportConfig{
		Jobs:        []string{"vmstorage-prod", "vmselect-prod"},
		Obfuscation: domain.ObfuscationConfig{Enabled: true, ObfuscateJob: true},
	}
	got := s.buildArchiveMetadata("id", cfg, 1, nil).Jobs
	want := []string{obfuscation.Value("vmstorage-prod"), obfuscation.Value("vmselect-prod")}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("jobs = %v; want %v", got, want)
	}

	cfg.Obfuscation.ObfuscateJob = false
	cfg.Obfuscation.ObfuscateInstance = true
	if got := s.buildArchiveMetadata("id", cfg, 1, nil).Jobs; got[0] != "vmstorage-prod" {
		t.Fatalf("jobs must stay readable when the job label is not obfuscated: %v", got)
	}
}

// End to end: ExecuteExport primes the filter with vm_app_version of the same window and job matcher
// before every batch, and writes only MDX series plus mdx_filtered to the archive.
func TestExecuteExportWithMDXFilter(t *testing.T) {
	var mu sync.Mutex
	var selectors []string
	srv := newIPv4Server(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		selector := r.Form.Get("match[]")
		mu.Lock()
		selectors = append(selectors, selector)
		mu.Unlock()
		if strings.HasPrefix(selector, "vm_app_version") {
			_, _ = w.Write([]byte(mdxLine(`"__name__":"vm_app_version","job":"vmstorage","instance":"a:8482"`)))
			return
		}
		_, _ = w.Write([]byte(mdxLine(`"__name__":"vm_rows","job":"vmstorage","instance":"a:8482"`) +
			mdxLine(`"__name__":"node_load1","job":"vmstorage","instance":"b:9100"`)))
	}))
	defer srv.Close()

	dir := t.TempDir()
	cfg := regressionConfig(srv.URL, dir)
	cfg.MDXFilter = true
	res, err := NewExportService(dir, "test").ExecuteExport(context.Background(), cfg)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	metrics := readArchiveMetrics(t, res.ArchivePath)
	if len(metrics) != 2 { // one kept series per 60s batch, two batches
		t.Fatalf("want 2 kept lines, got %d: %v", len(metrics), metrics)
	}
	for _, m := range metrics {
		labels := m["metric"].(map[string]interface{})
		if labels["instance"] != "a:8482" || labels["victoriametrics_app"] != "true" {
			t.Fatalf("unexpected series %v", labels)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(selectors) != 4 || selectors[0] != `vm_app_version{job=~"vmstorage"}` || selectors[2] != `vm_app_version{job=~"vmstorage"}` {
		t.Fatalf("want a vm_app_version request before each batch, got %q", selectors)
	}
}

// A failed priming request must fail the export instead of producing an empty, "successful" bundle.
func TestExecuteExportFailsWhenPrimingFails(t *testing.T) {
	srv := newIPv4Server(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if strings.HasPrefix(r.Form.Get("match[]"), "vm_app_version") {
			http.Error(w, "boom", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(mdxLine(`"__name__":"vm_rows","job":"vmstorage","instance":"a:8482"`)))
	}))
	defer srv.Close()
	dir := t.TempDir()
	cfg := regressionConfig(srv.URL, dir)
	cfg.MDXFilter = true
	if _, err := NewExportService(dir, "test").ExecuteExport(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "MDX filter") {
		t.Fatalf("want a priming error, got %v", err)
	}
}
