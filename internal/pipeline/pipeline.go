// Package pipeline wires the stages together and implements the cost-aware
// cascade. It is the orchestration layer: it owns no I/O details itself, only
// the *policy* of "what runs, in what order, and when do we stop spending".
//
// The three cost levers all live here, in the order they fire (cheapest first):
//
//	Lever 1 — already-seen filter: a store lookup skips anything we've processed,
//	          so the LLM never sees the same article twice.
//	Lever 2 — per-run cap: we process at most MaxArticles new items, bounding
//	          how much we can possibly spend on any single run (free-tier guard).
//	Lever 3 — cascade triage: a cheap Flash call decides worth; only the winners
//	          reach the expensive Summarize call.
package pipeline

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/kaisei0326/cost-aware-note-summarizer/internal/core"
	"github.com/kaisei0326/cost-aware-note-summarizer/internal/llm"
	"github.com/kaisei0326/cost-aware-note-summarizer/internal/notify"
	"github.com/kaisei0326/cost-aware-note-summarizer/internal/store"
)

// Fetcher is the feed-fetching dependency, defined here (at the consumer) as a
// minimal interface rather than importing feed.Fetcher concretely. This is the
// idiomatic Go "accept interfaces" rule, and it pays off directly in tests: the
// pipeline test injects a fake that returns canned articles, so the whole
// cascade runs with no network and no real feed.
type Fetcher interface {
	Fetch(ctx context.Context, urls []string) ([]core.Article, error)
}

// Pipeline depends on three interfaces (Fetcher, llm.Client, notify.Notifier)
// plus the store. Depending on interfaces — not concrete types — is what lets
// the tests run the entire cascade with a Mock LLM and a Noop notifier, end to
// end, for free.
type Pipeline struct {
	fetcher  Fetcher
	store    *store.Store
	llm      llm.Client
	notifier notify.Notifier

	feeds       []string
	maxArticles int
	log         *slog.Logger
}

func New(fetcher Fetcher, st *store.Store, client llm.Client, notifier notify.Notifier, feeds []string, maxArticles int, log *slog.Logger) *Pipeline {
	return &Pipeline{
		fetcher:     fetcher,
		store:       st,
		llm:         client,
		notifier:    notifier,
		feeds:       feeds,
		maxArticles: maxArticles,
		log:         log,
	}
}

// Stats summarizes one run, mostly for the closing log line and tests.
type Stats struct {
	Fetched    int // total items returned by the feeds
	New        int // not previously seen (passed lever 1)
	Triaged    int // sent through stage-1 triage
	Worth      int // triage said "summarize this"
	Summarized int // stage-2 summaries produced
	Notified   int // delivered to the channel
}

// Run executes one full pass. It returns Stats plus the first fatal error.
// Per-article failures are logged and skipped so one bad article never aborts
// the batch — but a context cancellation/timeout stops the whole run.
func (p *Pipeline) Run(ctx context.Context) (Stats, error) {
	var st Stats

	articles, err := p.fetcher.Fetch(ctx, p.feeds)
	if err != nil {
		return st, fmt.Errorf("fetch feeds: %w", err)
	}
	st.Fetched = len(articles)
	p.log.InfoContext(ctx, "fetched feeds", "count", st.Fetched, "feeds", len(p.feeds))

	for _, a := range articles {
		// Stop cleanly if the run deadline fired between articles.
		if ctx.Err() != nil {
			return st, ctx.Err()
		}
		// Lever 2: never process more than the per-run cap of *new* articles.
		if st.New >= p.maxArticles {
			p.log.InfoContext(ctx, "reached per-run cap", "cap", p.maxArticles)
			break
		}

		// A child logger carrying article_id tags every downstream line, so a
		// single article's journey through the cascade is greppable end to end.
		log := p.log.With("article_id", a.ID, "source", a.Source)

		// Lever 1: cheapest possible gate — have we already handled this?
		seen, err := p.store.Seen(ctx, a.ID)
		if err != nil {
			log.ErrorContext(ctx, "seen check failed", "error", err)
			continue
		}
		if seen {
			continue
		}
		st.New++

		if err := p.process(ctx, log, a, &st); err != nil {
			log.ErrorContext(ctx, "processing failed", "error", err)
			// keep going with the next article
		}
	}

	p.log.InfoContext(ctx, "run complete",
		"fetched", st.Fetched, "new", st.New, "worth", st.Worth,
		"summarized", st.Summarized, "notified", st.Notified)
	return st, nil
}

// process runs the cascade for a single new article and persists the outcome.
func (p *Pipeline) process(ctx context.Context, log *slog.Logger, a core.Article, st *Stats) error {
	// Lever 3, stage 1: cheap triage.
	st.Triaged++
	verdict, err := p.llm.Triage(ctx, a)
	if err != nil {
		return fmt.Errorf("triage: %w", err)
	}
	log.InfoContext(ctx, "triaged", "worth", verdict.WorthSummarizing, "score", verdict.Score)

	rec := store.Record{
		ID:        a.ID,
		Title:     a.Title,
		Link:      a.Link,
		Source:    a.Source,
		Published: a.Published,
		Worth:     verdict.WorthSummarizing,
		Score:     verdict.Score,
	}

	// If triage rejects it, we STILL persist the record (Worth=false). That is
	// what stops us from re-triaging — and paying for — the same article next
	// run. Then we return early, never reaching the expensive stage 2.
	if !verdict.WorthSummarizing {
		return p.store.Save(ctx, rec)
	}
	st.Worth++

	// Lever 3, stage 2: expensive summary, only for articles worth it.
	sum, err := p.llm.Summarize(ctx, a)
	if err != nil {
		// Persist the triage verdict anyway so we don't re-triage next time.
		_ = p.store.Save(ctx, rec)
		return fmt.Errorf("summarize: %w", err)
	}
	st.Summarized++
	rec.Headline = sum.Headline
	rec.Summary = sum.Body
	rec.Tags = sum.Tags

	// Persist before notifying: a delivered-but-unsaved summary would be
	// re-sent on the next run, whereas a saved-but-unsent one is just a missed
	// notification. Saving first is the safer ordering.
	if err := p.store.Save(ctx, rec); err != nil {
		return fmt.Errorf("save: %w", err)
	}

	if err := p.notifier.Notify(ctx, notify.Message{
		Title:   sum.Headline,
		URL:     a.Link,
		Summary: sum.Body,
		Source:  a.Source,
		Tags:    sum.Tags,
	}); err != nil {
		return fmt.Errorf("notify: %w", err)
	}
	st.Notified++
	log.InfoContext(ctx, "notified")
	return nil
}
