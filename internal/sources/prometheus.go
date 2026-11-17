package sources

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/template"
)

// Query is one fact of a prometheus source: an instant PromQL query and the
// fixed texts it fills. Every sample of the result is one fact; an empty
// result says nothing (filter in PromQL, e.g. "> 0").
type Query struct {
	// ID names the fact, unique within the source.
	ID string `yaml:"id"`
	// Query is the PromQL expression, evaluated at the time the segment airs.
	Query string `yaml:"query"`
	// Title and Message are text/template strings with .Source, .Value
	// (formatted with Decimals), .Float and .Labels.
	Title   string `yaml:"title"`
	Message string `yaml:"message"`
	// Decimals rounds .Value; default 0.
	Decimals int `yaml:"decimals"`
	// Limit caps the facts of one query (samples beyond it are dropped);
	// default 10.
	Limit int `yaml:"limit"`
}

const defaultQueryLimit = 10

// queryData is what the templates of a Query see.
type queryData struct {
	Source string
	Value  string
	Float  float64
	Labels map[string]string
}

type compiledQuery struct {
	Query
	title, message *template.Template
}

func compileQuery(q Query) (compiledQuery, error) {
	c := compiledQuery{Query: q}
	if q.ID == "" || q.Query == "" || q.Message == "" {
		return c, errors.New("query needs id, query and message")
	}
	if q.Decimals < 0 || q.Decimals > 6 {
		return c, fmt.Errorf("query %s: decimals %d, want 0 to 6", q.ID, q.Decimals)
	}
	var err error
	// missingkey=zero: an absent label renders empty instead of "<no value>"
	if c.message, err = template.New("message").Option("missingkey=zero").Parse(q.Message); err != nil {
		return c, fmt.Errorf("query %s: message: %w", q.ID, err)
	}
	c.title = c.message // no title: the message is the title too
	if q.Title != "" {
		if c.title, err = template.New("title").Option("missingkey=zero").Parse(q.Title); err != nil {
			return c, fmt.Errorf("query %s: title: %w", q.ID, err)
		}
	}
	return c, nil
}

// Prometheus reports facts from PromQL queries against the HTTP API.
type Prometheus struct {
	SourceName string
	URL        string // base URL, e.g. http://prometheus.monitoring.svc:9090
	TokenFile  string // optional bearer token, read on every query (rotation)
	Queries    []compiledQuery
	Want       []string // query IDs; empty: all
	HTTP       *http.Client
	Log        *slog.Logger // failed queries while others succeed; nil: slog.Default()
}

// NewPrometheus builds a prometheus source from its config.
func NewPrometheus(c Config, hc *http.Client) (*Prometheus, error) {
	p := &Prometheus{SourceName: c.Name, URL: c.URL, TokenFile: c.TokenFile, Want: c.Facts, HTTP: hc}
	for _, q := range c.Queries {
		cq, err := compileQuery(q)
		if err != nil {
			return nil, err
		}
		p.Queries = append(p.Queries, cq)
	}
	return p, nil
}

func (p *Prometheus) Name() string { return p.SourceName }

// Facts runs every wanted query. A failing query is reported and the others
// still count; only when every query fails is the source an error.
func (p *Prometheus) Facts(ctx context.Context) ([]Fact, error) {
	var (
		facts []Fact
		errs  []error
		ran   int
	)
	for _, q := range p.Queries {
		if !wants(p.Want, q.ID) {
			continue
		}
		ran++
		samples, err := p.query(ctx, q.Query.Query)
		if err != nil {
			errs = append(errs, fmt.Errorf("query %s: %w", q.ID, err))
			continue
		}
		fs, err := p.render(q, samples)
		if err != nil {
			errs = append(errs, fmt.Errorf("query %s: %w", q.ID, err))
			continue
		}
		facts = append(facts, fs...)
	}
	if ran > 0 && len(errs) == ran {
		return nil, errors.Join(errs...)
	}
	if len(errs) > 0 {
		log := p.Log
		if log == nil {
			log = slog.Default()
		}
		for _, err := range errs {
			log.Warn("prometheus query failed, skipped", "source", p.SourceName, "error", err)
		}
	}
	return facts, nil
}

type sample struct {
	labels map[string]string
	value  float64
}

func (p *Prometheus) render(q compiledQuery, samples []sample) ([]Fact, error) {
	// stable order, so Limit drops the same samples every time
	sort.Slice(samples, func(i, j int) bool { return labelKey(samples[i].labels) < labelKey(samples[j].labels) })
	limit := q.Limit
	if limit <= 0 {
		limit = defaultQueryLimit
	}
	var facts []Fact
	for _, s := range samples {
		if len(facts) == limit {
			break
		}
		if math.IsNaN(s.value) || math.IsInf(s.value, 0) {
			continue
		}
		d := queryData{
			Source: p.SourceName,
			Value:  strconv.FormatFloat(s.value, 'f', q.Decimals, 64),
			Float:  s.value,
			Labels: s.labels,
		}
		title, err := execute(q.title, d)
		if err != nil {
			return nil, err
		}
		msg, err := execute(q.message, d)
		if err != nil {
			return nil, err
		}
		id := p.SourceName + "/" + q.ID
		if k := labelKey(s.labels); k != "" {
			id += "/" + k
		}
		facts = append(facts, Fact{ID: id, Source: p.SourceName, Title: title, Message: msg})
	}
	return facts, nil
}

func execute(t *template.Template, d queryData) (string, error) {
	var b bytes.Buffer
	if err := t.Execute(&b, d); err != nil {
		return "", err
	}
	return strings.TrimSpace(b.String()), nil
}

// labelKey is a stable identity of a label set ("a=1,b=2"), without __name__.
func labelKey(l map[string]string) string {
	keys := make([]string, 0, len(l))
	for k := range l {
		if k != "__name__" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + l[k]
	}
	return strings.Join(parts, ",")
}

// query runs an instant query and returns its samples (vector or scalar).
func (p *Prometheus) query(ctx context.Context, q string) ([]sample, error) {
	u := strings.TrimRight(p.URL, "/") + "/api/v1/query?" + url.Values{"query": {q}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if p.TokenFile != "" {
		tok, err := os.ReadFile(p.TokenFile)
		if err != nil {
			return nil, fmt.Errorf("token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(tok)))
	}
	hc := p.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}

	var r struct {
		Status    string `json:"status"`
		Error     string `json:"error"`
		ErrorType string `json:"errorType"`
		Data      struct {
			ResultType string          `json:"resultType"`
			Result     json.RawMessage `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	if r.Status != "success" {
		return nil, fmt.Errorf("%s: %s: %s", resp.Status, r.ErrorType, r.Error)
	}

	switch r.Data.ResultType {
	case "vector":
		var v []struct {
			Metric map[string]string `json:"metric"`
			Value  [2]any            `json:"value"`
		}
		if err := json.Unmarshal(r.Data.Result, &v); err != nil {
			return nil, err
		}
		out := make([]sample, 0, len(v))
		for _, s := range v {
			f, err := parseValue(s.Value)
			if err != nil {
				return nil, err
			}
			out = append(out, sample{labels: s.Metric, value: f})
		}
		return out, nil
	case "scalar":
		var v [2]any
		if err := json.Unmarshal(r.Data.Result, &v); err != nil {
			return nil, err
		}
		f, err := parseValue(v)
		if err != nil {
			return nil, err
		}
		return []sample{{value: f}}, nil
	default:
		return nil, fmt.Errorf("result type %q, want vector or scalar", r.Data.ResultType)
	}
}

// parseValue reads the [timestamp, "value"] pair of the Prometheus API.
func parseValue(v [2]any) (float64, error) {
	s, ok := v[1].(string)
	if !ok {
		return 0, fmt.Errorf("value %v is not a string", v[1])
	}
	return strconv.ParseFloat(s, 64)
}
