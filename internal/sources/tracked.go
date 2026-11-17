package sources

import (
	"context"
	"sync"
	"time"
)

// Status is the result of a source's last query, for the UI.
type Status struct {
	Name     string
	At       time.Time // zero: not queried yet
	Took     time.Duration
	Facts    int
	Err      string
	Disabled bool
}

// Tracked wraps a Source and remembers its last query.
type Tracked struct {
	Source
	mu   sync.Mutex
	last Status
}

// Track wraps every source.
func Track(srcs []Source) []*Tracked {
	out := make([]*Tracked, len(srcs))
	for i, s := range srcs {
		out[i] = &Tracked{Source: s, last: Status{Name: s.Name()}}
	}
	return out
}

// Facts queries the wrapped source and records the result.
func (t *Tracked) Facts(ctx context.Context) ([]Fact, error) {
	start := time.Now()
	facts, err := t.Source.Facts(ctx)
	st := Status{Name: t.Name(), At: start, Took: time.Since(start).Round(time.Millisecond), Facts: len(facts)}
	if err != nil {
		st.Err = err.Error()
	}
	t.mu.Lock()
	t.last = st
	t.mu.Unlock()
	return facts, err
}

// Status returns the last query's result.
func (t *Tracked) Status() Status {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.last
}
