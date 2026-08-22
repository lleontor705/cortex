#!/bin/bash
# Cortex — Shared helpers for Claude Code hooks
# WARNING: Do not read from stdin here — scripts source this before reading their hook input.

# Detect project name from git remote, with fallbacks.
# Priority: git remote origin repo name > git root basename > cwd basename
detect_project() {
  local dir="$1"

  # Try git remote origin URL
  local url
  url=$(git -C "$dir" remote get-url origin 2>/dev/null)
  if [ -n "$url" ]; then
    local name
    name=$(echo "$url" | sed 's/\.git$//' | sed 's|.*[/:]||')
    if [ -n "$name" ]; then
      echo "$name"
      return
    fi
  fi

  # Fallback: git root directory name
  local root
  root=$(git -C "$dir" rev-parse --show-toplevel 2>/dev/null)
  if [ -n "$root" ]; then
    basename "$root"
    return
  fi

  # Final fallback: cwd basename
  basename "$dir"
}

# Emit a bounded, secret-free outcome classification on stderr. Never echo
# credentials, payloads, or response bodies.
cortex_signal() {
  printf '{"outcome":"%s"}\n' "$1" >&2
}

# Map a curl exit status and HTTP status to a stable classification token.
cortex_classify() {
  local curl_status=$1 http_status=$2
  if [[ $curl_status -eq 28 ]]; then printf 'timeout'; return; fi
  if [[ $curl_status -ne 0 ]]; then printf 'unavailable'; return; fi
  case "$http_status" in
    2??) printf 'success' ;;
    401) printf 'unauthorized' ;;
    403) printf 'forbidden' ;;
    409) printf 'conflict' ;;
    400|404|405|413|422) printf 'validation' ;;
    *) printf 'unavailable' ;;
  esac
}

# REM-TRANSPORT-001: plaintext delivery is loopback-only; any other host must
# be https. The 127/8 host must be a strict decimal dotted quad: octets 0-255,
# no leading zeros, no short forms, so ambiguous numeric hosts are never
# treated as loopback.
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

# SEC-04 credential precedence: explicit API key, HTTP token, then a
# readable config file. No credential means no protected traffic.
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
