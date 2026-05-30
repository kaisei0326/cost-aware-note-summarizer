// Package core holds the domain types shared by every stage of the pipeline.
//
// It deliberately imports nothing from the rest of the project, so feed, store,
// llm, notify and pipeline can all depend on it without creating an import
// cycle between themselves. Keeping the canonical Article type in one leaf
// package also means there is a single source of truth for "what is an article"
// instead of each package redefining its own near-duplicate struct.
package core

import "time"

// Article is one feed item, normalized across the different RSS/Atom sources.
type Article struct {
	// ID is the stable identity used for de-duplication. We prefer the feed
	// item's GUID and fall back to its link, so the same article fetched on two
	// different runs (or from two feeds) resolves to the same row in the store.
	ID string

	Title string
	Link  string

	// Source is the feed title (or host) — used to group/label notifications.
	Source string

	// Content is the body/summary text from the feed item. It is the only field
	// we send to the LLM, so the pipeline truncates it to keep token cost bounded.
	Content string

	// Published is the item's publish time, or the zero value if the feed did
	// not provide a parseable timestamp.
	Published time.Time
}
