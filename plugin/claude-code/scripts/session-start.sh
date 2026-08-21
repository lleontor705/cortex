#!/bin/bash
# Cortex — SessionStart hook for Claude Code
#
# 1. Validates CORTEX_URL and reads the configured credential
# 2. Ensures the cortex HTTP server is reachable (only with a credential)
# 3. Creates a session, confirmed only by an exact persisted-identity echo
#    or a 409 proven through the read endpoint (SEC-04)
# 4. Injects the static Memory Protocol plus validated, authenticated
#    memory context
#
# Hook failures are observable through classifications but never block the host.

set -u

SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
CORTEX_HTTP_PORT="${CORTEX_HTTP_PORT:-7438}"
CORTEX_URL=${CORTEX_URL:-http://127.0.0.1:${CORTEX_HTTP_PORT}}
DELIVERY_TIMEOUT_SECONDS=2

source "${SCRIPT_DIR}/_helpers.sh"

# A 2xx body confirms the session only when it is the persisted Session for
# the exact identity that was sent (or read back on conflict).
session_echo_ok() {
  local file=$1 sid=$2 project=$3 dir=$4
  jq -e --arg sid "$sid" --arg project "$project" --arg dir "$dir" '
    type == "object" and
    .id == $sid and .project == $project and .directory == $dir and
    (.started_at | type == "string")' "$file" >/dev/null 2>&1
}

main() {
  local input session_id cwd project token encoded_session encoded_project
  local response http_status curl_status outcome confirmed=0 context_ok=0
  input=$(cat) || { cortex_signal validation; return; }
  session_id=$(jq -r '.session_id // empty' <<<"$input" 2>/dev/null)
  cwd=$(jq -r '.cwd // empty' <<<"$input" 2>/dev/null)
  project=$(detect_project "$cwd")

  validate_url || { cortex_signal config; return; }
  token=$(credential)
  if [[ -z $token ]]; then cortex_signal config; return; fi

  # Ensure the server is reachable. The spawn attempt happens only with a
  # configured credential: an unauthenticated hook sends nothing and starts
  # nothing.
  if ! curl -sf --max-time 1 "${CORTEX_URL}/health" >/dev/null 2>&1; then
    cortex serve >/dev/null 2>&1 &
    sleep 0.5
  fi

  if [[ -n $session_id && -n $project ]]; then
    response=$(mktemp "${TMPDIR:-/tmp}/cortex-session-start.XXXXXX") || { cortex_signal unavailable; return; }
    http_status=$(curl --silent --show-error --max-time "$DELIVERY_TIMEOUT_SECONDS" \
      --output "$response" --write-out '%{http_code}' \
      -X POST -H 'Content-Type: application/json' -H "Authorization: Bearer $token" \
      --data-binary "$(jq -cn --arg id "$session_id" --arg project "$project" --arg dir "$cwd" \
        '{id: $id, project: $project, directory: $dir}')" \
      "${CORTEX_URL}/api/sessions" 2>/dev/null)
    curl_status=$?
    outcome=$(cortex_classify "$curl_status" "$http_status")
    case "$outcome" in
      success)
        if session_echo_ok "$response" "$session_id" "$project" "$cwd"; then
          confirmed=1
        else
          cortex_signal invalid_response
        fi
        ;;
      conflict)
        # A bare 409 body is an error object and cannot prove which session
        # exists: confirm the exact persisted identity through the read
        # endpoint before trusting it.
        encoded_session=$(printf '%s' "$session_id" | jq -sRr @uri)
        http_status=$(curl --silent --show-error --max-time "$DELIVERY_TIMEOUT_SECONDS" \
          --output "$response" --write-out '%{http_code}' \
          -H "Authorization: Bearer $token" \
          "${CORTEX_URL}/api/sessions/${encoded_session}" 2>/dev/null)
        curl_status=$?
        outcome=$(cortex_classify "$curl_status" "$http_status")
        if [[ $outcome == success ]] && session_echo_ok "$response" "$session_id" "$project" "$cwd"; then
          confirmed=1
        else
          cortex_signal invalid_response
        fi
        ;;
      *)
        cortex_signal "$outcome"
        ;;
    esac
    rm -f "$response"
  fi

  # Memory context is trusted only from an authenticated read after a
  # confirmed session, and only when every item is an object with a string
  # title. Anything else is classified and injected as nothing.
  CONTEXT=''
  if [[ $confirmed == 1 ]]; then
    encoded_project=$(printf '%s' "$project" | jq -sRr @uri)
    if response=$(mktemp "${TMPDIR:-/tmp}/cortex-search.XXXXXX"); then
      http_status=$(curl --silent --show-error --max-time "$DELIVERY_TIMEOUT_SECONDS" \
        --output "$response" --write-out '%{http_code}' \
        -H "Authorization: Bearer $token" \
        "${CORTEX_URL}/api/search?q=*&project=${encoded_project}&limit=10" 2>/dev/null)
      curl_status=$?
      outcome=$(cortex_classify "$curl_status" "$http_status")
      if [[ $outcome == success ]]; then
        if jq -e 'type == "array" and all(.[]; type == "object" and (.title | type == "string"))' \
          "$response" >/dev/null 2>&1; then
          CONTEXT=$(jq -r '.[].title' "$response" 2>/dev/null | head -20)
          context_ok=1
        else
          cortex_signal invalid_response
        fi
      else
        cortex_signal "$outcome"
      fi
      rm -f "$response"
    fi
  fi

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
- User's FIRST message references the project — call cortex_search with keywords

### SESSION CLOSE — before saying "done"/"listo":
Call cortex_session_summary with: Goal, Discoveries, Accomplished, Next Steps, Relevant Files.
PROTOCOL

  # Inject memory context only when it was read and validated
  if [ -n "$CONTEXT" ]; then
    printf "\n### Recent memories for project '%s':\n%s\n" "$project" "$CONTEXT"
  fi

  # Exactly one success classification, and only when the whole protected
  # flow completed: session identity confirmed and context read validated.
  if [[ $confirmed == 1 && $context_ok == 1 ]]; then
    cortex_signal success
  fi
}

main "$@"
exit 0
