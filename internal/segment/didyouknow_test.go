package segment

import (
	"context"
	"errors"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/backstage"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/moderation"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/panel"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/schedule"
)

type fakeCatalog struct {
	entities []backstage.Entity
	err      error
	kinds    []string
}

func (f *fakeCatalog) Entities(_ context.Context, kinds []string) ([]backstage.Entity, error) {
	f.kinds = kinds
	return f.entities, f.err
}

func catalog() *fakeCatalog {
	return &fakeCatalog{entities: []backstage.Entity{
		{Kind: "Component", Name: "homerun2-scout", Type: "service", Description: "Analytics for homerun2 messages"},
		{Kind: "Component", Name: "machinery", Title: "Machinery", Type: "service", Description: "Fleet state of the lab's VMs"},
		{Kind: "System", Name: "homerun2", Description: "  "}, // no description: never picked
		// says nothing beyond the name, or deprecated: never picked (#32)
		{Kind: "Component", Name: "dave", Description: "dave"},
		{Kind: "Component", Name: "wled-mock", Title: "WLED Mock", Description: "WLED mock."},
		{Kind: "Component", Name: "vault-labda", Lifecycle: "Deprecated", Description: "RETIRED: old Vault in LabDA"},
		{Kind: "Component", Name: "docs-site", Type: "website", Description: "The team's docs"},
	}}
}

func dykSegment(f *fakeCatalog) *DidYouKnow {
	return &DidYouKnow{Catalog: f, Title: panel.Card4x6, MessageMax: 120, Rand: rand.New(rand.NewPCG(1, 2))}
}

func TestDidYouKnowAirsEachEntryOncePerWindow(t *testing.T) {
	f := catalog()
	d := dykSegment(f)
	seen := map[string]bool{}
	for i := range 3 {
		items, err := d.Build(context.Background(), now.Add(time.Duration(i)*time.Hour), nil)
		if err != nil || len(items) != 1 {
			t.Fatalf("round %d: %+v, %v", i, items, err)
		}
		it := items[0]
		if !strings.HasPrefix(it.Title, "Did you know?") || !panel.Card4x6.Fits(it.Title) {
			t.Errorf("title %q", it.Title)
		}
		if seen[it.Message] {
			t.Fatalf("repeated within the window: %q", it.Message)
		}
		seen[it.Message] = true
	}
	for _, want := range []string{
		"homerun2-scout: Analytics for homerun2 messages",
		"Machinery: Fleet state of the lab's VMs",
		"docs-site: The team's docs",
	} {
		if !seen[want] {
			t.Errorf("never aired: %q (aired %v)", want, seen)
		}
	}
	// everything aired: nothing to say, no error
	items, err := d.Build(context.Background(), now.Add(4*time.Hour), nil)
	if err != nil || len(items) != 0 {
		t.Fatalf("exhausted: %+v, %v", items, err)
	}
	// after SeenFor (7 days) they come back
	items, _ = d.Build(context.Background(), now.Add(8*24*time.Hour), nil)
	if len(items) != 1 {
		t.Fatalf("after the window: %+v", items)
	}
	if strings.Join(f.kinds, ",") != "component,system,api,resource" {
		t.Fatalf("default kinds %v", f.kinds)
	}
}

func TestDidYouKnowParamsAndPreview(t *testing.T) {
	f := catalog()
	d := dykSegment(f)
	p := schedule.Params{"kinds": []any{"component"}, "types": []any{"Website"}, "count": 3}
	// a preview does not use up the entry
	for range 2 {
		items, err := d.Build(Preview(context.Background()), now, p)
		if err != nil || len(items) != 1 || items[0].Message != "docs-site: The team's docs" {
			t.Fatalf("preview: %+v, %v", items, err)
		}
	}
	if strings.Join(f.kinds, ",") != "component" {
		t.Fatalf("kinds %v", f.kinds)
	}
}

func TestDidYouKnowErrorsAndModeration(t *testing.T) {
	if _, err := (&DidYouKnow{}).Build(context.Background(), now, nil); err == nil || !strings.Contains(err.Error(), "RADIO_BACKSTAGE_URL") {
		t.Fatalf("unconfigured: %v", err)
	}
	if _, err := dykSegment(&fakeCatalog{err: errors.New("401")}).Build(context.Background(), now, nil); err == nil {
		t.Fatal("catalog error swallowed")
	}

	f := &fakeCatalog{entities: []backstage.Entity{{Kind: "Component", Name: "homerun2-scout", Description: "Analytics for homerun2 messages"}}}
	d := dykSegment(f)
	d.Moderator = &moderation.Moderator{Client: answer("Did you know the scout keeps 30 days of messages?")}
	items, _ := d.Build(context.Background(), now, nil)
	if items[0].Message != "homerun2-scout: Analytics for homerun2 messages" {
		t.Fatalf("invented answer aired: %q", items[0].Message)
	}
	d = dykSegment(f)
	d.Moderator = &moderation.Moderator{Client: answer("Did you know homerun2-scout counts every homerun2 message?")}
	items, _ = d.Build(context.Background(), now, nil)
	if items[0].Message != "Did you know homerun2-scout counts every homerun2 message?" {
		t.Fatalf("good answer rejected: %q", items[0].Message)
	}
}
