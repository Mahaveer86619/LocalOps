package tasks

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound is returned when a task ID doesn't exist.
var ErrNotFound = errors.New("tasks: not found")

// Store persists tasks and their events in SQLite.
type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("tsk_%x", b)
}

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

// Create inserts a new task in StatusRunning (a task reported to LocalOps
// is, by definition, already underway - README's Start() example goes
// straight to sending progress updates) and records a CREATED + STARTED
// event pair.
func (s *Store) Create(ctx context.Context, t Task) (Task, error) {
	now := time.Now().UTC()
	t.ID = newID()
	t.Status = StatusRunning
	t.CreatedAt = now
	t.StartedAt = &now
	t.UpdatedAt = now
	if t.ExpectedHeartbeatS <= 0 {
		t.ExpectedHeartbeatS = 60
	}

	details, err := marshalDetails(t.Details)
	if err != nil {
		return Task{}, fmt.Errorf("tasks: marshal details: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO tasks (id, server, type, status, progress, description, pid, details, error, control_action, expected_heartbeat_s, created_at, started_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, '', '', ?, ?, ?, ?)`,
		t.ID, t.Server, t.Type, t.Status, t.Progress, t.Description, t.PID, details, t.ExpectedHeartbeatS,
		fmtTime(t.CreatedAt), fmtTime(*t.StartedAt), fmtTime(t.UpdatedAt),
	)
	if err != nil {
		return Task{}, fmt.Errorf("tasks: insert: %w", err)
	}

	if err := insertEvent(ctx, tx, t.ID, "CREATED", ""); err != nil {
		return Task{}, err
	}
	if err := insertEvent(ctx, tx, t.ID, "STARTED", t.Description); err != nil {
		return Task{}, err
	}

	if err := tx.Commit(); err != nil {
		return Task{}, err
	}
	return t, nil
}

func insertEvent(ctx context.Context, tx *sql.Tx, taskID, event, message string) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO task_events (task_id, event, message, timestamp) VALUES (?, ?, ?, ?)`,
		taskID, event, message, fmtTime(time.Now().UTC()))
	if err != nil {
		return fmt.Errorf("tasks: insert event %s: %w", event, err)
	}
	return nil
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

func scanTask(row interface{ Scan(...any) error }) (Task, error) {
	var t Task
	var pid sql.NullInt64
	var details, createdAt, updatedAt string
	var startedAt, completedAt, lastHeartbeatAt sql.NullString
	err := row.Scan(
		&t.ID, &t.Server, &t.Type, &t.Status, &t.Progress, &t.Description, &pid, &details,
		&t.Error, &t.ControlAction, &t.ExpectedHeartbeatS,
		&createdAt, &startedAt, &updatedAt, &completedAt, &lastHeartbeatAt,
	)
	if err != nil {
		return Task{}, err
	}
	if pid.Valid {
		v := int(pid.Int64)
		t.PID = &v
	}
	t.Details = unmarshalDetails(details)
	t.CreatedAt = *parseTime(createdAt)
	t.StartedAt = parseTime(startedAt.String)
	t.UpdatedAt = *parseTime(updatedAt)
	t.CompletedAt = parseTime(completedAt.String)
	t.LastHeartbeatAt = parseTime(lastHeartbeatAt.String)
	return t, nil
}

const taskColumns = `id, server, type, status, progress, description, pid, details, error, control_action, expected_heartbeat_s, created_at, started_at, updated_at, completed_at, last_heartbeat_at`

// Get fetches a single task by ID.
func (s *Store) Get(ctx context.Context, id string) (Task, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM tasks WHERE id = ?`, id)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	if err != nil {
		return Task{}, err
	}
	return t, nil
}

// ListFilter narrows List results; zero-valued fields are ignored.
type ListFilter struct {
	Status Status
	Server string
	Type   string
}

// List returns tasks, most recently created first.
func (s *Store) List(ctx context.Context, f ListFilter) ([]Task, error) {
	query := `SELECT ` + taskColumns + ` FROM tasks WHERE 1=1`
	var args []any
	if f.Status != "" {
		query += ` AND status = ?`
		args = append(args, f.Status)
	}
	if f.Server != "" {
		query += ` AND server = ?`
		args = append(args, f.Server)
	}
	if f.Type != "" {
		query += ` AND type = ?`
		args = append(args, f.Type)
	}
	query += ` ORDER BY created_at DESC`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Update applies a progress/status/description/details change and records
// a PROGRESS event. Empty status/description leave the existing value
// unchanged; nil details leave details unchanged.
func (s *Store) Update(ctx context.Context, id string, progress *int, status *Status, description *string, details map[string]any) (Task, error) {
	current, err := s.Get(ctx, id)
	if err != nil {
		return Task{}, err
	}
	if progress != nil {
		current.Progress = *progress
	}
	if status != nil {
		current.Status = *status
	}
	if description != nil {
		current.Description = *description
	}
	if details != nil {
		current.Details = details
	}
	current.UpdatedAt = time.Now().UTC()

	detailsJSON, err := marshalDetails(current.Details)
	if err != nil {
		return Task{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		UPDATE tasks SET progress = ?, status = ?, description = ?, details = ?, updated_at = ? WHERE id = ?`,
		current.Progress, current.Status, current.Description, detailsJSON, fmtTime(current.UpdatedAt), id)
	if err != nil {
		return Task{}, err
	}
	if err := insertEvent(ctx, tx, id, "PROGRESS", fmt.Sprintf("%d%% %s", current.Progress, current.Description)); err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, err
	}
	return current, nil
}

// Heartbeat records liveness without touching progress/status.
func (s *Store) Heartbeat(ctx context.Context, id string) error {
	now := fmtTime(time.Now().UTC())
	res, err := s.db.ExecContext(ctx, `UPDATE tasks SET last_heartbeat_at = ?, updated_at = ? WHERE id = ?`, now, now, id)
	if err != nil {
		return err
	}
	return checkAffected(res)
}

// Finish transitions a task to a terminal status (completed/failed/
// cancelled/stopped), stamping completed_at and recording the matching
// event.
func (s *Store) Finish(ctx context.Context, id string, status Status, errMsg string) (Task, error) {
	if !status.Terminal() {
		return Task{}, fmt.Errorf("tasks: %s is not a terminal status", status)
	}
	now := time.Now().UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		UPDATE tasks SET status = ?, error = ?, completed_at = ?, updated_at = ?, control_action = '' WHERE id = ?`,
		status, errMsg, fmtTime(now), fmtTime(now), id)
	if err != nil {
		return Task{}, err
	}
	if err := checkAffected(res); err != nil {
		return Task{}, err
	}

	event := map[Status]string{
		StatusCompleted: "COMPLETED",
		StatusFailed:    "FAILED",
		StatusCancelled: "CANCELLED",
		StatusStopped:   "STOPPED",
	}[status]
	if err := insertEvent(ctx, tx, id, event, errMsg); err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, err
	}
	return s.Get(ctx, id)
}

// RequestControl records a control action (README §7) for the integrated
// application to notice and act on. Pause/resume also update status
// directly since, unlike cancel/stop, they're not terminal and nothing
// else will move the status for them.
func (s *Store) RequestControl(ctx context.Context, id string, action ControlAction) (Task, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()

	now := fmtTime(time.Now().UTC())
	switch action {
	case ControlPause:
		_, err = tx.ExecContext(ctx, `UPDATE tasks SET control_action = ?, status = ?, updated_at = ? WHERE id = ?`, action, StatusPaused, now, id)
	case ControlResume:
		_, err = tx.ExecContext(ctx, `UPDATE tasks SET control_action = ?, status = ?, updated_at = ? WHERE id = ?`, action, StatusRunning, now, id)
	default:
		_, err = tx.ExecContext(ctx, `UPDATE tasks SET control_action = ?, updated_at = ? WHERE id = ?`, action, now, id)
	}
	if err != nil {
		return Task{}, err
	}
	if err := insertEvent(ctx, tx, id, "CONTROL_"+string(action), ""); err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, err
	}
	return s.Get(ctx, id)
}

// Events returns a task's lifecycle history, oldest first.
func (s *Store) Events(ctx context.Context, taskID string) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, task_id, event, message, timestamp FROM task_events WHERE task_id = ? ORDER BY id ASC`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Event
	for rows.Next() {
		var e Event
		var ts string
		if err := rows.Scan(&e.ID, &e.TaskID, &e.Event, &e.Message, &ts); err != nil {
			return nil, err
		}
		e.Timestamp = *parseTime(ts)
		out = append(out, e)
	}
	return out, rows.Err()
}

// Running returns every task currently in StatusRunning, used by the
// stuck-task sweep (README §8).
func (s *Store) Running(ctx context.Context) ([]Task, error) {
	return s.List(ctx, ListFilter{Status: StatusRunning})
}

func checkAffected(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
