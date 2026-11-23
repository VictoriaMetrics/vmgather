package snapshot

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/backup/actions"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/backup/common"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/backup/fslocal"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/backup/fsremote"

	"github.com/VictoriaMetrics/vmgather/internal/domain"
)

// stateFilename is stored at the snapshot root, next to the snapshot directories.
const stateFilename = "vmgather_state.json"

// state is the progress of periodic snapshots. It is written only after a snapshot upload completed.
type state struct {
	// LastEnd is the end of the last completed window; the next window starts here.
	LastEnd time.Time `json:"last_end"`
	// LastSnapshot is the name of the last uploaded snapshot directory, for people reading the bucket.
	LastSnapshot string    `json:"last_snapshot,omitempty"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// stateStore keeps the state in the same storage as the snapshots, through the vmbackup RemoteFS.
type stateStore struct {
	fs common.RemoteFS
}

func (s stateStore) load() (state, error) {
	var st state
	ok, err := s.fs.HasFile(stateFilename)
	if err != nil {
		return st, fmt.Errorf("cannot check %s at %s: %w", stateFilename, s.fs, err)
	}
	if !ok {
		return st, nil
	}
	data, err := s.fs.ReadFile(stateFilename)
	if err != nil {
		return st, fmt.Errorf("cannot read %s at %s: %w", stateFilename, s.fs, err)
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return st, fmt.Errorf("cannot parse %s at %s: %w", stateFilename, s.fs, err)
	}
	return st, nil
}

func (s stateStore) save(st state) error {
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if local, ok := s.fs.(*fsremote.FS); ok {
		// fsremote.CreateFile is a plain write: a kill in the middle would leave a broken file that
		// stops every later run. Object storage replaces an object in one request.
		err = writeFileAtomic(filepath.Join(local.Dir, stateFilename), data)
	} else {
		err = s.fs.CreateFile(stateFilename, data)
	}
	if err != nil {
		return fmt.Errorf("cannot write %s at %s: %w", stateFilename, s.fs, err)
	}
	return nil
}

// writeFileAtomic writes data to a temp file in the same directory, syncs it and renames it over path,
// so path holds either the old or the new content.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.ignore")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}

// joinURL appends a directory name to a storage URL such as s3://bucket/prefix.
func joinURL(root, name string) string {
	return strings.TrimRight(root, "/") + "/" + name
}

// snapshotName returns the deterministic directory name of a window, so a retry overwrites the same place.
func snapshotName(w domain.TimeRange) string {
	return w.Start.UTC().Format(nameTimeLayout) + "_" + w.End.UTC().Format(nameTimeLayout)
}

const nameTimeLayout = "20060102T150405Z"

// bundleFilename is the name of the vmgather bundle inside every snapshot directory.
const bundleFilename = "vmgather.zip"

// uploadDir uploads the files of srcDir to dst with vmbackup's actions.Backup: streaming multipart upload
// of every file, then backup_metadata.ignore and, last, backup_complete.ignore. A rerun into the same
// dst replaces the parts. srcDir must be named like a VictoriaMetrics snapshot (snapshotutil.NewName()).
func uploadDir(ctx context.Context, srcDir, dst string) error {
	fs, err := actions.NewRemoteFS(ctx, dst, nil)
	if err != nil {
		return err
	}
	defer fs.MustStop()
	b := actions.Backup{
		Concurrency: uploadConcurrency,
		Src:         &fslocal.FS{Dir: srcDir},
		Dst:         fs,
	}
	if err := b.Run(); err != nil {
		return err
	}
	// actions.Backup takes no context. s3, gs and azblob stop on cancel through the context of the
	// RemoteFS; report a cancel that came during the last request.
	return ctx.Err()
}
