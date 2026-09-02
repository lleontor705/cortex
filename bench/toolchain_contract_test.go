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
	if !strings.Contains(goMod, "go 1.26.0\n") {
		t.Error("go.mod must declare Go language version 1.26.0")
	}
	if !strings.Contains(goMod, "toolchain go1.26.5\n") {
		t.Error("go.mod must require toolchain go1.26.5")
	}
	for _, dependency := range []string{
		"golang.org/x/text v0.39.0",
		"google.golang.org/grpc v1.82.1",
	} {
		if !strings.Contains(goMod, dependency) {
			t.Errorf("go.mod must pin %s", dependency)
		}
	}

	for _, workflow := range []string{".github/workflows/ci.yml", ".github/workflows/release.yml"} {
		text := readContractFile(t, filepath.Join(root, filepath.FromSlash(workflow)))
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, `go-version: "1.26`) && line != `go-version: "1.26.5"` {
				t.Errorf("%s pins a non-exact Go toolchain", workflow)
			}
		}
		if !strings.Contains(text, `go-version: "1.26.5"`) {
			t.Errorf("%s must use exact Go 1.26.5", workflow)
		}
	}

	dockerfile := readContractFile(t, filepath.Join(root, "docker", "Dockerfile"))
	if !strings.Contains(dockerfile, "FROM golang:1.26.5-alpine AS builder") {
		t.Error("Docker builder must use Go 1.26.5")
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
