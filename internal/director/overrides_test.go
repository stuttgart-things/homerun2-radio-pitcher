package director

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/schedule"
)

const overridePlan = `
clock:
  ":00": { segment: time-check }
  ":15": { segment: news }
clocks:
  quiet: {}
  newsy:
    ":10": { segment: news }
`

// berlin returns a time in Europe/Berlin on the test day.
func berlin(t *testing.T, hhmm string) time.Time {
	t.Helper()
	loc, _ := time.LoadLocation("Europe/Berlin")
	tm, err := time.ParseInLocation("2006-01-02 15:04", "2026-11-17 "+hhmm, loc)
	if err != nil {
		t.Fatal(err)
	}
	return tm
}

func TestPauseAndResume(t *testing.T) {
	tc := &countSeg{name: "time-check", n: 1}
	d, rec, _ := setup(t, overridePlan, tc, &countSeg{name: "news", n: 1})
	now := berlin(t, "09:50")

	if err := d.Pause(now, now.Add(25*time.Hour), "test"); err == nil {
		t.Fatal("pause beyond a day accepted")
	}
	if err := d.Pause(now, berlin(t, "11:30"), "test"); err != nil {
		t.Fatal(err)
	}
	if at, slots, _ := d.Next(now); at.Format("15:04") != "12:00" || slots[0].Segment != "time-check" {
		t.Fatalf("next while paused = %s", at.Format("15:04"))
	}
	d.Tick(context.Background(), berlin(t, "10:00"), false)
	d.Tick(context.Background(), berlin(t, "10:15"), false)
	if len(rec.msgs) != 0 {
		t.Fatalf("aired while paused: %d", len(rec.msgs))
	}
	// the pause ends on its own
	d.Tick(context.Background(), berlin(t, "12:00"), false)
	if tc.calls != 1 {
		t.Fatalf("not aired after the pause: %d", tc.calls)
	}
	if o := d.Overrides(berlin(t, "12:00")); !o.PausedUntil.IsZero() {
		t.Fatalf("pause still active: %+v", o)
	}

	_ = d.Pause(berlin(t, "12:01"), berlin(t, "14:00"), "test")
	d.Resume("test")
	d.Tick(context.Background(), berlin(t, "13:00"), false)
	if tc.calls != 2 {
		t.Fatal("resume did not end the pause")
	}
}

func TestSkipNext(t *testing.T) {
	tc := &countSeg{name: "time-check", n: 1}
	news := &countSeg{name: "news", n: 1}
	d, _, _ := setup(t, overridePlan, tc, news)

	at, err := d.SkipNext(berlin(t, "09:50"), "test")
	if err != nil || at.Format("15:04") != "10:00" {
		t.Fatalf("skip = %s, %v", at.Format("15:04"), err)
	}
	if next, _, _ := d.Next(berlin(t, "09:50")); next.Format("15:04") != "10:15" {
		t.Fatalf("next after skip = %s", next.Format("15:04"))
	}
	d.Tick(context.Background(), berlin(t, "10:00"), false)
	d.Tick(context.Background(), berlin(t, "10:15"), false)
	d.Tick(context.Background(), berlin(t, "11:00"), false)
	if tc.calls != 1 || news.calls != 1 {
		t.Fatalf("calls time-check=%d news=%d, want 1 1 (10:00 skipped once)", tc.calls, news.calls)
	}

	_, _ = d.SkipNext(berlin(t, "11:50"), "test")
	d.Unskip("test")
	d.Tick(context.Background(), berlin(t, "12:00"), false)
	if tc.calls != 2 {
		t.Fatal("unskip did not cancel")
	}
}

func TestSwitchClock(t *testing.T) {
	tc := &countSeg{name: "time-check", n: 1}
	news := &countSeg{name: "news", n: 1}
	d, _, _ := setup(t, overridePlan, tc, news)
	now := berlin(t, "09:55")

	if err := d.SwitchClock(now, "missing", now.Add(time.Hour), "test"); err == nil || !strings.Contains(err.Error(), "not in the schedule") {
		t.Fatalf("unknown clock: %v", err)
	}
	if err := d.SwitchClock(now, "newsy", berlin(t, "11:00"), "test"); err != nil {
		t.Fatal(err)
	}
	s, _, _ := d.Schedule.Get()
	if name, _ := d.ClockAt(s, berlin(t, "10:30")); name != "newsy" {
		t.Fatalf("ClockAt = %s", name)
	}
	d.Tick(context.Background(), berlin(t, "10:00"), false) // newsy has no :00
	d.Tick(context.Background(), berlin(t, "10:10"), false)
	d.Tick(context.Background(), berlin(t, "11:00"), false) // switch over
	if tc.calls != 1 || news.calls != 1 {
		t.Fatalf("calls time-check=%d news=%d", tc.calls, news.calls)
	}

	_ = d.SwitchClock(berlin(t, "11:05"), "quiet", berlin(t, "13:00"), "test")
	d.RestoreClock("test")
	if name, _ := d.ClockAt(s, berlin(t, "11:30")); name != "default" {
		t.Fatalf("after restore ClockAt = %s", name)
	}
}

func TestAirNow(t *testing.T) {
	news := &countSeg{name: "news", n: 2}
	d, rec, _ := setup(t, overridePlan, news)
	if err := d.AirNow("nope", nil, "test"); err == nil {
		t.Fatal("unknown segment accepted")
	}
	if err := d.AirNow("news", schedule.Params{"count": 1}, "test"); err != nil {
		t.Fatal(err)
	}
	d.wg.Wait()
	if news.calls != 1 || len(rec.msgs) != 1 {
		t.Fatalf("calls=%d pitched=%d", news.calls, len(rec.msgs))
	}
	d.onAir.Store(true)
	if err := d.AirNow("news", nil, "test"); err == nil {
		t.Fatal("air now while on air accepted")
	}
}
