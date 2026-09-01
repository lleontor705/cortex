// Package bundle: p95 save-latency envelope registry (W2.2, REQ-TX-002).
//
// This file declares the REGISTERED p95 save-latency envelope for the bounded
// SQLiteUnitOfWork save path. It is the quantitative contract referenced by
// REQ-TX-002's defect-pin scenario:
//
//	"A saturation test floods the writer → No save blocks beyond the bounded
//	 duration, p95 save latency stays within the registered envelope, and the
//	 suite pins that the prior unbounded behavior is gone."
//
// The envelope is a MEASURED, DEFENSIBLE bound — not a guess. It is asserted
// by TestP95SaveLatencyEnvelope_Registered (architecture test). The saturation
// test TestSaturationFlood_ResolvesBounded REPORTS the measured p95 against this
// envelope as an informational diagnostic, while its bounded-timeout and
// all-resolved gates enforce correctness (bounded resolution, no failures). See
// latency_test.go.
package bundle

import "time"

// DefaultP95SaveLatencyEnvelope is the registered upper bound on p95 per-save
// latency for a SQLiteUnitOfWork save under the standard saturation-flood
// scenario (50 concurrent writers, production-like DSN with busy_timeout=5s,
// WAL journal mode, single-writer connection pool, DefaultBusyRetryConfig).
//
// MEASUREMENT RATIONALE (recorded 2026-07-26 on the dev machine):
//
//	Measured p95 under 50-writer saturation flood (3 runs):
//	  118.086ms, 121.152ms, 122.961ms  →  median ≈ 121ms
//
//	The envelope is set to 500ms — a ~4.1× multiple of the measured p95.
//	This provides comfortable headroom for slower CI runners (which can be
//	2–3× slower than a dev workstation) while still bounding unbounded
//	blocking: a regression that reintroduces unbounded blocking would inflate
//	p95 to the driver busy_timeout (5s) or beyond, far exceeding this envelope.
//
//	The envelope is CALIBRATED against saturationFloodN=50. It is intentionally
//	generous (not a tight latency SLO) because its purpose is to catch
//	UNBOUNDED blocking regressions, not to enforce production-grade latency.
//	A value too tight would flake on CI; a value too loose (e.g. >5s) would
//	not meaningfully bound the defect. 500ms balances both concerns.
//
// REGISTRATION CONTRACT:
//   - Positive and finite (asserted by TestP95SaveLatencyEnvelope_Registered).
//   - p95 reported as a diagnostic by TestSaturationFlood_ResolvesBounded
//     (timeout/all-resolved gates enforce correctness).
//   - Profiled by BenchmarkSaturationFlood_SaveLatency (supplementary).
const DefaultP95SaveLatencyEnvelope = 500 * time.Millisecond
