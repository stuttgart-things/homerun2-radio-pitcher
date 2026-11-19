// Package ui serves the read-only programme UI (htmx) under BASE_PATH, and
// the manifest and fragments homerun2-portal composes into its overview.
// Nothing here changes what airs: the preview builds a segment without
// pitching it and without touching its state.
package ui

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/director"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/panel"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/schedule"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/segment"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/sources"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

var templates = template.Must(template.New("").Funcs(template.FuncMap{
	"join": strings.Join,
}).ParseFS(templateFS, "templates/*.html"))

// Server is the UI. Every field but Now and PreviewTimeout is required.
type Server struct {
	BasePath string // "" or "/radio"
	Version  string
	Location *time.Location
	Schedule func() (*schedule.Schedule, error)
	Director *director.Director
	Segments segment.Registry
	Sources  []*sources.Tracked
	Title    panel.Title
	// Now is time.Now outside tests.
	Now func() time.Time
	// PreviewTimeout bounds a preview; 0 means 60s.
	PreviewTimeout time.Duration
	// Token enables the session controls (RADIO_UI_TOKEN); empty: read-only.
	Token string
	// Started is when the station started: the archive begins there.
	Started time.Time

	preview sync.Mutex // one preview at a time: it queries sources and the model
}

// Handler returns the UI routes, relative to BasePath (mount it with
// http.StripPrefix or handlers.Mount).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(static))))
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("GET /aired", s.archive)
	mux.HandleFunc("GET /ui/part/now", s.part("now"))
	mux.HandleFunc("GET /ui/part/aired", s.part("aired"))
	mux.HandleFunc("GET /ui/part/sources", s.part("sources"))
	mux.HandleFunc("POST /ui/preview", s.previewHandler)
	mux.HandleFunc("GET /ui/manifest.json", s.manifest)
	mux.HandleFunc("GET /ui/fragment/on-air", s.part("fragment-on-air"))
	mux.HandleFunc("GET /ui/fragment/last-aired", s.part("fragment-last-aired"))
	if s.Token != "" {
		mux.HandleFunc("GET /ui/part/control", s.part("control"))
		mux.HandleFunc("POST /ui/login", s.login)
		mux.HandleFunc("POST /ui/logout", s.logout)
		mux.HandleFunc("POST /ui/control/{action}", s.control)
	}
	return mux
}

// view is what every template sees.
type view struct {
	Base        string
	Version     string
	PortalTheme bool
	Now         time.Time
	Zone        string
	Err         string // schedule load error (last good version on air)

	Show     *showView
	Clock    string
	Slots    []slotView
	Next     *nextView
	OnAir    bool
	Timeline []barView
	NowPct   float64
	Extras   []extraView

	Controls  bool // session controls are configured
	LoggedIn  bool
	Override  *overrideView
	Clocks    []string
	Durations []durationOption
	Notice    string
	NoticeErr bool

	Aired    []director.Aired
	Sources  []sources.Status
	Segments []string
	Card     panel.Title
}

type showView struct {
	Name, Window string
}

type slotView struct {
	Minute  string
	Pct     float64 // position on the hour bar
	Segment string
	Params  string
	Unknown bool
	Next    bool
	Past    bool
	Skipped bool // a session override skips it this hour
}

type nextView struct {
	At       string
	In       string
	Segments string
}

type barView struct {
	Name    string
	Window  string
	Clock   string
	Left    float64
	Width   float64
	Current bool
}

type extraView struct {
	At      string
	Segment string
	Unknown bool
	Done    bool
}

func (s *Server) now() time.Time {
	t := time.Now()
	if s.Now != nil {
		t = s.Now()
	}
	return t.In(s.loc())
}

func (s *Server) loc() *time.Location {
	if s.Location != nil {
		return s.Location
	}
	return time.Local
}

// build fills the view from the schedule and the director.
func (s *Server) build(r *http.Request) view {
	now := s.now()
	v := view{
		Base:        s.BasePath,
		Version:     s.Version,
		PortalTheme: r.Header.Get("X-Forwarded-Prefix") != "",
		Now:         now,
		Zone:        s.loc().String(),
		Card:        s.Title,
		Segments:    s.Segments.Names(),
	}
	if s.Director != nil {
		v.OnAir = s.Director.OnAir()
		v.Aired = s.Director.Recent(20)
		v.Override = overrides(s.Director.Overrides(now), now)
	}
	v.Controls = s.Token != ""
	v.LoggedIn = v.Controls && s.authorized(r) != ""
	v.Durations = durations
	for _, t := range s.Sources {
		st := t.Status()
		if !st.At.IsZero() {
			st.At = st.At.In(s.loc())
		}
		v.Sources = append(v.Sources, st)
	}
	sched, err := s.Schedule()
	if err != nil {
		v.Err = err.Error()
	}
	if sched == nil {
		return v
	}
	known := s.Segments.Has

	minute := now.Hour()*60 + now.Minute()
	v.NowPct = pct(minute)
	show, inShow := sched.ShowAt(now)
	if inShow {
		v.Show = &showView{Name: show.Show, Window: show.Window}
		for _, e := range show.Extra {
			at := show.From
			if e.HasAt {
				at = e.At
			}
			// done: its time has passed within the current window
			done := sinceStart(show, schedule.TimeOfDay(minute)) > sinceStart(show, at)
			v.Extras = append(v.Extras, extraView{At: at.String(), Segment: e.Segment, Unknown: !known(e.Segment), Done: done})
		}
	}
	for _, sh := range sched.Programme {
		clock := sh.Clock
		if clock == "" {
			clock = "default"
		}
		cur := inShow && sh.Window == show.Window
		from, to := int(sh.From), int(sh.To)
		if from < to {
			v.Timeline = append(v.Timeline, barView{Name: sh.Show, Window: sh.Window, Clock: clock, Left: pct(from), Width: pct(to - from), Current: cur})
		} else { // crosses midnight: two bars
			v.Timeline = append(v.Timeline,
				barView{Name: sh.Show, Window: sh.Window, Clock: clock, Left: pct(from), Width: pct(24*60 - from), Current: cur},
				barView{Name: sh.Show, Window: sh.Window, Clock: clock, Left: 0, Width: pct(to), Current: cur})
		}
	}

	v.Clocks = append([]string{"default"}, sortedKeys(sched.Clocks)...)
	name, slots := sched.ClockAt(now)
	at, next, ok := sched.Next(now, known)
	if s.Director != nil {
		name, slots = s.Director.ClockAt(sched, now)
		at, next, ok = s.Director.Next(now)
	}
	v.Clock = name
	ms := make([]int, 0, len(slots))
	for m := range slots {
		ms = append(ms, m)
	}
	sort.Ints(ms)
	for _, m := range ms {
		for i, sl := range slots[m] {
			minute := fmt.Sprintf(":%02d", m)
			if i > 0 { // the rest of a block: same minute, aired after the first
				minute = ""
			}
			v.Slots = append(v.Slots, slotView{
				Minute:  minute,
				Pct:     float64(m) * 100 / 60,
				Segment: sl.Segment,
				Params:  params(sl.Params),
				Unknown: !known(sl.Segment),
				Next:    ok && at.Minute() == m && at.Hour() == now.Hour(),
				Past:    m <= now.Minute(),
				Skipped: skipped(s.Director, now, m),
			})
		}
	}
	if ok {
		names := make([]string, len(next))
		for i, sl := range next {
			names[i] = sl.Segment
		}
		v.Next = &nextView{At: at.Format("15:04"), In: until(at.Sub(now)), Segments: strings.Join(names, ", ")}
	}
	return v
}

// sinceStart is how many minutes after the show's start m lies.
func sinceStart(s schedule.Show, m schedule.TimeOfDay) int {
	return (int(m) - int(s.From) + 24*60) % (24 * 60)
}

func pct(minutes int) float64 { return float64(minutes) * 100 / (24 * 60) }

func until(d time.Duration) string {
	m := int(d.Round(time.Minute) / time.Minute)
	switch {
	case m < 1:
		return "now"
	case m < 60:
		return fmt.Sprintf("in %d min", m)
	default:
		return fmt.Sprintf("in %dh %02dmin", m/60, m%60)
	}
}

func params(p schedule.Params) string {
	keys := make([]string, 0, len(p))
	for k := range p {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s=%v", k, p[k])
	}
	return strings.Join(parts, " ")
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	s.render(w, "index", s.build(r))
}

// archiveView is the archive page: every aired card the director keeps,
// filtered by segment and a search text.
type archiveView struct {
	view
	Items   []director.Aired
	Total   int
	Keep    int
	Started string
	Segment string
	Q       string
}

// SameDay reports whether t is today (the page shows the date otherwise).
func (a archiveView) SameDay(t time.Time) bool {
	y1, m1, d1 := t.In(a.Now.Location()).Date()
	y2, m2, d2 := a.Now.Date()
	return y1 == y2 && m1 == m2 && d1 == d2
}

func (s *Server) archive(w http.ResponseWriter, r *http.Request) {
	av := archiveView{
		view:    s.build(r),
		Segment: strings.TrimSpace(r.URL.Query().Get("segment")),
		Q:       strings.TrimSpace(r.URL.Query().Get("q")),
	}
	if !s.Started.IsZero() {
		av.Started = s.Started.In(s.loc()).Format("02.01. 15:04")
	}
	if s.Director != nil {
		av.Keep = s.Director.Archive
		if av.Keep <= 0 {
			av.Keep = director.DefaultArchive
		}
		all := s.Director.Recent(av.Keep)
		av.Total = len(all)
		// the filter offers what aired, besides the registered segments
		for _, a := range all {
			if !slices.Contains(av.Segments, a.Segment) {
				av.Segments = append(av.Segments, a.Segment)
			}
		}
		sort.Strings(av.Segments)
		q := strings.ToLower(av.Q)
		for _, a := range all {
			if av.Segment != "" && a.Segment != av.Segment {
				continue
			}
			if q != "" && !strings.Contains(strings.ToLower(a.Title+" "+a.Message), q) {
				continue
			}
			a.At = a.At.In(s.loc())
			av.Items = append(av.Items, a)
		}
	}
	s.render(w, "archive", av)
}

func (s *Server) part(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { s.render(w, name, s.build(r)) }
}

func (s *Server) render(w http.ResponseWriter, name string, v any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := templates.ExecuteTemplate(w, name, v); err != nil {
		slog.Warn("ui: render", "template", name, "error", err)
	}
}

// card is one previewed item, wrapped like the matrix shows it.
type card struct {
	Lines   []string
	Fits    bool
	Message string
}

type previewView struct {
	Segment string
	Params  string
	Cards   []card
	Err     string
	Took    string
}

// previewHandler builds a segment now without pitching it.
func (s *Server) previewHandler(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("segment"))
	raw := strings.TrimSpace(r.FormValue("params"))
	pv := previewView{Segment: name, Params: raw}
	seg, ok := s.Segments[name]
	if !ok {
		pv.Err = fmt.Sprintf("unknown segment %q", name)
		s.render(w, "preview", pv)
		return
	}
	p, err := schedule.ParseParams(strings.Fields(raw))
	if err != nil {
		pv.Err = err.Error()
		s.render(w, "preview", pv)
		return
	}
	if !s.preview.TryLock() {
		w.WriteHeader(http.StatusTooManyRequests)
		pv.Err = "another preview is running, try again in a moment"
		s.render(w, "preview", pv)
		return
	}
	defer s.preview.Unlock()

	timeout := s.PreviewTimeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(segment.Preview(r.Context()), timeout)
	defer cancel()
	start := time.Now()
	items, err := seg.Build(ctx, s.now(), p)
	pv.Took = time.Since(start).Round(time.Millisecond).String()
	if err != nil {
		pv.Err = err.Error()
	}
	for _, it := range items {
		pv.Cards = append(pv.Cards, card{Lines: panel.Wrap(it.Title, s.Title.Width), Fits: s.Title.Fits(it.Title), Message: it.Message})
	}
	s.render(w, "preview", pv)
}

// Fragment and Manifest follow homerun2-portal's micro-frontend contract.
type Fragment struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	URL            string `json:"url"`
	RefreshSeconds int    `json:"refreshSeconds"`
}

type Manifest struct {
	Name      string     `json:"name"`
	Title     string     `json:"title"`
	Version   string     `json:"version"`
	BasePath  string     `json:"basePath"`
	Fragments []Fragment `json:"fragments"`
}

func (s *Server) manifest(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(Manifest{
		Name:     "homerun2-radio-pitcher",
		Title:    "Radio",
		Version:  s.Version,
		BasePath: s.BasePath,
		Fragments: []Fragment{
			{ID: "on-air", Title: "On air", URL: s.BasePath + "/ui/fragment/on-air", RefreshSeconds: 15},
			{ID: "last-aired", Title: "Last aired", URL: s.BasePath + "/ui/fragment/last-aired", RefreshSeconds: 15},
		},
	})
}

func sortedKeys(m map[string]schedule.Clock) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// skipped reports whether a session override skips minute m of this hour.
func skipped(d *director.Director, now time.Time, m int) bool {
	if d == nil {
		return false
	}
	sk := d.Overrides(now).Skip
	if sk.IsZero() {
		return false
	}
	sk = sk.In(now.Location())
	return sk.Hour() == now.Hour() && sk.Minute() == m && sk.YearDay() == now.YearDay()
}
