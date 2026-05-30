// Package notify delivers finished summaries to a chat channel.
//
// Like llm.Client, Notifier is an interface so the pipeline does not care how
// (or whether) messages are delivered. That gives us a Noop implementation for
// DRY_RUN / tests, and room to add Slack later without touching the pipeline.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
)

// Message is the channel-agnostic payload the pipeline produces.
type Message struct {
	Title   string
	URL     string
	Summary string
	Source  string
	Tags    []string
}

// Notifier sends one message.
type Notifier interface {
	Notify(ctx context.Context, m Message) error
}

// --- Discord ---

// Discord posts to an Incoming Webhook URL. Discord webhooks need no app/bot
// token — the URL itself is the credential, which is why it is stored as a
// GitHub Secret and injected via the environment.
type Discord struct {
	webhookURL string
	http       *http.Client
	log        *slog.Logger
}

func NewDiscord(webhookURL string, client *http.Client, log *slog.Logger) *Discord {
	return &Discord{webhookURL: webhookURL, http: client, log: log}
}

var _ Notifier = (*Discord)(nil)

// discordPayload mirrors the subset of the webhook schema we use. A single rich
// "embed" renders the summary with a clickable title and a footer of tags.
type discordPayload struct {
	Embeds []discordEmbed `json:"embeds"`
}

type discordEmbed struct {
	Title       string        `json:"title"`
	URL         string        `json:"url,omitempty"`
	Description string        `json:"description"`
	Footer      discordFooter `json:"footer"`
}

type discordFooter struct {
	Text string `json:"text"`
}

func (d *Discord) Notify(ctx context.Context, m Message) error {
	footer := m.Source
	if len(m.Tags) > 0 {
		footer = fmt.Sprintf("%s · %s", m.Source, strings.Join(m.Tags, ", "))
	}

	body, err := json.Marshal(discordPayload{Embeds: []discordEmbed{{
		Title:       truncate(m.Title, 256), // Discord caps embed titles at 256
		URL:         m.URL,
		Description: truncate(m.Summary, 4096),
		Footer:      discordFooter{Text: truncate(footer, 2048)},
	}}})
	if err != nil {
		return fmt.Errorf("marshal discord payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build discord request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.http.Do(req)
	if err != nil {
		return fmt.Errorf("post to discord: %w", err)
	}
	defer resp.Body.Close()

	// A successful webhook returns 204 No Content (or 200 with ?wait=true).
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("discord returned status %d", resp.StatusCode)
	}
	return nil
}

// --- Noop ---

// Noop satisfies Notifier without sending anything. Used for DRY_RUN and tests.
type Noop struct {
	Log *slog.Logger
}

var _ Notifier = (*Noop)(nil)

func (n Noop) Notify(ctx context.Context, m Message) error {
	if n.Log != nil {
		n.Log.InfoContext(ctx, "dry-run: skipping notification", "title", m.Title, "url", m.URL)
	}
	return nil
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}
