package postgrestest

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

type diagnosticFailConn struct{}

func (diagnosticFailConn) Query(context.Context, string, ...any) (Rows, error) {
	return nil, fmt.Errorf("sampler failed")
}
func (diagnosticFailConn) Close(context.Context) error { return nil }

func withObserverEnv(t *testing.T, enabled bool) {
	t.Helper()
	old, ok := lookupEnv("CORTEX_C32_OBSERVER")
	if enabled {
		_ = setEnv("CORTEX_C32_OBSERVER", "1")
	} else {
		_ = unsetEnv("CORTEX_C32_OBSERVER")
	}
	t.Cleanup(func() {
		if ok {
			_ = setEnv("CORTEX_C32_OBSERVER", old)
		} else {
			_ = unsetEnv("CORTEX_C32_OBSERVER")
		}
	})
}

// Small indirections keep the test portable while avoiding a second observer
// implementation in the focused diagnostic tests.
var lookupEnv = os.LookupEnv
var setEnv = os.Setenv
var unsetEnv = os.Unsetenv

func TestDiagnosticOptOutIsNoOp(t *testing.T) {
	withObserverEnv(t, false)
	d := NewDiagnostic(Config{RunPrefix: "secret/run"})
	if err := d.Start(context.Background(), nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := d.Stop(); got.Identity != "" || got.Status != "" {
		t.Fatalf("opt-out emitted diagnostic: %+v", got)
	}
}

func TestDiagnosticOverheadBoundaryBlocksBeforeSampling(t *testing.T) {
	withObserverEnv(t, true)
	d := NewDiagnostic(Config{RunPrefix: "run_"})
	if err := d.Start(context.Background(), []time.Duration{100, 100}, []time.Duration{106, 106}); err != nil {
		t.Fatal(err)
	}
	r := d.Stop()
	if r.Status != DiagnosticBlocked || r.Complete || len(r.Samples) != 0 {
		t.Fatalf("overhead gate not fail-closed: %+v", r)
	}
}

func TestDiagnosticIncompleteTelemetryAndPhaseOrder(t *testing.T) {
	withObserverEnv(t, true)
	d := NewDiagnostic(Config{RunPrefix: "run_"})
	for _, p := range []Phase{PhasePath, PhaseRepetition, PhaseBlock, PhasePopulation, PhaseCold, PhaseWarm, PhaseMeasured} {
		d.RecordPhase(p)
	}
	if err := d.Start(context.Background(), []time.Duration{time.Millisecond}, nil); err != nil {
		t.Fatal(err)
	}
	r := d.Stop()
	if r.Status != DiagnosticBlocked || r.Complete || len(r.PhaseEvents) != 7 {
		t.Fatalf("incomplete telemetry did not fail closed: %+v", r)
	}
	for i, e := range r.PhaseEvents {
		if e.Phase != []Phase{PhasePath, PhaseRepetition, PhaseBlock, PhasePopulation, PhaseCold, PhaseWarm, PhaseMeasured}[i] {
			t.Fatalf("phase order=%v", r.PhaseEvents)
		}
	}
}

func TestDiagnosticReportEmitsExactlyOnce(t *testing.T) {
	withObserverEnv(t, true)
	d := NewDiagnostic(Config{RunPrefix: "run/secret"})
	_ = d.Start(context.Background(), []time.Duration{100}, []time.Duration{106})
	var lines []string
	if err := d.EmitReport(func(s string) { lines = append(lines, s) }); err != nil {
		t.Fatal(err)
	}
	if err := d.EmitReport(func(s string) { lines = append(lines, s) }); err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "C32_OBSERVER_REPORT ") {
		t.Fatalf("emission=%q", lines)
	}
	if strings.Contains(lines[0], "run/secret") {
		t.Fatalf("unsanitized identity: %s", lines[0])
	}
}

func TestDiagnosticWaitFirstSampleReturnsOnSamplerFailure(t *testing.T) {
	withObserverEnv(t, true)
	d := NewDiagnostic(Config{RunPrefix: "run_", Pooler: func(context.Context) (QueryConn, error) {
		return diagnosticFailConn{}, nil
	}})
	if err := d.Start(context.Background(), []time.Duration{time.Millisecond}, nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if d.WaitFirstSample(ctx) {
		t.Fatal("failed sampler reported a first sample")
	}
	_ = d.Stop()
}

func TestDiagnosticWaitFirstSampleHonorsTimeout(t *testing.T) {
	withObserverEnv(t, true)
	d := NewDiagnostic(Config{RunPrefix: "run_", Pooler: func(context.Context) (QueryConn, error) {
		time.Sleep(100 * time.Millisecond)
		return diagnosticFailConn{}, nil
	}})
	if err := d.Start(context.Background(), []time.Duration{time.Millisecond}, nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if d.WaitFirstSample(ctx) {
		t.Fatal("timed out sampler reported a first sample")
	}
	_ = d.Stop()
}

func TestDiagnosticWaitFirstSampleReturnsOnSuccess(t *testing.T) {
	withObserverEnv(t, true)
	d := NewDiagnostic(Config{RunPrefix: "run_", Pooler: func(context.Context) (QueryConn, error) {
		return &lifecycleConn{}, nil
	}, Postgres: func(context.Context) (QueryConn, error) { return &lifecycleConn{}, nil }})
	if err := d.Start(context.Background(), []time.Duration{time.Millisecond}, nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if !d.WaitFirstSample(ctx) {
		t.Fatal("successful sampler did not report a first sample")
	}
	_ = d.Stop()
}
