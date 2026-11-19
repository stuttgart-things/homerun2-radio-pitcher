package segment

import (
	"context"
	"time"

	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/moderation"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/schedule"
)

// TimeCheck announces the time and the date.
//
// Parameters: casual (default false): with moderation, a casual host line
// on the scrolling line that keeps the time and the weekday ("It's 15:00 on
// a Tuesday, the afternoon is all yours"); on any failure the date airs as
// usual.
type TimeCheck struct {
	Moderator *moderation.Moderator // nil: the fixed date
	// MessageMax caps the scrolling line in characters.
	MessageMax int
}

func (TimeCheck) Name() string { return "time-check" }

// Build returns one card: "It's 15:00" and "Tuesday, 17 November 2026".
func (t TimeCheck) Build(ctx context.Context, now time.Time, p schedule.Params) ([]Item, error) {
	clock := now.Format("15:04")
	date := now.Format("Monday, 2 January 2006")
	msg := date
	if p.Bool("casual") {
		template := "It's " + clock + " on " + date + "."
		if line, ok := t.Moderator.TimeLine(ctx, template, t.MessageMax, clock, now.Format("Monday")); ok {
			msg = line
		}
	}
	return []Item{{Title: "It's " + clock, Message: msg}}, nil
}
