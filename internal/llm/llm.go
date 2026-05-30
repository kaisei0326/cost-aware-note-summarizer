// Package llm defines the LLM client abstraction used by the pipeline.
//
// The Client interface is the single most important design decision in this
// project for two reasons:
//
//  1. Cost. The whole point of the pipeline is to spend as few (and as cheap)
//     LLM calls as possible. Tests must NOT hit the real Gemini API — that
//     would burn the free-tier quota and make CI slow and flaky. By depending
//     on this interface, the pipeline can be tested against Mock for free.
//
//  2. Cascading. The two methods model the two cascade stages explicitly:
//     Triage is the cheap stage-1 gate (a Flash model deciding "is this even
//     worth summarizing?"), and Summarize is the expensive stage-2 work (a Pro
//     model), only ever called for articles that passed triage.
package llm

import (
	"context"

	"github.com/kaisei0326/cost-aware-note-summarizer/internal/core"
)

// TriageResult is the stage-1 verdict.
type TriageResult struct {
	WorthSummarizing bool
	Score            float64 // 0..1 confidence/usefulness, for logging & storage
	Reason           string
}

// Summary is the stage-2 output.
type Summary struct {
	Headline string
	Body     string
	Tags     []string
}

// Client is implemented by the real Gemini client and by Mock.
type Client interface {
	// Triage runs the cheap model to decide whether an article deserves a full
	// summary. Implementations should keep the prompt and output tiny.
	Triage(ctx context.Context, a core.Article) (TriageResult, error)

	// Summarize runs the more capable model to produce the detailed summary.
	Summarize(ctx context.Context, a core.Article) (Summary, error)
}
