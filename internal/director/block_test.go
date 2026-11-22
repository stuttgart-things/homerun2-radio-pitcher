package director

import (
	"context"
	"reflect"
	"testing"
	"time"
)

const blockPlan = `
clock:
  ":00":
    - { segment: time-check }
    - { segment: news, count: 2, spacing: 25s, card-tags: [show=news] }
    - { segment: infra-facts, count: 1, card-tags: "show=news" }
  ":30": { segment: time-check }
`

func TestTickAirsBlockWithSpacingAndTags(t *testing.T) {
	tc := &countSeg{name: "time-check", n: 1}
	news := &countSeg{name: "news", n: 1}
	infra := &countSeg{name: "infra-facts", n: 1}
	d, rec, slept := setup(t, blockPlan, tc, news, infra)

	// 14:00 UTC = 15:00 Berlin
	d.Tick(context.Background(), time.Date(2026, 11, 17, 14, 0, 0, 0, time.UTC), false)

	var tags []string
	for _, m := range rec.msgs {
		tags = append(tags, m.Tags)
	}
	want := []string{"segment=time-check", "segment=news,show=news", "segment=news,show=news", "segment=infra-facts,show=news"}
	if !reflect.DeepEqual(tags, want) {
		t.Fatalf("tags:\n got %v\nwant %v", tags, want)
	}
	// time-check → news: the default; between news items and after the
	// news slot: its own 25 s
	wantSleep := []time.Duration{15 * time.Second, 25 * time.Second, 25 * time.Second}
	if !reflect.DeepEqual(*slept, wantSleep) {
		t.Fatalf("spacing: got %v, want %v", *slept, wantSleep)
	}
}

func TestTickBlockSkipsGapOfSilentSlot(t *testing.T) {
	tc := &countSeg{name: "time-check", n: 1}
	news := &countSeg{name: "news", n: 1}
	silent := &countSeg{name: "releases", n: 0}
	infra := &countSeg{name: "infra-facts", n: 1}
	d, rec, slept := setup(t, `
clock:
  ":00":
    - { segment: time-check }
    - { segment: news, count: 1, spacing: 25s }
    - { segment: releases, spacing: 25s }
    - { segment: infra-facts, count: 1 }
`, tc, news, silent, infra)
	d.Tick(context.Background(), time.Date(2026, 11, 17, 14, 0, 0, 0, time.UTC), false)
	if len(rec.msgs) != 3 {
		t.Fatalf("pitched %d, want 3", len(rec.msgs))
	}
	// time-check → news 15 s, news → (releases silent) → infra 25 s once
	if want := []time.Duration{15 * time.Second, 25 * time.Second}; !reflect.DeepEqual(*slept, want) {
		t.Fatalf("spacing: got %v, want %v", *slept, want)
	}
}

func TestJingleOnFirstCard(t *testing.T) {
	tc := &countSeg{name: "time-check", n: 1}
	news := &countSeg{name: "news", n: 1}
	d, rec, _ := setup(t, `
clock:
  ":00":
    - { segment: time-check, jingle: station-id.gif }
    - { segment: news, count: 2, card-tags: [show=news] }
`, tc, news)
	d.Tick(context.Background(), time.Date(2026, 11, 17, 14, 0, 0, 0, time.UTC), false)
	var tags []string
	for _, m := range rec.msgs {
		tags = append(tags, m.Tags)
	}
	want := []string{"segment=time-check,gif=station-id.gif", "segment=news,show=news", "segment=news,show=news"}
	if !reflect.DeepEqual(tags, want) {
		t.Fatalf("tags:\n got %v\nwant %v", tags, want)
	}
	if a := d.Recent(1)[0]; a.Image != "" {
		t.Errorf("newest aired = %+v", a)
	}
	if a := d.Recent(3)[2]; a.Image != "station-id.gif" {
		t.Errorf("first aired image = %q", a.Image)
	}
}

func TestBlockSlotStampedWhenAired(t *testing.T) {
	tc := &countSeg{name: "time-check", n: 1}
	news := &countSeg{name: "news", n: 1}
	d, _, _ := setup(t, `
clock:
  ":00":
    - { segment: time-check }
    - { segment: news, count: 1 }
`, tc, news)
	d.Sleep = func(context.Context, time.Duration) error { time.Sleep(30 * time.Millisecond); return nil }
	tick := time.Date(2026, 11, 17, 14, 0, 0, 0, time.UTC)
	d.Tick(context.Background(), tick, false)
	got := d.Recent(2) // newest first
	if !got[1].At.Equal(tick) {
		t.Errorf("first slot at %v, want the tick %v", got[1].At, tick)
	}
	if !got[0].At.After(tick.Add(20 * time.Millisecond)) {
		t.Errorf("second slot at %v, want when it aired, after the gap", got[0].At)
	}
}
