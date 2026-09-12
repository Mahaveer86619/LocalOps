# LocalOps — Design

> This is the original design spec: the full rationale, data model, and
> API design behind LocalOps, section-numbered and referenced elsewhere
> in the repo (code comments, [CLAUDE.md](../CLAUDE.md)) as "README §N".
> For an overview, install steps, and usage examples, see the
> [top-level README](../README.md) instead — this document is the "why",
> that one is the "how".
>
> Implementation has since diverged from this spec in a few small,
> deliberate ways (noted where relevant): `client/` is a plain package in
> the same Go module rather than its own nested module (§6, §22), it
> lives at the repo root rather than under `pkg/` (§22), and the ping
> watcher is a continuous daemon rather than a cron-driven script (§13.3).

## 1. Overview

A lightweight, self-hosted **local task tracking and operations control system**, written in Go.

The primary target environment is an **Ubuntu mini PC running inside an office network**. The machine is accessed remotely through **Tailscale + SSH**, with development and debugging commonly performed through **Neovim, tmux, and Claude Code**.

The system is a reusable control plane for locally running Go services and workers, and for the ambient "is everything actually okay" checks that don't belong to any one service (a tunnel, a ping check, a cron job).

The first and most important integration target is the existing **FK IMS WVS (`fk-ims-wvs-server`) project**, which already contains a Background Tasks API that tracks long-running goroutines/workers, task IDs, process IDs, descriptions, progress, status, task types, and subtypes.

LocalOps generalizes and expands that existing concept into a standalone service that other local applications, and non-application things (tunnels, network checks), can integrate with through a very small Go API layer — or, for non-Go things, a couple of curl calls.

The project must remain **simple, lightweight, and easy to integrate**. Do not turn it into Kubernetes, Airflow, a generic process manager, or a full monitoring platform.

---

## 2. Core Problem

Long-running local workers, and long-standing local *conditions*, need a way to report their state externally, in one place, queryable over SSH.

Two related but distinct problems:

**A. Discrete tasks with a lifecycle** — WVS may run:

* exports
* billing record processing
* shipment processing
* sync operations (e.g. v2sync, IMS sync)
* backfills
* reconciliation jobs
* other long-running workers

These start, make progress, and end (complete/fail/cancel).

**B. Standing conditions with no natural end** — the mini PC also has things that are either "up" or "down" for long stretches, and only become interesting when they flip:

* a persistent SSH tunnel to another network
* periodic reachability/ping checks against a third-party system
* a systemd service that should just always be running

LocalOps owns the tracking and control plane for both shapes, distinguishing them rather than forcing condition B into a fake 0–100% progress bar (see [§13](#13-connectivity-watchers--alerting)).

A worker should be able to:

1. Start a task.
2. Receive a task ID.
3. Periodically update progress.
4. Update human-readable status/context.
5. Attach arbitrary task-specific JSON details.
6. Report completion.
7. Report failure.
8. Report cancellation.
9. Send heartbeats/progress updates.
10. React to cancellation/pause/stop signals initiated externally.

A watcher (condition B) should be able to:

1. Register itself once (name, kind, check interval, expected behavior).
2. Report each check's outcome (ok / degraded / down) with an optional message.
3. Trigger an alert (Slack) when it flips from ok to down, and when it recovers.
4. Be queried the same way a task is — "what's the state of the office tunnel right now?"

The application that actually performs the work (or the check) remains responsible for its own business/check logic. LocalOps never runs the check itself, and never re-establishes a tunnel or fixes a network — it reports and, on failure, tells a human.

---

## 3. Core Architectural Principle

Separate **task tracking/control** from **task execution**.

LocalOps does NOT execute application business logic, and does NOT perform remediation.

```text
                    LocalOps
                       │
             ┌─────────┴─────────┐
             │                   │
         Track task          Control task
             │                   │
             └─────────┬─────────┘
                       │
                       ▼
                      WVS
                       │
                       ▼
                    Worker
                       │
                       ▼
                Business logic
```

If a user requests cancellation:

```text
User
  ↓
LocalOps
  ↓
cancel signal
  ↓
WVS
  ↓
context.Cancel()
  ↓
worker exits cleanly
```

If a watcher goes down:

```text
ping-check script
  ↓
reports "down" to LocalOps
  ↓
LocalOps flips watcher state, fires Slack alert
  ↓
human fixes the network manually
  ↓
ping-check script reports "ok" again
  ↓
LocalOps flips state back, fires recovery alert
```

LocalOps should not normally directly kill WVS processes or goroutines, restart tunnels, or retry connections itself. The integrated application/script owns execution and determines how cancellation, pausing, cleanup, remediation, and shutdown are handled. LocalOps' job stops at "observe, record, signal, alert."

---

## 4. Generic Task Model

The task model holds a small set of generic fields.

```text
Task
├── ID
├── Server
├── Type
├── Status
├── Progress
├── Description
├── PID
├── Details
├── CreatedAt
├── StartedAt
├── UpdatedAt
├── CompletedAt
└── Error
```

Do not create application-specific database columns for every possible task property.

The most important extensibility mechanism is:

```text
details: JSON
```

`details` can contain arbitrary structured information belonging to the application/task.

```json
{
  "server": "wvs-server",
  "type": "sync",
  "status": "running",
  "progress": 42,
  "description": "v2sync retry run",
  "details": {
    "sync_stage": "v2",
    "batch": 17,
    "awb_count": 8200
  }
}
```

```json
{
  "server": "wvs-server",
  "type": "export",
  "status": "running",
  "progress": 65,
  "description": "Billing record export",
  "details": {
    "export_type": "billing_record",
    "vendor_id": 42,
    "record_count": 125000,
    "file": "billing_2026_09.csv"
  }
}
```

LocalOps should not need to understand these application-specific fields.

---

## 5. Task Types

Tasks support generic:

```text
server
type
```

The application can use whatever type hierarchy makes sense. Avoid hardcoding WVS-specific task types into LocalOps — it just stores and exposes the values.

For WVS, examples might be:

```text
server: wvs-server

type:
    sync
        v1sync
        v2sync
        ims_sync
    export
        billing_record
        shipment
    maintenance
        cleanup
        reconciliation
```

If the application needs richer categorization, it can put additional information inside `details`.

---

## 6. Go Integration SDK

Integration should be extremely easy, and — critically — **optional and silent when not enabled**.

```go
task := tracker.Start(ctx, tracker.Task{
    Server:      "wvs-server",
    Type:        "sync",
    Description: "v2sync retry run",
    Details: map[string]any{
        "sync_stage": "v2",
    },
})

task.Update(20, "Fetching pending records")
task.Update(50, "Submitting to v2 endpoint")
task.Update(80, "Verifying latch state")
task.Complete()
```

Failure:

```go
task.Fail(err)
```

Cancellation integrates naturally with Go contexts:

```go
ctx, cancel := context.WithCancel(parent)
defer cancel()

task := tracker.Start(ctx, ...)
```

### 6.1 Mode switching (`PROFILE` / `LOCALOPS_ENABLED`)

The SDK must work identically whether or not LocalOps is actually running, and must never be the reason a production job fails.

* The client reads the host application's existing config (in WVS's case, `PROFILE`, alongside `LOCALOPS_URL`).
* Default (`PROFILE=production`, or `LOCALOPS_URL` unset): the SDK constructs a **no-op client**. `Start/Update/Heartbeat/Complete/Fail` are cheap no-ops that return immediately — zero network calls, zero risk to prod.
* When `PROFILE=staging` (or `LOCALOPS_ENABLED=true` explicitly, so it isn't hardwired to one env name forever) **and** `LOCALOPS_URL` is set: the SDK constructs a **real client** that talks to the local LocalOps instance over HTTP (default `http://localhost:<port>`, since it's mini-PC-local — Tailscale makes it reachable from elsewhere if ever needed).
* All real-client calls are fire-and-forget from the caller's point of view: non-2xx responses and network errors are logged (via the host app's existing logger) and swallowed, never returned to business logic and never panic. A worker's correctness must never depend on LocalOps being up.
* One process-wide client is constructed at startup from config; `tracker.Start(...)` uses it implicitly. No per-call plumbing of URLs/tokens through the app.

```go
// internal/config or main.go, once at startup
tracker.Init(tracker.Config{
    Enabled: cfg.Profile == "staging", // or cfg.LocalOpsEnabled
    BaseURL: cfg.LocalOpsURL,          // e.g. http://localhost:7717
    Server:  "wvs-server",
})
```

This is the actual mechanism that lets WVS adopt LocalOps incrementally: flip `PROFILE=staging` on the mini PC only, leave prod untouched, and the exact same call sites (`task.Update(...)`) become real tracked tasks on staging while staying inert in prod.

The SDK should make it easy for workers to start / update / heartbeat / complete / fail / cancel / update details without requiring the application developer to manually construct HTTP requests, and it should hide the REST API implementation entirely.

---

## 7. Context Cancellation and Control

Task control is a core feature. LocalOps supports signals:

```text
cancel
pause
resume
stop
```

LocalOps only communicates the requested state/control action — the integrated application is responsible for the actual behavior.

```text
LocalOps
   │
   │ CANCEL task 123
   ▼
WVS task manager
   │
   ▼
context.CancelFunc
   │
   ▼
worker detects ctx.Done()
   │
   ▼
cleanup
   │
   ▼
task → cancelled
```

Do not implement arbitrary remote shell execution as part of the task-control mechanism.

---

## 8. Heartbeats

Long-running tasks send heartbeat/progress updates, so LocalOps can distinguish "process exists" from "task is actually making progress":

```text
Task:                 billing export
Progress:             65%
Last heartbeat:       27 minutes ago
Expected heartbeat:   every 60 seconds
State:                possibly stuck
```

LocalOps exposes a `stuck`/`unresponsive` indication (no heartbeat within N× the expected interval) without automatically killing the task. A stuck task is a good candidate to also raise through the same alerting path as watcher failures (see [§13](#13-connectivity-watchers--alerting)) rather than inventing a second notification mechanism.

---

## 9. Task History

LocalOps persists task lifecycle information. A task moves through states such as:

```text
created → queued → running → paused → completed
                            → failed
                            → cancelled / stopped
```

Keep the state machine simple and explicit. Task history allows investigation of previous runs:

```text
Task #9281
10:32:01 CREATED
10:32:02 STARTED
10:32:15 PROGRESS 10%
10:33:04 PROGRESS 25%
10:34:17 PROGRESS 50%
10:35:42 PROGRESS 65%
10:37:10 FAILED
```

Store structured task events/history separately from normal application logs. Do not attempt to replace the existing logging system.

---

## 10. Existing Logs

The existing application's log system remains the source of detailed logs. LocalOps provides enough information to correlate:

```text
Task → PID / worker → structured task events → application logs
```

LocalOps should not duplicate the application's entire logging infrastructure.

---

## 11. System Monitoring

Expose basic system health on the mini PC:

```text
CPU
RAM
Disk
Uptime
Load
Process information
```

```text
SYSTEM
────────────────────
CPU       23%
RAM       41%
Disk      67%
Uptime    18 days

SERVICES
────────────────────
wvs-server       healthy
billing-worker   healthy
sync-worker      running
```

Modular, so more metrics can be bolted on later. Not a Prometheus/Grafana replacement.

---

## 12. Process Awareness

Where possible, a task exposes:

```text
PID
server
worker
task ID
```

So an operator can correlate `Task #123 → WVS worker → PID 18231 → CPU/RAM/process info`.

LocalOps is not intended to replace systemd, tmux, or Linux process management — use the OS's existing mechanisms wherever possible.

---

## 13. Connectivity Watchers & Alerting

This is the piece that doesn't fit the Task model above, and needs its own light concept: **Watchers**.

### 13.1 Why not just a Task

A Task has a beginning and an end (created → … → completed/failed). An SSH tunnel or a ping check has neither — it's either currently fine or currently not, indefinitely, and what matters is *state transitions* and *how long it's been in each state*, not a progress percentage. Forcing it into the Task table would mean either one Task that "runs" forever (breaks history/reporting, never completes) or a new Task spawned per check (spams the task list with thousands of 2-second-long "tasks" for a check that runs every minute). Neither is right, so it's a parallel, equally small model.

### 13.2 Watcher model

```text
Watcher
├── ID
├── Name              e.g. "office-tunnel", "flipkart-vpn-ping"
├── Kind               ssh_tunnel | ping | custom
├── ExpectedIntervalS   how often a check-in is expected
├── State              ok | degraded | down | unknown
├── LastCheckAt
├── LastOkAt
├── LastMessage        free text from the checker
├── ConsecutiveFails
└── Details            JSON, arbitrary (target host, latency, etc.)

WatcherEvent
├── ID
├── WatcherID
├── State              the state it transitioned to
├── Message
└── Timestamp
```

Same spirit as tasks: a tiny generic model, arbitrary `details` JSON, event history kept separately.

### 13.3 How a check reports in

The checker can be anything — a Go binary using the same SDK, or (very deliberately, per the original ask) a **plain bash script** run from cron/systemd-timer that does a ping and a curl:

```bash
#!/usr/bin/env bash
# ping-check.sh — runs every 60s via systemd timer or cron
if ping -c1 -W2 "$TARGET_HOST" >/dev/null 2>&1; then
  curl -s -X POST localhost:7717/watchers/flipkart-vpn-ping/checkin \
    -d '{"state":"ok"}'
else
  curl -s -X POST localhost:7717/watchers/flipkart-vpn-ping/checkin \
    -d '{"state":"down","message":"ping timeout"}'
fi
```

No SDK required for this path — a shell script and two curl calls is the whole integration surface, matching the "simple bash script pinging on an interval" from the original ask.

### 13.4 Alerting (Slack)

LocalOps owns exactly one piece of outbound notification: a Slack webhook/bot integration, used for:

* a watcher transitioning `ok → down` (fire once, not on every failed check — see debounce below)
* a watcher recovering `down → ok`
* a task detected as stuck (no heartbeat within N× expected interval)
* (optionally, later) a task reporting `failed`

Rules, kept deliberately simple:

* **Debounce, don't spam.** Alert on the *transition*, not on every check. `ConsecutiveFails` crossing a small threshold (e.g. 2–3) before alerting avoids paging for one blip.
* **One config, one webhook.** A single Slack incoming-webhook URL (or bot token + channel) in LocalOps config. No per-watcher routing in v1.
* **No auto-remediation.** The alert is the entire response — "ping check failed, go look at the network" — exactly as in the original ask ("I would need to manually fix the network issue"). LocalOps never retries the tunnel, restarts a service, or pages anything beyond Slack.
* Recovery notifications close the loop so a fixed issue doesn't sit "alerted" forever in someone's head.

### 13.5 CLI/API surface

Watchers reuse the same shape as tasks wherever sensible:

```http
POST  /watchers                  register a watcher
GET   /watchers
GET   /watchers/:name
POST  /watchers/:name/checkin    { state, message?, details? }
GET   /watchers/:name/events
```

```bash
localops watchers
localops watcher office-tunnel
localops watcher flipkart-vpn-ping events
```

---

## 14. REST API

```http
POST   /tasks
GET    /tasks
GET    /tasks/:id

POST   /tasks/:id/update
POST   /tasks/:id/heartbeat
POST   /tasks/:id/complete
POST   /tasks/:id/fail

POST   /tasks/:id/cancel
POST   /tasks/:id/pause
POST   /tasks/:id/resume
POST   /tasks/:id/stop

GET    /tasks/:id/events

POST   /watchers
GET    /watchers
GET    /watchers/:name
POST   /watchers/:name/checkin
GET    /watchers/:name/events

GET    /health
GET    /system
```

Exact API design can be refined during implementation. The Go SDK consumes this API.

---

## 15. CLI

A lightweight CLI for local/SSH/Claude usage — particularly important since the mini PC is normally accessed over SSH.

```bash
localops status
localops tasks
localops tasks running
localops tasks failed
localops task 9281
localops task 9281 cancel

localops watchers
localops watcher office-tunnel

localops system
localops health
```

Claude Code should be able to use the CLI naturally.

---

## 16. Claude Code Integration

Claude is an important consumer, but should not be tightly coupled into the core — it inspects the system through the CLI/API like any other client.

```text
Developer → SSH → Ubuntu mini PC → Claude Code
    localops status
    localops tasks
    localops task <id>
    localops watchers
    → inspect logs / code → debug
```

Claude should be able to understand: currently running tasks, failed tasks, stuck tasks, task progress/details, watcher state (is the tunnel/ping check currently down), system health, and recent history.

Do not give LocalOps unrestricted AI control over the machine. Keep privileged operations explicit and controlled — Claude reads state and reasons about it; it doesn't get a shortcut around the same control endpoints a human would use.

---

## 17. Scheduling / Recurring Operations

The mini PC is also used for recurring operations — periodic synchronization, and periodic watcher checks (§13).

```text
billing-sync   every 14 days
vendor-sync    every 14 days
```

Each scheduled execution creates a normal tracked task:

```text
Schedule → creates Task → worker executes → Task tracked normally
```

The scheduler itself should not have a completely separate task model. Do not build a full workflow engine.

---

## 18. Tailscale / SSH / tmux

Normal access pattern:

```text
Windows PC
    │ Tailscale
    ▼
Ubuntu mini PC
    │
    ├── SSH
    ├── tmux
    ├── Neovim
    ├── Claude Code
    └── LocalOps
```

LocalOps coexists with tmux rather than replacing it:

```text
Terminal 1 → Neovim
Terminal 2 → tmux / application
Terminal 3 → LocalOps CLI
Browser    → LocalOps dashboard/API
Claude     → CLI + codebase + logs
```

---

## 19. Dashboard

A lightweight web interface around the REST API, focused on operational visibility:

```text
SYSTEM HEALTH            RUNNING TASKS                RECENT
──────────────           ──────────────               ──────────────
CPU    23%                ● Billing Export   65%       ✓ Vendor Sync
RAM    41%                ● v2sync retry     32%       ✓ Billing Export
DISK   67%                ● Image Processing 81%       ✗ Reconciliation

WATCHERS
──────────────
● office-tunnel        ok        (checked 12s ago)
● flipkart-vpn-ping    DOWN      (down 4m, alerted)
```

Task/watcher details display arbitrary JSON in a readable way. The dashboard is just another API client, not the primary architecture.

---

## 20. Storage

SQLite. Minimal entities:

```text
tasks
task_events
watchers
watcher_events
schedules
```

JSON `details` stored as TEXT/JSON depending on the chosen SQLite driver. Avoid unnecessary database complexity.

---

## 21. Technology

```text
Go
Echo
SQLite
Tailscale
SSH
tmux
systemd
Slack webhook/bot (outbound alerts only)
```

Use standard Go libraries wherever practical. Do not introduce Kubernetes, Redis, Kafka, RabbitMQ, PostgreSQL, Prometheus, or Grafana unless a concrete requirement appears later.

---

## 22. Project Structure

```text
localops/
├── cmd/
│   ├── localops/
│   │   └── main.go
│   └── localops-cli/
│       └── main.go
│
├── internal/
│   ├── config/
│   ├── server/
│   ├── tasks/
│   ├── events/
│   ├── watchers/        # ping/tunnel/custom checks + state machine
│   ├── notify/           # Slack webhook/bot alerting
│   ├── scheduler/
│   ├── health/
│   ├── system/
│   └── storage/
│
├── pkg/
│   └── client/           # Go SDK (tracker.Start/Update/Complete/... + no-op mode)
│
├── migrations/
├── configs/
├── deployments/
├── scripts/
│   └── ping-check.sh     # example bash-only watcher checker
├── Makefile
├── Dockerfile
└── README.md
```

Adjust if implementation reveals a simpler approach.

---

## 23. Most Important Design Constraint

The system must remain **easy to integrate**. Adding LocalOps to an existing Go service should require:

```text
1. Add Go client dependency
2. Configure LocalOps endpoint (only active when PROFILE=staging / LOCALOPS_ENABLED=true)
3. Start a task
4. Update task from worker
```

It should not require restructuring the application's architecture, changing worker implementations significantly, adopting a message broker, adding a database to the client application, or understanding LocalOps internals. For a non-Go checker, integration is a couple of `curl` calls (§13.3) — even less.

---

## 24. WVS Integration

WVS is the first real-world integration. Its existing Background Tasks API already demonstrates the required concepts: task ID, process ID, description, progress, status, task types, subtypes, system metrics, worker callbacks, goroutine-based workers.

```text
Existing WVS BG Tasks → identify reusable concepts → LocalOps generic model → Go SDK → WVS becomes first LocalOps client
```

Use it as the reference when designing the generalized API — don't blindly copy WVS-specific assumptions. Migration is incremental, gated entirely by the `PROFILE=staging` switch (§6.1): staging opts in per-deploy with zero risk to prod, since the no-op client is what prod always uses until this is proven out.

Alongside WVS, the mini PC's own always-on conditions become the first Watchers: the SSH tunnel to the office network, and a ping check against whatever third-party system this was originally built to babysit.

---

## 25. Explicit Non-Goals

Do NOT turn this project into:

* Kubernetes
* Docker orchestration
* Airflow
* a generic process manager
* a cloud monitoring platform
* a log aggregation platform
* a distributed tracing platform
* a CI/CD system
* a full remote desktop
* a browser terminal
* a file synchronization platform
* a replacement for SSH
* a replacement for tmux
* a replacement for systemd
* a general-purpose alerting/paging platform (Slack, and only Slack, and only for the cases in §13.4)
* a remediation/auto-healing engine (it alerts a human; it never fixes the network itself)

The system is a **local task lifecycle, watcher, observability, and control plane** — nothing more.

---

## 26. Development Phases

### Phase 1 — Core Task API
Task creation, state, progress, description, type, server, JSON details, timestamps, completion/failure, SQLite persistence.

### Phase 2 — Go SDK
`Start / Update / Heartbeat / Complete / Fail / Cancel`, plus the `PROFILE`-driven no-op-vs-real client switch (§6.1) from day one — this is what makes Phase 9 safe.

### Phase 3 — Task Events
Persistent lifecycle/event history.

### Phase 4 — Control
`cancel / pause / resume / stop`, application-controlled execution, Go context integration.

### Phase 5 — System Health
CPU, RAM, disk, uptime, process information.

### Phase 6 — Watchers & Alerting
Watcher model + check-in endpoint, state machine (ok/degraded/down) with debounce, Slack webhook integration, the bash `ping-check.sh` example, and an SSH-tunnel watcher for the office tunnel.

### Phase 7 — CLI
Operational commands for SSH and Claude, covering both tasks and watchers.

### Phase 8 — Scheduler
Recurring tasks, including the two-week sync jobs.

### Phase 9 — Dashboard
Lightweight UI over the API (tasks + watchers + system health).

### Phase 10 — WVS Migration
Integrate WVS incrementally on staging (`PROFILE=staging`) and validate that the existing background-task functionality is represented cleanly by LocalOps before ever considering it for prod.

---

## 27. Final Goal

The finished system should make the Ubuntu mini PC feel like a small, self-hosted development/staging operations node:

```text
                         Windows
                            │
                         Tailscale
                            │
                            ▼
                    Ubuntu Mini PC
                            │
                  ┌─────────┴─────────┐
                  │     LocalOps      │
                  │                   │
                  │ Tasks             │
                  │ Events            │
                  │ Watchers          │
                  │ Health            │
                  │ Control           │
                  │ Scheduler         │
                  │ Slack alerts      │
                  └─────────┬─────────┘
                            │
             ┌──────────────┼──────────────┬─────────────────┐
             ▼              ▼              ▼                 ▼
            WVS          Sync Jobs      SSH tunnel      Ping check
             │                          (watcher)        (watcher,
             ▼                                            bash script)
          Workers
             │
             ├── progress
             ├── heartbeat
             ├── details
             └── context cancellation

Developer access: SSH + tmux + Neovim + Claude Code
```

The most important characteristic: **LocalOps stays generic while the application (or checker script) retains ownership of its actual work and its own remediation.**

WVS should be able to say:

> "I have a v2sync retry running at 42%, here is its JSON context, and I'm ready to receive a cancellation signal."

A ping-check script should be able to say:

> "The Flipkart VPN ping just failed for the third time in a row."

LocalOps should be able to answer:

> "Here are all running tasks and watchers, their health, history, progress, context, and available control actions — and I already told you on Slack about the ping failure."

That is the core product.
