package opencode

import (
	"strings"
	"testing"
)

func TestEmbeddedPluginContracts(t *testing.T) {
	source := Source()
	for _, want := range []string{"/api/prompts", "session.deleted", "/api/sessions/${encodeURIComponent(sessionId)}/end", "/api/observations"} {
		if !strings.Contains(source, want) {
			t.Errorf("embedded plugin missing %q", want)
		}
	}
	if strings.Count(source, `return "cortex"`) != 1 {
		t.Fatal("binary fallback must appear exactly once")
	}
	if strings.Contains(source, `type: "prompt"`) {
		t.Fatal("plugin still stores user prompts as observations")
	}
}
