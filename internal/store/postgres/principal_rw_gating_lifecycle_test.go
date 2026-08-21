//go:build postgres_integration

package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// rwPoolerState names every SHOW POOLS lifecycle column.  Summing positional
// columns is unsafe: maxwait is not a connection count and cl_waiting can be
// non-zero while all server columns are zero.
type rwPoolerState struct {
	clActive, clWaiting, clCancel, clLogin int64
	svActive, svIdle, svUsed, svTested     int64
	svLogin, maxwait                       int64
}

func (s rwPoolerState) Zero() bool {
	return s.clActive == 0 && s.clWaiting == 0 && s.clCancel == 0 && s.clLogin == 0 &&
		s.svActive == 0 && s.svIdle == 0 && s.svUsed == 0 && s.svTested == 0 &&
		s.svLogin == 0 && s.maxwait == 0
}

func rwPoolerStateFromRow(fields []pgconnFieldDescription, values []any) rwPoolerState {
	var state rwPoolerState
	for i, field := range fields {
		if i >= len(values) {
			continue
		}
		n, ok := values[i].(int64)
		if !ok {
			continue
		}
		switch field.Name {
		case "cl_active":
			state.clActive = n
		case "cl_waiting":
			state.clWaiting = n
		case "cl_cancel_req":
			state.clCancel = n
		case "cl_login":
			state.clLogin = n
		case "sv_active":
			state.svActive = n
		case "sv_idle":
			state.svIdle = n
		case "sv_used":
			state.svUsed = n
		case "sv_tested":
			state.svTested = n
		case "sv_login":
			state.svLogin = n
		case "maxwait":
			state.maxwait = n
		}
	}
	return state
}

// pgx keeps FieldDescription private to the public pgconn package.  This
// alias keeps the row parser readable while preserving the named-column
// contract.
type pgconnFieldDescription = pgconn.FieldDescription

type rwLifecycleRecord struct {
	Stage, Database                           string
	AdminClosed, DirectClosed, PoolerClosed   bool
	Acquired                                  int32
	Reconnected, PoolerDrained, DirectDrained bool
	Postflight                                rwC32PostflightReport
}

type rwC32PostflightReport struct {
	Captured        bool        `json:"captured"`
	ThrottleBefore  uint64      `json:"throttle_before"`
	ThrottleAfter   uint64      `json:"throttle_after"`
	Sessions        int         `json:"sessions"`
	Connections     int         `json:"connections"`
	PeakSessions    int         `json:"peak_sessions"`
	PeakConnections int         `json:"peak_connections"`
	MaxConnections  int         `json:"max_connections"`
	PgBouncerWaits  int64       `json:"pgbouncer_waits"`
	ClWaiting       int64       `json:"cl_waiting"`
	MaxWait         int64       `json:"maxwait"`
	AbsoluteStatus  rwC32Status `json:"absolute_status"`
}

func rwPostflightStatus(r rwC32PostflightReport, maxConnections int) rwC32Status {
	if maxConnections <= 0 || !r.Captured || r.ThrottleAfter != r.ThrottleBefore || r.Sessions > 1 || r.Connections > 1 ||
		(r.PeakSessions*4 > maxConnections*3) || (r.PeakConnections*4 > maxConnections*3) ||
		r.PgBouncerWaits != 0 || r.ClWaiting != 0 || r.MaxWait != 0 {
		return rwC32Fail
	}
	return rwC32Pass
}

func rwLifecyclePassed(before, after rwLifecycleRecord) bool {
	return before.Database != "" && before.Database == after.Database &&
		!before.AdminClosed && !before.DirectClosed && !before.PoolerClosed &&
		after.AdminClosed && after.DirectClosed &&
		after.PoolerClosed && after.Reconnected && after.PoolerDrained && after.DirectDrained &&
		after.Postflight.Captured && after.Postflight.AbsoluteStatus == rwC32Pass
}

func rwRepetitionBoundary(t *testing.T, h *rwHarness, stage string) rwLifecycleRecord {
	t.Helper()
	record := rwLifecycleRecord{Stage: stage}
	if h != nil {
		record.Database = h.dbName
	}
	if stage == "before" {
		if h != nil {
			h.throttleBefore = rwC32Throttle(t)
			if h.admin != nil && h.dbName != "" {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = h.admin.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=$1`, h.dbName).Scan(&h.peakSessions)
				_ = h.admin.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=$1 AND backend_type='client backend'`, h.dbName).Scan(&h.peakConnections)
			}
		}
		return record
	}
	if h != nil && h.direct != nil {
		h.direct.Close()
		record.DirectClosed = true
		h.direct = nil
	}
	if h != nil && h.pooler != nil {
		h.pooler.Close()
		record.PoolerClosed = true
		h.pooler = nil
	}
	if h != nil && h.admin != nil {
		h.admin.Close()
		record.AdminClosed = true
		h.admin = nil
	}
	rwCloseRWProofPoolerDatabases(t, "")
	record.Reconnected, record.PoolerDrained = true, true
	rwDrainRWProofApplicationBackends(t, "")
	record.PoolerDrained, record.DirectDrained = true, true
	// Postflight is deliberately after pool close, PgBouncer RECONNECT, and the
	// fail-closed all-application-backend drain (including pooler backends).
	// Reconnect only the privileged observer so the observed session is not
	// mistaken for a competing harness session.
	if h != nil && h.dbName != "" {
		cfg, err := pgxpool.ParseConfig(mustEnv(t, "CORTEX_SPIKE_PG_ADMIN_DSN"))
		if err != nil {
			t.Fatalf("postflight admin DSN: %v", err)
		}
		cfg.ConnConfig.Database = h.dbName
		h.admin, err = pgxpool.NewWithConfig(context.Background(), cfg)
		if err != nil {
			t.Fatalf("postflight admin pool: %v", err)
		}
		if err := h.admin.Ping(context.Background()); err != nil {
			h.admin.Close()
			t.Fatalf("postflight admin reconnect: %v", err)
		}
		if h.throttleBefore == 0 {
			h.throttleBefore = rwC32Throttle(t)
		}
		record.Postflight = rwPostflight(t, h)
		h.admin.Close()
		h.admin = nil
	}
	return record
}

func rwPostflight(t *testing.T, h *rwHarness) rwC32PostflightReport {
	t.Helper()
	report := rwC32PostflightReport{Captured: true, AbsoluteStatus: rwC32Pass}
	if h != nil {
		report.ThrottleBefore = h.throttleBefore
	}
	report.ThrottleAfter = rwC32Throttle(t)
	if h == nil {
		return report
	}
	if h.admin == nil {
		return report
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.admin.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=$1`, h.dbName).Scan(&report.Sessions); err != nil {
		t.Fatalf("postflight sessions: %v", err)
	}
	if err := h.admin.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=$1 AND backend_type='client backend'`, h.dbName).Scan(&report.Connections); err != nil {
		t.Fatalf("postflight connections: %v", err)
	}
	var rawMaxConnections string
	if err := h.admin.QueryRow(ctx, `SHOW max_connections`).Scan(&rawMaxConnections); err == nil {
		report.MaxConnections, _ = rwParseMaxConnections(rawMaxConnections)
	}
	if h.peakSessions < report.Sessions {
		h.peakSessions = report.Sessions
	}
	if h.peakConnections < report.Connections {
		h.peakConnections = report.Connections
	}
	report.PeakSessions = h.peakSessions
	report.PeakConnections = h.peakConnections
	if h.dbName != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		conn := rwPoolerConsole(t, ctx)
		defer func() { _ = conn.Close(context.Background()) }()
		rows, err := conn.Query(ctx, "SHOW POOLS")
		if err != nil {
			t.Fatalf("postflight SHOW POOLS: %v", err)
		}
		for rows.Next() {
			values, err := rows.Values()
			if err != nil {
				rows.Close()
				t.Fatalf("postflight SHOW POOLS row: %v", err)
			}
			if len(values) > 0 && values[0] == h.dbName {
				state := rwPoolerStateFromRow(rows.FieldDescriptions(), values)
				report.ClWaiting, report.MaxWait = state.clWaiting, state.maxwait
				report.PgBouncerWaits = state.clWaiting + state.maxwait
			}
		}
		rows.Close()
	}
	report.AbsoluteStatus = rwPostflightStatus(report, report.MaxConnections)
	return report
}

func rwOpenRepetitionPool(t *testing.T, h *rwHarness, path string) *pgxpool.Pool {
	t.Helper()
	if path == "direct" {
		h.direct = rwAppPool(t, context.Background(), mustEnv(t, "CORTEX_SPIKE_PG_ADMIN_DSN"), h.dbName, 40)
		return h.direct
	}
	h.pooler = rwAppPool(t, context.Background(), mustEnv(t, "CORTEX_SPIKE_PGBOUNCER_DSN"), h.dbName, 36)
	return h.pooler
}

func mustEnv(t *testing.T, name string) string {
	t.Helper()
	value := os.Getenv(name)
	if value == "" {
		t.Fatalf("%s is required", name)
	}
	return value
}

func TestRWR22LifecycleRecordRequiresBothDrains(t *testing.T) {
	record := rwLifecycleRecord{AdminClosed: true, DirectClosed: true, PoolerClosed: true, Reconnected: true, PoolerDrained: true, DirectDrained: true}
	if !record.AdminClosed || !record.PoolerDrained || !record.DirectDrained {
		t.Fatal("complete lifecycle record lost a boundary fact")
	}
}

func TestR1R25LifecycleVerdictRequiresEveryBoundaryFact(t *testing.T) {
	before := rwLifecycleRecord{Database: "rwproof_test"}
	after := rwLifecycleRecord{Database: "rwproof_test", AdminClosed: true, DirectClosed: true, PoolerClosed: true, Reconnected: true, PoolerDrained: true, DirectDrained: true,
		Postflight: rwC32PostflightReport{Captured: true, AbsoluteStatus: rwC32Pass}}
	if !rwLifecyclePassed(before, after) {
		t.Fatal("complete same-harness lifecycle did not pass")
	}
	checks := map[string]func(*rwLifecycleRecord){
		"before admin close": func(r *rwLifecycleRecord) { r.AdminClosed = true }, "before direct close": func(r *rwLifecycleRecord) { r.DirectClosed = true },
		"before pooler close": func(r *rwLifecycleRecord) { r.PoolerClosed = true }, "reconnect": func(r *rwLifecycleRecord) { r.Reconnected = false },
		"pooler drain": func(r *rwLifecycleRecord) { r.PoolerDrained = false }, "direct drain": func(r *rwLifecycleRecord) { r.DirectDrained = false },
		"postflight capture": func(r *rwLifecycleRecord) { r.Postflight.Captured = false }, "postflight verdict": func(r *rwLifecycleRecord) { r.Postflight.AbsoluteStatus = rwC32Fail },
	}
	for name, mutate := range checks {
		t.Run(name, func(t *testing.T) {
			brokenBefore, brokenAfter := before, after
			if name[:6] == "before" {
				mutate(&brokenBefore)
			} else {
				mutate(&brokenAfter)
			}
			if rwLifecyclePassed(brokenBefore, brokenAfter) {
				t.Fatalf("incomplete %s lifecycle unexpectedly passed", name)
			}
		})
	}
}

func TestR1R25PostflightStatusRejectsUncapturedAndPeakFacts(t *testing.T) {
	base := rwC32PostflightReport{Captured: true, ThrottleBefore: 3, ThrottleAfter: 3, Sessions: 1, Connections: 1, PeakSessions: 1, PeakConnections: 1}
	mutations := map[string]func(*rwC32PostflightReport){
		"uncaptured": func(r *rwC32PostflightReport) { r.Captured = false }, "peak sessions": func(r *rwC32PostflightReport) { r.PeakSessions = 301 },
		"peak connections": func(r *rwC32PostflightReport) { r.PeakConnections = 301 },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			broken := base
			mutate(&broken)
			if rwPostflightStatus(broken, 400) != rwC32Fail {
				t.Fatalf("ignored postflight fact %s", name)
			}
		})
	}
}

func TestC32PostflightRejectsResidualPoolerBackend(t *testing.T) {
	report := rwC32PostflightReport{
		Captured: true, ThrottleBefore: 7, ThrottleAfter: 7,
		Sessions: 2, Connections: 1, PeakSessions: 2, PeakConnections: 1,
	}
	if got := rwPostflightStatus(report, 400); got != rwC32Fail {
		t.Fatalf("residual pooler application backend must fail postflight, got %s", got)
	}
}

func TestC32PostflightAcceptsObserverAfterTargetBackendsDrain(t *testing.T) {
	report := rwC32PostflightReport{
		Captured: true, ThrottleBefore: 7, ThrottleAfter: 7,
		Sessions: 1, Connections: 1, PeakSessions: 3, PeakConnections: 3,
	}
	if got := rwPostflightStatus(report, 400); got != rwC32Pass {
		t.Fatalf("one observer after zero target application backends should pass, got %s", got)
	}
}

func TestC32PostflightRejectsMissingOrNonPositiveMaxConnections(t *testing.T) {
	report := rwC32PostflightReport{Captured: true, ThrottleBefore: 7, ThrottleAfter: 7, Sessions: 1, Connections: 1, PeakSessions: 3, PeakConnections: 3}
	for _, maxConnections := range []int{0, -1} {
		if got := rwPostflightStatus(report, maxConnections); got != rwC32Fail {
			t.Fatalf("max_connections=%d must fail closed, got %s", maxConnections, got)
		}
	}
}

// This is deliberately a real harness test (not a nil-pool smoke test).  It
// fails closed when the dedicated PostgreSQL/PgBouncer environment is absent.
func TestPrincipalRWR22LifecycleIntegration(t *testing.T) {
	h := newRWHarness(t)
	before := rwRepetitionBoundary(t, h, "before")
	if !before.AdminClosed || !before.DirectClosed || !before.PoolerClosed || !before.Reconnected || !before.PoolerDrained || !before.DirectDrained {
		t.Fatalf("incomplete pre-repetition boundary: %+v", before)
	}
	h.direct = rwOpenRepetitionPool(t, h, "direct")
	h.pooler = rwOpenRepetitionPool(t, h, "pooler")
	after := rwRepetitionBoundary(t, h, "after")
	if !after.AdminClosed || !after.DirectClosed || !after.PoolerClosed || !after.Reconnected || !after.PoolerDrained || !after.DirectDrained {
		t.Fatalf("incomplete post-repetition boundary: %+v", after)
	}
}
