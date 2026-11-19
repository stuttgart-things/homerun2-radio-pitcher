package segment

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/panel"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/schedule"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/sources"
)

type fakeRollouts struct {
	name  string
	list  []sources.Rollout
	err   error
	since time.Time
}

func (f *fakeRollouts) Name() string { return f.name }
func (f *fakeRollouts) Rollouts(_ context.Context, since time.Time) ([]sources.Rollout, error) {
	f.since = since
	var out []sources.Rollout
	for _, r := range f.list {
		if r.At.After(since) {
			out = append(out, r)
		}
	}
	return out, f.err
}

func TestRollouts(t *testing.T) {
	berlin, _ := time.LoadLocation("Europe/Berlin")
	at := time.Date(2026, 11, 19, 15, 0, 0, 0, berlin)
	k := &fakeRollouts{name: "dev", list: []sources.Rollout{
		{ID: "a", Source: "dev", Kind: sources.KindDeployment, Namespace: "homerun2", Name: "homerun2-radio-pitcher", Version: "v0.14.0", Upgrade: true, At: at.Add(-time.Hour)},
		{ID: "b", Source: "dev", Kind: sources.KindDeployment, Namespace: "homerun2", Name: "ui", Version: "v1.0.0", At: at.Add(-2 * time.Hour)},
		{ID: "c", Source: "dev", Kind: sources.KindDeployment, Namespace: "old", Name: "old", At: at.Add(-10 * time.Hour)},
	}}
	f := &fakeRollouts{name: "dev", list: []sources.Rollout{
		{ID: "h", Source: "dev", Kind: sources.KindHelmRelease, Namespace: "cert-manager", Name: "cert-manager", Version: "v1.15.0", Upgrade: true, At: at.Add(-30 * time.Minute)},
	}}
	broken := &fakeRollouts{name: "down", err: errors.New("forbidden")}
	r := &Rollouts{Sources: []sources.RolloutSource{k, f, broken}, Title: panel.Card4x6, MessageMax: 120}
	ctx := context.Background()

	items, err := r.Build(ctx, at, schedule.Params{"count": 5})
	if err != nil {
		t.Fatal(err)
	}
	if k.since != at.Add(-6*time.Hour) {
		t.Errorf("since = %v", k.since)
	}
	want := []string{
		"dev: Helm release cert-manager upgraded to chart v1.15.0 in cert-manager at 14:30",
		"dev: homerun2-radio-pitcher v0.14.0 rolled out in homerun2 at 14:00",
		"dev: ui v1.0.0 deployed in homerun2 at 13:00",
	}
	if len(items) != len(want) {
		t.Fatalf("items = %+v", items)
	}
	for i, w := range want {
		if items[i].Message != w {
			t.Errorf("item %d: %q, want %q", i, items[i].Message, w)
		}
	}
	if !panel.Card4x6.Fits(items[1].Title) {
		t.Errorf("title does not fit: %q", items[1].Title)
	}

	// aired once
	if items, _ = r.Build(ctx, at, schedule.Params{"count": 5}); len(items) != 0 {
		t.Fatalf("repeated: %+v", items)
	}

	// rollouts of one namespace within 10 min share a card
	g := &fakeRollouts{name: "labul", list: []sources.Rollout{
		{ID: "s", Source: "labul", Kind: sources.KindDeployment, Namespace: "argocd", Name: "argocd-server", Version: "v3.6.2", Upgrade: true, At: at.Add(-time.Hour)},
		{ID: "r", Source: "labul", Kind: sources.KindDeployment, Namespace: "argocd", Name: "argocd-redis", Version: "8.6.4", Upgrade: true, At: at.Add(-time.Hour - 5*time.Minute)},
		{ID: "x", Source: "labul", Kind: sources.KindDeployment, Namespace: "argocd", Name: "argocd-dex", Version: "v2.46.1", Upgrade: true, At: at.Add(-3 * time.Hour)},
	}}
	rg := &Rollouts{Sources: []sources.RolloutSource{g}, Title: panel.Card4x6, MessageMax: 120}
	items, _ = rg.Build(ctx, at, schedule.Params{"count": 1})
	if len(items) != 1 || items[0].Message != "labul: 2 rollouts in argocd at 14:00: argocd-server v3.6.2, argocd-redis 8.6.4" || items[0].Title != panel.Card4x6.Fit("Deployed: 2 in argocd") {
		t.Fatalf("group = %+v", items)
	}
	// both are seen now; the older one is next
	if items, _ = rg.Build(ctx, at, schedule.Params{"count": 1}); len(items) != 1 || items[0].Message != "labul: argocd-dex v2.46.1 rolled out in argocd at 12:00" {
		t.Fatalf("after group = %+v", items)
	}

	// filters and within
	r2 := &Rollouts{Sources: []sources.RolloutSource{k, f}, Title: panel.Card4x6, MessageMax: 120}
	items, _ = r2.Build(ctx, at, schedule.Params{"count": 5, "kinds": []any{"deployment"}, "namespaces": "homerun2,old", "within": "12h"})
	if len(items) != 3 || items[2].Message != "dev: old deployed in old at 05:00" {
		t.Fatalf("filtered = %+v", items)
	}

	// every source down: an error
	r3 := &Rollouts{Sources: []sources.RolloutSource{broken}, Title: panel.Card4x6, MessageMax: 120}
	if _, err := r3.Build(ctx, at, nil); err == nil {
		t.Fatal("no error with every source down")
	}
	// none configured: silent
	if items, err := (&Rollouts{}).Build(ctx, at, nil); err != nil || len(items) != 0 {
		t.Fatalf("no sources: %v %v", items, err)
	}
}
