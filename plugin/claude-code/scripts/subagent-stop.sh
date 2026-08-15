#!/usr/bin/env bash
# Cortex SubagentStop hook: authenticated, bounded passive delivery.

# Hook failures are observable but must never block Claude Code.
set -u

SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
CAPTURE_BYTE_LIMIT=2000
DELIVERY_TIMEOUT_SECONDS=1
CORTEX_HTTP_PORT="${CORTEX_HTTP_PORT:-7438}"
CORTEX_URL=${CORTEX_URL:-http://127.0.0.1:${CORTEX_HTTP_PORT}}

signal() {
  # Emit classification only. Never echo response bodies, credentials, or payloads.
  printf '{"outcome":"%s"}\n' "$1" >&2
}

credential() {
  if [[ -n ${CORTEX_API_KEY:-} ]]; then
    printf '%s' "$CORTEX_API_KEY"
  elif [[ -n ${CORTEX_HTTP_TOKEN:-} ]]; then
    printf '%s' "$CORTEX_HTTP_TOKEN"
  elif [[ -n ${CORTEX_CONFIG_FILE:-} && -r $CORTEX_CONFIG_FILE ]]; then
    jq -r '.api_key // .http_token // empty' \
      "$CORTEX_CONFIG_FILE" 2>/dev/null
  fi
}

classify_delivery() {
  local curl_status=$1 http_status=$2 response_file=$3
  if [[ $curl_status -eq 28 ]]; then signal timeout; return; fi
  if [[ $curl_status -ne 0 ]]; then signal unavailable; return; fi
  case "$http_status" in
    2??)
      if jq -e 'type == "object"' "$response_file" >/dev/null 2>&1; then
        signal success
      else
        signal invalid_response
      fi
      ;;
    401) signal unauthorized ;;
    403) signal forbidden ;;
    409) signal conflict ;;
    400|404|405|413|422) signal validation ;;
    *) signal unavailable ;;
  esac
}

main() {
  local input token session_id cwd project capture request response http_status curl_status
  input=$(cat) || { signal validation; return; }
  if ! jq -e 'type == "object"' <<<"$input" >/dev/null 2>&1; then
    signal validation
    return
  fi

  session_id=$(jq -r '.session_id // empty' <<<"$input")
  cwd=$(jq -r '.cwd // empty' <<<"$input")
  [[ $(jq -r '.stdout // empty | length' <<<"$input") -gt 0 ]] || return

  token=$(credential)
  if [[ -z $token ]]; then signal config; return; fi

  # Keep this hook directly executable even if a neighboring installed asset
  # was checked out with CRLF line endings.
  project=$(basename -- "${cwd:-unknown}")
  capture=$(printf '%s' "$input" | jq --argjson limit "$CAPTURE_BYTE_LIMIT" \
    -f "${SCRIPT_DIR}/utf8-truncate.jq") || { signal validation; return; }
  request=$(jq -cn --arg sid "$session_id" --arg project "$project" --argjson capture "$capture" \
    '{session_id:$sid,project:$project,title:"Passive capture from subagent",type:"passive",scope:"project"} + $capture') \
    || { signal validation; return; }

  response=$(mktemp "${TMPDIR:-/tmp}/cortex-subagent-response.XXXXXX") || { signal unavailable; return; }
  http_status=$(curl --silent --show-error --max-time "$DELIVERY_TIMEOUT_SECONDS" \
    --output "$response" --write-out '%{http_code}' \
    -X POST -H 'Content-Type: application/json' -H "Authorization: Bearer $token" \
    --data-binary "$request" "${CORTEX_URL}/api/observations" 2>/dev/null)
  curl_status=$?
  classify_delivery "$curl_status" "$http_status" "$response"
  rm -f "$response"
}

main "$@"
exit 0
