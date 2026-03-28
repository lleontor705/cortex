#!/bin/bash
# Cortex — UserPromptSubmit hook for Claude Code
#
# FIRST message: injects ToolSearch instruction to load all cortex memory tools.
# Subsequent messages: nudges to save if > 15 minutes since last save.

CORTEX_PORT="${CORTEX_PORT:-7438}"
CORTEX_URL="http://127.0.0.1:${CORTEX_PORT}"

# Load shared helpers
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "${SCRIPT_DIR}/_helpers.sh"

# Read hook input from stdin
INPUT=$(cat)
CWD=$(echo "$INPUT" | jq -r '.cwd // empty')
SESSION_ID=$(echo "$INPUT" | jq -r '.session_id // empty')
PROJECT=$(detect_project "$CWD")

# Default: no injection
OUTPUT="{}"

# First-message detection via state file
if [ -n "$SESSION_ID" ]; then
  SESSION_KEY="cortex-claude-${SESSION_ID}-tools-loaded"
else
  SAFE_PROJECT=$(printf '%s' "${PROJECT:-unknown}" | tr -cs 'a-zA-Z0-9_-' '_')
  SESSION_KEY="cortex-claude-${SAFE_PROJECT}-$$-tools-loaded"
fi

STATE_FILE="/tmp/${SESSION_KEY}"

if [ ! -f "$STATE_FILE" ]; then
  # First message — inject ToolSearch + mem_context instruction
  touch "$STATE_FILE" 2>/dev/null || true

  TOOL_MSG="CRITICAL FIRST ACTION — Execute this ToolSearch NOW before responding to the user:"$'\n'"select:mcp__plugin_cortex_cortex__mem_save,mcp__plugin_cortex_cortex__mem_search,mcp__plugin_cortex_cortex__mem_context,mcp__plugin_cortex_cortex__mem_session_summary,mcp__plugin_cortex_cortex__mem_get_observation,mcp__plugin_cortex_cortex__mem_save_prompt"$'\n\n'"After loading tools, call mem_context to check for prior session history before responding."
  OUTPUT=$(jq -n --arg msg "$TOOL_MSG" '{"systemMessage": $msg}')

  printf '%s\n' "$OUTPUT"
  exit 0
fi

# Subsequent messages — save nudge logic
if [ -z "$PROJECT" ]; then
  echo "$OUTPUT"
  exit 0
fi

# Check session age — skip nudge if < 5 minutes
SESSION_START=""
if [ -n "$SESSION_ID" ]; then
  SESSION_START=$(curl -sf "${CORTEX_URL}/api/sessions/${SESSION_ID}" --max-time 0.2 2>/dev/null \
    | jq -r '.started_at // empty' 2>/dev/null)
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
ENCODED_PROJECT=$(printf '%s' "$PROJECT" | jq -sRr @uri)
LAST_SAVE_JSON=$(curl -sf \
  "${CORTEX_URL}/api/observations?project=${ENCODED_PROJECT}&limit=1" \
  --max-time 0.2 2>/dev/null)

if [ -z "$LAST_SAVE_JSON" ]; then
  echo "$OUTPUT"
  exit 0
fi

LAST_SAVE_AT=$(echo "$LAST_SAVE_JSON" | jq -r '.[0].created_at // empty' 2>/dev/null)

if [ -z "$LAST_SAVE_AT" ]; then
  echo "$OUTPUT"
  exit 0
fi

LAST_EPOCH=$(date -j -f "%Y-%m-%dT%H:%M:%S" "${LAST_SAVE_AT%%.*}" "+%s" 2>/dev/null \
  || date -d "${LAST_SAVE_AT%%.*}" "+%s" 2>/dev/null \
  || echo "0")
NOW_EPOCH=$(date "+%s")
ELAPSED=$(( NOW_EPOCH - LAST_EPOCH ))

# Nudge if > 15 minutes since last save
if [ "$ELAPSED" -gt 900 ]; then
  OUTPUT=$(jq -n \
    '{"systemMessage": "MEMORY REMINDER: It'\''s been over 15 minutes since your last save. If you'\''ve made decisions, discoveries, or completed significant work, call mem_save now."}')
fi

echo "$OUTPUT"
exit 0
