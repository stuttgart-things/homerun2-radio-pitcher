package schedule

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseParams reads "key=value" pairs (as `once` and the UI's preview take
// them). A value that is an integer becomes an int.
func ParseParams(kvs []string) (Params, error) {
	p := Params{}
	for _, kv := range kvs {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf("parameter %q, want key=value", kv)
		}
		if n, err := strconv.Atoi(v); err == nil {
			p[k] = n
		} else {
			p[k] = v
		}
	}
	return p, p.validate()
}

// Params are the parameters of a slot, every key besides `segment`.
type Params map[string]any

// Int returns the integer parameter key, or def when it is unset.
func (p Params) Int(key string, def int) int {
	v, ok := p[key]
	if !ok {
		return def
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(n)); err == nil {
			return i
		}
	}
	return def
}

// String returns the string parameter key, or def when it is unset.
func (p Params) String(key, def string) string {
	if v, ok := p[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
		return fmt.Sprint(v)
	}
	return def
}

// Bool returns a yes/no parameter: a YAML bool, or "true"/"yes"/"on" as
// `once` and the UI's preview pass it. Unset or anything else is false.
func (p Params) Bool(key string) bool {
	switch v := p[key].(type) {
	case bool:
		return v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "yes", "on":
			return true
		}
	}
	return false
}

// Strings returns a list parameter: a YAML list or a comma-separated string.
func (p Params) Strings(key string) []string {
	v, ok := p[key]
	if !ok {
		return nil
	}
	var out []string
	switch l := v.(type) {
	case []any:
		for _, e := range l {
			if s := strings.TrimSpace(fmt.Sprint(e)); s != "" {
				out = append(out, s)
			}
		}
	case string:
		for _, e := range strings.Split(l, ",") {
			if s := strings.TrimSpace(e); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

// Duration returns a Go duration parameter ("25s"), or def when it is
// unset or not a duration (validate reports that).
func (p Params) Duration(key string, def time.Duration) time.Duration {
	v, ok := p[key]
	if !ok {
		return def
	}
	d, err := time.ParseDuration(strings.TrimSpace(fmt.Sprint(v)))
	if err != nil {
		return def
	}
	return d
}

// Spacing bounds: below a second the catcher would get the cards at once,
// above two minutes a slot runs into the next.
const (
	minSpacing = time.Second
	maxSpacing = 2 * time.Minute
)

// validate checks the parameters every segment shares: count, and the
// director's spacing (pause between the slot's items) and card-tags (extra
// message tags, key=value).
func (p Params) validate() error {
	if v, ok := p["count"]; ok {
		n := p.Int("count", -1)
		if n < 1 || n > 10 {
			return fmt.Errorf("count %v: must be a number from 1 to 10", v)
		}
	}
	if v, ok := p["spacing"]; ok {
		d, err := time.ParseDuration(strings.TrimSpace(fmt.Sprint(v)))
		if err != nil || d < minSpacing || d > maxSpacing {
			return fmt.Errorf("spacing %v: must be a duration from %s to %s, e.g. 25s", v, minSpacing, maxSpacing)
		}
	}
	for _, t := range p.Strings("card-tags") {
		if k, v, ok := strings.Cut(t, "="); !ok || strings.TrimSpace(k) == "" || strings.TrimSpace(v) == "" || strings.Contains(t, " ") {
			return fmt.Errorf("card-tags: %q, want key=value without spaces", t)
		}
		if strings.HasPrefix(t, "segment=") {
			return fmt.Errorf("card-tags: %q, the director sets segment= itself", t)
		}
	}
	return nil
}
