package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/schedule"
)

func get(t *testing.T, mux *http.ServeMux, path string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Code, rec.Body.String()
}

func TestHealthAndReady(t *testing.T) {
	s, _ := schedule.Parse(strings.NewReader("clock:\n  \":00\": { segment: time-check }\n"))
	var redisErr, schedErr error
	var sched = s
	mux := Mux(Build{Version: "v0.1.0"}, Status{
		Ready:     func(context.Context) error { return redisErr },
		Schedule:  func() (*schedule.Schedule, error) { return sched, schedErr },
		LastAired: func() time.Time { return time.Time{} },
	})

	if code, body := get(t, mux, "/health"); code != 200 || !strings.Contains(body, "v0.1.0") {
		t.Fatalf("health: %d %s", code, body)
	}
	if code, body := get(t, mux, "/ready"); code != 200 || !strings.Contains(body, `"slots":1`) {
		t.Fatalf("ready: %d %s", code, body)
	}

	schedErr = errors.New("bad yaml")
	if code, body := get(t, mux, "/ready"); code != 200 || !strings.Contains(body, "last good version") {
		t.Fatalf("ready with stale schedule: %d %s", code, body)
	}

	redisErr = errors.New("connection refused")
	if code, _ := get(t, mux, "/ready"); code != 503 {
		t.Fatalf("ready without redis: %d", code)
	}

	redisErr, sched = nil, nil
	if code, _ := get(t, mux, "/ready"); code != 503 {
		t.Fatalf("ready without schedule: %d", code)
	}

	Observe("news", nil)
	if code, body := get(t, mux, "/metrics"); code != 200 || !strings.Contains(body, `homerun2_radio_items_total{result="pitched",segment="news"} 1`) {
		t.Fatalf("metrics: %d", code)
	}
}
