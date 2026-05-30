package pipeline

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/kaisei0326/cost-aware-note-summarizer/internal/core"
	"github.com/kaisei0326/cost-aware-note-summarizer/internal/llm"
	"github.com/kaisei0326/cost-aware-note-summarizer/internal/notify"
	"github.com/kaisei0326/cost-aware-note-summarizer/internal/store"
)

// fakeFetcher returns a fixed list of articles, no network involved.
type fakeFetcher struct{ articles []core.Article }

func (f fakeFetcher) Fetch(context.Context, []string) ([]core.Article, error) {
	return f.articles, nil
}

// countingNotifier records how many messages it "delivered".
type countingNotifier struct{ count int }

func (c *countingNotifier) Notify(context.Context, notify.Message) error {
	c.count++
	return nil
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func articles(ids ...string) []core.Article {
	out := make([]core.Article, len(ids))
	for i, id := range ids {
		out[i] = core.Article{ID: id, Title: "title-" + id, Link: "https://x/" + id, Content: "body"}
	}
	return out
}

// TestRun_Cascade is the headline test: it drives the full cascade with a Mock
// LLM (zero API cost) and asserts the cost levers actually fire.
func TestRun_Cascade(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name        string
		feed        []core.Article
		preseed     []string // article IDs already in the store (lever 1)
		maxArticles int      // per-run cap (lever 2)
		triage      func(ctx context.Context, a core.Article) (llm.TriageResult, error)
		wantNew     int
		wantWorth   int
		wantSummed  int
		wantNotify  int
	}{
		{
			name:        "all new and all worth -> all summarized & notified",
			feed:        articles("a", "b", "c"),
			maxArticles: 10,
			wantNew:     3, wantWorth: 3, wantSummed: 3, wantNotify: 3,
		},
		{
			name:        "lever 1: already-seen articles are skipped before any LLM call",
			feed:        articles("a", "b", "c"),
			preseed:     []string{"a", "b"},
			maxArticles: 10,
			wantNew:     1, wantWorth: 1, wantSummed: 1, wantNotify: 1,
		},
		{
			name:        "lever 2: per-run cap bounds how many new articles we process",
			feed:        articles("a", "b", "c", "d", "e"),
			maxArticles: 2,
			wantNew:     2, wantWorth: 2, wantSummed: 2, wantNotify: 2,
		},
		{
			name:        "lever 3: triage rejection stops short of the expensive summary",
			feed:        articles("a", "b"),
			maxArticles: 10,
			triage: func(_ context.Context, a core.Article) (llm.TriageResult, error) {
				// reject "a", accept "b"
				return llm.TriageResult{WorthSummarizing: a.ID == "b", Score: 0.5}, nil
			},
			wantNew: 2, wantWorth: 1, wantSummed: 1, wantNotify: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st, err := store.Open(ctx, t.TempDir()+"/p.db")
			if err != nil {
				t.Fatalf("store.Open: %v", err)
			}
			defer st.Close()

			for _, id := range tt.preseed {
				if err := st.Save(ctx, store.Record{ID: id, Title: "seed", Link: "l"}); err != nil {
					t.Fatalf("preseed: %v", err)
				}
			}

			notifier := &countingNotifier{}
			p := New(
				fakeFetcher{articles: tt.feed},
				st,
				&llm.Mock{TriageFunc: tt.triage},
				notifier,
				[]string{"unused"},
				tt.maxArticles,
				quietLogger(),
			)

			got, err := p.Run(ctx)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got.New != tt.wantNew {
				t.Errorf("New = %d, want %d", got.New, tt.wantNew)
			}
			if got.Worth != tt.wantWorth {
				t.Errorf("Worth = %d, want %d", got.Worth, tt.wantWorth)
			}
			if got.Summarized != tt.wantSummed {
				t.Errorf("Summarized = %d, want %d", got.Summarized, tt.wantSummed)
			}
			if got.Notified != tt.wantNotify {
				t.Errorf("Notified = %d, want %d", got.Notified, tt.wantNotify)
			}
			if notifier.count != tt.wantNotify {
				t.Errorf("notifier delivered %d, want %d", notifier.count, tt.wantNotify)
			}
		})
	}
}

// TestRun_SeenAfterProcessing verifies a processed article is not re-processed
// on a second run — the persistent side of cost lever 1.
func TestRun_SeenAfterProcessing(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir()+"/p.db")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	p := New(fakeFetcher{articles: articles("a", "b")}, st, &llm.Mock{}, notify.Noop{}, nil, 10, quietLogger())

	first, err := p.Run(ctx)
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if first.New != 2 {
		t.Fatalf("first run New = %d, want 2", first.New)
	}

	second, err := p.Run(ctx)
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if second.New != 0 {
		t.Errorf("second run New = %d, want 0 (already seen)", second.New)
	}
}
