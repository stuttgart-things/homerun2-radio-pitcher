package schedule

import (
	"fmt"
	"strconv"
	"strings"
)

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

// validate checks the parameters every segment shares.
func (p Params) validate() error {
	if v, ok := p["count"]; ok {
		n := p.Int("count", -1)
		if n < 1 || n > 10 {
			return fmt.Errorf("count %v: must be a number from 1 to 10", v)
		}
	}
	return nil
}
