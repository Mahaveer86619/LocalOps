// Package scheduler implements the minimal recurring-task mechanism from
// README §17: fixed-interval schedules that create a normal Task when
// due. Deliberately not a cron/workflow engine - one table, one ticker.
package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"time"

	"github.com/Mahaveer86619/LocalOps/internal/tasks"
)

var ErrNotFound = errors.New("scheduler: not found")

// Schedule is a recurring task template.
type Schedule struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	Server      string     `json:"server"`
	Type        string     `json:"type"`
	Description string     `json:"description"`
	IntervalS   int        `json:"interval_s"`
	Enabled     bool       `json:"enabled"`
	LastRunAt   *time.Time `json:"last_run_at,omitempty"`
	NextRunAt   time.Time  `json:"next_run_at"`
	CreatedAt   time.Time  `json:"created_at"`
}

type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

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

const scheduleColumns = `id, name, server, type, description, interval_s, enabled, last_run_at, next_run_at, created_at`

func scan(row interface{ Scan(...any) error }) (Schedule, error) {
	var s Schedule
	var enabled int
	var lastRunAt, nextRunAt, createdAt string
	err := row.Scan(&s.ID, &s.Name, &s.Server, &s.Type, &s.Description, &s.IntervalS, &enabled, &lastRunAt, &nextRunAt, &createdAt)
	if err != nil {
		return Schedule{}, err
	}
	s.Enabled = enabled != 0
	s.LastRunAt = parseTime(lastRunAt)
	s.NextRunAt = *parseTime(nextRunAt)
	s.CreatedAt = *parseTime(createdAt)
	return s, nil
}

// Create registers a new recurring schedule, due to run for the first
// time after intervalS seconds.
func (st *Store) Create(ctx context.Context, name, server, typ, description string, intervalS int) (Schedule, error) {
	now := time.Now().UTC()
	next := now.Add(time.Duration(intervalS) * time.Second)
	_, err := st.db.ExecContext(ctx, `
		INSERT INTO schedules (name, server, type, description, interval_s, enabled, next_run_at, created_at)
		VALUES (?, ?, ?, ?, ?, 1, ?, ?)`,
		name, server, typ, description, intervalS, fmtTime(next), fmtTime(now))
	if err != nil {
		return Schedule{}, err
	}
	return st.Get(ctx, name)
}

func (st *Store) Get(ctx context.Context, name string) (Schedule, error) {
	row := st.db.QueryRowContext(ctx, `SELECT `+scheduleColumns+` FROM schedules WHERE name = ?`, name)
	s, err := scan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Schedule{}, ErrNotFound
	}
	return s, err
}

func (st *Store) List(ctx context.Context) ([]Schedule, error) {
	rows, err := st.db.QueryContext(ctx, `SELECT `+scheduleColumns+` FROM schedules ORDER BY next_run_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Schedule
	for rows.Next() {
		s, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (st *Store) due(ctx context.Context, now time.Time) ([]Schedule, error) {
	rows, err := st.db.QueryContext(ctx, `SELECT `+scheduleColumns+` FROM schedules WHERE enabled = 1 AND next_run_at <= ?`, fmtTime(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Schedule
	for rows.Next() {
		s, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (st *Store) markRun(ctx context.Context, id int64, ranAt, nextRunAt time.Time) error {
	_, err := st.db.ExecContext(ctx, `UPDATE schedules SET last_run_at = ?, next_run_at = ? WHERE id = ?`,
		fmtTime(ranAt), fmtTime(nextRunAt), id)
	return err
}

// Runner ticks the schedule table and creates a Task for each due
// schedule. It does not execute anything itself (README §3: LocalOps
// never runs application business logic) - the created Task is exactly
// the same record a worker would create by calling the SDK directly, so
// whatever process is meant to actually do the work is expected to notice
// it (e.g. by polling GET /tasks) or, more commonly, the schedule exists
// purely to mark "this cron/systemd-timer-driven job should have run" for
// visibility even when the real execution is owned by cron.
type Runner struct {
	Schedules *Store
	Tasks     *tasks.Store
	Interval  time.Duration
}

func NewRunner(schedules *Store, taskStore *tasks.Store, interval time.Duration) *Runner {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &Runner{Schedules: schedules, Tasks: taskStore, Interval: interval}
}

func (r *Runner) Run(ctx context.Context) {
	ticker := time.NewTicker(r.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.tick(ctx)
		}
	}
}

func (r *Runner) tick(ctx context.Context) {
	now := time.Now().UTC()
	due, err := r.Schedules.due(ctx, now)
	if err != nil {
		log.Printf("scheduler: list due schedules: %v", err)
		return
	}
	for _, s := range due {
		if _, err := r.Tasks.Create(ctx, tasks.Task{
			Server:      s.Server,
			Type:        s.Type,
			Description: s.Description,
			Details:     map[string]any{"schedule": s.Name},
		}); err != nil {
			log.Printf("scheduler: create task for schedule %s: %v", s.Name, err)
			continue
		}
		next := now.Add(time.Duration(s.IntervalS) * time.Second)
		if err := r.Schedules.markRun(ctx, s.ID, now, next); err != nil {
			log.Printf("scheduler: mark run for schedule %s: %v", s.Name, err)
		}
	}
}
