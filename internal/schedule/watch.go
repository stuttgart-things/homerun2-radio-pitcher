package schedule

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// Watcher serves the schedule at Path and reloads it when the file changes,
// e.g. when Kubernetes swaps a ConfigMap. A broken new version is reported
// and the last good one stays on air.
type Watcher struct {
	Path string

	mu      sync.Mutex
	current *Schedule
	modTime time.Time
	size    int64
	lastErr error
}

// Get returns the current schedule, reloading it if the file changed. It
// returns changed=true when a new version was loaded, and the reload error
// when the file changed but could not be used.
func (w *Watcher) Get() (s *Schedule, changed bool, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	fi, err := os.Stat(w.Path)
	if err != nil {
		w.lastErr = err
		return w.current, false, err
	}
	if w.current != nil && fi.ModTime().Equal(w.modTime) && fi.Size() == w.size {
		return w.current, false, nil
	}
	next, err := Load(w.Path)
	// remember the version even when broken, so the error is reported once
	w.modTime, w.size = fi.ModTime(), fi.Size()
	if err != nil {
		w.lastErr = fmt.Errorf("%s: %w", w.Path, err)
		return w.current, false, w.lastErr
	}
	w.current, w.lastErr = next, nil
	return next, true, nil
}

// Current returns the last good schedule and the last load error.
func (w *Watcher) Current() (*Schedule, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.current, w.lastErr
}
