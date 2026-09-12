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
cmd/localops/         server binary (Echo API + stuck-task sweep + scheduler)
cmd/localops-cli/      operator CLI (talks to the server over plain HTTP)
internal/config/       server-side env config (LOCALOPS_* vars, .env loader)
internal/tasks/        task model + SQLite store (design doc §4, §9)
internal/watchers/     watcher model + store + debounced Slack alerting (design doc §13)
internal/notify/       Slack webhook sender only — no alert-decision logic here
internal/scheduler/    minimal fixed-interval recurring tasks (design doc §17) — not a workflow engine
internal/health/       stuck-task sweep (design doc §8)
internal/system/       CPU/RAM/disk/uptime via gopsutil (design doc §11)
internal/server/       Echo router + handlers implementing design doc §14's REST API
internal/storage/      SQLite connection + migration runner
migrations/            embedded SQL schema (embed.go exposes it to internal/storage)
client/                the Go SDK — a plain package, NOT its own module (see above)
scripts/               bash-only integrations: ping-watch.sh (continuous, change-only
                        callbacks), ssh-tunnel-check.sh (periodic/cron), task-wrap.sh
                        (wraps an unmodified existing binary/cron job so it gets Task
                        tracking via curl, no source changes needed)
deployments/           systemd units + install guides (local run + persistent install)
configs/               example .env for the server
docs/DESIGN.md          the original design spec (source of truth for intent)
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

## Watcher alerting: per-watcher fail_threshold

`internal/watchers/service.go`'s alert rule fires when
`ConsecutiveFails == effectiveThreshold`, where `effectiveThreshold` is
the watcher's own `FailThreshold` if it registered one (nonzero), else
the server-wide `LOCALOPS_WATCHER_FAIL_THRESHOLD` default. This exists
because two very different check-in patterns both need to work:

- **Periodic/cron checkers** (e.g. `ssh-tunnel-check.sh`) call check-in
  on every check, so `ConsecutiveFails` climbs 1-by-1 and the
  server-side default threshold (e.g. 2) genuinely debounces a single
  blip.
- **Continuous daemons** (e.g. `ping-watch.sh`) already debounce
  locally and only ever send *one* `down` check-in per outage episode —
  `ConsecutiveFails` would sit at 1 forever if it relied on the global
  default, so they register with `fail_threshold: 1` (via the checkin
  payload or `POST /watchers`) so their single report still alerts.

Don't remove the per-watcher override without re-solving this — it's not
incidental complexity.

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
against one real box. If a task involves actually deploying/configuring
that box (SSH details, real hostnames, which watcher tracks what),
that's operational context the user provides per-session — don't assume
or invent specifics beyond what's in this conversation or a private,
untracked notes file.

## Testing changes

```
go build ./...
go vet ./...
go run ./cmd/localops        # LOCALOPS_DB_PATH defaults to ./localops.db
```

There's no test suite yet — when adding one, prefer exercising
`internal/*/store.go` against a real `:memory:` SQLite DB (via
`storage.Open(":memory:")`) over mocking `database/sql`.
