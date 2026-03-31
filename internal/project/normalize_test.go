package project

import "testing"

func TestNormalizeProject(t *testing.T) {
	tests := []struct {
		input       string
		wantNorm    string
		wantWarning bool
	}{
		{"cortex", "cortex", false},
		{"Cortex", "cortex", true},
		{"  CORTEX  ", "cortex", true},
		{"my--project", "my-project", true},
		{"my__project", "my_project", true},
		{"already-clean", "already-clean", false},
		{"", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			norm, warn := NormalizeProject(tt.input)
			if norm != tt.wantNorm {
				t.Errorf("NormalizeProject(%q) normalized = %q, want %q", tt.input, norm, tt.wantNorm)
			}
			if (warn != "") != tt.wantWarning {
				t.Errorf("NormalizeProject(%q) warning = %q, wantWarning = %v", tt.input, warn, tt.wantWarning)
			}
		})
	}
}
