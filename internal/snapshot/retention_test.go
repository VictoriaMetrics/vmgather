package snapshot

import (
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"testing"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/backup/fsremote"
)

// Retention on fs:// removes expired snapshot dirs, also those without parts, and leaves no directory
// handles open: on Windows an open handle keeps the directory of a kept snapshot from being deleted later.
// fscommon.RemoveEmptyDirs in VictoriaMetrics v1.156.0 leaks one handle per non-empty directory.
func TestApplyRetentionLocalFS(t *testing.T) {
	root := t.TempDir()
	part := "0000000000000003_0000000000000000_0000000000000003"
	for _, name := range []string{"20261121T050000Z_20261121T060000Z", "20261121T110000Z_20261121T120000Z"} {
		dir := filepath.Join(root, name, bundleFilename)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, part), []byte("abc"), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	// An expired snapshot whose parts are already gone (a run died during deletion): ListParts
	// does not show it, but it must still be removed.
	emptyExpired := filepath.Join(root, "20261121T040000Z_20261121T050000Z")
	if err := os.MkdirAll(filepath.Join(emptyExpired, bundleFilename), 0o750); err != nil {
		t.Fatal(err)
	}
	// A GC would close leaked handles through finalizers and hide the leak.
	defer debug.SetGCPercent(debug.SetGCPercent(-1))

	before := openFiles(t)
	deleted, err := applyRetention(&fsremote.FS{Dir: root}, mustTime(t, "2026-11-21T09:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if after := openFiles(t); after != before {
		t.Fatalf("open files: %d before retention, %d after", before, after)
	}
	if deleted != 2 {
		t.Fatalf("deleted %d snapshots; want 2", deleted)
	}
	for _, dir := range []string{filepath.Join(root, "20261121T050000Z_20261121T060000Z"), emptyExpired} {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("expired snapshot dir %s is still there: %v", dir, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "20261121T110000Z_20261121T120000Z", bundleFilename, part)); err != nil {
		t.Fatalf("kept snapshot was touched: %v", err)
	}
}

// openFiles returns the number of open files of the process, or -1 where /dev/fd is missing (Windows,
// where the TempDir cleanup fails on a leaked handle instead).
func openFiles(t *testing.T) int {
	t.Helper()
	if runtime.GOOS == "windows" {
		return -1
	}
	entries, err := os.ReadDir("/dev/fd")
	if err != nil {
		t.Fatalf("cannot list open files: %v", err)
	}
	return len(entries)
}
