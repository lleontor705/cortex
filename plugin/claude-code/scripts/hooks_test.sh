#!/usr/bin/env bash
# Deterministic contract harness for the Claude delivery hooks (REM-PLUGIN-001).
# Requires bash, jq, python3, and coreutils timeout; missing tools bail out with
# exit 127 so the gate is reported BLOCKED instead of silently passing.

set -uo pipefail

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
SUBAGENT_HOOK="$SCRIPT_DIR/subagent-stop.sh"
SESSION_HOOK="$SCRIPT_DIR/session-stop.sh"
API_KEY='cortex-secret-T23-canary'
PAYLOAD_CANARY='private-payload-T23-canary'
PASS=0
FAIL=0

fail() { printf 'not ok - %s\n' "$1"; FAIL=$((FAIL + 1)); }
pass() { printf 'ok - %s\n' "$1"; PASS=$((PASS + 1)); }
fixture_fail() {
  printf 'Bail out! fixture construction failed: %s\n' "$1" >&2
  exit 1
}

require() {
  command -v "$1" >/dev/null 2>&1 || {
    printf 'Bail out! required command not found: %s\n' "$1" >&2
    exit 127
  }
}

require jq
require python3
require timeout

TMP_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/cortex-hooks-test.XXXXXX") || exit 1
trap 'rm -rf "$TMP_ROOT"' EXIT HUP INT TERM
mkdir -p "$TMP_ROOT/bin"

cat >"$TMP_ROOT/bin/curl" <<'FIXTURE'
#!/usr/bin/env bash
set -u
: "${CORTEX_FIXTURE_LOG:?}"
printf '%q ' "$@" >>"$CORTEX_FIXTURE_LOG"
printf '\n' >>"$CORTEX_FIXTURE_LOG"

output=''
write_out=''
data=''
while (($#)); do
  case "$1" in
    -o|--output) output=${2-}; shift 2 ;;
    -w|--write-out) write_out=${2-}; shift 2 ;;
    -d|--data|--data-raw|--data-binary) data=${2-}; shift 2 ;;
    *) shift ;;
  esac
done
printf '%s' "$data" >"${CORTEX_FIXTURE_LOG}.body"

case "${CORTEX_FIXTURE_MODE:-http}" in
  transport) exit 7 ;;
  timeout) exit 28 ;;
esac

body='{"id":1,"status":"created"}'
if [[ -n ${CORTEX_FIXTURE_ECHO:-} ]]; then
  # Echo the exact persisted Observation the real server returns for the
  # request this fixture just captured, optionally tampered per case. The
  # additive tags field proves the contract tolerates extra server fields.
  body=$(jq -c '{id:42,title:"Passive capture from subagent",type:"passive",scope:"project",topic_key:"",confidence:0,source:"",tags:[],created_at:"2026-01-01T00:00:00Z",updated_at:"2026-01-01T00:00:00Z"} + {session_id:.session_id,project:.project,content:.content}' \
    "${CORTEX_FIXTURE_LOG}.body") || exit 9
  case "${CORTEX_FIXTURE_TAMPER:-}" in
    session_id) body=$(printf '%s' "$body" | jq -c '.session_id = "tampered-session"') ;;
    content) body=$(printf '%s' "$body" | jq -c '.content = "tampered-content"') ;;
    title) body=$(printf '%s' "$body" | jq -c '.title = "tampered-title"') ;;
    project) body=$(printf '%s' "$body" | jq -c '.project = "tampered-project"') ;;
    id) body=$(printf '%s' "$body" | jq -c '.id = 0') ;;
  esac
fi
body=${CORTEX_FIXTURE_BODY-$body}
code=${CORTEX_FIXTURE_STATUS:-200}
if [[ -n "$output" ]]; then
  printf '%s' "$body" >"$output"
else
  printf '%s' "$body"
fi
if [[ -n "$write_out" ]]; then
  printf '%s' "${write_out//\%\{http_code\}/$code}"
fi
exit 0
FIXTURE
chmod +x "$TMP_ROOT/bin/curl"

run_hook() {
  local hook=$1 input=$2 name=$3
  shift 3
  local case_dir="$TMP_ROOT/$name"
  mkdir -p "$case_dir"
  : >"$case_dir/request"
  printf '%s' "$input" | CORTEX_API_KEY="$API_KEY" env \
    "$@" \
    PATH="$TMP_ROOT/bin:$PATH" \
    CORTEX_FIXTURE_LOG="$case_dir/request" \
    timeout 2 "$hook" >"$case_dir/stdout" 2>"$case_dir/stderr"
  printf '%s' "$?" >"$case_dir/status"
}

run_hook_file() {
  local hook=$1 input_file=$2 name=$3
  shift 3
  local case_dir="$TMP_ROOT/$name"
  mkdir -p "$case_dir"
  : >"$case_dir/request"
  env \
    PATH="$TMP_ROOT/bin:$PATH" \
    CORTEX_FIXTURE_LOG="$case_dir/request" \
    CORTEX_API_KEY="$API_KEY" \
    "$@" \
    timeout 2 bash -c 'cat >"$1" || exit 125; bash "$2" <"$1"' \
      _ "$case_dir/received-input" "$hook" \
      <"$input_file" >"$case_dir/stdout" 2>"$case_dir/stderr"
  printf '%s' "$?" >"$case_dir/status"
}

assert_nonblocking() {
  local name=$1
  [[ $(cat "$TMP_ROOT/$name/status") == 0 ]] \
    && pass "$name returns control" \
    || fail "$name must return zero without blocking"
}

combined_output() { cat "$TMP_ROOT/$1/stdout" "$TMP_ROOT/$1/stderr"; }

assert_no_canaries() {
  local name=$1
  if combined_output "$name" | grep -F -e "$API_KEY" -e "$PAYLOAD_CANARY" >/dev/null; then
    fail "$name leaks a secret or payload canary"
  else
    pass "$name redacts secret and payload canaries"
  fi
}

assert_outcome() {
  local name=$1 expected=$2
  if combined_output "$name" | grep -Eiq "(outcome[\"': =]+|\b)$expected\b"; then
    pass "$name reports $expected"
  else
    fail "$name must report $expected"
  fi
}

assert_no_success() {
  local name=$1
  if combined_output "$name" | grep -Eiq 'outcome[\"'"'"': =]+success|delivery[[:space:]_-]*success'; then
    fail "$name reports false success"
  else
    pass "$name reports no success"
  fi
}

session_input='{"session_id":"session-T23"}'
subagent_input=$(jq -cn --arg content "evidence $PAYLOAD_CANARY" \
  '{session_id:"session-T23",cwd:"/tmp",stdout:$content}')

# ORACLE-PLUGIN-001: allowed environment credential, clean environment, and auth failures.
for hook_name in session subagent; do
  if [[ $hook_name == session ]]; then hook=$SESSION_HOOK; input=$session_input; else hook=$SUBAGENT_HOOK; input=$subagent_input; fi

  run_hook "$hook" "$input" "$hook_name-credential"
  assert_nonblocking "$hook_name-credential"
  if grep -F "$API_KEY" "$TMP_ROOT/$hook_name-credential/request" >/dev/null; then
    pass "$hook_name-credential sends configured credential"
  else
    fail "$hook_name-credential must authenticate from CORTEX_API_KEY"
  fi
  assert_no_canaries "$hook_name-credential"

  run_hook "$hook" "$input" "$hook_name-missing" -u CORTEX_API_KEY
  assert_nonblocking "$hook_name-missing"
  assert_outcome "$hook_name-missing" config
  assert_no_success "$hook_name-missing"
  [[ ! -s "$TMP_ROOT/$hook_name-missing/request" ]] \
    && pass "$hook_name-missing sends no unauthenticated request" \
    || fail "$hook_name-missing must not send a request"

  for code in 401 403; do
    label=unauthorized; [[ $code == 403 ]] && label=forbidden
    run_hook "$hook" "$input" "$hook_name-$code" \
      CORTEX_FIXTURE_STATUS="$code" \
      CORTEX_FIXTURE_BODY="{\"error\":\"$API_KEY $PAYLOAD_CANARY\"}"
    assert_nonblocking "$hook_name-$code"
    assert_outcome "$hook_name-$code" "$label"
    assert_no_success "$hook_name-$code"
    assert_no_canaries "$hook_name-$code"
  done
done

# ORACLE-PLUGIN-002: explicit success and bounded failure classification for both hooks.
for hook_name in session subagent; do
  if [[ $hook_name == session ]]; then
    hook=$SESSION_HOOK
    input=$session_input
    valid_contract=(CORTEX_FIXTURE_BODY='{"status":"ended"}')
  else
    hook=$SUBAGENT_HOOK
    input=$subagent_input
    valid_contract=(CORTEX_FIXTURE_ECHO=observation)
  fi

  run_hook "$hook" "$input" "$hook_name-2xx" "${valid_contract[@]}"
  assert_nonblocking "$hook_name-2xx"
  successes=$(combined_output "$hook_name-2xx" | grep -Eic 'outcome[\"'"'"': =]+success|delivery[[:space:]_-]*success' || true)
  [[ $successes == 1 ]] && pass "$hook_name-2xx reports exactly one success" \
    || fail "$hook_name-2xx must report exactly one success"

  for row in '409 conflict http' '500 unavailable http' 'timeout timeout timeout' 'transport unavailable transport' 'invalid invalid_response http'; do
    read -r case_name expected mode <<<"$row"
    body='{"id":1,"status":"created"}'
    status=200
    [[ $case_name == 409 ]] && status=409
    [[ $case_name == 500 ]] && status=500
    [[ $case_name == invalid ]] && body='not-json'
    run_hook "$hook" "$input" "$hook_name-$case_name" \
      CORTEX_FIXTURE_MODE="$mode" CORTEX_FIXTURE_STATUS="$status" CORTEX_FIXTURE_BODY="$body"
    assert_nonblocking "$hook_name-$case_name"
    assert_outcome "$hook_name-$case_name" "$expected"
    assert_no_success "$hook_name-$case_name"
    assert_no_canaries "$hook_name-$case_name"
  done
done

# ORACLE-PLUGIN-004: a 2xx body is success only when it is the exact contract
# body for the endpoint; empty, cross-endpoint, extra-key, and tampered
# identity bodies must stay invalid_response.
contract_case=0
for body in '{}' '{"status":"created"}' '{"status":"running"}' '{"status":"ended","extra":true}'; do
  contract_case=$((contract_case + 1))
  name="session-contract-$contract_case"
  run_hook "$SESSION_HOOK" "$session_input" "$name" \
    CORTEX_FIXTURE_STATUS=200 CORTEX_FIXTURE_BODY="$body"
  assert_nonblocking "$name"
  assert_outcome "$name" invalid_response
  assert_no_success "$name"
  assert_no_canaries "$name"
done

for row in 'empty {}' 'cross-endpoint {"status":"ended"}'; do
  read -r case_name body <<<"$row"
  name="subagent-contract-$case_name"
  run_hook "$SUBAGENT_HOOK" "$subagent_input" "$name" \
    CORTEX_FIXTURE_STATUS=200 CORTEX_FIXTURE_BODY="$body"
  assert_nonblocking "$name"
  assert_outcome "$name" invalid_response
  assert_no_success "$name"
  assert_no_canaries "$name"
done

for tamper in session_id content title project id; do
  name="subagent-contract-tamper-$tamper"
  run_hook "$SUBAGENT_HOOK" "$subagent_input" "$name" \
    CORTEX_FIXTURE_ECHO=observation CORTEX_FIXTURE_TAMPER="$tamper"
  assert_nonblocking "$name"
  assert_outcome "$name" invalid_response
  assert_no_success "$name"
  assert_no_canaries "$name"
done

# ORACLE-PLUGIN-005: plaintext CORTEX_URL targets must be loopback, validated
# before any credential is read; https targets stay deliverable.
for hook_name in session subagent; do
  if [[ $hook_name == session ]]; then
    hook=$SESSION_HOOK
    input=$session_input
    valid_contract=(CORTEX_FIXTURE_BODY='{"status":"ended"}')
  else
    hook=$SUBAGENT_HOOK
    input=$subagent_input
    valid_contract=(CORTEX_FIXTURE_ECHO=observation)
  fi

  for row in 'remote http://10.0.0.5:7438' 'lookalike http://127.0.0.1.evil.com:7438' 'empty-host http://:7438' 'over-255-high http://127.999.999.999:7438' 'over-255 http://127.256.0.1:7438' 'missing-octet http://127.0.0:7438' 'short-form http://127.1:7438' 'leading-zero http://127.0.0.001:7438'; do
    read -r case_name url <<<"$row"
    name="$hook_name-url-$case_name"
    run_hook "$hook" "$input" "$name" CORTEX_URL="$url"
    assert_nonblocking "$name"
    assert_outcome "$name" config
    assert_no_success "$name"
    [[ ! -s "$TMP_ROOT/$name/request" ]] \
      && pass "$name sends no request" \
      || fail "$name must not send any request or read the credential"
  done

  run_hook "$hook" "$input" "$hook_name-url-https" \
    CORTEX_URL=https://cortex.internal:8443 "${valid_contract[@]}"
  assert_nonblocking "$hook_name-url-https"
  assert_outcome "$hook_name-url-https" success
  if grep -F "https://cortex.internal:8443" "$TMP_ROOT/$hook_name-url-https/request" >/dev/null; then
    pass "$hook_name-url-https delivers over https"
  else
    fail "$hook_name-url-https must use the configured https URL"
  fi

  run_hook "$hook" "$input" "$hook_name-url-localhost" \
    CORTEX_URL=http://localhost:7438 "${valid_contract[@]}"
  assert_nonblocking "$hook_name-url-localhost"
  assert_outcome "$hook_name-url-localhost" success

  run_hook "$hook" "$input" "$hook_name-url-ipv4-loopback" \
    CORTEX_URL=http://127.0.0.1:7438 "${valid_contract[@]}"
  assert_nonblocking "$hook_name-url-ipv4-loopback"
  assert_outcome "$hook_name-url-ipv4-loopback" success

  run_hook "$hook" "$input" "$hook_name-url-ipv4-loopback-max" \
    CORTEX_URL=http://127.255.255.255:7438 "${valid_contract[@]}"
  assert_nonblocking "$hook_name-url-ipv4-loopback-max"
  assert_outcome "$hook_name-url-ipv4-loopback-max" success

  run_hook "$hook" "$input" "$hook_name-url-ipv6-loopback" \
    CORTEX_URL='http://[::1]:7438' "${valid_contract[@]}"
  assert_nonblocking "$hook_name-url-ipv6-loopback"
  assert_outcome "$hook_name-url-ipv6-loopback" success
done

# ORACLE-PLUGIN-003: passive capture is byte-bounded before JSON and remains valid UTF-8.
check_metrics() {
  local name=$1 original_file=$2
  python3 - "$TMP_ROOT/$name/request.body" "$original_file" <<'PY'
import json, pathlib, sys

request = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
original = pathlib.Path(sys.argv[2]).read_text(encoding="utf-8")

def candidates(value):
    if isinstance(value, dict):
        if {"content", "truncated", "original_bytes", "stored_bytes"} <= value.keys():
            yield value
        for child in value.values():
            yield from candidates(child)
    elif isinstance(value, list):
        for child in value:
            yield from candidates(child)

record = next(candidates(request), None)
if record is None:
    raise SystemExit("missing truncation metadata")
stored = record["content"]
stored.encode("utf-8")
assert record["truncated"] is True
assert record["original_bytes"] == len(original.encode("utf-8"))
assert record["stored_bytes"] == len(stored.encode("utf-8"))
assert record["stored_bytes"] < record["original_bytes"]
assert original.startswith(stored), "content is not truncated at a complete character boundary"
assert "\ufffd" not in stored
PY
}

assert_received_corpus() {
  local name=$1 original_file=$2 corpus=$3
  python3 - "$TMP_ROOT/$name/received-input" "$original_file" "$corpus" <<'PY'
import json, pathlib, sys

received = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
expected = pathlib.Path(sys.argv[2]).read_text(encoding="utf-8")
corpus = sys.argv[3]
actual = received.get("stdout")
assert actual == expected, "hook stdin does not contain the intended corpus"
assert len(actual) == len(expected) == 100000, "unexpected corpus rune scale"
expected_bytes = 250000 if corpus == "unicode" else 100000
assert len(actual.encode("utf-8")) == len(expected.encode("utf-8")) == expected_bytes, \
    "unexpected corpus byte scale"
if corpus == "unicode":
    assert "é🙂界" in actual, "Unicode marker missing from hook stdin"
PY
}

ascii_file="$TMP_ROOT/ascii.txt"
unicode_file="$TMP_ROOT/unicode.txt"
python3 - "$ascii_file" "$unicode_file" <<'PY'
import pathlib, sys
pathlib.Path(sys.argv[1]).write_text("A" * 100000, encoding="utf-8")
pathlib.Path(sys.argv[2]).write_text("Aé🙂界" * 25000, encoding="utf-8")
PY
[[ -s "$ascii_file" && -s "$unicode_file" ]] \
  || fixture_fail "corpus files are missing or empty"

for corpus in ascii unicode; do
  file="$TMP_ROOT/$corpus.txt"
  input_file="$TMP_ROOT/$corpus-input.json"
  if ! jq -cn --rawfile content "$file" \
    '{session_id:"session-T23",cwd:"/tmp",stdout:$content}' >"$input_file"; then
    fixture_fail "$corpus JSON encoding"
  fi
  if ! jq -e --rawfile expected "$file" \
    '.stdout == $expected' "$input_file" >/dev/null; then
    fixture_fail "$corpus JSON validation"
  fi
  run_hook_file "$SUBAGENT_HOOK" "$input_file" "subagent-$corpus" CORTEX_FIXTURE_ECHO=observation
  assert_nonblocking "subagent-$corpus"
  if assert_received_corpus "subagent-$corpus" "$file" "$corpus" \
    2>"$TMP_ROOT/subagent-$corpus/received-error"; then
    pass "subagent-$corpus hook receives intended byte/rune corpus"
  else
    fixture_fail "subagent-$corpus hook-input proof"
  fi
  if check_metrics "subagent-$corpus" "$file" 2>"$TMP_ROOT/subagent-$corpus/metrics-error"; then
    pass "subagent-$corpus has valid UTF-8 byte metrics"
  else
    fail "subagent-$corpus must truncate before JSON with consistent byte metrics"
  fi
  assert_outcome "subagent-$corpus" success
  assert_no_canaries "subagent-$corpus"
done

printf '1..%d\n' "$((PASS + FAIL))"
printf '# pass=%d fail=%d\n' "$PASS" "$FAIL"
((FAIL == 0))
