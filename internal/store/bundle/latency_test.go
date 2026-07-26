// Package bundle_test houses the p95 save-latency envelope architecture test
// and saturation-flood benchmark for REQ-TX-002 (W2.2).
//
// This file is the companion to W2.1's bounded-save path (SQLiteUnitOfWork
// with BusyRetryConfig retry). W2.1 proved that a saturation flood resolves
// within a bounded window (TestUnitOfWork_NoUnboundedBlocking). W2.2 REGISTERS
// the quantitative p95 envelope and ENFORCES it via an architecture test +
// saturation-flood Test.
//
// REQ-TX-002 Error scenario (defect pin):
//   "A saturation test floods the writer → No save blocks beyond the bounded
//    duration, p95 save latency stays within the registered envelope, and the
//    suite pins that the prior unbounded behavior is gone."
package bundle_test

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite" // Pure Go SQLite driver (zero-CGO)

	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/store/bundle"
)

// ---------------------------------------------------------------------------
// RED #1: Architecture test — p95 envelope registered, positive, finite
// ---------------------------------------------------------------------------

// TestP95SaveLatencyEnvelope_Registered is the architecture test that asserts
// the p95 save-latency envelope is DECLARED, POSITIVE, and FINITE. It is the
// "registered and asserted" acceptance criterion for W2.2.
//
// The envelope is a design contract: it bounds the maximum acceptable p95 save
// latency under the standard saturation-flood scenario (50 concurrent writers,
// production-like DSN with busy_timeout=5s, single-writer pool). See
// DefaultP95SaveLatencyEnvelope in latency.go for the measurement rationale.
func TestP95SaveLatencyEnvelope_Registered(t *testing.T) {
	env := bundle.DefaultP95SaveLatencyEnvelope

	if env <= 0 {
		t.Fatalf("DefaultP95SaveLatencyEnvelope = %v, must be positive (REQ-TX-002 envelope not registered)", env)
	}

	// Finite check: must not be the max representable duration.
	if env >= time.Duration(math.MaxInt64) {
		t.Fatalf("DefaultP95SaveLatencyEnvelope = %v, must be finite", env)
	}

	// Sanity: the envelope must be within a reasonable order of magnitude for
	// a local SQLite single-writer save (sub-second to low-single-digit seconds).
	// Anything above 60s would not meaningfully bound unbounded blocking.
	if env > 60*time.Second {
		t.Errorf("DefaultP95SaveLatencyEnvelope = %v, unreasonably large for a local SQLite bound (>60s)", env)
	}

	t.Logf("registered p95 envelope: %v", env)
}

// ---------------------------------------------------------------------------
// RED #2: Saturation-flood Test — p95 within envelope (ENFORCED gate)
// ---------------------------------------------------------------------------

// saturationFloodN is the standard saturation-flood writer count. The envelope
// is calibrated against this concurrency level (see latency.go rationale).
const saturationFloodN = 50

// TestSaturationFlood_P95WithinEnvelope is the ENFORCED gate for REQ-TX-002's
// defect-pin scenario. Unlike a Benchmark (which does not fail CI on assertion),
// this Test floods the UnitOfWork save path with saturationFloodN concurrent
// writers, samples per-save wall-clock latency, computes p95, and FAILS if p95
// exceeds the registered envelope. This pins that the prior unbounded blocking
// behavior is gone: an unbounded hang would inflate p95 to the busy_timeout
// (5s) or beyond, far exceeding the envelope.
func TestSaturationFlood_P95WithinEnvelope(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping saturation-flood latency test in -short mode")
	}

	db := setupProdLikeDB(t)
	uow := bundle.NewSQLiteUnitOfWork(db, domain.DefaultBusyRetryConfig())

	latencies := make([]float64, saturationFloodN)
	var completed int64
	var failed int64
	var wg sync.WaitGroup
	start := make(chan struct{})

	// Bounded context: if saves block unbounded, the test fails fast.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	for i := 0; i < saturationFloodN; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start
			ps := []*tableParticipant{
				{name: fmt.Sprintf("flood-%d", id), table: "obs_part"},
			}
			t0 := time.Now()
			err := uow.Do(ctx, nil, toParticipantSlice(ps), enlistAll(ps))
			latencies[id] = float64(time.Since(t0).Microseconds()) / 1000.0 // ms
			if err == nil {
				atomic.AddInt64(&completed, 1)
			} else {
				atomic.AddInt64(&failed, 1)
			}
		}(i)
	}

	done := make(chan struct{})
	go func() {
		close(start)
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// flood resolved — good
	case <-time.After(60 * time.Second):
		t.Fatalf("saturation flood did not resolve within 60s — UNBOUNDED BLOCKING (REQ-TX-002 defect)")
	}

	sort.Float64s(latencies)
	p50 := percentileMs(latencies, 0.50)
	p95 := percentileMs(latencies, 0.95)
	p99 := percentileMs(latencies, 0.99)
	maxLat := latencies[len(latencies)-1]

	envelopeMs := float64(bundle.DefaultP95SaveLatencyEnvelope.Microseconds()) / 1000.0

	t.Logf("saturation flood N=%d: completed=%d failed=%d", saturationFloodN, completed, failed)
	t.Logf("latency: p50=%.3fms p95=%.3fms p99=%.3fms max=%.3fms | envelope=%.3fms",
		p50, p95, p99, maxLat, envelopeMs)

	// ALL saves must have resolved (no goroutine leak).
	total := completed + failed
	if total != saturationFloodN {
		t.Errorf("flood resolved %d/%d saves — some goroutines leaked", total, saturationFloodN)
	}

	// ENFORCED assertion: p95 must stay within the registered envelope.
	if p95 > envelopeMs {
		t.Errorf("REQ-TX-002 DEFECT PIN FAILED: p95 save latency %.3fms exceeds registered envelope %.3fms (%v) — "+
			"unbounded blocking regression or envelope miscalibration",
			p95, envelopeMs, bundle.DefaultP95SaveLatencyEnvelope)
	}
}

// ---------------------------------------------------------------------------
// Benchmark (supplementary profiling — not an enforced gate)
// ---------------------------------------------------------------------------

// BenchmarkSaturationFlood_SaveLatency provides a profiling entry point for
// `go test -bench`. It floods the save path and reports per-operation
// allocation stats. The ENFORCED p95 bound lives in TestSaturationFlood_P95WithinEnvelope;
// this benchmark supplements it with ns/op and allocs/op for regression tracking.
func BenchmarkSaturationFlood_SaveLatency(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "bench.db")
	dsn := path + "?_pragma=busy_timeout=5000&_pragma=journal_mode=WAL"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		b.Fatalf("sql.Open: %v", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	b.Cleanup(func() { _ = db.Close() })

	if _, err := db.Exec("CREATE TABLE IF NOT EXISTS obs_part (id INTEGER PRIMARY KEY AUTOINCREMENT, val TEXT)"); err != nil {
		b.Fatalf("create table: %v", err)
	}

	uow := bundle.NewSQLiteUnitOfWork(db, domain.DefaultBusyRetryConfig())
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		ps := []*tableParticipant{
			{name: fmt.Sprintf("bench-%d", i), table: "obs_part"},
		}
		if err := uow.Do(ctx, nil, toParticipantSlice(ps), enlistAll(ps)); err != nil {
			b.Fatalf("Do: %v", err)
		}
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// setupProdLikeDB creates a FILE-BASED SQLite database with a PRODUCTION-LIKE
// DSN: busy_timeout=5s (driver handles short contention), WAL journal mode,
// foreign keys ON, single-writer connection pool (local-mode serialization).
// This mirrors the real local-mode deployment, unlike the adversarial
// busy_timeout=0 DSN used by W2.1's retry-cap tests.
func setupProdLikeDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "prodlike.db")
	dsn := path + "?_pragma=busy_timeout=5000&_pragma=foreign_keys=ON&_pragma=journal_mode=WAL"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	db.SetMaxOpenConns(1) // single-writer serialization (local mode)
	db.SetMaxIdleConns(1)
	for _, tbl := range participantTables {
		if _, err := db.Exec(fmt.Sprintf(
			"CREATE TABLE IF NOT EXISTS %s (id INTEGER PRIMARY KEY AUTOINCREMENT, val TEXT)", tbl,
		)); err != nil {
			t.Fatalf("create table %s: %v", tbl, err)
		}
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// percentileMs returns the p-th percentile from a SORTED slice of latencies
// (in milliseconds), using the nearest-rank method.
func percentileMs(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)) * p)
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
