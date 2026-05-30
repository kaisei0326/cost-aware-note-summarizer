// Package config loads all runtime configuration from environment variables.
//
// This is the 12-factor approach: the same binary behaves differently in local
// dev, CI and the GitHub Actions cron job purely through the environment, with
// no config files baked into the image. caarlos0/env maps env vars onto a typed
// struct (with defaults and parsing for slices/durations) so the rest of the
// code reads strongly-typed fields instead of calling os.Getenv everywhere.
package config

import (
	"errors"
	"fmt"
	"time"

	"github.com/caarlos0/env/v11"
)

// Config is the fully-resolved configuration for one pipeline run.
type Config struct {
	// --- Secrets (provided via GitHub Secrets in CI, .env locally) ---
	GeminiAPIKey      string `env:"GEMINI_API_KEY"`
	DiscordWebhookURL string `env:"DISCORD_WEBHOOK_URL"`

	// --- Feeds & storage ---
	FeedURLs []string `env:"FEED_URLS" envSeparator:","`
	DBPath   string   `env:"DB_PATH" envDefault:"./data/summarizer.db"`

	// --- Cascading models (cost lever #2: cheap triage, expensive summary) ---
	TriageModel  string `env:"GEMINI_TRIAGE_MODEL" envDefault:"gemini-2.5-flash-lite"`
	SummaryModel string `env:"GEMINI_SUMMARY_MODEL" envDefault:"gemini-2.5-flash"`

	// MaxArticles caps how many *new* articles we process per run. This is the
	// design knob that keeps us inside the Gemini free tier: even if 200 new
	// items appear, we only ever spend LLM calls on the newest N.
	MaxArticles int `env:"MAX_ARTICLES_PER_RUN" envDefault:"10"`

	// MaxContentChars truncates article bodies before sending them to the LLM.
	// Fewer input tokens = lower cost and faster calls (cost lever).
	MaxContentChars int `env:"MAX_CONTENT_CHARS" envDefault:"4000"`

	// --- Runtime behaviour ---
	HTTPTimeout time.Duration `env:"HTTP_TIMEOUT" envDefault:"30s"`
	RunTimeout  time.Duration `env:"RUN_TIMEOUT" envDefault:"5m"`
	LogLevel    string        `env:"LOG_LEVEL" envDefault:"info"`

	// DryRun skips the Discord notification (still fetches + triages + summarizes
	// + stores). Handy for local testing without spamming a real channel.
	DryRun bool `env:"DRY_RUN" envDefault:"false"`
}

// defaultFeeds are used when FEED_URLS is not set. Both are stable, public,
// Japanese tech feeds that need no auth.
var defaultFeeds = []string{
	"https://zenn.dev/feed",
	"https://b.hatena.ne.jp/hotentry/it.rss",
}

// Load parses the environment into a Config and validates it.
//
// We validate by hand (rather than the `env:"...,required"` tag) so the error
// message can say *why* a value is needed and so requirements can depend on
// other fields — e.g. the Discord webhook is only required when DryRun is off.
func Load() (*Config, error) {
	var c Config
	if err := env.Parse(&c); err != nil {
		return nil, fmt.Errorf("parse config from environment: %w", err)
	}

	if len(c.FeedURLs) == 0 {
		c.FeedURLs = defaultFeeds
	}

	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	return &c, nil
}

func (c *Config) validate() error {
	var errs []error
	if c.GeminiAPIKey == "" {
		errs = append(errs, errors.New("GEMINI_API_KEY is required"))
	}
	if !c.DryRun && c.DiscordWebhookURL == "" {
		errs = append(errs, errors.New("DISCORD_WEBHOOK_URL is required unless DRY_RUN=true"))
	}
	if c.MaxArticles <= 0 {
		errs = append(errs, errors.New("MAX_ARTICLES_PER_RUN must be > 0"))
	}
	return errors.Join(errs...)
}
