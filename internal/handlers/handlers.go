// Package handlers serves /health, /ready and /metrics.
package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/schedule"
)

// Build identifies the binary.
type Build struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

// Status is what /ready checks.
type Status struct {
	// Ready pings the delivery backend (Redis).
	Ready func(ctx context.Context) error
	// Schedule returns the schedule on air and the last load error.
	Schedule func() (*schedule.Schedule, error)
	// LastAired is when the last item went out.
	LastAired func() time.Time
}

// Metrics counts pitched items.
var Pitched = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "homerun2_radio_items_total",
	Help: "Programme items by segment and result (pitched, error).",
}, []string{"segment", "result"})

// Moderated counts the model's answers by kind (headline, fact, blurb,
// intro, time) and
// result (moderation.Result*): used, or why the fixed text aired instead.
var Moderated = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "homerun2_radio_moderation_total",
	Help: "Model calls by kind and result (used, rejected_number, rejected_name, rejected_length, rejected_empty, rejected_echo, error).",
}, []string{"kind", "result"})

func init() { prometheus.MustRegister(Pitched, Moderated) }

// ObserveModeration records one model call for the metrics.
func ObserveModeration(kind, result string) {
	Moderated.WithLabelValues(kind, result).Inc()
}

// Observe records one item for the metrics.
func Observe(seg string, err error) {
	result := "pitched"
	if err != nil {
		result = "error"
	}
	Pitched.WithLabelValues(seg, result).Inc()
}

// Mux returns the HTTP routes.
func Mux(b Build, s Status) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "version": b.Version, "commit": b.Commit, "date": b.Date})
	})
	mux.HandleFunc("GET /ready", func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{"status": "ready"}
		code := http.StatusOK

		sched, schedErr := s.Schedule()
		switch {
		case sched == nil:
			code = http.StatusServiceUnavailable
			body["schedule"] = errString(schedErr, "not loaded")
		case schedErr != nil:
			// on air with the last good version: ready, but say so
			body["schedule"] = "last good version on air: " + schedErr.Error()
		default:
			body["schedule"] = "ok"
			body["slots"] = len(sched.Clock)
		}

		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.Ready(ctx); err != nil {
			code = http.StatusServiceUnavailable
			body["redis"] = err.Error()
		} else {
			body["redis"] = "ok"
		}
		if t := s.LastAired(); !t.IsZero() {
			body["lastAired"] = t.Format(time.RFC3339)
		}
		if code != http.StatusOK {
			body["status"] = "not ready"
		}
		writeJSON(w, code, body)
	})
	mux.Handle("GET /metrics", promhttp.Handler())
	return mux
}

// Mount serves app (the UI) under basePath next to the root routes of root
// (/health, /ready, /metrics stay at the root, as the probes and the portal
// expect). Without a base path app takes every other path.
func Mount(basePath string, root *http.ServeMux, app http.Handler) http.Handler {
	if app == nil {
		return root
	}
	if basePath == "" {
		root.Handle("/", app)
		return root
	}
	toUI := http.RedirectHandler(basePath+"/", http.StatusTemporaryRedirect)
	root.Handle(basePath+"/", http.StripPrefix(basePath, app))
	root.Handle(basePath, toUI)
	root.Handle("GET /{$}", toUI)
	return root
}

func errString(err error, def string) string {
	if err != nil {
		return err.Error()
	}
	return def
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
