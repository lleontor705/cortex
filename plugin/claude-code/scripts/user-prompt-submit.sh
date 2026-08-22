#!/bin/bash
# Cortex — UserPromptSubmit hook for Claude Code
#
# FIRST message: injects the static ToolSearch instruction (no network).
# Subsequent messages: authenticated, validated protected reads decide the
# save nudge. Hook failures never block Claude Code; every failed read
# classifies, skips the nudge, and retries on a later host event.

set -u

SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
CORTEX_HTTP_PORT="${CORTEX_HTTP_PORT:-7438}"
CORTEX_URL=${CORTEX_URL:-http://127.0.0.1:${CORTEX_HTTP_PORT}}
READ_TIMEOUT_SECONDS=1

source "${SCRIPT_DIR}/_helpers.sh"

main() {
  local input cwd session_id project output token safe_project
  local encoded_session encoded_project response http_status curl_status outcome
  input=$(cat) || { echo '{}'; exit 0; }
  cwd=$(jq -r '.cwd // empty' <<<"$input" 2>/dev/null)
  session_id=$(jq -r '.session_id // empty' <<<"$input" 2>/dev/null)
  project=$(detect_project "$cwd")

  # Default: no injection
  OUTPUT="{}"

  # First-message detection via state file (local state only, no network)
  if [ -n "$session_id" ]; then
    SESSION_KEY="cortex-claude-${session_id}-tools-loaded"
  else
    safe_project=$(printf '%s' "${project:-unknown}" | tr -cs 'a-zA-Z0-9_-' '_')
    SESSION_KEY="cortex-claude-${safe_project}-$$-tools-loaded"
  fi

  STATE_FILE="/tmp/${SESSION_KEY}"

  if [ ! -f "$STATE_FILE" ]; then
    # First message — inject the static ToolSearch + cortex_context instruction
    touch "$STATE_FILE" 2>/dev/null || true

    TOOL_MSG="CRITICAL FIRST ACTION — Execute this ToolSearch NOW before responding to the user:"$'\n'"select:mcp__plugin_cortex_cortex__cortex_save,mcp__plugin_cortex_cortex__cortex_search,mcp__plugin_cortex_cortex__cortex_context,mcp__plugin_cortex_cortex__cortex_session_summary,mcp__plugin_cortex_cortex__cortex_get_observation,mcp__plugin_cortex_cortex__cortex_save_prompt,mcp__plugin_cortex_cortex__cortex_relate,mcp__plugin_cortex_cortex__cortex_graph,mcp__plugin_cortex_cortex__cortex_search_hybrid,mcp__plugin_cortex_cortex__cortex_revision_history"$'\n\n'"After loading tools, call cortex_context to check for prior session history before responding."
    OUTPUT=$(jq -n --arg msg "$TOOL_MSG" '{"systemMessage": $msg}')

    printf '%s\n' "$OUTPUT"
    exit 0
  fi

  # Subsequent messages — save nudge logic
  if [ -z "$project" ]; then
    echo "$OUTPUT"
    exit 0
  fi

  # SEC-04: every protected read needs a validated target and a configured
  # credential; any failure skips the nudge and retries on a later event.
  validate_url || { cortex_signal config; echo "$OUTPUT"; exit 0; }
  token=$(credential)
  if [[ -z $token ]]; then cortex_signal config; echo "$OUTPUT"; exit 0; fi

  # Check session age — skip nudge if < 5 minutes
  SESSION_START=""
  if [ -n "$session_id" ]; then
    response=$(mktemp "${TMPDIR:-/tmp}/cortex-prompt-session.XXXXXX") || { echo "$OUTPUT"; exit 0; }
    encoded_session=$(printf '%s' "$session_id" | jq -sRr @uri)
    http_status=$(curl --silent --show-error --max-time "$READ_TIMEOUT_SECONDS" \
      --output "$response" --write-out '%{http_code}' \
      -H "Authorization: Bearer $token" \
      "${CORTEX_URL}/api/sessions/${encoded_session}" 2>/dev/null)
    curl_status=$?
    outcome=$(cortex_classify "$curl_status" "$http_status")
    if [[ $outcome != success ]]; then
      cortex_signal "$outcome"
      rm -f "$response"
      echo "$OUTPUT"
      exit 0
    fi
    if ! jq -e '(.started_at | type == "string") and length > 0' "$response" >/dev/null 2>&1; then
      cortex_signal invalid_response
      rm -f "$response"
      echo "$OUTPUT"
      exit 0
    fi
    SESSION_START=$(jq -r '.started_at' "$response" 2>/dev/null)
    rm -f "$response"
  fi

  if [ -n "$SESSION_START" ]; then
    SESSION_START_EPOCH=$(date -j -f "%Y-%m-%dT%H:%M:%S" "${SESSION_START%%.*}" "+%s" 2>/dev/null \
      || date -d "${SESSION_START%%.*}" "+%s" 2>/dev/null \
      || echo "0")
    NOW_EPOCH=$(date "+%s")
    SESSION_AGE_SECS=$(( NOW_EPOCH - SESSION_START_EPOCH ))

    if [ "$SESSION_AGE_SECS" -lt 300 ]; then
      echo "$OUTPUT"
      exit 0
    fi
  fi

  # Check last save time
  encoded_project=$(printf '%s' "$project" | jq -sRr @uri)
  response=$(mktemp "${TMPDIR:-/tmp}/cortex-prompt-observations.XXXXXX") || { echo "$OUTPUT"; exit 0; }
  http_status=$(curl --silent --show-error --max-time "$READ_TIMEOUT_SECONDS" \
    --output "$response" --write-out '%{http_code}' \
    -H "Authorization: Bearer $token" \
    "${CORTEX_URL}/api/observations?project=${encoded_project}&limit=1" 2>/dev/null)
  curl_status=$?
  outcome=$(cortex_classify "$curl_status" "$http_status")
  if [[ $outcome != success ]]; then
    cortex_signal "$outcome"
    rm -f "$response"
    echo "$OUTPUT"
    exit 0
  fi
  if ! jq -e 'type == "array" and length > 0 and (.[0] | type == "object") and (.[0].created_at | type == "string")' \
    "$response" >/dev/null 2>&1; then
    cortex_signal invalid_response
    rm -f "$response"
    echo "$OUTPUT"
    exit 0
  fi
  LAST_SAVE_AT=$(jq -r '.[0].created_at' "$response" 2>/dev/null)
  rm -f "$response"

  LAST_EPOCH=$(date -j -f "%Y-%m-%dT%H:%M:%S" "${LAST_SAVE_AT%%.*}" "+%s" 2>/dev/null \
    || date -d "${LAST_SAVE_AT%%.*}" "+%s" 2>/dev/null \
    || echo "0")
  NOW_EPOCH=$(date "+%s")
  ELAPSED=$(( NOW_EPOCH - LAST_EPOCH ))

  # Nudge if > 15 minutes since last save
  if [ "$ELAPSED" -gt 900 ]; then
    OUTPUT=$(jq -n \
      '{"systemMessage": "MEMORY REMINDER: It'\''s been over 15 minutes since your last save. If you'\''ve made decisions, discoveries, or completed significant work, call cortex_save now."}')
  fi

  echo "$OUTPUT"
  exit 0
}

main "$@"
exit 0
