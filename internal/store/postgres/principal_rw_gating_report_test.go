//go:build postgres_integration

package postgres

import (
	"encoding/json"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// rwC32ReportFinalizer is the only report emission path.  Setup and runtime
// failures may call t.Fatal, but the deferred finalizer still emits the
// complete retained record exactly once.
type rwC32ReportFinalizer struct {
	once     sync.Once
	report   rwC32MachineReport
	observer *rwC32ObserverRun
}

// rwC32ParentProtocolStatus is the fail-closed gate immediately before the
// parent report is retained. No usable samples are BLOCKED; partial evidence
// or recorded setup/flow failures are FAIL.
func rwC32ParentProtocolStatus(blocks []rwC32MachineBlock, counts rwR1R24Counts, countsErr error, failures []string, current rwC32Status) rwC32Status {
	if counts.Global == 0 {
		return rwC32Blocked
	}
	if countsErr != nil || len(blocks) == 0 || len(failures) != 0 {
		return rwC32Fail
	}
	return current
}

// newRWC32BlockedReport is also the defensive report retained before any
// setup call.  testing.T.Fatal runs deferred cleanup, so every failure path
// must start with a complete typed record rather than relying on a later Set.
func newRWC32BlockedReport(reason string) rwC32MachineReport {
	return rwC32MachineReport{
		Source:         "principal-rw-gating",
		Status:         rwC32Blocked,
		Path:           "setup",
		Repetition:     0,
		Repetitions:    rwC32Reps,
		Order:          []string{"direct", "pooler"},
		QuantileMethod: "nearest-rank (p95=ceil(0.95*n))",
		Preflight: rwC32PreflightReport{
			Eligible: false, Reason: reason, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
			GoVersion: runtime.Version(), GOTOOLCHAIN: os.Getenv("GOTOOLCHAIN"),
			PoolMode: "transaction", PoolCapacity: rwC32Workers,
			CPUUtilization: []float64{}, RTTSamples: []time.Duration{},
			Thresholds: rwC32ThresholdReport{RatioFloor: rwC32RatioFloor, P95Budget: rwC32P95Budget,
				MaxRTT: rwC32MaxRTT, MaxSessions: rwC32MaxSessions, MaxConnections: rwC32MaxConnections},
		},
		ProtocolVerdict: protocolPerformanceVerdict{
			Status: rwC32Blocked, Reason: "protocol samples unavailable: " + reason,
			SamplesRetained: rwC32Blocked, NoAuthFailures: rwC32Blocked, NoLockWaits: rwC32Blocked,
			RatioWithinFloor: rwC32Blocked, TailWithinFactor: rwC32Blocked,
		},
		AbsoluteHostVerdict: absoluteHostFloorVerdict{
			Status: rwC32Blocked, Reason: "absolute host floor unavailable: " + reason,
			Eligibility: rwC32Blocked, P95WithinFloor: rwC32Blocked,
		},
		Cleanup:           rwC32CleanupReport{Completed: true, Postflight: []rwC32PostflightReport{}},
		RepetitionReports: []rwR1R24RepetitionReport{},
		Reasons:           []string{reason},
		Blocks:            []rwC32MachineBlock{},
	}
}

func (f *rwC32ReportFinalizer) Set(report rwC32MachineReport) { f.report = report }

func (f *rwC32ReportFinalizer) Finalize(t *testing.T) {
	t.Helper()
	if f.observer != nil {
		defer func() { _ = f.observer.Emit(func(s string) { t.Log(s) }) }()
	}
	f.once.Do(func() { logC32MachineReport(t, f.report) })
	if f.report.ProtocolVerdict.Status != rwC32Pass ||
		(f.report.Preflight.Eligible && f.report.AbsoluteHostVerdict.Status != rwC32Pass) {
		t.Fatalf("C32 verdict failed after final report: protocol=%s absolute=%s preflight_eligible=%t protocol_reason=%q absolute_reason=%q", f.report.ProtocolVerdict.Status, f.report.AbsoluteHostVerdict.Status, f.report.Preflight.Eligible, f.report.ProtocolVerdict.Reason, f.report.AbsoluteHostVerdict.Reason)
	}
}

func TestRWC32FinalReportGateIsFailClosed(t *testing.T) {
	tests := []struct {
		name     string
		protocol rwC32Status
		absolute rwC32Status
		eligible bool
		wantFail bool
	}{
		{name: "typed protocol fail without narrative failures", protocol: rwC32Fail, absolute: rwC32Blocked, wantFail: true},
		{name: "eligible absolute fail", protocol: rwC32Pass, absolute: rwC32Fail, eligible: true, wantFail: true},
		{name: "protocol and absolute pass", protocol: rwC32Pass, absolute: rwC32Pass, eligible: true, wantFail: false},
		{name: "blocked preflight remains non-pass", protocol: rwC32Blocked, absolute: rwC32Blocked, wantFail: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := rwC32MachineReport{
				ProtocolVerdict:     protocolPerformanceVerdict{Status: tt.protocol},
				AbsoluteHostVerdict: absoluteHostFloorVerdict{Status: tt.absolute},
				Preflight:           rwC32PreflightReport{Eligible: tt.eligible},
			}
			gotFail := report.ProtocolVerdict.Status != rwC32Pass ||
				(report.Preflight.Eligible && report.AbsoluteHostVerdict.Status != rwC32Pass)
			if gotFail != tt.wantFail {
				t.Fatalf("final report gate failure=%t, want %t", gotFail, tt.wantFail)
			}
		})
	}
}

func TestRWC32FinalReportExitGate(t *testing.T) {
	caseName := os.Getenv("RWC32_FINAL_REPORT_CASE")
	if caseName != "" {
		report := rwC32MachineReport{ProtocolVerdict: protocolPerformanceVerdict{Status: rwC32Pass}, AbsoluteHostVerdict: absoluteHostFloorVerdict{Status: rwC32Pass}, Preflight: rwC32PreflightReport{Eligible: true}}
		switch caseName {
		case "protocol-fail":
			report.ProtocolVerdict.Status = rwC32Fail
		case "absolute-fail":
			report.AbsoluteHostVerdict.Status = rwC32Fail
		case "blocked":
			report.ProtocolVerdict.Status = rwC32Blocked
			report.AbsoluteHostVerdict.Status = rwC32Blocked
			report.Preflight.Eligible = false
		case "pass":
		default:
			t.Fatalf("unknown final report case %q", caseName)
		}
		(&rwC32ReportFinalizer{report: report}).Finalize(t)
		return
	}
	for _, tc := range []struct {
		name string
		fail bool
	}{
		{name: "protocol-fail", fail: true}, {name: "absolute-fail", fail: true},
		{name: "pass", fail: false}, {name: "blocked", fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestRWC32FinalReportExitGate$", "-test.v")
			cmd.Env = append(os.Environ(), "RWC32_FINAL_REPORT_CASE="+tc.name)
			raw, err := cmd.CombinedOutput()
			if (err != nil) != tc.fail {
				t.Fatalf("final report case %s exit error=%v, want failure=%t: %s", tc.name, err, tc.fail, raw)
			}
			if strings.Count(string(raw), "C32_MACHINE_REPORT") != 1 {
				t.Fatalf("final report case %s emitted more than one report: %s", tc.name, raw)
			}
		})
	}
}

func TestRWC32ReportUsesAbsoluteTypedStatuses(t *testing.T) {
	report := rwC32MachineReport{
		Source:              "principal-rw-gating",
		Status:              rwC32Blocked,
		ProtocolVerdict:     protocolPerformanceVerdict{Status: rwC32Fail, SamplesRetained: rwC32Fail},
		AbsoluteHostVerdict: absoluteHostFloorVerdict{Status: rwC32Blocked, Eligibility: rwC32Blocked},
		Reasons:             []string{"dedicated host unavailable"},
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, `"status":"BLOCKED"`) || !strings.Contains(text, `"protocol_performance_verdict"`) {
		t.Fatalf("typed report status missing: %s", text)
	}
	if strings.Contains(text, `"pass"`) {
		t.Fatalf("report must not encode inferred pass booleans: %s", text)
	}
}

func TestRWC32BlockedReportPropagatesMaxRTTThreshold(t *testing.T) {
	report := newRWC32BlockedReport("historical setup failure")
	if report.Preflight.Thresholds.MaxRTT != 12*time.Millisecond {
		t.Fatalf("machine report MaxRTT=%v, want 12ms", report.Preflight.Thresholds.MaxRTT)
	}
}

func TestRWC32StatusCannotPassIncompleteSamples(t *testing.T) {
	protocol, absolute := rwC32Verdicts(rwC32Stats{TPS: 100, P95: 1}, rwC32Stats{TPS: 100, P95: 1}, rwC32PreflightResult{Eligible: true})
	if protocol.Status == rwC32Pass || absolute.Status != rwC32Blocked {
		t.Fatalf("incomplete run passed: protocol=%+v absolute=%+v", protocol, absolute)
	}
}

func TestR1R25ReportRetainsExactlyOneTypedEmissionShape(t *testing.T) {
	report := rwC32MachineReport{Source: "principal-rw-gating", Status: rwC32Pass, Repetitions: rwC32Reps,
		ProtocolVerdict: protocolPerformanceVerdict{Status: rwC32Pass}, AbsoluteHostVerdict: absoluteHostFloorVerdict{Status: rwC32Pass}}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["source"] != "principal-rw-gating" || decoded["status"] != string(rwC32Pass) || decoded["repetitions"] != float64(rwC32Reps) {
		t.Fatalf("report emission lost normative identity: %v", decoded)
	}
	if _, ok := decoded["protocol_performance_verdict"]; !ok {
		t.Fatal("report emission omitted protocol verdict")
	}
}

// TestR1R24ReportFinalizerEmitsExactlyOnce exercises the real logger seam in
// a child test process.  Calling Finalize twice must not duplicate the machine
// record; using a subprocess makes the count observable without a fake
// testing.T or a disconnected logger.
func TestR1R24ReportFinalizerEmitsExactlyOnce(t *testing.T) {
	if os.Getenv("RWC32_REPORT_FINALIZER_CHILD") == "1" {
		finalizer := rwC32ReportFinalizer{report: newRWC32BlockedReport("setup or preflight terminated before a final verdict")}
		finalizer.Set(rwC32MachineReport{Source: "principal-rw-gating", Status: rwC32Pass,
			ProtocolVerdict:     protocolPerformanceVerdict{Status: rwC32Pass},
			AbsoluteHostVerdict: absoluteHostFloorVerdict{Status: rwC32Pass},
			Preflight:           rwC32PreflightReport{Eligible: true},
			Cleanup:             rwC32CleanupReport{Completed: true}})
		finalizer.Finalize(t)
		finalizer.Finalize(t)
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestR1R24ReportFinalizerEmitsExactlyOnce$", "-test.v")
	cmd.Env = append(os.Environ(), "RWC32_REPORT_FINALIZER_CHILD=1")
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("finalizer child failed: %v", err)
	}
	output := string(raw)
	if got := strings.Count(output, "C32_MACHINE_REPORT"); got != 1 {
		t.Fatalf("machine report emission count=%d, want exactly one", got)
	}
	if !strings.Contains(output, `"source":"principal-rw-gating"`) || !strings.Contains(output, `"cleanup":{"completed":true`) {
		t.Fatalf("child emission was not a complete typed report: %s", output)
	}
}

func TestRWC32ParentAggregationEmitsOneNonPassReportForInvalidBlocks(t *testing.T) {
	if os.Getenv("RWC32_PARENT_AGGREGATION_CHILD") == "1" {
		cases := [][]rwC32MachineBlock{nil}
		for _, blocks := range cases {
			counts, err := rwR1R24EvidenceCounts(blocks, 2, rwC32Reps)
			finalizer := rwC32ReportFinalizer{report: newRWC32BlockedReport("parent aggregation rejected retained evidence")}
			status := rwC32ParentProtocolStatus(blocks, counts, err, nil, rwC32Pass)
			finalizer.Set(rwC32MachineReport{Source: "principal-rw-gating", Status: status,
				ProtocolVerdict:     protocolPerformanceVerdict{Status: status},
				AbsoluteHostVerdict: absoluteHostFloorVerdict{Status: rwC32Blocked},
				Cleanup:             rwC32CleanupReport{Completed: true}})
			finalizer.Finalize(t)
		}
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestRWC32ParentAggregationEmitsOneNonPassReportForInvalidBlocks$", "-test.v")
	cmd.Env = append(os.Environ(), "RWC32_PARENT_AGGREGATION_CHILD=1")
	raw, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("parent aggregation child unexpectedly passed")
	}
	output := string(raw)
	if got := strings.Count(output, "C32_MACHINE_REPORT"); got != 1 {
		t.Fatalf("parent aggregation emitted %d reports, want exactly one", got)
	}
	if strings.Contains(output, `"status":"PASS"`) {
		t.Fatalf("invalid parent aggregation emitted PASS: %s", output)
	}
}

// The normative runner must still publish a complete BLOCKED record when
// setup/preflight exits through testing.T.Fatal.  This is intentionally an
// integration-level subprocess check: isolated JSON construction cannot catch
// a deferred finalizer retaining its zero-value report.
func TestR1R24NormativeSetupFailureEmitsCompleteReport(t *testing.T) {
	if os.Getenv("RWC32_NORMATIVE_FAILURE_CHILD") == "1" {
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestPrincipalRWFullFlowThroughputC32$", "-test.v")
	env := make([]string, 0, len(os.Environ()))
	for _, item := range os.Environ() {
		if strings.HasPrefix(item, "CORTEX_SPIKE_PG_") || strings.HasPrefix(item, "CORTEX_C32_") {
			continue
		}
		env = append(env, item)
	}
	env = append(env, "RWC32_NORMATIVE_FAILURE_CHILD=1")
	cmd.Env = env
	raw, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("normative setup-failure child unexpectedly passed")
	}
	output := string(raw)
	if got := strings.Count(output, "C32_MACHINE_REPORT"); got != 1 {
		t.Fatalf("normative machine report emission count=%d, want exactly one", got)
	}
	line := output[strings.Index(output, "C32_MACHINE_REPORT"):]
	if !strings.Contains(line, `"source":"principal-rw-gating"`) ||
		!strings.Contains(line, `"protocol_performance_verdict"`) ||
		!strings.Contains(line, `"absolute_host_floor_verdict"`) ||
		!strings.Contains(line, `"cleanup":{"completed":true`) ||
		!strings.Contains(line, `"goos":"`+runtime.GOOS+`"`) ||
		!strings.Contains(line, `"goarch":"`+runtime.GOARCH+`"`) ||
		!strings.Contains(line, `"go_version":"`+runtime.Version()+`"`) {
		t.Fatalf("normative setup failure emitted incomplete report: %s", line)
	}
}
