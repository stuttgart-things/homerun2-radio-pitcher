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
	// Observe, when set, is told the outcome of every model call: kind
	// (headline, summary, fact, facts, blurb, intro, time) and one of the
	// Result* values.
	Observe func(kind, result string)
}

// Outcomes of a model call, for Moderator.Observe.
const (
	ResultUsed           = "used"            // the answer aired
	ResultRejectedNumber = "rejected_number" // a number not in the input (NumbersFrom)
	ResultRejectedName   = "rejected_name"   // dropped a name it had to keep
	ResultRejectedLength = "rejected_length" // too long for the card
	ResultRejectedEmpty  = "rejected_empty"  // nothing left after cleaning
	ResultRejectedEcho   = "rejected_echo"   // only repeated the input
	ResultError          = "error"           // the call failed: timeout, HTTP error, rate limit
)

var (
	errNumber = errors.New("number check")
	errName   = errors.New("name check")
	errLength = errors.New("length check")
	errEmpty  = errors.New("empty answer")
	errEcho   = errors.New("answer only repeats the input")
)

// Result maps the error of a model call to its Result* value.
func Result(err error) string {
	switch {
	case err == nil:
		return ResultUsed
	case errors.Is(err, errNumber):
		return ResultRejectedNumber
	case errors.Is(err, errName):
		return ResultRejectedName
	case errors.Is(err, errLength):
		return ResultRejectedLength
	case errors.Is(err, errEmpty):
		return ResultRejectedEmpty
	case errors.Is(err, errEcho):
		return ResultRejectedEcho
	default:
		return ResultError
	}
}

// done records the outcome of a call and logs a fallback.
func (m *Moderator) done(kind string, err error) {
	if m.Observe != nil {
		m.Observe(kind, Result(err))
	}
	if err != nil {
		m.logger().Debug("moderation fallback", "kind", kind, "result", Result(err), "reason", err)
	}
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

const blurbSystem = `You are the host of an office radio that plays on a 64x64 LED matrix.
You turn an entry of the team's software catalog into one short, casual English "did you know" sentence. Rules:
- Answer with the sentence only: one line, no quotes.
- Keep the entry's name exactly as it is.
- Use only what the entry says. Do not invent features, owners or facts.
- Use only the numbers in the entry, written exactly as they are, as digits. Never spell a number out.`

const introSystem = `You are the host of an office radio that plays on a 64x64 LED matrix.
You announce the next segment in one short, casual English line, in your own words. Rules:
- Answer with the line only: one line, no quotes.
- Name the segment exactly as given.
- Only announce. Do not make up headlines, facts, names or results.
- No numbers.`

const timeSystem = `You are the host of an office radio that plays on a 64x64 LED matrix.
You turn a time check into one short, casual English line. Rules:
- Answer with the line only: one line, no quotes.
- Keep the time and the weekday exactly as they are.
- Use only the numbers in the template, written exactly as they are, as digits. Never spell a number out. No other numbers.
- Do not invent events, weather or news.`

const summarySystem = `You are the host of an office radio that plays on a 64x64 LED matrix.
You sum up a news item in one short English sentence. Rules:
- Answer with the sentence only: one line, no quotes.
- Use only what the headline and the description say. Do not invent anything.
- Use only numbers that are in the headline or the description, written exactly as they are, as digits. Never spell a number out.
- If the description says nothing about the content (only links, points or comment counts), answer SKIP.`

// errSkip is the model's way to say there is nothing to sum up.
var errSkip = fmt.Errorf("%w: model answered SKIP", errEmpty)

// Summary sums up a news item (headline and its feed description) in one
// sentence of at most maxLen characters. Numbers only from headline and
// description; a SKIP answer (nothing to sum up) counts as rejected_empty.
// ok is false without a model or when a check fails; the caller then airs
// the headline.
func (m *Moderator) Summary(ctx context.Context, headline, description string, maxLen int) (line string, ok bool) {
	headline, description = panel.Clean(headline), panel.Clean(description)
	if m == nil || m.Client == nil || description == "" {
		return "", false
	}
	user := "Headline: " + headline + "\nDescription: " + description
	if maxLen > 0 {
		user = fmt.Sprintf("At most %d characters.\n%s", maxLen, user)
	}
	out, err := m.ask(ctx, summarySystem, user, headline+" "+description)
	if err == nil && strings.EqualFold(strings.Trim(out, " ."), "skip") {
		err = errSkip
	}
	if err == nil && maxLen > 0 && utf8.RuneCountInString(out) > maxLen {
		err = fmt.Errorf("%w: answer longer than %d characters", errLength, maxLen)
	}
	if err == nil && strings.EqualFold(out, headline) {
		err = errEcho
	}
	m.done("summary", err)
	if err != nil {
		return "", false
	}
	return out, true
}

const combineSystem = `You are the host of an office radio that plays on a 64x64 LED matrix.
You join several infrastructure facts into one short, casual English sentence. Rules:
- Answer with the sentence only: one line, no quotes.
- Keep every fact and every number. Write each number exactly as it is, as digits. Never spell a number out. No other numbers, no arithmetic, no totals.
- Keep every cluster or source name exactly as it is.
- Do not invent names or facts.`

// Combine joins facts (each a fixed sentence holding its numbers) into one
// sentence of at most maxLen characters. Stricter than Phrase: the answer
// must keep every number of the facts, as often as there (a dropped number
// is a lost fact), add none, and keep every keep string (the source names).
// ok is false without a model or when a check fails; the caller then airs
// the facts one by one.
func (m *Moderator) Combine(ctx context.Context, facts []string, maxLen int, keep ...string) (line string, ok bool) {
	var clean []string
	for _, f := range facts {
		clean = append(clean, panel.Clean(f))
	}
	source := strings.Join(clean, " ")
	user := "Facts:\n- " + strings.Join(clean, "\n- ")
	if m == nil || m.Client == nil {
		return "", false
	}
	out, err := m.ask(ctx, combineSystem, user, source)
	if err == nil {
		err = keepsNumbers(out, source)
	}
	if err == nil && maxLen > 0 && utf8.RuneCountInString(out) > maxLen {
		err = fmt.Errorf("%w: answer longer than %d characters", errLength, maxLen)
	}
	for _, k := range keep {
		if err == nil && k != "" && !strings.Contains(strings.ToLower(out), strings.ToLower(k)) {
			err = fmt.Errorf("%w: answer drops %q", errName, k)
		}
	}
	m.done("facts", err)
	if err != nil {
		return "", false
	}
	return out, true
}

// keepsNumbers is NumbersFrom the other way round: every number of source
// is in out, at least as often.
func keepsNumbers(out, source string) error {
	have := numbers(out)
	for n, c := range numbers(source) {
		if have[n] < c {
			return fmt.Errorf("%w: answer drops number %q", errNumber, n)
		}
	}
	return nil
}

// Intro writes a casual host line that announces segment name, which
// brings about (e.g. "News", "the latest headlines"). The line must name the
// segment and has no numbers. ok is false without a model or when the answer
// fails a check (or only repeats the input); the caller then airs its fixed
// line.
func (m *Moderator) Intro(ctx context.Context, name, about string, maxLen int) (line string, ok bool) {
	input := "Next segment: " + panel.Clean(name)
	if about != "" {
		input += "\nWhat it brings: " + panel.Clean(about)
	}
	return m.try(ctx, "intro", introSystem, input, input, maxLen, []string{name})
}

// TimeLine turns a time check (template, e.g. "It's 15:00 on Tuesday, 17
// November 2026.") into a casual line that keeps keep (the time and the
// weekday). ok is false without a model or when the answer fails a check;
// the caller then airs its fixed text.
func (m *Moderator) TimeLine(ctx context.Context, template string, maxLen int, keep ...string) (line string, ok bool) {
	template = panel.Clean(template)
	return m.try(ctx, "time", timeSystem, "Template: "+template, template, maxLen, keep)
}

// Blurb turns a catalog entry (template: "<name>: <description>") into one
// sentence of at most maxLen characters that keeps name. The same checks
// as Phrase; on any failure the template airs.
func (m *Moderator) Blurb(ctx context.Context, template, name string, maxLen int) string {
	template = panel.Clean(template)
	if m == nil || m.Client == nil {
		return template
	}
	user := "Entry: " + template
	if maxLen > 0 {
		user = fmt.Sprintf("At most %d characters.\n%s", maxLen, user)
	}
	out, err := m.ask(ctx, blurbSystem, user, template)
	if err == nil && maxLen > 0 && utf8.RuneCountInString(out) > maxLen {
		err = fmt.Errorf("%w: answer longer than %d characters", errLength, maxLen)
	}
	if err == nil && name != "" && !strings.Contains(strings.ToLower(out), strings.ToLower(name)) {
		err = fmt.Errorf("%w: answer drops %q", errName, name)
	}
	m.done("blurb", err)
	if err != nil {
		return template
	}
	return out
}

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
		err = fmt.Errorf("%w: answer does not fit %dx%d: %q", errLength, title.Lines, title.Width, out)
	}
	m.done("headline", err)
	if err != nil {
		return fallback
	}
	return out
}

// Phrase rewords a fact sentence (template is the fixed text, which already
// holds every number). The answer may be at most maxLen characters and must
// contain every keep string (e.g. the source name) that the template
// contains, case-insensitively.
func (m *Moderator) Phrase(ctx context.Context, template string, maxLen int, keep ...string) string {
	return m.rephrase(ctx, "fact", factSystem, template, maxLen, keep)
}

// rephrase asks the model to reword template under system; on any failure
// the template comes back.
func (m *Moderator) rephrase(ctx context.Context, kind, system, template string, maxLen int, keep []string) string {
	template = panel.Clean(template)
	if out, ok := m.try(ctx, kind, system, "Template: "+template, template, maxLen, keep); ok {
		return out
	}
	return template
}

// try asks the model (user is the prompt, source the text its numbers and
// names come from) and checks the answer: the numbers (ask), at most maxLen
// characters, every keep string that source contains (case-insensitively),
// and not just source again. It reports the outcome to Observe.
func (m *Moderator) try(ctx context.Context, kind, system, user, source string, maxLen int, keep []string) (string, bool) {
	if m == nil || m.Client == nil {
		return "", false
	}
	out, err := m.ask(ctx, system, user, source)
	if err == nil && maxLen > 0 && utf8.RuneCountInString(out) > maxLen {
		err = fmt.Errorf("%w: answer longer than %d characters", errLength, maxLen)
	}
	for _, k := range keep {
		k = strings.ToLower(k)
		if err == nil && k != "" && strings.Contains(strings.ToLower(source), k) && !strings.Contains(strings.ToLower(out), k) {
			err = fmt.Errorf("%w: answer drops %q", errName, k)
		}
	}
	if err == nil && strings.EqualFold(out, panel.Clean(source)) {
		err = errEcho
	}
	m.done(kind, err)
	if err != nil {
		return "", false
	}
	return out, true
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
		return "", errEmpty
	}
	if err := NumbersFrom(out, source); err != nil {
		return "", fmt.Errorf("%w: %w", errNumber, err)
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
