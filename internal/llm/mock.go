package llm

import (
	"context"

	"github.com/kaisei0326/cost-aware-note-summarizer/internal/core"
)

// Mock is a test double for Client. It never makes a network call, so tests
// that use it cost nothing and never touch the Gemini free tier.
//
// Each method delegates to an optional function field, letting a test script
// exact behaviour (e.g. "reject this article", "return an error"). When a field
// is nil, a deterministic default is used so the zero-value Mock is still a
// usable, well-behaved client.
type Mock struct {
	TriageFunc    func(ctx context.Context, a core.Article) (TriageResult, error)
	SummarizeFunc func(ctx context.Context, a core.Article) (Summary, error)
}

// compile-time assertion that Mock satisfies the interface.
var _ Client = (*Mock)(nil)

func (m *Mock) Triage(ctx context.Context, a core.Article) (TriageResult, error) {
	if m.TriageFunc != nil {
		return m.TriageFunc(ctx, a)
	}
	return TriageResult{WorthSummarizing: true, Score: 1, Reason: "mock"}, nil
}

func (m *Mock) Summarize(ctx context.Context, a core.Article) (Summary, error) {
	if m.SummarizeFunc != nil {
		return m.SummarizeFunc(ctx, a)
	}
	return Summary{Headline: a.Title, Body: "mock summary", Tags: []string{"mock"}}, nil
}
