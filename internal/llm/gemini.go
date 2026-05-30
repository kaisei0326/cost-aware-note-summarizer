package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/kaisei0326/cost-aware-note-summarizer/internal/core"
)

// geminiBaseURL is the Generative Language REST endpoint. We talk to it with
// plain net/http instead of pulling in the Gemini SDK: the contract is a single
// JSON POST, hand-rolling it keeps dependencies (and the scratch binary) small,
// and it makes the request/response shape explicit — easy to explain in an
// interview and easy to keep behind the Client interface.
const geminiBaseURL = "https://generativelanguage.googleapis.com/v1beta/models"

// Gemini is the production Client. It holds two model names so a single client
// drives both cascade stages (cheap triage model, capable summary model).
type Gemini struct {
	apiKey       string
	triageModel  string
	summaryModel string
	maxChars     int
	http         *http.Client
	log          *slog.Logger
}

func NewGemini(apiKey, triageModel, summaryModel string, maxChars int, client *http.Client, log *slog.Logger) *Gemini {
	return &Gemini{
		apiKey:       apiKey,
		triageModel:  triageModel,
		summaryModel: summaryModel,
		maxChars:     maxChars,
		http:         client,
		log:          log,
	}
}

// --- Gemini request/response wire types (only the fields we use) ---

type genRequest struct {
	Contents         []genContent     `json:"contents"`
	GenerationConfig genGenerationCfg `json:"generationConfig"`
}

type genContent struct {
	Parts []genPart `json:"parts"`
}

type genPart struct {
	Text string `json:"text"`
}

type genGenerationCfg struct {
	Temperature float64 `json:"temperature"`
	// Asking for application/json makes Gemini return strict JSON we can
	// unmarshal directly, instead of prose we'd have to scrape.
	ResponseMIMEType string `json:"responseMimeType"`
}

type genResponse struct {
	Candidates []struct {
		Content genContent `json:"content"`
	} `json:"candidates"`
}

func (g *Gemini) Triage(ctx context.Context, a core.Article) (TriageResult, error) {
	prompt := fmt.Sprintf(`あなたは技術記事のトリアージ担当です。
次の記事が「詳細な要約を作る価値があるか」を判定してください。
ノイズ・宣伝・内容の薄い記事は false にしてください。
必ず次のJSON形式のみで答えてください（前後に文章を付けない）:
{"worth_summarizing": <bool>, "score": <0.0-1.0>, "reason": "<日本語で簡潔に>"}

タイトル: %s
本文: %s`, a.Title, g.truncate(a.Content))

	raw, err := g.generate(ctx, g.triageModel, prompt)
	if err != nil {
		return TriageResult{}, fmt.Errorf("triage %q: %w", a.ID, err)
	}

	var out struct {
		Worth  bool    `json:"worth_summarizing"`
		Score  float64 `json:"score"`
		Reason string  `json:"reason"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return TriageResult{}, fmt.Errorf("triage %q: decode model output: %w", a.ID, err)
	}
	return TriageResult{WorthSummarizing: out.Worth, Score: out.Score, Reason: out.Reason}, nil
}

func (g *Gemini) Summarize(ctx context.Context, a core.Article) (Summary, error) {
	prompt := fmt.Sprintf(`次の技術記事を日本語で要約してください。
必ず次のJSON形式のみで答えてください（前後に文章を付けない）:
{"headline": "<1行の見出し>", "body": "<3〜5文の要約>", "tags": ["<関連技術タグ>", ...]}

タイトル: %s
本文: %s`, a.Title, g.truncate(a.Content))

	raw, err := g.generate(ctx, g.summaryModel, prompt)
	if err != nil {
		return Summary{}, fmt.Errorf("summarize %q: %w", a.ID, err)
	}

	var out struct {
		Headline string   `json:"headline"`
		Body     string   `json:"body"`
		Tags     []string `json:"tags"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return Summary{}, fmt.Errorf("summarize %q: decode model output: %w", a.ID, err)
	}
	return Summary(out), nil
}

// generate POSTs one prompt to a model and returns the text of the first
// candidate. The API key goes in the query string per the REST API contract.
func (g *Gemini) generate(ctx context.Context, model, prompt string) (string, error) {
	body, err := json.Marshal(genRequest{
		Contents:         []genContent{{Parts: []genPart{{Text: prompt}}}},
		GenerationConfig: genGenerationCfg{Temperature: 0.2, ResponseMIMEType: "application/json"},
	})
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/%s:generateContent?key=%s", geminiBaseURL, model, g.apiKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("call gemini: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Read a bounded amount of the error body for context without risking
		// an unbounded read on a misbehaving endpoint.
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(http.MaxBytesReader(nil, resp.Body, 2048))
		return "", fmt.Errorf("gemini status %d: %s", resp.StatusCode, buf.String())
	}

	var parsed genResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if len(parsed.Candidates) == 0 || len(parsed.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("gemini returned no candidates")
	}
	return parsed.Candidates[0].Content.Parts[0].Text, nil
}

// truncate bounds the characters sent to the model. Fewer input tokens directly
// lowers cost and latency — a small but real cost lever applied on every call.
func (g *Gemini) truncate(s string) string {
	if g.maxChars <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= g.maxChars {
		return s
	}
	return string(r[:g.maxChars])
}
