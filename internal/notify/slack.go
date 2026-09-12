// Package notify owns LocalOps' one piece of outbound notification: Slack
// (README §13.4). It never decides *whether* to alert - callers pass in
// pre-decided transition events - and it never retries or remediates.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// Slack sends messages to a single incoming webhook URL. A zero-value
// Slack (empty WebhookURL) is a safe no-op, so notify.Slack{} can be used
// wherever Slack simply isn't configured.
type Slack struct {
	WebhookURL string
	HTTPClient *http.Client
}

func New(webhookURL string) *Slack {
	return &Slack{WebhookURL: webhookURL, HTTPClient: &http.Client{Timeout: 5 * time.Second}}
}

type slackPayload struct {
	Text string `json:"text"`
}

// Send posts text to the configured webhook. Errors are logged, not
// returned as fatal - a Slack outage must never take down LocalOps itself.
func (s *Slack) Send(ctx context.Context, text string) {
	if s == nil || s.WebhookURL == "" {
		return
	}
	body, err := json.Marshal(slackPayload{Text: text})
	if err != nil {
		log.Printf("notify: marshal slack payload: %v", err)
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.WebhookURL, bytes.NewReader(body))
	if err != nil {
		log.Printf("notify: build slack request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	client := s.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("notify: send slack message: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		log.Printf("notify: slack webhook returned %s", resp.Status)
	}
}

// WatcherDown announces a watcher going ok/unknown -> down.
func (s *Slack) WatcherDown(ctx context.Context, name, message string) {
	s.Send(ctx, fmt.Sprintf(":red_circle: *%s* is DOWN — %s", name, orDefault(message, "no message")))
}

// WatcherRecovered announces a watcher going down -> ok.
func (s *Slack) WatcherRecovered(ctx context.Context, name string) {
	s.Send(ctx, fmt.Sprintf(":large_green_circle: *%s* has recovered", name))
}

// TaskStuck announces a task with no heartbeat within its expected window
// (README §8, alerted through the same path as watcher failures rather
// than a second notification mechanism).
func (s *Slack) TaskStuck(ctx context.Context, taskID, description string, minutesSinceHeartbeat int) {
	s.Send(ctx, fmt.Sprintf(":warning: task `%s` (%s) looks stuck — no heartbeat for %dm", taskID, orDefault(description, "no description"), minutesSinceHeartbeat))
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
