package client

import "context"

// CheckIn reports a watcher's outcome for this check (README §13.3). It is
// the Go-side equivalent of the two-line curl call a bash checker would
// use against POST /watchers/:name/checkin - use this when the checker
// happens to be Go code instead of a shell script; no registration call is
// required first, LocalOps creates the watcher on first check-in.
//
// state should be "ok", "degraded", or "down". message and details are
// optional context ("ping timeout", latency numbers, etc.). Like every
// other SDK call, this is fire-and-forget: disabled/unreachable LocalOps
// never affects the caller.
func CheckIn(ctx context.Context, name, state string, message string, details map[string]any) {
	activeTransport().checkIn(ctx, name, checkinRequest{
		State:   state,
		Message: message,
		Details: details,
	})
}
