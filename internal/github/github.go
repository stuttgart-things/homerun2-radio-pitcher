// Package github reads the releases an organization published, from its
// public events (GET /orgs/{org}/events): one request per refresh, so it
// stays within the 60 requests an hour GitHub allows without a token.
package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Release is one published release.
type Release struct {
	Repo string // without the org, e.g. homerun2-radio-pitcher
	Tag  string
	// Note is the first change of the release notes as plain text, empty
	// when the notes have none.
	Note string
	URL  string
	At   time.Time
}

// Client lists an organization's releases. Results are cached for CacheFor,
// so several slots in a row cost one request.
type Client struct {
	Org   string
	Token string // optional
	HTTP  *http.Client
	// BaseURL defaults to https://api.github.com.
	BaseURL string
	// CacheFor defaults to 10 minutes.
	CacheFor time.Duration
	// Pages is how many pages of 100 events to read at most (default 3).
	Pages int

	mu      sync.Mutex
	cached  []Release
	fetched time.Time
	since   time.Time
}

// Releases returns the published, non-draft, non-prerelease releases since
// since, newest first.
func (c *Client) Releases(ctx context.Context, since time.Time) ([]Release, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ttl := c.CacheFor
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	if c.cached != nil && time.Since(c.fetched) < ttl && !since.Before(c.since) {
		return after(c.cached, since), nil
	}
	rs, err := c.fetch(ctx, since)
	if err != nil {
		return nil, err
	}
	c.cached, c.fetched, c.since = rs, time.Now(), since
	return after(rs, since), nil
}

func after(rs []Release, since time.Time) []Release {
	out := []Release{}
	for _, r := range rs {
		if r.At.After(since) {
			out = append(out, r)
		}
	}
	return out
}

type event struct {
	Type      string    `json:"type"`
	CreatedAt time.Time `json:"created_at"`
	Repo      struct {
		Name string `json:"name"`
	} `json:"repo"`
	Payload struct {
		Action  string `json:"action"`
		Release struct {
			TagName     string    `json:"tag_name"`
			Body        string    `json:"body"`
			HTMLURL     string    `json:"html_url"`
			Draft       bool      `json:"draft"`
			Prerelease  bool      `json:"prerelease"`
			PublishedAt time.Time `json:"published_at"`
		} `json:"release"`
	} `json:"payload"`
}

func (c *Client) fetch(ctx context.Context, since time.Time) ([]Release, error) {
	base := strings.TrimRight(c.BaseURL, "/")
	if base == "" {
		base = "https://api.github.com"
	}
	pages := c.Pages
	if pages <= 0 {
		pages = 3
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	out := []Release{}
	seen := map[string]bool{}
	for page := 1; page <= pages; page++ {
		u := fmt.Sprintf("%s/orgs/%s/events?per_page=100&page=%d", base, url.PathEscape(c.Org), page)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", "homerun2-radio-pitcher")
		if c.Token != "" {
			req.Header.Set("Authorization", "Bearer "+c.Token)
		}
		resp, err := hc.Do(req)
		if err != nil {
			return nil, fmt.Errorf("github events: %w", err)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		_ = resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("github events: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("github events: %s", resp.Status)
		}
		var evs []event
		if err := json.Unmarshal(body, &evs); err != nil {
			return nil, fmt.Errorf("github events: %w", err)
		}
		for _, e := range evs {
			rel := e.Payload.Release
			if e.Type != "ReleaseEvent" || e.Payload.Action != "published" || rel.Draft || rel.Prerelease || rel.TagName == "" {
				continue
			}
			at := rel.PublishedAt
			if at.IsZero() {
				at = e.CreatedAt
			}
			key := e.Repo.Name + "@" + rel.TagName
			if seen[key] {
				continue
			}
			seen[key] = true
			_, repo, _ := strings.Cut(e.Repo.Name, "/")
			out = append(out, Release{Repo: repo, Tag: rel.TagName, Note: FirstNote(rel.Body), URL: rel.HTMLURL, At: at})
		}
		// events come newest first: a page that reaches back before since
		// or a short page is the last one needed
		if len(evs) < 100 || (len(evs) > 0 && evs[len(evs)-1].CreatedAt.Before(since)) {
			break
		}
	}
	return out, nil
}

var (
	mdLink    = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	refParens = regexp.MustCompile(`\s*\((?:#?\d+|[0-9a-f]{7,40})\)(?:\s*,?\s*\((?:#?\d+|[0-9a-f]{7,40})\))*`)
	scope     = regexp.MustCompile(`^\*\*([^*]+):\*\*\s*`)
	closes    = regexp.MustCompile(`(?i),?\s*(?:closes|fixes|resolves)\s+(?:[\w.-]+/[\w.-]+)?#\d+(?:\s*,?\s*(?:[\w.-]+/[\w.-]+)?#\d+)*`)
	spaces    = regexp.MustCompile(`\s+`)
)

// FirstNote is the first list entry of release notes (semantic-release:
// "* **scope:** text ([#12](…)) ([abc1234](…))") as plain text:
// "scope: text".
func FirstNote(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "* ") && !strings.HasPrefix(line, "- ") {
			continue
		}
		s := strings.TrimSpace(line[2:])
		s = mdLink.ReplaceAllString(s, "$1")
		s = refParens.ReplaceAllString(s, "")
		s = closes.ReplaceAllString(s, "")
		s = scope.ReplaceAllString(s, "$1: ")
		s = strings.NewReplacer("**", "", "`", "").Replace(s)
		return strings.TrimSpace(spaces.ReplaceAllString(s, " "))
	}
	return ""
}
