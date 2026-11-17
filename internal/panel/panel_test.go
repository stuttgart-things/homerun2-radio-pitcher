package panel

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestWrap(t *testing.T) {
	got := Wrap("pod crash loop detected", 12)
	want := []string{"pod crash", "loop", "detected"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("Wrap = %q, want %q", got, want)
	}
	got = Wrap("Supercalifragilistic is long", 8)
	want = []string{"Supercal", "ifragili", "stic is", "long"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("Wrap long word = %q, want %q", got, want)
	}
	if len(Wrap("", 12)) != 0 || Wrap("x", 0) != nil {
		t.Fatal("degenerate input")
	}
}

func TestFitKeepsWhatFits(t *testing.T) {
	for _, s := range []string{"15:00 Uhr", "312 Pods auf homerun2-dev", "Flux: 41 von 42 Ready"} {
		if got := Card5x7.Fit(s); got != s {
			t.Errorf("Fit(%q) = %q", s, got)
		}
	}
}

func TestFitShortensAtWords(t *testing.T) {
	in := "Kubernetes 1.36 bringt neue Features für Sidecar-Container und mehr Sicherheit im Cluster"
	for _, title := range []Title{Card5x7, Card4x6} {
		got := title.Fit(in)
		if !title.Fits(got) {
			t.Errorf("%v: %q does not fit: %q", title, got, Wrap(got, title.Width))
		}
		if !strings.HasSuffix(got, Ellipsis) {
			t.Errorf("%v: %q has no ellipsis", title, got)
		}
		body := strings.TrimSuffix(got, Ellipsis)
		if !strings.HasPrefix(in, body) {
			t.Errorf("%v: %q is not a prefix of the input", title, body)
		}
		// at a word boundary
		if next := in[len(body):]; next != "" && next[0] != ' ' {
			t.Errorf("%v: %q cut mid-word", title, got)
		}
	}
}

func TestFitOneLongWord(t *testing.T) {
	got := Card5x7.Fit(strings.Repeat("x", 80))
	if utf8.RuneCountInString(got) != 36 || !strings.HasSuffix(got, Ellipsis) {
		t.Fatalf("got %q (%d)", got, utf8.RuneCountInString(got))
	}
	if !Card5x7.Fits(got) {
		t.Fatal("does not fit")
	}
}

func TestClean(t *testing.T) {
	if got := Clean("  a\n\tb   c "); got != "a b c" {
		t.Fatalf("Clean = %q", got)
	}
}

func TestTruncate(t *testing.T) {
	if got := Truncate("short", 20); got != "short" {
		t.Fatal(got)
	}
	got := Truncate("one two three four five six seven", 16)
	if utf8.RuneCountInString(got) > 16 || !strings.HasSuffix(got, Ellipsis) || strings.Contains(got, "thr…") {
		t.Fatalf("Truncate = %q", got)
	}
	if got := Truncate("abcdefghijklmnopqrstuvwxyz", 10); got != "abcdefghi"+Ellipsis {
		t.Fatalf("Truncate no spaces = %q", got)
	}
}
