package segment

import (
	"context"
	"errors"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/moderation"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/panel"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/schedule"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/sources"
)

var now = time.Date(2026, 11, 17, 15, 0, 0, 0, time.UTC)

func TestTimeCheck(t *testing.T) {
	items, err := TimeCheck{}.Build(context.Background(), now, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Title != "It's 15:00" || items[0].Message != "Tuesday, 17 November 2026" {
		t.Fatalf("items = %+v", items)
	}
	if !panel.Card5x7.Fits(items[0].Title) {
		t.Fatal("time check title should fit the large font")
	}
}

const rssDoc = `<?xml version="1.0"?>
<rss version="2.0" xmlns:dc="http://purl.org/dc/elements/1.1/"><channel><title>x</title>
<item><title>Old &amp; boring</title><guid>1</guid><pubDate>Mon, 16 Nov 2026 08:00:00 +0000</pubDate></item>
<item><title><![CDATA[<b>Kubernetes 1.36</b> brings sidecar containers to every cluster in the lab]]></title><link>https://e/2</link><pubDate>Tue, 17 Nov 2026 14:00:00 +0000</pubDate></item>
<item><title>Ancient</title><guid>3</guid><dc:date>2026-10-01T08:00:00Z</dc:date></item>
<item><guid>4</guid></item>
</channel></rss>`

const atomDoc = `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom"><title>cncf</title>
<entry><title>Flux 3.0 released</title><id>urn:a</id><link href="https://c/a"/><updated>2026-11-17T12:00:00Z</updated></entry>
<entry><title>KubeCon recap</title><link href="https://c/b"/><published>2026-11-15T12:00:00Z</published></entry>
</feed>`

func TestParseFeed(t *testing.T) {
	hs, err := ParseFeed("heise", strings.NewReader(rssDoc))
	if err != nil {
		t.Fatal(err)
	}
	if len(hs) != 3 {
		t.Fatalf("headlines = %+v", hs)
	}
	if hs[0].Title != "Old & boring" || hs[0].ID != "1" {
		t.Errorf("entity: %+v", hs[0])
	}
	if hs[1].Title != "Kubernetes 1.36 brings sidecar containers to every cluster in the lab" || hs[1].ID != "https://e/2" {
		t.Errorf("markup: %+v", hs[1])
	}
	if hs[2].Published.IsZero() {
		t.Error("dc:date not parsed")
	}

	hs, err = ParseFeed("cncf", strings.NewReader(atomDoc))
	if err != nil {
		t.Fatal(err)
	}
	if len(hs) != 2 || hs[0].ID != "urn:a" || hs[1].ID != "https://c/b" || hs[1].Published.IsZero() {
		t.Fatalf("atom = %+v", hs)
	}

	if _, err := ParseFeed("x", strings.NewReader("<html></html>")); err == nil {
		t.Fatal("html accepted as a feed")
	}
}

func TestParseFeeds(t *testing.T) {
	fs, err := ParseFeeds("heise=https://www.heise.de/rss/heise-atom.xml, https://www.cncf.io/feed/ ,")
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 2 || fs[0].Name != "heise" || fs[1].Name != "www.cncf.io" {
		t.Fatalf("feeds = %+v", fs)
	}
	if _, err := ParseFeeds("x=ftp://a"); err == nil {
		t.Fatal("ftp accepted")
	}
}

func fakeFetch(docs map[string]string) FeedFetcher {
	return func(_ context.Context, f Feed) ([]Headline, error) {
		d, ok := docs[f.Name]
		if !ok {
			return nil, errors.New("down")
		}
		return ParseFeed(f.Name, strings.NewReader(d))
	}
}

func newsSegment(docs map[string]string) *News {
	return &News{
		Feeds:      []Feed{{Name: "heise", URL: "https://h"}, {Name: "cncf", URL: "https://c"}},
		Fetch:      fakeFetch(docs),
		Title:      panel.Card4x6,
		MessageMax: 120,
		MaxAge:     7 * 24 * time.Hour,
	}
}

func TestNewsNewestUnseenAndShortened(t *testing.T) {
	n := newsSegment(map[string]string{"heise": rssDoc, "cncf": atomDoc})
	items, err := n.Build(context.Background(), now, schedule.Params{"count": 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %+v", items)
	}
	// newest: heise 14:00, then cncf 12:00
	if !strings.HasPrefix(items[0].Message, "heise: Kubernetes 1.36") || items[1].Title != "Flux 3.0 released" {
		t.Fatalf("order: %+v", items)
	}
	if !panel.Card4x6.Fits(items[0].Title) || !strings.HasSuffix(items[0].Title, panel.Ellipsis) {
		t.Fatalf("title not shortened: %q", items[0].Title)
	}

	// no repeats: next run gets the next ones; "Ancient" is too old
	items, _ = n.Build(context.Background(), now.Add(5*time.Minute), schedule.Params{"count": 5})
	var titles []string
	for _, it := range items {
		titles = append(titles, it.Title)
	}
	if strings.Join(titles, "|") != "Old & boring|KubeCon recap" {
		t.Fatalf("second run = %v", titles)
	}

	// all seen: nothing to say, no error
	items, err = n.Build(context.Background(), now.Add(10*time.Minute), nil)
	if err != nil || len(items) != 0 {
		t.Fatalf("third run = %+v, %v", items, err)
	}

	// after SeenFor the headlines may air again
	items, _ = n.Build(context.Background(), now.Add(13*time.Hour), nil)
	if len(items) != 1 {
		t.Fatalf("after ttl = %+v", items)
	}
}

func TestNewsFeedFilterAndFailures(t *testing.T) {
	n := newsSegment(map[string]string{"cncf": atomDoc}) // heise is down
	items, err := n.Build(context.Background(), now, schedule.Params{"feeds": []any{"cncf"}})
	if err != nil || len(items) != 1 || !strings.HasPrefix(items[0].Message, "cncf: ") {
		t.Fatalf("filtered: %+v, %v", items, err)
	}
	// one feed down, the other answers
	items, err = n.Build(context.Background(), now, nil)
	if err != nil || len(items) != 1 {
		t.Fatalf("partial: %+v, %v", items, err)
	}
	// all down
	n = newsSegment(nil)
	if _, err := n.Build(context.Background(), now, nil); err == nil {
		t.Fatal("expected error when every feed fails")
	}
	if _, err := n.Build(context.Background(), now, schedule.Params{"feeds": "nope"}); err == nil {
		t.Fatal("expected error for unknown feed")
	}
}

func TestNewsWithModel(t *testing.T) {
	n := newsSegment(map[string]string{"heise": rssDoc})
	n.Moderator = &moderation.Moderator{Client: answer("Kubernetes 1.36: sidecars everywhere")}
	items, _ := n.Build(context.Background(), now, nil)
	if items[0].Title != "Kubernetes 1.36: sidecars everywhere" {
		t.Fatalf("title = %q", items[0].Title)
	}
	// the message keeps the original headline
	if !strings.Contains(items[0].Message, "brings sidecar containers") {
		t.Fatalf("message = %q", items[0].Message)
	}
}

func TestFetchFeedHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/gone" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(atomDoc))
	}))
	defer srv.Close()
	hs, err := FetchFeed(context.Background(), srv.Client(), Feed{Name: "c", URL: srv.URL + "/feed"})
	if err != nil || len(hs) != 2 {
		t.Fatalf("%v %v", hs, err)
	}
	if _, err := FetchFeed(context.Background(), srv.Client(), Feed{Name: "c", URL: srv.URL + "/gone"}); err == nil {
		t.Fatal("404 accepted")
	}
}

type fakeSource struct {
	name  string
	facts []sources.Fact
	err   error
}

func (f fakeSource) Name() string                                  { return f.name }
func (f fakeSource) Facts(context.Context) ([]sources.Fact, error) { return f.facts, f.err }

func fact(id, title, msg string) sources.Fact {
	return sources.Fact{ID: id, Title: title, Message: msg}
}

func infraSegment(srcs ...sources.Source) *InfraFacts {
	return &InfraFacts{
		Sources:    srcs,
		Title:      panel.Card4x6,
		MessageMax: 120,
		Rand:       rand.New(rand.NewPCG(1, 2)),
	}
}

func TestInfraFacts(t *testing.T) {
	dev := fakeSource{name: "dev", facts: []sources.Fact{
		fact("dev/pods", "312 pods on dev", "dev: 312 pods, 298 of them running"),
		fact("dev/nodes", "dev: 3 nodes", "dev: 3 of 3 nodes ready"),
	}}
	broken := fakeSource{name: "harvester", err: errors.New("connection refused")}
	s := infraSegment(dev, broken)

	items, err := s.Build(context.Background(), now, schedule.Params{"count": 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Title == items[1].Title {
		t.Fatalf("items = %+v", items)
	}
	// count larger than the facts available
	items, _ = s.Build(context.Background(), now, schedule.Params{"count": 5})
	if len(items) != 2 {
		t.Fatalf("items = %+v", items)
	}
}

func TestInfraFactsAvoidsRepeats(t *testing.T) {
	var facts []sources.Fact
	for _, id := range []string{"a", "b", "c", "d", "e", "f"} {
		facts = append(facts, fact(id, "title "+id, "message "+id))
	}
	s := infraSegment(fakeSource{name: "x", facts: facts})
	s.Recent = 4
	var last []string
	for range 20 {
		items, _ := s.Build(context.Background(), now, schedule.Params{"count": 2})
		for _, it := range items {
			for _, l := range last {
				if l == it.Title {
					t.Fatalf("%q repeated within the recent window", it.Title)
				}
			}
		}
		last = nil
		for _, it := range items {
			last = append(last, it.Title)
		}
	}
}

func TestInfraFactsFailures(t *testing.T) {
	if _, err := infraSegment().Build(context.Background(), now, nil); err == nil {
		t.Fatal("expected error without sources")
	}
	s := infraSegment(fakeSource{name: "x", err: errors.New("down")})
	if _, err := s.Build(context.Background(), now, nil); err == nil {
		t.Fatal("expected error when every source fails")
	}
	s = infraSegment(fakeSource{name: "x", facts: []sources.Fact{fact("a", "t", "m")}})
	if _, err := s.Build(context.Background(), now, schedule.Params{"sources": "y"}); err == nil {
		t.Fatal("expected error for unknown source filter")
	}
}

func TestInfraFactsModeration(t *testing.T) {
	src := fakeSource{name: "dev", facts: []sources.Fact{fact("dev/pods", "312 pods on dev", "dev: 312 pods, 298 of them running")}}

	s := infraSegment(src)
	s.Moderator = &moderation.Moderator{Client: answer("298 of 312 pods are humming along on dev.")}
	items, _ := s.Build(context.Background(), now, nil)
	if items[0].Message != "298 of 312 pods are humming along on dev." {
		t.Fatalf("moderated = %q", items[0].Message)
	}

	// the model invents a number: fixed text instead
	s = infraSegment(src)
	s.Moderator = &moderation.Moderator{Client: answer("14 pods on dev are not running.")}
	items, _ = s.Build(context.Background(), now, nil)
	if items[0].Message != "dev: 312 pods, 298 of them running" {
		t.Fatalf("fallback = %q", items[0].Message)
	}
}

func TestRegistry(t *testing.T) {
	r := NewRegistry(TimeCheck{}, &News{}, &InfraFacts{})
	if !r.Has("news") || r.Has("jingle") {
		t.Fatal("Has")
	}
	if strings.Join(r.Names(), ",") != "infra-facts,news,time-check" {
		t.Fatalf("names = %v", r.Names())
	}
}

type answer string

func (a answer) Complete(context.Context, string, string) (string, error) { return string(a), nil }

func TestPreviewKeepsNoState(t *testing.T) {
	n := newsSegment(map[string]string{"heise": rssDoc, "cncf": atomDoc})
	ctx := Preview(context.Background())
	if !IsPreview(ctx) || IsPreview(context.Background()) {
		t.Fatal("IsPreview")
	}
	first, _ := n.Build(ctx, now, schedule.Params{"count": 2})
	again, _ := n.Build(ctx, now, schedule.Params{"count": 2})
	aired, _ := n.Build(context.Background(), now, schedule.Params{"count": 2})
	if len(first) != 2 || first[0] != again[0] || first[0] != aired[0] {
		t.Fatalf("preview changed what airs: %v / %v / %v", first, again, aired)
	}

	s := infraSegment(fakeSource{name: "x", facts: []sources.Fact{fact("a", "A", "a"), fact("b", "B", "b")}})
	for range 5 {
		_, _ = s.Build(ctx, now, schedule.Params{"count": 2})
	}
	if len(s.recent) != 0 {
		t.Fatalf("preview remembered facts: %v", s.recent)
	}
}

// fetchHeadlines serves fixed headlines, newest first.
func fetchHeadlines(titles ...string) FeedFetcher {
	return func(_ context.Context, f Feed) ([]Headline, error) {
		var hs []Headline
		for i, t := range titles {
			hs = append(hs, Headline{Feed: f.Name, ID: t, Title: t, Published: now.Add(-time.Duration(i) * time.Minute)})
		}
		return hs, nil
	}
}

func TestNewsSkipsBlockWords(t *testing.T) {
	titles := []string{
		"We need smart people who give a shit",
		"FUCKING slow builds, again",
		"This is bullshit, says the CTO",
		"Scunthorpe council moves to Kubernetes", // "cunt" inside a word: kept
		"Go 1.27 is out",
	}
	n := &News{Feeds: []Feed{{Name: "hn"}}, Fetch: fetchHeadlines(titles...), Title: panel.Card4x6, MessageMax: 120}
	items, err := n.Build(context.Background(), now, schedule.Params{"count": 5})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range items {
		got = append(got, it.Message)
	}
	want := []string{"hn: Scunthorpe council moves to Kubernetes", "hn: Go 1.27 is out"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("default list: %q", got)
	}

	// an own list replaces the default; an empty one blocks nothing
	n = &News{Feeds: []Feed{{Name: "hn"}}, Fetch: fetchHeadlines(titles...), Title: panel.Card4x6, MessageMax: 120, BlockWords: []string{"go"}}
	if items, _ := n.Build(context.Background(), now, schedule.Params{"count": 5}); len(items) != 4 {
		t.Fatalf("own list: %+v", items)
	}
	n = &News{Feeds: []Feed{{Name: "hn"}}, Fetch: fetchHeadlines(titles...), Title: panel.Card4x6, MessageMax: 120, BlockWords: []string{}}
	if items, _ := n.Build(context.Background(), now, schedule.Params{"count": 5}); len(items) != 5 {
		t.Fatalf("empty list: %+v", items)
	}
}

func TestInfraFactsCombine(t *testing.T) {
	dev := fakeSource{name: "dev", facts: []sources.Fact{
		{ID: "dev/pods", Source: "dev", Title: "312 pods on dev", Message: "dev: 312 pods, 298 of them running"},
		{ID: "dev/nodes", Source: "dev", Title: "dev: 3 nodes", Message: "dev: 3 of 3 nodes ready"},
	}}
	combine := schedule.Params{"count": 2, "combine": true}

	// the model keeps every number and the source: one card
	s := infraSegment(dev)
	s.Moderator = &moderation.Moderator{Client: answer("dev runs 312 pods, 298 of them running, on 3 of 3 nodes ready.")}
	items, err := s.Build(context.Background(), now, combine)
	if err != nil || len(items) != 1 {
		t.Fatalf("combined: %+v, %v", items, err)
	}
	if items[0].Title != "dev" || items[0].Message != "dev runs 312 pods, 298 of them running, on 3 of 3 nodes ready." {
		t.Errorf("combined card %+v", items[0])
	}

	// a dropped number, an added one, a total, a dropped name, no model:
	// the facts air one by one, as without combine
	for _, a := range []string{
		"dev runs 312 pods on 3 nodes.",                            // 298 and one 3 dropped
		"dev runs 312 pods, 298 running, 14 not, on 3 of 3 nodes.", // 14 is new
		"dev: 312 pods, 298 of them running, 3 of 3 nodes ready, 318 in all.",
		"The cluster runs 312 pods, 298 of them running, on 3 of 3 nodes ready.",
	} {
		s := infraSegment(dev)
		s.Moderator = &moderation.Moderator{Client: answer(a)}
		if items, _ := s.Build(context.Background(), now, combine); len(items) != 2 {
			t.Errorf("answer %q: %+v", a, items)
		}
	}
	if items, _ := infraSegment(dev).Build(context.Background(), now, combine); len(items) != 2 {
		t.Errorf("no model: %+v", items)
	}

	// facts of two sources: a neutral title, both names kept
	two := []sources.Source{dev, fakeSource{name: "harvester", facts: []sources.Fact{
		{ID: "hv/vms", Source: "harvester", Title: "harvester: 12 VMs", Message: "harvester: 12 VMs"},
	}}}
	s = infraSegment(two...)
	s.Moderator = &moderation.Moderator{Client: answer("dev runs 312 pods, 298 of them running, on 3 of 3 nodes ready, and harvester has 12 VMs.")}
	items, _ = s.Build(context.Background(), now, schedule.Params{"count": 3, "combine": true})
	if len(items) != 1 || items[0].Title != "Infra facts" {
		t.Errorf("two sources: %+v", items)
	}
}
