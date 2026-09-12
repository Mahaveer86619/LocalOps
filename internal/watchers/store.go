package watchers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

var ErrNotFound = errors.New("watchers: not found")

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

func marshalDetails(d map[string]any) (string, error) {
	if d == nil {
		return "{}", nil
	}
	b, err := json.Marshal(d)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func unmarshalDetails(s string) map[string]any {
	if s == "" {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func fmtTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTime(s string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return nil
	}
	return &t
}

const watcherColumns = `id, name, kind, expected_interval_s, fail_threshold, state, last_check_at, last_ok_at, last_message, consecutive_fails, details, created_at, updated_at`

func scanWatcher(row interface{ Scan(...any) error }) (Watcher, error) {
	var w Watcher
	var details, createdAt, updatedAt string
	var lastCheckAt, lastOkAt sql.NullString
	err := row.Scan(&w.ID, &w.Name, &w.Kind, &w.ExpectedIntervalS, &w.FailThreshold, &w.State, &lastCheckAt, &lastOkAt,
		&w.LastMessage, &w.ConsecutiveFails, &details, &createdAt, &updatedAt)
	if err != nil {
		return Watcher{}, err
	}
	w.LastCheckAt = parseTime(lastCheckAt.String)
	w.LastOkAt = parseTime(lastOkAt.String)
	w.Details = unmarshalDetails(details)
	w.CreatedAt = *parseTime(createdAt)
	w.UpdatedAt = *parseTime(updatedAt)
	return w, nil
}

// Register creates a watcher if it doesn't exist yet (name is unique);
// registering an existing watcher just updates its kind/expected
// interval/fail threshold, so re-running a registration call is safe.
// failThreshold of 0 means "use the server-wide default".
func (s *Store) Register(ctx context.Context, name string, kind Kind, expectedIntervalS, failThreshold int, details map[string]any) (Watcher, error) {
	if kind == "" {
		kind = KindCustom
	}
	if expectedIntervalS <= 0 {
		expectedIntervalS = 60
	}
	detailsJSON, err := marshalDetails(details)
	if err != nil {
		return Watcher{}, err
	}
	now := fmtTime(time.Now().UTC())

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO watchers (name, kind, expected_interval_s, fail_threshold, state, details, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET kind = excluded.kind, expected_interval_s = excluded.expected_interval_s, fail_threshold = excluded.fail_threshold, updated_at = excluded.updated_at`,
		name, kind, expectedIntervalS, failThreshold, StateUnknown, detailsJSON, now, now)
	if err != nil {
		return Watcher{}, err
	}
	return s.Get(ctx, name)
}

// Get fetches a watcher by name.
func (s *Store) Get(ctx context.Context, name string) (Watcher, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+watcherColumns+` FROM watchers WHERE name = ?`, name)
	w, err := scanWatcher(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Watcher{}, ErrNotFound
	}
	if err != nil {
		return Watcher{}, err
	}
	return w, nil
}

// List returns all watchers, most recently updated first.
func (s *Store) List(ctx context.Context) ([]Watcher, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+watcherColumns+` FROM watchers ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Watcher
	for rows.Next() {
		w, err := scanWatcher(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// CheckInResult carries enough information for the caller (service layer)
// to decide whether this check-in crosses an alerting threshold, without
// the store needing to know anything about Slack.
type CheckInResult struct {
	Watcher      Watcher
	PreviousState State
}

// CheckIn records one check's outcome (README §13.3), creating the
// watcher on first check-in if it doesn't exist yet (no separate
// registration call required for the common case). Every check-in is
// recorded as a watcher_event, independent of whether the state changed -
// that's the per-check history; alert debouncing is a separate decision
// made by the caller using ConsecutiveFails/PreviousState.
//
// failThreshold, when non-nil, sets/updates the watcher's per-watcher
// alert threshold (see Watcher.FailThreshold) as a side effect of this
// check-in - lets a checker that self-debounces (continuous scripts that
// only call back on state change) declare "alert me on my first down
// report" without a separate registration call. Pass nil to leave it
// unchanged (or 0, the server-wide default, if the watcher is new).
func (s *Store) CheckIn(ctx context.Context, name string, state State, message string, details map[string]any, failThreshold *int) (CheckInResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CheckInResult{}, err
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	nowStr := fmtTime(now)

	var existing Watcher
	row := tx.QueryRowContext(ctx, `SELECT `+watcherColumns+` FROM watchers WHERE name = ?`, name)
	existing, err = scanWatcher(row)
	previousState := StateUnknown
	switch {
	case errors.Is(err, sql.ErrNoRows):
		detailsJSON, mErr := marshalDetails(nil)
		if mErr != nil {
			return CheckInResult{}, mErr
		}
		initialThreshold := 0
		if failThreshold != nil {
			initialThreshold = *failThreshold
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO watchers (name, kind, expected_interval_s, fail_threshold, state, details, created_at, updated_at)
			VALUES (?, 'custom', 60, ?, ?, ?, ?, ?)`,
			name, initialThreshold, StateUnknown, detailsJSON, nowStr, nowStr)
		if err != nil {
			return CheckInResult{}, err
		}
		row = tx.QueryRowContext(ctx, `SELECT `+watcherColumns+` FROM watchers WHERE name = ?`, name)
		existing, err = scanWatcher(row)
		if err != nil {
			return CheckInResult{}, err
		}
	case err != nil:
		return CheckInResult{}, err
	default:
		previousState = existing.State
	}

	consecutiveFails := existing.ConsecutiveFails
	if state == StateOK {
		consecutiveFails = 0
	} else {
		consecutiveFails++
	}

	var detailsJSON string
	if details != nil {
		detailsJSON, err = marshalDetails(details)
	} else {
		detailsJSON, err = marshalDetails(existing.Details)
	}
	if err != nil {
		return CheckInResult{}, err
	}

	lastOkAt := existing.LastOkAt
	if state == StateOK {
		lastOkAt = &now
	}
	var lastOkStr string
	if lastOkAt != nil {
		lastOkStr = fmtTime(*lastOkAt)
	}

	threshold := existing.FailThreshold
	if failThreshold != nil {
		threshold = *failThreshold
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE watchers SET state = ?, last_check_at = ?, last_ok_at = ?, last_message = ?, consecutive_fails = ?, details = ?, fail_threshold = ?, updated_at = ?
		WHERE name = ?`,
		state, nowStr, lastOkStr, message, consecutiveFails, detailsJSON, threshold, nowStr, name)
	if err != nil {
		return CheckInResult{}, err
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO watcher_events (watcher_id, state, message, timestamp) VALUES (?, ?, ?, ?)`,
		existing.ID, state, message, nowStr); err != nil {
		return CheckInResult{}, err
	}

	if err := tx.Commit(); err != nil {
		return CheckInResult{}, err
	}

	updated, err := s.Get(ctx, name)
	if err != nil {
		return CheckInResult{}, err
	}
	return CheckInResult{Watcher: updated, PreviousState: previousState}, nil
}

// Events returns a watcher's state-transition/check-in history, oldest
// first.
func (s *Store) Events(ctx context.Context, name string) ([]Event, error) {
	w, err := s.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, watcher_id, state, message, timestamp FROM watcher_events WHERE watcher_id = ? ORDER BY id ASC`, w.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Event
	for rows.Next() {
		var e Event
		var ts string
		if err := rows.Scan(&e.ID, &e.WatcherID, &e.State, &e.Message, &ts); err != nil {
			return nil, err
		}
		e.Timestamp = *parseTime(ts)
		out = append(out, e)
	}
	return out, rows.Err()
}
