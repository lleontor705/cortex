#!/bin/bash
# Cortex — SubagentStop hook for Claude Code (async)
#
# Reads subagent output from stdin, POSTs to passive capture endpoint.

CORTEX_PORT="${CORTEX_PORT:-7438}"
CORTEX_URL="http://127.0.0.1:${CORTEX_PORT}"

# Load shared helpers
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "${SCRIPT_DIR}/_helpers.sh"

# Read hook input from stdin
INPUT=$(cat)
SESSION_ID=$(echo "$INPUT" | jq -r '.session_id // empty')
CWD=$(echo "$INPUT" | jq -r '.cwd // empty')
OUTPUT=$(echo "$INPUT" | jq -r '.stdout // empty')
PROJECT=$(detect_project "$CWD")

# Nothing to capture if no output
[ -z "$OUTPUT" ] && exit 0

# Create observation via HTTP API
curl -sf "${CORTEX_URL}/api/observations" \
  -X POST \
  -H "Content-Type: application/json" \
  -d "$(jq -n \
    --arg sid "$SESSION_ID" \
    --arg content "$OUTPUT" \
    --arg project "$PROJECT" \
    --arg title "Passive capture from subagent" \
    '{session_id: $sid, content: $content, project: $project, title: $title, type: "passive", scope: "project"}')" \
  > /dev/null 2>&1

exit 0
