package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"nhooyr.io/websocket"
)

// SocketModeClient runs Slack's Socket Mode protocol: an outbound
// WebSocket connection FROM LocalOps TO Slack, the opposite direction
// from POST /slack/commands. Slash commands arrive over this socket
// instead, so no public HTTP endpoint (Funnel, a reverse proxy, a
// signing secret) is needed at all - trade-off is Slack's Request URL
// field goes unused; this is the alternative delivery path, not an
// addition to it.
type SocketModeClient struct {
	AppToken   string // xapp-... token, Basic Information > App-Level Tokens, connections:write scope
	Server     *Server
	HTTPClient *http.Client
}

func NewSocketModeClient(appToken string, s *Server) *SocketModeClient {
	return &SocketModeClient{AppToken: appToken, Server: s, HTTPClient: &http.Client{Timeout: 10 * time.Second}}
}

// Run connects and reconnects (with backoff) until ctx is cancelled. Meant
// to be launched as `go client.Run(ctx)` from main, alongside the HTTP
// server - the two are independent delivery paths for the same commands.
func (c *SocketModeClient) Run(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		if err := c.runOnce(ctx); err != nil && ctx.Err() == nil {
			log.Printf("slack socket mode: %v (retrying in %s)", err, backoff)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (c *SocketModeClient) runOnce(ctx context.Context) error {
	wssURL, err := c.openConnection(ctx)
	if err != nil {
		return fmt.Errorf("open connection: %w", err)
	}

	conn, _, err := websocket.Dial(ctx, wssURL, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.CloseNow()

	log.Println("slack socket mode: connected")

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}

		var env socketEnvelope
		if err := json.Unmarshal(data, &env); err != nil {
			log.Printf("slack socket mode: bad envelope: %v", err)
			continue
		}

		switch env.Type {
		case "hello":
			// nothing to do - just confirms the connection is ready.
		case "disconnect":
			return fmt.Errorf("server requested disconnect (%s)", env.Reason)
		case "slash_commands":
			c.handleSlashCommand(ctx, conn, env)
		default:
			// interactive, events_api, etc: ack so Slack doesn't retry,
			// but nothing here acts on them yet.
			if env.EnvelopeID != "" {
				_ = c.ack(ctx, conn, env.EnvelopeID, nil)
			}
		}
	}
}

type socketEnvelope struct {
	EnvelopeID             string          `json:"envelope_id"`
	Type                   string          `json:"type"`
	Reason                 string          `json:"reason,omitempty"`
	AcceptsResponsePayload bool            `json:"accepts_response_payload"`
	Payload                json.RawMessage `json:"payload"`
}

type slashCommandPayload struct {
	Command string `json:"command"`
	Text    string `json:"text"`
}

func (c *SocketModeClient) handleSlashCommand(ctx context.Context, conn *websocket.Conn, env socketEnvelope) {
	var p slashCommandPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		log.Printf("slack socket mode: bad slash command payload: %v", err)
		_ = c.ack(ctx, conn, env.EnvelopeID, nil)
		return
	}

	reply := c.Server.dispatchSlackCommand(ctx, p.Command, p.Text)
	if err := c.ack(ctx, conn, env.EnvelopeID, map[string]string{
		"response_type": "ephemeral",
		"text":          reply,
	}); err != nil {
		log.Printf("slack socket mode: ack failed: %v", err)
	}
}

// ack replies over the same socket within Slack's response window -
// payload nil sends a bare acknowledgement (used for event types this
// client doesn't act on); non-nil delivers the command's reply inline,
// same as accepts_response_payload lets us skip a separate response_url
// HTTP call entirely.
func (c *SocketModeClient) ack(ctx context.Context, conn *websocket.Conn, envelopeID string, payload any) error {
	msg := map[string]any{"envelope_id": envelopeID}
	if payload != nil {
		msg["payload"] = payload
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	writeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return conn.Write(writeCtx, websocket.MessageText, b)
}

type openConnectionResponse struct {
	OK    bool   `json:"ok"`
	URL   string `json:"url"`
	Error string `json:"error"`
}

// openConnection calls apps.connections.open to get a fresh, single-use
// WebSocket URL - required before every connect/reconnect, per Slack's
// Socket Mode protocol.
func (c *SocketModeClient) openConnection(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://slack.com/api/apps.connections.open", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.AppToken)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var out openConnectionResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if !out.OK {
		return "", fmt.Errorf("slack api error: %s", out.Error)
	}
	return out.URL, nil
}
