package sandbox

import (
	"testing"
)

func TestNormalizeLanguage(t *testing.T) {
	tests := []struct {
		input    string
		expected Language
		wantErr  bool
	}{
		{"python", LanguagePython, false},
		{"Python3", LanguagePython, false},
		{"PY", LanguagePython, false},
		{"node", LanguageNode, false},
		{"javascript", LanguageNode, false},
		{"js", LanguageNode, false},
		{"typescript", LanguageNode, false},
		{"ts", LanguageNode, false},
		{"bun", LanguageBun, false},
		{"go", LanguageGo, false},
		{"powershell", LanguagePowerShell, false},
		{"pwsh", LanguagePowerShell, false},
		{"bash", LanguageBash, false},
		{"sh", LanguageBash, false},
		{"cobol", "", true},
		{"unknown", "", true},
	}

	for _, tt := range tests {
		got, err := NormalizeLanguage(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Errorf("NormalizeLanguage(%q) expected error, got nil", tt.input)
			}
		} else {
			if err != nil {
				t.Errorf("NormalizeLanguage(%q) unexpected error: %v", tt.input, err)
			}
			if got != tt.expected {
				t.Errorf("NormalizeLanguage(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		}
	}
}

func TestDetectRuntimes(t *testing.T) {
	runtimes := DetectRuntimes()
	t.Logf("Detected runtimes on host: %+v", runtimes)
	// We expect at least one runtime on any development machine (go or powershell or python)
	if len(runtimes) == 0 {
		t.Log("Warning: No runtimes detected in PATH")
	}
}
