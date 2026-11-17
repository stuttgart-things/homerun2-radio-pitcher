package moderation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/panel"
)

// Moderator phrases segment texts through a Client and checks the result.
// A nil Moderator, or one without a Client, always returns the fallback.
type Moderator struct {
	Client  Client
	Timeout time.Duration // per call; 0 means 20s
	Log     *slog.Logger
}

const headlineSystem = `You are the host of an office radio that plays on a 64x64 LED matrix.
You shorten news headlines. Rules:
- Answer with the shortened headline only: one line, no quotes.
- Keep the headline's language.
- Do not invent anything. Do not add or change any number.
- Write numbers as digits, exactly as in the headline. Never spell a number out.`

const factSystem = `You are the host of an office radio that plays on a 64x64 LED matrix.
You turn an infrastructure fact into one short, casual English sentence. Rules:
- Answer with the sentence only: one line, no quotes.
- Use only the numbers in the template, written exactly as they are, as digits. Never spell a number out. No other numbers, no arithmetic, no estimates.
- Keep the name at the start of the template (the cluster or source) exactly as it is.
- Do not invent names or facts.`

// Headline shortens a news headline to fit title. Without a model, or when the
// answer fails a check, it returns title.Fit(headline).
func (m *Moderator) Headline(ctx context.Context, headline string, title panel.Title) string {
	headline = panel.Clean(headline)
	fallback := title.Fit(headline)
	if m == nil || m.Client == nil || title.Fits(headline) {
		return fallback
	}
	user := fmt.Sprintf("Shorten to at most %d characters so it fits %d lines of %d characters:\n%s",
		title.Lines*title.Width-title.Lines, title.Lines, title.Width, headline)
	out, err := m.ask(ctx, headlineSystem, user, headline)
	if err == nil && !title.Fits(out) {
		err = fmt.Errorf("answer does not fit %dx%d: %q", title.Lines, title.Width, out)
	}
	if err != nil {
		m.logger().Debug("moderation fallback", "kind", "headline", "reason", err)
		return fallback
	}
	return out
}

// Phrase rewords a fact sentence (template is the fixed text, which already
// holds every number). The answer may be at most maxLen characters and must
// contain every keep string (e.g. the source name), case-insensitively.
func (m *Moderator) Phrase(ctx context.Context, template string, maxLen int, keep ...string) string {
	template = panel.Clean(template)
	if m == nil || m.Client == nil {
		return template
	}
	out, err := m.ask(ctx, factSystem, "Template: "+template, template)
	if err == nil && maxLen > 0 && utf8.RuneCountInString(out) > maxLen {
		err = fmt.Errorf("answer longer than %d characters", maxLen)
	}
	for _, k := range keep {
		if err == nil && k != "" && !strings.Contains(strings.ToLower(out), strings.ToLower(k)) {
			err = fmt.Errorf("answer drops %q", k)
		}
	}
	if err != nil {
		m.logger().Debug("moderation fallback", "kind", "fact", "reason", err)
		return template
	}
	return out
}

// ask calls the model and applies the checks every answer must pass.
func (m *Moderator) ask(ctx context.Context, system, user, source string) (string, error) {
	timeout := m.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := m.Client.Complete(ctx, system, user)
	if err != nil {
		return "", err
	}
	out = clean(out)
	if out == "" {
		return "", errors.New("empty answer")
	}
	if err := NumbersFrom(out, source); err != nil {
		return "", err
	}
	return out, nil
}

func (m *Moderator) logger() *slog.Logger {
	if m.Log != nil {
		return m.Log
	}
	return slog.Default()
}

// clean takes the first line and strips quotes and markdown the model may add.
func clean(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.Trim(s, " \t\"'„“”«»*`")
	return panel.Clean(s)
}

var (
	numberRE = regexp.MustCompile(`\d+`)
	wordRE   = regexp.MustCompile(`\p{L}+`)
	// German compounds like "vierundsechzig".
	germanCompoundRE = regexp.MustCompile(`^(ein|zwei|drei|vier|fünf|sechs|sieben|acht|neun)und(zwanzig|dreißig|vierzig|fünfzig|sechzig|siebzig|achtzig|neunzig)$`)
)

// numberWords are numbers spelled out, English and German. "a" and "ein"
// are left out: too common to tell apart from the article.
var numberWords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`
		zero one two three four five six seven eight nine ten eleven twelve
		thirteen fourteen fifteen sixteen seventeen eighteen nineteen
		twenty thirty forty fourty fifty sixty seventy eighty ninety
		hundred hundreds thousand thousands million millions billion billions
		dozen dozens
		null eins zwei drei vier fünf sechs sieben acht neun zehn elf zwölf
		dreizehn vierzehn fünfzehn sechzehn siebzehn achtzehn neunzehn
		zwanzig dreißig vierzig fünfzig sechzig siebzig achtzig neunzig
		hundert tausend million millionen milliarde milliarden dutzend`) {
		numberWords[w] = true
	}
}

// numbers counts the numbers in s: digit runs (without leading zeros) and
// number words (lower case).
func numbers(s string) map[string]int {
	n := map[string]int{}
	for _, d := range numberRE.FindAllString(s, -1) {
		n[strings.TrimLeft(d, "0")]++
	}
	for _, w := range wordRE.FindAllString(strings.ToLower(s), -1) {
		if numberWords[w] || germanCompoundRE.MatchString(w) {
			n[w]++
		}
	}
	return n
}

// NumbersFrom checks the ground rule: every number in out must also appear in
// source, at most as often as there. Numbers spelled out as words count too
// ("sixty-four" is not in "64 VMs"). A model may drop a number, but never add,
// change, repeat or spell one out.
func NumbersFrom(out, source string) error {
	have := numbers(source)
	for n, c := range numbers(out) {
		if c > have[n] {
			if have[n] == 0 {
				return fmt.Errorf("answer has number %q that is not in the source", n)
			}
			return fmt.Errorf("answer has number %q %d times, the source %d", n, c, have[n])
		}
	}
	return nil
}
