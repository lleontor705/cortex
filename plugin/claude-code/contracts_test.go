package claudecode

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestHooksUseOfficialHTTPPort(t *testing.T) {
	for _, name := range []string{"session-start.sh", "post-compaction.sh", "user-prompt-submit.sh", "subagent-stop.sh", "session-stop.sh"} {
		data, err := os.ReadFile(filepath.Join("scripts", name))
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		if !strings.Contains(text, `CORTEX_HTTP_PORT="${CORTEX_HTTP_PORT:-7438}"`) || !strings.Contains(text, `${CORTEX_HTTP_PORT}`) {
			t.Errorf("%s does not use CORTEX_HTTP_PORT with default 7438", name)
		}
		if strings.Contains(text, "CORTEX_PORT") {
			t.Errorf("%s still references obsolete CORTEX_PORT", name)
		}
	}
}

// TestDeliveryHooksAlwaysReturnControl pins the packaging invariant that the
// delivery and context hooks end with an unconditional exit 0: a Cortex hook
// failure is observable through its classification but must never block the
// host.
func TestDeliveryHooksAlwaysReturnControl(t *testing.T) {
	for _, name := range []string{
		"session-stop.sh", "subagent-stop.sh", "session-start.sh", "user-prompt-submit.sh",
	} {
		data, err := os.ReadFile(filepath.Join("scripts", name))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(strings.TrimSpace(string(data)), "exit 0") {
			t.Errorf("%s must end with an unconditional exit 0 so it never blocks the host", name)
		}
	}
}

// TestSubagentStopUsesPackagedUTF8Truncator ensures passive captures are
// byte-bounded by whole runes before JSON with auditable byte metadata.
func TestSubagentStopUsesPackagedUTF8Truncator(t *testing.T) {
	truncator, err := os.ReadFile(filepath.Join("scripts", "utf8-truncate.jq"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"utf8bytelength", "truncated", "original_bytes", "stored_bytes"} {
		if !strings.Contains(string(truncator), want) {
			t.Errorf("utf8-truncate.jq missing %q", want)
		}
	}
	subagent, err := os.ReadFile(filepath.Join("scripts", "subagent-stop.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(subagent), "utf8-truncate.jq") {
		t.Error("subagent-stop.sh must truncate passive captures via utf8-truncate.jq")
	}
}

// TestHooksContractHarnessDeclaresDependencies keeps the Bash gate honest:
// the harness must probe its external tools and bail out loudly when the
// workstation cannot run it (BLOCKED), never silently pass.
func TestHooksContractHarnessDeclaresDependencies(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("scripts", "hooks_test.sh"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"require jq", "require python3", "require timeout"} {
		if !strings.Contains(text, want) {
			t.Errorf("hooks_test.sh missing %q dependency probe", want)
		}
	}
}

// TestSessionStopAcceptsOnlyEndedStatus pins the SessionStop delivery
// contract: POST /api/sessions/{id}/end returns exactly {"status":"ended"},
// so a 2xx body is success only when it is that single-key object. Any other
// 2xx body (empty object, another endpoint's body, extra keys) must classify
// as invalid_response instead of a false success.
func TestSessionStopAcceptsOnlyEndedStatus(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("scripts", "session-stop.sh"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{`.status == "ended"`, `keys | length`} {
		if !strings.Contains(text, want) {
			t.Errorf("session-stop.sh must validate the ended-status contract, missing %q", want)
		}
	}
}

// TestSubagentStopValidatesPersistedObservation pins the SubagentStop
// delivery contract: a 2xx response is success only when it is the persisted
// Observation for the exact session, content, and sent classification fields.
// Additive server fields (e.g. tags) must be tolerated, so the validation is
// field-identity based and must not gate on an exact key count.
func TestSubagentStopValidatesPersistedObservation(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("scripts", "subagent-stop.sh"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		`--arg sid`, `--arg content`, `--arg title`, `--arg project`,
		`.session_id == $sid`, `.content == $content`,
		`.title == $title`, `.project == $project`,
		`(.id | type == "number")`, `.id > 0`,
		`.type == "passive"`, `.scope == "project"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("subagent-stop.sh must validate persisted observation identity, missing %q", want)
		}
	}
	if strings.Contains(text, "keys | length") {
		t.Error("subagent-stop.sh must not gate on an exact key count; additive fields like tags are allowed")
	}
}

// TestHooksRejectPlaintextNonLoopbackURLBeforeCredential pins REM-TRANSPORT-001:
// hooks must refuse an http:// CORTEX_URL whose host is not loopback before
// any credential is read, so a misconfigured plaintext target can never
// receive the bearer token. https:// targets are always allowed.
func TestHooksRejectPlaintextNonLoopbackURLBeforeCredential(t *testing.T) {
	for _, name := range []string{"session-stop.sh", "subagent-stop.sh"} {
		data, err := os.ReadFile(filepath.Join("scripts", name))
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		call := strings.Index(text, "validate_url ||")
		credential := strings.Index(text, "token=$(credential)")
		if call < 0 {
			t.Errorf("%s must call validate_url before reading the credential", name)
			continue
		}
		if credential < 0 || call > credential {
			t.Errorf("%s must validate CORTEX_URL before the credential is read", name)
		}
		for _, want := range []string{"https", "127.", "localhost", "::1"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s loopback/https validation missing %q", name, want)
			}
		}
	}
}

// TestHooksValidateStrictLoopbackOctets pins the REM-TRANSPORT-001 127/8
// acceptance to strict decimal dotted quads: each trailing octet must be
// 0-255 with no leading zeros, so syntactically invalid numeric hosts
// (127.999.999.999, 127.256.0.1, 127.1, 127.0.0.001) are rejected before
// any credential is read, while 127.0.0.1 and 127.255.255.255 stay
// deliverable. The ERE is extracted verbatim from each hook and executed
// here because bash ERE and Go's RE2 share this syntax.
func TestHooksValidateStrictLoopbackOctets(t *testing.T) {
	extract := regexp.MustCompile(`\^127\\\.\(.*\)\$`)
	for _, name := range []string{"session-stop.sh", "subagent-stop.sh"} {
		data, err := os.ReadFile(filepath.Join("scripts", name))
		if err != nil {
			t.Fatal(err)
		}
		literal := extract.FindString(string(data))
		if literal == "" {
			t.Errorf("%s must pin a strict bounded-octet 127/8 regex (25[0-5] alternation, no leading zeros)", name)
			continue
		}
		re, err := regexp.Compile(literal)
		if err != nil {
			t.Fatalf("%s loopback regex does not compile: %v", name, err)
		}
		for _, host := range []string{"127.0.0.1", "127.255.255.255"} {
			if !re.MatchString(host) {
				t.Errorf("%s must accept loopback %s", name, host)
			}
		}
		for _, host := range []string{
			"127.999.999.999", "127.256.0.1", "127.0.0", "127.1",
			"127.0.0.001", "127.0.0.1.evil.com",
		} {
			if re.MatchString(host) {
				t.Errorf("%s must reject numeric host %s", name, host)
			}
		}
	}
}

// TestSharedHelpersProvideAuthPrimitives pins SEC-04/REM-TRANSPORT-001
// plumbing: _helpers.sh must publish the strict loopback/https URL policy,
// the credential precedence, and the shared bounded classification signal
// used by the SessionStart and UserPromptSubmit context hooks.
func TestSharedHelpersProvideAuthPrimitives(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("scripts", "_helpers.sh"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"validate_url()", "credential()",
		"cortex_signal()", "cortex_classify()",
		"CORTEX_API_KEY", "CORTEX_HTTP_TOKEN", "CORTEX_CONFIG_FILE",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("_helpers.sh missing %q", want)
		}
	}
	extract := regexp.MustCompile(`\^127\\\.\(.*\)\$`)
	literal := extract.FindString(text)
	if literal == "" {
		t.Fatal("_helpers.sh must pin a strict bounded-octet 127/8 regex")
	}
	re, err := regexp.Compile(literal)
	if err != nil {
		t.Fatalf("_helpers.sh loopback regex does not compile: %v", err)
	}
	for _, host := range []string{"127.0.0.1", "127.255.255.255"} {
		if !re.MatchString(host) {
			t.Errorf("_helpers.sh must accept loopback %s", host)
		}
	}
	for _, host := range []string{"127.999.999.999", "127.256.0.1", "127.0.0", "127.1", "127.0.0.001"} {
		if re.MatchString(host) {
			t.Errorf("_helpers.sh must reject numeric host %s", host)
		}
	}
}

// TestSessionStartAuthenticatesAndConfirmsSession pins SEC-04 for the
// SessionStart hook: the URL policy and credential are resolved before any
// network activity, the server spawn happens only with a credential, the
// session create and every protected read carry the bearer token, the
// session is confirmed only by an exact persisted-identity echo or a 409
// proven through the read endpoint, and injected memory context comes only
// from a validated authenticated search response.
func TestSessionStartAuthenticatesAndConfirmsSession(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("scripts", "session-start.sh"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)

	validate := strings.Index(text, "validate_url ||")
	credential := strings.Index(text, "token=$(credential)")
	protected := strings.Index(text, `"${CORTEX_URL}/api/sessions"`)
	spawn := strings.Index(text, "cortex serve")
	if validate < 0 || credential < 0 || protected < 0 {
		t.Fatal("session-start.sh must validate the URL, read the credential, then call protected endpoints")
	}
	if validate > credential || credential > protected {
		t.Error("session-start.sh must validate CORTEX_URL, then read the credential, before any protected call")
	}
	if spawn >= 0 && credential > spawn {
		t.Error("session-start.sh may spawn cortex serve only after a credential is configured")
	}

	if got := strings.Count(text, `-H "Authorization: Bearer $token"`); got < 3 {
		t.Errorf("session-start.sh must authenticate session create, conflict read-back, and search (found %d bearer headers)", got)
	}

	for _, want := range []string{
		`.id == $sid`, `.project == $project`, `.directory == $dir`,
		`(.started_at | type == "string")`,
		`conflict)`, `/api/sessions/${encoded_session}`,
		`all(.[]; type == "object" and (.title | type == "string"))`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("session-start.sh missing %q", want)
		}
	}
}

// TestUserPromptSubmitAuthenticatesProtectedReads pins SEC-04 for the
// UserPromptSubmit hook: the nudge logic's protected reads carry the bearer
// credential only after URL validation, and responses are validated before
// any nudge decision; the static first-message path stays offline.
func TestUserPromptSubmitAuthenticatesProtectedReads(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("scripts", "user-prompt-submit.sh"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)

	validate := strings.Index(text, "validate_url ||")
	credential := strings.Index(text, "token=$(credential)")
	protected := strings.Index(text, `"${CORTEX_URL}/api/`)
	if validate < 0 || credential < 0 || protected < 0 {
		t.Fatal("user-prompt-submit.sh must validate the URL, read the credential, then read protected endpoints")
	}
	if validate > credential || credential > protected {
		t.Error("user-prompt-submit.sh must validate CORTEX_URL, then read the credential, before any protected read")
	}

	if got := strings.Count(text, `-H "Authorization: Bearer $token"`); got < 2 {
		t.Errorf("user-prompt-submit.sh must authenticate both protected reads (found %d bearer headers)", got)
	}

	for _, want := range []string{
		`.started_at | type == "string"`,
		`.[0].created_at | type == "string"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("user-prompt-submit.sh missing response validation %q", want)
		}
	}
}
