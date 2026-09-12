-- LocalOps initial schema (README §20). Kept intentionally small: two
-- lifecycle-bearing tables (tasks, watchers) each with their own event
-- history, plus schedules. `details` is free-form JSON text everywhere -
-- LocalOps never needs application-specific columns.

CREATE TABLE IF NOT EXISTS tasks (
    id                TEXT PRIMARY KEY,
    server            TEXT NOT NULL,
    type              TEXT NOT NULL,
    status            TEXT NOT NULL DEFAULT 'created',
    progress          INTEGER NOT NULL DEFAULT 0,
    description       TEXT NOT NULL DEFAULT '',
    pid               INTEGER,
    details           TEXT NOT NULL DEFAULT '{}',
    error             TEXT NOT NULL DEFAULT '',
    control_action    TEXT NOT NULL DEFAULT '', -- '', 'cancel', 'pause', 'resume', 'stop'
    expected_heartbeat_s INTEGER NOT NULL DEFAULT 60,
    created_at        TEXT NOT NULL,
    started_at        TEXT,
    updated_at        TEXT NOT NULL,
    completed_at      TEXT,
    last_heartbeat_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
CREATE INDEX IF NOT EXISTS idx_tasks_server_type ON tasks(server, type);

CREATE TABLE IF NOT EXISTS task_events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id    TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    event      TEXT NOT NULL, -- e.g. CREATED, STARTED, PROGRESS, COMPLETED, FAILED, CANCELLED
    message    TEXT NOT NULL DEFAULT '',
    timestamp  TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_task_events_task_id ON task_events(task_id, id);

CREATE TABLE IF NOT EXISTS watchers (
    id                    INTEGER PRIMARY KEY AUTOINCREMENT,
    name                  TEXT NOT NULL UNIQUE,
    kind                  TEXT NOT NULL DEFAULT 'custom', -- ssh_tunnel | ping | custom
    expected_interval_s   INTEGER NOT NULL DEFAULT 60,
    fail_threshold        INTEGER NOT NULL DEFAULT 0, -- 0 = use the server-wide default (see internal/watchers/service.go)
    state                 TEXT NOT NULL DEFAULT 'unknown', -- ok | degraded | down | unknown
    last_check_at         TEXT,
    last_ok_at            TEXT,
    last_message          TEXT NOT NULL DEFAULT '',
    consecutive_fails     INTEGER NOT NULL DEFAULT 0,
    details               TEXT NOT NULL DEFAULT '{}',
    created_at            TEXT NOT NULL,
    updated_at            TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS watcher_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    watcher_id  INTEGER NOT NULL REFERENCES watchers(id) ON DELETE CASCADE,
    state       TEXT NOT NULL,
    message     TEXT NOT NULL DEFAULT '',
    timestamp   TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_watcher_events_watcher_id ON watcher_events(watcher_id, id);

CREATE TABLE IF NOT EXISTS schedules (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    name          TEXT NOT NULL UNIQUE,
    server        TEXT NOT NULL,
    type          TEXT NOT NULL,
    description   TEXT NOT NULL DEFAULT '',
    interval_s    INTEGER NOT NULL, -- simple fixed-interval scheduling (README §17 explicitly rules out a full workflow engine)
    enabled       INTEGER NOT NULL DEFAULT 1,
    last_run_at   TEXT,
    next_run_at   TEXT NOT NULL,
    created_at    TEXT NOT NULL
);
