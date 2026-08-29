//go:build docker_e2e

package e2e_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const composeProjectPrefix = "cortexe2e"

func TestDockerComposeBootstrapAuthenticationSearchAndUI(t *testing.T) {
	if testing.Short() {
		t.Skip("Docker composition E2E is disabled with -short")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is not available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	stack := newComposeStack(t, ctx)
	t.Cleanup(stack.down)

	stack.run("up", "--build", "--detach", "--wait")
	stack.runInServer(`
set -eu
status=$(curl -sS -o /dev/null -w "%{http_code}" http://127.0.0.1:7438/api/me)
test "$status" = 401
test "$(stat -c %a /home/cortex/.cortex/server-bootstrap.env)" = 600

set -a
. /home/cortex/.cortex/server-bootstrap.env
set +a
status=$(curl -sS -o /dev/null -w "%{http_code}" -H "Authorization: Bearer $CORTEX_HTTP_TOKEN" http://127.0.0.1:7438/api/me)
test "$status" = 200
status=$(curl -sS -o /dev/null -w "%{http_code}" -H "Authorization: Bearer $CORTEX_HTTP_TOKEN" -H "X-Cortex-Workspace: 00000000-0000-0000-0000-000000000000" http://127.0.0.1:7438/api/me)
test "$status" = 403

session=$(curl -fsS -H "Authorization: Bearer $CORTEX_HTTP_TOKEN" -H "Content-Type: application/json" -d '{"project":"e2e-search","directory":"/workspace/e2e-search"}' http://127.0.0.1:7438/api/sessions)
session_id=$(printf '%s' "$session" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
test -n "$session_id"
curl -fsS -o /dev/null -H "Authorization: Bearer $CORTEX_HTTP_TOKEN" -H "Content-Type: application/json" -d "{\"session_id\":\"$session_id\",\"project\":\"e2e-search\",\"scope\":\"project\",\"source\":\"manual\",\"type\":\"manual\",\"title\":\"ApplicationDbContext.cs E2E\",\"content\":\"ApplicationDbContext.cs validates tenant workspace search indexing.\"}" http://127.0.0.1:7438/api/observations
result=$(curl -fsS -H "Authorization: Bearer $CORTEX_HTTP_TOKEN" 'http://127.0.0.1:7438/api/search?q=ApplicationDbContext.cs')
printf '%s' "$result" | grep -q 'ApplicationDbContext.cs E2E'
echo SERVER_E2E_OK
`)
	stack.run("exec", "-T", "cortex-ui", "wget", "-q", "--spider", "http://127.0.0.1:3000")

	stack.run("restart", "cortex-server")
	stack.waitForServerHealth()
	stack.runInServer(`
set -eu
set -a
. /home/cortex/.cortex/server-bootstrap.env
set +a
status=$(curl -sS -o /dev/null -w "%{http_code}" -H "Authorization: Bearer $CORTEX_HTTP_TOKEN" http://127.0.0.1:7438/api/me)
test "$status" = 200
echo BOOTSTRAP_RESTART_OK
`)
}

type composeStack struct {
	t       *testing.T
	ctx     context.Context
	root    string
	project string
	env     []string
}

func newComposeStack(t *testing.T, ctx context.Context) *composeStack {
	t.Helper()
	root, err := filepath.Abs(filepath.Join(".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	return &composeStack{
		t:       t,
		ctx:     ctx,
		root:    root,
		project: fmt.Sprintf("%s%d", composeProjectPrefix, time.Now().UnixNano()),
		env:     cleanBootstrapEnvironment(os.Environ()),
	}
}

func (s *composeStack) run(args ...string) string {
	s.t.Helper()
	commandArgs := append([]string{"compose", "-p", s.project, "-f", "docker-compose.yml", "-f", "e2e/docker-compose.e2e.yml"}, args...)
	cmd := exec.CommandContext(s.ctx, "docker", commandArgs...)
	cmd.Dir = s.root
	cmd.Env = s.env
	output, err := cmd.CombinedOutput()
	if err != nil {
		s.t.Fatalf("docker compose %s: %v\n%s", strings.Join(args, " "), err, sanitizeDockerOutput(string(output)))
	}
	return string(output)
}

func (s *composeStack) runInServer(script string) {
	s.t.Helper()
	s.run("exec", "-T", "cortex-server", "sh", "-ec", script)
}

func (s *composeStack) waitForServerHealth() {
	s.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		cmd := exec.CommandContext(s.ctx, "docker", "compose", "-p", s.project, "-f", "docker-compose.yml", "-f", "e2e/docker-compose.e2e.yml", "exec", "-T", "cortex-server", "sh", "-ec", `test "$(curl -sS -o /dev/null -w "%{http_code}" http://127.0.0.1:7438/health)" = 200`)
		cmd.Dir = s.root
		cmd.Env = s.env
		if err := cmd.Run(); err == nil {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	s.t.Fatal("cortex-server did not become healthy after restart")
}

func (s *composeStack) down() {
	cmd := exec.Command("docker", "compose", "-p", s.project, "-f", "docker-compose.yml", "-f", "e2e/docker-compose.e2e.yml", "down", "--volumes", "--remove-orphans")
	cmd.Dir = s.root
	cmd.Env = s.env
	if output, err := cmd.CombinedOutput(); err != nil {
		s.t.Logf("E2E cleanup failed: %v\n%s", err, sanitizeDockerOutput(string(output)))
	}
}

func cleanBootstrapEnvironment(base []string) []string {
	keys := map[string]struct{}{
		"CORTEX_SERVER_TENANT_ID":         {},
		"CORTEX_SERVER_WORKSPACE_ID":      {},
		"CORTEX_SERVER_PRINCIPAL_SUBJECT": {},
		"CORTEX_HTTP_TOKEN":               {},
	}
	result := make([]string, 0, len(base)+len(keys))
	for _, entry := range base {
		name, _, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		if _, override := keys[name]; !override {
			result = append(result, entry)
		}
	}
	for key := range keys {
		result = append(result, key+"=")
	}
	return result
}

func sanitizeDockerOutput(output string) string {
	lines := strings.Split(output, "\n")
	safe := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.Contains(line, "tenant_owner_bearer=") || strings.Contains(line, "CORTEX_HTTP_TOKEN=") || strings.Contains(line, "Authorization: Bearer") {
			continue
		}
		safe = append(safe, line)
	}
	return strings.Join(safe, "\n")
}
