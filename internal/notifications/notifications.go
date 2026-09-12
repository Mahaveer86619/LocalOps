// Package notifications implements the generic, undebounced "force
// notify" path: any script or caller that has already decided a message
// is worth sending posts it here, and it goes straight to Slack (via
// internal/notify) with no state machine, no threshold, no transition
// detection - that's the whole point of it existing alongside Watchers.
// A record is kept purely for history/audit (`localops-cli notifications`).
package notifications

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Mahaveer86619/LocalOps/internal/notify"
)

// Level is a free-form severity hint, purely cosmetic (affects the Slack
// message's prefix/emoji) - LocalOps doesn't branch behavior on it.
type Level string

const (
	LevelInfo     Level = "info"
	LevelWarning  Level = "warning"
	LevelCritical Level = "critical"
)

func (l Level) normalized() Level {
	switch l {
	case LevelWarning, LevelCritical:
		return l
	default:
		return LevelInfo
	}
}

// Notification is one recorded force-notify call.
type Notification struct {
	ID        int64     `json:"id"`
	Source    string    `json:"source,omitempty"`
	Level     Level     `json:"level"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

type Store struct {
	db    *sql.DB
	slack *notify.Slack
}

func NewStore(db *sql.DB, slack *notify.Slack) *Store {
	return &Store{db: db, slack: slack}
}

func fmtTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

var levelPrefix = map[Level]string{
	LevelInfo:     ":information_source:",
	LevelWarning:  ":warning:",
	LevelCritical: ":rotating_light:",
}

// Send records the notification and forwards it to Slack immediately -
// no debounce, no transition check. The caller (a bash script, the SDK,
// a curl call) has already decided this is worth sending.
func (s *Store) Send(ctx context.Context, source string, level Level, message string) (Notification, error) {
	level = level.normalized()
	now := time.Now().UTC()

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO notifications (source, level, message, created_at) VALUES (?, ?, ?, ?)`,
		source, level, message, fmtTime(now))
	if err != nil {
		return Notification{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Notification{}, err
	}

	text := fmt.Sprintf("%s %s", levelPrefix[level], message)
	if source != "" {
		text = fmt.Sprintf("%s *%s*: %s", levelPrefix[level], source, message)
	}
	s.slack.Send(ctx, text)

	return Notification{ID: id, Source: source, Level: level, Message: message, CreatedAt: now}, nil
}

// List returns the most recent notifications, newest first.
func (s *Store) List(ctx context.Context, limit int) ([]Notification, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, source, level, message, created_at FROM notifications ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Notification
	for rows.Next() {
		var n Notification
		var createdAt string
		if err := rows.Scan(&n.ID, &n.Source, &n.Level, &n.Message, &createdAt); err != nil {
			return nil, err
		}
		n.CreatedAt = parseTime(createdAt)
		out = append(out, n)
	}
	return out, rows.Err()
}
