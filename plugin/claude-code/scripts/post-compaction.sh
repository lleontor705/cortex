#!/bin/bash
# Cortex — Post-compaction hook for Claude Code
#
# When compaction happens, inject Memory Protocol + context and instruct
# the agent to persist the compacted summary via cortex_session_summary.

CORTEX_PORT="${CORTEX_PORT:-7438}"
CORTEX_URL="http://127.0.0.1:${CORTEX_PORT}"

# Load shared helpers
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "${SCRIPT_DIR}/_helpers.sh"

# Read hook input from stdin
INPUT=$(cat)
SESSION_ID=$(echo "$INPUT" | jq -r '.session_id // empty')
CWD=$(echo "$INPUT" | jq -r '.cwd // empty')
PROJECT=$(detect_project "$CWD")

# Ensure session exists
if [ -n "$SESSION_ID" ] && [ -n "$PROJECT" ]; then
  curl -sf "${CORTEX_URL}/api/sessions" \
    -X POST \
    -H "Content-Type: application/json" \
    -d "$(jq -n --arg id "$SESSION_ID" --arg project "$PROJECT" --arg dir "$CWD" \
      '{id: $id, project: $project, directory: $dir}')" \
    > /dev/null 2>&1
fi

# Inject Memory Protocol + compaction instruction
cat <<'PROTOCOL'
## Cortex Persistent Memory — ACTIVE PROTOCOL

You have cortex memory tools. This protocol is MANDATORY and ALWAYS ACTIVE.

### CORE TOOLS — always available
cortex_save, cortex_search, cortex_context, cortex_session_summary, cortex_get_observation, cortex_save_prompt

### PROACTIVE SAVE — do NOT wait for user to ask
Call `cortex_save` IMMEDIATELY after decisions, bugfixes, discoveries, patterns, preferences.

**Self-check after EVERY task**: "Did I just make a decision, fix a bug, or learn something? If yes → cortex_save NOW."

### SESSION CLOSE — before saying "done"/"listo":
Call `cortex_session_summary` with: Goal, Discoveries, Accomplished, Next Steps, Relevant Files.

---

CRITICAL INSTRUCTION POST-COMPACTION — follow these steps IN ORDER:
PROTOCOL

printf "\n1. FIRST: Call cortex_session_summary with the content of the compacted summary above. Use project: '%s'.\n" "$PROJECT"
printf "   This preserves what was accomplished before compaction.\n\n"
printf "2. THEN: Call cortex_context with project: '%s' to recover recent session history and observations.\n" "$PROJECT"
printf "   Read the returned context carefully — it tells you what was being worked on.\n\n"
cat <<'PROTOCOL'
3. If you need more detail on a specific topic, call cortex_search with relevant keywords.

4. Only THEN continue working on what the user asked.

All 4 steps are MANDATORY. Without them, you lose context and start blind.
PROTOCOL

exit 0
