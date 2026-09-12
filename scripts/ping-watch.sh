#!/usr/bin/env bash
# ping-watch.sh — continuous reachability watcher. Run it in a tmux pane
# alongside tunnel-watch.sh, not cron/systemd.
#
# Models itself as ONE LocalOps Task for as long as it runs: reports its
# current reachable/unreachable status via normal task updates and
# heartbeats every PING_INTERVAL_S, and calls notify.sh to force an
# immediate Slack message only when reachability actually changes - never
# on every ping. A small local debounce (DOWN_THRESHOLD/UP_THRESHOLD)
# avoids a single dropped ICMP packet reading as a real outage; set
# DOWN_THRESHOLD=1 for an immediate callback on the very first failure.
#
# Config (env):
#   TARGET_HOST       host to ping                                  (default 172.18.36.78)
#   TASK_SERVER       default "localops"
#   TASK_DESCRIPTION  default "Ping watcher: $TARGET_HOST"
#   PING_INTERVAL_S   seconds between pings                          (default 5)
#   DOWN_THRESHOLD    consecutive failed pings before reporting down  (default 2)
#   UP_THRESHOLD      consecutive successful pings before reporting   (default 1)
#                     ok again after a down episode
#   LOCALOPS_URL      default http://localhost:7717
set -uo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
source ./task-lib.sh
export NOTIFY_SOURCE="${NOTIFY_SOURCE:-ping-watch}"

TARGET_HOST="${TARGET_HOST:-172.18.36.78}"
TASK_SERVER="${TASK_SERVER:-localops}"
TASK_DESCRIPTION="${TASK_DESCRIPTION:-Ping watcher: $TARGET_HOST}"
PING_INTERVAL_S="${PING_INTERVAL_S:-5}"
DOWN_THRESHOLD="${DOWN_THRESHOLD:-2}"
UP_THRESHOLD="${UP_THRESHOLD:-1}"

TASK_ID=$(task_create "$TASK_SERVER" "watch" "$TASK_DESCRIPTION")
echo "[ping-watch] tracking $TARGET_HOST as task ${TASK_ID:-<localops disabled or unreachable - continuing anyway>}"

last_status=""      # "" until the baseline is established, then "ok" or "down"
consecutive_ok=0
consecutive_down=0
down_since=""

cleanup() {
  task_update "$TASK_ID" "stopped"
  task_stop "$TASK_ID"
  echo "[ping-watch] stopped"
  exit 0
}
trap cleanup SIGINT SIGTERM

while true; do
  if ping -c1 -W2 "$TARGET_HOST" >/dev/null 2>&1; then
    consecutive_ok=$((consecutive_ok + 1))
    consecutive_down=0

    if [[ "$last_status" != "ok" && "$consecutive_ok" -ge "$UP_THRESHOLD" ]]; then
      msg="reachable again"
      if [[ -n "$down_since" ]]; then
        msg="reachable again after $(( $(date +%s) - down_since ))s down"
      fi
      task_update "$TASK_ID" "ok: $TARGET_HOST reachable"
      if [[ -n "$last_status" ]]; then
        ./notify.sh "$TARGET_HOST $msg" "info"
      fi
      echo "[ping-watch] $msg"
      last_status="ok"
      down_since=""
    fi
  else
    consecutive_down=$((consecutive_down + 1))
    consecutive_ok=0
    [[ -z "$down_since" ]] && down_since=$(date +%s)

    if [[ "$last_status" != "down" && "$consecutive_down" -ge "$DOWN_THRESHOLD" ]]; then
      task_update "$TASK_ID" "down: $TARGET_HOST unreachable"
      if [[ -n "$last_status" ]]; then
        ./notify.sh "$TARGET_HOST unreachable ($consecutive_down consecutive failed pings)" "warning"
      fi
      echo "[ping-watch] down ($consecutive_down consecutive failures)"
      last_status="down"
    fi
  fi

  task_heartbeat "$TASK_ID"
  sleep "$PING_INTERVAL_S"
done
