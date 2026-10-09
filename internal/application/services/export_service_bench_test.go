package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"testing"

	"github.com/VictoriaMetrics/vmgather/internal/domain"
	"github.com/VictoriaMetrics/vmgather/internal/infrastructure/obfuscation"
	"github.com/VictoriaMetrics/vmgather/internal/infrastructure/vm"
)

// BenchmarkProcessMetricsIntoWriter compares processing CPU and allocations
// with the decode-and-marshal pipeline used before the export optimizations.
// Output goes to io.Discard, so this does not measure disk or network throughput.
// Single-line cases expose parser setup costs; streams exercise parser reuse.
func BenchmarkProcessMetricsIntoWriter(b *testing.B) {
	modes := []struct {
		name   string
		config domain.ObfuscationConfig
	}{
		{name: "passthrough"},
		{name: "drop", config: domain.ObfuscationConfig{DropLabels: []string{"namespace"}}},
		{name: "obfuscate", config: domain.ObfuscationConfig{
			Enabled: true, ObfuscateInstance: true, ObfuscateJob: true,
			CustomLabels: []string{"namespace", "pod"},
		}},
	}
	for _, samples := range []int{1, 100, 10000} {
		metric := vm.ExportedMetric{
			Metric: map[string]string{
				"__name__": "vm_app_version", "instance": "10.20.30.40:8482",
				"job": "storage-prod", "component": "vmstorage",
				"namespace": "monitoring", "pod": "vmstorage-0", "env": "prod",
			},
			Values: make([]interface{}, samples), Timestamps: make([]int64, samples),
		}
		for i := range metric.Values {
			metric.Values[i] = float64(i%37) / 8
			metric.Timestamps[i] = 1699728000000 + int64(i)*30000
		}
		line, err := json.Marshal(metric)
		if err != nil {
			b.Fatal(err)
		}
		line = append(line, '\n')
		for _, lines := range []int{1, 64} {
			data := bytes.Repeat(line, lines)
			for _, mode := range modes {
				for _, baseline := range []bool{true, false} {
					implementation := "optimized"
					if baseline {
						implementation = "baseline"
					}
					name := fmt.Sprintf("samples=%d/lines=%d/%s/%s", samples, lines, mode.name, implementation)
					b.Run(name, func(b *testing.B) {
						service := &exportServiceImpl{}
						obfuscator := obfuscation.NewObfuscator()
						b.SetBytes(int64(len(data)))
						b.ReportAllocs()
						b.ResetTimer()
						for i := 0; i < b.N; i++ {
							reader := bytes.NewReader(data)
							var count int
							var err error
							if baseline {
								count, err = benchmarkDecodeAndMarshal(service, reader, mode.config, obfuscator)
							} else {
								count, err = service.processMetricsIntoWriter(reader, mode.config, obfuscator, io.Discard)
							}
							if err != nil || count != lines {
								b.Fatalf("count=%d, want=%d, err=%v", count, lines, err)
							}
						}
					})
				}
			}
		}
	}
}

func benchmarkDecodeAndMarshal(service *exportServiceImpl, reader io.Reader, config domain.ObfuscationConfig, obfuscator *obfuscation.Obfuscator) (int, error) {
	decoder := vm.NewExportDecoder(reader)
	count := 0
	for {
		metric, err := decoder.Decode()
		if err == io.EOF {
			return count, nil
		}
		if err != nil {
			return 0, err
		}
		for _, label := range config.DropLabels {
			delete(metric.Metric, label)
		}
		if config.Enabled {
			service.applyObfuscation(metric, obfuscator, config)
		}
		data, err := json.Marshal(metric)
		if err != nil {
			return 0, err
		}
		if _, err := io.Discard.Write(data); err != nil {
			return 0, err
		}
		if _, err := io.Discard.Write([]byte{'\n'}); err != nil {
			return 0, err
		}
		count++
	}
}
