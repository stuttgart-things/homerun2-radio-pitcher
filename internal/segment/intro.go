package segment

import (
	"context"
	"strings"
	"time"

	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/moderation"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/panel"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/schedule"
)

// announcement is how the host introduces a segment: its name on air and
// what it brings.
type announcement struct {
	label string // "News"
	line  string // "the latest headlines"
}

var announcements = map[string]announcement{
	"news":              {"News", "the latest headlines"},
	"infra-facts":       {"Infra facts", "a look at our clusters"},
	"daily-summary":     {"Daily summary", "the day in numbers"},
	"tabletennis-stats": {"Table tennis", "what happened at the table"},
	"did-you-know":      {"Did you know?", "something from our software catalog"},
	"time-check":        {"Time check", "the time"},
}

// Host announces the next segment: a card "Next up: News" with "The latest
// headlines" on the scrolling line, or with moderation a casual host line
// that keeps the segment's name and has no numbers. On any failure of the
// model the fixed line airs.
type Host struct {
	Moderator *moderation.Moderator // nil: fixed lines only
	Title     panel.Title
	// MessageMax caps the scrolling line in characters.
	MessageMax int
}

// Intro returns the announcement card of segment seg.
func (h *Host) Intro(ctx context.Context, seg string) Item {
	a, ok := announcements[seg]
	if !ok {
		a = announcement{label: strings.ReplaceAll(seg, "-", " ")}
	}
	msg, ok := h.Moderator.Intro(ctx, strings.TrimRight(a.label, "?!. "), a.line, h.MessageMax)
	if !ok {
		msg = capitalize(a.line)
		if msg == "" {
			msg = "Coming up next"
		}
	}
	return Item{
		Title:   h.Title.Fit("Next up: " + a.label),
		Message: panel.Truncate(msg, h.MessageMax),
	}
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// WithIntros wraps every segment so that a slot with `intro: true` starts
// with the host's announcement. The announcement airs only when the segment
// has something to say: never "Next up: news" followed by silence.
func (r Registry) WithIntros(h *Host) Registry {
	out := Registry{}
	for name, s := range r {
		out[name] = introduced{Segment: s, host: h}
	}
	return out
}

type introduced struct {
	Segment
	host *Host
}

func (s introduced) Build(ctx context.Context, now time.Time, p schedule.Params) ([]Item, error) {
	items, err := s.Segment.Build(ctx, now, p)
	if err != nil || len(items) == 0 || !p.Bool("intro") {
		return items, err
	}
	return append([]Item{s.host.Intro(ctx, s.Name())}, items...), nil
}
