#!/usr/bin/env bash
# ping-watch.sh — continuous watcher daemon for a reachability check
# (README §13). Unlike a cron-driven checker that reports every check,
# this runs forever and calls back to LocalOps *only on state change*:
# once when it goes down, once when it recovers. Meant to run under
# systemd (see deployments/ping-watch.service), not cron.
#
# Config (env):
#   TARGET_HOST      host to ping                              (default 172.18.36.78)
#   WATCHER_NAME     name this reports under                    (default third-party-ping)
#   LOCALOPS_URL     base URL of the server                      (default http://localhost:7717)
#   PING_INTERVAL_S  seconds between pings                       (default 5)
#   DOWN_THRESHOLD   consecutive failed pings before reporting   (default 2)
#                    "down" - set to 1 for an immediate,
#                    no-debounce callback on the very first
#                    failed ping.
#   UP_THRESHOLD     consecutive successful pings before          (default 1)
#                    reporting "ok" again after a down episode.
#
# The watcher registers itself with fail_threshold=1 on its very first
# check-in: this script has already debounced locally by the time it ever
# reports "down", so the server should alert on that first report rather
# than waiting for more (which would never come - this script only calls
# back on change, see internal/watchers/service.go).
set -uo pipefail

TARGET_HOST="${TARGET_HOST:-172.18.36.78}"
WATCHER_NAME="${WATCHER_NAME:-third-party-ping}"
LOCALOPS_URL="${LOCALOPS_URL:-http://localhost:7717}"
PING_INTERVAL_S="${PING_INTERVAL_S:-5}"
DOWN_THRESHOLD="${DOWN_THRESHOLD:-2}"
UP_THRESHOLD="${UP_THRESHOLD:-1}"

log() { echo "[ping-watch:$WATCHER_NAME] $*"; }

checkin() {
  local state="$1" message="${2:-}"
  curl -s -X POST "$LOCALOPS_URL/watchers/$WATCHER_NAME/checkin" \
    -H 'Content-Type: application/json' \
    -d "{\"state\":\"$state\",\"message\":\"$message\",\"details\":{\"target\":\"$TARGET_HOST\"},\"fail_threshold\":1}" \
    >/dev/null 2>&1
}

trap 'log "stopping"; exit 0' SIGINT SIGTERM

reported_state="unknown"   # last state we actually told LocalOps about
consecutive_ok=0
consecutive_down=0
down_since=""

log "watching $TARGET_HOST every ${PING_INTERVAL_S}s (down after $DOWN_THRESHOLD fails, up after $UP_THRESHOLD ok)"

while true; do
  if ping -c1 -W2 "$TARGET_HOST" >/dev/null 2>&1; then
    consecutive_ok=$((consecutive_ok + 1))
    consecutive_down=0

    if [[ "$reported_state" != "ok" && "$consecutive_ok" -ge "$UP_THRESHOLD" ]]; then
      msg="recovered"
      if [[ -n "$down_since" ]]; then
        msg="recovered after $(( $(date +%s) - down_since ))s down"
      fi
      log "$msg"
      checkin "ok" "$msg"
      reported_state="ok"
      down_since=""
    fi
  else
    consecutive_down=$((consecutive_down + 1))
    consecutive_ok=0
    [[ -z "$down_since" ]] && down_since=$(date +%s)

    if [[ "$reported_state" != "down" && "$consecutive_down" -ge "$DOWN_THRESHOLD" ]]; then
      log "ping to $TARGET_HOST failing ($consecutive_down consecutive)"
      checkin "down" "ping timeout ($consecutive_down consecutive failures)"
      reported_state="down"
    fi
  fi

  sleep "$PING_INTERVAL_S"
done
