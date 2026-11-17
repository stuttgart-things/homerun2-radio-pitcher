package segment

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/moderation"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/panel"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/schedule"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/scout"
)

type fakeScout struct {
	report scout.Report
	err    error
	tags   map[string]int64
	window string
}

func (f *fakeScout) Digest(_ context.Context, w string) (scout.Report, error) {
	f.window = w
	return f.report, f.err
}

func (f *fakeScout) Tagged(_ context.Context, _ string, tag string) (int64, error) {
	n, ok := f.tags[tag]
	if !ok {
		return 0, errors.New("no such tag")
	}
	return n, nil
}

func dayReport() scout.Report {
	return scout.Report{
		Current: scout.Summary{Total: 312, Severities: map[string]int64{"error": 3, "info": 309},
			TopSystems: []scout.SystemCount{{System: "kubernetes", Count: 120, Alerts: 2}}},
		Previous: &scout.Summary{Total: 290},
	}
}

func summarySegment(f *fakeScout) *DailySummary {
	return &DailySummary{Scout: f, Title: panel.Card4x6, MessageMax: 120}
}

func TestDailySummary(t *testing.T) {
	f := &fakeScout{report: dayReport(), tags: map[string]int64{"release": 4}}
	items, err := summarySegment(f).Build(context.Background(), now, schedule.Params{"tags": []any{"release", "missing"}, "count": 5})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range items {
		got = append(got, it.Title+" | "+it.Message)
	}
	want := []string{
		"24h: 312 msgs, 3 err | In the last 24 h: 312 messages, 3 errors, 0 critical (before: 290)",
		"24h: 4 release | In the last 24 h: 4 messages tagged release",
		"Busiest: kubernetes | In the last 24 h: kubernetes sent 120 messages, 2 of them alerts",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("items:\n%s", strings.Join(got, "\n"))
	}
	if f.window != scout.Daily {
		t.Fatalf("window = %s", f.window)
	}
	for _, it := range items {
		if !panel.Card4x6.Fits(it.Title) {
			t.Errorf("title does not fit: %q", it.Title)
		}
	}
}

func TestDailySummarySkipsOwnSystem(t *testing.T) {
	r := dayReport()
	r.Current.TopSystems = append([]scout.SystemCount{{System: "radio", Count: 200}}, r.Current.TopSystems...)
	s := summarySegment(&fakeScout{report: r})
	s.Own = "radio"
	items, _ := s.Build(context.Background(), now, nil)
	if last := items[len(items)-1]; last.Title != "Busiest: kubernetes" {
		t.Fatalf("busiest = %+v", last)
	}
}

func TestDailySummaryVariants(t *testing.T) {
	// default count 3, weekly, incomplete window: "at least", no comparison
	r := dayReport()
	r.WindowIncomplete = true
	r.Current.TopSystems = nil
	f := &fakeScout{report: r}
	items, err := summarySegment(f).Build(context.Background(), now, schedule.Params{"window": "weekly", "count": 1})
	if err != nil || len(items) != 1 {
		t.Fatalf("items = %+v, %v", items, err)
	}
	if items[0].Message != "In the last 7 days: at least 312 messages, 3 errors, 0 critical" || !strings.HasPrefix(items[0].Title, "7d:") {
		t.Fatalf("item = %+v", items[0])
	}

	if _, err := summarySegment(f).Build(context.Background(), now, schedule.Params{"window": "monthly"}); err == nil {
		t.Fatal("bad window accepted")
	}
	if _, err := (&DailySummary{}).Build(context.Background(), now, nil); err == nil || !strings.Contains(err.Error(), "RADIO_SCOUT_URL") {
		t.Fatalf("unconfigured: %v", err)
	}
	f.err = errors.New("503")
	if _, err := summarySegment(f).Build(context.Background(), now, nil); err == nil {
		t.Fatal("scout error swallowed")
	}
}

func TestDailySummaryModeration(t *testing.T) {
	f := &fakeScout{report: dayReport()}
	s := summarySegment(f)
	// the model invents a number: the fixed text airs
	s.Moderator = &moderation.Moderator{Client: answer("Busy day: 400 messages and 3 errors.")}
	items, _ := s.Build(context.Background(), now, schedule.Params{"count": 1})
	if items[0].Message != "In the last 24 h: 312 messages, 3 errors, 0 critical (before: 290)" {
		t.Fatalf("invented number aired: %q", items[0].Message)
	}
}
