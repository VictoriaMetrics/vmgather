package snapshot

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"log"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/backup/actions"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/backup/backupnames"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/snapshot/snapshotutil"

	"github.com/VictoriaMetrics/vmgather/internal/application/services"
	"github.com/VictoriaMetrics/vmgather/internal/domain"
)

// uploadConcurrency is the number of parallel part uploads. A snapshot has one bundle file, which is
// split into parts only above 1 GiB, so a small number is enough.
const uploadConcurrency = 2

// Config is the snapshot mode configuration.
type Config struct {
	// Export holds the connection, the jobs (empty means discover them on every run) and obfuscation.
	// Mode, time range, staging and the MDX filter are set by the runner.
	Export domain.ExportConfig
	// Dst is the snapshot root: s3://bucket/prefix, gs://, azblob:// or fs:///abs/path.
	Dst string
	// Period is the length of one snapshot window, at least one minute and a whole number of seconds
	// (snapshot names have second precision). Window ends are aligned to it.
	Period time.Duration
	// Lag keeps the newest data out of the window, so late samples are included.
	Lag time.Duration
	// MaxCatchUp limits the number of windows per scheduled run.
	MaxCatchUp int
	// Start is where the first window starts when there is no state yet (zero: one period before the end).
	Start time.Time
	// End fixes the end for a one-time backfill. With End set, Start is required and the state is
	// neither read nor written, so a backfill never moves the progress of scheduled runs.
	End time.Time
	// Retention deletes snapshots whose window ended more than Retention ago (0 keeps them forever).
	// Scheduled runs apply it; a backfill does not, so backfill into its own prefix.
	Retention time.Duration
	// WorkDir holds the local files of the running snapshot. It is emptied before and after every run.
	WorkDir string
	// Version is the vmgather version written to metadata.json.
	Version string
}

func (c Config) validate() error {
	switch {
	case c.Dst == "":
		return errors.New("snapshot destination is empty")
	case c.Period < time.Minute || c.Period%time.Second != 0:
		return fmt.Errorf("snapshot period must be at least 1m and a whole number of seconds, got %s", c.Period)
	case c.Retention < 0:
		return fmt.Errorf("snapshot retention must not be negative, got %s", c.Retention)
	case c.Lag < 0:
		return fmt.Errorf("snapshot lag must not be negative, got %s", c.Lag)
	case c.MaxCatchUp <= 0 && c.End.IsZero():
		return fmt.Errorf("snapshot max catch-up must be positive, got %d", c.MaxCatchUp)
	case !c.End.IsZero() && c.Start.IsZero():
		return errors.New("a backfill with a fixed end needs a start time")
	case !c.End.IsZero() && !c.Start.Before(c.End):
		return fmt.Errorf("snapshot start %s must be before end %s", c.Start.Format(time.RFC3339), c.End.Format(time.RFC3339))
	case !c.End.IsZero() && !c.End.Equal(c.End.Truncate(c.Period)):
		return fmt.Errorf("snapshot end %s must be a multiple of the period %s", c.End.Format(time.RFC3339), c.Period)
	case c.WorkDir == "":
		return errors.New("snapshot work dir is empty")
	case c.Export.Mode == domain.ExportModeCustom:
		return errors.New("snapshot mode exports VictoriaMetrics components; custom mode is not supported")
	}
	return nil
}

// Runner exports and uploads snapshot windows.
type Runner struct {
	cfg Config
	now func() time.Time
	// discoverJobs returns the components and jobs that had vm_app_version at any time in the window.
	discoverJobs func(ctx context.Context, conn domain.VMConnection, tr domain.TimeRange) (components, jobs []string, err error)
}

// NewRunner returns a runner for cfg.
func NewRunner(cfg Config) (*Runner, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Runner{cfg: cfg, now: time.Now, discoverJobs: discoverJobs}, nil
}

// Loop runs RunOnce every interval until ctx is done. Errors are logged; the next run retries.
func (r *Runner) Loop(ctx context.Context, interval time.Duration) {
	for {
		if err := r.RunOnce(ctx); err != nil && ctx.Err() == nil {
			log.Printf("[ERROR] snapshot run failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// RunOnce exports and uploads every pending window, oldest first, and stops at the first error.
// The state moves only after a window is fully uploaded, so a failed or canceled window is retried
// by the next run into the same place.
func (r *Runner) RunOnce(ctx context.Context) (err error) {
	if err := r.cleanWorkDir(); err != nil {
		return err
	}
	// Nothing stays on the local disk between runs, also after a failure.
	defer func() {
		if cleanErr := r.cleanWorkDir(); cleanErr != nil && err == nil {
			err = cleanErr
		}
	}()
	rootFS, err := actions.NewRemoteFS(ctx, r.cfg.Dst, nil)
	if err != nil {
		return fmt.Errorf("cannot open snapshot destination %q: %w", r.cfg.Dst, err)
	}
	defer rootFS.MustStop()
	store := stateStore{fs: rootFS}

	backfill := !r.cfg.End.IsZero()
	end := r.now().Add(-r.cfg.Lag)
	var windows iter.Seq[domain.TimeRange]
	if backfill {
		if r.cfg.End.After(end) {
			return fmt.Errorf("snapshot end %s is less than -snapshot.lag behind now", r.cfg.End.Format(time.RFC3339))
		}
		windows = planWindows(time.Time{}, r.cfg.Start, r.cfg.End, r.cfg.Period, math.MaxInt)
	} else {
		st, err := store.load()
		if err != nil {
			return err
		}
		if st.LastEnd.After(r.now()) {
			return fmt.Errorf("%s ends at %s, after the current time: check the clock or the state file",
				stateFilename, st.LastEnd.Format(time.RFC3339))
		}
		windows = planWindows(st.LastEnd, r.cfg.Start, end, r.cfg.Period, r.cfg.MaxCatchUp)
	}
	planned := 0
	for w := range windows {
		planned++
		name := snapshotName(w)
		if backfill {
			// A backfill keeps no state; skipping uploaded windows makes a failed backfill restartable.
			done, err := rootFS.HasFile(name + "/" + backupnames.BackupCompleteFilename)
			if err != nil {
				return err
			}
			if done {
				log.Printf("[SNAPSHOT] %s is already uploaded, skipping", name)
				continue
			}
		}
		started := time.Now()
		metrics, err := r.exportAndUpload(ctx, w, joinURL(r.cfg.Dst, name))
		if err != nil {
			return fmt.Errorf("snapshot %s: %w", name, err)
		}
		if !backfill {
			st := state{LastEnd: w.End, LastSnapshot: name, UpdatedAt: r.now().UTC()}
			if err := store.save(st); err != nil {
				return fmt.Errorf("snapshot %s was uploaded, but the state was not saved (the next run uploads it again): %w", name, err)
			}
		}
		if metrics == 0 {
			log.Printf("[WARN] snapshot %s: no MDX series in the window, nothing uploaded", name)
			continue
		}
		log.Printf("[SNAPSHOT][OK] %s: %d export lines in %s", name, metrics, time.Since(started).Round(time.Millisecond))
	}
	if planned == 0 {
		log.Printf("[SNAPSHOT] nothing to do: the next window is not complete yet")
	}
	if !backfill && r.cfg.Retention > 0 {
		if _, err := applyRetention(rootFS, r.now().Add(-r.cfg.Retention)); err != nil {
			return fmt.Errorf("snapshot retention: %w", err)
		}
	}
	return nil
}

// exportAndUpload exports one window and uploads it. A window without MDX series is not uploaded and
// returns 0, so an empty window (for example before any component existed) never blocks later windows.
func (r *Runner) exportAndUpload(ctx context.Context, w domain.TimeRange, dst string) (int, error) {
	cfg := r.cfg.Export
	cfg.Mode = domain.ExportModeCluster
	cfg.MDXFilter = true
	// Snapshot windows are half-open: a sample exactly at w.End belongs to the next window.
	cfg.TimeRange = domain.TimeRange{Start: w.Start, End: w.End.Add(-time.Millisecond)}
	cfg.StagingDir = filepath.Join(r.cfg.WorkDir, "staging")
	cfg.StagingFile = ""
	cfg.ResumeFromBatch = 0
	if len(cfg.Jobs) == 0 {
		components, jobs, err := r.discoverJobs(ctx, cfg.Connection, w)
		if err != nil {
			return 0, fmt.Errorf("cannot discover VictoriaMetrics components: %w", err)
		}
		if len(jobs) == 0 {
			return 0, nil
		}
		cfg.Components, cfg.Jobs = components, jobs
	}

	// actions.Backup needs a source directory named like a VictoriaMetrics snapshot.
	dir := filepath.Join(r.cfg.WorkDir, snapshotutil.NewName())
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return 0, err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	result, err := services.NewExportService(dir, r.cfg.Version).ExecuteExport(ctx, cfg)
	if err != nil {
		return 0, err
	}
	if result.MetricsExported == 0 {
		return 0, nil
	}
	if err := os.Rename(result.ArchivePath, filepath.Join(dir, bundleFilename)); err != nil {
		return 0, err
	}
	if err := uploadDir(ctx, dir, dst); err != nil {
		return 0, fmt.Errorf("upload to %s failed: %w", dst, err)
	}
	return result.MetricsExported, nil
}

// cleanWorkDir removes files left by a killed run. The work dir belongs to snapshot mode.
func (r *Runner) cleanWorkDir() error {
	if err := os.RemoveAll(r.cfg.WorkDir); err != nil {
		return err
	}
	return os.MkdirAll(r.cfg.WorkDir, 0o750)
}

func discoverJobs(ctx context.Context, conn domain.VMConnection, tr domain.TimeRange) ([]string, []string, error) {
	found, err := services.DiscoverVMJobs(ctx, conn, tr)
	if err != nil {
		return nil, nil, err
	}
	var components, jobs []string
	for _, c := range found {
		components = append(components, c.Component)
		jobs = append(jobs, c.Jobs...)
	}
	return components, jobs, nil
}
