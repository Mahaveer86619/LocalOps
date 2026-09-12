#!/usr/bin/env bash
# task-wrap.sh — wraps an existing binary/script (that has no LocalOps
# integration at all, and that you don't want to rebuild right now) so it
# still shows up as a tracked Task, using nothing but curl.
#
# Usage:
#   task-wrap.sh --server myapp --type sync --description "v2sync retry run" \
#                 [--notify] \
#                 -- ./bin/check/v2sync_retry_run -recent-only -days=30
#
# Behavior:
#   - POSTs /tasks at start, capturing the task id.
#   - Runs the wrapped command; sends a heartbeat every 30s while it runs.
#   - On exit 0: POSTs /tasks/:id/complete.
#   - On nonzero exit: POSTs /tasks/:id/fail with the exit code and the
#     last output lines, and, if --notify was given, forces an immediate
#     Slack message via /notify (no debounce - a wrapped job failing is
#     usually worth knowing about right away).
#
# Never blocks or fails the wrapped command's own exit code on LocalOps
# being unreachable - curl failures are swallowed, same spirit as the SDK.
set -uo pipefail

LOCALOPS_URL="${LOCALOPS_URL:-http://localhost:7717}"
SERVER=""
TYPE=""
DESCRIPTION=""
NOTIFY_ON_FAIL=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --server) SERVER="$2"; shift 2 ;;
    --type) TYPE="$2"; shift 2 ;;
    --description) DESCRIPTION="$2"; shift 2 ;;
    --notify) NOTIFY_ON_FAIL=1; shift ;;
    --) shift; break ;;
    *) echo "task-wrap: unknown argument $1" >&2; exit 2 ;;
  esac
done

if [[ -z "$SERVER" || -z "$TYPE" || $# -eq 0 ]]; then
  echo "usage: task-wrap.sh --server S --type T [--description D] [--notify] -- <command...>" >&2
  exit 2
fi

extract_id() {
  grep -o '"id"[[:space:]]*:[[:space:]]*"[^"]*"' | head -n1 | sed -E 's/.*:"([^"]*)"/\1/'
}

TASK_JSON=$(curl -s -X POST "$LOCALOPS_URL/tasks" \
  -H 'Content-Type: application/json' \
  -d "{\"server\":\"$SERVER\",\"type\":\"$TYPE\",\"description\":\"$DESCRIPTION\"}") || true
TASK_ID=$(printf '%s' "$TASK_JSON" | extract_id)

heartbeat_loop() {
  [[ -z "$TASK_ID" ]] && return
  while true; do
    sleep 30
    curl -s -X POST "$LOCALOPS_URL/tasks/$TASK_ID/heartbeat" >/dev/null 2>&1 || true
  done
}
heartbeat_loop &
HB_PID=$!

OUT_FILE="$(mktemp)"
"$@" >"$OUT_FILE" 2>&1
EXIT_CODE=$?

kill "$HB_PID" >/dev/null 2>&1 || true

if [[ -n "$TASK_ID" ]]; then
  if [[ $EXIT_CODE -eq 0 ]]; then
    curl -s -X POST "$LOCALOPS_URL/tasks/$TASK_ID/complete" >/dev/null 2>&1 || true
  else
    TAIL=$(tail -n 20 "$OUT_FILE" | sed 's/"/\\"/g' | tr '\n' ' ')
    curl -s -X POST "$LOCALOPS_URL/tasks/$TASK_ID/fail" \
      -H 'Content-Type: application/json' \
      -d "{\"error\":\"exit $EXIT_CODE: $TAIL\"}" >/dev/null 2>&1 || true
    if [[ "$NOTIFY_ON_FAIL" -eq 1 ]]; then
      "$(dirname "${BASH_SOURCE[0]}")/notify.sh" "$DESCRIPTION failed (exit $EXIT_CODE)" "critical" "task-wrap"
    fi
  fi
fi

cat "$OUT_FILE"
rm -f "$OUT_FILE"
exit $EXIT_CODE
