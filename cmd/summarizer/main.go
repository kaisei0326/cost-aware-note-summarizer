// Command summarizer runs the cost-aware summarization pipeline exactly once
// and exits. It is built to be launched by a scheduler (GitHub Actions cron or
// `docker run`), not to run as a long-lived daemon: each invocation fetches,
// cascades, notifies, and stops. That model is the cheapest to operate — no
// idle server, no always-on cost.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/kaisei0326/cost-aware-note-summarizer/internal/config"
	"github.com/kaisei0326/cost-aware-note-summarizer/internal/feed"
	"github.com/kaisei0326/cost-aware-note-summarizer/internal/llm"
	"github.com/kaisei0326/cost-aware-note-summarizer/internal/notify"
	"github.com/kaisei0326/cost-aware-note-summarizer/internal/pipeline"
	"github.com/kaisei0326/cost-aware-note-summarizer/internal/store"
)

func main() {
	// run() owns all the real logic and returns an error; main() just maps that
	// to a process exit code. This keeps main tiny and makes the body testable.
	if err := run(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := newLogger(cfg.LogLevel)
	slog.SetDefault(log)

	// One shared HTTP client (with the configured timeout) is reused by the
	// feed fetcher, the Gemini client and the Discord notifier — so every
	// outbound call inherits the same connection pool and timeout.
	httpClient := &http.Client{Timeout: cfg.HTTPTimeout}

	// Cancel on SIGINT/SIGTERM (a graceful container stop) and bound the whole
	// run with RunTimeout so a stuck call can never hang the scheduled job.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, cfg.RunTimeout)
	defer cancel()

	st, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()

	gemini := llm.NewGemini(cfg.GeminiAPIKey, cfg.TriageModel, cfg.SummaryModel, cfg.MaxContentChars, httpClient, log)

	// Pick the notifier from config: real Discord, or a logging no-op in dry-run.
	var notifier notify.Notifier
	if cfg.DryRun {
		notifier = notify.Noop{Log: log}
	} else {
		notifier = notify.NewDiscord(cfg.DiscordWebhookURL, httpClient, log)
	}

	p := pipeline.New(
		feed.New(httpClient, log),
		st,
		gemini,
		notifier,
		cfg.FeedURLs,
		cfg.MaxArticles,
		log,
	)

	_, err = p.Run(ctx)
	return err
}

// newLogger configures a JSON slog handler. Structured JSON logs are what a log
// backend (Cloud Logging, etc.) ingests as queryable fields — and the per-stage
// common fields the pipeline attaches (article_id, source) turn the output into
// a poor-man's trace you can filter by a single article.
func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}
