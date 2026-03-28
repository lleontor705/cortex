package main

import (
	"bytes"
	"testing"
)

func TestRunUnknownCommandReturnsError(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := run([]string{"cortex", "wat"}, stdout, stderr)
	if code == 0 {
		t.Fatalf("run() code = %d, want non-zero", code)
	}
	if stderr.Len() == 0 {
		t.Fatal("run() stderr was empty")
	}
}

func TestRunHelpReturnsZero(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := run([]string{"cortex", "help"}, stdout, stderr)
	if code != 0 {
		t.Fatalf("run() code = %d, want 0", code)
	}
	if stdout.Len() == 0 {
		t.Fatal("run() stdout was empty")
	}
	if stderr.Len() != 0 {
		t.Fatalf("run() stderr = %q, want empty", stderr.String())
	}
}
