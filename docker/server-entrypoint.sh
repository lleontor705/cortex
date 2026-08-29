#!/bin/sh
# Docker-only bootstrap for the development server. It creates a stable
# tenant-scoped bootstrap authority set on first start without placing credentials in the
# compose file or image layers. It intentionally does nothing unless enabled
# through CORTEX_SERVER_AUTO_BOOTSTRAP=true.
set -eu

if [ "${CORTEX_SERVER_AUTO_BOOTSTRAP:-false}" != "true" ]; then
  exec "$@"
fi

state_file="${CORTEX_BOOTSTRAP_STATE_FILE:-/home/cortex/.cortex/server-bootstrap.env}"
state_dir=$(dirname "$state_file")

fail() {
  echo "cortex: Docker bootstrap: $*" >&2
  exit 2
}

is_safe_value() {
  case "$1" in
    ""|*[!A-Za-z0-9_+/=-]*) return 1 ;;
    *) return 0 ;;
  esac
}

read_state_value() {
  key="$1"
  value=$(sed -n "s/^${key}=//p" "$state_file" | head -n 1)
  is_safe_value "$value" || fail "state file is malformed"
  printf '%s' "$value"
}

write_state() {
  umask 077
  mkdir -p "$state_dir"
  temp_file=$(mktemp "${state_file}.tmp.XXXXXX")
  {
    echo "CORTEX_SERVER_TENANT_ID=$CORTEX_SERVER_TENANT_ID"
    echo "CORTEX_SERVER_WORKSPACE_ID=$CORTEX_SERVER_WORKSPACE_ID"
    echo "CORTEX_SERVER_PRINCIPAL_SUBJECT=$CORTEX_SERVER_PRINCIPAL_SUBJECT"
    echo "CORTEX_HTTP_TOKEN=$CORTEX_HTTP_TOKEN"
  } > "$temp_file"
  chmod 600 "$temp_file"
  mv "$temp_file" "$state_file"
}

random_uuid() {
  cat /proc/sys/kernel/random/uuid
}

random_bearer() {
  random=$(dd if=/dev/urandom bs=48 count=1 2>/dev/null | base64)
  is_safe_value "$random" || fail "could not generate a tenant owner credential"
  printf 'cortex_admin_%s' "$random"
}

configured_count=0
for value in "${CORTEX_SERVER_TENANT_ID:-}" "${CORTEX_SERVER_WORKSPACE_ID:-}" "${CORTEX_SERVER_PRINCIPAL_SUBJECT:-}" "${CORTEX_HTTP_TOKEN:-}"; do
  [ -n "$value" ] && configured_count=$((configured_count + 1))
done

if [ -f "$state_file" ]; then
  saved_tenant=$(read_state_value CORTEX_SERVER_TENANT_ID)
  saved_workspace=$(read_state_value CORTEX_SERVER_WORKSPACE_ID)
  saved_subject=$(read_state_value CORTEX_SERVER_PRINCIPAL_SUBJECT)
  saved_token=$(read_state_value CORTEX_HTTP_TOKEN)

  [ "$configured_count" -eq 0 ] || {
    [ "${CORTEX_SERVER_TENANT_ID:-}" = "$saved_tenant" ] &&
      [ "${CORTEX_SERVER_WORKSPACE_ID:-}" = "$saved_workspace" ] &&
      [ "${CORTEX_SERVER_PRINCIPAL_SUBJECT:-}" = "$saved_subject" ] &&
      [ "${CORTEX_HTTP_TOKEN:-}" = "$saved_token" ] ||
      fail "explicit bootstrap values conflict with the persisted state"
  }

  CORTEX_SERVER_TENANT_ID="$saved_tenant"
  CORTEX_SERVER_WORKSPACE_ID="$saved_workspace"
  CORTEX_SERVER_PRINCIPAL_SUBJECT="$saved_subject"
  CORTEX_HTTP_TOKEN="$saved_token"
elif [ "$configured_count" -eq 0 ]; then
  CORTEX_SERVER_TENANT_ID=$(random_uuid)
  CORTEX_SERVER_WORKSPACE_ID=$(random_uuid)
  CORTEX_SERVER_PRINCIPAL_SUBJECT=$(random_uuid)
  CORTEX_HTTP_TOKEN=$(random_bearer)
  write_state

  echo "cortex: Docker bootstrap created the default tenant and owner credential; copy it now."
  echo "cortex: tenant_id=$CORTEX_SERVER_TENANT_ID workspace_id=$CORTEX_SERVER_WORKSPACE_ID"
  echo "cortex: tenant_owner_bearer=$CORTEX_HTTP_TOKEN"
  echo "cortex: this bearer is scoped to the default tenant; it is not a global SaaS administrator token."
  echo "cortex: the bearer is shown only for this initial Docker bootstrap; Docker logs may retain it."
elif [ "$configured_count" -ne 4 ]; then
  fail "configure all of tenant ID, workspace ID, principal subject, and HTTP token, or none"
fi

export CORTEX_SERVER_TENANT_ID CORTEX_SERVER_WORKSPACE_ID CORTEX_SERVER_PRINCIPAL_SUBJECT CORTEX_HTTP_TOKEN
exec "$@"
