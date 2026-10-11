package tui

import (
	"strings"
	"time"

	"github.com/lleontor705/cortex/v2/internal/app"
)

// SyncState is the honest state of the hybrid sync subsystem as surfaced by the
// Dashboard header chip and the Configuration server status. The vocabulary is
// closed so that no value can be silently mistaken for a running loop.
type SyncState int

const (
	// SyncUnknown is the fail-closed zero value: no snapshot has been taken, so
	// the state is unknown and must never render as success.
	SyncUnknown SyncState = iota
	// SyncRunning means the replication loop goroutine is currently active.
	SyncRunning
	// SyncDisabled means an endpoint exists but sync was explicitly turned off.
	SyncDisabled
	// SyncNotConfigured means no sync endpoint has been configured yet.
	SyncNotConfigured
	// SyncRestartRequired means sync is configured but the loop is not running
	// and will not start until the process restarts.
	SyncRestartRequired
	// SyncError means sync is configured but cannot operate; Reason explains why.
	SyncError
)

// String returns the stable identifier for the state used in diagnostics and tests.
func (s SyncState) String() string {
	switch s {
	case SyncUnknown:
		return "unknown"
	case SyncRunning:
		return "running"
	case SyncDisabled:
		return "disabled"
	case SyncNotConfigured:
		return "not-configured"
	case SyncRestartRequired:
		return "restart-required"
	case SyncError:
		return "error"
	default:
		return "invalid"
	}
}

// SyncChip is the honest status indicator for the sync subsystem. Interval is
// meaningful only while State is SyncRunning; Reason is meaningful only while
// State is SyncError.
type SyncChip struct {
	State    SyncState
	Interval time.Duration
	Reason   string
}

// Text renders the chip label. A non-running state never renders as success:
// disabled, unconfigured, restart-pending, and failed states are always labelled
// as such.
func (c SyncChip) Text() string {
	switch c.State {
	case SyncUnknown:
		return "Sync: unknown"
	case SyncRunning:
		if c.Interval > 0 {
			return "Sync: running · every " + c.Interval.String()
		}
		return "Sync: running"
	case SyncDisabled:
		return "Sync: disabled"
	case SyncNotConfigured:
		return "Sync: not configured"
	case SyncRestartRequired:
		return "Sync: restart required"
	case SyncError:
		if reason := strings.TrimSpace(c.Reason); reason != "" {
			return "Sync: error — " + reason
		}
		return "Sync: error"
	default:
		return "Sync: unknown"
	}
}

// IsSuccess reports whether the chip represents an actually-running loop. Only
// SyncRunning qualifies, so callers gate green styling on this method rather
// than on the mere absence of an error.
func (c SyncChip) IsSuccess() bool {
	return c.State == SyncRunning
}

// SyncChipFromStatus derives the honest chip from a live app.SyncStatus
// snapshot. hasEndpoint reports whether a sync URL is configured even while sync
// is disabled, which is what separates SyncDisabled from SyncNotConfigured.
// Interval is left unset; callers populate it from configuration when running.
func SyncChipFromStatus(status app.SyncStatus, hasEndpoint bool) SyncChip {
	switch {
	case status.Running:
		return SyncChip{State: SyncRunning}
	case status.Configured && !status.TokenSet:
		return SyncChip{State: SyncError, Reason: "no sync token configured"}
	case status.Configured:
		return SyncChip{State: SyncRestartRequired}
	case hasEndpoint:
		return SyncChip{State: SyncDisabled}
	default:
		return SyncChip{State: SyncNotConfigured}
	}
}

// ToastKind classifies the single honest toast a configuration save can
// produce. The vocabulary has no generic success: a save either applied,
// requires a restart for named fields, or failed.
type ToastKind int

const (
	// ToastSavedApplied means every change was written and hot-reloaded.
	ToastSavedApplied ToastKind = iota
	// ToastSavedRestartRequired means the config was written but named fields
	// need a process restart before they take effect.
	ToastSavedRestartRequired
	// ToastFailed means the save did not take effect and never shows success.
	ToastFailed
)

// String returns the stable identifier for the toast kind.
func (k ToastKind) String() string {
	switch k {
	case ToastSavedApplied:
		return "saved-applied"
	case ToastSavedRestartRequired:
		return "saved-restart-required"
	case ToastFailed:
		return "failed"
	default:
		return "invalid"
	}
}

// SaveResult is the single honest outcome of a configuration save. Because the
// toast message and the restart banner are both derived from this one value,
// "saved — applied" can never coexist with a restart banner, and a failure can
// never render as success.
type SaveResult struct {
	// Err is non-nil only when the save did not take effect.
	Err error
	// RestartFields names exactly the changed fields that require a process
	// restart. It is ignored when Err is set.
	RestartFields []string
}

// Kind reduces the save outcome to its honest toast class.
func (r SaveResult) Kind() ToastKind {
	switch {
	case r.Err != nil:
		return ToastFailed
	case len(r.RestartFields) > 0:
		return ToastSavedRestartRequired
	default:
		return ToastSavedApplied
	}
}

// ToastMessage renders the honest user-facing toast text.
func (r SaveResult) ToastMessage() string {
	switch r.Kind() {
	case ToastFailed:
		return "Save failed: " + r.Err.Error()
	case ToastSavedRestartRequired:
		return "Saved — restart required for: " + strings.Join(r.RestartFields, ", ")
	default:
		return "Saved — changes applied"
	}
}

// Banner returns the amber restart banner for this outcome. It is empty unless
// the save succeeded and genuinely requires a restart, which structurally makes
// a saved-applied toast coexisting with a banner unrepresentable.
func (r SaveResult) Banner() RestartBanner {
	if r.Kind() != ToastSavedRestartRequired {
		return RestartBanner{}
	}
	return RestartBanner{Fields: append([]string(nil), r.RestartFields...)}
}

// RestartBanner lists exactly the fields that require a process restart after a
// save. A zero-value banner means no restart is required and must not be shown.
type RestartBanner struct {
	Fields []string
}

// Visible reports whether the banner has fields to show.
func (b RestartBanner) Visible() bool {
	return len(b.Fields) > 0
}

// Text renders the banner copy, naming every affected field. It returns an empty
// string when no restart is required.
func (b RestartBanner) Text() string {
	if !b.Visible() {
		return ""
	}
	return "⚠ Saved. Restart required for: " + strings.Join(b.Fields, ", ") +
		". All other changes are already applied."
}
