package segment

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/panel"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/schedule"
)

// mixFetch serves per feed a list of titles in feed order; the dates make
// "newest first" differ from that order (the last title is the newest).
func mixFetch(feeds map[string][]string) FeedFetcher {
	return func(_ context.Context, f Feed) ([]Headline, error) {
		var hs []Headline
		for i, t := range feeds[f.Name] {
			hs = append(hs, Headline{Feed: f.Name, ID: t, Title: t, Published: now.Add(-time.Hour + time.Duration(i)*time.Minute)})
		}
		return hs, nil
	}
}

func messages(items []Item) string {
	var out []string
	for _, it := range items {
		out = append(out, it.Message)
	}
	return strings.Join(out, " | ")
}

func TestNewsMixTakesTurns(t *testing.T) {
	n := &News{
		Feeds: []Feed{{Name: "heise"}, {Name: "hn"}, {Name: "cncf"}},
		Fetch: mixFetch(map[string][]string{
			"heise": {"h1", "h2", "h3", "h4"},
			"hn":    {"n1"},
			"cncf":  {"c1", "c2"},
		}),
		Title: panel.Card4x6, MessageMax: 120,
	}
	ctx := context.Background()

	items, err := n.Build(ctx, now, schedule.Params{"count": 6, "mix": true})
	if err != nil {
		t.Fatal(err)
	}
	// turns in RADIO_NEWS_FEEDS order, each feed in its own order; hn has
	// nothing left after n1 and drops out
	if got, want := messages(items), "heise: h1 | hn: n1 | cncf: c1 | heise: h2 | cncf: c2 | heise: h3"; got != want {
		t.Fatalf("mix:\n got %s\nwant %s", got, want)
	}

	// seen headlines are skipped within a feed's turn
	items, _ = n.Build(ctx, now, schedule.Params{"count": 3, "mix": true})
	if got, want := messages(items), "heise: h4"; got != want {
		t.Fatalf("after airing: got %s, want %s", got, want)
	}
}

func TestNewsMixFollowsSlotFeedOrder(t *testing.T) {
	n := &News{
		Feeds: []Feed{{Name: "heise"}, {Name: "hn"}, {Name: "cncf"}},
		Fetch: mixFetch(map[string][]string{
			"heise": {"h1", "h2"},
			"hn":    {"n1", "n2"},
			"cncf":  {"c1"},
		}),
		Title: panel.Card4x6, MessageMax: 120,
	}
	items, err := n.Build(context.Background(), now, schedule.Params{"count": 4, "mix": true, "feeds": []any{"hn", "heise"}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := messages(items), "hn: n1 | heise: h1 | hn: n2 | heise: h2"; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestNewsWithoutMixIsNewestFirst(t *testing.T) {
	n := &News{
		Feeds: []Feed{{Name: "heise"}, {Name: "hn"}},
		Fetch: mixFetch(map[string][]string{"heise": {"h1", "h2", "h3"}, "hn": {"n1"}}),
		Title: panel.Card4x6, MessageMax: 120,
	}
	items, _ := n.Build(context.Background(), now, schedule.Params{"count": 2})
	if got, want := messages(items), "heise: h3 | heise: h2"; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}
