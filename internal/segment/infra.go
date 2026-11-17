package segment

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"slices"
	"sync"
	"time"

	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/moderation"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/panel"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/schedule"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/sources"
)

// InfraFacts airs randomly picked facts from the infrastructure sources.
//
// Parameters: count (default 1), sources (names, default all).
type InfraFacts struct {
	Sources   []sources.Source
	Moderator *moderation.Moderator
	Title     panel.Title
	// MessageMax caps the scrolling line in characters.
	MessageMax int
	// Timeout bounds each source's query.
	Timeout time.Duration
	// Recent is how many of the last aired facts are avoided when others
	// are available.
	Recent int
	Rand   *rand.Rand
	Log    *slog.Logger

	mu     sync.Mutex
	recent []string
}

func (s *InfraFacts) Name() string { return "infra-facts" }

func (s *InfraFacts) Build(ctx context.Context, _ time.Time, p schedule.Params) ([]Item, error) {
	srcs := s.Sources
	if want := p.Strings("sources"); len(want) > 0 {
		srcs = nil
		for _, src := range s.Sources {
			if slices.Contains(want, src.Name()) {
				srcs = append(srcs, src)
			}
		}
	}
	if len(srcs) == 0 {
		return nil, errors.New("infra-facts: no sources configured (RADIO_SOURCES_PATH) or none matches")
	}

	facts := s.collect(ctx, srcs)
	if len(facts) == 0 {
		return nil, errors.New("infra-facts: no source answered")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// fresh facts first (shuffled), then recently aired ones (shuffled)
	var fresh, stale []sources.Fact
	for _, f := range facts {
		if slices.Contains(s.recent, f.ID) {
			stale = append(stale, f)
		} else {
			fresh = append(fresh, f)
		}
	}
	s.shuffle(fresh)
	s.shuffle(stale)
	ordered := append(fresh, stale...)

	count := min(p.Int("count", 1), len(ordered))
	items := make([]Item, 0, count)
	for _, f := range ordered[:count] {
		items = append(items, Item{
			Title:   s.Title.Fit(f.Title),
			Message: panel.Truncate(s.Moderator.Phrase(ctx, f.Message, s.MessageMax), s.MessageMax),
		})
		s.remember(f.ID)
	}
	return items, nil
}

// collect queries the sources concurrently; a failing source is logged and
// left out.
func (s *InfraFacts) collect(ctx context.Context, srcs []sources.Source) []sources.Fact {
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	var (
		wg  sync.WaitGroup
		mu  sync.Mutex
		all []sources.Fact
	)
	for _, src := range srcs {
		wg.Add(1)
		go func(src sources.Source) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			fs, err := src.Facts(cctx)
			if err != nil {
				s.logger().Warn("source failed, its facts are skipped", "source", src.Name(), "error", err)
				return
			}
			mu.Lock()
			all = append(all, fs...)
			mu.Unlock()
		}(src)
	}
	wg.Wait()
	// stable order before shuffling, so a seeded Rand is deterministic
	slices.SortFunc(all, func(a, b sources.Fact) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return all
}

func (s *InfraFacts) shuffle(fs []sources.Fact) {
	r := s.Rand
	if r == nil {
		r = rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 0))
		s.Rand = r
	}
	r.Shuffle(len(fs), func(i, j int) { fs[i], fs[j] = fs[j], fs[i] })
}

func (s *InfraFacts) remember(id string) {
	n := s.Recent
	if n <= 0 {
		n = 4
	}
	s.recent = append(s.recent, id)
	if len(s.recent) > n {
		s.recent = s.recent[len(s.recent)-n:]
	}
}

func (s *InfraFacts) logger() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}
