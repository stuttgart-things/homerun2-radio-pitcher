package segment

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/github"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/panel"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/schedule"
)

type fakeReleases struct {
	list  []github.Release
	err   error
	since time.Time
}

func (f *fakeReleases) Releases(_ context.Context, since time.Time) ([]github.Release, error) {
	f.since = since
	return f.list, f.err
}

func TestReleasesSegment(t *testing.T) {
	berlin, _ := time.LoadLocation("Europe/Berlin")
	at := time.Date(2026, 11, 19, 15, 0, 0, 0, berlin)
	f := &fakeReleases{list: []github.Release{
		{Repo: "homerun2-radio-pitcher", Tag: "v0.15.0", Note: "news mix lets the feeds take turns", URL: "https://github.com/o/r/releases/tag/v0.15.0", At: at.Add(-time.Hour)},
		{Repo: "argocd", Tag: "v0.119.0", At: at.Add(-2 * time.Hour)},
	}}
	r := &Releases{Lister: f, Org: "stuttgart-things", Title: panel.Card4x6, MessageMax: 120}
	ctx := context.Background()

	items, err := r.Build(ctx, at, schedule.Params{"count": 3})
	if err != nil {
		t.Fatal(err)
	}
	if f.since != at.Add(-24*time.Hour) {
		t.Errorf("since = %v", f.since)
	}
	if len(items) != 2 ||
		items[0].Message != "homerun2-radio-pitcher v0.15.0: news mix lets the feeds take turns" ||
		items[1].Message != "stuttgart-things released argocd v0.119.0 at 13:00" ||
		!panel.Card4x6.Fits(items[0].Title) ||
		items[0].URL != "https://github.com/o/r/releases/tag/v0.15.0" {
		t.Fatalf("items = %+v", items)
	}
	// each once
	if items, _ = r.Build(ctx, at, schedule.Params{"count": 3}); len(items) != 0 {
		t.Fatalf("repeated: %+v", items)
	}
	// repos filter and within
	r2 := &Releases{Lister: f, Org: "o", Title: panel.Card4x6, MessageMax: 120}
	items, _ = r2.Build(ctx, at, schedule.Params{"repos": []any{"argocd"}, "within": "3h"})
	if len(items) != 1 || f.since != at.Add(-3*time.Hour) {
		t.Fatalf("filtered = %+v", items)
	}
	// errors surface, no lister is silent
	if _, err := (&Releases{Lister: &fakeReleases{err: errors.New("403")}}).Build(ctx, at, nil); err == nil {
		t.Fatal("no error")
	}
	if items, err := (&Releases{}).Build(ctx, at, nil); err != nil || items != nil {
		t.Fatalf("no lister: %v %v", items, err)
	}
}
