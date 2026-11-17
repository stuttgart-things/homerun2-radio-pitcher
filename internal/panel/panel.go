// Package panel fits text to the card of the 64x64 LED panel.
//
// The led-catcher word-wraps a card title onto 3 lines of 12 characters
// (5x7 font) or 4 lines of 16 (4x6 font) and cuts what does not fit with an
// ellipsis, possibly mid-word. Shortening here, at word boundaries, keeps the
// headline readable instead.
package panel

import (
	"strings"
	"unicode/utf8"
)

// Ellipsis ends text that was shortened.
const Ellipsis = "…"

// Title is the space of a card title.
type Title struct {
	Lines int // lines on the card
	Width int // characters per line
}

// Card5x7 and Card4x6 are the two title layouts of a led-catcher card.
var (
	Card5x7 = Title{Lines: 3, Width: 12}
	Card4x6 = Title{Lines: 4, Width: 16}
)

// Clean collapses whitespace (newlines, tabs, runs of spaces) to single
// spaces and trims the ends.
func Clean(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// Wrap word-wraps s the way the catcher does: greedy, a word longer than a
// line is broken where it has to be.
func Wrap(s string, width int) []string {
	if width < 1 {
		return nil
	}
	var lines []string
	cur := ""
	for _, w := range strings.Fields(s) {
		for utf8.RuneCountInString(w) > width {
			if cur != "" {
				lines = append(lines, cur)
				cur = ""
			}
			r := []rune(w)
			lines = append(lines, string(r[:width]))
			w = string(r[width:])
		}
		switch {
		case cur == "":
			cur = w
		case utf8.RuneCountInString(cur)+1+utf8.RuneCountInString(w) <= width:
			cur += " " + w
		default:
			lines = append(lines, cur)
			cur = w
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

// Fits reports whether s wraps into the title space.
func (t Title) Fits(s string) bool {
	return len(Wrap(s, t.Width)) <= t.Lines
}

// Fit returns s when it fits the title space, otherwise the longest run of
// whole leading words that fits followed by an ellipsis. A first word too long
// for the space is cut.
func (t Title) Fit(s string) string {
	s = Clean(s)
	if t.Fits(s) {
		return s
	}
	words := strings.Fields(s)
	for n := len(words) - 1; n > 0; n-- {
		cand := strings.TrimRight(strings.Join(words[:n], " "), " ,;:-–—") + Ellipsis
		if t.Fits(cand) {
			return cand
		}
	}
	max := t.Lines*t.Width - 1
	r := []rune(s)
	if max < 1 {
		return ""
	}
	if len(r) > max {
		r = r[:max]
	}
	return string(r) + Ellipsis
}

// Truncate shortens s to at most max characters at a word boundary, ending in
// an ellipsis when it was shortened. It is meant for the scrolling message
// line, which has no line limit but should not run for minutes.
func Truncate(s string, max int) string {
	s = Clean(s)
	if max < 1 || utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)[:max-1]
	cut := string(r)
	if i := strings.LastIndex(cut, " "); i > max/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,;:-–—") + Ellipsis
}
