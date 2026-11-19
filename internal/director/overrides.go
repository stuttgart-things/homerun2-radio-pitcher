package director

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/schedule"
)

// Overrides are session controls laid on top of the schedule from git: they
// live in memory, expire on their own and survive a schedule reload (a
// restart clears them). The schedule file itself is changed only by PR.
type Overrides struct {
	// PausedUntil: no slot airs before it (zero: not paused).
	PausedUntil time.Time
	// Skip is the minute ("2006-01-02T15:04") whose slots are skipped once.
	Skip time.Time
	// Clock replaces the clock of the show until ClockUntil.
	Clock      string
	ClockUntil time.Time
}

// MaxOverride caps how long a pause or a clock switch may last.
const MaxOverride = 24 * time.Hour

// active returns the overrides still in force at t.
func (o Overrides) active(t time.Time) Overrides {
	if !o.PausedUntil.After(t) {
		o.PausedUntil = time.Time{}
	}
	if o.Skip.Before(t.Truncate(time.Minute)) {
		o.Skip = time.Time{}
	}
	if !o.ClockUntil.After(t) {
		o.Clock, o.ClockUntil = "", time.Time{}
	}
	return o
}

// Overrides returns the overrides in force now.
func (d *Director) Overrides(now time.Time) Overrides {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.overrides = d.overrides.active(now)
	return d.overrides
}

// Pause stops airing until until (at most MaxOverride ahead). Air now still
// works: it is an explicit action.
func (d *Director) Pause(now, until time.Time, who string) error {
	if !until.After(now) || until.Sub(now) > MaxOverride {
		return fmt.Errorf("pause until %s: must be within the next %s", until.Format("15:04"), MaxOverride)
	}
	d.mu.Lock()
	d.overrides.PausedUntil = until
	d.mu.Unlock()
	d.logger().Info("override: paused", "until", until.Format(time.RFC3339), "by", who)
	return nil
}

// Resume ends a pause.
func (d *Director) Resume(who string) {
	d.mu.Lock()
	d.overrides.PausedUntil = time.Time{}
	d.mu.Unlock()
	d.logger().Info("override: resumed", "by", who)
}

// SkipNext skips the next slot that would air after now, once. It returns
// the time of the skipped slot.
func (d *Director) SkipNext(now time.Time, who string) (time.Time, error) {
	at, _, ok := d.Next(now)
	if !ok {
		return time.Time{}, errors.New("nothing airs within a day")
	}
	d.mu.Lock()
	d.overrides.Skip = at
	d.mu.Unlock()
	d.logger().Info("override: skip", "at", at.Format(time.RFC3339), "by", who)
	return at, nil
}

// Unskip cancels a pending skip.
func (d *Director) Unskip(who string) {
	d.mu.Lock()
	d.overrides.Skip = time.Time{}
	d.mu.Unlock()
	d.logger().Info("override: skip cancelled", "by", who)
}

// SwitchClock airs the named clock ("default" or one of clocks) instead of
// the show's clock until until.
func (d *Director) SwitchClock(now time.Time, clock string, until time.Time, who string) error {
	s := d.current()
	if s == nil {
		return errors.New("no schedule loaded")
	}
	if _, ok := s.Clocks[clock]; !ok && clock != "default" {
		return fmt.Errorf("clock %q is not in the schedule", clock)
	}
	if !until.After(now) || until.Sub(now) > MaxOverride {
		return fmt.Errorf("switch until %s: must be within the next %s", until.Format("15:04"), MaxOverride)
	}
	d.mu.Lock()
	d.overrides.Clock, d.overrides.ClockUntil = clock, until
	d.mu.Unlock()
	d.logger().Info("override: clock", "clock", clock, "until", until.Format(time.RFC3339), "by", who)
	return nil
}

// RestoreClock ends a clock switch.
func (d *Director) RestoreClock(who string) {
	d.mu.Lock()
	d.overrides.Clock, d.overrides.ClockUntil = "", time.Time{}
	d.mu.Unlock()
	d.logger().Info("override: clock restored", "by", who)
}

// AirNow airs a segment at once, in the background, unless a slot is on air.
func (d *Director) AirNow(seg string, params schedule.Params, who string) error {
	if !d.Segments.Has(seg) {
		return fmt.Errorf("unknown segment %q", seg)
	}
	if !d.onAir.CompareAndSwap(false, true) {
		return errors.New("a slot is on air, try again when it is done")
	}
	ctx := d.runCtx()
	d.logger().Info("override: air now", "segment", seg, "by", who)
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer d.onAir.Store(false)
		if _, err := d.Air(ctx, seg, params, time.Now()); err != nil {
			d.logger().Warn("air now failed", "segment", seg, "error", err)
		}
	}()
	return nil
}

// due is what airs at t with the overrides applied: nothing while paused or
// at a skipped minute, the switched clock while one is set.
func (d *Director) due(s *schedule.Schedule, t time.Time, o Overrides) []schedule.Slot {
	o = o.active(t)
	if !o.PausedUntil.IsZero() || (!o.Skip.IsZero() && o.Skip.Equal(t.Truncate(time.Minute))) {
		return nil
	}
	var out []schedule.Slot
	for _, sl := range s.DueWithClock(t, o.Clock) {
		if d.Segments.Has(sl.Segment) {
			out = append(out, sl)
		}
	}
	return out
}

// Next returns the next minute after now (within a day) at which a slot
// airs, with the overrides applied, and its slots.
func (d *Director) Next(now time.Time) (time.Time, []schedule.Slot, bool) {
	s := d.current()
	if s == nil {
		return time.Time{}, nil, false
	}
	o := d.Overrides(now)
	at := now.In(d.loc()).Truncate(time.Minute)
	for range 24 * 60 {
		at = at.Add(time.Minute)
		if slots := d.due(s, at, o); len(slots) > 0 {
			return at, slots, true
		}
	}
	return time.Time{}, nil, false
}

// ClockAt names the clock on air at t (a switched one first) and its slots.
func (d *Director) ClockAt(s *schedule.Schedule, t time.Time) (string, map[int]schedule.Block) {
	o := d.Overrides(t)
	if o.Clock != "" {
		if o.Clock == "default" {
			return "default", s.Clock
		}
		if c, ok := s.Clocks[o.Clock]; ok {
			return o.Clock, c.Slots
		}
	}
	return s.ClockAt(t)
}

// current is the schedule on air, nil without one.
func (d *Director) current() *schedule.Schedule {
	if d.Schedule == nil {
		return nil
	}
	s, _, _ := d.Schedule.Get()
	return s
}

func (d *Director) runCtx() context.Context {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ctx != nil {
		return d.ctx
	}
	return context.Background()
}
