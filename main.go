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
	"strconv"
	"strings"
	"syscall"
	"time"
	// Embedded zone data: the static base image has no tzdata.
	_ "time/tzdata"

	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/config"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/director"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/handlers"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/moderation"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/pitcher"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/schedule"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/segment"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/sources"
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
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
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
	case "help", "-h", "--help":
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

// station wires the configuration into a director.
type station struct {
	cfg      *config.Config
	watcher  *schedule.Watcher
	pitcher  pitcher.Pitcher
	director *director.Director
}

func newStation(cfg *config.Config, dryRun bool) (*station, error) {
	log := slog.Default()

	var mod *moderation.Moderator
	if cfg.LLMEnabled {
		mod = &moderation.Moderator{
			Client:  &moderation.Ollama{URL: cfg.OllamaURL, Model: cfg.OllamaModel, API: cfg.OllamaAPI, Temperature: 0.3},
			Timeout: cfg.LLMTimeout,
		}
		log.Info("moderation on", "ollama", cfg.OllamaURL, "model", cfg.OllamaModel, "api", cfg.OllamaAPI)
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
	if cfg.SourcesPath != "" {
		cfgs, err := sources.LoadConfig(cfg.SourcesPath)
		if err != nil {
			return nil, fmt.Errorf("RADIO_SOURCES_PATH: %w", err)
		}
		var errs []error
		srcs, errs = sources.Build(cfgs, cfg.SourceTimeout)
		for _, e := range errs {
			log.Warn("source left out", "error", e)
		}
		for _, s := range srcs {
			log.Info("source on", "name", s.Name())
		}
	} else {
		log.Warn("no sources configured (RADIO_SOURCES_PATH), infra-facts slots stay silent")
	}

	segs := segment.NewRegistry(
		segment.TimeCheck{},
		&segment.News{
			Feeds:      feeds,
			Fetch:      segment.HTTPFetcher(&http.Client{Timeout: cfg.NewsTimeout}),
			Moderator:  mod,
			Title:      cfg.Title,
			MessageMax: cfg.MessageMax,
			SeenFor:    cfg.NewsSeenFor,
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
	)

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
		Observe:  handlers.Observe,
	}
	return &station{cfg: cfg, watcher: w, pitcher: p, director: d}, nil
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

	srv := &http.Server{
		Addr: ":" + cfg.Port,
		Handler: handlers.Mux(handlers.Build{Version: version, Commit: commit, Date: date}, handlers.Status{
			Ready:     st.pitcher.Ready,
			Schedule:  st.watcher.Current,
			LastAired: st.director.LastAired,
		}),
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
	params := schedule.Params{}
	for _, kv := range rest[1:] {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return fmt.Errorf("once: parameter %q, want key=value", kv)
		}
		if n, err := strconv.Atoi(v); err == nil {
			params[k] = n
		} else {
			params[k] = v
		}
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
	known := segment.NewRegistry(segment.TimeCheck{}, &segment.News{}, &segment.InfraFacts{})
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
	return nil
}
