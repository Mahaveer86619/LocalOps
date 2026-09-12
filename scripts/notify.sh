#!/usr/bin/env bash
# notify.sh — force an immediate Slack notification with arbitrary
# content via LocalOps' generic POST /notify. No state, no debounce, no
# threshold: the caller has already decided this is worth sending.
#
# Usage: notify.sh "<message>" [level] [source]
#   level  — info | warning | critical (default: info)
#   source — free text identifying the caller (default: $NOTIFY_SOURCE or "manual")
#
# Meant to be sourced or called by other scripts (see tunnel-watch.sh,
# ping-watch.sh) as well as used standalone / from `localops-cli notify`.
set -uo pipefail

MESSAGE="${1:?usage: notify.sh <message> [level] [source]}"
LEVEL="${2:-info}"
SOURCE="${3:-${NOTIFY_SOURCE:-manual}}"
LOCALOPS_URL="${LOCALOPS_URL:-http://localhost:7717}"

# Escapes a string for embedding in a JSON string literal without a hard
# jq/python dependency - handles backslashes, quotes, and newlines, which
# covers everything these scripts actually send.
json_escape() {
  local s="$1"
  s="${s//\\/\\\\}"
  s="${s//\"/\\\"}"
  s="${s//$'\n'/\\n}"
  printf '%s' "$s"
}

curl -s -X POST "$LOCALOPS_URL/notify" \
  -H 'Content-Type: application/json' \
  -d "{\"message\":\"$(json_escape "$MESSAGE")\",\"level\":\"$LEVEL\",\"source\":\"$SOURCE\"}" \
  >/dev/null 2>&1 || true
