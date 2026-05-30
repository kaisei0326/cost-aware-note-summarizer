package llm

import "testing"

func TestDecodeFirstJSON(t *testing.T) {
	type triage struct {
		Worth bool    `json:"worth_summarizing"`
		Score float64 `json:"score"`
	}

	tests := []struct {
		name      string
		raw       string
		wantWorth bool
		wantErr   bool
	}{
		{
			name:      "clean object",
			raw:       `{"worth_summarizing":true,"score":0.8}`,
			wantWorth: true,
		},
		{
			// The exact failure seen in production: a valid object followed by
			// stray content. json.Unmarshal errors here; decodeFirstJSON must not.
			name:      "trailing junk after the object is ignored",
			raw:       `{"worth_summarizing":true,"score":0.8}"oops"`,
			wantWorth: true,
		},
		{
			name:      "trailing second object is ignored",
			raw:       "{\"worth_summarizing\":false,\"score\":0.1}\n{\"worth_summarizing\":true}",
			wantWorth: false,
		},
		{
			name:    "leading prose is still an error",
			raw:     `sure thing: {"worth_summarizing":true}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out triage
			err := decodeFirstJSON(tt.raw, &out)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if out.Worth != tt.wantWorth {
				t.Errorf("Worth = %v, want %v", out.Worth, tt.wantWorth)
			}
		})
	}
}
