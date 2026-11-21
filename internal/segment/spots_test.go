package segment

import (
	"context"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/panel"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/schedule"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/spots"
)

func spotPool(t *testing.T) []spots.Spot {
	t.Helper()
	ps, err := spots.Parse([]byte(`
spots:
  - { id: a, title: "Spot A", message: "a message" }
  - { id: b, title: "Spot B" }
  - { id: c, title: "Spot C" }
  - { id: q, kind: quote, text: "Talk is cheap.", author: Linus Torvalds }
  - { id: x, title: "Weekend only", weekdays: [sat, sun] }
`), 120)
	if err != nil {
		t.Fatal(err)
	}
	return ps
}

func TestSpotsAvoidRecent(t *testing.T) {
	s := &Spots{Pool: spotPool(t), Title: panel.Card4x6, Recent: 3, Rand: rand.New(rand.NewPCG(1, 2))}
	thu := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	var aired []string
	for range 12 {
		items, err := s.Build(context.Background(), thu, nil)
		if err != nil || len(items) != 1 {
			t.Fatalf("items %v err %v", items, err)
		}
		aired = append(aired, items[0].Title)
		if items[0].Title == "Weekend only" {
			t.Fatal("weekend spot on a Thursday")
		}
	}
	// 4 active spots, the last 3 avoided: every window of 4 has them all
	for i := 0; i+4 <= len(aired); i++ {
		seen := map[string]bool{}
		for _, a := range aired[i : i+4] {
			seen[a] = true
		}
		if len(seen) != 4 {
			t.Fatalf("repeat within 4: %v", aired)
		}
	}
}

func TestSpotsParamsAndPreview(t *testing.T) {
	s := &Spots{Pool: spotPool(t), Title: panel.Card4x6}
	at := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)

	items, _ := s.Build(context.Background(), at, schedule.Params{"kinds": "quote"})
	if len(items) != 1 || items[0].Message != "\u201cTalk is cheap.\u201d Linus Torvalds" || !panel.Card4x6.Fits(items[0].Title) {
		t.Fatalf("quote = %+v", items)
	}
	items, _ = s.Build(context.Background(), at, schedule.Params{"ids": []any{"a", "b"}, "count": 5})
	if len(items) != 2 {
		t.Fatalf("ids, count above the pool: %+v", items)
	}
	// a preview does not count as aired
	s2 := &Spots{Pool: spotPool(t), Title: panel.Card4x6}
	_, _ = s2.Build(Preview(context.Background()), at, nil)
	if len(s2.recent) != 0 {
		t.Fatalf("preview recorded %v", s2.recent)
	}
	// nothing active or no pool: silent
	if items, err := s2.Build(context.Background(), at, schedule.Params{"ids": "x"}); err != nil || items != nil {
		t.Fatalf("inactive: %v %v", items, err)
	}
	if items, err := (&Spots{}).Build(context.Background(), at, nil); err != nil || items != nil {
		t.Fatalf("no pool: %v %v", items, err)
	}
}

func TestSpotsGIFCarriesImage(t *testing.T) {
	ps, err := spots.Parse([]byte("spots:\n  - { id: g, kind: gif, image: sunset.gif, title: \"Evening\" }\n  - { id: a, title: \"Spot A\" }\n"), 120)
	if err != nil {
		t.Fatal(err)
	}
	s := &Spots{Pool: ps, Title: panel.Card4x6, Rand: rand.New(rand.NewPCG(1, 2))}
	items, err := s.Build(context.Background(), time.Now(), schedule.Params{"kinds": "gif"})
	if err != nil || len(items) != 1 || items[0].Image != "sunset.gif" || items[0].Title != "Evening" {
		t.Fatalf("gif spot = %+v, %v", items, err)
	}
	items, _ = s.Build(context.Background(), time.Now(), schedule.Params{"ids": "a"})
	if len(items) != 1 || items[0].Image != "" {
		t.Fatalf("text spot = %+v", items)
	}
}
