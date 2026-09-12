// Package watchers implements the Watcher model (README §13): standing
// up/down conditions (an SSH tunnel, a reachability ping, a systemd
// service) that don't fit the Task lifecycle because they have no natural
// end - only state transitions matter.
package watchers

import "time"

// Kind classifies what a watcher checks, purely for display/filtering -
// LocalOps doesn't behave differently per kind.
type Kind string

const (
	KindSSHTunnel Kind = "ssh_tunnel"
	KindPing      Kind = "ping"
	KindCustom    Kind = "custom"
)

// State is a watcher's current condition.
type State string

const (
	StateUnknown  State = "unknown"
	StateOK       State = "ok"
	StateDegraded State = "degraded"
	StateDown     State = "down"
)

// Watcher is the generic standing-condition model (README §13.2).
type Watcher struct {
	ID                int64  `json:"id"`
	Name              string `json:"name"`
	Kind              Kind   `json:"kind"`
	ExpectedIntervalS int    `json:"expected_interval_s"`
	// FailThreshold overrides the server-wide default number of
	// consecutive "down" check-ins required before the first alert
	// (Service.FailThreshold) - 0 means "use the default". A continuous
	// checker that already debounces locally before ever reporting
	// "down" (see scripts/ping-watch.sh) should register with
	// FailThreshold=1, since it will only ever send one "down" check-in
	// per outage episode; a periodic/cron-style checker that reports
	// every check as-is should leave this at 0.
	FailThreshold    int            `json:"fail_threshold,omitempty"`
	State            State          `json:"state"`
	LastCheckAt      *time.Time     `json:"last_check_at,omitempty"`
	LastOkAt         *time.Time     `json:"last_ok_at,omitempty"`
	LastMessage      string         `json:"last_message,omitempty"`
	ConsecutiveFails int            `json:"consecutive_fails"`
	Details          map[string]any `json:"details,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

// Overdue reports whether the watcher hasn't checked in within n times its
// expected interval - itself a "possibly stuck" signal, same spirit as a
// task with no heartbeat.
func (w Watcher) Overdue(now time.Time, n int) bool {
	if w.ExpectedIntervalS <= 0 || w.LastCheckAt == nil {
		return false
	}
	threshold := time.Duration(n) * time.Duration(w.ExpectedIntervalS) * time.Second
	return now.Sub(*w.LastCheckAt) > threshold
}

// Event is a single watcher state transition (README §13.2).
type Event struct {
	ID        int64     `json:"id"`
	WatcherID int64     `json:"watcher_id"`
	State     State     `json:"state"`
	Message   string    `json:"message,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}
