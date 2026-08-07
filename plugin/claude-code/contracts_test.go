package claudecode

import (
	"os"
	"path/filepath"
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
