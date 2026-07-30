// Defect pin for W8.1: ensures the sqlite package (source AND test files)
// compiles under both the default (zero-CGO stub) and cortex_vectors build
// tags. This was broken when the dimension constants were extracted to
// vector_constants.go but the cortex_vectors-gated test file still
// referenced the old unexported EmbeddingDimension symbol.
//
// Regression scenario: a tagged test file references a symbol that only
// exists under one build configuration. Default builds pass; cortex_vectors
// builds fail at compile time. This pin catches that by live-invoking
// go vet under the alternate tag.
package sqlite

import (
	"os"
	"os/exec"
	"testing"
)

// TestVectorConstantsExported pins REQ-VEC-001: the shared dimension
// constants must be visible in the default (non-tagged) build so the
// sqlite_blob adapter and any consumer can reference them without a build
// tag. If this test fails, the constants were accidentally moved into a
// build-tag-gated file.
func TestVectorConstantsExported(t *testing.T) {
	if DefaultEmbeddingDimension < MinEmbeddingDimension ||
		DefaultEmbeddingDimension > MaxEmbeddingDimension {
		t.Errorf("DefaultEmbeddingDimension=%d must be within [%d, %d]",
			DefaultEmbeddingDimension, MinEmbeddingDimension, MaxEmbeddingDimension)
	}
	if MinEmbeddingDimension <= 0 {
		t.Errorf("MinEmbeddingDimension must be positive, got %d", MinEmbeddingDimension)
	}
	if MaxEmbeddingDimension <= MinEmbeddingDimension {
		t.Errorf("MaxEmbeddingDimension=%d must exceed MinEmbeddingDimension=%d",
			MaxEmbeddingDimension, MinEmbeddingDimension)
	}
}

// TestCortexVectorsBuildCompiles is the defect pin: it invokes
// `go vet -tags cortex_vectors .` against this package to verify that the
// tagged build configuration (including all cortex_vectors-gated test
// files) compiles cleanly. This catches the class of regression where a
// tagged test file references a symbol that no longer exists.
func TestCortexVectorsBuildCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live cross-tag compilation in -short mode")
	}
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("cannot determine package directory: %v", err)
	}
	cmd := exec.Command("go", "vet", "-tags", "cortex_vectors", ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go vet -tags cortex_vectors . FAILED (tagged build is broken): %v\n%s", err, out)
	}
}

// TestDefaultBuildCompiles is the mirror pin: it invokes `go vet .`
// (no tags) to verify the default (stub) build compiles. This catches the
// reverse regression where a default-build symbol is accidentally moved
// into a tagged-only file.
func TestDefaultBuildCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live cross-tag compilation in -short mode")
	}
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("cannot determine package directory: %v", err)
	}
	cmd := exec.Command("go", "vet", ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go vet . FAILED (default build is broken): %v\n%s", err, out)
	}
}
