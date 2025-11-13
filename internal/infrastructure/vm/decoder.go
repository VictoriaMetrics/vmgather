package vm

import (
	"bufio"
	"encoding/json"
	"io"
)

// ExportDecoder decodes JSONL export stream
type ExportDecoder struct {
	scanner *bufio.Scanner
}

// NewExportDecoder creates a new export decoder
func NewExportDecoder(r io.Reader) *ExportDecoder {
	scanner := bufio.NewScanner(r)
	// Set larger buffer for metrics with many labels
	scanner.Buffer(make([]byte, 64*1024), 1024*1024) // 64KB initial, 1MB max

	return &ExportDecoder{
		scanner: scanner,
	}
}

// Decode decodes next metric from stream
// Returns io.EOF when stream ends
func (d *ExportDecoder) Decode() (*ExportedMetric, error) {
	if !d.scanner.Scan() {
		if err := d.scanner.Err(); err != nil {
			return nil, err
		}
		return nil, io.EOF
	}

	line := d.scanner.Bytes()

	var metric ExportedMetric
	if err := json.Unmarshal(line, &metric); err != nil {
		return nil, err
	}

	return &metric, nil
}

