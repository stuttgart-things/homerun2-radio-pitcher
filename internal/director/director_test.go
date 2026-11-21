package director

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	homerun "github.com/stuttgart-things/homerun-library/v4"

	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/schedule"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/segment"
)

type fixedSchedule struct{ s *schedule.Schedule }

func (f fixedSchedule) Get() (*schedule.Schedule, bool, error) { return f.s, false, nil }

type recorder struct {
	mu   sync.Mutex
	msgs []homerun.Message
	err  error
}

func (r *recorder) Pitch(_ context.Context, m homerun.Message) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return "", r.err
	}
	r.msgs = append(r.msgs, m)
	return "id", nil
}
func (r *recorder) Ready(context.Context) error { return nil }
func (r *recorder) Close() error                { return nil }

type countSeg struct {
	name  string
	n     int
	err   error
	calls int
	got   schedule.Params
}

func (c *countSeg) Name() string { return c.name }
func (c *countSeg) Build(_ context.Context, now time.Time, p schedule.Params) ([]segment.Item, error) {
	c.calls++
	c.got = p
	var items []segment.Item
	for i := range p.Int("count", c.n) {
		items = append(items, segment.Item{Title: c.name + " " + now.Format("15:04"), Message: strings.Repeat("x", i+1)})
	}
	return items, c.err
}

func setup(t *testing.T, plan string, segs ...segment.Segment) (*Director, *recorder, *[]time.Duration) {
	t.Helper()
	s, err := schedule.Parse(strings.NewReader(plan))
	if err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Europe/Berlin")
	rec := &recorder{}
	var slept []time.Duration
	d := &Director{
		Schedule: fixedSchedule{s},
		Segments: segment.NewRegistry(segs...),
		Pitcher:  rec,
		Station:  Station{System: "radio", Severity: "INFO", Author: "radio"},
		Location: loc,
		Spacing:  15 * time.Second,
		Sleep:    func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil },
	}
	return d, rec, &slept
}

const plan = `
clock:
  ":00": { segment: time-check }
  ":05": { segment: news, count: 3 }
  ":35": { segment: tabletennis-stats }
`

func TestTickAirsDueSlot(t *testing.T) {
	news := &countSeg{name: "news", n: 1}
	tc := &countSeg{name: "time-check", n: 1}
	d, rec, slept := setup(t, plan, news, tc)

	// 14:05:00 UTC = 15:05 in Berlin
	at := time.Date(2026, 11, 17, 14, 5, 0, 500e6, time.UTC)
	d.Tick(context.Background(), at, false)

	if news.calls != 1 || tc.calls != 0 {
		t.Fatalf("calls news=%d time-check=%d", news.calls, tc.calls)
	}
	if len(rec.msgs) != 3 {
		t.Fatalf("pitched %d, want 3", len(rec.msgs))
	}
	if len(*slept) != 2 || (*slept)[0] != 15*time.Second {
		t.Fatalf("spacing = %v", *slept)
	}
	m := rec.msgs[0]
	if m.System != "radio" || m.Severity != "INFO" || m.Tags != "segment=news" || m.Author != "radio" {
		t.Fatalf("message = %+v", m)
	}
	if m.Title != "news 15:05" {
		t.Fatalf("segment saw local time %q", m.Title)
	}
	if _, err := time.Parse(time.RFC3339, m.Timestamp); err != nil {
		t.Fatalf("timestamp %q: %v", m.Timestamp, err)
	}
	if d.LastAired().IsZero() {
		t.Fatal("LastAired not set")
	}
}

func TestTickOncePerMinute(t *testing.T) {
	tc := &countSeg{name: "time-check", n: 1}
	d, rec, _ := setup(t, plan, tc, &countSeg{name: "news"})
	at := time.Date(2026, 11, 17, 14, 0, 0, 0, time.UTC)
	d.Tick(context.Background(), at, false)
	d.Tick(context.Background(), at.Add(30*time.Second), false)
	if tc.calls != 1 || len(rec.msgs) != 1 {
		t.Fatalf("calls=%d pitched=%d", tc.calls, len(rec.msgs))
	}
	d.Tick(context.Background(), at.Add(time.Hour), false)
	if tc.calls != 2 {
		t.Fatalf("next hour: calls=%d", tc.calls)
	}
}

func TestTickSkipsFreeMinutesAndUnknownSegments(t *testing.T) {
	news := &countSeg{name: "news"}
	d, rec, _ := setup(t, plan, news, &countSeg{name: "time-check"})
	d.Tick(context.Background(), time.Date(2026, 11, 17, 14, 1, 0, 0, time.UTC), false)
	d.Tick(context.Background(), time.Date(2026, 11, 17, 14, 35, 0, 0, time.UTC), false)
	if len(rec.msgs) != 0 {
		t.Fatalf("pitched %+v", rec.msgs)
	}
}

func TestTickSkipsWhileOnAir(t *testing.T) {
	slow := &blockingSeg{release: make(chan struct{}), started: make(chan struct{})}
	d, _, _ := setup(t, "clock:\n  \":00\": { segment: slow }\n  \":01\": { segment: slow }\n", slow)
	ctx := context.Background()
	d.Tick(ctx, time.Date(2026, 11, 17, 14, 0, 0, 0, time.UTC), true)
	<-slow.started
	d.Tick(ctx, time.Date(2026, 11, 17, 14, 1, 0, 0, time.UTC), true)
	close(slow.release)
	d.wg.Wait()
	if n := slow.calls.Load(); n != 1 {
		t.Fatalf("calls = %d, want 1", n)
	}
}

func TestAirErrors(t *testing.T) {
	broken := &countSeg{name: "news", err: errors.New("feeds down")}
	var observed []string
	d, rec, _ := setup(t, plan, broken)
	d.Observe = func(seg string, err error) { observed = append(observed, seg+":"+okOrErr(err)) }

	if _, err := d.Air(context.Background(), "news", nil, time.Now()); err == nil {
		t.Fatal("segment error not returned")
	}
	if _, err := d.Air(context.Background(), "jingle", nil, time.Now()); err == nil {
		t.Fatal("unknown segment accepted")
	}
	ok := &countSeg{name: "time-check", n: 2}
	d.Segments["time-check"] = ok
	rec.err = errors.New("redis down")
	if n, err := d.Air(context.Background(), "time-check", nil, time.Now()); err == nil || n != 0 {
		t.Fatalf("pitch failure: n=%d err=%v", n, err)
	}
	if len(observed) != 3 || observed[0] != "news:feeds down" || observed[1] != "time-check:redis down" {
		t.Fatalf("observed = %v", observed)
	}
}

func TestAirNothingToSay(t *testing.T) {
	d, rec, _ := setup(t, plan, &countSeg{name: "news", n: 0})
	n, err := d.Air(context.Background(), "news", nil, time.Now())
	if err != nil || n != 0 || len(rec.msgs) != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestAirPassesParams(t *testing.T) {
	news := &countSeg{name: "news", n: 1}
	d, _, _ := setup(t, plan, news)
	d.Tick(context.Background(), time.Date(2026, 11, 17, 14, 5, 0, 0, time.UTC), false)
	if news.got.Int("count", 0) != 3 {
		t.Fatalf("params = %v", news.got)
	}
}

type blockingSeg struct {
	release chan struct{}
	started chan struct{}
	calls   atomic.Int32
}

func (b *blockingSeg) Name() string { return "slow" }
func (b *blockingSeg) Build(context.Context, time.Time, schedule.Params) ([]segment.Item, error) {
	if b.calls.Add(1) == 1 {
		close(b.started)
	}
	<-b.release
	return nil, nil
}

func okOrErr(err error) string {
	if err == nil {
		return "ok"
	}
	return err.Error()
}

const showPlan = `
clock:
  ":00": { segment: time-check }
clocks:
  night: {}
programme:
  "07:00-10:00": { show: morning-show, extra: [news, unknown-extra] }
  "22:00-06:00": { show: night, clock: night }
`

func TestTickAirsExtraThenClock(t *testing.T) {
	news := &countSeg{name: "news", n: 2}
	tc := &countSeg{name: "time-check", n: 1}
	d, rec, slept := setup(t, showPlan, news, tc)

	// 06:00 UTC = 07:00 in Berlin: the morning show starts, extra first
	d.Tick(context.Background(), time.Date(2026, 11, 17, 6, 0, 0, 0, time.UTC), false)
	var tags []string
	for _, m := range rec.msgs {
		tags = append(tags, m.Tags)
	}
	if got := strings.Join(tags, ","); got != "segment=news,segment=news,segment=time-check" {
		t.Fatalf("aired %s", got)
	}
	// news items spaced, then a pause before the clock slot
	if len(*slept) != 2 {
		t.Fatalf("spacing = %v", *slept)
	}

	// 08:00 Berlin: the extra was once per window
	d.Tick(context.Background(), time.Date(2026, 11, 17, 7, 0, 0, 0, time.UTC), false)
	if news.calls != 1 || tc.calls != 2 {
		t.Fatalf("calls news=%d time-check=%d", news.calls, tc.calls)
	}

	// 23:00 Berlin: night, empty clock
	d.Tick(context.Background(), time.Date(2026, 11, 17, 22, 0, 0, 0, time.UTC), false)
	if tc.calls != 2 {
		t.Fatalf("night aired time-check")
	}
}

func TestMessageImageTag(t *testing.T) {
	d := &Director{Station: Station{System: "radio", Severity: "INFO", Author: "radio"}}
	m := d.Message("spots", segment.Item{Title: "Evening", Message: "Evening", Image: "sunset.gif"}, time.Now(), "show=news")
	if m.Tags != "segment=spots,show=news,gif=sunset.gif" {
		t.Errorf("tags = %q", m.Tags)
	}
	if m := d.Message("news", segment.Item{Title: "t"}, time.Now()); m.Tags != "segment=news" {
		t.Errorf("tags without image = %q", m.Tags)
	}
}
