package segment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"time"

	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/moderation"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/panel"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/schedule"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/scout"
)

// Analytics is what the summary needs from homerun2-scout (scout.Client).
type Analytics interface {
	Digest(ctx context.Context, window string) (scout.Report, error)
	Tagged(ctx context.Context, window, tag string) (int64, error)
}

// DailySummary airs the balance of a window from homerun2-scout: how many
// messages, errors and critical ones, how many carried a tag (e.g.
// release), and the busiest system. Scout's windows are rolling ("the last
// 24 h"), and the cards say so.
//
// Parameters: window (hourly, daily, weekly; default daily), count (default
// 3: overview, tags, top system, in this order), tags (names). The top
// system is scout's first: "Most alerts" when it raised any, else
// "Busiest".
type DailySummary struct {
	Scout     Analytics // nil: not configured
	Moderator *moderation.Moderator
	Title     panel.Title
	// MessageMax caps the scrolling line in characters.
	MessageMax int
	Timeout    time.Duration
	// Own is the station's own system (RADIO_SYSTEM): never named the
	// busiest. Its cards are in scout's counts unless scout excludes it
	// (DIGEST_EXCLUDE_SYSTEMS).
	Own string
	Log *slog.Logger
}

func (d *DailySummary) Name() string { return "daily-summary" }

var windowLabel = map[string][2]string{
	scout.Hourly: {"1h", "the last hour"},
	scout.Daily:  {"24h", "the last 24 h"},
	scout.Weekly: {"7d", "the last 7 days"},
}

func (d *DailySummary) Build(ctx context.Context, _ time.Time, p schedule.Params) ([]Item, error) {
	if d.Scout == nil {
		return nil, errors.New("daily-summary: no scout configured (RADIO_SCOUT_URL)")
	}
	window := p.String("window", scout.Daily)
	if !scout.ValidWindow(window) {
		return nil, fmt.Errorf("daily-summary: window %q, want hourly, daily or weekly", window)
	}
	short, long := windowLabel[window][0], windowLabel[window][1]
	timeout := d.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	r, err := d.Scout.Digest(ctx, window)
	if err != nil {
		return nil, fmt.Errorf("daily-summary: %w", err)
	}
	cur := r.Current
	atLeast := ""
	if r.WindowIncomplete {
		atLeast = "at least " // retention already dropped part of the window
	}

	var facts []summaryFact
	msg := fmt.Sprintf("In %s: %s%d messages, %d errors, %d critical",
		long, atLeast, cur.Total, cur.Severities["error"], cur.Severities["critical"])
	if r.Previous != nil && !r.WindowIncomplete {
		msg += fmt.Sprintf(" (before: %d)", r.Previous.Total)
	}
	facts = append(facts, summaryFact{title: fmt.Sprintf("%s: %d msgs, %d err", short, cur.Total, cur.Severities["error"]), message: msg})

	for _, tag := range p.Strings("tags") {
		n, err := d.Scout.Tagged(ctx, window, tag)
		if err != nil {
			d.logger().Warn("daily-summary: tag count failed, skipped", "tag", tag, "error", err)
			continue
		}
		facts = append(facts, summaryFact{
			title:   fmt.Sprintf("%s: %d %s", short, n, tag),
			message: fmt.Sprintf("In %s: %d messages tagged %s", long, n, tag),
		})
	}

	// Scout ranks its top systems by alerts first, then by messages, and
	// returns only a few: the first one is the busiest only when no system
	// raised an alert. A system without alerts may be missing from the
	// list, so the radio cannot rank by messages itself.
	if top, ok := busiest(cur.TopSystems, d.Own); ok {
		m := fmt.Sprintf("In %s: %s sent %d messages", long, top.System, top.Count)
		title := "Busiest: " + top.System
		if top.Alerts > 0 {
			m = fmt.Sprintf("In %s: %s raised the most alerts, %d of its %d messages", long, top.System, top.Alerts, top.Count)
			title = "Most alerts: " + top.System
		}
		facts = append(facts, summaryFact{title: title, message: m})
	}

	count := min(p.Int("count", 3), len(facts))
	items := make([]Item, 0, count)
	for _, f := range facts[:count] {
		items = append(items, Item{
			Title:   d.Title.Fit(f.title),
			Message: panel.Truncate(d.Moderator.Phrase(ctx, f.message, d.MessageMax), d.MessageMax),
		})
	}
	return items, nil
}

type summaryFact struct{ title, message string }

// busiest is scout's first system that is not the station itself.
func busiest(systems []scout.SystemCount, own string) (scout.SystemCount, bool) {
	for _, s := range systems {
		if s.System != own {
			return s, true
		}
	}
	return scout.SystemCount{}, false
}

func (d *DailySummary) logger() *slog.Logger {
	if d.Log != nil {
		return d.Log
	}
	return slog.Default()
}
