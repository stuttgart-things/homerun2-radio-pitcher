// Package segment builds the programme items the director airs. A segment
// turns the parameters of a schedule slot into one or more items, each of
// which becomes one card on the panel.
package segment

import (
	"context"
	"sort"
	"time"

	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/schedule"
)

// Item is one card: Title in the card's title area, Message on the
// scrolling line.
type Item struct {
	Title   string
	Message string
}

// Segment builds items for a slot. An empty result is not an error: there was
// nothing worth saying (no unseen news, every source down).
type Segment interface {
	Name() string
	Build(ctx context.Context, now time.Time, p schedule.Params) ([]Item, error)
}

type previewKey struct{}

// Preview marks ctx as a preview: the segment builds its items as if it
// aired now, but keeps no state (no headline marked seen, no fact marked
// recent), so the next real slot is not changed by looking at it.
func Preview(ctx context.Context) context.Context {
	return context.WithValue(ctx, previewKey{}, true)
}

// IsPreview reports whether ctx is a preview.
func IsPreview(ctx context.Context) bool {
	v, _ := ctx.Value(previewKey{}).(bool)
	return v
}

// Registry holds the segments by name.
type Registry map[string]Segment

// NewRegistry registers the given segments.
func NewRegistry(segs ...Segment) Registry {
	r := Registry{}
	for _, s := range segs {
		r[s.Name()] = s
	}
	return r
}

// Has reports whether a segment of that name exists.
func (r Registry) Has(name string) bool {
	_, ok := r[name]
	return ok
}

// Names lists the registered segments, sorted.
func (r Registry) Names() []string {
	out := make([]string, 0, len(r))
	for n := range r {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
