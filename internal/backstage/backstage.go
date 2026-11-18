// Package backstage reads entities from the Backstage catalog API
// (GET /api/catalog/entities/by-query): kind, name, title and description,
// for the did-you-know segment.
package backstage

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Entity is the part of a catalog entity the radio needs.
type Entity struct {
	Kind        string
	Namespace   string
	Name        string
	Title       string
	Description string
	Type        string // spec.type, e.g. service, website
}

// Ref is the entity reference, kind:namespace/name in lower case.
func (e Entity) Ref() string {
	ns := e.Namespace
	if ns == "" {
		ns = "default"
	}
	return strings.ToLower(e.Kind + ":" + ns + "/" + e.Name)
}

// Label is how the entity is called on air: its title, else its name.
func (e Entity) Label() string {
	if e.Title != "" {
		return e.Title
	}
	return e.Name
}

// Client talks to the Backstage backend.
type Client struct {
	URL   string // backend base URL, e.g. https://backstage.example.com
	Token string // a static external access token (backend.auth.externalAccess)
	HTTP  *http.Client
	// Max caps how many entities one call reads (default 1000).
	Max int
}

const pageSize = 250

type rawEntity struct {
	Kind     string `json:"kind"`
	Metadata struct {
		Namespace   string `json:"namespace"`
		Name        string `json:"name"`
		Title       string `json:"title"`
		Description string `json:"description"`
	} `json:"metadata"`
	Spec struct {
		Type string `json:"type"`
	} `json:"spec"`
}

type page struct {
	Items    []rawEntity `json:"items"`
	PageInfo struct {
		NextCursor string `json:"nextCursor"`
	} `json:"pageInfo"`
}

// Entities returns the entities of the given kinds (any kind when empty),
// following the cursor up to Max.
func (c *Client) Entities(ctx context.Context, kinds []string) ([]Entity, error) {
	max := c.Max
	if max <= 0 {
		max = 1000
	}
	q := url.Values{
		"fields": {"kind,metadata.namespace,metadata.name,metadata.title,metadata.description,spec.type"},
		"limit":  {strconv.Itoa(min(pageSize, max))},
	}
	for _, k := range kinds {
		q.Add("filter", "kind="+k) // several filters: any of them
	}

	var out []Entity
	for {
		var p page
		if err := c.get(ctx, q, &p); err != nil {
			return nil, err
		}
		for _, r := range p.Items {
			out = append(out, Entity{
				Kind: r.Kind, Namespace: r.Metadata.Namespace, Name: r.Metadata.Name,
				Title: r.Metadata.Title, Description: r.Metadata.Description, Type: r.Spec.Type,
			})
		}
		if p.PageInfo.NextCursor == "" || len(out) >= max || len(p.Items) == 0 {
			break
		}
		// with a cursor, the filters live in the cursor; fields and limit
		// are read from every request
		q = url.Values{"cursor": {p.PageInfo.NextCursor}, "limit": q["limit"], "fields": q["fields"]}
	}
	if len(out) > max {
		out = out[:max]
	}
	return out, nil
}

func (c *Client) get(ctx context.Context, q url.Values, v any) error {
	u := strings.TrimRight(c.URL, "/") + "/api/catalog/entities/by-query?" + q.Encode()
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
		return fmt.Errorf("backstage: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("backstage: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(data))
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return fmt.Errorf("backstage catalog: %s: %s", resp.Status, msg)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("backstage catalog: decode: %w", err)
	}
	return nil
}
