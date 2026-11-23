package services

import (
	"fmt"
	"io"

	"github.com/valyala/fastjson"

	"github.com/VictoriaMetrics/vmgather/internal/domain"
	"github.com/VictoriaMetrics/vmgather/internal/infrastructure/obfuscation"
	"github.com/VictoriaMetrics/vmgather/internal/infrastructure/vm"
)

// lineProcessor applies the per-line changes of one export, in the same order as vmagent:
// the MDX filter on the original labels, then dropped labels, then obfuscation.
type lineProcessor struct {
	mdx        *mdxFilter
	dropLabels []string
	obfuscate  *obfuscation.Labels
}

func newLineProcessor(config domain.ExportConfig) *lineProcessor {
	p := &lineProcessor{
		dropLabels: config.Obfuscation.DropLabels,
		obfuscate:  obfuscation.NewLabels(config.Obfuscation.LabelNames()),
	}
	if config.MDXFilter {
		p.mdx = newMDXFilter()
	}
	return p
}

// close releases the MDX filter. It is safe to call on a nil processor.
func (p *lineProcessor) close() {
	if p != nil && p.mdx != nil {
		p.mdx.close()
	}
}

// passthrough is true when lines are copied unchanged, which skips JSON parsing.
func (p *lineProcessor) passthrough() bool {
	return p == nil || (p.mdx == nil && len(p.dropLabels) == 0 && p.obfuscate == nil)
}

// process reads export lines from r, changes them and writes the kept lines to w.
// It returns the number of written lines.
func (p *lineProcessor) process(r io.Reader, w io.Writer) (int, error) {
	if p.passthrough() {
		count, err := vm.CopyLines(r, w)
		if err != nil {
			return 0, fmt.Errorf("copy error: %w", err)
		}
		return count, nil
	}

	// Only the "metric" object changes; "values" and "timestamps" pass through untouched.
	scanner := vm.NewLineScanner(r)
	var parser fastjson.Parser
	var arena fastjson.Arena
	var buf []byte
	written := 0
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		arena.Reset()
		v, err := parser.ParseBytes(line)
		if err != nil {
			return 0, fmt.Errorf("decode error: %w", err)
		}
		keep, err := p.transform(v.GetObject("metric"), &arena)
		if err != nil {
			return 0, err
		}
		if !keep {
			continue
		}
		buf = v.MarshalTo(buf[:0])
		buf = append(buf, '\n')
		if _, err := w.Write(buf); err != nil {
			return 0, fmt.Errorf("write error: %w", err)
		}
		written++
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("decode error: %w", err)
	}
	return written, nil
}

func (p *lineProcessor) transform(metric *fastjson.Object, arena *fastjson.Arena) (bool, error) {
	if metric == nil {
		// lib/mdx drops series without job and instance labels.
		return p.mdx == nil, nil
	}
	if p.mdx != nil {
		keep, setApp, err := p.mdx.keep(metric)
		if err != nil || !keep {
			return false, err
		}
		if setApp {
			metric.Set(mdxAppLabel, arena.NewString("true"))
		}
	}
	for _, name := range p.dropLabels {
		metric.Del(name)
	}
	if p.obfuscate != nil {
		for _, name := range p.obfuscate.Names() {
			v := metric.Get(name)
			if v == nil {
				continue
			}
			value, err := v.StringBytes()
			if err != nil {
				return false, fmt.Errorf("decode error: invalid value of label %q: %w", name, err)
			}
			metric.Set(name, arena.NewString(p.obfuscate.ValueBytes(value)))
		}
	}
	return true, nil
}
