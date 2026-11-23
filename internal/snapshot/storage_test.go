package snapshot

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/backup/actions"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/backup/common"
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

// On fs:// the state is replaced atomically through a temp file, which must not stay behind.
func TestStateStoreLocalWriteLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	fs, err := actions.NewRemoteFS(context.Background(), "fs://"+dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer fs.MustStop()
	store := stateStore{fs: fs}
	for h := 10; h <= 11; h++ {
		if err := store.save(state{LastEnd: time.Date(2026, 11, 21, h, 0, 0, 0, time.UTC)}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.load()
	if err != nil || !got.LastEnd.Equal(time.Date(2026, 11, 21, 11, 0, 0, 0, time.UTC)) {
		t.Fatalf("load after two saves: %+v, %v", got, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != stateFilename {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("files in the destination: %v; want only %s", names, stateFilename)
	}
}

// The uploaded snapshot must be readable with vmrestore's actions.Restore, which checks the
// backup_complete.ignore marker that actions.Backup writes last.
func TestBackupUploaderRoundTripWithRestore(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	// After a rerun actions.Backup calls RemoveEmptyDirs, which in VictoriaMetrics v1.156.0 leaves
	// directory handles open until a GC; Windows cannot delete a directory with an open handle.
	t.Cleanup(runtime.GC)
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

	want := payload + "vm_rows 2\n"
	if got := readRemoteFile(t, dst, bundleFilename); string(got) != want {
		t.Fatalf("remote has %d bytes; want the second upload (%d bytes)", len(got), len(want))
	}

	// vmrestore runs on Linux; fslocal.FinalizeFile fsyncs a file opened read-only, which Windows rejects.
	if runtime.GOOS == "windows" {
		t.Log("actions.Restore is not checked on Windows")
		return
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
	if string(got) != want {
		t.Fatalf("restored %d bytes; want the second upload (%d bytes)", len(got), len(want))
	}
}

// readRemoteFile returns the content of file in the snapshot at dst. Every part must belong to the
// current version of the file, so a rerun into the same place keeps one copy.
func readRemoteFile(t *testing.T, dst, file string) []byte {
	t.Helper()
	fs, err := actions.NewRemoteFS(context.Background(), dst, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer fs.MustStop()
	parts, err := fs.ListParts()
	if err != nil {
		t.Fatal(err)
	}
	var fileParts []common.Part
	for _, p := range parts {
		if p.Path == file {
			fileParts = append(fileParts, p)
		}
	}
	if len(fileParts) == 0 {
		t.Fatalf("%s has no parts of %s", dst, file)
	}
	common.SortParts(fileParts)
	var buf bytes.Buffer
	for _, p := range fileParts {
		if p.FileSize != fileParts[0].FileSize || p.Offset != uint64(buf.Len()) {
			t.Fatalf("%s: parts of different versions or with gaps: %v", file, fileParts)
		}
		if err := fs.DownloadPart(p, &buf); err != nil {
			t.Fatal(err)
		}
	}
	if uint64(buf.Len()) != fileParts[0].FileSize {
		t.Fatalf("%s: got %d bytes from parts; want %d", file, buf.Len(), fileParts[0].FileSize)
	}
	return buf.Bytes()
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
