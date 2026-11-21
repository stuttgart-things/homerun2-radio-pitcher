// Package spots reads spots.yaml: the pool of short cards the spots segment
// airs between the programme's slots, like a radio's commercial breaks.
// Spots are fixed text from the file; nothing is generated.
package spots

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"

	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/panel"
)

// Kinds of spot.
const (
	KindText  = "text"  // title + scrolling line
	KindQuote = "quote" // a quote and who said it
	KindGIF   = "gif"   // a picture the catcher plays, then a text card
)

// Spot is one entry of the pool.
type Spot struct {
	ID   string `yaml:"id"`
	Kind string `yaml:"kind"` // text (default), quote or gif

	// text and gif
	Title   string `yaml:"title"`
	Message string `yaml:"message"` // optional; the title scrolls without it

	// gif: a file in the catcher's visual_aid/ or an https:// URL. The card
	// carries it as the tag gif=<image>; a catcher without a rule for it
	// shows the card alone.
	Image string `yaml:"image"`

	// quote
	Text   string `yaml:"text"`
	Author string `yaml:"author"`

	// Weight makes a spot come up more often (default 1, 0 is off).
	Weight *int `yaml:"weight"`
	// Weekdays limits the spot to these days (mon … sun); empty: every day.
	Weekdays []string `yaml:"weekdays"`
	// Between limits it to a time of day, "11:00-12:00" (may cross
	// midnight); empty: all day.
	Between string `yaml:"between"`
	// From and Until limit it to dates, "2026-11-20", both inclusive.
	From  string `yaml:"from"`
	Until string `yaml:"until"`

	days       []time.Weekday
	fromMin    int
	toMin      int
	hasWindow  bool
	from, upTo time.Time // upTo: the day after Until
}

// Card is the spot as the panel shows it: the card title and the scrolling
// line.
func (s Spot) Card() (title, message string) {
	if s.Kind == KindQuote {
		return "Quote: " + s.Author, "“" + s.Text + "” " + s.Author
	}
	if s.Message == "" {
		return s.Title, s.Title
	}
	return s.Title, s.Message
}

// Weighting is the spot's weight, 1 when unset.
func (s Spot) Weighting() int {
	if s.Weight == nil {
		return 1
	}
	return *s.Weight
}

// Active reports whether the spot may air at t (in the station's zone).
func (s Spot) Active(t time.Time) bool {
	if s.Weighting() <= 0 {
		return false
	}
	if len(s.days) > 0 && !slices.Contains(s.days, t.Weekday()) {
		return false
	}
	if s.hasWindow {
		m := t.Hour()*60 + t.Minute()
		if s.fromMin <= s.toMin {
			if m < s.fromMin || m >= s.toMin {
				return false
			}
		} else if m < s.fromMin && m >= s.toMin {
			return false
		}
	}
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	if !s.from.IsZero() && day.Before(s.from) {
		return false
	}
	if !s.upTo.IsZero() && !day.Before(s.upTo) {
		return false
	}
	return true
}

var weekdays = map[string]time.Weekday{
	"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday,
	"thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday,
}

// Load reads and checks the pool at path. maxLen is the longest scrolling
// line the panel takes (RADIO_MESSAGE_MAX): a spot is never cut, so a
// longer one is an error.
func Load(path string, maxLen int) ([]Spot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data, maxLen)
}

// Parse reads and checks a pool; it reports every problem at once.
func Parse(data []byte, maxLen int) ([]Spot, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var f struct {
		Spots []Spot `yaml:"spots"`
	}
	if err := dec.Decode(&f); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("spots: file is empty")
		}
		return nil, fmt.Errorf("spots: %w", err)
	}
	var errs []error
	ids := map[string]bool{}
	for i := range f.Spots {
		s := &f.Spots[i]
		s.ID = strings.TrimSpace(s.ID)
		where := fmt.Sprintf("spot %d", i+1)
		if s.ID != "" {
			where = fmt.Sprintf("spot %q", s.ID)
		}
		bad := func(format string, a ...any) { errs = append(errs, fmt.Errorf(where+": "+format, a...)) }
		switch {
		case s.ID == "":
			bad("no id")
		case ids[s.ID]:
			bad("id given twice")
		}
		ids[s.ID] = true
		if s.Kind == "" {
			s.Kind = KindText
		}
		s.Image = strings.TrimSpace(s.Image)
		switch s.Kind {
		case KindText, KindGIF:
			s.Title, s.Message = strings.TrimSpace(s.Title), strings.TrimSpace(s.Message)
			if s.Title == "" {
				bad("a %s spot needs a title", s.Kind)
			}
			if s.Text != "" || s.Author != "" {
				bad("text and author belong to quote spots")
			}
			if s.Kind == KindGIF {
				if err := panel.CheckImage(s.Image); err != nil {
					bad("%v", err)
				}
			} else if s.Image != "" {
				bad("image belongs to gif spots")
			}
		case KindQuote:
			s.Text, s.Author = strings.TrimSpace(s.Text), strings.TrimSpace(s.Author)
			if s.Text == "" || s.Author == "" {
				bad("a quote spot needs text and author")
			}
			if s.Title != "" || s.Message != "" || s.Image != "" {
				bad("title, message and image do not belong to quote spots")
			}
		default:
			bad("kind %q, want text, quote or gif", s.Kind)
		}
		if _, msg := s.Card(); maxLen > 0 && utf8.RuneCountInString(msg) > maxLen {
			bad("scrolling line has %d characters, the panel takes %d (spots are never cut)", utf8.RuneCountInString(msg), maxLen)
		}
		if s.Weight != nil && *s.Weight < 0 {
			bad("weight %d, must be 0 or more", *s.Weight)
		}
		for _, d := range s.Weekdays {
			wd, ok := weekdays[strings.ToLower(strings.TrimSpace(d))]
			if !ok {
				bad("weekday %q, want mon, tue, wed, thu, fri, sat or sun", d)
				continue
			}
			s.days = append(s.days, wd)
		}
		if s.Between != "" {
			a, b, ok := strings.Cut(s.Between, "-")
			from, err1 := clock(a)
			to, err2 := clock(b)
			if !ok || err1 != nil || err2 != nil || from == to {
				bad("between %q, want \"HH:MM-HH:MM\"", s.Between)
			} else {
				s.fromMin, s.toMin, s.hasWindow = from, to, true
			}
		}
		if s.From != "" {
			t, err := time.Parse(time.DateOnly, strings.TrimSpace(s.From))
			if err != nil {
				bad("from %q, want YYYY-MM-DD", s.From)
			}
			s.from = t
		}
		if s.Until != "" {
			t, err := time.Parse(time.DateOnly, strings.TrimSpace(s.Until))
			if err != nil {
				bad("until %q, want YYYY-MM-DD", s.Until)
			} else {
				s.upTo = t.AddDate(0, 0, 1)
			}
		}
		if !s.from.IsZero() && !s.upTo.IsZero() && !s.from.Before(s.upTo) {
			bad("from %s is after until %s", s.From, s.Until)
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return f.Spots, nil
}

func clock(s string) (int, error) {
	h, m, ok := strings.Cut(strings.TrimSpace(s), ":")
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if !ok || err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, fmt.Errorf("%q is not HH:MM", s)
	}
	return hh*60 + mm, nil
}

// When describes the spot's limits for people, "always" without any:
// "mon, tue · 11:00-12:00 · 2026-11-20 – 2026-12-02".
func (s Spot) When() string {
	var parts []string
	if len(s.Weekdays) > 0 {
		parts = append(parts, strings.Join(s.Weekdays, ", "))
	}
	if s.Between != "" {
		parts = append(parts, s.Between)
	}
	switch {
	case s.From != "" && s.Until != "":
		parts = append(parts, s.From+" – "+s.Until)
	case s.From != "":
		parts = append(parts, "from "+s.From)
	case s.Until != "":
		parts = append(parts, "until "+s.Until)
	}
	if len(parts) == 0 {
		return "always"
	}
	return strings.Join(parts, " · ")
}
