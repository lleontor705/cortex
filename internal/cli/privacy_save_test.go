package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/app"
)

func TestCLIPrivacy_SaveRedactedProseAndNoCanaryLeak(t *testing.T) {
	_, openDB := setupCLITestDB(t)
	const canaryT = "canary_cli_title_1122"
	const canaryC = "canary_cli_content_3344"

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := Run([]string{
		"cortex", "save",
		"Feature <private>" + canaryT + "</private> info",
		"Content with <private>" + canaryC + "</private> details",
		"--type", "decision",
		"--project", "cortex-priv",
	}, stdout, stderr)

	if code != 0 {
		t.Fatalf("save code = %d, stderr = %q", code, stderr.String())
	}
	outStr := stdout.String()
	if strings.Contains(outStr, canaryT) || strings.Contains(outStr, canaryC) {
		t.Fatalf("stdout leaked canary: %q", outStr)
	}
	if !strings.Contains(outStr, "[REDACTED]") {
		t.Fatalf("stdout missing [REDACTED]: %q", outStr)
	}

	db := openDB()
	defer func() { _ = db.Close() }()

	var title, content string
	if err := db.QueryRow("SELECT title, content FROM observations LIMIT 1").Scan(&title, &content); err != nil {
		t.Fatalf("query observation: %v", err)
	}
	if strings.Contains(title, canaryT) || strings.Contains(content, canaryC) {
		t.Fatalf("persisted canary leak: title=%q content=%q", title, content)
	}
	if title != "Feature [REDACTED] info" || content != "Content with [REDACTED] details" {
		t.Fatalf("unexpected persisted text: title=%q content=%q", title, content)
	}
}

func TestCLIPrivacy_SaveRejectionZeroEffects(t *testing.T) {
	const canary = "canary_cli_save_rej_9988"

	cases := []struct {
		name string
		args []string
	}{
		{"unclosed title", []string{"cortex", "save", "Title <private>" + canary, "Body", "--project", "test"}},
		{"stray closing title", []string{"cortex", "save", "Title </private>", "Body", "--project", "test"}},
		{"unclosed content", []string{"cortex", "save", "Title", "Body <private>" + canary, "--project", "test"}},
		{"stray closing content", []string{"cortex", "save", "Title", "Body </private>", "--project", "test"}},
		{"nested content", []string{"cortex", "save", "Title", "<private>a <private>b</private></private>", "--project", "test"}},
		{"pure private title", []string{"cortex", "save", "<private>" + canary + "</private>", "Body", "--project", "test"}},
		{"pure private content", []string{"cortex", "save", "Title", "<private>" + canary + "</private>", "--project", "test"}},
		{"marker in project", []string{"cortex", "save", "Title", "Body", "--project", "<private>" + canary + "</private>"}},
		{"marker in topic", []string{"cortex", "save", "Title", "Body", "--project", "test", "--topic", "<private>" + canary + "</private>"}},
		{"marker in scope", []string{"cortex", "save", "Title", "Body", "--project", "test", "--scope", "<private>" + canary + "</private>"}},
		{"marker in type", []string{"cortex", "save", "Title", "Body", "--project", "test", "--type", "<private>" + canary + "</private>"}},
		{"invalid utf8 in title", []string{"cortex", "save", "Title \xff\xfe", "Body", "--project", "test"}},
		{"invalid utf8 in content", []string{"cortex", "save", "Title", "Body \xff\xfe", "--project", "test"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, openDB := setupCLITestDB(t)

			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			code := Run(tc.args, stdout, stderr)
			if code != 1 {
				t.Fatalf("expected exit code 1, got %d (stdout=%q, stderr=%q)", code, stdout.String(), stderr.String())
			}
			errStr := stderr.String()
			if strings.Contains(errStr, canary) {
				t.Fatalf("stderr leaked canary: %q", errStr)
			}
			if !strings.Contains(errStr, "privacy:") {
				t.Fatalf("stderr missing privacy error: %q", errStr)
			}

			db := openDB()
			defer func() { _ = db.Close() }()

			if obsCount := countDBRows(t, db, "observations"); obsCount != 0 {
				t.Fatalf("expected 0 observations after rejection, got %d", obsCount)
			}
			if sessCount := countDBRows(t, db, "sessions"); sessCount != 0 {
				t.Fatalf("expected 0 sessions after rejection, got %d", sessCount)
			}
		})
	}
}

func TestCLIPrivacy_SaveRawEmptyWhitespaceRejectionZeroEffects(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"raw empty title", []string{"cortex", "save", "", "Valid Body", "--project", "test"}},
		{"whitespace title spaces", []string{"cortex", "save", "   ", "Valid Body", "--project", "test"}},
		{"whitespace title tab and newline", []string{"cortex", "save", "\t\n", "Valid Body", "--project", "test"}},
		{"raw empty content", []string{"cortex", "save", "Valid Title", "", "--project", "test"}},
		{"whitespace content spaces", []string{"cortex", "save", "Valid Title", "   ", "--project", "test"}},
		{"whitespace content tab and newline", []string{"cortex", "save", "Valid Title", " \t \n ", "--project", "test"}},
		{"raw empty both", []string{"cortex", "save", "", "", "--project", "test"}},
		{"whitespace both", []string{"cortex", "save", "   ", "   ", "--project", "test"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, openDB := setupCLITestDB(t)
			a, err := app.Open(context.Background(), app.Options{})
			if err != nil {
				t.Fatalf("init db: %v", err)
			}
			_ = a.Close()

			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			code := Run(tc.args, stdout, stderr)
			if code != 1 {
				t.Fatalf("expected exit code 1, got %d (stdout=%q, stderr=%q)", code, stdout.String(), stderr.String())
			}
			errStr := stderr.String()
			if !strings.Contains(errStr, "is required") {
				t.Fatalf("stderr missing required field error: %q", errStr)
			}

			db := openDB()
			defer func() { _ = db.Close() }()

			if obsCount := countDBRows(t, db, "observations"); obsCount != 0 {
				t.Fatalf("expected 0 observations after rejection, got %d", obsCount)
			}
			if sessCount := countDBRows(t, db, "sessions"); sessCount != 0 {
				t.Fatalf("expected 0 sessions after rejection, got %d", sessCount)
			}
		})
	}
}
