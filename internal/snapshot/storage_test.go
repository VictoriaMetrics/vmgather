package snapshot

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/backup/actions"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/backup/fslocal"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/snapshot/snapshotutil"

	"github.com/VictoriaMetrics/vmgather/internal/domain"
)

func TestStateStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	fs, err := actions.NewRemoteFS(ctx, "fs://"+t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer fs.MustStop()
	store := stateStore{fs: fs}

	st, err := store.load()
	if err != nil {
		t.Fatalf("load without state: %v", err)
	}
	if !st.LastEnd.IsZero() {
		t.Fatalf("missing state must be empty, got %+v", st)
	}

	want := state{
		LastEnd:      time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC),
		LastSnapshot: "20261121T090000Z_20261121T100000Z",
		UpdatedAt:    time.Date(2026, 11, 21, 10, 5, 0, 0, time.UTC),
	}
	if err := store.save(want); err != nil {
		t.Fatal(err)
	}
	got, err := store.load()
	if err != nil {
		t.Fatal(err)
	}
	if !got.LastEnd.Equal(want.LastEnd) || got.LastSnapshot != want.LastSnapshot {
		t.Fatalf("got %+v; want %+v", got, want)
	}
}

// The uploaded snapshot must be readable with vmrestore's actions.Restore, which checks the
// backup_complete.ignore marker that actions.Backup writes last.
func TestBackupUploaderRoundTripWithRestore(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	src := filepath.Join(root, "staging", snapshotutil.NewName())
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("vm_rows 1\n", 1000)
	if err := os.WriteFile(filepath.Join(src, bundleFilename), []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}

	dst := joinURL("fs://"+filepath.Join(root, "bucket", "prefix"), "20261121T090000Z_20261121T100000Z")
	if err := uploadDir(ctx, src, dst); err != nil {
		t.Fatalf("upload: %v", err)
	}
	// A rerun of the same window into the same place must work and keep one copy.
	if err := os.WriteFile(filepath.Join(src, bundleFilename), []byte(payload+"vm_rows 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := uploadDir(ctx, src, dst); err != nil {
		t.Fatalf("second upload: %v", err)
	}

	remote, err := actions.NewRemoteFS(ctx, dst, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer remote.MustStop()
	out := filepath.Join(root, "restored")
	r := actions.Restore{Concurrency: 1, Src: remote, Dst: &fslocal.FS{Dir: out}}
	if err := r.Run(ctx); err != nil {
		t.Fatalf("restore: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(out, bundleFilename))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != payload+"vm_rows 2\n" {
		t.Fatalf("restored %d bytes; want the second upload (%d bytes)", len(got), len(payload)+10)
	}
}

func TestSnapshotName(t *testing.T) {
	w := domain.TimeRange{
		Start: time.Date(2026, 11, 21, 9, 0, 0, 0, time.FixedZone("x", 3*3600)),
		End:   time.Date(2026, 11, 21, 10, 0, 0, 0, time.FixedZone("x", 3*3600)),
	}
	if got := snapshotName(w); got != "20261121T060000Z_20261121T070000Z" {
		t.Fatalf("snapshotName = %q", got)
	}
	if got := joinURL("s3://bucket/prefix/", "a"); got != "s3://bucket/prefix/a" {
		t.Fatalf("joinURL = %q", got)
	}
}
