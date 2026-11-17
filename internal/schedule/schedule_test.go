package schedule

import (
	"os"
	"strings"
	"testing"
	"time"
)

const draft = `
clock:
  ":00": { segment: time-check, jingle: station-id.gif }
  ":05": { segment: news, count: 3 }
  ":15": { segment: infra-facts, count: 2 }
  ":30": { segment: time-check, style: random }
  ":35": { segment: tabletennis-stats }
  ":45": { segment: infra-facts, count: 2 }
  default: { segment: idle-clock, style: rotate }

programme:
  "07:00-10:00": { show: morning-show, extra: [weather, calendar] }
  "10:00-17:00": { show: daytime }
  "17:00-19:00": { show: after-work, extra: [daily-summary] }
  "19:00-07:00": { show: night, brightness: 20 }
`

func TestParseDraft(t *testing.T) {
	p, err := Parse(strings.NewReader(draft))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got, want := p.Minutes(), []int{0, 5, 15, 30, 35, 45}; !equalInts(got, want) {
		t.Fatalf("minutes = %v, want %v", got, want)
	}
	news := p.Clock[5]
	if news.Segment != "news" || news.Params.Int("count", 1) != 3 {
		t.Fatalf("news slot = %+v", news)
	}
	if got := p.Clock[0].Params.String("jingle", ""); got != "station-id.gif" {
		t.Fatalf("jingle = %q", got)
	}
	if p.Default == nil || p.Default.Segment != "idle-clock" {
		t.Fatalf("default = %+v", p.Default)
	}
	if len(p.Programme) != 4 {
		t.Fatalf("programme = %d shows", len(p.Programme))
	}
	night := p.Programme[3]
	if night.Show != "night" || night.Brightness == nil || *night.Brightness != 20 {
		t.Fatalf("night = %+v", night)
	}
	if got := strings.Join(p.Programme[0].Extra, ","); got != "weather,calendar" {
		t.Fatalf("extra = %q", got)
	}
}

func TestParseMinuteForms(t *testing.T) {
	p, err := Parse(strings.NewReader(`
clock:
  ":07": { segment: news }
  "08": { segment: news }
  9: { segment: news }
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Minutes(); !equalInts(got, []int{7, 8, 9}) {
		t.Fatalf("minutes = %v", got)
	}
}

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		"empty":            ``,
		"no slots":         "clock: {}\n",
		"bad minute":       "clock:\n  \":60\": { segment: news }\n",
		"not a minute":     "clock:\n  \"noon\": { segment: news }\n",
		"no segment":       "clock:\n  \":00\": { count: 2 }\n",
		"duplicate minute": "clock:\n  \":05\": { segment: news }\n  \"5\": { segment: news }\n",
		"bad count":        "clock:\n  \":05\": { segment: news, count: 0 }\n",
		"unknown key":      "clock:\n  \":05\": { segment: news }\nprogramm: {}\n",
		"slot not a map":   "clock:\n  \":05\": news\n",
		"bad window":       "clock:\n  \":05\": { segment: news }\nprogramme:\n  \"7-10\": { show: x }\n",
		"empty window":     "clock:\n  \":05\": { segment: news }\nprogramme:\n  \"07:00-07:00\": { show: x }\n",
		"no show":          "clock:\n  \":05\": { segment: news }\nprogramme:\n  \"07:00-10:00\": { extra: [x] }\n",
		"brightness range": "clock:\n  \":05\": { segment: news }\nprogramme:\n  \"07:00-10:00\": { show: x, brightness: 120 }\n",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(strings.NewReader(in)); err == nil {
				t.Fatalf("expected an error for %q", in)
			}
		})
	}
}

func TestParseReportsAllErrors(t *testing.T) {
	_, err := Parse(strings.NewReader("clock:\n  \":61\": { segment: a }\n  \":02\": { }\n"))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), ":61") || !strings.Contains(err.Error(), ":02") {
		t.Fatalf("error should name both slots: %v", err)
	}
}

func TestDue(t *testing.T) {
	p, err := Parse(strings.NewReader(draft))
	if err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Europe/Berlin")
	cases := []struct {
		hh, mm  int
		segment string
	}{
		{9, 0, "time-check"},
		{9, 5, "news"},
		{14, 15, "infra-facts"},
		{23, 30, "time-check"},
		{0, 45, "infra-facts"},
		{9, 1, ""},
		{9, 59, ""},
	}
	for _, c := range cases {
		at := time.Date(2026, 11, 17, c.hh, c.mm, 30, 0, loc)
		s, ok := p.Due(at)
		if c.segment == "" {
			if ok {
				t.Errorf("%02d:%02d: unexpected slot %q", c.hh, c.mm, s.Segment)
			}
			continue
		}
		if !ok || s.Segment != c.segment {
			t.Errorf("%02d:%02d: got %q (%v), want %q", c.hh, c.mm, s.Segment, ok, c.segment)
		}
	}
}

func TestShowAt(t *testing.T) {
	p, err := Parse(strings.NewReader(draft))
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"06:59": "night",
		"07:00": "morning-show",
		"09:59": "morning-show",
		"12:00": "daytime",
		"17:30": "after-work",
		"19:00": "night",
		"23:59": "night",
		"00:00": "night",
	}
	for at, want := range cases {
		tm, _ := time.Parse("15:04", at)
		s, ok := p.ShowAt(tm)
		if !ok || s.Show != want {
			t.Errorf("%s: got %q (%v), want %q", at, s.Show, ok, want)
		}
	}
}

func TestUnknown(t *testing.T) {
	p, err := Parse(strings.NewReader(draft))
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{"time-check": true, "news": true, "infra-facts": true}
	got := p.Unknown(func(s string) bool { return known[s] })
	if len(got) != 1 || got[0] != ":35 tabletennis-stats" {
		t.Fatalf("unknown = %v", got)
	}
}

func TestParams(t *testing.T) {
	p := Params{"count": 3, "f": 2.0, "s": "4", "list": []any{"a", " b "}, "csv": "x, y,,z", "name": "heise"}
	if p.Int("count", 1) != 3 || p.Int("f", 1) != 2 || p.Int("s", 1) != 4 || p.Int("missing", 7) != 7 {
		t.Fatal("Int")
	}
	if p.String("name", "") != "heise" || p.String("count", "") != "3" || p.String("x", "d") != "d" {
		t.Fatal("String")
	}
	if got := strings.Join(p.Strings("list"), "|"); got != "a|b" {
		t.Fatalf("list = %q", got)
	}
	if got := strings.Join(p.Strings("csv"), "|"); got != "x|y|z" {
		t.Fatalf("csv = %q", got)
	}
}

// The example shipped in the repository must stay valid.
func TestExampleFile(t *testing.T) {
	for _, path := range []string{"../../examples/schedule.yaml"} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if _, err := Load(path); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
