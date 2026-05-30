package store

import (
	"context"
	"testing"
	"time"
)

// openTestStore spins up a real SQLite database in a temp dir. modernc.org/sqlite
// is pure Go, so this runs in CI with no C toolchain and no external service.
func openTestStore(t *testing.T) *Store {
	t.Helper()
	path := t.TempDir() + "/test.db"
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestSeen(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	tests := []struct {
		name    string
		save    *Record // saved before the check, if non-nil
		queryID string
		want    bool
	}{
		{name: "unknown id is not seen", queryID: "missing", want: false},
		{
			name:    "saved id is seen",
			save:    &Record{ID: "a1", Title: "t", Link: "l", Published: time.Now()},
			queryID: "a1",
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.save != nil {
				if err := s.Save(ctx, *tt.save); err != nil {
					t.Fatalf("Save: %v", err)
				}
			}
			got, err := s.Seen(ctx, tt.queryID)
			if err != nil {
				t.Fatalf("Seen: %v", err)
			}
			if got != tt.want {
				t.Errorf("Seen(%q) = %v, want %v", tt.queryID, got, tt.want)
			}
		})
	}
}

func TestSaveIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	rec := Record{ID: "dup", Title: "first", Link: "l", Worth: true, Score: 0.5, Tags: []string{"go"}}
	if err := s.Save(ctx, rec); err != nil {
		t.Fatalf("first Save: %v", err)
	}

	// Saving the same ID again must upsert, not error on the PK conflict.
	rec.Title = "second"
	rec.Score = 0.9
	if err := s.Save(ctx, rec); err != nil {
		t.Fatalf("second Save: %v", err)
	}

	seen, err := s.Seen(ctx, "dup")
	if err != nil || !seen {
		t.Fatalf("Seen after upsert = %v, %v; want true, nil", seen, err)
	}
}
