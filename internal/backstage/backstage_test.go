package backstage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEntitiesFollowsTheCursor(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/catalog/entities/by-query" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, `{"error":{"name":"AuthenticationError"}}`, http.StatusUnauthorized)
			return
		}
		q := r.URL.Query()
		calls = append(calls, r.URL.RawQuery)
		if q.Get("fields") == "" {
			t.Errorf("fields missing: %s", r.URL.RawQuery)
		}
		var body any
		switch q.Get("cursor") {
		case "":
			if got := strings.Join(q["filter"], "|"); got != "kind=component|kind=system" {
				t.Errorf("filters %q", got)
			}
			body = map[string]any{
				"items": []any{map[string]any{"kind": "Component",
					"metadata": map[string]any{"name": "scout", "namespace": "default", "description": "Analytics"},
					"spec":     map[string]any{"type": "service", "lifecycle": "production"}}},
				"pageInfo": map[string]any{"nextCursor": "c2"},
			}
		case "c2":
			body = map[string]any{
				"items":    []any{map[string]any{"kind": "System", "metadata": map[string]any{"name": "homerun2", "title": "homerun2"}}},
				"pageInfo": map[string]any{},
			}
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()

	c := &Client{URL: srv.URL + "/", Token: "tok"}
	es, err := c.Entities(context.Background(), []string{"component", "system"})
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 2 || len(calls) != 2 {
		t.Fatalf("entities %+v, calls %v", es, calls)
	}
	if es[0].Ref() != "component:default/scout" || es[0].Type != "service" || es[0].Lifecycle != "production" || es[0].Label() != "scout" {
		t.Errorf("first %+v", es[0])
	}
	if es[1].Ref() != "system:default/homerun2" {
		t.Errorf("second ref %q", es[1].Ref())
	}

	c.Token = "wrong"
	if _, err := c.Entities(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("wrong token: %v", err)
	}
}
