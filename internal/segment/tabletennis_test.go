package segment

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/moderation"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/panel"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/schedule"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/schmetterpause"
)

type fakeTable struct {
	stats schmetterpause.Statistics
	err   error
	days  int
}

func (f *fakeTable) Statistics(_ context.Context, days int) (schmetterpause.Statistics, error) {
	f.days = days
	return f.stats, f.err
}

func weekAtTheTable() schmetterpause.Statistics {
	return schmetterpause.Statistics{
		Matches: 12, Sets: 30, Points: 612, Deuce: 4, Whitewashes: 2,
		LongestSet: &schmetterpause.SetScore{Home: 14, Away: 12},
		MostWins: []schmetterpause.PlayerScore{
			{DisplayName: "Cem", Won: 5, Lost: 1},
			{DisplayName: "Anna", Won: 4, Lost: 2},
		},
	}
}

func tableSegment(f *fakeTable) *TabletennisStats {
	return &TabletennisStats{Source: f, Title: panel.Card4x6, MessageMax: 120}
}

func cards(items []Item) string {
	var out []string
	for _, it := range items {
		out = append(out, it.Title+" | "+it.Message)
	}
	return strings.Join(out, "\n")
}

func TestTabletennisStats(t *testing.T) {
	f := &fakeTable{stats: weekAtTheTable()}
	items, err := tableSegment(f).Build(context.Background(), now, schedule.Params{"count": 4})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"7d: 12 matches | Table tennis in the last 7 days: 12 matches, 30 sets, 612 points",
		"Longest set 14:12 | Longest set at the table in the last 7 days: 14:12",
		"Most wins: Cem | Most wins in the last 7 days: Cem, 5 won and 1 lost",
		"7d: 4 deuce sets | In the last 7 days 4 sets went to deuce and 2 matches ended without a set lost",
	}, "\n")
	if got := cards(items); got != want {
		t.Fatalf("items:\n%s", got)
	}
	if f.days != 7 {
		t.Fatalf("days = %d", f.days)
	}
	for _, it := range items {
		if !panel.Card4x6.Fits(it.Title) {
			t.Errorf("title does not fit: %q", it.Title)
		}
	}
}

func TestTabletennisStatsVariants(t *testing.T) {
	// a day, the cap cut in, a tie of two, default count 3
	s := weekAtTheTable()
	s.Matches, s.Truncated, s.LongestSet = 1, true, nil
	s.MostWins = []schmetterpause.PlayerScore{{DisplayName: "Cem", Won: 1}, {DisplayName: "Anna", Won: 1, Lost: 1}}
	items, err := tableSegment(&fakeTable{stats: s}).Build(context.Background(), now, schedule.Params{"days": 1})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"24h: 1 match | Table tennis in the last 24 h: at least 1 match, 30 sets, 612 points",
		"Most wins: Cem and Anna | Most wins in the last 24 h: Cem and Anna, 1 each",
		"24h: 4 deuce sets | In the last 24 h 4 sets went to deuce and 2 matches ended without a set lost",
	}, "\n")
	if got := cards(items); got != want {
		t.Fatalf("items:\n%s", got)
	}

	// a three-way tie names nobody
	s.MostWins = append(s.MostWins, schmetterpause.PlayerScore{DisplayName: "Bea", Won: 1})
	items, _ = tableSegment(&fakeTable{stats: s}).Build(context.Background(), now, schedule.Params{"count": 5})
	if strings.Contains(cards(items), "Most wins") {
		t.Fatalf("three-way tie aired:\n%s", cards(items))
	}

	// nothing played: nothing airs, no error
	items, err = tableSegment(&fakeTable{}).Build(context.Background(), now, nil)
	if err != nil || len(items) != 0 {
		t.Fatalf("empty week: %+v, %v", items, err)
	}

	if _, err := tableSegment(&fakeTable{}).Build(context.Background(), now, schedule.Params{"days": 0}); err == nil {
		t.Fatal("days 0 accepted")
	}
	if _, err := (&TabletennisStats{}).Build(context.Background(), now, nil); err == nil || !strings.Contains(err.Error(), "RADIO_SCHMETTERPAUSE_URL") {
		t.Fatalf("unconfigured: %v", err)
	}
	if _, err := tableSegment(&fakeTable{err: errors.New("401")}).Build(context.Background(), now, nil); err == nil {
		t.Fatal("source error swallowed")
	}
}

func TestTabletennisStatsModeration(t *testing.T) {
	seg := tableSegment(&fakeTable{stats: weekAtTheTable()})
	// an invented number, and a dropped name: the fixed text airs both times
	seg.Moderator = &moderation.Moderator{Client: answer("Busy week: 15 matches at the table!")}
	items, _ := seg.Build(context.Background(), now, schedule.Params{"count": 1})
	if items[0].Message != "Table tennis in the last 7 days: 12 matches, 30 sets, 612 points" {
		t.Fatalf("invented number aired: %q", items[0].Message)
	}
	seg.Moderator = &moderation.Moderator{Client: answer("Our champion won 5 and lost 1 in the last 7 days.")}
	items, _ = seg.Build(context.Background(), now, schedule.Params{"count": 3})
	if items[2].Message != "Most wins in the last 7 days: Cem, 5 won and 1 lost" {
		t.Fatalf("name dropped: %q", items[2].Message)
	}
}
