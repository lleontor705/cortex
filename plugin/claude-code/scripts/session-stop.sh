#!/usr/bin/env bash
# Cortex Stop hook: authenticated session-end delivery that never blocks the host.

set -u

DELIVERY_TIMEOUT_SECONDS=1
CORTEX_HTTP_PORT="${CORTEX_HTTP_PORT:-7438}"
CORTEX_URL=${CORTEX_URL:-http://127.0.0.1:${CORTEX_HTTP_PORT}}

signal() { printf '{"outcome":"%s"}\n' "$1" >&2; }

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
      # The session-end endpoint returns exactly {"status":"ended"}; any other
      # 2xx body (empty object, another endpoint's, extra keys) is a false
      # success and must classify as invalid_response.
      if jq -e 'type == "object" and (keys | length) == 1 and .status == "ended"' "$response_file" >/dev/null 2>&1; then
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
  local input session_id encoded_session token response http_status curl_status
  input=$(cat) || { signal validation; return; }
  if ! session_id=$(jq -er '.session_id | select(type == "string" and length > 0)' <<<"$input" 2>/dev/null); then
    signal validation
    return
  fi
  validate_url || { signal config; return; }
  token=$(credential)
  if [[ -z $token ]]; then signal config; return; fi
  encoded_session=$(printf '%s' "$session_id" | jq -sRr @uri)
  response=$(mktemp "${TMPDIR:-/tmp}/cortex-session-response.XXXXXX") || { signal unavailable; return; }
  http_status=$(curl --silent --show-error --max-time "$DELIVERY_TIMEOUT_SECONDS" \
    --output "$response" --write-out '%{http_code}' \
    -X POST -H 'Content-Type: application/json' -H "Authorization: Bearer $token" \
    --data-binary '{}' "${CORTEX_URL}/api/sessions/${encoded_session}/end" 2>/dev/null)
  curl_status=$?
  classify_delivery "$curl_status" "$http_status" "$response"
  rm -f "$response"
}

main "$@"
exit 0
