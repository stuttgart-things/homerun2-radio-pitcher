package spots

import (
	"os"
	"strings"
	"testing"
	"time"
)

const pool = `
spots:
  - id: lunch
    title: "Lunch at 12"
    message: "Canteen today: pasta"
    weekdays: [mon, tue, wed, thu, fri]
    between: "11:00-12:00"
  - id: knuth
    kind: quote
    text: "Premature optimization is the root of all evil."
    author: Donald Knuth
  - id: kubecon
    title: "KubeCon soon"
    from: 2026-11-20
    until: 2026-12-02
  - id: late
    title: "Go home"
    between: "22:00-02:00"
  - id: off
    title: "Not now"
    weight: 0
`

func at(s string) time.Time {
	berlin, _ := time.LoadLocation("Europe/Berlin")
	t, err := time.ParseInLocation("2006-01-02 15:04", s, berlin)
	if err != nil {
		panic(err)
	}
	return t
}

func TestParseAndActive(t *testing.T) {
	ps, err := Parse([]byte(pool), 120)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Spot{}
	for _, p := range ps {
		byID[p.ID] = p
	}
	for _, c := range []struct {
		id, at string
		want   bool
	}{
		{"lunch", "2026-11-19 11:30", true},  // Thursday
		{"lunch", "2026-11-19 12:00", false}, // window end is exclusive
		{"lunch", "2026-11-21 11:30", false}, // Saturday
		{"knuth", "2026-11-21 03:00", true},
		{"kubecon", "2026-11-19 10:00", false},
		{"kubecon", "2026-11-20 00:00", true},
		{"kubecon", "2026-12-02 23:59", true}, // until is inclusive
		{"kubecon", "2026-12-03 00:00", false},
		{"late", "2026-11-19 23:30", true}, // crosses midnight
		{"late", "2026-11-19 01:59", true},
		{"late", "2026-11-19 02:00", false},
		{"off", "2026-11-19 10:00", false},
	} {
		if got := byID[c.id].Active(at(c.at)); got != c.want {
			t.Errorf("%s at %s: active %v, want %v", c.id, c.at, got, c.want)
		}
	}
	if title, msg := byID["knuth"].Card(); title != "Quote: Donald Knuth" || msg != "\u201cPremature optimization is the root of all evil.\u201d Donald Knuth" {
		t.Errorf("quote card = %q / %q", title, msg)
	}
	if title, msg := byID["kubecon"].Card(); title != "KubeCon soon" || msg != "KubeCon soon" {
		t.Errorf("text card without message = %q / %q", title, msg)
	}
}

func TestParseErrors(t *testing.T) {
	_, err := Parse([]byte(`
spots:
  - title: "no id"
  - { id: dup, title: a }
  - { id: dup, title: b }
  - { id: q, kind: quote, text: "only text" }
  - { id: mixed, title: t, author: someone }
  - { id: kind, kind: gif }
  - { id: long, title: t, message: "`+strings.Repeat("x", 121)+`" }
  - { id: day, title: t, weekdays: [funday] }
  - { id: window, title: t, between: "25:00-26:00" }
  - { id: dates, title: t, from: 2026-12-02, until: 2026-11-20 }
  - { id: weight, title: t, weight: -1 }
`), 120)
	for _, want := range []string{"spot 1: no id", `"dup": id given twice`, "needs text and author", "belong to quote spots", `kind "gif"`, "121 characters", `weekday "funday"`, `between "25:00-26:00"`, "is after until", "weight -1"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("errors lack %q:\n%v", want, err)
		}
	}
	if _, err := Parse([]byte("spots:\n  - { id: a, title: t, colour: red }\n"), 120); err == nil {
		t.Error("unknown key accepted")
	}
	if _, err := Parse(nil, 120); err == nil {
		t.Error("empty file accepted")
	}
}

func TestExampleFile(t *testing.T) {
	data, err := os.ReadFile("../../examples/spots.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(data, 120); err != nil {
		t.Fatalf("examples/spots.yaml: %v", err)
	}
}

func TestWhen(t *testing.T) {
	ps, err := Parse([]byte(`spots:
  - {id: a, title: A}
  - {id: b, title: B, weekdays: [mon, fri], between: "11:00-12:00"}
  - {id: c, title: C, from: "2026-11-20", until: "2026-12-02"}
  - {id: d, title: D, until: "2026-12-02"}
`), 100)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"always", "mon, fri · 11:00-12:00", "2026-11-20 – 2026-12-02", "until 2026-12-02"}
	for i, s := range ps {
		if got := s.When(); got != want[i] {
			t.Errorf("%s: When() = %q, want %q", s.ID, got, want[i])
		}
	}
}
