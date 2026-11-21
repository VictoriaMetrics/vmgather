package vm

import (
	"context"
	"fmt"
	"io"
	"sync/atomic"
	"time"
)

// ExportIdleTimeout is how long an /api/v1/export request may go without receiving any bytes
// (including the wait for response headers) before it is canceled.
var ExportIdleTimeout = 2 * time.Minute

// idleWatchdog cancels a request when no data arrives for timeout. Every received chunk re-arms it.
type idleWatchdog struct {
	timeout time.Duration
	timer   *time.Timer
	fired   atomic.Bool
	gotData atomic.Bool
}

func newIdleWatchdog(timeout time.Duration, cancel context.CancelFunc) *idleWatchdog {
	w := &idleWatchdog{timeout: timeout}
	w.timer = time.AfterFunc(timeout, func() {
		w.fired.Store(true)
		cancel()
	})
	return w
}

func (w *idleWatchdog) touch() {
	w.gotData.Store(true)
	w.timer.Reset(w.timeout)
}

func (w *idleWatchdog) stop() {
	w.timer.Stop()
}

// err describes why the watchdog fired. A stream that stalled after some data is a query timeout,
// so the caller may split the time range. No data at all usually means a dead or hung upstream;
// it is returned as a plain error so the export fails fast instead of splitting many times.
// Neither error wraps context.Canceled, so it is never confused with a user cancel.
func (w *idleWatchdog) err() error {
	if w.gotData.Load() {
		return &APIError{
			Kind: ErrorKindQueryTimeout,
			Body: fmt.Sprintf("export stream stalled: no data from VictoriaMetrics for %s", w.timeout),
		}
	}
	return fmt.Errorf("no response from VictoriaMetrics for %s", w.timeout)
}

// watchedBody re-arms the watchdog on every read and reports the watchdog error instead of the
// generic "context canceled" error when the watchdog fired.
type watchedBody struct {
	body     io.ReadCloser
	watchdog *idleWatchdog
	cancel   context.CancelFunc
}

func (b *watchedBody) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	if n > 0 {
		b.watchdog.touch()
	}
	if err != nil && err != io.EOF && b.watchdog.fired.Load() {
		return n, b.watchdog.err()
	}
	return n, err
}

func (b *watchedBody) Close() error {
	b.watchdog.stop()
	b.cancel()
	return b.body.Close()
}
