package bench

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGoToolchainContract(t *testing.T) {
	t.Parallel()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve repository root")
	}
	root := filepath.Dir(filepath.Dir(currentFile))

	goMod := readContractFile(t, filepath.Join(root, "go.mod"))
	if !strings.Contains(goMod, "go 1.27.0\n") {
		t.Error("go.mod must declare Go language version 1.27.0")
	}
	if !strings.Contains(goMod, "toolchain go1.27.1\n") {
		t.Error("go.mod must require toolchain go1.27.1")
	}
	for _, dependency := range []string{
		"golang.org/x/text v0.41.0",
		"google.golang.org/grpc v1.84.0",
	} {
		if !strings.Contains(goMod, dependency) {
			t.Errorf("go.mod must pin %s", dependency)
		}
	}

	// ci.yml and release.yml were flipped to the aligned 1.27.1 pins by the tc2
	// alignment task; ci-reusable.yml carries the same aligned 1.27.1 pins.
	for _, workflow := range []struct {
		path    string
		version string
	}{
		{".github/workflows/ci-reusable.yml", "1.27.1"},
		{".github/workflows/ci.yml", "1.27.1"},
		{".github/workflows/release.yml", "1.27.1"},
	} {
		text := readContractFile(t, filepath.Join(root, filepath.FromSlash(workflow.path)))
		exact := `go-version: "` + workflow.version + `"`
		major := `go-version: "` + workflow.version[:strings.Index(workflow.version, ".")]
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, major) && line != exact {
				t.Errorf("%s pins a non-exact Go toolchain", workflow.path)
			}
		}
		if !strings.Contains(text, exact) {
			t.Errorf("%s must use exact Go %s", workflow.path, workflow.version)
		}
	}

	dockerfile := readContractFile(t, filepath.Join(root, "docker", "Dockerfile"))
	if !strings.Contains(dockerfile, "FROM golang:1.27.1-alpine AS builder") {
		t.Error("Docker builder must use Go 1.27.1")
	}
	if !strings.Contains(dockerfile, "CGO_ENABLED=0 go build") {
		t.Error("Docker build must preserve zero-CGO")
	}
}

func readContractFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}
