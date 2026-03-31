package project

import "testing"

func TestExtractRepoName(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{"SSH with .git", "git@github.com:user/repo.git", "repo"},
		{"SSH without .git", "git@github.com:user/repo", "repo"},
		{"HTTPS with .git", "https://github.com/user/repo.git", "repo"},
		{"HTTPS without .git", "https://github.com/user/repo", "repo"},
		{"nested path", "https://gitlab.com/group/subgroup/repo.git", "repo"},
		{"empty", "", ""},
		{"just name", "repo", "repo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractRepoName(tt.url)
			if got != tt.want {
				t.Errorf("extractRepoName(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestNormalize(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"MyProject", "myproject"},
		{"  spaced  ", "spaced"},
		{"", "unknown"},
		{"UPPER", "upper"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := normalize(tt.input)
			if got != tt.want {
				t.Errorf("normalize(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestDetectProject_EmptyDir(t *testing.T) {
	if got := DetectProject(""); got != "unknown" {
		t.Errorf("DetectProject('') = %q, want 'unknown'", got)
	}
}

func TestDetectProject_DashGuard(t *testing.T) {
	// Should not panic or error with dash-prefixed dir
	got := DetectProject("-dangerous")
	if got == "" {
		t.Error("DetectProject('-dangerous') should not return empty")
	}
}

func TestDetectProject_PlainDir(t *testing.T) {
	got := DetectProject("/home/user/MyProject")
	if got != "myproject" {
		t.Errorf("DetectProject('/home/user/MyProject') = %q, want 'myproject'", got)
	}
}
