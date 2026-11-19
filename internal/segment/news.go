package segment

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"
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
// Parameters: count (default 1), feeds (names, default all), mix (default
// false): the feeds take turns, each in its own order, instead of all
// headlines newest first; summary (default false): with moderation, the
// scrolling line is "<feed>: <one sentence>" summed up from the feed's
// description, numbers only from the headline and the description; else
// "<feed>: <headline>".
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
	// BlockWords skips a headline that has one of these words, matched at
	// the start of a word and case-insensitive ("fuck" also catches
	// "fucking", but "cunt" not "Scunthorpe"). nil: DefaultBlockWords.
	BlockWords []string
	Log        *slog.Logger

	mu      sync.Mutex
	seen    map[string]time.Time // headline key -> when aired
	blockRE *regexp.Regexp
	blockOK bool // blockRE built
}

// DefaultBlockWords keep swear words off the office display.
var DefaultBlockWords = []string{
	"shit", "bullshit", "fuck", "motherfuck", "cunt", "bitch", "asshole",
	"dickhead", "wanker", "bollocks", "piss", "bastard", "crap",
}

// blocked reports whether title has a block word. Called with n.mu held.
func (n *News) blocked(title string) bool {
	if !n.blockOK {
		n.blockOK = true
		words := n.BlockWords
		if words == nil {
			words = DefaultBlockWords
		}
		var alts []string
		for _, w := range words {
			if w = strings.TrimSpace(w); w != "" {
				alts = append(alts, regexp.QuoteMeta(w))
			}
		}
		if len(alts) > 0 {
			n.blockRE = regexp.MustCompile(`(?i)\b(?:` + strings.Join(alts, "|") + `)`)
		}
	}
	return n.blockRE != nil && n.blockRE.MatchString(title)
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
		wg     sync.WaitGroup
		mu     sync.Mutex
		byFeed = make([][]Headline, len(feeds))
		errs   int
	)
	for i, f := range feeds {
		wg.Add(1)
		go func(i int, f Feed) {
			defer wg.Done()
			hs, err := n.Fetch(ctx, f)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs++
				n.logger().Warn("feed failed, skipped", "feed", f.Name, "error", err)
				return
			}
			byFeed[i] = hs
		}(i, f)
	}
	wg.Wait()
	if errs == len(feeds) {
		return nil, fmt.Errorf("news: all %d feeds failed", errs)
	}
	mix := p.Bool("mix")
	if mix {
		orderFeeds(feeds, p.Strings("feeds"), byFeed)
	}

	n.mu.Lock()
	defer n.mu.Unlock()
	n.prune(now)

	count := p.Int("count", 1)
	var all []Headline
	if mix {
		all = n.takeTurns(now, byFeed, count)
	} else {
		for _, hs := range byFeed {
			all = append(all, hs...)
		}
		// newest first; undated headlines last, in feed order
		sort.SliceStable(all, func(i, j int) bool { return all[i].Published.After(all[j].Published) })
	}

	var items []Item
	for _, h := range all {
		if len(items) == count {
			break
		}
		if !n.airable(now, h) {
			continue
		}
		key := h.Feed + "\x00" + h.ID
		if !IsPreview(ctx) {
			n.seen[key] = now
		}
		msg := h.Feed + ": " + h.Title
		if p.Bool("summary") {
			if line, ok := n.Moderator.Summary(ctx, h.Title, h.Description, n.MessageMax-len([]rune(h.Feed))-2); ok {
				msg = h.Feed + ": " + line
			}
		}
		items = append(items, Item{
			Title:   n.Moderator.Headline(ctx, h.Title, n.Title),
			Message: panel.Truncate(msg, n.MessageMax),
		})
	}
	return items, nil
}

// airable reports whether h may air: not too old, not aired within
// SeenFor, no block word. Called with n.mu held.
func (n *News) airable(now time.Time, h Headline) bool {
	if n.MaxAge > 0 && !h.Published.IsZero() && now.Sub(h.Published) > n.MaxAge {
		return false
	}
	if _, ok := n.seen[h.Feed+"\x00"+h.ID]; ok {
		return false
	}
	if n.blocked(h.Title) {
		n.logger().Debug("headline skipped, block word", "feed", h.Feed, "title", h.Title)
		return false
	}
	return true
}

// takeTurns picks up to count airable headlines, one per feed in turn,
// each feed in its own order. A feed with nothing left drops out. Called
// with n.mu held.
func (n *News) takeTurns(now time.Time, byFeed [][]Headline, count int) []Headline {
	next := make([]int, len(byFeed))
	var out []Headline
	for len(out) < count {
		took := false
		for i, hs := range byFeed {
			if len(out) == count {
				break
			}
			for next[i] < len(hs) {
				h := hs[next[i]]
				next[i]++
				if n.airable(now, h) {
					out = append(out, h)
					took = true
					break
				}
			}
		}
		if !took {
			break
		}
	}
	return out
}

// orderFeeds sorts byFeed (fetched for feeds, in RADIO_NEWS_FEEDS order)
// into the order the slot lists them in want, so the turns follow the slot.
func orderFeeds(feeds []Feed, want []string, byFeed [][]Headline) {
	if len(want) == 0 {
		return
	}
	idx := make([]int, len(feeds))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		return slices.Index(want, feeds[idx[a]].Name) < slices.Index(want, feeds[idx[b]].Name)
	})
	sorted := make([][]Headline, len(idx))
	for i, j := range idx {
		sorted[i] = byFeed[j]
	}
	copy(byFeed, sorted)
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
