// Package scout reads the message analytics of homerun2-scout: the digest of
// a window (counts per severity, top systems, the previous window) and the
// number of messages with a tag. Every number the radio says about the day
// comes from here.
package scout

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Windows scout knows; each ends now and is rolling, not a calendar day.
const (
	Hourly = "hourly"
	Daily  = "daily"
	Weekly = "weekly"
)

// ValidWindow reports whether w is one of Hourly, Daily, Weekly.
func ValidWindow(w string) bool { return w == Hourly || w == Daily || w == Weekly }

// SystemCount is one system of a window.
type SystemCount struct {
	System string `json:"system"`
	Count  int64  `json:"count"`
	Alerts int64  `json:"alerts"`
}

// Summary is what one window held.
type Summary struct {
	Start      time.Time        `json:"start"`
	End        time.Time        `json:"end"`
	Total      int64            `json:"total"`
	Severities map[string]int64 `json:"severities"`
	TopSystems []SystemCount    `json:"topSystems"`
}

// Report is scout's digest of a window.
type Report struct {
	Schedule         string   `json:"schedule"`
	Current          Summary  `json:"current"`
	Previous         *Summary `json:"previous"`
	WindowIncomplete bool     `json:"windowIncomplete"`
}

// Client talks to scout's REST API.
type Client struct {
	URL   string // base URL, e.g. http://homerun2-scout.homerun2.svc
	Token string // scout's AUTH_TOKEN
	HTTP  *http.Client
}

// Digest returns the digest of the window (Hourly, Daily, Weekly) ending now.
func (c *Client) Digest(ctx context.Context, window string) (Report, error) {
	var r Report
	err := c.get(ctx, "/analytics/digest", url.Values{"schedule": {window}}, &r)
	return r, err
}

// Tagged returns how many messages of the window carry tag.
func (c *Client) Tagged(ctx context.Context, window, tag string) (int64, error) {
	var r struct {
		Total int64 `json:"total"`
	}
	err := c.get(ctx, "/analytics/messages", url.Values{"schedule": {window}, "tags": {tag}, "limit": {"1"}}, &r)
	return r.Total, err
}

func (c *Client) get(ctx context.Context, path string, q url.Values, v any) error {
	u := strings.TrimRight(c.URL, "/") + path + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
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
		return fmt.Errorf("scout: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("scout: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("scout %s: %s: %s", path, resp.Status, strings.TrimSpace(string(data)))
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("scout %s: decode: %w", path, err)
	}
	return nil
}
