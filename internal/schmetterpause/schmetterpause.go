// Package schmetterpause reads the office's table-tennis counts from
// schmetterpause's GET /api/statistics (its ADR-0032): matches, sets,
// points, the longest set and who won most. Every number the radio says
// about the table comes from here.
package schmetterpause

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// SetScore is one set as it was played, home first.
type SetScore struct {
	Home int `json:"home"`
	Away int `json:"away"`
}

// PlayerScore is one player's record in the window.
type PlayerScore struct {
	DisplayName string `json:"display_name"`
	Won         int    `json:"won"`
	Lost        int    `json:"lost"`
}

// Statistics is the answer for one window. Counts are confirmed matches
// only; Truncated means schmetterpause's match cap cut into the window, so
// the counts are too low.
type Statistics struct {
	From        *time.Time    `json:"from"`
	Until       time.Time     `json:"until"`
	Truncated   bool          `json:"truncated"`
	Matches     int           `json:"matches"`
	Sets        int           `json:"sets"`
	Points      int           `json:"points"`
	Deuce       int           `json:"deuce"`
	Whitewashes int           `json:"whitewashes"`
	LongestSet  *SetScore     `json:"longest_set"`
	MostWins    []PlayerScore `json:"most_wins"`
}

// Client talks to schmetterpause.
type Client struct {
	URL   string // base URL, e.g. http://schmetterpause.schmetterpause-demo.svc
	Token string // schmetterpause's SP_STATISTICS_TOKEN
	HTTP  *http.Client
}

// Statistics returns the counts of the last days days.
func (c *Client) Statistics(ctx context.Context, days int) (Statistics, error) {
	var s Statistics
	u := strings.TrimRight(c.URL, "/") + "/api/statistics?" + url.Values{"days": {strconv.Itoa(days)}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return s, err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return s, fmt.Errorf("schmetterpause: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return s, fmt.Errorf("schmetterpause: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return s, fmt.Errorf("schmetterpause /api/statistics: %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, fmt.Errorf("schmetterpause /api/statistics: decode: %w", err)
	}
	return s, nil
}
