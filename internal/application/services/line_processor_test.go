package services

import (
	"bytes"
	"io"

	"github.com/VictoriaMetrics/vmgather/internal/domain"
)

// processLines runs the export line processor with the given obfuscation settings.
func processLines(r io.Reader, cfg domain.ObfuscationConfig, w io.Writer) (int, error) {
	proc := newLineProcessor(domain.ExportConfig{Obfuscation: cfg})
	defer proc.close()
	return proc.process(r, w)
}

// processMetrics applies the obfuscation settings to reader and returns the result.
func (s *exportServiceImpl) processMetrics(reader io.Reader, cfg domain.ObfuscationConfig) (io.Reader, int, error) {
	var out bytes.Buffer
	count, err := processLines(reader, cfg, &out)
	if err != nil {
		return nil, 0, err
	}
	return &out, count, nil
}
