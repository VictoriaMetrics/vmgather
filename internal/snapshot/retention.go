package snapshot

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/backup/backupnames"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/backup/common"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/backup/fsremote"
)

// applyRetention deletes the snapshot directories whose window ended before cutoff, including
// incomplete ones left by uploads that never finished. It returns the number of deleted snapshots.
//
// The completion marker goes first, so a snapshot that is only partly deleted is never taken for a
// complete one; its remaining parts are still listed, and deleted, by the next run.
func applyRetention(fs common.RemoteFS, cutoff time.Time) (int, error) {
	parts, err := fs.ListParts()
	if err != nil {
		return 0, fmt.Errorf("cannot list snapshots at %s: %w", fs, err)
	}
	expired := make(map[string][]common.Part)
	for _, p := range parts {
		name, _, ok := strings.Cut(p.Path, "/")
		if !ok {
			continue
		}
		if end, ok := snapshotEnd(name); ok && end.Before(cutoff) {
			expired[name] = append(expired[name], p)
		}
	}
	// On a local filesystem a snapshot dir may have no parts left (a run died after deleting them, or
	// the dir removal failed), so ListParts no longer shows it: find such dirs by name.
	if local, ok := fs.(*fsremote.FS); ok {
		entries, err := os.ReadDir(local.Dir)
		if err != nil && !os.IsNotExist(err) {
			return 0, fmt.Errorf("cannot list snapshots at %s: %w", fs, err)
		}
		for _, e := range entries {
			if end, ok := snapshotEnd(e.Name()); ok && e.IsDir() && end.Before(cutoff) {
				if _, seen := expired[e.Name()]; !seen {
					expired[e.Name()] = nil
				}
			}
		}
	}
	names := make([]string, 0, len(expired))
	for name := range expired {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, file := range []string{backupnames.BackupCompleteFilename, backupnames.BackupMetadataFilename} {
			if err := fs.DeleteFile(name + "/" + file); err != nil {
				return 0, err
			}
		}
		for _, p := range expired[name] {
			if err := fs.DeletePart(p); err != nil {
				return 0, err
			}
		}
		if err := removeSnapshotDir(fs, name); err != nil {
			return 0, err
		}
		log.Printf("[SNAPSHOT] deleted %s: older than the retention", name)
	}
	return len(names), nil
}

// removeSnapshotDir removes the directory of a deleted snapshot on a local filesystem destination
// (fs://); object storage has no directories. It does not call fs.RemoveEmptyDirs: in VictoriaMetrics
// v1.156.0 it leaves the handles of non-empty directories open, and on Windows an open handle keeps a
// directory from being deleted.
func removeSnapshotDir(fs common.RemoteFS, name string) error {
	local, ok := fs.(*fsremote.FS)
	if !ok {
		return nil
	}
	if err := os.RemoveAll(filepath.Join(local.Dir, name)); err != nil {
		return fmt.Errorf("cannot remove %s: %w", name, err)
	}
	return nil
}

// snapshotEnd returns the window end encoded in a snapshot directory name.
func snapshotEnd(name string) (time.Time, bool) {
	start, end, ok := strings.Cut(name, "_")
	if !ok {
		return time.Time{}, false
	}
	if _, err := time.Parse(nameTimeLayout, start); err != nil {
		return time.Time{}, false
	}
	t, err := time.Parse(nameTimeLayout, end)
	return t, err == nil
}
