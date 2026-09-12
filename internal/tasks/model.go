// Package tasks implements the generic task model (README §4, §9): a
// small fixed set of fields plus an arbitrary JSON `details` blob,
// persisted with a lifecycle event history.
package tasks

import "time"

// Status values form the state machine described in README §9:
//
//	created -> queued -> running -> paused -> completed
//	                               -> failed
//	                               -> cancelled / stopped
type Status string

const (
	StatusCreated   Status = "created"
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusPaused    Status = "paused"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
	StatusStopped   Status = "stopped"
)

// Terminal reports whether the status ends the task's lifecycle - no
// further updates/heartbeats/control actions are meaningful after this.
func (s Status) Terminal() bool {
	switch s {
	case StatusCompleted, StatusFailed, StatusCancelled, StatusStopped:
		return true
	default:
		return false
	}
}

// ControlAction is a control signal a caller can request (README §7).
// LocalOps only records the request; the integrated application is
// responsible for noticing it (via the SDK's context polling, or a plain
// GET) and acting on it.
type ControlAction string

const (
	ControlNone   ControlAction = ""
	ControlCancel ControlAction = "cancel"
	ControlPause  ControlAction = "pause"
	ControlResume ControlAction = "resume"
	ControlStop   ControlAction = "stop"
)

// Task is the generic task model (README §4). Details is arbitrary,
// application-owned JSON - LocalOps never interprets it.
type Task struct {
	ID                 string         `json:"id"`
	Server             string         `json:"server"`
	Type               string         `json:"type"`
	Status             Status         `json:"status"`
	Progress           int            `json:"progress"`
	Description        string         `json:"description"`
	PID                *int           `json:"pid,omitempty"`
	Details            map[string]any `json:"details,omitempty"`
	Error              string         `json:"error,omitempty"`
	ControlAction      ControlAction  `json:"control_action,omitempty"`
	ExpectedHeartbeatS int            `json:"expected_heartbeat_s"`
	CreatedAt          time.Time      `json:"created_at"`
	StartedAt          *time.Time     `json:"started_at,omitempty"`
	UpdatedAt          time.Time      `json:"updated_at"`
	CompletedAt        *time.Time     `json:"completed_at,omitempty"`
	LastHeartbeatAt    *time.Time     `json:"last_heartbeat_at,omitempty"`
}

// Stuck reports whether the task is running but hasn't heartbeat within N
// times its expected interval (README §8). n is the multiplier (e.g. 3).
func (t Task) Stuck(now time.Time, n int) bool {
	if t.Status != StatusRunning || t.ExpectedHeartbeatS <= 0 {
		return false
	}
	last := t.StartedAt
	if t.LastHeartbeatAt != nil {
		last = t.LastHeartbeatAt
	}
	if last == nil {
		last = &t.CreatedAt
	}
	threshold := time.Duration(n) * time.Duration(t.ExpectedHeartbeatS) * time.Second
	return now.Sub(*last) > threshold
}

// Event is a single lifecycle event (README §9): CREATED, STARTED,
// PROGRESS, COMPLETED, FAILED, CANCELLED, etc.
type Event struct {
	ID        int64     `json:"id"`
	TaskID    string    `json:"task_id"`
	Event     string    `json:"event"`
	Message   string    `json:"message,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}
