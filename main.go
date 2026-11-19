// homerun2-radio-pitcher plays a programme on the homerun2 LED matrix: it
// airs the segments of a schedule (time check, news, infra facts) into the
// Redis stream `ambient`, which the led-catcher shows only while the panel is
// free.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	// Embedded zone data: the static base image has no tzdata.
	_ "time/tzdata"

	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/backstage"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/config"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/director"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/github"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/handlers"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/moderation"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/pitcher"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/schedule"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/schmetterpause"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/scout"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/segment"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/sources"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/spots"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/ui"
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

const usage = `Usage: homerun2-radio-pitcher [command] [flags]

Commands:
  serve                 air the schedule and serve /health, /ready, /metrics (default)
  once <segment> [k=v]  air one segment now and exit, e.g. "once news count=2"
  validate              check the schedule and the sources file and exit
  version               print the version

Flags of once: -dry-run prints the messages instead of pitching them.
Configuration comes from the environment, see the README.
`

func main() {
	config.SetupLogging()
	cmd, args := command(os.Args[1:])
	var err error
	switch cmd {
	case "serve":
		err = serve()
	case "once":
		err = once(args)
	case "validate":
		err = validate()
	case "version":
		fmt.Printf("homerun2-radio-pitcher %s (%s, %s)\n", version, commit, date)
	case "help":
		fmt.Print(usage)
	default:
		fmt.Fprint(os.Stderr, usage)
		err = fmt.Errorf("unknown command %q", cmd)
	}
	if err != nil {
		slog.Error("exit", "error", err)
		os.Exit(1)
	}
}

// command splits the arguments into the command and its own arguments.
// Without arguments it is serve. A leading flag is never serve: -h and
// --help ask for the usage, and any other flag is not a command (serve
// takes none), so a mistyped flag cannot start a second director (#39).
func command(args []string) (string, []string) {
	if len(args) == 0 {
		return "serve", nil
	}
	switch args[0] {
	case "-h", "-help", "--help":
		return "help", nil
	}
	return args[0], args[1:]
}

// station wires the configuration into a director.
type station struct {
	cfg      *config.Config
	watcher  *schedule.Watcher
	pitcher  pitcher.Pitcher
	director *director.Director
	segments segment.Registry
	sources  []*sources.Tracked
}

func newStation(cfg *config.Config, dryRun bool) (*station, error) {
	log := slog.Default()

	var mod *moderation.Moderator
	if cfg.LLMEnabled {
		mod = &moderation.Moderator{
			Client:  &moderation.Ollama{URL: cfg.OllamaURL, Model: cfg.OllamaModel, API: cfg.OllamaAPI, APIKey: cfg.LLMAPIKey, Temperature: 0.3},
			Timeout: cfg.LLMTimeout,
			Observe: handlers.ObserveModeration,
		}
		log.Info("moderation on", "ollama", cfg.OllamaURL, "model", cfg.OllamaModel, "api", cfg.OllamaAPI, "apiKey", cfg.LLMAPIKey != "")
	} else {
		log.Info("moderation off, fixed texts only")
	}

	feeds, err := segment.ParseFeeds(cfg.NewsFeeds)
	if err != nil {
		return nil, fmt.Errorf("RADIO_NEWS_FEEDS: %w", err)
	}
	if len(feeds) == 0 {
		log.Warn("no news feeds configured (RADIO_NEWS_FEEDS), news slots stay silent")
	}

	var srcs []sources.Source
	var tracked []*sources.Tracked
	var rolling []sources.RolloutSource
	if cfg.SourcesPath != "" {
		cfgs, err := sources.LoadConfig(cfg.SourcesPath)
		if err != nil {
			return nil, fmt.Errorf("RADIO_SOURCES_PATH: %w", err)
		}
		var errs []error
		var built []sources.Source
		built, errs = sources.Build(cfgs, cfg.SourceTimeout)
		tracked = sources.Track(built)
		for _, t := range tracked {
			srcs = append(srcs, t)
			if r, ok := t.Source.(sources.RolloutSource); ok {
				rolling = append(rolling, r)
			}
		}
		for _, e := range errs {
			log.Warn("source left out", "error", e)
		}
		for _, s := range srcs {
			log.Info("source on", "name", s.Name())
		}
	} else {
		log.Warn("no sources configured (RADIO_SOURCES_PATH), infra-facts slots stay silent")
	}

	var scoutClient segment.Analytics
	if cfg.ScoutURL != "" {
		scoutClient = &scout.Client{URL: cfg.ScoutURL, Token: cfg.ScoutToken, HTTP: &http.Client{Timeout: cfg.SourceTimeout}}
		log.Info("scout on", "url", cfg.ScoutURL, "token", cfg.ScoutToken != "")
	} else {
		log.Warn("no scout configured (RADIO_SCOUT_URL), daily-summary slots stay silent")
	}

	var table segment.TableStats
	if cfg.SchmetterpauseURL != "" {
		table = &schmetterpause.Client{URL: cfg.SchmetterpauseURL, Token: cfg.SchmetterpauseToken, HTTP: &http.Client{Timeout: cfg.SourceTimeout}}
		log.Info("schmetterpause on", "url", cfg.SchmetterpauseURL, "token", cfg.SchmetterpauseToken != "")
	} else {
		log.Warn("no schmetterpause configured (RADIO_SCHMETTERPAUSE_URL), tabletennis-stats slots stay silent")
	}

	var cat segment.Catalog
	if cfg.BackstageURL != "" {
		cat = &backstage.Client{URL: cfg.BackstageURL, Token: cfg.BackstageToken, HTTP: &http.Client{Timeout: cfg.SourceTimeout}}
		log.Info("backstage on", "url", cfg.BackstageURL, "token", cfg.BackstageToken != "")
	} else {
		log.Warn("no backstage configured (RADIO_BACKSTAGE_URL), did-you-know slots stay silent")
	}

	var pool []spots.Spot
	if cfg.SpotsPath != "" {
		var err error
		if pool, err = spots.Load(cfg.SpotsPath, cfg.MessageMax); err != nil {
			return nil, fmt.Errorf("RADIO_SPOTS_PATH: %w", err)
		}
		log.Info("spots on", "path", cfg.SpotsPath, "spots", len(pool))
	} else {
		log.Warn("no spots configured (RADIO_SPOTS_PATH), spots slots stay silent")
	}

	var rel segment.ReleaseLister
	if cfg.ReleasesOrg != "" {
		rel = &github.Client{Org: cfg.ReleasesOrg, Token: cfg.GitHubToken, HTTP: &http.Client{Timeout: cfg.SourceTimeout}}
		log.Info("github releases on", "org", cfg.ReleasesOrg, "token", cfg.GitHubToken != "")
	} else {
		log.Warn("no github org configured (RADIO_RELEASES_ORG), releases slots stay silent")
	}

	segs := segment.NewRegistry(
		segment.TimeCheck{Moderator: mod, MessageMax: cfg.MessageMax},
		&segment.News{
			Feeds:      feeds,
			Fetch:      segment.HTTPFetcher(&http.Client{Timeout: cfg.NewsTimeout}),
			Moderator:  mod,
			Title:      cfg.Title,
			MessageMax: cfg.MessageMax,
			SeenFor:    cfg.NewsSeenFor,
			BlockWords: cfg.NewsBlockWords,
			MaxAge:     cfg.NewsMaxAge,
		},
		&segment.InfraFacts{
			Sources:    srcs,
			Moderator:  mod,
			Title:      cfg.Title,
			MessageMax: cfg.MessageMax,
			Timeout:    cfg.SourceTimeout,
			Recent:     cfg.FactsRecent,
		},
		&segment.DailySummary{
			Scout:      scoutClient,
			Moderator:  mod,
			Title:      cfg.Title,
			MessageMax: cfg.MessageMax,
			Timeout:    cfg.SourceTimeout,
			Own:        cfg.System,
		},
		&segment.TabletennisStats{
			Source:     table,
			Moderator:  mod,
			Title:      cfg.Title,
			MessageMax: cfg.MessageMax,
			Timeout:    cfg.SourceTimeout,
		},
		&segment.DidYouKnow{
			Catalog:    cat,
			Moderator:  mod,
			Title:      cfg.Title,
			MessageMax: cfg.MessageMax,
			Timeout:    cfg.SourceTimeout,
			SeenFor:    cfg.DidYouKnowSeenFor,
		},
		&segment.Releases{
			Lister:     rel,
			Org:        cfg.ReleasesOrg,
			Title:      cfg.Title,
			MessageMax: cfg.MessageMax,
			Timeout:    cfg.SourceTimeout,
		},
		&segment.Spots{Pool: pool, Title: cfg.Title},
		&segment.Rollouts{
			Sources:    rolling,
			Title:      cfg.Title,
			MessageMax: cfg.MessageMax,
			Timeout:    cfg.SourceTimeout,
		},
	).WithIntros(&segment.Host{Moderator: mod, Title: cfg.Title, MessageMax: cfg.MessageMax})

	var p pitcher.Pitcher
	if dryRun {
		p = &pitcher.Writer{W: os.Stdout}
		log.Info("dry run, messages are printed, not pitched")
	} else {
		p = pitcher.NewRedis(cfg.Redis, cfg.Stream)
		log.Info("pitching", "redis", cfg.Redis.Addr+":"+cfg.Redis.Port, "stream", cfg.Stream)
	}

	w := &schedule.Watcher{Path: cfg.SchedulePath}
	d := &director.Director{
		Schedule: w,
		Segments: segs,
		Pitcher:  p,
		Station:  director.Station{System: cfg.System, Severity: cfg.Severity, Author: cfg.Author},
		Location: cfg.Location,
		Spacing:  cfg.Spacing,
		Archive:  cfg.ArchiveSize,
		Observe:  handlers.Observe,
	}
	return &station{cfg: cfg, watcher: w, pitcher: p, director: d, segments: segs, sources: tracked}, nil
}

func serve() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	st, err := newStation(cfg, cfg.DryRun)
	if err != nil {
		return err
	}
	defer func() { _ = st.pitcher.Close() }()

	slog.Info("homerun2-radio-pitcher on air", "version", version, "schedule", cfg.SchedulePath, "timezone", cfg.Location.String())

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	root := handlers.Mux(handlers.Build{Version: version, Commit: commit, Date: date}, handlers.Status{
		Ready:     st.pitcher.Ready,
		Schedule:  st.watcher.Current,
		LastAired: st.director.LastAired,
	})
	var app http.Handler
	if cfg.UIEnabled {
		app = (&ui.Server{
			BasePath: cfg.BasePath,
			Version:  version,
			Location: cfg.Location,
			Schedule: st.watcher.Current,
			Director: st.director,
			Segments: st.segments,
			Sources:  st.sources,
			Title:    cfg.Title,
			Token:    cfg.UIToken,
			Started:  time.Now(),
		}).Handler()
		slog.Info("programme UI on", "path", cfg.BasePath+"/", "controls", cfg.UIToken != "")
	}
	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           handlers.Mount(cfg.BasePath, root, app),
		ReadHeaderTimeout: 5 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	done := make(chan struct{})
	go func() { _ = st.director.Run(ctx); close(done) }()

	select {
	case <-ctx.Done():
	case err = <-errc:
		stop()
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
	<-done
	slog.Info("off air")
	return err
}

// once airs one segment now: for trying a segment on the real panel.
func once(args []string) error {
	fs := flag.NewFlagSet("once", flag.ContinueOnError)
	dry := fs.Bool("dry-run", false, "print the messages instead of pitching them")
	// flags may come before or after the segment name
	var rest []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return err
		}
		args = fs.Args()
		if len(args) > 0 {
			rest = append(rest, args[0])
			args = args[1:]
		}
	}
	if len(rest) == 0 {
		return errors.New("once: name a segment, e.g. once news count=2")
	}
	params, err := schedule.ParseParams(rest[1:])
	if err != nil {
		return fmt.Errorf("once: %w", err)
	}

	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	st, err := newStation(cfg, *dry || cfg.DryRun)
	if err != nil {
		return err
	}
	defer func() { _ = st.pitcher.Close() }()
	n, err := st.director.Air(context.Background(), rest[0], params, time.Now())
	if err != nil {
		return err
	}
	slog.Info("aired", "segment", rest[0], "items", n)
	return nil
}

// validate checks the schedule and the sources file, for CI and for a PR
// that changes the programme.
func validate() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	s, err := schedule.Load(cfg.SchedulePath)
	if err != nil {
		return err
	}
	known := segment.NewRegistry(segment.TimeCheck{}, &segment.News{}, &segment.InfraFacts{}, &segment.DailySummary{}, &segment.TabletennisStats{}, &segment.DidYouKnow{}, &segment.Rollouts{}, &segment.Releases{}, &segment.Spots{})
	for _, u := range s.Unknown(known.Has) {
		slog.Warn("unknown segment, will be skipped", "slot", u, "known", known.Names())
	}
	slog.Info("schedule ok", "path", cfg.SchedulePath, "minutes", s.Minutes(), "programme", len(s.Programme))
	if cfg.SourcesPath != "" {
		cfgs, err := sources.LoadConfig(cfg.SourcesPath)
		if err != nil {
			return err
		}
		slog.Info("sources ok", "path", cfg.SourcesPath, "sources", len(cfgs))
	}
	if cfg.SpotsPath != "" {
		pool, err := spots.Load(cfg.SpotsPath, cfg.MessageMax)
		if err != nil {
			return err
		}
		slog.Info("spots ok", "path", cfg.SpotsPath, "spots", len(pool))
	}
	return nil
}
