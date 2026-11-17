package ui

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/director"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/schedule"
)

// Session controls (stage 2): pause, skip the next slot, switch the clock,
// air a segment now. They need RADIO_UI_TOKEN: either as a bearer token
// (scripts) or once in the page, which sets an HttpOnly, SameSite=Strict
// cookie. A cookie only counts on requests that carry htmx's HX-Request
// header, which a form on another site cannot set.

const cookieName = "radio_ui"

type durationOption struct {
	Minutes int
	Label   string
}

var durations = []durationOption{
	{15, "15 min"}, {30, "30 min"}, {60, "1 h"}, {120, "2 h"}, {240, "4 h"}, {480, "8 h"}, {720, "12 h"},
}

type overrideView struct {
	PausedUntil string
	SkipAt      string
	Clock       string
	ClockUntil  string
}

func overrides(o director.Overrides, now time.Time) *overrideView {
	if o.PausedUntil.IsZero() && o.Skip.IsZero() && o.Clock == "" {
		return nil
	}
	v := &overrideView{Clock: o.Clock}
	if !o.PausedUntil.IsZero() {
		v.PausedUntil = clockTime(o.PausedUntil, now)
	}
	if !o.Skip.IsZero() {
		v.SkipAt = clockTime(o.Skip, now)
	}
	if o.Clock != "" {
		v.ClockUntil = clockTime(o.ClockUntil, now)
	}
	return v
}

// clockTime is "15:04", or "Wed 15:04" when it is not today.
func clockTime(t, now time.Time) string {
	t = t.In(now.Location())
	if t.YearDay() != now.YearDay() || t.Year() != now.Year() {
		return t.Format("Mon 15:04")
	}
	return t.Format("15:04")
}

func (s *Server) cookieValue() string {
	sum := sha256.Sum256([]byte("homerun2-radio-ui:" + s.Token))
	return hex.EncodeToString(sum[:])
}

// authorized returns who the request acts as ("api" with the bearer token,
// "ui" with the cookie), or "" when it may not control the station.
func (s *Server) authorized(r *http.Request) string {
	if s.Token == "" {
		return ""
	}
	if tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		if subtle.ConstantTimeCompare([]byte(tok), []byte(s.Token)) == 1 {
			return "api"
		}
		return ""
	}
	c, err := r.Cookie(cookieName)
	if err != nil {
		return ""
	}
	if subtle.ConstantTimeCompare([]byte(c.Value), []byte(s.cookieValue())) != 1 {
		return ""
	}
	// GET renders the controls; changes need htmx's header (CSRF)
	if r.Method != http.MethodGet && r.Header.Get("HX-Request") != "true" {
		return ""
	}
	return "ui"
}

func (s *Server) setCookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    value,
		Path:     s.BasePath + "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		SameSite: http.SameSiteStrictMode,
	})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	v := s.build(r)
	tok := r.FormValue("token")
	if r.Header.Get("HX-Request") != "true" || subtle.ConstantTimeCompare([]byte(tok), []byte(s.Token)) != 1 {
		time.Sleep(500 * time.Millisecond) // slows down guessing
		status(w, r, http.StatusForbidden)
		v.Notice, v.NoticeErr = "wrong token", true
		s.render(w, "control", v)
		return
	}
	s.setCookie(w, r, s.cookieValue(), 12*60*60)
	v.LoggedIn = true
	s.render(w, "control", v)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	s.setCookie(w, r, "", -1)
	v := s.build(r)
	v.LoggedIn = false
	s.render(w, "control", v)
}

// control runs one action and answers with the control panel; the page's
// "now" section reloads on the radio-changed event.
func (s *Server) control(w http.ResponseWriter, r *http.Request) {
	who := s.authorized(r)
	if who == "" {
		status(w, r, http.StatusForbidden)
		v := s.build(r)
		v.Notice, v.NoticeErr = "not logged in", true
		s.render(w, "control", v)
		return
	}
	now := s.now()
	d := s.Director
	var notice string
	var err error
	switch r.PathValue("action") {
	case "pause":
		var until time.Time
		if until, err = s.until(r, now); err == nil {
			if err = d.Pause(now, until, who); err == nil {
				notice = "paused until " + clockTime(until, now)
			}
		}
	case "resume":
		d.Resume(who)
		notice = "resumed"
	case "skip":
		var at time.Time
		if at, err = d.SkipNext(now, who); err == nil {
			notice = "the slot at " + clockTime(at, now) + " is skipped"
		}
	case "unskip":
		d.Unskip(who)
		notice = "skip cancelled"
	case "clock":
		var until time.Time
		clock := r.FormValue("clock")
		if until, err = s.until(r, now); err == nil {
			if err = d.SwitchClock(now, clock, until, who); err == nil {
				notice = fmt.Sprintf("clock %s until %s", clock, clockTime(until, now))
			}
		}
	case "restore":
		d.RestoreClock(who)
		notice = "clock of the programme restored"
	case "air":
		seg := strings.TrimSpace(r.FormValue("segment"))
		var p schedule.Params
		if p, err = schedule.ParseParams(strings.Fields(r.FormValue("params"))); err == nil {
			if err = d.AirNow(seg, p, who); err == nil {
				notice = seg + " goes on air"
			}
		}
	default:
		http.NotFound(w, r)
		return
	}
	v := s.build(r)
	v.LoggedIn = true
	if err != nil {
		status(w, r, http.StatusBadRequest)
		v.Notice, v.NoticeErr = err.Error(), true
	} else {
		v.Notice = notice
		w.Header().Set("HX-Trigger", "radio-changed")
	}
	s.render(w, "control", v)
}

// until reads the form's minutes (one of durations).
func (s *Server) until(r *http.Request, now time.Time) (time.Time, error) {
	m, err := strconv.Atoi(r.FormValue("minutes"))
	if err != nil || m < 1 || time.Duration(m)*time.Minute > director.MaxOverride {
		return time.Time{}, fmt.Errorf("minutes %q: 1 to %d", r.FormValue("minutes"), int(director.MaxOverride/time.Minute))
	}
	return now.Add(time.Duration(m) * time.Minute), nil
}

// status sets an error status for scripts. htmx does not swap 4xx answers,
// so a page request gets 200 and the error in the panel.
func status(w http.ResponseWriter, r *http.Request, code int) {
	if r.Header.Get("HX-Request") == "true" {
		return
	}
	w.WriteHeader(code)
}
