// Package feed fetches and normalizes RSS/Atom feeds into core.Article values.
//
// We do the HTTP GET ourselves (with a context-bound *http.Request) and only
// hand the response body to gofeed's Parse. That gives us full control over the
// timeout and cancellation of every network call — context propagation is a
// core Go discipline and matters here because the whole run is wrapped in a
// deadline so a slow feed can never hang the cron job.
package feed

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/kaisei0326/cost-aware-note-summarizer/internal/core"
	"github.com/mmcdole/gofeed"
)

// Fetcher retrieves feeds. It holds the shared HTTP client (so connection reuse
// and the configured timeout apply to every feed) and a logger.
type Fetcher struct {
	client *http.Client
	parser *gofeed.Parser
	log    *slog.Logger
}

func New(client *http.Client, log *slog.Logger) *Fetcher {
	return &Fetcher{client: client, parser: gofeed.NewParser(), log: log}
}

// Fetch downloads every feed URL and returns the de-duplicated articles,
// round-robin interleaved across sources (see interleave). A single failing
// feed is logged and skipped rather than failing the whole run — one flaky
// source should not stop the others.
//
// Interleaving matters because of the per-run cap downstream: if we simply
// concatenated the feeds, the cap would be filled entirely by the first feed
// (which has dozens of items) and later sources would be starved every run.
func (f *Fetcher) Fetch(ctx context.Context, urls []string) ([]core.Article, error) {
	seen := make(map[string]struct{})
	perFeed := make([][]core.Article, 0, len(urls))

	for _, u := range urls {
		items, err := f.fetchOne(ctx, u)
		if err != nil {
			// If the context is done, stop entirely; otherwise skip this feed.
			if ctx.Err() != nil {
				return interleave(perFeed), ctx.Err()
			}
			f.log.WarnContext(ctx, "skipping feed", "url", u, "error", err)
			continue
		}
		// De-dup across all feeds while preserving this feed's own ordering.
		deduped := make([]core.Article, 0, len(items))
		for _, a := range items {
			if _, dup := seen[a.ID]; dup {
				continue
			}
			seen[a.ID] = struct{}{}
			deduped = append(deduped, a)
		}
		perFeed = append(perFeed, deduped)
	}
	return interleave(perFeed), nil
}

// interleave merges the per-feed article lists round-robin: the first item of
// every feed, then the second of every feed, and so on. Feeds that run out are
// simply skipped for the remaining rounds. This gives the downstream cap a mix
// of sources instead of draining one feed before reaching the next.
func interleave(perFeed [][]core.Article) []core.Article {
	total, longest := 0, 0
	for _, items := range perFeed {
		total += len(items)
		if len(items) > longest {
			longest = len(items)
		}
	}

	out := make([]core.Article, 0, total)
	for i := 0; i < longest; i++ {
		for _, items := range perFeed {
			if i < len(items) {
				out = append(out, items[i])
			}
		}
	}
	return out
}

func (f *Fetcher) fetchOne(ctx context.Context, url string) ([]core.Article, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get feed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("feed returned status %d", resp.StatusCode)
	}

	parsed, err := f.parser.Parse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("parse feed: %w", err)
	}

	out := make([]core.Article, 0, len(parsed.Items))
	for _, it := range parsed.Items {
		out = append(out, toArticle(it, parsed))
	}
	return out, nil
}

func toArticle(it *gofeed.Item, parent *gofeed.Feed) core.Article {
	// Prefer the GUID for identity; many feeds set it to a stable permalink.
	// Fall back to the link so we always have *some* dedup key.
	id := it.GUID
	if id == "" {
		id = it.Link
	}

	// Description is the short summary; Content is the (optional) full body.
	body := it.Description
	if it.Content != "" {
		body = it.Content
	}

	a := core.Article{
		ID:      id,
		Title:   it.Title,
		Link:    it.Link,
		Content: body,
	}
	if parent != nil {
		a.Source = parent.Title
	}
	if it.PublishedParsed != nil {
		a.Published = *it.PublishedParsed
	} else if it.UpdatedParsed != nil {
		a.Published = *it.UpdatedParsed
	}
	return a
}
