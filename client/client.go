package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// transport is the internal seam between "real HTTP client" and "no-op".
// Every method swallows its own errors (logging them) - nothing here ever
// returns an error to application code, by design (README §6.1: LocalOps
// must never be the reason a production job fails).
type transport interface {
	createTask(ctx context.Context, req createTaskRequest) (id string, ok bool)
	// updateTask reports progress and a free-text status message, stored
	// server-side as the task's description - never its lifecycle status.
	updateTask(ctx context.Context, id string, progress int, statusMessage string)
	heartbeat(ctx context.Context, id string)
	completeTask(ctx context.Context, id string)
	failTask(ctx context.Context, id string, errMsg string)
	updateDetails(ctx context.Context, id string, details map[string]any)
	checkIn(ctx context.Context, name string, req checkinRequest)
}

// noopTransport is used whenever LocalOps is disabled or unconfigured.
// Every call is free: no allocation beyond the interface dispatch, no I/O.
type noopTransport struct{}

func (noopTransport) createTask(ctx context.Context, req createTaskRequest) (string, bool) {
	return "", false
}
func (noopTransport) updateTask(ctx context.Context, id string, progress int, statusMessage string) {}
func (noopTransport) heartbeat(ctx context.Context, id string)                             {}
func (noopTransport) completeTask(ctx context.Context, id string)                          {}
func (noopTransport) failTask(ctx context.Context, id string, errMsg string)               {}
func (noopTransport) updateDetails(ctx context.Context, id string, details map[string]any) {}
func (noopTransport) checkIn(ctx context.Context, name string, req checkinRequest)         {}

// httpTransport is the real client, used only when Config.Enabled is true.
type httpTransport struct {
	baseURL string
	server  string
	hc      *http.Client
	log     Logger
}

func newTransport(cfg Config) transport {
	if !cfg.Enabled || cfg.BaseURL == "" {
		return noopTransport{}
	}
	logger := cfg.Logger
	if logger == nil {
		logger = stderrLogger{}
	}
	return &httpTransport{
		baseURL: cfg.BaseURL,
		server:  cfg.Server,
		hc:      &http.Client{Timeout: 5 * time.Second},
		log:     logger,
	}
}

type createTaskRequest struct {
	Server      string         `json:"server"`
	Type        string         `json:"type"`
	Description string         `json:"description"`
	Details     map[string]any `json:"details,omitempty"`
}

type createTaskResponse struct {
	ID string `json:"id"`
}

type checkinRequest struct {
	State   string         `json:"state"`
	Message string         `json:"message,omitempty"`
	Details map[string]any `json:"details,omitempty"`
}

func (t *httpTransport) do(ctx context.Context, method, path string, body any) {
	if _, ok := t.doWithResponse(ctx, method, path, body); !ok {
		return
	}
}

// doWithResponse performs the request and, on a 2xx response, returns the
// raw body and true. Any failure (network, non-2xx, timeout) is logged and
// reported back as ok=false - callers never propagate this as an error.
func (t *httpTransport) doWithResponse(ctx context.Context, method, path string, body any) ([]byte, bool) {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.log.Printf("encode request for %s %s: %v", method, path, err)
			return nil, false
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, t.baseURL+path, &buf)
	if err != nil {
		t.log.Printf("build request for %s %s: %v", method, path, err)
		return nil, false
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.hc.Do(req)
	if err != nil {
		t.log.Printf("request %s %s: %v", method, path, err)
		return nil, false
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.log.Printf("request %s %s: unexpected status %s", method, path, resp.Status)
		return nil, false
	}

	var out bytes.Buffer
	if _, err := out.ReadFrom(resp.Body); err != nil {
		t.log.Printf("read response for %s %s: %v", method, path, err)
		return nil, false
	}
	return out.Bytes(), true
}

func (t *httpTransport) createTask(ctx context.Context, req createTaskRequest) (string, bool) {
	if req.Server == "" {
		req.Server = t.server
	}
	body, ok := t.doWithResponse(ctx, http.MethodPost, "/tasks", req)
	if !ok {
		return "", false
	}
	var out createTaskResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.log.Printf("decode create-task response: %v", err)
		return "", false
	}
	return out.ID, true
}

func (t *httpTransport) updateTask(ctx context.Context, id string, progress int, statusMessage string) {
	t.do(ctx, http.MethodPost, fmt.Sprintf("/tasks/%s/update", id), map[string]any{
		"progress":    progress,
		"description": statusMessage,
	})
}

func (t *httpTransport) heartbeat(ctx context.Context, id string) {
	t.do(ctx, http.MethodPost, fmt.Sprintf("/tasks/%s/heartbeat", id), nil)
}

func (t *httpTransport) completeTask(ctx context.Context, id string) {
	t.do(ctx, http.MethodPost, fmt.Sprintf("/tasks/%s/complete", id), nil)
}

func (t *httpTransport) failTask(ctx context.Context, id string, errMsg string) {
	t.do(ctx, http.MethodPost, fmt.Sprintf("/tasks/%s/fail", id), map[string]any{"error": errMsg})
}

func (t *httpTransport) updateDetails(ctx context.Context, id string, details map[string]any) {
	t.do(ctx, http.MethodPost, fmt.Sprintf("/tasks/%s/update", id), map[string]any{"details": details})
}

func (t *httpTransport) checkIn(ctx context.Context, name string, req checkinRequest) {
	t.do(ctx, http.MethodPost, fmt.Sprintf("/watchers/%s/checkin", name), req)
}
