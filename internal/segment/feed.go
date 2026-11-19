package segment

import (
	"context"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/panel"
)

// Feed is a configured news feed.
type Feed struct {
	Name string
	URL  string
}

// ParseFeeds reads "name=url,name=url". An entry without a name is named
// after its host.
func ParseFeeds(s string) ([]Feed, error) {
	var out []Feed
	for _, e := range strings.Split(s, ",") {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		name, url, ok := strings.Cut(e, "=")
		if !ok {
			url, name = e, ""
		}
		name, url = strings.TrimSpace(name), strings.TrimSpace(url)
		if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			return nil, fmt.Errorf("feed %q: url must start with http:// or https://", e)
		}
		if name == "" {
			name = strings.SplitN(strings.TrimPrefix(strings.TrimPrefix(url, "https://"), "http://"), "/", 2)[0]
		}
		out = append(out, Feed{Name: name, URL: url})
	}
	return out, nil
}

// Headline is one entry of a feed.
type Headline struct {
	Feed      string
	ID        string // guid, id or link
	Link      string // the article, empty when the feed gives none
	Title     string
	Published time.Time // zero when the feed gives none
	// Description is the entry's text without markup, at most
	// descriptionMax characters; empty when the feed gives none.
	Description string
}

// descriptionMax caps a description: enough for a summary, short enough
// for a prompt.
const descriptionMax = 800

// rss and atom cover RSS 2.0 (and 0.9x) and Atom 1.0.
type rss struct {
	Items []struct {
		Title   string `xml:"title"`
		Link    string `xml:"link"`
		GUID    string `xml:"guid"`
		PubDate string `xml:"pubDate"`
		Date    string `xml:"http://purl.org/dc/elements/1.1/ date"`
		Desc    string `xml:"description"`
		Content string `xml:"http://purl.org/rss/1.0/modules/content/ encoded"`
	} `xml:"channel>item"`
}

type atom struct {
	Entries []struct {
		Title string `xml:"title"`
		ID    string `xml:"id"`
		Links []struct {
			Href string `xml:"href,attr"`
			Rel  string `xml:"rel,attr"`
		} `xml:"link"`
		Updated   string `xml:"updated"`
		Published string `xml:"published"`
		Summary   string `xml:"summary"`
		Content   string `xml:"content"`
	} `xml:"entry"`
}

// webLink keeps only http(s) links: the UI renders them as hrefs.
func webLink(s string) string {
	if strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://") {
		return s
	}
	return ""
}

// ParseFeed reads an RSS or Atom document.
func ParseFeed(feed string, r io.Reader) ([]Headline, error) {
	data, err := io.ReadAll(io.LimitReader(r, 8<<20))
	if err != nil {
		return nil, err
	}
	var root struct{ XMLName xml.Name }
	if err := xml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("feed %s: %w", feed, err)
	}
	var out []Headline
	switch strings.ToLower(root.XMLName.Local) {
	case "rss", "rdf":
		var doc rss
		if err := xml.Unmarshal(data, &doc); err != nil {
			return nil, fmt.Errorf("feed %s: %w", feed, err)
		}
		for _, it := range doc.Items {
			id := firstNonEmpty(it.GUID, it.Link, it.Title)
			out = append(out, Headline{Feed: feed, ID: id, Link: webLink(strings.TrimSpace(it.Link)), Title: cleanTitle(it.Title), Published: parseDate(firstNonEmpty(it.PubDate, it.Date)),
				Description: cleanDescription(firstNonEmpty(it.Desc, it.Content))})
		}
	case "feed":
		var doc atom
		if err := xml.Unmarshal(data, &doc); err != nil {
			return nil, fmt.Errorf("feed %s: %w", feed, err)
		}
		for _, e := range doc.Entries {
			link := ""
			for _, l := range e.Links {
				if l.Rel == "" || l.Rel == "alternate" {
					link = strings.TrimSpace(l.Href)
					break
				}
			}
			if link == "" && len(e.Links) > 0 {
				link = strings.TrimSpace(e.Links[0].Href)
			}
			out = append(out, Headline{Feed: feed, ID: firstNonEmpty(e.ID, link, e.Title), Link: webLink(link), Title: cleanTitle(e.Title), Published: parseDate(firstNonEmpty(e.Published, e.Updated)),
				Description: cleanDescription(firstNonEmpty(e.Summary, e.Content))})
		}
	default:
		return nil, fmt.Errorf("feed %s: neither RSS nor Atom (root element %q)", feed, root.XMLName.Local)
	}
	// drop entries without a title
	kept := out[:0]
	for _, h := range out {
		if h.Title != "" {
			kept = append(kept, h)
		}
	}
	return kept, nil
}

// FetchFeed downloads and parses a feed.
func FetchFeed(ctx context.Context, hc *http.Client, f Feed) ([]Headline, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "homerun2-radio-pitcher")
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml, text/xml")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("feed %s: %w", f.Name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("feed %s: %s", f.Name, resp.Status)
	}
	return ParseFeed(f.Name, resp.Body)
}

var tagRE = regexp.MustCompile(`<[^>]*>`)

// cleanTitle removes markup a feed may put into titles.
func cleanTitle(s string) string {
	s = tagRE.ReplaceAllString(s, "")
	return panel.Clean(html.UnescapeString(s))
}

// cleanDescription strips markup (escaped in the feed or not) and caps the
// length at a word boundary.
func cleanDescription(s string) string {
	s = html.UnescapeString(s) // escaped HTML first, so the tags show
	s = tagRE.ReplaceAllString(s, " ")
	s = strings.Join(strings.Fields(html.UnescapeString(s)), " ")
	s = panel.Clean(s)
	if r := []rune(s); len(r) > descriptionMax {
		s = string(r[:descriptionMax])
		if i := strings.LastIndexByte(s, ' '); i > descriptionMax/2 {
			s = s[:i]
		}
	}
	return s
}

var dateLayouts = []string{
	time.RFC1123Z, time.RFC1123, time.RFC3339, time.RFC3339Nano,
	"Mon, 2 Jan 2006 15:04:05 -0700", "Mon, 2 Jan 2006 15:04:05 MST",
	"2 Jan 2006 15:04:05 -0700", "2006-01-02T15:04:05Z0700", "2006-01-02",
}

func parseDate(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, l := range dateLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s = strings.TrimSpace(s); s != "" {
			return s
		}
	}
	return ""
}
