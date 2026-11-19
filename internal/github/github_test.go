package github

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const notes = "## [1.31.1](https://github.com/o/r/compare/v1.31.0...v1.31.1) (2026-11-19)\n\n\n### Bug Fixes\n\n* **catalog:** keep field names in the VM cards ([#249](https://github.com/o/r/issues/249)) ([#250](https://github.com/o/r/issues/250)) ([4fc6287](https://github.com/o/r/commit/4fc6287abc))\n* second fix\n"

func ev(typ, repo, action, tag string, at time.Time, pre bool) string {
	return fmt.Sprintf(`{"type":%q,"created_at":%q,"repo":{"name":%q},"payload":{"action":%q,"release":{"tag_name":%q,"body":%q,"html_url":"https://x/%s","prerelease":%t,"published_at":%q}}}`,
		typ, at.Format(time.RFC3339), repo, action, tag, notes, tag, pre, at.Format(time.RFC3339))
}

func TestReleases(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/orgs/stuttgart-things/events" || r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "bad request "+r.URL.String(), http.StatusBadRequest)
			return
		}
		evs := []string{
			ev("PushEvent", "stuttgart-things/radio", "", "", now, false),
			ev("ReleaseEvent", "stuttgart-things/radio", "published", "v0.15.0", now.Add(-time.Hour), false),
			ev("ReleaseEvent", "stuttgart-things/radio", "published", "v0.16.0-rc.1", now.Add(-2*time.Hour), true),
			ev("ReleaseEvent", "stuttgart-things/backstage", "published", "v1.31.1", now.Add(-3*time.Hour), false),
			ev("ReleaseEvent", "stuttgart-things/old", "published", "v1.0.0", now.Add(-48*time.Hour), false),
		}
		fmt.Fprint(w, "["+strings.Join(evs, ",")+"]")
	}))
	defer srv.Close()

	c := &Client{Org: "stuttgart-things", Token: "tok", BaseURL: srv.URL, HTTP: srv.Client()}
	got, err := c.Releases(context.Background(), now.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Repo != "radio" || got[0].Tag != "v0.15.0" || got[1].Repo != "backstage" {
		t.Fatalf("releases = %+v", got)
	}
	if got[1].Note != "catalog: keep field names in the VM cards" {
		t.Errorf("note = %q", got[1].Note)
	}
	// cached: a narrower window costs no request
	got, _ = c.Releases(context.Background(), now.Add(-2*time.Hour))
	if len(got) != 1 || calls.Load() != 1 {
		t.Fatalf("cached: %+v, %d calls", got, calls.Load())
	}
	// a wider window refetches
	if _, err := c.Releases(context.Background(), now.Add(-72*time.Hour)); err != nil || calls.Load() != 2 {
		t.Fatalf("wider window: %v, %d calls", err, calls.Load())
	}
}

func TestReleasesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rate limited", http.StatusForbidden)
	}))
	defer srv.Close()
	c := &Client{Org: "o", BaseURL: srv.URL, HTTP: srv.Client()}
	if _, err := c.Releases(context.Background(), time.Now().Add(-time.Hour)); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("err = %v", err)
	}
}

func TestFirstNote(t *testing.T) {
	for in, want := range map[string]string{
		notes: "catalog: keep field names in the VM cards",
		"# [0.15.0](x) (2026-11-19)\n\n### Features\n\n* news mix lets the feeds take turns, each in its own order ([ad494c4](https://x/commit/ad494c4e7efe))\n": "news mix lets the feeds take turns, each in its own order",
		"- plain `code` entry (#12)": "plain code entry",
		"* news mix ([ad494c4](https://x/c/ad494c4)), closes [#50](https://x/issues/50) [#51](https://x/issues/51)":                                                                       "news mix",
		"* **fluent-bit:** expose Buffer_Size ([#1045](https://x/issues/1045)) ([fe1264c](https://x/commit/fe1264ca0e74)), closes [stuttgart-things/harvester#753](https://x/issues/753)": "fluent-bit: expose Buffer_Size",
		"no list here": "",
	} {
		if got := FirstNote(in); got != want {
			t.Errorf("FirstNote(%q) = %q, want %q", in[:min(30, len(in))], got, want)
		}
	}
}
