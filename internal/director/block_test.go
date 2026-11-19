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
