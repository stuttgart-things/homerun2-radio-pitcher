package schedule

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatcherReloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schedule.yaml")
	write := func(s string, mod time.Time) {
		if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	t0 := time.Now().Add(-time.Hour)
	write("clock:\n  \":05\": { segment: news }\n", t0)

	w := &Watcher{Path: path}
	s, changed, err := w.Get()
	if err != nil || !changed || s.Clock[5].Segment != "news" {
		t.Fatalf("first load: %v %v %+v", changed, err, s)
	}
	if _, changed, _ = w.Get(); changed {
		t.Fatal("unchanged file reported as changed")
	}

	// broken update: the old schedule stays
	write("clock: [\n", t0.Add(time.Minute))
	s, changed, err = w.Get()
	if err == nil || changed || s == nil || s.Clock[5].Segment != "news" {
		t.Fatalf("broken update: %v %v %+v", changed, err, s)
	}
	if cur, lastErr := w.Current(); cur == nil || lastErr == nil {
		t.Fatal("Current should keep the old schedule and the error")
	}

	// good update
	write("clock:\n  \":10\": { segment: time-check }\n", t0.Add(2*time.Minute))
	s, changed, err = w.Get()
	if err != nil || !changed || s.Clock[10].Segment != "time-check" {
		t.Fatalf("good update: %v %v %+v", changed, err, s)
	}

	// file gone
	_ = os.Remove(path)
	if s, _, err := w.Get(); err == nil || s == nil {
		t.Fatal("missing file should keep the schedule and report an error")
	}
}
