package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/director"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/handlers"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/panel"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/schedule"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/segment"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/spots"
)

const pool = `spots:
  - id: lunch
    title: "Lunch at 12"
    message: "Canteen today: pasta"
    weekdays: [mon, tue, wed, thu, fri]
    between: "11:00-12:00"
  - id: quote-knuth
    kind: quote
    text: "Premature optimization is the root of all evil."
    author: Donald Knuth
    weight: 3
  - id: retired
    title: "Old news <b>"
    weight: 0
  - id: evening
    kind: gif
    image: sunset.gif
    title: "Good evening"
    between: "17:00-23:00"
`

func spotsServer(t *testing.T, token string) *httptest.Server {
	t.Helper()
	ps, err := spots.Parse([]byte(pool), 200)
	if err != nil {
		t.Fatal(err)
	}
	s, err := schedule.Parse(strings.NewReader(plan))
	if err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Europe/Berlin")
	now, _ := time.ParseInLocation("2006-01-02 15:04", "2026-11-17 11:30", loc) // a Tuesday
	segs := segment.NewRegistry(segment.TimeCheck{}, &segment.Spots{Pool: ps, Title: panel.Card4x6})
	d := &director.Director{Schedule: staticSchedule{s}, Segments: segs, Pitcher: nopPitcher{}, Location: loc, Sleep: func(context.Context, time.Duration) error { return nil }}
	for range 2 {
		if _, err := d.Air(context.Background(), "spots", schedule.Params{"ids": "lunch"}, now); err != nil {
			t.Fatal(err)
		}
	}
	u := &Server{
		BasePath: "/radio", Version: "v1.2.3", Location: loc,
		Schedule: func() (*schedule.Schedule, error) { return s, nil },
		Director: d, Segments: segs, Title: panel.Card4x6,
		Now: func() time.Time { return now }, Token: token, Spots: ps,
	}
	srv := httptest.NewServer(handlers.Mount("/radio", http.NewServeMux(), u.Handler()))
	t.Cleanup(srv.Close)
	return srv
}

func TestSpotsPage(t *testing.T) {
	srv := spotsServer(t, testToken)
	code, body := get(t, srv.URL+"/radio/spots")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	for _, want := range []string{
		"4 in spots.yaml, 2 may air now",
		"mon, tue, wed, thu, fri · 11:00-12:00",
		"Canteen today: pasta",
		"2×",                   // lunch aired twice
		"Quote: Donald\nKnuth", // wrapped like the panel
		"Old news &lt;b&gt;",   // escaped
		"<td class=\"nowrap\">off</td>",
		"Unlock the controls",
		"▶ sunset.gif",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("spots page lacks %q", want)
		}
	}
	if strings.Contains(body, "Air now") {
		t.Error("air now without login")
	}

	// unlocked: an air-now button per spot that may air now
	_, body = get(t, srv.URL+"/radio/spots", "Authorization", "Bearer "+testToken)
	if n := strings.Count(body, ">Air now<"); n != 2 || !strings.Contains(body, `value="ids=lunch"`) || strings.Contains(body, `value="ids=retired"`) {
		t.Errorf("air now buttons = %d:\n%s", n, body)
	}

	// the programme page links the pool
	_, body = get(t, srv.URL+"/radio/")
	if !strings.Contains(body, `href="/radio/spots"`) {
		t.Error("index lacks the spots link")
	}
}

func TestNoSpotsPage(t *testing.T) {
	srv, _ := server(t, "/radio", "07:10")
	if code, _ := get(t, srv.URL+"/radio/spots"); code != http.StatusNotFound {
		t.Errorf("status %d, want 404 without a pool", code)
	}
	if _, body := get(t, srv.URL+"/radio/"); strings.Contains(body, "/spots") {
		t.Error("index links a pool that does not exist")
	}
}
