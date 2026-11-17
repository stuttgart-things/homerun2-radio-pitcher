// Package director runs the station: once a minute it looks up the slot due
// in the schedule, has the segment build its items and pitches them, one
// card at a time, into the ambient stream.
package director

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	homerun "github.com/stuttgart-things/homerun-library/v4"

	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/pitcher"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/schedule"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/segment"
)

// ScheduleSource returns the schedule to air. changed is true when it is a
// new version.
type ScheduleSource interface {
	Get() (s *schedule.Schedule, changed bool, err error)
}

// Station is the fixed part of every message.
type Station struct {
	System   string // "radio"
	Severity string // "INFO"
	Author   string // footer of the card
}

// Observer is told about every pitched item (metrics).
type Observer func(segment string, err error)

// Director airs the schedule.
type Director struct {
	Schedule ScheduleSource
	Segments segment.Registry
	Pitcher  pitcher.Pitcher
	Station  Station
	Location *time.Location
	// Spacing is the pause between the items of one slot. The catcher shows
	// only the newest ambient card, so items pitched together would replace
	// each other at once; space them by about a card's duration.
	Spacing time.Duration
	Log     *slog.Logger
	Observe Observer

	// Sleep waits between items; tests replace it.
	Sleep func(ctx context.Context, d time.Duration) error

	mu        sync.Mutex
	lastSlot  string // minute already handled, "2006-01-02T15:04"
	onAir     atomic.Bool
	lastAired atomic.Pointer[time.Time]
	wg        sync.WaitGroup
}

// Run ticks at the start of every minute until ctx ends, then waits for a
// segment still on air.
func (d *Director) Run(ctx context.Context) error {
	d.reload()
	for {
		now := time.Now()
		next := now.Truncate(time.Minute).Add(time.Minute).Add(500 * time.Millisecond)
		t := time.NewTimer(next.Sub(now))
		select {
		case <-ctx.Done():
			t.Stop()
			d.wg.Wait()
			return nil
		case at := <-t.C:
			d.reload()
			d.Tick(ctx, at, true)
		}
	}
}

// reload picks up a changed schedule and reports unknown segments once per
// version.
func (d *Director) reload() {
	s, changed, err := d.Schedule.Get()
	if err != nil {
		d.logger().Error("schedule not loaded, keeping the last good one", "error", err)
	}
	if !changed || s == nil {
		return
	}
	d.logger().Info("schedule loaded", "slots", len(s.Clock), "minutes", s.Minutes())
	for _, u := range s.Unknown(d.Segments.Has) {
		d.logger().Warn("schedule slot has an unknown segment and is skipped", "slot", u, "known", d.Segments.Names())
	}
}

// Tick airs the slot due at the minute of now, at most once per minute. With
// async the segment runs in the background and Tick returns at once; a slot
// that comes due while the previous one is still on air is skipped.
func (d *Director) Tick(ctx context.Context, now time.Time, async bool) {
	now = now.In(d.loc())
	key := now.Format("2006-01-02T15:04")

	d.mu.Lock()
	if key == d.lastSlot {
		d.mu.Unlock()
		return
	}
	d.lastSlot = key
	d.mu.Unlock()

	s, _, _ := d.Schedule.Get()
	if s == nil {
		return
	}
	slot, ok := s.Due(now)
	if !ok {
		return
	}
	if !d.Segments.Has(slot.Segment) {
		d.logger().Debug("slot skipped, unknown segment", "segment", slot.Segment)
		return
	}
	if !d.onAir.CompareAndSwap(false, true) {
		d.logger().Warn("previous slot still on air, slot skipped", "segment", slot.Segment, "at", key)
		return
	}
	run := func() {
		defer d.onAir.Store(false)
		if _, err := d.Air(ctx, slot.Segment, slot.Params, now); err != nil {
			d.logger().Warn("slot not aired", "segment", slot.Segment, "error", err)
		}
	}
	if async {
		d.wg.Add(1)
		go func() { defer d.wg.Done(); run() }()
		return
	}
	run()
}

// Air builds a segment now and pitches its items. It returns how many items
// were pitched.
func (d *Director) Air(ctx context.Context, name string, params schedule.Params, now time.Time) (int, error) {
	seg, ok := d.Segments[name]
	if !ok {
		return 0, fmt.Errorf("unknown segment %q, known: %v", name, d.Segments.Names())
	}
	now = now.In(d.loc())
	items, err := seg.Build(ctx, now, params)
	if err != nil {
		d.observe(name, err)
		return 0, err
	}
	if len(items) == 0 {
		d.logger().Info("segment had nothing to say", "segment", name)
		return 0, nil
	}
	pitched := 0
	for i, it := range items {
		if i > 0 {
			if err := d.sleep(ctx, d.Spacing); err != nil {
				return pitched, err
			}
		}
		at := now
		if i > 0 {
			at = time.Now().In(d.loc())
		}
		msg := d.Message(name, it, at)
		id, err := d.Pitcher.Pitch(ctx, msg)
		d.observe(name, err)
		if err != nil {
			d.logger().Error("pitch failed", "segment", name, "title", msg.Title, "error", err)
			continue
		}
		pitched++
		t := time.Now()
		d.lastAired.Store(&t)
		d.logger().Info("on air", "segment", name, "title", msg.Title, "message", msg.Message, "id", id)
	}
	if pitched == 0 {
		return 0, fmt.Errorf("no item of %s could be pitched", name)
	}
	return pitched, nil
}

// Message turns an item into the homerun2 message the catcher renders.
func (d *Director) Message(seg string, it segment.Item, at time.Time) homerun.Message {
	return homerun.Message{
		Title:     it.Title,
		Message:   it.Message,
		Severity:  d.Station.Severity,
		System:    d.Station.System,
		Author:    d.Station.Author,
		Timestamp: at.Format(time.RFC3339),
		Tags:      "segment=" + seg,
	}
}

// LastAired is when the last item went out, zero if none yet.
func (d *Director) LastAired() time.Time {
	if t := d.lastAired.Load(); t != nil {
		return *t
	}
	return time.Time{}
}

func (d *Director) loc() *time.Location {
	if d.Location != nil {
		return d.Location
	}
	return time.Local
}

func (d *Director) sleep(ctx context.Context, dur time.Duration) error {
	if d.Sleep != nil {
		return d.Sleep(ctx, dur)
	}
	if dur <= 0 {
		return nil
	}
	t := time.NewTimer(dur)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (d *Director) observe(seg string, err error) {
	if d.Observe != nil {
		d.Observe(seg, err)
	}
}

func (d *Director) logger() *slog.Logger {
	if d.Log != nil {
		return d.Log
	}
	return slog.Default()
}
