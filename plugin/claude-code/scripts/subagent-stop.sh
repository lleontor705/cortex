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

# REM-TRANSPORT-001: plaintext delivery is loopback-only; any other host must
# be https. Validated before any credential is read so a misconfigured
# plaintext target can never receive the bearer token. The 127/8 host must be
# a strict decimal dotted quad: octets 0-255, no leading zeros, no short
# forms, so ambiguous numeric hosts are never treated as loopback.
validate_url() {
  local scheme rest host
  scheme=${CORTEX_URL%%://*}
  rest=${CORTEX_URL#*://}
  rest=${rest%%\#*}
  rest=${rest%%\?*}
  rest=${rest%%/*}
  host=${rest##*@}
  if [[ $host == \[* ]]; then
    host=${host#\[}
    host=${host%%\]*}
  else
    host=${host%%:*}
  fi
  if [[ $scheme == https ]]; then return 0; fi
  [[ $scheme == http ]] || return 1
  [[ $host == localhost || $host == ::1 || $host =~ ^127\.(25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9][0-9]|[0-9])\.(25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9][0-9]|[0-9])\.(25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9][0-9]|[0-9])$ ]]
}

# A 2xx response is success only when it is the persisted Observation for the
# exact session, content, and classification fields that were sent. Validation
# is field-identity based: additive server fields (e.g. tags) are tolerated,
# and an exact key count is never required.
classify_delivery() {
  local curl_status=$1 http_status=$2 response_file=$3 expect_session=$4 expect_content=$5 expect_project=$6
  if [[ $curl_status -eq 28 ]]; then signal timeout; return; fi
  if [[ $curl_status -ne 0 ]]; then signal unavailable; return; fi
  case "$http_status" in
    2??)
      if jq -e --arg sid "$expect_session" --arg content "$expect_content" \
        --arg title "Passive capture from subagent" --arg project "$expect_project" '
        type == "object" and
        (.id | type == "number") and .id > 0 and
        .session_id == $sid and
        .content == $content and
        .title == $title and
        .project == $project and
        .type == "passive" and
        .scope == "project"' "$response_file" >/dev/null 2>&1; then
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

  validate_url || { signal config; return; }
  token=$(credential)
  if [[ -z $token ]]; then signal config; return; fi

  # Keep this hook directly executable even if a neighboring installed asset
  # was checked out with CRLF line endings.
  project=$(basename -- "${cwd:-unknown}")
  capture=$(printf '%s' "$input" | jq --argjson limit "$CAPTURE_BYTE_LIMIT" \
    -f "${SCRIPT_DIR}/utf8-truncate.jq") || { signal validation; return; }
  expected_content=$(printf '%s' "$capture" | jq -r '.content')
  request=$(jq -cn --arg sid "$session_id" --arg project "$project" --argjson capture "$capture" \
    '{session_id:$sid,project:$project,title:"Passive capture from subagent",type:"passive",scope:"project"} + $capture') \
    || { signal validation; return; }

  response=$(mktemp "${TMPDIR:-/tmp}/cortex-subagent-response.XXXXXX") || { signal unavailable; return; }
  http_status=$(curl --silent --show-error --max-time "$DELIVERY_TIMEOUT_SECONDS" \
    --output "$response" --write-out '%{http_code}' \
    -X POST -H 'Content-Type: application/json' -H "Authorization: Bearer $token" \
    --data-binary "$request" "${CORTEX_URL}/api/observations" 2>/dev/null)
  curl_status=$?
  classify_delivery "$curl_status" "$http_status" "$response" "$session_id" "$expected_content" "$project"
  rm -f "$response"
}

main "$@"
exit 0
