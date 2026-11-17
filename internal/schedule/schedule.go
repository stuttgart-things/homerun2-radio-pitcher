// Package schedule reads the radio schedule (schedule.yaml): a format clock
// that maps a minute of the hour to a segment, and a day programme of time
// windows. The MVP airs the clock only; the programme is parsed and
// validated so its format is fixed before it is used.
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
	// Clock maps a minute of the hour (0-59) to the slot aired then. It
	// repeats every hour.
	Clock map[int]Slot
	// Default is the `default` entry of the clock: what runs between slots.
	// The idle screen belongs to the catcher, so the pitcher does not air
	// it; it is kept so the file can say what the panel shows.
	Default *Slot
	// Programme is the list of time windows of the day, in file order.
	Programme []Show
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
	Show   string   `yaml:"show"`
	Extra  []string `yaml:"extra"`
	// Brightness of the panel during the show in percent, nil when unset.
	Brightness *int `yaml:"brightness"`
}

// TimeOfDay is a time of day in minutes after midnight.
type TimeOfDay int

func (c TimeOfDay) String() string { return fmt.Sprintf("%02d:%02d", int(c)/60, int(c)%60) }

// Contains reports whether the time of day of t lies in the window. A window
// whose end is before its start crosses midnight ("19:00-07:00").
func (s Show) Contains(t time.Time) bool {
	m := TimeOfDay(t.Hour()*60 + t.Minute())
	if s.From <= s.To {
		return m >= s.From && m < s.To
	}
	return m >= s.From || m < s.To
}

// file is the YAML layout.
type file struct {
	Clock     map[string]Slot `yaml:"clock"`
	Programme yaml.Node       `yaml:"programme"`
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

	p := &Schedule{Clock: map[int]Slot{}}
	var errs []error

	if len(f.Clock) == 0 {
		errs = append(errs, errors.New("clock: no slots"))
	}
	for key, slot := range f.Clock {
		if slot.Segment == "" {
			errs = append(errs, fmt.Errorf("clock %q: no segment", key))
			continue
		}
		if key == "default" {
			s := slot
			p.Default = &s
			continue
		}
		minute, err := parseMinute(key)
		if err != nil {
			errs = append(errs, fmt.Errorf("clock %q: %w", key, err))
			continue
		}
		if _, dup := p.Clock[minute]; dup {
			errs = append(errs, fmt.Errorf("clock %q: minute %d given twice", key, minute))
			continue
		}
		if err := slot.Params.validate(); err != nil {
			errs = append(errs, fmt.Errorf("clock %q: %w", key, err))
			continue
		}
		p.Clock[minute] = slot
	}

	shows, err := parseProgramme(&f.Programme)
	if err != nil {
		errs = append(errs, err)
	}
	p.Programme = shows

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return p, nil
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

// Due returns the slot aired at the minute of t, if there is one. t must
// already be in the station's time zone.
func (p *Schedule) Due(t time.Time) (Slot, bool) {
	s, ok := p.Clock[t.Minute()]
	return s, ok
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

// Minutes returns the minutes of the clock in order.
func (p *Schedule) Minutes() []int {
	ms := make([]int, 0, len(p.Clock))
	for m := range p.Clock {
		ms = append(ms, m)
	}
	sort.Ints(ms)
	return ms
}

// Unknown returns a line for every slot whose segment is not in known. The
// director skips those slots; the caller decides whether that is fatal.
func (p *Schedule) Unknown(known func(string) bool) []string {
	var out []string
	for _, m := range p.Minutes() {
		if s := p.Clock[m]; !known(s.Segment) {
			out = append(out, fmt.Sprintf(":%02d %s", m, s.Segment))
		}
	}
	return out
}
