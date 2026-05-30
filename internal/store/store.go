// Package store persists processed articles in SQLite and answers the question
// the pipeline asks before spending any LLM tokens: "have we already seen this?"
//
// This "already-processed" filter is cost lever #1 — the cheapest possible
// gate, a single indexed primary-key lookup, that prevents us from ever sending
// the same article to the LLM twice.
//
// The driver is modernc.org/sqlite, a pure-Go SQLite implementation (no CGo).
// That choice is deliberate: with no C compiler in the loop the whole program
// builds to a single static binary, which is what lets the Docker image use a
// scratch base and come out at a few MB. (mattn/go-sqlite3 would force CGo and
// a libc in the runtime image.)
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

// Store wraps the database handle. *sql.DB is a connection pool, safe for
// concurrent use, so we hold exactly one for the process lifetime.
type Store struct {
	db *sql.DB
}

// Record is the persisted form of a processed article. It is intentionally a
// store-owned type (not llm.Summary or core.Article) so the storage layer has
// no dependency on the LLM or feed packages — the pipeline maps results into it.
type Record struct {
	ID        string
	Title     string
	Link      string
	Source    string
	Published time.Time

	// Worth records the stage-1 triage verdict. We store rejected articles too
	// (Worth=false), otherwise we would re-triage them — and pay for it — on
	// every future run.
	Worth bool
	Score float64

	// Summary fields are only populated when Worth is true.
	Headline string
	Summary  string
	Tags     []string
}

const schema = `
CREATE TABLE IF NOT EXISTS articles (
    id           TEXT PRIMARY KEY,
    title        TEXT NOT NULL,
    link         TEXT NOT NULL,
    source       TEXT,
    published    TIMESTAMP,
    worth        INTEGER NOT NULL DEFAULT 0,
    score        REAL,
    headline     TEXT,
    summary      TEXT,
    tags         TEXT, -- JSON array
    processed_at TIMESTAMP NOT NULL
);`

// Open opens (creating the parent dir and file if needed) the SQLite database
// and applies the schema. The context bounds the migration step.
func Open(ctx context.Context, path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create db dir %q: %w", dir, err)
		}
	}

	// busy_timeout makes concurrent writers wait instead of erroring out;
	// harmless for a single-process job but a good default.
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", path, err)
	}

	if _, err := db.ExecContext(ctx, schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close releases the underlying connection pool.
func (s *Store) Close() error { return s.db.Close() }

// Seen reports whether an article with this ID has already been processed.
func (s *Store) Seen(ctx context.Context, id string) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM articles WHERE id = ? LIMIT 1`, id).Scan(&exists)
	switch {
	case err == sql.ErrNoRows:
		return false, nil
	case err != nil:
		return false, fmt.Errorf("query seen %q: %w", id, err)
	default:
		return true, nil
	}
}

// Save upserts a processed article. We use INSERT .. ON CONFLICT so a re-run
// that somehow reaches an already-stored article updates it rather than failing.
func (s *Store) Save(ctx context.Context, r Record) error {
	tags, err := json.Marshal(r.Tags)
	if err != nil {
		return fmt.Errorf("marshal tags: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
        INSERT INTO articles (id, title, link, source, published, worth, score, headline, summary, tags, processed_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(id) DO UPDATE SET
            worth=excluded.worth, score=excluded.score, headline=excluded.headline,
            summary=excluded.summary, tags=excluded.tags, processed_at=excluded.processed_at`,
		r.ID, r.Title, r.Link, r.Source, r.Published,
		r.Worth, r.Score, r.Headline, r.Summary, string(tags), time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("save article %q: %w", r.ID, err)
	}
	return nil
}
