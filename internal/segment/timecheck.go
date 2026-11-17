package segment

import (
	"context"
	"time"

	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/schedule"
)

// TimeCheck announces the time and the date.
type TimeCheck struct{}

func (TimeCheck) Name() string { return "time-check" }

// Build returns one card: "It's 15:00" and "Tuesday, 17 November 2026".
func (TimeCheck) Build(_ context.Context, now time.Time, _ schedule.Params) ([]Item, error) {
	return []Item{{
		Title:   "It's " + now.Format("15:04"),
		Message: now.Format("Monday, 2 January 2006"),
	}}, nil
}
