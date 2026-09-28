package platform

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/app"
)

// --- Triad enumeration ---

func TestModeTriadEnumeration(t *testing.T) {
	cases := []struct {
		mode Mode
		raw  string
	}{
		{ModeLocal, "local"},
		{ModeHybrid, "hybrid"},
		{ModeServer, "server"},
	}
	for _, tc := range cases {
		if string(tc.mode) != tc.raw {
			t.Errorf("mode %v = %q, want %q", tc.mode, string(tc.mode), tc.raw)
		}
		if !tc.mode.Valid() {
			t.Errorf("mode %q should be Valid()", tc.raw)
		}
	}

	for _, invalid := range []Mode{"", "legacy", "LOCAL", "remote"} {
		if invalid.Valid() {
			t.Errorf("mode %q should not be Valid()", invalid)
		}
	}
}

// --- ParseModeStrict: compatibility of the two syntactic forms ---

func TestParseModeDefaultsToLocal(t *testing.T) {
	mode, clean, err := ParseModeStrict([]string{"cortex", "search", "foo"})
	if err != nil {
		t.Fatalf("ParseModeStrict() error = %v, want nil", err)
	}
	if mode != ModeLocal {
		t.Errorf("ParseModeStrict() mode = %q, want %q", mode, ModeLocal)
	}
	if len(clean) != 3 {
		t.Errorf("ParseModeStrict() clean = %v, want 3 elements", clean)
	}
}

func TestParseModeAcceptsEveryTriadValue(t *testing.T) {
	for _, raw := range []string{"local", "hybrid", "server"} {
		spaceMode, spaceClean, err := ParseModeStrict([]string{"cortex", "--mode", raw, "search"})
		if err != nil {
			t.Fatalf("ParseModeStrict(--mode %s) error = %v", raw, err)
		}
		if string(spaceMode) != raw {
			t.Errorf("ParseModeStrict(--mode %s) mode = %q", raw, spaceMode)
		}
		if len(spaceClean) != 2 {
			t.Errorf("ParseModeStrict(--mode %s) clean = %v, want 2 elements (flag stripped)", raw, spaceClean)
		}

		eqMode, eqClean, err := ParseModeStrict([]string{"cortex", "--mode=" + raw})
		if err != nil {
			t.Fatalf("ParseModeStrict(--mode=%s) error = %v", raw, err)
		}
		if eqMode != spaceMode {
			t.Errorf("ParseModeStrict(--mode=%s) = %q, want %q", raw, eqMode, spaceMode)
		}
		if len(eqClean) != 1 {
			t.Errorf("ParseModeStrict(--mode=%s) clean = %v, want 1 element", raw, eqClean)
		}
	}
}

func TestParseModeFlagPositionFlexible(t *testing.T) {
	mode, clean, err := ParseModeStrict([]string{"cortex", "search", "--mode", "hybrid", "foo"})
	if err != nil {
		t.Fatalf("ParseModeStrict() error = %v", err)
	}
	if mode != ModeHybrid {
		t.Errorf("ParseModeStrict() mode = %q, want %q", mode, ModeHybrid)
	}
	if len(clean) != 3 {
		t.Errorf("ParseModeStrict() clean = %v, want 3 elements", clean)
	}
}

// --- ParseModeStrict: fail-closed unknown values ---

func TestParseModeUnknownModeFailsClosed(t *testing.T) {
	for _, args := range [][]string{
		{"cortex", "--mode", "legacy", "serve"},
		{"cortex", "--mode=legacy"},
	} {
		mode, clean, err := ParseModeStrict(args)
		if err == nil {
			t.Fatalf("ParseModeStrict(%v) error = nil, want fail-closed error", args)
		}
		if mode != Mode("legacy") {
			t.Errorf("ParseModeStrict(%v) mode = %q, want the raw rejected value", args, mode)
		}
		lower := strings.ToLower(err.Error())
		for _, want := range []string{"local", "hybrid", "server"} {
			if !strings.Contains(lower, want) {
				t.Errorf("ParseModeStrict(%v) error = %q, must enumerate %q", args, err, want)
			}
		}
		if len(clean) != len(args) {
			t.Errorf("ParseModeStrict(%v) returned mutated args %v on error", args, clean)
		}
	}
}

// --- ParseMode: cmd/cortex two-value call contract ---

func TestParseModeCompatAcceptsTriad(t *testing.T) {
	for _, raw := range []string{"local", "hybrid", "server"} {
		mode, _ := ParseMode([]string{"cortex", "--mode=" + raw})
		if string(mode) != raw {
			t.Errorf("ParseMode(--mode=%s) = %q", raw, mode)
		}
	}
}

func TestParseModeCompatRejectsUnknownForConsumer(t *testing.T) {
	mode, clean := ParseMode([]string{"cortex", "--mode", "legacy", "serve"})
	if mode.Valid() {
		t.Fatalf("ParseMode(unknown) = %q, want an invalid mode the consumer switch rejects", mode)
	}
	if len(clean) != 4 {
		t.Errorf("ParseMode(unknown) args = %v, want unchanged", clean)
	}
}

// --- Select: local/hybrid delegation ---

func TestSelectLocalDelegatesToAppOpen(t *testing.T) {
	rt, err := Select(ModeLocal, context.Background(), app.Options{InMemory: true})
	if err != nil {
		t.Fatalf("Select(ModeLocal) error = %v", err)
	}
	defer func() { _ = rt.Close() }()

	if rt.App == nil || rt.App.Stores == nil || rt.App.Config == nil {
		t.Fatal("Select(ModeLocal) did not fully wire app.Open")
	}
	if rt.App.Stores.Observations == nil {
		t.Fatal("Select(ModeLocal) returned nil Observations store")
	}
}

func TestSelectHybridUsesLocalComposition(t *testing.T) {
	rt, err := Select(ModeHybrid, context.Background(), app.Options{InMemory: true, DisableRemoteSync: true})
	if err != nil {
		t.Fatalf("Select(ModeHybrid) error = %v", err)
	}
	defer func() { _ = rt.Close() }()

	if rt.App == nil {
		t.Fatal("Select(ModeHybrid) must reuse the local SQLite composition")
	}
}

// --- Select: server boundary ---

func TestSelectServerReturnsError(t *testing.T) {
	rt, err := Select(ModeServer, context.Background(), app.Options{InMemory: true})
	if err == nil {
		t.Fatal("Select(ModeServer) should preserve the local/server boundary")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "cmd/cortex") {
		t.Errorf("Select(ModeServer) error = %q, want cmd/cortex bridge guidance", err.Error())
	}
	if rt != nil {
		t.Error("Select(ModeServer) should return nil local Runtime")
	}
}

func TestSelectUnknownModeReturnsError(t *testing.T) {
	_, err := Select(Mode("bogus"), context.Background(), app.Options{InMemory: true})
	if err == nil {
		t.Fatal("Select(unknown mode) should return an error")
	}
}

func TestSelectServerDoesNotStartGoroutines(t *testing.T) {
	var before, after int
	for attempt := 0; attempt < 5; attempt++ {
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
		before = runtime.NumGoroutine()

		_, _ = Select(ModeServer, context.Background(), app.Options{InMemory: true})

		time.Sleep(20 * time.Millisecond)
		runtime.GC()
		after = runtime.NumGoroutine()
		if after <= before {
			return
		}
	}
	if after > before {
		t.Errorf("Select(ModeServer) started goroutines: before=%d after=%d", before, after)
	}
}
