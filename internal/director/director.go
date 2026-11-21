// Package director runs the station: once a minute it looks up the slot due
// in the schedule, has the segment build its items and pitches them, one
// card at a time, into the ambient stream.
package director

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
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

// Aired is one item the director pitched (or failed to), for the UI.
type Aired struct {
	At      time.Time
	Segment string
	Title   string
	Message string
	URL     string
	Image   string
	ID      string
	Err     string
}

// DefaultArchive is how many aired items the director keeps when Archive
// is unset.
const DefaultArchive = 300

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
	// Archive is how many aired items Recent keeps (in memory; a restart
	// starts empty). 0 means DefaultArchive.
	Archive int
	Log     *slog.Logger
	Observe Observer

	// Sleep waits between items; tests replace it.
	Sleep func(ctx context.Context, d time.Duration) error

	mu        sync.Mutex
	lastSlot  string // minute already handled, "2006-01-02T15:04"
	lastShow  string // show on air at the last tick, "" outside the programme
	recent    []Aired
	overrides Overrides
	ctx       context.Context // of Run, for Air now
	onAir     atomic.Bool
	lastAired atomic.Pointer[time.Time]
	wg        sync.WaitGroup
}

// Run ticks at the start of every minute until ctx ends, then waits for a
// segment still on air.
func (d *Director) Run(ctx context.Context) error {
	d.mu.Lock()
	d.ctx = ctx
	d.mu.Unlock()
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
	d.showChange(s, now)
	o := d.Overrides(now)
	if !o.PausedUntil.IsZero() {
		if len(s.Due(now)) > 0 {
			d.logger().Info("slot skipped, station paused", "until", o.PausedUntil.Format("15:04"))
		}
		return
	}
	if !o.Skip.IsZero() && o.Skip.Equal(now.Truncate(time.Minute)) {
		d.logger().Info("slot skipped on request", "at", key)
		d.mu.Lock()
		d.overrides.Skip = time.Time{}
		d.mu.Unlock()
		return
	}
	var slots []schedule.Slot
	for _, slot := range s.DueWithClock(now, o.Clock) {
		if !d.Segments.Has(slot.Segment) {
			d.logger().Debug("slot skipped, unknown segment", "segment", slot.Segment)
			continue
		}
		slots = append(slots, slot)
	}
	if len(slots) == 0 {
		return
	}
	if !d.onAir.CompareAndSwap(false, true) {
		d.logger().Warn("previous slot still on air, slot skipped", "segment", slots[0].Segment, "at", key)
		return
	}
	run := func() {
		defer d.onAir.Store(false)
		// the slots of one minute (extras, a block): space them like items,
		// by the spacing of the last slot that aired something; a slot with
		// nothing to say leaves no gap
		var gap time.Duration
		start := time.Now()
		for i, slot := range slots {
			if gap > 0 {
				if err := d.sleep(ctx, gap); err != nil {
					return
				}
			}
			// a later slot of the block airs (and is built and stamped) when
			// it really goes on air, not at the minute's start
			at := now
			if i > 0 {
				at = now.Add(time.Since(start))
			}
			n, err := d.Air(ctx, slot.Segment, slot.Params, at)
			if err != nil {
				d.logger().Warn("slot not aired", "segment", slot.Segment, "error", err)
			}
			if n > 0 {
				gap = slot.Params.Duration("spacing", d.Spacing)
			} else {
				gap = 0
			}
		}
	}
	if async {
		d.wg.Add(1)
		go func() { defer d.wg.Done(); run() }()
		return
	}
	run()
}

// showChange logs when the programme moves to another show.
func (d *Director) showChange(s *schedule.Schedule, now time.Time) {
	name := ""
	show, ok := s.ShowAt(now)
	if ok {
		name = show.Show
	}
	d.mu.Lock()
	changed := name != d.lastShow
	d.lastShow = name
	d.mu.Unlock()
	if !changed {
		return
	}
	if ok {
		clock := show.Clock
		if clock == "" {
			clock = "default"
		}
		d.logger().Info("show on air", "show", show.Show, "window", show.Window, "clock", clock, "extras", len(show.Extra))
	} else {
		d.logger().Info("no show on air, default clock")
	}
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
	spacing := params.Duration("spacing", d.Spacing)
	tags := params.Strings("card-tags")
	// a jingle plays before the slot's first card (the catcher's radio-gif
	// rule), unless that card brings its own picture
	jingle := strings.TrimSpace(params.String("jingle", ""))
	pitched := 0
	for i, it := range items {
		if i == 0 && jingle != "" && it.Image == "" {
			it.Image = jingle
		}
		if i > 0 {
			if err := d.sleep(ctx, spacing); err != nil {
				return pitched, err
			}
		}
		at := now
		if i > 0 {
			at = time.Now().In(d.loc())
		}
		msg := d.Message(name, it, at, tags...)
		id, err := d.Pitcher.Pitch(ctx, msg)
		d.observe(name, err)
		d.record(Aired{At: at, Segment: name, Title: msg.Title, Message: msg.Message, URL: msg.URL, Image: it.Image, ID: id, Err: errText(err)})
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
// tags (key=value) follow segment=<seg>, e.g. show=news for a profile rule
// that holds those cards longer; an item's image adds gif=<image>.
func (d *Director) Message(seg string, it segment.Item, at time.Time, tags ...string) homerun.Message {
	all := append([]string{"segment=" + seg}, tags...)
	if it.Image != "" {
		all = append(all, "gif="+it.Image)
	}
	return homerun.Message{
		Title:     it.Title,
		Message:   it.Message,
		Severity:  d.Station.Severity,
		System:    d.Station.System,
		Author:    d.Station.Author,
		Timestamp: at.Format(time.RFC3339),
		Tags:      strings.Join(all, ","),
		URL:       it.URL,
	}
}

func (d *Director) record(a Aired) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.recent = append(d.recent, a)
	limit := d.Archive
	if limit <= 0 {
		limit = DefaultArchive
	}
	if len(d.recent) > limit {
		d.recent = slices.Clone(d.recent[len(d.recent)-limit:])
	}
}

// Recent returns the last aired items, newest first, at most n.
func (d *Director) Recent(n int) []Aired {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]Aired, 0, min(n, len(d.recent)))
	for i := len(d.recent) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, d.recent[i])
	}
	return out
}

// OnAir reports whether a slot is being aired right now.
func (d *Director) OnAir() bool { return d.onAir.Load() }

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
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
