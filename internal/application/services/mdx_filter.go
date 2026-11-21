package services

import (
	"context"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/bytesutil"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/mdx"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/prompb"
	"github.com/valyala/fastjson"

	"github.com/VictoriaMetrics/vmgather/internal/domain"
	"github.com/VictoriaMetrics/vmgather/internal/infrastructure/vm"
)

const (
	// mdxAppLabel is the label lib/mdx adds to every series it keeps.
	mdxAppLabel = "victoriametrics_app"
	// mdxPrimeMetric registers VictoriaMetrics instances in the filter before the main export.
	mdxPrimeMetric = "vm_app_version"
)

// mdxFilter keeps the same series as vmagent -remoteWrite.mdx.enable, by running VictoriaMetrics lib/mdx
// on every exported line. lib/mdx keeps a series of a job+instance(+tenant) only after it saw a
// vm_app_version series (or a victoriametrics_app="true" label) of that instance, so the filter must be
// primed with the vm_app_version series of the window first.
type mdxFilter struct {
	filter *mdx.Filter
	labels []prompb.Label
	tss    [1]prompb.TimeSeries
	// dropped counts series removed by the filter, for logs.
	dropped int
}

func newMDXFilter() *mdxFilter {
	return &mdxFilter{filter: mdx.NewFilter()}
}

// keep runs lib/mdx for one series. setApp is true when lib/mdx added victoriametrics_app="true",
// which the caller must also set on the exported line.
//
// Label strings point into the parsed line; lib/mdx copies what it keeps, and the result is read
// before the next line is parsed.
func (f *mdxFilter) keep(metric *fastjson.Object) (keep, setApp bool, err error) {
	labels := f.labels[:0]
	metric.Visit(func(key []byte, v *fastjson.Value) {
		if err != nil {
			return
		}
		value, e := v.StringBytes()
		if e != nil {
			err = fmt.Errorf("decode error: invalid value of label %q: %w", key, e)
			return
		}
		labels = append(labels, prompb.Label{
			Name:  bytesutil.ToUnsafeString(key),
			Value: bytesutil.ToUnsafeString(value),
		})
	})
	f.labels = labels
	if err != nil {
		return false, false, err
	}

	ctx := mdx.GetContext()
	f.tss[0] = prompb.TimeSeries{Labels: labels}
	out := f.filter.Filter(ctx, f.tss[:])
	if len(out) > 0 {
		keep = true
		setApp = len(out[0].Labels) > len(labels)
	}
	f.tss[0] = prompb.TimeSeries{}
	mdx.PutContext(ctx)
	if !keep {
		f.dropped++
	}
	return keep, setApp, nil
}

// prime registers the instances of the vm_app_version series read from r.
func (f *mdxFilter) prime(r io.Reader) error {
	scanner := vm.NewLineScanner(r)
	var parser fastjson.Parser
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		v, err := parser.ParseBytes(line)
		if err != nil {
			return fmt.Errorf("decode error: %w", err)
		}
		if metric := v.GetObject("metric"); metric != nil {
			if _, _, err := f.keep(metric); err != nil {
				return err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("decode error: %w", err)
	}
	f.dropped = 0
	return nil
}

func (f *mdxFilter) close() {
	f.filter.MustStop()
}

// primeMDX loads the vm_app_version series of the window into the filter; it does nothing without one.
// Transient errors are retried like export attempts; any other error fails the export, because an
// unprimed filter would silently drop every series. lib/mdx forgets an instance after one hour without
// a hit, so priming runs before every batch.
func (s *exportServiceImpl) primeMDX(ctx context.Context, client *vm.Client, config domain.ExportConfig, f *mdxFilter, window domain.TimeRange) error {
	if f == nil {
		return nil
	}
	selector, ok := applyJobMatcherToSelector(mdxPrimeMetric, config.Jobs)
	if !ok {
		selector = mdxPrimeMetric
	}
	for retry := 0; ; retry++ {
		err := s.primeMDXOnce(ctx, client, selector, f, window)
		if err == nil {
			log.Printf("[MDX] %d VictoriaMetrics instances known for %s..%s",
				f.filter.VMInstancesCount(), window.Start.Format(time.RFC3339), window.End.Format(time.RFC3339))
			return nil
		}
		if vm.ErrorKindOf(err) != vm.ErrorKindTransient || retry >= len(transientRetryDelays) {
			return fmt.Errorf("cannot read %s for the MDX filter: %w", mdxPrimeMetric, err)
		}
		if err := sleepContext(ctx, transientRetryDelays[retry]); err != nil {
			return err
		}
	}
}

func (s *exportServiceImpl) primeMDXOnce(ctx context.Context, client *vm.Client, selector string, f *mdxFilter, window domain.TimeRange) error {
	reader, err := client.Export(ctx, selector, window.Start, window.End)
	if err != nil {
		return err
	}
	err = f.prime(reader)
	if closeErr := reader.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	return err
}
