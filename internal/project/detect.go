package project

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func DetectProject(dir string) string {
	if dir == "" {
		return "unknown"
	}
	if strings.HasPrefix(dir, "-") {
		dir = "./" + dir
	}
	if name := detectFromGitRemote(dir); name != "" {
		return normalize(name)
	}
	if name := detectFromGitRoot(dir); name != "" {
		return normalize(name)
	}
	base := filepath.Base(dir)
	if base == "" || base == "." {
		return "unknown"
	}
	return normalize(base)
}

func normalize(name string) string {
	n := strings.TrimSpace(strings.ToLower(name))
	if n == "" {
		return "unknown"
	}
	return n
}

func detectFromGitRemote(dir string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "remote", "get-url", "origin")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return extractRepoName(strings.TrimSpace(string(out)))
}

func detectFromGitRoot(dir string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return ""
	}
	return filepath.Base(root)
}

func extractRepoName(url string) string {
	url = strings.TrimSuffix(url, ".git")
	parts := strings.FieldsFunc(url, func(r rune) bool {
		return r == '/' || r == ':'
	})
	if len(parts) == 0 {
		return ""
	}
	return strings.TrimSpace(parts[len(parts)-1])
}
