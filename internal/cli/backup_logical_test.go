package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunBackupRestoreLogicalRoundTrip exercises the CLI surface of the
// portable logical snapshot against a real v2 database: save → backup --out →
// fresh DB → restore --from → the memory is readable again.
func TestRunBackupRestoreLogicalRoundTrip(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cortex.db")
	t.Setenv("CORTEX_DATABASE_PATH", dbPath)
	t.Setenv("CORTEX_DATABASE_IN_MEMORY", "false")
	t.Setenv("CORTEX_SYNC_ENABLED", "false")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	if code := Run([]string{"cortex", "save", "Backup me", "Portable logical snapshot payload", "--type", "decision", "--project", "demo"}, stdout, stderr); code != 0 {
		t.Fatalf("save code = %d, stderr = %q", code, stderr.String())
	}

	archive := filepath.Join(t.TempDir(), "snap.tar.gz")
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"cortex", "backup", "--out", archive}, stdout, stderr); code != 0 {
		t.Fatalf("backup code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Portable backup created") {
		t.Fatalf("backup stdout = %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Observations:  1") {
		t.Fatalf("backup stdout missing observation count = %q", stdout.String())
	}

	// Point the CLI at a fresh (wiped) database and restore.
	freshPath := filepath.Join(t.TempDir(), "fresh.db")
	t.Setenv("CORTEX_DATABASE_PATH", freshPath)

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"cortex", "restore", "--from", archive}, stdout, stderr); code != 0 {
		t.Fatalf("restore code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Observations:  1 restored") {
		t.Fatalf("restore stdout = %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "reindex") {
		t.Fatalf("restore stdout missing re-embed guidance = %q", stdout.String())
	}

	// The restored memory is readable via the normal read path.
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"cortex", "search", "Portable logical snapshot payload"}, stdout, stderr); code != 0 {
		t.Fatalf("search after restore code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Backup me") {
		t.Fatalf("search after restore stdout = %q", stdout.String())
	}

	// Idempotent re-run: everything skipped.
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"cortex", "restore", "--from", archive}, stdout, stderr); code != 0 {
		t.Fatalf("second restore code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Observations:  0 restored, 1 skipped") ||
		!strings.Contains(stdout.String(), "Sessions:      0 restored, 1 skipped") {
		t.Fatalf("second restore stdout = %q", stdout.String())
	}
}

func TestRunBackupRequiresOut(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cortex.db")
	t.Setenv("CORTEX_DATABASE_PATH", dbPath)
	t.Setenv("CORTEX_DATABASE_IN_MEMORY", "false")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	if code := Run([]string{"cortex", "restore"}, stdout, stderr); code != 1 {
		t.Fatalf("restore without --from code = %d", code)
	}
	if !strings.Contains(stderr.String(), "--from") {
		t.Fatalf("restore usage stderr = %q", stderr.String())
	}
}
