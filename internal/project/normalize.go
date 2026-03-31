package project

import (
	"fmt"
	"strings"
)

// NormalizeProject normalizes a project name: lowercase, trim, collapse consecutive hyphens/underscores.
// Returns the normalized name and a warning if the name was changed.
func NormalizeProject(project string) (normalized string, warning string) {
	if project == "" {
		return "", ""
	}
	n := strings.TrimSpace(strings.ToLower(project))
	for strings.Contains(n, "--") {
		n = strings.ReplaceAll(n, "--", "-")
	}
	for strings.Contains(n, "__") {
		n = strings.ReplaceAll(n, "__", "_")
	}
	if n == project {
		return n, ""
	}
	return n, fmt.Sprintf("Project name normalized: %q -> %q", project, n)
}
