#!/usr/bin/env bash
# task-lib.sh — shared helpers for a script that models itself as ONE
# long-running LocalOps Task for its whole lifetime (create once on
# startup, report status via normal task updates/heartbeats in a loop,
# stop the task on exit). Source this, don't execute it directly:
#
#   source "$(dirname "${BASH_SOURCE[0]}")/task-lib.sh"
#   TASK_ID=$(task_create "myapp" "watch" "SSH tunnel watcher")
#   task_update "$TASK_ID" "connected"
#   task_heartbeat "$TASK_ID"
#   task_stop "$TASK_ID"
#
# Every function is a best-effort curl call: LocalOps being down or
# unreachable never breaks the calling script (same no-op-safe spirit as
# the Go SDK).
LOCALOPS_URL="${LOCALOPS_URL:-http://localhost:7717}"

_task_extract_id() {
  grep -o '"id"[[:space:]]*:[[:space:]]*"[^"]*"' | head -n1 | sed -E 's/.*:"([^"]*)"/\1/'
}

# task_create SERVER TYPE DESCRIPTION — creates the task and prints its
# id (empty if LocalOps is disabled/unreachable - callers should treat an
# empty id as "fine, just don't bother calling update/heartbeat/stop", or
# simply keep calling them: they're safe no-ops on an empty id).
task_create() {
  local server="$1" type="$2" description="$3"
  curl -s -X POST "$LOCALOPS_URL/tasks" \
    -H 'Content-Type: application/json' \
    -d "{\"server\":\"$server\",\"type\":\"$type\",\"description\":\"$description\"}" 2>/dev/null \
    | _task_extract_id
}

# task_update ID STATUS_TEXT — free-text status message (e.g.
# "connected", "not connected"), stored as the task's description.
task_update() {
  local id="$1" status_text="$2"
  [[ -z "$id" ]] && return 0
  curl -s -X POST "$LOCALOPS_URL/tasks/$id/update" \
    -H 'Content-Type: application/json' \
    -d "{\"description\":\"$status_text\"}" >/dev/null 2>&1 || true
}

# task_heartbeat ID — call periodically to show the watcher process is
# still alive (README §8).
task_heartbeat() {
  local id="$1"
  [[ -z "$id" ]] && return 0
  curl -s -X POST "$LOCALOPS_URL/tasks/$id/heartbeat" >/dev/null 2>&1 || true
}

# task_stop ID — call on clean shutdown so the task doesn't sit "running"
# forever in `localops-cli tasks` after you've actually stopped the
# script.
task_stop() {
  local id="$1"
  [[ -z "$id" ]] && return 0
  curl -s -X POST "$LOCALOPS_URL/tasks/$id/stop" >/dev/null 2>&1 || true
}
