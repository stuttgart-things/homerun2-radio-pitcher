package ui

import (
	"net/http"
	"time"

	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/director"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/panel"
)

// spotsView is the pool page: every spot of spots.yaml as the panel shows
// it, whether it may air now, and how often it aired since the start.
type spotsView struct {
	view
	Pool   []spotRow
	Active int
}

type spotRow struct {
	ID, Kind  string
	Card      card
	Weight    int
	When      string
	ActiveNow bool
	Aired     int
	LastAired time.Time
}

// SameDay reports whether t is today (the page shows the date otherwise).
func (v spotsView) SameDay(t time.Time) bool { return sameDay(t, v.Now) }

func sameDay(t, now time.Time) bool {
	y1, m1, d1 := t.In(now.Location()).Date()
	y2, m2, d2 := now.Date()
	return y1 == y2 && m1 == m2 && d1 == d2
}

func (s *Server) spotsPage(w http.ResponseWriter, r *http.Request) {
	sv := spotsView{view: s.build(r)}
	type tally struct {
		n    int
		last time.Time
	}
	// a spot's card is fixed text: the aired card with its line is the spot
	aired := map[string]*tally{}
	if s.Director != nil {
		keep := s.Director.Archive
		if keep <= 0 {
			keep = director.DefaultArchive
		}
		for _, a := range s.Director.Recent(keep) {
			if a.Segment != "spots" || a.Err != "" {
				continue
			}
			t := aired[a.Message]
			if t == nil {
				t = &tally{}
				aired[a.Message] = t
			}
			t.n++
			if a.At.After(t.last) {
				t.last = a.At
			}
		}
	}
	for _, sp := range s.Spots {
		title, msg := sp.Card()
		title = s.Title.Fit(title)
		row := spotRow{
			ID:        sp.ID,
			Kind:      sp.Kind,
			Card:      card{Lines: panel.Wrap(title, s.Title.Width), Fits: s.Title.Fits(title), Message: msg},
			Weight:    sp.Weighting(),
			When:      sp.When(),
			ActiveNow: sp.Active(sv.Now),
		}
		if t := aired[msg]; t != nil {
			row.Aired, row.LastAired = t.n, t.last.In(s.loc())
		}
		if row.ActiveNow {
			sv.Active++
		}
		sv.Pool = append(sv.Pool, row)
	}
	s.render(w, "spots", sv)
}
