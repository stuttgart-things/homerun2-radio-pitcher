package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/schedule"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/segment"
)

// linked airs one card with a URL, one with a javascript: URL that the
// news segment would never set but the template must still escape.
type linked struct{}

func (linked) Name() string { return "news" }
func (linked) Build(_ context.Context, _ time.Time, p schedule.Params) ([]segment.Item, error) {
	return []segment.Item{
		{Title: "heise: VW per Bluetooth", Message: "heise: Hunderttausende VW lassen sich kapern", URL: "https://www.heise.de/news/1"},
		{Title: "hn: Gemini", Message: "hn: Gemini 4 Neon"},
	}, nil
}

func TestArchive(t *testing.T) {
	srv, _, d := serverWithToken(t, "/radio", "15:16", "")
	d.Segments = segment.NewRegistry(segment.TimeCheck{}, &fake{}, linked{})
	if _, err := d.Air(context.Background(), "news", nil, time.Now()); err != nil {
		t.Fatal(err)
	}

	code, body := get(t, srv.URL+"/radio/aired")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	for _, want := range []string{
		"3 of 3 cards",
		`href="https://www.heise.de/news/1"`,
		"Hunderttausende VW",
		"fake: 3 of 4 &lt;script&gt;", // escaped
		`href="/radio/"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("archive lacks %q", want)
		}
	}

	_, body = get(t, srv.URL+"/radio/aired?segment=news&q=vw")
	if !strings.Contains(body, "1 of 3 cards") || strings.Contains(body, "Gemini") || !strings.Contains(body, `<option selected>news</option>`) {
		t.Errorf("filtered archive:\n%s", body)
	}

	// the programme page links the archive and the article
	_, body = get(t, srv.URL+"/radio/")
	if !strings.Contains(body, `href="/radio/aired"`) || !strings.Contains(body, `href="https://www.heise.de/news/1"`) {
		t.Error("index lacks the archive or article link")
	}
}
