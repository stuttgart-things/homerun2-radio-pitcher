// Package config reads the service configuration from the environment, once
// at startup.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"regexp"

	homerun "github.com/stuttgart-things/homerun-library/v4"

	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/moderation"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/panel"
)

// Config is the whole service configuration.
type Config struct {
	Port string

	Redis  homerun.RedisConfig
	Stream string
	DryRun bool

	SchedulePath string
	SourcesPath  string
	Location     *time.Location

	System   string
	Severity string
	Author   string

	Spacing time.Duration
	// ArchiveSize is how many aired cards the UI's archive keeps.
	ArchiveSize int
	Title       panel.Title
	MessageMax  int

	NewsFeeds   string
	NewsTimeout time.Duration
	NewsSeenFor time.Duration
	NewsMaxAge  time.Duration
	// NewsBlockWords skips headlines with one of these words (matched at a
	// word start, case-insensitive). nil: the segment's default list; empty:
	// none (RADIO_NEWS_BLOCK_WORDS=none).
	NewsBlockWords []string

	SourceTimeout time.Duration
	FactsRecent   int

	// ScoutURL and ScoutToken reach homerun2-scout for daily-summary.
	ScoutURL   string
	ScoutToken string

	// SchmetterpauseURL and SchmetterpauseToken reach schmetterpause's
	// GET /api/statistics for tabletennis-stats (its SP_STATISTICS_TOKEN).
	SchmetterpauseURL   string
	SchmetterpauseToken string

	// BackstageURL and BackstageToken reach the Backstage catalog for
	// did-you-know (a static external access token); an aired entry is not
	// repeated within DidYouKnowSeenFor.
	BackstageURL   string
	BackstageToken string
	// ReleasesOrg is the GitHub organization whose releases the releases
	// segment airs; GitHubToken is optional (60 requests an hour without).
	ReleasesOrg       string
	GitHubToken       string
	DidYouKnowSeenFor time.Duration

	// BasePath is the URL prefix of the UI (BASE_PATH), e.g. "/radio"
	// behind homerun2-portal; "" serves it at the root.
	BasePath string
	// UIEnabled switches the read-only programme UI (RADIO_UI_ENABLED).
	UIEnabled bool
	// UIToken enables the session controls of the UI (RADIO_UI_TOKEN).
	UIToken string

	LLMEnabled  bool
	OllamaURL   string
	OllamaModel string
	OllamaAPI   string
	LLMAPIKey   string
	LLMTimeout  time.Duration
}

// Load reads the environment. getenv is os.Getenv outside tests.
func Load(getenv func(string) string) (*Config, error) {
	e := env{get: getenv}
	c := &Config{
		Port: e.str("PORT", "8080"),
		Redis: homerun.RedisConfig{
			Addr:     e.str("REDIS_ADDR", "localhost"),
			Port:     e.str("REDIS_PORT", "6379"),
			Password: e.str("REDIS_PASSWORD", ""),
		},
		Stream: e.str("REDIS_STREAM", "ambient"),
		DryRun: e.boolean("RADIO_DRY_RUN", false),

		SchedulePath: e.str("RADIO_SCHEDULE_PATH", "/etc/homerun2-radio-pitcher/schedule.yaml"),
		SourcesPath:  e.str("RADIO_SOURCES_PATH", ""),

		System:   e.str("RADIO_SYSTEM", "radio"),
		Severity: e.str("RADIO_SEVERITY", "INFO"),
		Author:   e.str("RADIO_AUTHOR", "radio"),

		Spacing:     e.duration("RADIO_ITEM_SPACING", 15*time.Second),
		ArchiveSize: e.integer("RADIO_ARCHIVE_SIZE", 300),
		Title: panel.Title{
			Lines: e.integer("RADIO_TITLE_LINES", panel.Card4x6.Lines),
			Width: e.integer("RADIO_TITLE_WIDTH", panel.Card4x6.Width),
		},
		MessageMax: e.integer("RADIO_MESSAGE_MAX", 120),

		NewsFeeds:      e.str("RADIO_NEWS_FEEDS", ""),
		NewsTimeout:    e.duration("RADIO_NEWS_TIMEOUT", 10*time.Second),
		NewsSeenFor:    e.duration("RADIO_NEWS_SEEN_FOR", 12*time.Hour),
		NewsMaxAge:     e.duration("RADIO_NEWS_MAX_AGE", 48*time.Hour),
		NewsBlockWords: e.words("RADIO_NEWS_BLOCK_WORDS"),

		SourceTimeout: e.duration("RADIO_SOURCE_TIMEOUT", 10*time.Second),
		FactsRecent:   e.integer("RADIO_FACTS_RECENT", 4),
		ScoutURL:      e.str("RADIO_SCOUT_URL", ""),
		ScoutToken:    strings.TrimSpace(e.str("RADIO_SCOUT_TOKEN", "")),

		SchmetterpauseURL:   e.str("RADIO_SCHMETTERPAUSE_URL", ""),
		SchmetterpauseToken: strings.TrimSpace(e.str("RADIO_SCHMETTERPAUSE_TOKEN", "")),

		BackstageURL:      e.str("RADIO_BACKSTAGE_URL", ""),
		BackstageToken:    strings.TrimSpace(e.str("RADIO_BACKSTAGE_TOKEN", "")),
		ReleasesOrg:       strings.TrimSpace(e.str("RADIO_RELEASES_ORG", "")),
		GitHubToken:       strings.TrimSpace(e.str("RADIO_GITHUB_TOKEN", "")),
		DidYouKnowSeenFor: e.duration("RADIO_DID_YOU_KNOW_SEEN_FOR", 7*24*time.Hour),

		UIEnabled:   e.boolean("RADIO_UI_ENABLED", true),
		UIToken:     strings.TrimSpace(e.str("RADIO_UI_TOKEN", "")),
		LLMEnabled:  e.boolean("RADIO_LLM_ENABLED", false),
		OllamaURL:   e.str("OLLAMA_URL", ""),
		OllamaModel: e.str("OLLAMA_MODEL", ""),
		OllamaAPI:   e.str("OLLAMA_API", moderation.APIOpenAI),
		LLMAPIKey:   e.str("LLM_API_KEY", ""),
		LLMTimeout:  e.duration("OLLAMA_TIMEOUT", 20*time.Second),
	}

	bp, err := ParseBasePath(e.str("BASE_PATH", ""))
	if err != nil {
		e.errs = append(e.errs, err)
	}
	c.BasePath = bp
	if c.UIToken != "" && len(c.UIToken) < 16 {
		e.errs = append(e.errs, errors.New("RADIO_UI_TOKEN must be at least 16 characters"))
	}

	tz := e.str("RADIO_TIMEZONE", "Europe/Berlin")
	loc, err := time.LoadLocation(tz)
	if err != nil {
		e.errs = append(e.errs, fmt.Errorf("RADIO_TIMEZONE %q: %w", tz, err))
	}
	c.Location = loc

	if c.Stream == "" {
		e.errs = append(e.errs, errors.New("REDIS_STREAM must not be empty"))
	}
	if c.Title.Lines < 1 || c.Title.Width < 4 {
		e.errs = append(e.errs, errors.New("RADIO_TITLE_LINES must be >= 1 and RADIO_TITLE_WIDTH >= 4"))
	}
	if c.MessageMax < 10 {
		e.errs = append(e.errs, errors.New("RADIO_MESSAGE_MAX must be >= 10"))
	}
	if c.OllamaAPI != moderation.APIOpenAI && c.OllamaAPI != moderation.APINative {
		e.errs = append(e.errs, fmt.Errorf("OLLAMA_API %q: openai or native", c.OllamaAPI))
	}
	if c.LLMEnabled && (c.OllamaURL == "" || c.OllamaModel == "") {
		e.errs = append(e.errs, errors.New("RADIO_LLM_ENABLED needs OLLAMA_URL and OLLAMA_MODEL"))
	}
	return c, errors.Join(e.errs...)
}

type env struct {
	get  func(string) string
	errs []error
}

func (e *env) str(key, def string) string {
	if v, ok := e.lookup(key); ok {
		return v
	}
	return def
}

// words reads a comma-separated list, lower case: nil when key is unset
// (the caller's default), empty (not nil) when it is "none".
func (e *env) words(key string) []string {
	v, ok := e.lookup(key)
	if !ok {
		return nil
	}
	out := []string{}
	if strings.EqualFold(v, "none") {
		return out
	}
	for _, w := range strings.Split(v, ",") {
		if w = strings.ToLower(strings.TrimSpace(w)); w != "" {
			out = append(out, w)
		}
	}
	return out
}

func (e *env) lookup(key string) (string, bool) {
	v := strings.TrimSpace(e.get(key))
	return v, v != ""
}

func (e *env) integer(key string, def int) int {
	v, ok := e.lookup(key)
	if !ok {
		return def
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		e.errs = append(e.errs, fmt.Errorf("%s %q: not a number", key, v))
		return def
	}
	return i
}

func (e *env) boolean(key string, def bool) bool {
	v, ok := e.lookup(key)
	if !ok {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		e.errs = append(e.errs, fmt.Errorf("%s %q: true or false", key, v))
		return def
	}
	return b
}

func (e *env) duration(key string, def time.Duration) time.Duration {
	v, ok := e.lookup(key)
	if !ok {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		e.errs = append(e.errs, fmt.Errorf("%s %q: a duration like 15s or 12h", key, v))
		return def
	}
	return d
}

// SetupLogging configures slog from LOG_FORMAT (json|text) and LOG_LEVEL.
func SetupLogging() {
	var level slog.Level
	switch strings.ToLower(os.Getenv("LOG_LEVEL")) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: level}
	var h slog.Handler = slog.NewJSONHandler(os.Stdout, opts)
	if strings.ToLower(os.Getenv("LOG_FORMAT")) == "text" {
		h = slog.NewTextHandler(os.Stdout, opts)
	}
	slog.SetDefault(slog.New(h))
	homerun.SetLogger(slog.Default())
}

var basePathSegment = regexp.MustCompile(`^[A-Za-z0-9._~-]+$`)

// ParseBasePath normalises BASE_PATH to "/a/b" ("" and "/" become ""). Only
// plain path segments: the value is written into the page's HTML, and "." or
// ".." would make the prefix ambiguous.
func ParseBasePath(s string) (string, error) {
	trimmed := strings.Trim(strings.TrimSpace(s), "/")
	if trimmed == "" {
		return "", nil
	}
	for _, seg := range strings.Split(trimmed, "/") {
		if !basePathSegment.MatchString(seg) || seg == "." || seg == ".." {
			return "", fmt.Errorf("BASE_PATH %q: segment %q, want letters, digits, ._~-", s, seg)
		}
	}
	return "/" + trimmed, nil
}
