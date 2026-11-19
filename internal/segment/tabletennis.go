package segment

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/moderation"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/panel"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/schedule"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/schmetterpause"
)

// TableStats is what the segment needs from schmetterpause
// (schmetterpause.Client).
type TableStats interface {
	Statistics(ctx context.Context, days int) (schmetterpause.Statistics, error)
}

// TabletennisStats airs the office's table tennis of the last days from
// schmetterpause: matches, sets and points, the longest set, who won most,
// and how close it got (deuce sets, matches won without dropping a set).
// Plain ambient cards like every segment: never a held score, so the panel
// stays free for notifications.
//
// Parameters: days (1..366, default 7), count (default 3: overview, longest
// set, most wins, close games, in this order). Nothing played: nothing airs.
type TabletennisStats struct {
	Source    TableStats // nil: not configured
	Moderator *moderation.Moderator
	Title     panel.Title
	// MessageMax caps the scrolling line in characters.
	MessageMax int
	Timeout    time.Duration
}

func (t *TabletennisStats) Name() string { return "tabletennis-stats" }

// daysLabel is the window as a title prefix and in a sentence.
func daysLabel(days int) (short, long string) {
	switch days {
	case 1:
		return "24h", "the last 24 h"
	default:
		return fmt.Sprintf("%dd", days), fmt.Sprintf("the last %d days", days)
	}
}

func (t *TabletennisStats) Build(ctx context.Context, _ time.Time, p schedule.Params) ([]Item, error) {
	if t.Source == nil {
		return nil, errors.New("tabletennis-stats: no schmetterpause configured (RADIO_SCHMETTERPAUSE_URL)")
	}
	days := p.Int("days", 7)
	if days < 1 || days > 366 {
		return nil, fmt.Errorf("tabletennis-stats: days %d, want 1..366", days)
	}
	short, long := daysLabel(days)
	timeout := t.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	s, err := t.Source.Statistics(ctx, days)
	if err != nil {
		return nil, fmt.Errorf("tabletennis-stats: %w", err)
	}
	if s.Matches == 0 {
		return nil, nil
	}
	type fact struct {
		title, message string
		keep           []string
		fixed          bool // airs as is, never reworded
	}
	matchWord := plural(s.Matches, "match", "matches")
	overview := fact{
		title: fmt.Sprintf("%s: %d %s", short, s.Matches, matchWord),
		message: fmt.Sprintf("Table tennis in %s: %d %s, %d sets, %d points",
			long, s.Matches, matchWord, s.Sets, s.Points),
	}
	// schmetterpause's match cap cut into the window (#43): the counts are
	// a floor, and everything else covers only the matches counted. The
	// overview says "at least" and airs as is -- no check can catch the
	// model moving "at least" to the wrong number -- and the other cards
	// name the matches they cover instead of the window.
	scope, prefix := long, short+": "
	if s.Truncated {
		overview.title = fmt.Sprintf("%s: %d+ %s", short, s.Matches, plural(s.Matches+1, "match", "matches"))
		overview.message = fmt.Sprintf("Table tennis in %s: at least %d %s, %d sets, %d points",
			long, s.Matches, matchWord, s.Sets, s.Points)
		overview.fixed = true
		scope, prefix = fmt.Sprintf("the last %d %s", s.Matches, matchWord), ""
	}
	facts := []fact{overview}
	if s.LongestSet != nil {
		facts = append(facts, fact{
			title:   fmt.Sprintf("Longest set %d:%d", s.LongestSet.Home, s.LongestSet.Away),
			message: fmt.Sprintf("Longest set at the table in %s: %d:%d", scope, s.LongestSet.Home, s.LongestSet.Away),
		})
	}
	if names, won := leaders(s.MostWins); len(names) > 0 {
		who := strings.Join(names, " and ")
		f := fact{title: "Most wins: " + who, keep: names}
		if len(names) == 1 {
			lost := s.MostWins[0].Lost
			f.message = fmt.Sprintf("Most wins in %s: %s, %d won and %d lost", scope, who, won, lost)
		} else {
			f.message = fmt.Sprintf("Most wins in %s: %s, %d each", scope, who, won)
		}
		facts = append(facts, f)
	}
	if s.Deuce > 0 || s.Whitewashes > 0 {
		facts = append(facts, fact{
			title: fmt.Sprintf("%s%d deuce sets", prefix, s.Deuce),
			message: fmt.Sprintf("In %s %d sets went to deuce and %d %s ended without a set lost",
				scope, s.Deuce, s.Whitewashes, plural(s.Whitewashes, "match", "matches")),
		})
	}

	count := min(p.Int("count", 3), len(facts))
	items := make([]Item, 0, count)
	for _, f := range facts[:count] {
		msg := f.message
		if !f.fixed {
			msg = t.Moderator.Phrase(ctx, msg, t.MessageMax, f.keep...)
		}
		items = append(items, Item{
			Title:   t.Title.Fit(f.title),
			Message: panel.Truncate(msg, t.MessageMax),
		})
	}
	return items, nil
}

// leaders are the players on the top win count (schmetterpause lists the
// most wins first). A tie of more than two names nobody.
func leaders(scores []schmetterpause.PlayerScore) ([]string, int) {
	if len(scores) == 0 || scores[0].Won == 0 {
		return nil, 0
	}
	top := scores[0].Won
	var names []string
	for _, s := range scores {
		if s.Won == top {
			names = append(names, s.DisplayName)
		}
	}
	if len(names) > 2 {
		return nil, 0
	}
	return names, top
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
