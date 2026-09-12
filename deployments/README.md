# Deploying LocalOps

These guides assume the typical target: a small, always-on Linux box on
your local network, reached over SSH (directly or via Tailscale/similar),
that already runs a few things worth tracking — a tunnel, a periodic job,
a reachability check. Adjust paths/users below to taste; nothing here is
mini-PC-specific.

- [Local run](#local-run) — fastest way to get LocalOps up and poke at it
  with curl, no systemd required.
- [Persistent install](#persistent-install) — survive reboots, via systemd.
- [Wiring up watchers & tasks](#wiring-up-watchers--tasks) — connect the
  box's existing tunnel/cron jobs/reachability checks.

## Local run

For trying LocalOps out, or iterating on it directly on the box.

```bash
# On the box, or cross-compiled and scp'd over:
GOOS=linux GOARCH=amd64 go build -o localops ./cmd/localops
GOOS=linux GOARCH=amd64 go build -o localops-cli ./cmd/localops-cli

# Run it in the foreground (or inside tmux/screen) - a plain SQLite file
# in the working directory, default port 7717, no config needed:
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

Once you're happy with it, install as a systemd service so it survives
reboots and restarts on crash.

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
if you want Slack alerts (README §13.4) — everything else has a sane
default (see [`configs/localops.example.env`](../configs/localops.example.env)).

## Wiring up watchers & tasks

LocalOps only does anything once something reports to it. Three patterns
cover almost everything (README §13.3, §6):

| Pattern | Use for | How |
|---|---|---|
| **Continuous watcher daemon** | a standing condition you want checked frequently, reported only on change | [`scripts/ping-watch.sh`](../scripts/ping-watch.sh) + [`deployments/ping-watch.service`](ping-watch.service) |
| **Periodic watcher script** | a standing condition checked on a cron/timer cadence | [`scripts/ssh-tunnel-check.sh`](../scripts/ssh-tunnel-check.sh) via cron |
| **Task wrapper** | an existing binary/cron job you don't want to modify, but want tracked as a Task | [`scripts/task-wrap.sh`](../scripts/task-wrap.sh) |

### Example: a reachability check, continuously

```bash
sudo cp scripts/ping-watch.sh /opt/localops/scripts/
sudo cp deployments/ping-watch.service /etc/systemd/system/
# edit the unit's Environment= lines for your TARGET_HOST/WATCHER_NAME
sudo systemctl daemon-reload
sudo systemctl enable --now ping-watch
```

It reports `ok`/`down` to LocalOps **only when the state actually
changes** — see the script header for `DOWN_THRESHOLD`/`UP_THRESHOLD` if
you want more (or less) local debounce before it calls back.

### Example: a persistent tunnel, checked every minute via cron

```cron
* * * * * MATCH_PATTERN="youruser@remote-host" WATCHER_NAME=office-tunnel /opt/localops/scripts/ssh-tunnel-check.sh
```

### Example: tracking an existing cron job as a Task, unmodified

```cron
0 2 * * * /opt/localops/scripts/task-wrap.sh --server myapp --type maintenance \
  --description "Daily DB dump" --watcher db-dump-freshness -- /path/to/your/dump-script.sh
```

Same schedule as before, now visible in `localops-cli tasks` and
alerting through `db-dump-freshness` if it fails.

### Going further: the Go SDK

Once an application can be modified directly, replace its wrapper/cron
integration with real `client.Start/Update/Heartbeat/Complete` calls (see
[`client/README.md`](../client/README.md)) for actual progress reporting
instead of just start/end — see README §6 for the full pattern (including
the `PROFILE=staging` gate that keeps this a zero-risk, opt-in change).
