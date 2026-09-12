# CLAUDE.md

Guidance for Claude Code working in this repository.

## What this is

LocalOps: a self-hosted task-tracking and operations control system for a
small always-on Linux box, normally reached over SSH/Tailscale. The full
design spec — rationale, data model, API design — lives in
[docs/DESIGN.md](docs/DESIGN.md) (section-numbered, referenced elsewhere
as "README §N" even though it's since moved out of the top-level
README). [README.md](README.md) is the OSS-facing overview/install page;
read `docs/DESIGN.md` before making architectural changes — it's the
source of truth, not this file or the top-level README.

Core principle: LocalOps tracks and controls; it never executes business
logic or performs remediation (design doc §3). Keep that boundary when
adding features.

## Module layout

Single Go module, `github.com/Mahaveer86619/LocalOps`, at the repo root.
Deliberately **one** `go.mod`/`go.sum` for the whole repo — no nested
modules — so that a consumer (e.g. some other existing service) can
`replace` this repo at a local path and import just the `client`
subpackage without pulling in the server's dependencies (Echo, SQLite,
gopsutil): Go resolves per-package import graphs, not per-module, so an
unimported sibling package's deps never leak into the consumer's build.

```
cmd/localops/          server binary (Echo API + stuck-task sweep + scheduler)
cmd/localops-cli/      operator CLI (talks to the server over plain HTTP)
internal/config/       server-side env config (LOCALOPS_* vars, .env loader)
internal/tasks/        task model + SQLite store (design doc §4, §9)
internal/watchers/     watcher model + store + debounced Slack alerting (design doc §13)
internal/notify/       Slack webhook sender only — no alert-decision logic here
internal/notifications/ generic, undebounced "force notify" path (POST /notify) -
                        distinct from internal/watchers' debounced alerting; a caller
                        that's already decided a message is worth sending posts it
                        here directly, no state machine involved
internal/scheduler/    minimal fixed-interval recurring tasks (design doc §17) — not a workflow engine
internal/health/       stuck-task sweep (design doc §8)
internal/system/       CPU/RAM/disk/uptime via gopsutil (design doc §11)
internal/server/       Echo router + handlers implementing design doc §14's REST API
internal/storage/      SQLite connection + migration runner
migrations/            embedded SQL schema (embed.go exposes it to internal/storage)
client/                the Go SDK — a plain package, NOT its own module (see above)
scripts/               task-lib.sh (shared helpers: model a script as one long-running
                       Task), tunnel-watch.sh / ping-watch.sh (continuous, tmux-run
                       watchers - normal task updates/heartbeats, plus notify.sh only
                       on a real state change), notify.sh (POST /notify wrapper),
                       task-wrap.sh (wraps an unmodified existing binary/cron job so
                       it gets Task tracking via curl, no source changes needed)
deployments/           systemd unit + install guides (local run, persistent server
                       install, tmux-based watchers)
configs/               example .env for the server
docs/DESIGN.md         the original design spec (source of truth for intent)
```

## Client SDK (`client/`) contract — do not weaken this

Every method on `client.Handle` and the package-level `client.CheckIn`
must be safe to call when LocalOps is disabled or unreachable: no error
returned to the caller, no panic, no blocking beyond the HTTP timeout on
a background goroutine. This is the whole point (design doc §6.1) — a
worker's correctness must never depend on LocalOps being up. When editing
`client/*.go`, preserve the no-op-transport seam (`noopTransport` vs
`httpTransport` behind the `transport` interface in `client/client.go`).

Enablement rule: real client only when (`PROFILE=staging` OR
`LOCALOPS_ENABLED=true`) AND `LOCALOPS_URL` is set. Don't hardcode
`PROFILE` checks elsewhere — `ConfigFromEnv()` in `client/config.go` is
the one place that decides.

**`Task.Status` vs `Task.Description`**: `Status` is the small lifecycle
enum (created/queued/running/paused/completed/failed/cancelled/stopped) —
it only ever changes via `Complete`/`Fail`/a control action, never via a
free-text update. `Description` is where human-readable status *text*
goes (e.g. "Fetching pending records", "connected", "not connected").
`client.Handle.Update(progress, statusText)` writes to `Description`, not
`Status` — this was a real bug once (the SDK sent free text into the
`status` JSON field and the server dropped it straight into the enum
column); `tasks.Status.Valid()` now rejects unknown values server-side as
a second line of defense. Don't reintroduce a path that writes arbitrary
text into `Status`.

## Two alerting paths — don't conflate them

- **`internal/watchers`** (`POST /watchers/:name/checkin`): a stateful
  ok/degraded/down machine with server-side debounce
  (`ConsecutiveFails`/`FailThreshold` in `internal/watchers/service.go`).
  Use this for a checker that reports *every* check and wants LocalOps to
  decide when a run of failures becomes alert-worthy.
- **`internal/notifications`** (`POST /notify`): stateless, undebounced,
  arbitrary message content. Use this when the *caller* has already
  decided a message is worth sending — a continuous script that
  self-debounces before ever reporting a change (see `ping-watch.sh`,
  `tunnel-watch.sh`), or anything that just wants to force a Slack
  message on its own terms (`task-wrap.sh --notify`,
  `localops-cli notify`).

`tunnel-watch.sh`/`ping-watch.sh` deliberately don't use
`internal/watchers` at all: they model themselves as one long-running
Task (`scripts/task-lib.sh`) reporting normal updates/heartbeats, and call
`notify.sh` only on a confirmed state change. The per-watcher
`FailThreshold` override in `internal/watchers` (registering with
`fail_threshold: 1`) was an earlier design for making change-only
reporting work *through* the Watcher debounce path; it's still valid
code (a periodic/cron-style watcher checker can still use
`POST /watchers/.../checkin` the normal way) but the two flagship scripts
now use the simpler Task+notify pattern instead. Don't be surprised the
Watcher fail-threshold plumbing exists without those two scripts using
it — it's for whoever *does* want the stateful checkin model.

## Known gotchas already hit once — don't reintroduce

- **Nullable SQLite TEXT columns**: `started_at`, `completed_at`,
  `last_heartbeat_at` (tasks) and `last_check_at`, `last_ok_at`
  (watchers) can be NULL. Scan them into `sql.NullString`, never a bare
  `string` — a bare string scan panics/errors on NULL. Already fixed once
  in `internal/tasks/store.go` and `internal/watchers/store.go`; if you
  add another nullable timestamp column, follow the same pattern.
- **`go:embed` can't reach outside its own package directory** (no `..`
  in embed patterns). That's why `migrations/embed.go` lives inside
  `migrations/` as its own tiny package, imported by
  `internal/storage/db.go`, rather than `internal/storage` trying to
  embed a file from a sibling directory.
- **SQLite driver is `modernc.org/sqlite`** (pure Go, no cgo) —
  deliberate, so the binary cross-compiles to Linux/amd64 with a plain
  `GOOS=linux GOARCH=amd64 go build`. Don't swap in `mattn/go-sqlite3`
  (cgo) without a reason.

## Real deployment target

This repo's docs are written generically (placeholders like
`youruser@remote-host`) for public consumption, but it's actively used
against one real box, worked in through tmux. If a task involves actually
deploying/configuring that box (SSH details, real hostnames, which
watcher tracks what), that's operational context the user provides
per-session — don't assume or invent specifics beyond what's in this
conversation or a private, untracked notes file.

## Testing changes

```
go build ./...
go vet ./...
gofmt -l .                  # should print nothing
go run ./cmd/localops        # LOCALOPS_DB_PATH defaults to ./localops.db
```

There's no test suite yet — when adding one, prefer exercising
`internal/*/store.go` against a real `:memory:` SQLite DB (via
`storage.Open(":memory:")`) over mocking `database/sql`.
