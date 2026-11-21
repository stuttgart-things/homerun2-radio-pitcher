package segment

import (
	"context"
	"math/rand/v2"
	"slices"
	"sync"
	"time"

	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/panel"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/schedule"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/spots"
)

// Spots airs short cards from the pool (spots.yaml, RADIO_SPOTS_PATH), like
// a radio's commercial breaks: a weighted random pick among the spots
// active now (weekdays, between, from/until), avoiding the last Recent
// aired. Fixed texts from the file, never reworded or cut.
//
// Parameters: count (default 1), kinds (text, quote, gif; default all), ids
// (only these spots). A gif spot's card carries its image (Item.Image).
type Spots struct {
	Pool  []spots.Spot
	Title panel.Title
	// Recent is how many of the last aired spots are not repeated (default
	// 3); with fewer active spots than that, the oldest of them comes back.
	Recent int
	Rand   *rand.Rand

	mu     sync.Mutex
	recent []string // spot IDs, oldest first
}

func (s *Spots) Name() string { return "spots" }

func (s *Spots) Build(ctx context.Context, now time.Time, p schedule.Params) ([]Item, error) {
	kinds := p.Strings("kinds")
	ids := p.Strings("ids")
	var active []spots.Spot
	for _, sp := range s.Pool {
		if !sp.Active(now) {
			continue
		}
		if len(kinds) > 0 && !slices.Contains(kinds, sp.Kind) {
			continue
		}
		if len(ids) > 0 && !slices.Contains(ids, sp.ID) {
			continue
		}
		active = append(active, sp)
	}
	if len(active) == 0 {
		return nil, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Rand == nil {
		s.Rand = rand.New(rand.NewPCG(uint64(now.UnixNano()), 0))
	}
	keep := s.Recent
	if keep <= 0 {
		keep = 3
	}
	recent := slices.Clone(s.recent)

	count := min(p.Int("count", 1), len(active))
	items := make([]Item, 0, count)
	for range count {
		// leave out the recent ones, but never all of the active spots
		avoid := min(keep, len(active)-1)
		last := recent[max(0, len(recent)-avoid):]
		var pick []spots.Spot
		total := 0
		for _, sp := range active {
			if slices.Contains(last, sp.ID) {
				continue
			}
			pick = append(pick, sp)
			total += sp.Weighting()
		}
		n := s.Rand.IntN(total)
		var chosen spots.Spot
		for _, sp := range pick {
			if n < sp.Weighting() {
				chosen = sp
				break
			}
			n -= sp.Weighting()
		}
		recent = append(recent, chosen.ID)
		title, msg := chosen.Card()
		items = append(items, Item{Title: s.Title.Fit(title), Message: msg, Image: chosen.Image})
	}
	if !IsPreview(ctx) {
		if len(recent) > 50 {
			recent = recent[len(recent)-50:]
		}
		s.recent = recent
	}
	return items, nil
}
