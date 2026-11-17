package segment

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/moderation"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/panel"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/schedule"
)

// FeedFetcher loads the headlines of a feed. FetchFeed over HTTP in
// production, a fake in tests.
type FeedFetcher func(ctx context.Context, f Feed) ([]Headline, error)

// News airs the newest unseen headlines of the configured feeds.
//
// Parameters: count (default 1), feeds (names, default all).
type News struct {
	Feeds     []Feed
	Fetch     FeedFetcher
	Moderator *moderation.Moderator
	Title     panel.Title
	// MessageMax caps the scrolling line in characters.
	MessageMax int
	// SeenFor is how long an aired headline is not repeated.
	SeenFor time.Duration
	// MaxAge skips headlines older than this; 0 keeps all. Headlines
	// without a date are kept.
	MaxAge time.Duration
	Log    *slog.Logger

	mu   sync.Mutex
	seen map[string]time.Time // headline key -> when aired
}

func (n *News) Name() string { return "news" }

// HTTPFetcher fetches feeds with hc.
func HTTPFetcher(hc *http.Client) FeedFetcher {
	return func(ctx context.Context, f Feed) ([]Headline, error) { return FetchFeed(ctx, hc, f) }
}

func (n *News) Build(ctx context.Context, now time.Time, p schedule.Params) ([]Item, error) {
	feeds := n.Feeds
	if want := p.Strings("feeds"); len(want) > 0 {
		feeds = nil
		for _, f := range n.Feeds {
			if slices.Contains(want, f.Name) {
				feeds = append(feeds, f)
			}
		}
	}
	if len(feeds) == 0 {
		return nil, fmt.Errorf("news: no feeds configured (RADIO_NEWS_FEEDS) or none matches %v", p.Strings("feeds"))
	}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		all  []Headline
		errs int
	)
	for _, f := range feeds {
		wg.Add(1)
		go func(f Feed) {
			defer wg.Done()
			hs, err := n.Fetch(ctx, f)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs++
				n.logger().Warn("feed failed, skipped", "feed", f.Name, "error", err)
				return
			}
			all = append(all, hs...)
		}(f)
	}
	wg.Wait()
	if errs == len(feeds) {
		return nil, fmt.Errorf("news: all %d feeds failed", errs)
	}

	// newest first; undated headlines last, in feed order
	sort.SliceStable(all, func(i, j int) bool { return all[i].Published.After(all[j].Published) })

	n.mu.Lock()
	defer n.mu.Unlock()
	n.prune(now)

	count := p.Int("count", 1)
	var items []Item
	for _, h := range all {
		if len(items) == count {
			break
		}
		if n.MaxAge > 0 && !h.Published.IsZero() && now.Sub(h.Published) > n.MaxAge {
			continue
		}
		key := h.Feed + "\x00" + h.ID
		if _, ok := n.seen[key]; ok {
			continue
		}
		if !IsPreview(ctx) {
			n.seen[key] = now
		}
		items = append(items, Item{
			Title:   n.Moderator.Headline(ctx, h.Title, n.Title),
			Message: panel.Truncate(h.Feed+": "+h.Title, n.MessageMax),
		})
	}
	return items, nil
}

func (n *News) prune(now time.Time) {
	if n.seen == nil {
		n.seen = map[string]time.Time{}
	}
	ttl := n.SeenFor
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	for k, at := range n.seen {
		if now.Sub(at) > ttl {
			delete(n.seen, k)
		}
	}
}

func (n *News) logger() *slog.Logger {
	if n.Log != nil {
		return n.Log
	}
	return slog.Default()
}
