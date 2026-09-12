<div align="center">

# LocalOps

**A tiny, self-hosted control plane for the things running on your one
always-on Linux box — the workers, the tunnels, the cron jobs nobody
watches until they've been silently dead for a week.**

[![Go Version](https://img.shields.io/badge/go-1.25%2B-00ADD8?logo=go)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Status](https://img.shields.io/badge/status-active%20development-brightgreen)](#status)

[Why](#why) · [Features](#features) · [Quick start](#quick-start) · [Usage](#usage) · [Configuration](#configuration) · [Architecture](#architecture) · [Contributing](#contributing)

</div>

---

## Why

I run a mini PC that a few things depend on: a long-lived SSH tunnel to a
partner network, a reachability check against a third-party system, a
sync job, a nightly DB dump cron. None of it talked to anything. The
tunnel could die at 2am and the first I'd hear about it is when someone
asks why an integration stopped working three days later.

LocalOps is the small, boring answer to that: one place that knows
**what's currently running, what's currently up or down, and how long
it's been that way** — queryable over SSH via a CLI, or from code via a
tiny Go SDK, with a Slack ping when something that was fine stops being
fine.

It is deliberately not trying to be more than that. No Kubernetes, no
workflow engine, no metrics platform — see the [non-goals](docs/DESIGN.md#25-explicit-non-goals)
in the design doc if you're wondering where the line is.

## Features

- **Tasks** — discrete units of work with a beginning and an end
  (exports, syncs, backfills): progress, status, arbitrary JSON details,
  heartbeats, and a full event history per task.
- **Watchers** — standing conditions with *no* natural end (a tunnel, a
  reachability check): just `ok` / `degraded` / `down`, debounced Slack
  alerts on the transitions that matter, and how long it's been in the
  current state.
- **Control signals** — `cancel` / `pause` / `resume` / `stop`, wired
  straight into Go `context.Context` on the SDK side.
- **A Go SDK that is safe to leave in production code paths.** Disabled
  or unreachable, every call is a no-op — nothing it does can fail your
  job. See [Zero risk to production](#zero-risk-to-production).
- **No SDK required for the simple case.** A watcher can be a ten-line
  bash script doing `curl -X POST .../checkin`.
- **A CLI built for SSH sessions** (and for Claude Code, or whatever
  agent/operator is poking around your box over SSH).
- **SQLite, one binary, no external services.**

## Status

Actively used to track my own box. Tasks, watchers, Slack alerting,
system health, control signals, and the CLI are implemented and in use;
the scheduler is intentionally minimal and there's no dashboard yet (see
[docs/DESIGN.md §26](docs/DESIGN.md#26-development-phases) for the full
phase breakdown and what's still open). Contributions welcome — see
[Contributing](#contributing).

## Quick start

```bash
git clone https://github.com/Mahaveer86619/LocalOps
cd LocalOps
go build ./...
go run ./cmd/localops        # listens on :7717, SQLite at ./localops.db
```

```bash
# in another shell
go run ./cmd/localops-cli status
```

```
SYSTEM
------------------------
CPU     4%
RAM     38%
Disk    61%
Uptime  152189s

RUNNING TASKS
------------------------
(none)

WATCHERS
------------------------
(none)
```

That's the whole install. See [deployments/](deployments/) for running it
persistently (systemd) on a box you SSH into rather than your own
machine.

## Usage

### The Go SDK

```go
import "github.com/Mahaveer86619/LocalOps/client"

func main() {
    client.LoadEnvFile("")                  // loads ./.env if present, no-op otherwise
    client.Init(client.ConfigFromEnv())

    task := client.Start(ctx, client.Task{
        Type:        "sync",
        Description: "v2sync retry run",
        Details:     map[string]any{"sync_stage": "v2"},
    })
    defer task.Complete()

    task.Update(20, "Fetching pending records")
    task.Heartbeat()

    select {
    case <-task.Context().Done():
        return task.Context().Err() // cancelled locally, or remotely via LocalOps
    default:
    }
}
```

Consuming it from another project: this package is a plain subpackage of
this one Go module (no separate `go.mod`), so it costs a `replace` and a
`go get`, and importing only `client` never pulls in the server's own
dependencies (Echo, SQLite, gopsutil) — Go resolves per package, not per
module.

```bash
go mod edit -replace github.com/Mahaveer86619/LocalOps=../LocalOps
go get github.com/Mahaveer86619/LocalOps
```

See [`client/README.md`](client/README.md) for the full SDK reference.

#### Zero risk to production

```go
client.Init(client.Config{Enabled: false})
```

or simply never set `LOCALOPS_URL` — every `Task`/`Handle` method becomes
a no-op that returns immediately: no network calls, no goroutines, no
errors surfaced to your business logic. Flip a project over to LocalOps
by setting `PROFILE=staging` (or `LOCALOPS_ENABLED=true`) plus
`LOCALOPS_URL` in one environment, and leave production untouched.

### No SDK — a watcher from bash

The entire integration surface for a non-Go checker is one `curl` call:

```bash
#!/usr/bin/env bash
if ping -c1 -W2 "$TARGET_HOST" >/dev/null 2>&1; then
  curl -s -X POST localhost:7717/watchers/my-check/checkin -d '{"state":"ok"}'
else
  curl -s -X POST localhost:7717/watchers/my-check/checkin -d '{"state":"down","message":"ping timeout"}'
fi
```

Ready-to-use scripts live in [`scripts/`](scripts/) — meant to run as
long-lived tmux panes, not cron:

- [`tunnel-watch.sh`](scripts/tunnel-watch.sh) / [`ping-watch.sh`](scripts/ping-watch.sh)
  each model themselves as **one Task** for their whole run (status text
  like "connected" / "not connected" via normal task updates +
  heartbeats), and call `notify.sh` to force a Slack message only on a
  real state change — never on every check.
- [`notify.sh`](scripts/notify.sh) forces an immediate, undebounced Slack
  message with any content via `POST /notify` — for when a script has
  already decided something is worth alerting on, on its own terms.
- [`task-wrap.sh`](scripts/task-wrap.sh) wraps an existing binary or cron
  job — unmodified — so it shows up as a tracked Task.

```bash
./notify.sh "something worth knowing about" warning my-script
# or, once the server is up:
localops-cli notify "something worth knowing about" warning my-script
localops-cli notifications   # recent force-notify history
```

### The CLI

```bash
localops-cli status                 # system health + running tasks + watchers, one screen
localops-cli tasks running
localops-cli task <id>
localops-cli task <id> cancel
localops-cli watchers
localops-cli watcher office-tunnel
localops-cli watcher office-tunnel events
```

Set `LOCALOPS_URL` to point it at a non-default server.

## Configuration

### Server (`cmd/localops`)

| Variable | Default | Meaning |
|---|---|---|
| `LOCALOPS_PORT` | `7717` | HTTP listen port |
| `LOCALOPS_DB_PATH` | `localops.db` | SQLite file path |
| `LOCALOPS_SLACK_WEBHOOK_URL` | *(unset)* | Slack incoming webhook; unset disables alerting only, not tracking |
| `LOCALOPS_WATCHER_FAIL_THRESHOLD` | `2` | consecutive `down` check-ins before the first alert (per-watcher override via `fail_threshold`) |
| `LOCALOPS_STUCK_MULTIPLIER` | `3` | × a task's expected heartbeat interval before it's flagged stuck |
| `LOCALOPS_SWEEP_INTERVAL_S` | `30` | how often the stuck-task sweep and scheduler tick |

Loaded from the environment, optionally seeded from a `.env` file first —
see [`configs/localops.example.env`](configs/localops.example.env).

### Client SDK (`client/`)

| Variable | Meaning |
|---|---|
| `PROFILE` | `staging` enables the real client (with `LOCALOPS_URL` set); anything else keeps it a no-op |
| `LOCALOPS_ENABLED` | alternative to `PROFILE=staging`, for apps without a `PROFILE` concept |
| `LOCALOPS_URL` | base URL of the server, e.g. `http://localhost:7717` — required either way |
| `LOCALOPS_SERVER` | default `server` label on tasks/watcher check-ins this process reports |

See [`client/.env.example`](client/.env.example).

## Architecture

```
                    LocalOps
                       |
             +---------+---------+
             |                   |
         Track task          Control task
             |                   |
             +---------+---------+
                       |
                       v
              Your application
                       |
                       v
                Business logic
```

LocalOps tracks and signals; it never executes application logic and
never remediates. If a watcher goes down, LocalOps alerts a human — it
does not restart the tunnel itself. Two small, separate models cover
everything: a **Task** has a beginning and an end (an export, a sync); a
**Watcher** doesn't (a tunnel, a reachability check) — see
[docs/DESIGN.md §3–§4, §13](docs/DESIGN.md#3-core-architectural-principle)
for the full reasoning and the REST API this all sits on top of.

```
cmd/localops/         server binary (REST API + stuck-task sweep + scheduler)
cmd/localops-cli/     operator CLI
internal/             tasks, watchers, notify (Slack), scheduler, health, system, storage
migrations/           embedded SQLite schema
client/               the Go SDK (no separate module, see Usage above)
scripts/              bash-only watcher/task integrations, no SDK required
deployments/          systemd units + install guides
configs/              example server .env
docs/DESIGN.md         the full original design spec (data model, API, rationale)
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) — dev setup, ground rules (the
SDK's no-op contract is non-negotiable), and where the project
deliberately draws the line on scope.

## License

[MIT](LICENSE)
