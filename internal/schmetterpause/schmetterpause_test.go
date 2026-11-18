package schmetterpause

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStatistics(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/statistics" || r.URL.Query().Get("days") != "7" {
			t.Errorf("request %s", r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, `{"error":"a bearer token is required"}`, http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"until":"2026-11-18T12:00:00Z","truncated":false,"matches":12,"sets":30,
			"points":612,"deuce":4,"whitewashes":2,"longest_set":{"home":14,"away":12},
			"most_wins":[{"id":"x","display_name":"Cem","won":5,"lost":1}]}`))
	}))
	defer srv.Close()

	c := &Client{URL: srv.URL + "/", Token: "secret"}
	s, err := c.Statistics(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if s.Matches != 12 || s.Points != 612 || s.LongestSet == nil || s.LongestSet.Home != 14 ||
		len(s.MostWins) != 1 || s.MostWins[0].DisplayName != "Cem" || s.MostWins[0].Won != 5 {
		t.Errorf("decoded %+v", s)
	}

	c.Token = "wrong"
	if _, err := c.Statistics(context.Background(), 7); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("wrong token: err %v, want a 401", err)
	}
}
