#!/usr/bin/env bash
# ssh-tunnel-check.sh — watcher checker for a persistent SSH tunnel
# (README §13, "a persistent SSH tunnel to another network"). Same
# no-SDK pattern as the other scripts here: find the process, report
# ok/down. Run every 60s via cron or a systemd timer.
#
# MATCH_PATTERN must be set to whatever uniquely identifies your tunnel's
# ssh invocation, e.g. the remote user@host it connects out to:
#   MATCH_PATTERN="youruser@remote-host" WATCHER_NAME=office-tunnel ./ssh-tunnel-check.sh
set -euo pipefail

MATCH_PATTERN="${MATCH_PATTERN:?set MATCH_PATTERN to a substring unique to your tunnel's ssh command, e.g. the remote user@host}"
WATCHER_NAME="${WATCHER_NAME:-office-tunnel}"
LOCALOPS_URL="${LOCALOPS_URL:-http://localhost:7717}"

if pgrep -f "$MATCH_PATTERN" >/dev/null 2>&1; then
  PID="$(pgrep -f "$MATCH_PATTERN" | head -n1)"
  curl -s -X POST "$LOCALOPS_URL/watchers/$WATCHER_NAME/checkin" \
    -H 'Content-Type: application/json' \
    -d "{\"state\":\"ok\",\"details\":{\"pid\":$PID}}" >/dev/null
else
  curl -s -X POST "$LOCALOPS_URL/watchers/$WATCHER_NAME/checkin" \
    -H 'Content-Type: application/json' \
    -d '{"state":"down","message":"no matching ssh process found"}' >/dev/null
fi
