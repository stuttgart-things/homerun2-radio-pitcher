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
	news := p.Clock[5][0]
	if news.Segment != "news" || news.Params.Int("count", 1) != 3 {
		t.Fatalf("news slot = %+v", news)
	}
	if got := p.Clock[0][0].Params.String("jingle", ""); got != "station-id.gif" {
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
	if got := extraNames(p.Programme[0].Extra); got != "weather,calendar" {
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
		if got := segments(p.Due(at)); got != c.segment {
			t.Errorf("%02d:%02d: got %q, want %q", c.hh, c.mm, got, c.segment)
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
	want := ":35 tabletennis-stats|07:00-10:00 extra weather|07:00-10:00 extra calendar|17:00-19:00 extra daily-summary"
	if strings.Join(got, "|") != want {
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
	if got := strings.Join(p.Strings("count"), "|"); got != "3" { // ids=7 parses as a number
		t.Fatalf("number = %q", got)
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

const shows = `
clock:
  ":00": { segment: time-check }
  ":15": { segment: infra-facts }
clocks:
  morning:
    ":00": { segment: time-check }
    ":05": { segment: news, count: 3 }
    ":30": { segment: news }
  night: {}
programme:
  "07:00-10:00": { show: morning-show, clock: morning, extra: [weather] }
  "10:00-17:00": { show: daytime }
  "17:00-19:00":
    show: after-work
    extra:
      - { segment: daily-summary, at: "17:30", count: 1 }
  "22:00-06:00": { show: night, clock: night }
`

func TestDueWithShows(t *testing.T) {
	p, err := Parse(strings.NewReader(shows))
	if err != nil {
		t.Fatal(err)
	}
	for at, want := range map[string]string{
		"07:00": "weather,time-check", // extra at window start, then the show's clock
		"07:05": "news",               // morning clock
		"08:15": "",                   // not in the morning clock
		"12:15": "infra-facts",        // daytime: default clock
		"17:00": "time-check",         // extra has its own time
		"17:30": "daily-summary",      // ... here, the default clock has no :30
		"18:30": "",                   // once per window
		"23:00": "",                   // night: empty clock, silence
		"03:15": "",                   // night crosses midnight
		"06:15": "infra-facts",        // between shows: default clock
		"20:00": "time-check",         // between shows
	} {
		tm, _ := time.Parse("15:04", at)
		if got := segments(p.Due(tm)); got != want {
			t.Errorf("%s: got %q, want %q", at, got, want)
		}
	}
	e := p.Programme[2].Extra[0]
	if !e.HasAt || e.At.String() != "17:30" || e.Params.Int("count", 0) != 1 || e.Params["at"] != nil {
		t.Fatalf("extra = %+v", e)
	}
}

func TestProgrammeErrors(t *testing.T) {
	_, err := Parse(strings.NewReader(`
clock:
  ":00": { segment: time-check }
clocks:
  morning:
    ":61": { segment: news }
programme:
  "07:00-10:00": { show: a, clock: missing }
  "09:00-11:00": { show: b, extra: [{ segment: x, at: "12:00" }] }
  "12:00-13:00": { show: c, extra: [{ segment: y, at: "noon" }] }
  "14:00-15:00": { show: d, extra: [{ count: 2 }] }
`))
	if err == nil {
		t.Fatal("bad programme accepted")
	}
	for _, want := range []string{
		`clocks "morning" ":61"`,
		`clock "missing" is not in clocks`,
		`"09:00-11:00" overlaps "07:00-10:00" at 09:00`,
		`extra x at 12:00 is outside the window`,
		`at "noon"`,
		`extra without segment`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
}

func TestNextAndClockAt(t *testing.T) {
	p, err := Parse(strings.NewReader(shows))
	if err != nil {
		t.Fatal(err)
	}
	all := func(string) bool { return true }
	for from, want := range map[string]string{
		"07:00": "07:05 news",               // morning clock
		"06:59": "07:00 weather,time-check", // extra + clock at the window start
		"22:30": "06:00 time-check",         // night is silent, then the default clock
		"17:20": "17:30 daily-summary",      // extra with its own time
	} {
		tm, _ := time.Parse("15:04", from)
		at, slots, ok := p.Next(tm, all)
		if got := at.Format("15:04") + " " + segments(slots); !ok || got != want {
			t.Errorf("Next(%s) = %q, want %q", from, got, want)
		}
	}
	// unknown segments are passed over
	tm, _ := time.Parse("15:04", "06:59")
	at, slots, _ := p.Next(tm, func(s string) bool { return s != "weather" })
	if got := at.Format("15:04") + " " + segments(slots); got != "07:00 time-check" {
		t.Errorf("Next with unknown = %q", got)
	}
	if name, slots := p.ClockAt(tm); name != "default" || len(slots) != 2 {
		t.Errorf("ClockAt(06:59) = %s %d", name, len(slots))
	}
	tm, _ = time.Parse("15:04", "23:00")
	if name, slots := p.ClockAt(tm); name != "night" || len(slots) != 0 {
		t.Errorf("ClockAt(23:00) = %s %d", name, len(slots))
	}
	silent, _ := Parse(strings.NewReader("clock:\n  \":00\": { segment: x }\n"))
	if _, _, ok := silent.Next(tm, func(string) bool { return false }); ok {
		t.Error("Next found a slot although nothing is known")
	}
}

func TestParseParams(t *testing.T) {
	p, err := ParseParams([]string{"count=2", "feeds=cncf,kubernetes"})
	if err != nil || p.Int("count", 0) != 2 || strings.Join(p.Strings("feeds"), "|") != "cncf|kubernetes" {
		t.Fatalf("p = %v, %v", p, err)
	}
	for _, bad := range [][]string{{"count"}, {"=x"}, {"count=0"}} {
		if _, err := ParseParams(bad); err == nil {
			t.Errorf("ParseParams(%v) accepted", bad)
		}
	}
}

func segments(slots []Slot) string {
	names := make([]string, len(slots))
	for i, s := range slots {
		names[i] = s.Segment
	}
	return strings.Join(names, ",")
}

func extraNames(es []Extra) string {
	names := make([]string, len(es))
	for i, e := range es {
		names[i] = e.Segment
	}
	return strings.Join(names, ",")
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

func TestParseBlock(t *testing.T) {
	p, err := Parse(strings.NewReader(`
clock:
  ":00":
    - { segment: time-check, casual: true }
    - { segment: news, count: 6, mix: true, spacing: 25s, card-tags: [show=news] }
  ":15": { segment: infra-facts }
`))
	if err != nil {
		t.Fatal(err)
	}
	b := p.Clock[0]
	if len(b) != 2 || b.Segments() != "time-check, news" || b[1].Params.Duration("spacing", 0) != 25*time.Second {
		t.Fatalf("block = %+v", b)
	}
	due := p.Due(time.Date(2026, 11, 17, 15, 0, 0, 0, time.UTC))
	if len(due) != 2 || due[0].Segment != "time-check" || due[1].Segment != "news" {
		t.Fatalf("due = %+v", due)
	}
	if got := p.Unknown(func(s string) bool { return s != "news" }); len(got) != 1 || got[0] != ":00 news" {
		t.Fatalf("unknown = %v", got)
	}
}

func TestParseBlockErrors(t *testing.T) {
	for name, doc := range map[string]string{
		"empty list":        "clock:\n  \":00\": []\n",
		"no segment":        "clock:\n  \":00\":\n    - { segment: news }\n    - { count: 2 }\n",
		"default list":      "clock:\n  \":05\": { segment: news }\n  default:\n    - { segment: idle-clock }\n    - { segment: idle-clock }\n",
		"bad spacing":       "clock:\n  \":00\": { segment: news, spacing: 500ms }\n",
		"spacing not dur":   "clock:\n  \":00\": { segment: news, spacing: long }\n",
		"bad tag":           "clock:\n  \":00\": { segment: news, card-tags: [show] }\n",
		"tag with space":    "clock:\n  \":00\": { segment: news, card-tags: [\"show=the news\"] }\n",
		"segment tag":       "clock:\n  \":00\": { segment: news, card-tags: [segment=x] }\n",
		"bad count in list": "clock:\n  \":00\":\n    - { segment: time-check }\n    - { segment: news, count: 0 }\n",
	} {
		if _, err := Parse(strings.NewReader(doc)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
