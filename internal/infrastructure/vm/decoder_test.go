package vm

import (
	"bytes"
	"strings"
	"testing"
)

func TestCopyLines_PassthroughIsByteIdentical(t *testing.T) {
	data := `{"metric":{"__name__":"up"},"values":[1],"timestamps":[1]}
{"metric":{"__name__":"go_goroutines"},"values":[2],"timestamps":[2]}
`
	var out bytes.Buffer
	count, err := CopyLines(strings.NewReader(data), &out)
	if err != nil {
		t.Fatalf("CopyLines failed: %v", err)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}
	if out.String() != data {
		t.Fatalf("output not byte-identical: got %q, want %q", out.String(), data)
	}
}

func TestCopyLines_RejectsLinesNotShapedLikeJSONObjects(t *testing.T) {
	for _, line := range []string{"this is not json at all", `["array", "not", "object"]`, `{"unterminated":`} {
		var out bytes.Buffer
		if _, err := CopyLines(strings.NewReader(line), &out); err == nil {
			t.Errorf("expected error for line %q shaped like it isn't a JSON object", line)
		}
	}
}

func TestCopyLines_EmptyStream(t *testing.T) {
	var out bytes.Buffer
	count, err := CopyLines(strings.NewReader(""), &out)
	if err != nil {
		t.Fatalf("CopyLines failed on empty stream: %v", err)
	}
	if count != 0 {
		t.Fatalf("count = %d, want 0", count)
	}
}
