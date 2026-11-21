package vm

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
)

// ExportDecoder decodes JSONL export stream
type ExportDecoder struct {
	scanner *bufio.Scanner
}

// MaxLineBytes is the longest JSONL line accepted. /api/v1/export writes up to 8192 rows per line with
// reduce_mem_usage=1 and prints floats in full, so a series with very large values gives lines of several MB
// (2.6 MB measured on VictoriaMetrics v1.155.0). vmimporter accepts the same 16 MB.
const MaxLineBytes = 16 * 1024 * 1024

// NewLineScanner builds a bufio.Scanner sized for JSONL metric lines with many labels.
// The buffer starts at 64KB and grows only for long lines.
func NewLineScanner(r io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), MaxLineBytes)
	return scanner
}

// NewExportDecoder creates a new export decoder
func NewExportDecoder(r io.Reader) *ExportDecoder {
	return &ExportDecoder{
		scanner: NewLineScanner(r),
	}
}

// CopyLines streams each JSONL line's content from r to w unchanged, writing a
// single '\n' after each line, and returning the number of lines copied. Line
// endings are normalized to LF (the scanner splits on bufio.ScanLines, which
// strips a trailing '\r'), matching the LF-only output of VictoriaMetrics's
// own /api/v1/export - this is not a byte-identical passthrough for arbitrary
// CRLF input. Each line is sanity-checked
// (non-empty, starts with '{' and ends with '}') rather than fully validated
// with json.Valid: a live CPU profile showed json.Valid's byte-by-byte scan
// (including inside every string) as the single largest JSON-related cost on
// this path, more expensive than the decode+re-marshal it was meant to avoid
// paying for. The source here is VictoriaMetrics's own /api/v1/export output,
// not untrusted input, so a cheap shape check to catch genuinely broken
// responses is enough - it isn't meant to catch every malformed edge case.
func CopyLines(r io.Reader, w io.Writer) (int, error) {
	scanner := NewLineScanner(r)
	count := 0
	newline := []byte{'\n'}
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 || line[0] != '{' || line[len(line)-1] != '}' {
			return count, fmt.Errorf("invalid JSON line %d: expected an object", count+1)
		}
		if _, err := w.Write(line); err != nil {
			return count, err
		}
		if _, err := w.Write(newline); err != nil {
			return count, err
		}
		count++
	}
	if err := scanner.Err(); err != nil {
		return count, err
	}
	return count, nil
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
