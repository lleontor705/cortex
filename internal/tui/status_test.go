package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/app"
)

func TestStatusVocabulary(t *testing.T) {
	t.Run("sync state identifiers", func(t *testing.T) {
		cases := []struct {
			state SyncState
			want  string
		}{
			{SyncUnknown, "unknown"},
			{SyncRunning, "running"},
			{SyncDisabled, "disabled"},
			{SyncNotConfigured, "not-configured"},
			{SyncRestartRequired, "restart-required"},
			{SyncError, "error"},
			{SyncState(99), "invalid"},
		}
		for _, tc := range cases {
			if got := tc.state.String(); got != tc.want {
				t.Errorf("SyncState(%d).String() = %q, want %q", tc.state, got, tc.want)
			}
		}
	})

	t.Run("chip text is honest for every state", func(t *testing.T) {
		cases := []struct {
			name    string
			chip    SyncChip
			want    string
			success bool
		}{
			{"running with interval", SyncChip{State: SyncRunning, Interval: 30 * time.Second}, "Sync: running · every 30s", true},
			{"running without interval", SyncChip{State: SyncRunning}, "Sync: running", true},
			{"disabled", SyncChip{State: SyncDisabled}, "Sync: disabled", false},
			{"not configured", SyncChip{State: SyncNotConfigured}, "Sync: not configured", false},
			{"restart required", SyncChip{State: SyncRestartRequired}, "Sync: restart required", false},
			{"error with reason", SyncChip{State: SyncError, Reason: "connection refused"}, "Sync: error — connection refused", false},
			{"error without reason", SyncChip{State: SyncError}, "Sync: error", false},
			{"zero value fails closed", SyncChip{}, "Sync: unknown", false},
		}
		for _, tc := range cases {
			if got := tc.chip.Text(); got != tc.want {
				t.Errorf("%s: Text() = %q, want %q", tc.name, got, tc.want)
			}
			if got := tc.chip.IsSuccess(); got != tc.success {
				t.Errorf("%s: IsSuccess() = %v, want %v", tc.name, got, tc.success)
			}
			if !tc.success && strings.Contains(tc.chip.Text(), "running") {
				t.Errorf("%s: non-running chip must not render as running: %q", tc.name, tc.chip.Text())
			}
		}
	})

	t.Run("chip derived from live status", func(t *testing.T) {
		cases := []struct {
			name        string
			status      app.SyncStatus
			hasEndpoint bool
			want        SyncState
		}{
			{"running", app.SyncStatus{Running: true, Configured: true, TokenSet: true}, true, SyncRunning},
			{"configured without token is an error", app.SyncStatus{Configured: true, TokenSet: false}, true, SyncError},
			{"configured but not running needs restart", app.SyncStatus{Configured: true, TokenSet: true}, true, SyncRestartRequired},
			{"endpoint present but disabled", app.SyncStatus{Configured: false}, true, SyncDisabled},
			{"no endpoint", app.SyncStatus{}, false, SyncNotConfigured},
			{"running wins without endpoint", app.SyncStatus{Running: true}, false, SyncRunning},
		}
		for _, tc := range cases {
			got := SyncChipFromStatus(tc.status, tc.hasEndpoint)
			if got.State != tc.want {
				t.Errorf("%s: state = %s, want %s", tc.name, got.State, tc.want)
			}
			if tc.want == SyncError && strings.TrimSpace(got.Reason) == "" {
				t.Errorf("%s: error chip must carry a reason", tc.name)
			}
		}
	})

	t.Run("save result toast and banner", func(t *testing.T) {
		cases := []struct {
			name          string
			result        SaveResult
			wantKind      ToastKind
			wantKindName  string
			msgContains   string
			wantBanner    bool
			bannerHasText string
		}{
			{"applied", SaveResult{}, ToastSavedApplied, "saved-applied", "changes applied", false, ""},
			{"restart required names fields", SaveResult{RestartFields: []string{"database path"}}, ToastSavedRestartRequired, "saved-restart-required", "restart required for: database path", true, "database path"},
			{"failed carries reason", SaveResult{Err: errors.New("disk is read-only")}, ToastFailed, "failed", "disk is read-only", false, ""},
			{"failure ignores stale restart fields", SaveResult{Err: errors.New("boom"), RestartFields: []string{"database path"}}, ToastFailed, "failed", "boom", false, ""},
		}
		for _, tc := range cases {
			if got := tc.result.Kind(); got != tc.wantKind {
				t.Errorf("%s: Kind() = %v, want %v", tc.name, got, tc.wantKind)
			}
			if got := tc.result.Kind().String(); got != tc.wantKindName {
				t.Errorf("%s: Kind().String() = %q, want %q", tc.name, got, tc.wantKindName)
			}
			msg := tc.result.ToastMessage()
			if !strings.Contains(msg, tc.msgContains) {
				t.Errorf("%s: ToastMessage() = %q, want it to contain %q", tc.name, msg, tc.msgContains)
			}
			banner := tc.result.Banner()
			if banner.Visible() != tc.wantBanner {
				t.Errorf("%s: banner visible = %v, want %v", tc.name, banner.Visible(), tc.wantBanner)
			}
			if tc.wantBanner && !strings.Contains(banner.Text(), tc.bannerHasText) {
				t.Errorf("%s: banner text = %q, want it to contain %q", tc.name, banner.Text(), tc.bannerHasText)
			}
			if tc.wantKind == ToastSavedApplied && banner.Visible() {
				t.Errorf("%s: saved-applied toast must never coexist with a restart banner", tc.name)
			}
			if tc.wantKind == ToastFailed && strings.Contains(msg, "Saved") {
				t.Errorf("%s: failed save must never render as success: %q", tc.name, msg)
			}
		}
	})

	t.Run("banner names all fields and copies defensively", func(t *testing.T) {
		var zero RestartBanner
		if zero.Visible() || zero.Text() != "" {
			t.Errorf("zero banner must be invisible and empty, got %q", zero.Text())
		}

		result := SaveResult{RestartFields: []string{"database path", "config format"}}
		banner := result.Banner()
		text := banner.Text()
		if !banner.Visible() {
			t.Fatal("banner should be visible")
		}
		for _, field := range []string{"database path", "config format", "already applied"} {
			if !strings.Contains(text, field) {
				t.Errorf("banner text %q must name %q", text, field)
			}
		}

		result.RestartFields[0] = "mutated"
		if strings.Contains(banner.Text(), "mutated") {
			t.Error("banner must copy fields, not alias the SaveResult slice")
		}
	})
}
