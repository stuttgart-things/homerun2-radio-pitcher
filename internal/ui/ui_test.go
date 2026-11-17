package ui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	homerun "github.com/stuttgart-things/homerun-library/v4"

	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/director"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/handlers"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/panel"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/schedule"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/segment"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/sources"
)

const plan = `
clock:
  ":00": { segment: time-check }
  ":15": { segment: fake, count: 2 }
clocks:
  night: {}
programme:
  "07:00-10:00": { show: morning-show, extra: [weather] }
  "22:00-06:00": { show: night, clock: night }
`

// fake records whether it was built as a preview.
type fake struct{ preview bool }

func (f *fake) Name() string { return "fake" }
func (f *fake) Build(ctx context.Context, _ time.Time, p schedule.Params) ([]segment.Item, error) {
	f.preview = segment.IsPreview(ctx)
	return []segment.Item{
		{Title: "Short <b>title</b>", Message: "fake: 3 of 4 <script>"},
		{Title: strings.Repeat("overflowing ", 10), Message: "long"},
	}[:p.Int("count", 1)], nil
}

type fakeSource struct{}

func (fakeSource) Name() string { return "dev" }
func (fakeSource) Facts(context.Context) ([]sources.Fact, error) {
	return nil, errors.New("connection refused")
}

func server(t *testing.T, base string, at string) (*httptest.Server, *fake) {
	t.Helper()
	s, err := schedule.Parse(strings.NewReader(plan))
	if err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Europe/Berlin")
	now, _ := time.ParseInLocation("2006-01-02 15:04", "2026-11-17 "+at, loc)
	f := &fake{}
	segs := segment.NewRegistry(segment.TimeCheck{}, f)
	d := &director.Director{Segments: segs, Pitcher: nopPitcher{}, Location: loc, Sleep: func(context.Context, time.Duration) error { return nil }}
	if _, err := d.Air(context.Background(), "fake", schedule.Params{"count": 1}, now); err != nil {
		t.Fatal(err)
	}
	tracked := sources.Track([]sources.Source{fakeSource{}})
	_, _ = tracked[0].Facts(context.Background())

	u := &Server{
		BasePath: base, Version: "v1.2.3", Location: loc,
		Schedule: func() (*schedule.Schedule, error) { return s, nil },
		Director: d, Segments: segs, Sources: tracked, Title: panel.Card4x6,
		Now: func() time.Time { return now },
	}
	root := http.NewServeMux()
	root.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })
	srv := httptest.NewServer(handlers.Mount(base, root, u.Handler()))
	t.Cleanup(srv.Close)
	return srv, f
}

type nopPitcher struct{}

func (nopPitcher) Pitch(context.Context, homerun.Message) (string, error) { return "1-0", nil }
func (nopPitcher) Ready(context.Context) error                            { return nil }
func (nopPitcher) Close() error                                           { return nil }

func get(t *testing.T, url string, header ...string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestIndexUnderBasePath(t *testing.T) {
	srv, _ := server(t, "/radio", "07:10")
	code, body := get(t, srv.URL+"/radio/")
	if code != 200 {
		t.Fatalf("code %d", code)
	}
	for _, want := range []string{
		`src="/radio/static/htmx.min.js"`,
		`hx-get="/radio/ui/part/now"`,
		`hx-post="/radio/ui/preview"`,
		"morning-show",         // show on air
		"07:00-10:00",          //
		"clock <b>default</b>", // show without own clock
		"next: <b>fake</b> at 07:15",
		"(in 5 min)",
		"07:00 weather ✓",                     // extra at window start, already past
		"segment does not exist yet, skipped", // weather is unknown
		"connection refused",
		"Short &lt;b&gt;title&lt;/b&gt;", // escaped
	} {
		if !strings.Contains(body, want) {
			t.Errorf("index lacks %q", want)
		}
	}
	if strings.Contains(body, "homerun2-tokens.css\">") {
		t.Error("portal tokens linked without the portal")
	}
	_, body = get(t, srv.URL+"/radio/", "X-Forwarded-Prefix", "/radio")
	if !strings.Contains(body, `<link rel="stylesheet" href="/static/homerun2-tokens.css">`) {
		t.Error("portal tokens not linked behind the portal")
	}

	// redirects to the UI, root routes stay at the root
	if code, _ := get(t, srv.URL+"/radio"); code != http.StatusTemporaryRedirect {
		t.Errorf("/radio: %d", code)
	}
	if code, _ := get(t, srv.URL+"/"); code != http.StatusTemporaryRedirect {
		t.Errorf("/: %d", code)
	}
	if code, body := get(t, srv.URL+"/health"); code != 200 || body != "ok" {
		t.Errorf("/health: %d %q", code, body)
	}
	if code, body := get(t, srv.URL+"/radio/static/htmx.min.js"); code != 200 || !strings.Contains(body, "htmx") {
		t.Errorf("htmx: %d", code)
	}
}

func TestNightAndRoot(t *testing.T) {
	srv, _ := server(t, "", "23:30")
	code, body := get(t, srv.URL+"/")
	if code != 200 {
		t.Fatalf("code %d", code)
	}
	for _, want := range []string{"night", "clock <b>night</b>", "Empty clock: silence", "next: <b>time-check</b> at 06:00", `src="/static/htmx.min.js"`} {
		if !strings.Contains(body, want) {
			t.Errorf("night lacks %q", want)
		}
	}
}

func TestManifestAndFragments(t *testing.T) {
	srv, _ := server(t, "/radio", "12:14")
	_, body := get(t, srv.URL+"/radio/ui/manifest.json")
	var m Manifest
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatal(err)
	}
	if m.Title != "Radio" || len(m.Fragments) != 2 || m.Fragments[0].URL != "/radio/ui/fragment/on-air" || m.Fragments[1].URL != "/radio/ui/fragment/last-aired" {
		t.Fatalf("manifest = %+v", m)
	}
	for _, f := range m.Fragments {
		code, body := get(t, srv.URL+f.URL)
		if code != 200 || !strings.HasPrefix(body, `<div class="hr-frag">`) || strings.Contains(body, "<script") {
			t.Errorf("%s: %d %q", f.ID, code, body)
		}
		if !strings.Contains(body, `href="/radio/"`) {
			t.Errorf("%s: link without base path", f.ID)
		}
	}
	_, body = get(t, srv.URL+"/radio/ui/fragment/on-air")
	if !strings.Contains(body, "no show") || !strings.Contains(body, "fake <span class=\"hr-muted\">in 1 min</span>") {
		t.Errorf("on-air = %s", body)
	}
}

func TestPreview(t *testing.T) {
	srv, f := server(t, "/radio", "12:00")
	post := func(form url.Values) (int, string) {
		resp, err := http.PostForm(srv.URL+"/radio/ui/preview", form)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	code, body := post(url.Values{"segment": {"fake"}, "params": {"count=2"}})
	if code != 200 || !f.preview {
		t.Fatalf("code %d, preview %v", code, f.preview)
	}
	for _, want := range []string{"2 card(s)", "Short\n&lt;b&gt;title&lt;/b&gt;", "fake: 3 of 4 &lt;script&gt;", `class="title overflow"`} {
		if !strings.Contains(body, want) {
			t.Errorf("preview lacks %q:\n%s", want, body)
		}
	}
	if _, body := post(url.Values{"segment": {"nope"}}); !strings.Contains(body, `unknown segment &#34;nope&#34;`) {
		t.Errorf("unknown segment: %s", body)
	}
	if _, body := post(url.Values{"segment": {"fake"}, "params": {"count=99"}}); !strings.Contains(body, "count 99") {
		t.Errorf("bad params: %s", body)
	}
	// GET is not allowed: the preview queries sources and the model
	if code, _ := get(t, srv.URL+"/radio/ui/preview"); code != http.StatusMethodNotAllowed {
		t.Errorf("GET preview: %d", code)
	}
}

func TestUntil(t *testing.T) {
	for d, want := range map[time.Duration]string{0: "now", 4*time.Minute + 20*time.Second: "in 4 min", 135 * time.Minute: "in 2h 15min"} {
		if got := until(d); got != want {
			t.Errorf("until(%v) = %q, want %q", d, got, want)
		}
	}
}
