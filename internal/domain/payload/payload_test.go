package payload

import (
	"strings"
	"testing"
)

func TestShouldExternalize(t *testing.T) {
	if ShouldExternalize(50 * 1024) {
		t.Error("50KB should not be externalized")
	}
	if !ShouldExternalize(100 * 1024) {
		t.Error("100KB should be externalized")
	}
	if !ShouldExternalize(250 * 1024) {
		t.Error("250KB should be externalized")
	}
	if !ShouldExternalize(10, 5) {
		t.Error("10 bytes should be externalized when custom threshold is 5")
	}
}

func TestNewTransientPayload(t *testing.T) {
	var sb strings.Builder
	for i := 1; i <= 100; i++ {
		sb.WriteString("Log line entry data for testing\n")
	}
	content := sb.String()

	p := NewTransientPayload("sess-1", "my-project", "cortex_execute", content)

	if !strings.HasPrefix(p.ID, "payload-") {
		t.Errorf("Unexpected ID format: %s", p.ID)
	}
	if p.ByteCount != len(content) {
		t.Errorf("ByteCount = %d, want %d", p.ByteCount, len(content))
	}
	if p.TokensSaved <= 0 {
		t.Errorf("TokensSaved = %d, expected > 0", p.TokensSaved)
	}
	if !strings.Contains(p.Snippet, "externalized to transient store") {
		t.Errorf("Snippet missing truncation note: %s", p.Snippet)
	}
}
