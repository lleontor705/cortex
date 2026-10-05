//go:build !cortex_vectors

// Build-tag detector for CLI tests: indicates whether the cortex_vectors
// build tag is active. Default (stub) build -> false.
package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testVectorsEnabled = false

func TestDefaultBuildTargetEnablesCortexVectors(t *testing.T) {
	makefile, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	recipe, found := makefileTargetRecipe(string(makefile), "build")
	if !found {
		t.Fatal("Makefile has no build target")
	}
	if !strings.Contains(recipe, "-tags cortex_vectors") {
		t.Fatalf("build target must enable -tags cortex_vectors, got recipe:\n%s", recipe)
	}
}

func TestDoctorDegradedVectorNamesRemediation(t *testing.T) {
	setCLIEnv(t)
	code, out, errB := run(t, "cortex", "doctor")
	if code != 0 {
		t.Fatalf("doctor code = %d, stderr = %q", code, errB)
	}
	for _, want := range []string{
		"[WARN] Vector store: disabled",
		"vector index is degraded",
		"remediation: go build -tags cortex_vectors ./cmd/cortex",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("doctor degraded output missing %q in:\n%s", want, out)
		}
	}
}

// makefileTargetRecipe returns the tab-indented recipe lines of target,
// ending at the first line that is not part of that recipe.
func makefileTargetRecipe(content, target string) (string, bool) {
	marker := target + ":"
	var recipe strings.Builder
	found := false
	collecting := false
	for _, line := range strings.Split(content, "\n") {
		switch {
		case !found && strings.HasPrefix(line, marker):
			found, collecting = true, true
		case collecting && strings.HasPrefix(line, "\t"):
			recipe.WriteString(line)
			recipe.WriteString("\n")
		case collecting:
			collecting = false
		}
	}
	return recipe.String(), found
}
