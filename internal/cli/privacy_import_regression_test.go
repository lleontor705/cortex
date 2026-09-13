package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/app"
	"github.com/lleontor705/cortex/v2/internal/domain"
	_ "modernc.org/sqlite"
)

func TestCLIPrivacy_ImportRejectsValidFirstRequiredEmptySecond(t *testing.T) {
	cases := []struct {
		name          string
		secondTitle   string
		secondContent string
	}{
		{
			name:          "raw_empty_title",
			secondTitle:   "",
			secondContent: "Valid Content 2",
		},
		{
			name:          "whitespace_title",
			secondTitle:   "   ",
			secondContent: "Valid Content 2",
		},
		{
			name:          "raw_empty_content",
			secondTitle:   "Valid Title 2",
			secondContent: "",
		},
		{
			name:          "whitespace_content",
			secondTitle:   "Valid Title 2",
			secondContent: "   ",
		},
	}

	for i, tc := range cases {
		tc := tc
		idx := i
		t.Run(tc.name, func(t *testing.T) {
			canaryT := fmt.Sprintf("canary_reg_title_%d_7711", idx)
			canaryC := fmt.Sprintf("canary_reg_content_%d_8822", idx)

			item1 := domain.Observation{
				Title:     "Valid First <private>" + canaryT + "</private> Title",
				Content:   "Valid First <private>" + canaryC + "</private> Content",
				Project:   "p-regression",
				Type:      "manual",
				SessionID: fmt.Sprintf("s-imp-v1-%d", idx),
				Tags:      []string{"privacy", "regression"},
				Scope:     "project",
			}
			item2 := domain.Observation{
				Title:     tc.secondTitle,
				Content:   tc.secondContent,
				Project:   "p-regression",
				Type:      "manual",
				SessionID: fmt.Sprintf("s-imp-bad2-%d", idx),
				Tags:      []string{"privacy", "bad"},
				Scope:     "project",
			}

			t.Run("cli_import", func(t *testing.T) {
				_, openDB := setupCLITestDB(t)
				a, err := app.Open(context.Background(), app.Options{})
				if err != nil {
					t.Fatalf("init database schema: %v", err)
				}
				_ = a.Close()

				jsonData, mErr := json.Marshal([]domain.Observation{item1, item2})
				if mErr != nil {
					t.Fatalf("marshal observations to json: %v", mErr)
				}

				jsonFile := filepath.Join(t.TempDir(), "regression_import.json")
				if wErr := os.WriteFile(jsonFile, jsonData, 0o600); wErr != nil {
					t.Fatalf("write json file: %v", wErr)
				}

				stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
				code := Run([]string{"cortex", "import", "--from-json", "--path", jsonFile}, stdout, stderr)
				if code != 1 {
					t.Errorf("expected exit code 1 on rejected import, got %d (stdout=%q, stderr=%q)", code, stdout.String(), stderr.String())
				}

				db := openDB()
				defer func() { _ = db.Close() }()

				if obsCount := countDBRows(t, db, "observations"); obsCount != 0 {
					t.Errorf("expected 0 observations after rejected import, got %d", obsCount)
				}
				if sessCount := countDBRows(t, db, "sessions"); sessCount != 0 {
					t.Errorf("expected 0 sessions after rejected import, got %d", sessCount)
				}

				outStr := stdout.String()
				errStr := stderr.String()
				if strings.Contains(outStr, canaryT) || strings.Contains(outStr, canaryC) {
					t.Errorf("stdout leaked canary payload: %q", outStr)
				}
				if strings.Contains(errStr, canaryT) || strings.Contains(errStr, canaryC) {
					t.Errorf("stderr leaked canary payload: %q", errStr)
				}
				if strings.Contains(outStr, "Imported") {
					t.Errorf("expected no success summary in stdout on rejected import, got %q", outStr)
				}
			})

			t.Run("preflight_immutability", func(t *testing.T) {
				item1Copy := item1
				if len(item1.Tags) > 0 {
					item1Copy.Tags = append([]string(nil), item1.Tags...)
				}
				item2Copy := item2
				if len(item2.Tags) > 0 {
					item2Copy.Tags = append([]string(nil), item2.Tags...)
				}

				batch := []*domain.Observation{&item1Copy, &item2Copy}

				orig1Snapshot := item1Copy
				if len(item1Copy.Tags) > 0 {
					orig1Snapshot.Tags = append([]string(nil), item1Copy.Tags...)
				}
				orig2Snapshot := item2Copy
				if len(item2Copy.Tags) > 0 {
					orig2Snapshot.Tags = append([]string(nil), item2Copy.Tags...)
				}

				staged, pErr := preflightObservations(batch)
				if pErr == nil {
					t.Errorf("expected preflight error for empty/whitespace required field, got nil")
				}
				if staged != nil {
					t.Errorf("expected nil staged observations on preflight error, got %v", staged)
				}
				if !reflect.DeepEqual(item1Copy, orig1Snapshot) {
					t.Errorf("caller item1 mutated on preflight: got %+v, want %+v", item1Copy, orig1Snapshot)
				}
				if !reflect.DeepEqual(item2Copy, orig2Snapshot) {
					t.Errorf("caller item2 mutated on preflight: got %+v, want %+v", item2Copy, orig2Snapshot)
				}
				if item1Copy.Title != orig1Snapshot.Title || item1Copy.Content != orig1Snapshot.Content ||
					item1Copy.Project != orig1Snapshot.Project || item1Copy.SessionID != orig1Snapshot.SessionID ||
					item1Copy.Type != orig1Snapshot.Type || item1Copy.Scope != orig1Snapshot.Scope {
					t.Errorf("caller item1 field mutation detected: got (%q, %q, %q, %q), want (%q, %q, %q, %q)",
						item1Copy.Title, item1Copy.Content, item1Copy.Project, item1Copy.SessionID,
						orig1Snapshot.Title, orig1Snapshot.Content, orig1Snapshot.Project, orig1Snapshot.SessionID)
				}
				if item2Copy.Title != orig2Snapshot.Title || item2Copy.Content != orig2Snapshot.Content ||
					item2Copy.Project != orig2Snapshot.Project || item2Copy.SessionID != orig2Snapshot.SessionID ||
					item2Copy.Type != orig2Snapshot.Type || item2Copy.Scope != orig2Snapshot.Scope {
					t.Errorf("caller item2 field mutation detected: got (%q, %q, %q, %q), want (%q, %q, %q, %q)",
						item2Copy.Title, item2Copy.Content, item2Copy.Project, item2Copy.SessionID,
						orig2Snapshot.Title, orig2Snapshot.Content, orig2Snapshot.Project, orig2Snapshot.SessionID)
				}
			})
		})
	}
}
