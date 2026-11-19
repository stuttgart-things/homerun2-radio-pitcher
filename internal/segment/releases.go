package segment

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/github"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/panel"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/schedule"
)

// ReleaseLister lists an organization's releases since a time, newest
// first. github.Client in production, a fake in tests.
type ReleaseLister interface {
	Releases(ctx context.Context, since time.Time) ([]github.Release, error)
}

// Releases airs the releases the organization published lately (GitHub
// org events, RADIO_RELEASES_ORG), newest first; each airs once. Fixed
// texts: repo, tag and note come from GitHub.
//
// Parameters: count (default 1), within (Go duration, default 24h), repos
// (names without the org; default all).
type Releases struct {
	Lister     ReleaseLister
	Org        string
	Title      panel.Title
	MessageMax int
	Timeout    time.Duration
	// SeenFor is how long an aired release is not repeated (default 72h).
	SeenFor time.Duration

	mu   sync.Mutex
	seen map[string]time.Time // repo@tag -> when aired
}

func (r *Releases) Name() string { return "releases" }

func (r *Releases) Build(ctx context.Context, now time.Time, p schedule.Params) ([]Item, error) {
	if r.Lister == nil {
		return nil, nil
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	all, err := r.Lister.Releases(cctx, now.Add(-p.Duration("within", 24*time.Hour)))
	if err != nil {
		return nil, fmt.Errorf("releases: %w", err)
	}
	repos := p.Strings("repos")

	r.mu.Lock()
	defer r.mu.Unlock()
	r.prune(now)

	count := p.Int("count", 1)
	var items []Item
	for _, rel := range all {
		if len(items) == count {
			break
		}
		if len(repos) > 0 && !slices.Contains(repos, rel.Repo) {
			continue
		}
		key := rel.Repo + "@" + rel.Tag
		if _, ok := r.seen[key]; ok {
			continue
		}
		if !IsPreview(ctx) {
			r.seen[key] = now
		}
		msg := fmt.Sprintf("%s released %s %s at %s", r.Org, rel.Repo, rel.Tag, rel.At.In(now.Location()).Format("15:04"))
		if rel.Note != "" {
			msg = fmt.Sprintf("%s %s: %s", rel.Repo, rel.Tag, rel.Note)
		}
		items = append(items, Item{
			Title:   r.Title.Fit("Released: " + rel.Repo + " " + rel.Tag),
			Message: panel.Truncate(msg, r.MessageMax),
		})
	}
	return items, nil
}

func (r *Releases) prune(now time.Time) {
	if r.seen == nil {
		r.seen = map[string]time.Time{}
	}
	ttl := r.SeenFor
	if ttl <= 0 {
		ttl = 72 * time.Hour
	}
	for k, at := range r.seen {
		if now.Sub(at) > ttl {
			delete(r.seen, k)
		}
	}
}
