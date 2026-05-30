package feed

import (
	"testing"

	"github.com/kaisei0326/cost-aware-note-summarizer/internal/core"
)

func TestInterleave(t *testing.T) {
	// a builds an Article identified only by ID — enough to assert ordering.
	a := func(id string) core.Article { return core.Article{ID: id} }

	tests := []struct {
		name    string
		perFeed [][]core.Article
		want    []string
	}{
		{name: "no feeds", perFeed: nil, want: nil},
		{
			name:    "single feed keeps its order",
			perFeed: [][]core.Article{{a("z1"), a("z2"), a("z3")}},
			want:    []string{"z1", "z2", "z3"},
		},
		{
			name:    "two feeds alternate, longer feed's tail follows",
			perFeed: [][]core.Article{{a("z1"), a("z2"), a("z3")}, {a("h1"), a("h2")}},
			want:    []string{"z1", "h1", "z2", "h2", "z3"},
		},
		{
			name:    "three feeds round-robin",
			perFeed: [][]core.Article{{a("z1"), a("z2")}, {a("h1")}, {a("q1"), a("q2")}},
			want:    []string{"z1", "h1", "q1", "z2", "q2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := interleave(tt.perFeed)
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d (got %v)", len(got), len(tt.want), ids(got))
			}
			for i, want := range tt.want {
				if got[i].ID != want {
					t.Errorf("position %d = %q, want %q (full: %v)", i, got[i].ID, want, ids(got))
				}
			}
		})
	}
}

func ids(as []core.Article) []string {
	out := make([]string, len(as))
	for i, a := range as {
		out[i] = a.ID
	}
	return out
}
