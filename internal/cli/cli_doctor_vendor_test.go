package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunDoctor_VendorKeysNoticeWhenCortexKeysMissing(t *testing.T) {
	setCLIEnv(t)
	t.Setenv("OPENAI_API_KEY", "sk-mock-openai-key")
	t.Setenv("ANTHROPIC_API_KEY", "sk-mock-anthropic-key")
	t.Setenv("CORTEX_LLM_API_KEY", "")
	t.Setenv("CORTEX_EMBEDDING_API_KEY", "")

	out, errB := &bytes.Buffer{}, &bytes.Buffer{}
	code := Run([]string{"cortex", "doctor"}, out, errB)
	if code != 0 {
		t.Fatalf("doctor failed with code %d, stderr: %s", code, errB.String())
	}

	stdout := out.String()
	wantNotice := "[INFO] Detected vendor environment variable(s) (OPENAI_API_KEY, ANTHROPIC_API_KEY). Cortex deliberately uses CORTEX_LLM_API_KEY and CORTEX_EMBEDDING_API_KEY"
	if !strings.Contains(stdout, wantNotice) {
		t.Fatalf("expected doctor stdout to contain %q, got:\n%s", wantNotice, stdout)
	}
}

func TestRunDoctor_NoVendorNoticeWhenCortexKeysPresent(t *testing.T) {
	setCLIEnv(t)
	t.Setenv("OPENAI_API_KEY", "sk-mock-openai-key")
	t.Setenv("ANTHROPIC_API_KEY", "sk-mock-anthropic-key")
	t.Setenv("CORTEX_LLM_API_KEY", "ctx-llm-key")
	t.Setenv("CORTEX_EMBEDDING_API_KEY", "ctx-emb-key")

	out, errB := &bytes.Buffer{}, &bytes.Buffer{}
	code := Run([]string{"cortex", "doctor"}, out, errB)
	if code != 0 {
		t.Fatalf("doctor failed with code %d, stderr: %s", code, errB.String())
	}

	stdout := out.String()
	if strings.Contains(stdout, "Detected vendor environment variable(s)") {
		t.Fatalf("expected no vendor notice when Cortex keys are set, but got:\n%s", stdout)
	}
}

func TestRunDoctor_SingleVendorKeyNotice(t *testing.T) {
	setCLIEnv(t)
	t.Setenv("OPENAI_API_KEY", "sk-mock-openai-key")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("CORTEX_LLM_API_KEY", "")
	t.Setenv("CORTEX_EMBEDDING_API_KEY", "")

	out, errB := &bytes.Buffer{}, &bytes.Buffer{}
	code := Run([]string{"cortex", "doctor"}, out, errB)
	if code != 0 {
		t.Fatalf("doctor failed with code %d, stderr: %s", code, errB.String())
	}

	stdout := out.String()
	wantNotice := "[INFO] Detected vendor environment variable(s) (OPENAI_API_KEY). Cortex deliberately uses CORTEX_LLM_API_KEY and CORTEX_EMBEDDING_API_KEY"
	if !strings.Contains(stdout, wantNotice) {
		t.Fatalf("expected doctor stdout to contain %q, got:\n%s", wantNotice, stdout)
	}
}
