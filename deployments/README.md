# Deploying LocalOps

These guides assume the typical target: a small, always-on Linux box on
your local network, reached over SSH (directly or via Tailscale/similar),
normally worked in through tmux — LocalOps is meant to coexist with that,
not replace it.

- [Local run](#local-run) — fastest way to get LocalOps up and poke at it
  with curl, no systemd required.
- [Persistent install](#persistent-install) — survive reboots, via systemd.
- [Watchers in tmux](#watchers-in-tmux) — the SSH tunnel and reachability
  checkers, as long-running panes.
- [Tracking an existing cron job](#tracking-an-existing-cron-job) — wrap
  it, unmodified, as a Task.

## Local run

For trying LocalOps out, or iterating on it directly on the box.

```bash
# On the box, or cross-compiled and scp'd over:
GOOS=linux GOARCH=amd64 go build -o localops ./cmd/localops
GOOS=linux GOARCH=amd64 go build -o localops-cli ./cmd/localops-cli

# Run it in the foreground (or inside tmux) - a plain SQLite file in the
# working directory, default port 7717, no config needed:
./localops
```

In another pane:

```bash
./localops-cli health
./localops-cli status
```

Or from anywhere with curl:

```bash
curl -s localhost:7717/health
curl -s -X POST localhost:7717/tasks -H 'Content-Type: application/json' \
  -d '{"server":"demo","type":"test","description":"hello"}'
```

`Ctrl-C` stops it cleanly (graceful shutdown, see `cmd/localops/main.go`).
Nothing here needs root or a system-wide install — this is enough for
local development or a quick trial run on the target box before deciding
to make it persistent.

## Persistent install

Once you're happy with it, install the **server** as a systemd service so
it survives reboots and restarts on crash. (The watchers below are
different — they run in tmux, not systemd; see the next section.)

```bash
sudo mkdir -p /opt/localops
sudo cp localops localops-cli /opt/localops/
cp configs/localops.example.env /opt/localops/.env   # then edit it
sudo cp deployments/localops.service /etc/systemd/system/
sudo useradd --system --home /opt/localops localops || true
sudo chown -R localops:localops /opt/localops
sudo systemctl daemon-reload
sudo systemctl enable --now localops
```

Verify: `curl -s localhost:7717/health`.

At minimum, edit `/opt/localops/.env` to set `LOCALOPS_SLACK_WEBHOOK_URL`
if you want Slack alerts — everything else has a sane default (see
[`configs/localops.example.env`](../configs/localops.example.env)).

## Watchers in tmux

The SSH tunnel and reachability checks are plain bash scripts meant to
run as long-lived tmux panes, one tmux session per box (matching how
you'd normally work on it anyway):

```bash
tmux new -s localops-watch

# pane 1
cd /opt/localops/scripts
MATCH_PATTERN="youruser@remote-host" ./tunnel-watch.sh

# pane 2 (tmux split)
cd /opt/localops/scripts
TARGET_HOST=172.18.36.78 ./ping-watch.sh
```

Detach with `Ctrl-b d` — they keep running. Reattach any time with
`tmux attach -t localops-watch`.

Each script models itself as **one Task** for as long as it runs
(`localops-cli tasks` shows it, description holds its current status text
— e.g. "connected" / "not connected" for the tunnel, "ok: ... reachable"
/ "down: ... unreachable" for the ping check) and reports via normal task
updates + heartbeats. Neither one debounces through LocalOps' Watcher
state machine — they decide locally when something's actually a state
change (see `DOWN_THRESHOLD`/`UP_THRESHOLD` in `ping-watch.sh`) and then
call [`notify.sh`](../scripts/notify.sh) to force an immediate,
undebounced Slack message via `POST /notify`. That endpoint takes any
message content — it's also what you'd reach for from any other script
that wants to alert on its own terms:

```bash
./notify.sh "something worth knowing about" warning my-script
# or: localops-cli notify "something worth knowing about" warning my-script
```

`localops-cli notifications` shows recent force-notify history.

## Tracking an existing cron job

For a job you don't want to modify, wrap it instead of touching its
source — same schedule, now visible in `localops-cli tasks`:

```cron
0 2 * * * /opt/localops/scripts/task-wrap.sh --server myapp --type maintenance \
  --description "Daily DB dump" -- /path/to/your/dump-script.sh
```

See [`scripts/task-wrap.sh`](../scripts/task-wrap.sh) for details
(heartbeats while it runs, completes/fails the task from the wrapped
command's exit code).

### Going further: the Go SDK

Once an application can be modified directly, replace its wrapper/cron
integration with real `client.Start/Update/Heartbeat/Complete` calls (see
[`client/README.md`](../client/README.md)) for actual progress reporting
instead of just start/end — see [docs/DESIGN.md §6](../docs/DESIGN.md#6-go-integration-sdk)
for the full pattern (including the `PROFILE=staging` gate that keeps
this a zero-risk, opt-in change).
