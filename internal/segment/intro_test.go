package segment

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/moderation"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/panel"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/schedule"
)

// fixedSegment returns its items, or nothing.
type fixedSegment struct {
	name  string
	items []Item
	err   error
}

func (f fixedSegment) Name() string { return f.name }
func (f fixedSegment) Build(context.Context, time.Time, schedule.Params) ([]Item, error) {
	return f.items, f.err
}

// countingClient answers and counts the calls.
type countingClient struct {
	answer string
	calls  int
}

func (c *countingClient) Complete(context.Context, string, string) (string, error) {
	c.calls++
	return c.answer, nil
}

func TestIntroOnlyWhenAskedAndSomethingAirs(t *testing.T) {
	host := &Host{Title: panel.Card4x6, MessageMax: 120}
	headline := Item{Title: "Go 1.27 is out", Message: "go.dev"}
	r := NewRegistry(
		fixedSegment{name: "news", items: []Item{headline}},
		fixedSegment{name: "infra-facts"},                         // nothing to say
		fixedSegment{name: "daily-summary", err: errors.New("x")}, // failed
	).WithIntros(host)
	on := schedule.Params{"intro": true}

	items, err := r["news"].Build(context.Background(), now, on)
	if err != nil || len(items) != 2 || items[1] != headline {
		t.Fatalf("news with intro: %+v, %v", items, err)
	}
	if items[0].Title != "Next up: News" || items[0].Message != "The latest headlines" {
		t.Errorf("intro card %+v", items[0])
	}
	if !panel.Card4x6.Fits(items[0].Title) {
		t.Errorf("intro title does not fit: %q", items[0].Title)
	}
	if items, _ := r["news"].Build(context.Background(), now, nil); len(items) != 1 {
		t.Errorf("without intro: %+v", items)
	}
	// "on" from `once news intro=on` and the UI's preview
	if items, _ := r["news"].Build(context.Background(), now, schedule.Params{"intro": "on"}); len(items) != 2 {
		t.Errorf("intro=on: %+v", items)
	}
	if items, err := r["infra-facts"].Build(context.Background(), now, on); err != nil || len(items) != 0 {
		t.Errorf("nothing to say still airs nothing: %+v, %v", items, err)
	}
	if items, err := r["daily-summary"].Build(context.Background(), now, on); err == nil || len(items) != 0 {
		t.Errorf("a failed segment gets no intro: %+v, %v", items, err)
	}
	if r["news"].Name() != "news" || !r.Has("infra-facts") {
		t.Error("the wrapped registry keeps the names")
	}
}

func TestIntroModeration(t *testing.T) {
	cases := []struct {
		answer, want string
	}{
		{"Grab a coffee, here comes the news!", "Grab a coffee, here comes the news!"},
		// a number is never the model's: fixed line
		{"Here are the top 5 news stories!", "The latest headlines"},
		// the segment's name dropped: fixed line
		{"Stay tuned, something great is coming!", "The latest headlines"},
	}
	for _, c := range cases {
		var results []string
		h := &Host{Title: panel.Card4x6, MessageMax: 120, Moderator: &moderation.Moderator{
			Client:  answer(c.answer),
			Observe: func(kind, result string) { results = append(results, kind+"/"+result) },
		}}
		it := h.Intro(context.Background(), "news")
		if it.Message != c.want || it.Title != "Next up: News" {
			t.Errorf("answer %q: %+v, want message %q", c.answer, it, c.want)
		}
		if len(results) != 1 || results[0][:6] != "intro/" {
			t.Errorf("observed %v", results)
		}
	}

	// an unknown segment still gets a sensible card
	it := (&Host{Title: panel.Card4x6, MessageMax: 120}).Intro(context.Background(), "weather-report")
	if it.Title != panel.Card4x6.Fit("Next up: weather report") || it.Message != "Coming up next" {
		t.Errorf("unknown segment: %+v", it)
	}
}

func TestTimeCheckCasual(t *testing.T) {
	const date = "Tuesday, 17 November 2026"
	casual := schedule.Params{"casual": true}

	// without moderation, or not asked: the date
	if items, _ := (TimeCheck{}).Build(context.Background(), now, casual); items[0].Message != date {
		t.Errorf("no moderator: %+v", items)
	}
	c := &countingClient{answer: "It's 15:00 on a Tuesday, the afternoon is all yours."}
	tc := TimeCheck{Moderator: &moderation.Moderator{Client: c}, MessageMax: 120}
	if items, _ := tc.Build(context.Background(), now, nil); items[0].Message != date || c.calls != 0 {
		t.Errorf("not casual: %+v, %d calls", items, c.calls)
	}

	items, _ := tc.Build(context.Background(), now, casual)
	if items[0].Title != "It's 15:00" || items[0].Message != c.answer {
		t.Errorf("casual: %+v", items)
	}
	for _, bad := range []string{
		"Tuesday afternoon, the week is flying by.",       // drops the time
		"It's 15:00, time for a break.",                   // drops the weekday
		"It's 15:00 on a Tuesday, only 3 hours to go!",    // a number of its own
		"It's fifteen hundred on Tuesday, keep it going.", // spelled out
	} {
		tc.Moderator.Client = answer(bad)
		if items, _ := tc.Build(context.Background(), now, casual); items[0].Message != date {
			t.Errorf("answer %q aired: %+v", bad, items)
		}
	}
}

func TestParamsBool(t *testing.T) {
	p := schedule.Params{"a": true, "b": "yes", "c": "On", "d": false, "e": "no", "f": 1}
	for k, want := range map[string]bool{"a": true, "b": true, "c": true, "d": false, "e": false, "f": false, "unset": false} {
		if p.Bool(k) != want {
			t.Errorf("Bool(%q) = %v", k, !want)
		}
	}
}
