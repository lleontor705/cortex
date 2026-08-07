#!/bin/bash
# Cortex — SessionStart hook for Claude Code
#
# 1. Ensures the cortex HTTP server is reachable
# 2. Creates a session in cortex
# 3. Injects Memory Protocol instructions + memory context

CORTEX_HTTP_PORT="${CORTEX_HTTP_PORT:-7438}"
CORTEX_URL="http://127.0.0.1:${CORTEX_HTTP_PORT}"

# Load shared helpers
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "${SCRIPT_DIR}/_helpers.sh"

# Read hook input from stdin
INPUT=$(cat)
SESSION_ID=$(echo "$INPUT" | jq -r '.session_id // empty')
CWD=$(echo "$INPUT" | jq -r '.cwd // empty')
PROJECT=$(detect_project "$CWD")

# Ensure cortex server is running
if ! curl -sf "${CORTEX_URL}/health" --max-time 1 > /dev/null 2>&1; then
  cortex serve &>/dev/null &
  sleep 0.5
fi

# Create session
if [ -n "$SESSION_ID" ] && [ -n "$PROJECT" ]; then
  curl -sf "${CORTEX_URL}/api/sessions" \
    -X POST \
    -H "Content-Type: application/json" \
    -d "$(jq -n --arg id "$SESSION_ID" --arg project "$PROJECT" --arg dir "$CWD" \
      '{id: $id, project: $project, directory: $dir}')" \
    > /dev/null 2>&1
fi

# Fetch memory context
ENCODED_PROJECT=$(printf '%s' "$PROJECT" | jq -sRr @uri)
CONTEXT=$(curl -sf "${CORTEX_URL}/api/search?q=*&project=${ENCODED_PROJECT}&limit=10" --max-time 3 2>/dev/null | jq -r '.[].title // empty' 2>/dev/null | head -20)

# Inject Memory Protocol + context — stdout goes to Claude as additionalContext
cat <<'PROTOCOL'
## Cortex Persistent Memory — ACTIVE PROTOCOL

You have cortex memory tools. This protocol is MANDATORY and ALWAYS ACTIVE.

### CORE TOOLS — always available, no ToolSearch needed
cortex_save, cortex_search, cortex_context, cortex_session_summary, cortex_get_observation, cortex_save_prompt

Use ToolSearch for other tools: cortex_update, cortex_suggest_topic_key, cortex_session_start, cortex_session_end, cortex_stats, cortex_delete, cortex_timeline, cortex_capture_passive, cortex_relate, cortex_graph, cortex_score, cortex_archive, cortex_search_hybrid

### PROACTIVE SAVE — do NOT wait for user to ask
Call `cortex_save` IMMEDIATELY after ANY of these:
- Decision made (architecture, convention, workflow, tool choice)
- Bug fixed (include root cause)
- Convention or workflow documented/updated
- Non-obvious discovery, gotcha, or edge case found
- Pattern established (naming, structure, approach)
- User preference or constraint learned
- Feature implemented with non-obvious approach
- User confirms your recommendation ("dale", "go with that", "sounds good")
- User rejects an approach or expresses a preference

**Self-check after EVERY task**: "Did I or the user just make a decision, confirm a recommendation, express a preference, fix a bug, learn something, or establish a convention? If yes → cortex_save NOW."

### KNOWLEDGE GRAPH — relate observations
After saving related observations, use `cortex_relate` to create relationships:
- references, relates_to, follows, supersedes, contradicts
Use `cortex_graph` to explore connections from any observation.

### SEARCH MEMORY when:
- User asks to recall anything
- Starting work on something that might have been done before
- User mentions a topic you have no context on
- User's FIRST message references the project — call `cortex_search` with keywords

### SESSION CLOSE — before saying "done"/"listo":
Call `cortex_session_summary` with: Goal, Discoveries, Accomplished, Next Steps, Relevant Files.
PROTOCOL

# Inject memory context if available
if [ -n "$CONTEXT" ]; then
  printf "\n### Recent memories for project '%s':\n%s\n" "$PROJECT" "$CONTEXT"
fi

exit 0
