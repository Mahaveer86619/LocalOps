#!/usr/bin/env bash
# tunnel-watch.sh — continuous SSH tunnel watcher. Run it in a tmux pane
# (matches how this box is normally worked in - see the root README),
# not cron/systemd: it's a foreground loop for the life of the session.
#
# Models itself as ONE LocalOps Task for as long as it runs: reports its
# current connected/not-connected status via normal task updates and
# heartbeats every CHECK_INTERVAL_S, and calls notify.sh to force an
# immediate Slack message only when connectivity actually changes - never
# on every check.
#
# Config (env):
#   MATCH_PATTERN     required — substring unique to the tunnel's ssh
#                     command, e.g. the remote user@host it connects to
#   TASK_SERVER       default "localops"
#   TASK_DESCRIPTION  default "SSH tunnel watcher"
#   CHECK_INTERVAL_S  default 15
#   LOCALOPS_URL      default http://localhost:7717
#
# Usage:
#   MATCH_PATTERN="youruser@remote-host" ./tunnel-watch.sh
set -uo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
source ./task-lib.sh
export NOTIFY_SOURCE="${NOTIFY_SOURCE:-tunnel-watch}"

MATCH_PATTERN="${MATCH_PATTERN:?set MATCH_PATTERN to a substring unique to the tunnel ssh command, e.g. the remote user@host}"
TASK_SERVER="${TASK_SERVER:-localops}"
TASK_DESCRIPTION="${TASK_DESCRIPTION:-SSH tunnel watcher}"
CHECK_INTERVAL_S="${CHECK_INTERVAL_S:-15}"

TASK_ID=$(task_create "$TASK_SERVER" "watch" "$TASK_DESCRIPTION")
echo "[tunnel-watch] tracking as task ${TASK_ID:-<localops disabled or unreachable - continuing anyway>}"

last_status=""

cleanup() {
  task_update "$TASK_ID" "stopped"
  task_stop "$TASK_ID"
  echo "[tunnel-watch] stopped"
  exit 0
}
trap cleanup SIGINT SIGTERM

while true; do
  if pgrep -f "$MATCH_PATTERN" >/dev/null 2>&1; then
    status="connected"
  else
    status="not connected"
  fi

  task_update "$TASK_ID" "$status"
  task_heartbeat "$TASK_ID"

  # Only the baseline (first reading) is silent - a real change after
  # that always forces a Slack message via notify.sh.
  if [[ "$status" != "$last_status" ]]; then
    if [[ -n "$last_status" ]]; then
      if [[ "$status" == "connected" ]]; then
        ./notify.sh "SSH tunnel reconnected" "info"
      else
        ./notify.sh "SSH tunnel disconnected" "warning"
      fi
    fi
    echo "[tunnel-watch] $status"
    last_status="$status"
  fi

  sleep "$CHECK_INTERVAL_S"
done
