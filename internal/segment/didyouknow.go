package segment

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/backstage"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/moderation"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/panel"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/schedule"
)

// Catalog is what the segment needs from Backstage (backstage.Client).
type Catalog interface {
	Entities(ctx context.Context, kinds []string) ([]backstage.Entity, error)
}

// defaultKinds are the catalog kinds a did-you-know picks from: things the
// team built and runs, not users and groups.
var defaultKinds = []string{"component", "system", "api", "resource"}

// DidYouKnow airs a random entry of the Backstage catalog with its
// description: "Did you know?" as the title line, the entry's name and what
// it does as the message. Only entries with a description that says more than
// their name are picked, never a deprecated one (spec.lifecycle), and an
// aired entry is not repeated within SeenFor.
//
// Parameters: count (default 1), kinds (catalog kinds, default component,
// system, api, resource), types (spec.type, e.g. service, website; default
// any).
type DidYouKnow struct {
	Catalog   Catalog // nil: not configured
	Moderator *moderation.Moderator
	Title     panel.Title
	// MessageMax caps the scrolling line in characters.
	MessageMax int
	Timeout    time.Duration
	// SeenFor is how long an aired entry is not repeated (default 7 days).
	SeenFor time.Duration
	Rand    *rand.Rand

	mu   sync.Mutex
	seen map[string]time.Time // entity ref -> when aired
}

func (d *DidYouKnow) Name() string { return "did-you-know" }

func (d *DidYouKnow) Build(ctx context.Context, now time.Time, p schedule.Params) ([]Item, error) {
	if d.Catalog == nil {
		return nil, errors.New("did-you-know: no Backstage configured (RADIO_BACKSTAGE_URL)")
	}
	kinds := p.Strings("kinds")
	if len(kinds) == 0 {
		kinds = defaultKinds
	}
	types := p.Strings("types")
	timeout := d.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	all, err := d.Catalog.Entities(cctx, kinds)
	if err != nil {
		return nil, fmt.Errorf("did-you-know: %w", err)
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	d.prune(now)

	var fresh []backstage.Entity
	for _, e := range all {
		if !saysMore(e) || strings.EqualFold(e.Lifecycle, "deprecated") {
			continue
		}
		if len(types) > 0 && !containsFold(types, e.Type) {
			continue
		}
		if _, ok := d.seen[e.Ref()]; ok {
			continue
		}
		fresh = append(fresh, e)
	}
	r := d.Rand
	if r == nil {
		r = rand.New(rand.NewPCG(uint64(now.UnixNano()), 0))
		d.Rand = r
	}
	r.Shuffle(len(fresh), func(i, j int) { fresh[i], fresh[j] = fresh[j], fresh[i] })

	count := min(p.Int("count", 1), len(fresh))
	items := make([]Item, 0, count)
	for _, e := range fresh[:count] {
		if !IsPreview(ctx) {
			d.seen[e.Ref()] = now
		}
		name := e.Label()
		tmpl := name + ": " + strings.TrimSpace(e.Description)
		items = append(items, Item{
			Title:   d.Title.Fit("Did you know? " + name),
			Message: panel.Truncate(d.Moderator.Blurb(ctx, tmpl, name, d.MessageMax), d.MessageMax),
		})
	}
	return items, nil
}

func (d *DidYouKnow) prune(now time.Time) {
	if d.seen == nil {
		d.seen = map[string]time.Time{}
	}
	ttl := d.SeenFor
	if ttl <= 0 {
		ttl = 7 * 24 * time.Hour
	}
	for k, at := range d.seen {
		if now.Sub(at) > ttl {
			delete(d.seen, k)
		}
	}
}

// saysMore reports whether the description tells more than the entry's name
// or title: "dave" described as "dave" (or "Dave.") does not.
func saysMore(e backstage.Entity) bool {
	d := bare(e.Description)
	return d != "" && d != bare(e.Name) && d != bare(e.Title)
}

// bare is s in lower case with only its letters and digits.
func bare(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}
