// Package schedule reads the radio schedule (schedule.yaml): format clocks
// that map a minute of the hour to a segment, and a day programme of time
// windows (shows). A show airs a named clock instead of the default one, and
// its extras once per window.
package schedule

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Schedule is the parsed schedule.yaml.
type Schedule struct {
	// Clock maps a minute of the hour (0-59) to the slots aired then, one
	// after the other (a block, e.g. a news show; mostly one slot). It
	// repeats every hour. It is the default clock: it airs outside of every
	// show and in shows without their own clock.
	Clock map[int]Block
	// Default is the `default` entry of the clock: what runs between slots.
	// The idle screen belongs to the catcher, so the pitcher does not air
	// it; it is kept so the file can say what the panel shows.
	Default *Slot
	// Clocks are the named clocks shows refer to. An empty one is silence.
	Clocks map[string]Clock
	// Programme is the list of time windows of the day, in file order.
	Programme []Show
}

// Clock is a named format clock.
type Clock struct {
	Slots   map[int]Block
	Default *Slot
}

// Block is what a clock airs at one minute: one slot, or a list of slots
// aired one after the other.
type Block []Slot

// UnmarshalYAML reads a slot mapping or a list of them.
func (b *Block) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.SequenceNode {
		var s Slot
		if err := s.UnmarshalYAML(n); err != nil {
			return err
		}
		*b = Block{s}
		return nil
	}
	if len(n.Content) == 0 {
		return fmt.Errorf("line %d: an empty list of slots", n.Line)
	}
	out := make(Block, len(n.Content))
	for i, c := range n.Content {
		if err := out[i].UnmarshalYAML(c); err != nil {
			return err
		}
	}
	*b = out
	return nil
}

// Segments names the segments of the block, comma-separated.
func (b Block) Segments() string {
	names := make([]string, len(b))
	for i, s := range b {
		names[i] = s.Segment
	}
	return strings.Join(names, ", ")
}

// Slot is one entry of the clock: a segment and its parameters.
type Slot struct {
	Segment string
	Params  Params
}

// Show is one time window of the programme, e.g. "07:00-10:00".
type Show struct {
	Window string
	From   TimeOfDay
	To     TimeOfDay
	Show   string `yaml:"show"`
	// Clock names the clock of Clocks the show airs; empty: the default clock.
	Clock string `yaml:"clock"`
	// Extra are segments aired once per window, at their own time.
	Extra []Extra `yaml:"extra"`
	// Brightness of the panel during the show in percent, nil when unset.
	// Parsed, not aired: dimming is the catcher's (led-catcher issue).
	Brightness *int `yaml:"brightness"`
}

// Extra is a segment a show airs once per window: `weather`, or
// `{ segment: daily-summary, at: "17:30", count: 1 }`. Without `at` it airs
// at the start of the window.
type Extra struct {
	Slot
	At    TimeOfDay
	HasAt bool
}

// UnmarshalYAML reads a segment name or a slot mapping with optional `at`.
func (e *Extra) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		e.Segment = strings.TrimSpace(n.Value)
		return nil
	}
	if err := e.Slot.UnmarshalYAML(n); err != nil {
		return err
	}
	if v, ok := e.Params["at"]; ok {
		delete(e.Params, "at")
		at, err := parseTimeOfDay(fmt.Sprint(v))
		if err != nil {
			return fmt.Errorf("extra %s: at %q, want \"HH:MM\"", e.Segment, fmt.Sprint(v))
		}
		e.At, e.HasAt = at, true
	}
	return nil
}

// TimeOfDay is a time of day in minutes after midnight.
type TimeOfDay int

func (c TimeOfDay) String() string { return fmt.Sprintf("%02d:%02d", int(c)/60, int(c)%60) }

// Contains reports whether the time of day of t lies in the window. A window
// whose end is before its start crosses midnight ("19:00-07:00").
func (s Show) Contains(t time.Time) bool {
	return s.contains(TimeOfDay(t.Hour()*60 + t.Minute()))
}

func (s Show) contains(m TimeOfDay) bool {
	if s.From <= s.To {
		return m >= s.From && m < s.To
	}
	return m >= s.From || m < s.To
}

// file is the YAML layout.
type file struct {
	Clock     map[string]Block            `yaml:"clock"`
	Clocks    map[string]map[string]Block `yaml:"clocks"`
	Programme yaml.Node                  `yaml:"programme"`
}

// UnmarshalYAML reads `{ segment: news, count: 3 }`: `segment` names the
// segment, every other key is a parameter of it.
func (s *Slot) UnmarshalYAML(n *yaml.Node) error {
	var raw map[string]any
	if err := n.Decode(&raw); err != nil {
		return fmt.Errorf("line %d: a slot is a mapping like { segment: news }: %w", n.Line, err)
	}
	seg, _ := raw["segment"].(string)
	delete(raw, "segment")
	s.Segment = strings.TrimSpace(seg)
	s.Params = Params(raw)
	return nil
}

// Load reads and validates the schedule at path.
func Load(path string) (*Schedule, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return Parse(f)
}

// Parse reads and validates a schedule. It reports every problem it finds,
// not only the first.
func Parse(r io.Reader) (*Schedule, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var f file
	if err := dec.Decode(&f); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("schedule is empty")
		}
		return nil, fmt.Errorf("schedule: %w", err)
	}

	p := &Schedule{Clocks: map[string]Clock{}}
	var errs []error

	if len(f.Clock) == 0 {
		errs = append(errs, errors.New("clock: no slots"))
	}
	def, cerrs := parseClock("clock", f.Clock)
	errs = append(errs, cerrs...)
	p.Clock, p.Default = def.Slots, def.Default
	for name, slots := range f.Clocks {
		c, cerrs := parseClock(fmt.Sprintf("clocks %q", name), slots)
		errs = append(errs, cerrs...)
		p.Clocks[name] = c
	}

	shows, err := parseProgramme(&f.Programme)
	if err != nil {
		errs = append(errs, err)
	}
	p.Programme = shows
	errs = append(errs, p.checkProgramme()...)

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return p, nil
}

// parseClock reads the slots of one clock; where names it in errors.
func parseClock(where string, blocks map[string]Block) (Clock, []error) {
	c := Clock{Slots: map[int]Block{}}
	var errs []error
	for key, block := range blocks {
		bad := false
		for _, slot := range block {
			if slot.Segment == "" {
				errs = append(errs, fmt.Errorf("%s %q: no segment", where, key))
				bad = true
			} else if err := slot.Params.validate(); err != nil {
				errs = append(errs, fmt.Errorf("%s %q: %s: %w", where, key, slot.Segment, err))
				bad = true
			}
		}
		if bad {
			continue
		}
		if key == "default" {
			if len(block) != 1 {
				errs = append(errs, fmt.Errorf("%s \"default\": one slot, not a list", where))
				continue
			}
			s := block[0]
			c.Default = &s
			continue
		}
		minute, err := parseMinute(key)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s %q: %w", where, key, err))
			continue
		}
		if _, dup := c.Slots[minute]; dup {
			errs = append(errs, fmt.Errorf("%s %q: minute %d given twice", where, key, minute))
			continue
		}
		c.Slots[minute] = block
	}
	return c, errs
}

// checkProgramme checks what needs the whole schedule: clock references,
// extras inside their window, and windows that do not overlap.
func (p *Schedule) checkProgramme() []error {
	var errs []error
	var owner [24 * 60]string
	for _, s := range p.Programme {
		if s.Clock != "" {
			if _, ok := p.Clocks[s.Clock]; !ok {
				errs = append(errs, fmt.Errorf("programme %q: clock %q is not in clocks", s.Window, s.Clock))
			}
		}
		for _, e := range s.Extra {
			if e.Segment == "" {
				errs = append(errs, fmt.Errorf("programme %q: extra without segment", s.Window))
			} else if e.HasAt && !s.contains(e.At) {
				errs = append(errs, fmt.Errorf("programme %q: extra %s at %s is outside the window", s.Window, e.Segment, e.At))
			} else if err := e.Params.validate(); err != nil {
				errs = append(errs, fmt.Errorf("programme %q: extra %s: %w", s.Window, e.Segment, err))
			}
		}
		for m := TimeOfDay(0); m < 24*60; m++ {
			if !s.contains(m) {
				continue
			}
			if o := owner[m]; o != "" {
				errs = append(errs, fmt.Errorf("programme %q overlaps %q at %s", s.Window, o, m))
				break
			}
			owner[m] = s.Window
		}
	}
	return errs
}

// parseMinute accepts ":05", "05" and "5".
func parseMinute(key string) (int, error) {
	k := strings.TrimPrefix(strings.TrimSpace(key), ":")
	m, err := strconv.Atoi(k)
	if err != nil || m < 0 || m > 59 {
		return 0, fmt.Errorf("key must be a minute of the hour, \":00\" to \":59\"")
	}
	return m, nil
}

func parseProgramme(n *yaml.Node) ([]Show, error) {
	if n.Kind == 0 {
		return nil, nil
	}
	if n.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("programme (line %d): a mapping of \"HH:MM-HH:MM\" windows", n.Line)
	}
	var shows []Show
	var errs []error
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, val := n.Content[i].Value, n.Content[i+1]
		var s Show
		if err := val.Decode(&s); err != nil {
			errs = append(errs, fmt.Errorf("programme %q: %w", key, err))
			continue
		}
		from, to, err := parseWindow(key)
		if err != nil {
			errs = append(errs, fmt.Errorf("programme %q: %w", key, err))
			continue
		}
		if s.Show == "" {
			errs = append(errs, fmt.Errorf("programme %q: no show", key))
			continue
		}
		if s.Brightness != nil && (*s.Brightness < 0 || *s.Brightness > 100) {
			errs = append(errs, fmt.Errorf("programme %q: brightness is a percentage, 0 to 100", key))
			continue
		}
		s.Window, s.From, s.To = key, from, to
		shows = append(shows, s)
	}
	return shows, errors.Join(errs...)
}

func parseWindow(w string) (TimeOfDay, TimeOfDay, error) {
	a, b, ok := strings.Cut(w, "-")
	if !ok {
		return 0, 0, errors.New("window must be \"HH:MM-HH:MM\"")
	}
	from, err1 := parseTimeOfDay(a)
	to, err2 := parseTimeOfDay(b)
	if err1 != nil || err2 != nil {
		return 0, 0, errors.New("window must be \"HH:MM-HH:MM\"")
	}
	if from == to {
		return 0, 0, errors.New("window is empty")
	}
	return from, to, nil
}

func parseTimeOfDay(s string) (TimeOfDay, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil {
		return 0, err
	}
	return TimeOfDay(t.Hour()*60 + t.Minute()), nil
}

// Due returns the slots aired at the minute of t, in order: the extras of
// the show on air that are due now, then the slot of its clock. t must
// already be in the station's time zone.
func (p *Schedule) Due(t time.Time) []Slot { return p.DueWithClock(t, "") }

// DueWithClock is Due with the clock of the show replaced by the named clock
// (a session override). An empty or unknown name changes nothing.
func (p *Schedule) DueWithClock(t time.Time, override string) []Slot {
	var due []Slot
	clock := p.Clock
	if show, ok := p.ShowAt(t); ok {
		m := TimeOfDay(t.Hour()*60 + t.Minute())
		for _, e := range show.Extra {
			at := show.From
			if e.HasAt {
				at = e.At
			}
			if at == m {
				due = append(due, e.Slot)
			}
		}
		if show.Clock != "" {
			clock = p.Clocks[show.Clock].Slots
		}
	}
	if c, ok := p.Clocks[override]; ok {
		clock = c.Slots
	} else if override == "default" {
		clock = p.Clock
	}
	due = append(due, clock[t.Minute()]...)
	return due
}

// Next returns the first minute after t (at most a day ahead) at which a
// slot is due whose segment airs (known), and those slots. ok is false when
// nothing airs within a day.
func (p *Schedule) Next(t time.Time, known func(string) bool) (at time.Time, slots []Slot, ok bool) {
	at = t.Truncate(time.Minute)
	for range 24 * 60 {
		at = at.Add(time.Minute)
		var due []Slot
		for _, s := range p.Due(at) {
			if known == nil || known(s.Segment) {
				due = append(due, s)
			}
		}
		if len(due) > 0 {
			return at, due, true
		}
	}
	return time.Time{}, nil, false
}

// ClockAt returns the slots of the clock on air at t: the show's named clock
// or the default one, and the name ("default" for the default clock).
func (p *Schedule) ClockAt(t time.Time) (string, map[int]Block) {
	if show, ok := p.ShowAt(t); ok && show.Clock != "" {
		return show.Clock, p.Clocks[show.Clock].Slots
	}
	return "default", p.Clock
}

// ShowAt returns the programme window t falls in, if any.
func (p *Schedule) ShowAt(t time.Time) (Show, bool) {
	for _, s := range p.Programme {
		if s.Contains(t) {
			return s, true
		}
	}
	return Show{}, false
}

// Minutes returns the minutes of the default clock in order.
func (p *Schedule) Minutes() []int { return minutes(p.Clock) }

func minutes(slots map[int]Block) []int {
	ms := make([]int, 0, len(slots))
	for m := range slots {
		ms = append(ms, m)
	}
	sort.Ints(ms)
	return ms
}

// Unknown returns a line for every slot (clocks and extras) whose segment is
// not in known. The director skips those slots; the caller decides whether
// that is fatal.
func (p *Schedule) Unknown(known func(string) bool) []string {
	var out []string
	for _, m := range p.Minutes() {
		for _, s := range p.Clock[m] {
			if !known(s.Segment) {
				out = append(out, fmt.Sprintf(":%02d %s", m, s.Segment))
			}
		}
	}
	names := make([]string, 0, len(p.Clocks))
	for n := range p.Clocks {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		c := p.Clocks[n]
		for _, m := range minutes(c.Slots) {
			for _, s := range c.Slots[m] {
				if !known(s.Segment) {
					out = append(out, fmt.Sprintf("%s :%02d %s", n, m, s.Segment))
				}
			}
		}
	}
	for _, s := range p.Programme {
		for _, e := range s.Extra {
			if !known(e.Segment) {
				out = append(out, fmt.Sprintf("%s extra %s", s.Window, e.Segment))
			}
		}
	}
	return out
}
