package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// defaultControlPollInterval is how often a real (non-no-op) task handle
// polls LocalOps for an externally requested cancel/pause/stop, so that
// context.Context integration (README §7) works without the caller doing
// anything beyond passing ctx through as usual.
const defaultControlPollInterval = 5 * time.Second

var (
	mu      sync.RWMutex
	current = struct {
		cfg       Config
		transport transport
	}{transport: noopTransport{}}
)

// Init configures the process-wide LocalOps client. Call it once at
// startup (main.go / internal/config), after LoadEnvFile + ConfigFromEnv
// if you're using env-based config:
//
//	client.LoadEnvFile("")
//	client.Init(client.ConfigFromEnv())
//
// Every subsequent Start/CheckIn call in the process uses this
// configuration implicitly. Calling Init again replaces it (mainly useful
// in tests).
func Init(cfg Config) {
	mu.Lock()
	defer mu.Unlock()
	current.cfg = cfg
	current.transport = newTransport(cfg)
}

// Enabled reports whether the process-wide client is currently configured
// to make real network calls (vs. running as a no-op).
func Enabled() bool {
	mu.RLock()
	defer mu.RUnlock()
	return current.cfg.Enabled
}

func activeTransport() transport {
	mu.RLock()
	defer mu.RUnlock()
	return current.transport
}

// Task describes a unit of work to start tracking. Server and Type
// classify it (see README §5); Details is arbitrary JSON the application
// understands and LocalOps just stores and returns.
type Task struct {
	Server      string // defaults to the process-wide Config.Server if empty
	Type        string
	Description string
	Details     map[string]any
}

// Handle is a live reference to a started task. All methods are safe to
// call even when LocalOps is disabled/unreachable - they become no-ops.
type Handle struct {
	id     string
	tr     transport
	active bool // false for a no-op handle: nothing to do, ever

	ctx    context.Context
	cancel context.CancelFunc

	pollOnce sync.Once
	closed   atomic.Bool
}

// Start begins tracking a task and returns a Handle. ctx is the parent
// context for the task's lifetime; Handle.Context() returns a derived
// context that LocalOps can also cancel remotely (a "cancel" or "stop"
// control action - README §7), so workers should select on it exactly
// like any other context:
//
//	task := tracker.Start(ctx, tracker.Task{Type: "sync", Description: "v2sync retry"})
//	defer task.Complete()
//	select {
//	case <-task.Context().Done():
//	    return task.Context().Err()
//	default:
//	}
//
// When the client is disabled, Start performs zero network calls and
// Handle.Context() is simply ctx itself (no polling goroutine spawned).
func Start(ctx context.Context, task Task) *Handle {
	tr := activeTransport()

	id, ok := tr.createTask(ctx, createTaskRequest{
		Server:      task.Server,
		Type:        task.Type,
		Description: task.Description,
		Details:     task.Details,
	})
	if !ok {
		derived, cancel := context.WithCancel(ctx)
		h := &Handle{tr: noopTransport{}, active: false, ctx: derived, cancel: cancel}
		// No polling: a disabled/unreachable client never spawns goroutines.
		return h
	}

	derived, cancel := context.WithCancel(ctx)
	h := &Handle{id: id, tr: tr, active: true, ctx: derived, cancel: cancel}
	h.startControlPolling()
	return h
}

// ID returns the LocalOps task ID, or "" for a no-op handle.
func (h *Handle) ID() string { return h.id }

// Context returns a context derived from the one passed to Start, which is
// cancelled either when the parent is cancelled or when LocalOps records
// an external cancel/stop request for this task.
func (h *Handle) Context() context.Context { return h.ctx }

// Update reports progress (0-100) and a human-readable status message -
// free text describing what the task is doing right now (e.g. "Fetching
// pending records", "connected", "not connected"). This is NOT the task's
// lifecycle status (created/running/completed/...); that only ever
// changes via Complete/Fail or a server-side control action. Free text
// here is stored in the task's description field for exactly that reason.
func (h *Handle) Update(progress int, status string) {
	if !h.active {
		return
	}
	h.tr.updateTask(h.ctx, h.id, progress, status)
}

// UpdateDetails merges/replaces the task's arbitrary JSON details.
func (h *Handle) UpdateDetails(details map[string]any) {
	if !h.active {
		return
	}
	h.tr.updateDetails(h.ctx, h.id, details)
}

// Heartbeat records liveness without changing progress - use this from a
// long inner loop so LocalOps can tell "still working" from "stuck"
// (README §8).
func (h *Handle) Heartbeat() {
	if !h.active {
		return
	}
	h.tr.heartbeat(h.ctx, h.id)
}

// Complete marks the task as successfully finished and stops control
// polling.
func (h *Handle) Complete() {
	if !h.active {
		h.cancel()
		return
	}
	h.tr.completeTask(h.ctx, h.id)
	h.stop()
}

// Fail marks the task as failed with err's message and stops control
// polling. Safe to call with a nil err (records a generic failure).
func (h *Handle) Fail(err error) {
	if !h.active {
		h.cancel()
		return
	}
	msg := "failed"
	if err != nil {
		msg = err.Error()
	}
	h.tr.failTask(h.ctx, h.id, msg)
	h.stop()
}

func (h *Handle) stop() {
	if h.closed.CompareAndSwap(false, true) {
		h.cancel()
	}
}

// startControlPolling launches a goroutine that periodically asks LocalOps
// whether this task has been remotely cancelled/stopped, and cancels
// Handle.Context() if so. It exits as soon as the handle is stopped
// (Complete/Fail) or its context is done for any other reason.
func (h *Handle) startControlPolling() {
	h.pollOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(defaultControlPollInterval)
			defer ticker.Stop()
			for {
				select {
				case <-h.ctx.Done():
					return
				case <-ticker.C:
					if h.closed.Load() {
						return
					}
					if requested, ok := pollControl(h.ctx, h.tr, h.id); ok && requested {
						h.cancel()
						return
					}
				}
			}
		}()
	})
}

// pollControl is split out so it can go through the same transport seam;
// it uses the httpTransport's doWithResponse via a tiny local type switch
// rather than growing the transport interface for a single GET.
func pollControl(ctx context.Context, tr transport, id string) (cancelRequested bool, ok bool) {
	ht, isHTTP := tr.(*httpTransport)
	if !isHTTP {
		return false, false
	}
	body, ok := ht.doWithResponse(ctx, http.MethodGet, fmt.Sprintf("/tasks/%s/control", id), nil)
	if !ok {
		return false, false
	}
	var resp struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return false, false
	}
	switch resp.Action {
	case "cancel", "stop":
		return true, true
	default:
		return false, true
	}
}
