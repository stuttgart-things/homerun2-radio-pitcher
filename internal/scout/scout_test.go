package scout

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClient(t *testing.T) {
	var auth, query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, query = r.Header.Get("Authorization"), r.URL.RawQuery
		switch r.URL.Path {
		case "/analytics/digest":
			_, _ = w.Write([]byte(`{"schedule":"daily","message":{"title":"x"},"current":{"total":312,"severities":{"error":3,"info":309},"topSystems":[{"system":"kubernetes","count":120,"alerts":2}]},"previous":{"total":290}}`))
		case "/analytics/messages":
			_, _ = w.Write([]byte(`{"schedule":"daily","total":4,"omitted":3,"messages":[{}]}`))
		default:
			http.Error(w, `{"error":"index lacks timestamp_unix"}`, http.StatusServiceUnavailable)
		}
	}))
	defer srv.Close()
	c := &Client{URL: srv.URL + "/", Token: "tok"}

	r, err := c.Digest(context.Background(), Daily)
	if err != nil {
		t.Fatal(err)
	}
	if r.Current.Total != 312 || r.Current.Severities["error"] != 3 || r.Previous == nil || r.Previous.Total != 290 || r.Current.TopSystems[0].Alerts != 2 {
		t.Fatalf("report = %+v", r)
	}
	if auth != "Bearer tok" || query != "schedule=daily" {
		t.Fatalf("auth %q query %q", auth, query)
	}
	n, err := c.Tagged(context.Background(), Weekly, "release")
	if err != nil || n != 4 || !strings.Contains(query, "tags=release") || !strings.Contains(query, "schedule=weekly") {
		t.Fatalf("tagged = %d, %v, %q", n, err, query)
	}
	c.URL = srv.URL + "/broken"
	if _, err := c.Digest(context.Background(), Daily); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("err = %v", err)
	}
	if ValidWindow("monthly") || !ValidWindow(Hourly) {
		t.Fatal("ValidWindow")
	}
}
