package project

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func gitAvailable() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	return dir
}

func TestDetectProjectFromGitRemote(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not available")
	}
	dir := initGitRepo(t)
	runGit(t, dir, "remote", "add", "origin", "https://github.com/acme/MyRepo.git")

	if got := detectFromGitRemote(dir); got != "MyRepo" {
		t.Fatalf("detectFromGitRemote(%q) = %q, want %q", dir, got, "MyRepo")
	}
	if got := DetectProject(dir); got != "myrepo" {
		t.Fatalf("DetectProject(%q) = %q, want %q", dir, got, "myrepo")
	}
}

func TestDetectProjectFromGitRoot(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not available")
	}
	dir := initGitRepo(t)
	base := filepath.Base(dir)
	if got := detectFromGitRoot(dir); got != base {
		t.Fatalf("detectFromGitRoot(%q) = %q, want %q", dir, got, base)
	}
	if got := DetectProject(dir); got != normalize(base) {
		t.Fatalf("DetectProject(%q) = %q, want %q", dir, got, normalize(base))
	}
}

func TestDetectProjectDotBaseFallsBackToUnknown(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-a-repository", "missing")
	dotPath := missing + string(filepath.Separator) + "."
	if got := DetectProject(dotPath); got != "unknown" {
		t.Fatalf("DetectProject(%q) = %q, want %q", dotPath, got, "unknown")
	}
}

func TestFindSimilarNegativeMaxDistanceClampsToZero(t *testing.T) {
	matches := FindSimilar("cortex", []string{"cortax"}, -3)
	if len(matches) != 0 {
		t.Fatalf("negative maxDistance must clamp to 0, got %+v", matches)
	}
}

func TestFindSimilarSingleRuneNameUsesMinimumDistance(t *testing.T) {
	matches := FindSimilar("a", []string{"b"}, 3)
	found := false
	for _, m := range matches {
		if m.Name == "b" && m.MatchType == "levenshtein" && m.Distance == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("single-rune name should match at distance 1, got %+v", matches)
	}
}

func TestFindSimilarSortsLevenshteinByDistance(t *testing.T) {
	matches := FindSimilar("abcdef", []string{"abxyef", "abcxef"}, 3)
	if len(matches) != 2 {
		t.Fatalf("expected 2 levenshtein matches, got %+v", matches)
	}
	if matches[0].Name != "abcxef" || matches[0].Distance != 1 {
		t.Fatalf("first match = %+v, want abcxef at distance 1", matches[0])
	}
	if matches[1].Name != "abxyef" || matches[1].Distance != 2 {
		t.Fatalf("second match = %+v, want abxyef at distance 2", matches[1])
	}
}
