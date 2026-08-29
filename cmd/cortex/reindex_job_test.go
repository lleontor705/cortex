package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/config"
	serverplatform "github.com/lleontor705/cortex/v2/internal/platform/server"
)

type fakeServerReindexJob struct {
	project string
	closed  bool
}

func (j *fakeServerReindexJob) ReindexProject(_ context.Context, project string) (*serverplatform.ReindexResult, error) {
	j.project = project
	return &serverplatform.ReindexResult{Total: 1, Upserted: 1, Batches: 1}, nil
}

func (j *fakeServerReindexJob) Close() error {
	j.closed = true
	return nil
}

func TestRunServerReindexUsesBoundedJobAndClosesIt(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "server.yaml")
	if err := os.WriteFile(configPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	project := "10000000-a000-0000-0000-000000000003"
	job := &fakeServerReindexJob{}
	original := openServerReindexJob
	openServerReindexJob = func(context.Context, config.Config) (serverReindexJob, error) {
		return job, nil
	}
	t.Cleanup(func() { openServerReindexJob = original })

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := runContext(context.Background(), []string{"cortex", "--mode", "server", "--config", configPath, "reindex", "--project-id", project}, stdout, stderr)
	if code != 0 {
		t.Fatalf("code = %d stderr=%q", code, stderr.String())
	}
	if job.project != project || !job.closed {
		t.Fatalf("job project=%q closed=%v", job.project, job.closed)
	}
	if !strings.Contains(stdout.String(), "server reindex complete") || strings.Contains(stdout.String(), "server endpoint") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}
