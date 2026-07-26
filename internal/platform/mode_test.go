package platform

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lleontor705/cortex/internal/app"
)

// --- ParseMode tests ---

func TestParseModeDefaultsToLocal(t *testing.T) {
	mode, clean := ParseMode([]string{"cortex", "search", "foo"})
	if mode != ModeLocal {
		t.Errorf("ParseMode() mode = %q, want %q", mode, ModeLocal)
	}
	if len(clean) != 3 {
		t.Errorf("ParseMode() clean = %v, want 3 elements", clean)
	}
}

func TestParseModeExplicitLocal(t *testing.T) {
	mode, clean := ParseMode([]string{"cortex", "--mode", "local", "search", "foo"})
	if mode != ModeLocal {
		t.Errorf("ParseMode() mode = %q, want %q", mode, ModeLocal)
	}
	// "--mode" and "local" stripped → ["cortex", "search", "foo"]
	if len(clean) != 3 {
		t.Errorf("ParseMode() clean = %v, want 3 elements (flag stripped)", clean)
	}
}

func TestParseModeServer(t *testing.T) {
	mode, clean := ParseMode([]string{"cortex", "--mode", "server", "mcp"})
	if mode != ModeServer {
		t.Errorf("ParseMode() mode = %q, want %q", mode, ModeServer)
	}
	if len(clean) != 2 {
		t.Errorf("ParseMode() clean = %v, want 2 elements (flag stripped)", clean)
	}
}

func TestParseModeEqualsForm(t *testing.T) {
	mode, _ := ParseMode([]string{"cortex", "--mode=server"})
	if mode != ModeServer {
		t.Errorf("ParseMode() mode = %q, want %q", mode, ModeServer)
	}
}

func TestParseModeFlagPositionFlexible(t *testing.T) {
	// --mode can appear after the subcommand
	mode, clean := ParseMode([]string{"cortex", "search", "--mode", "server", "foo"})
	if mode != ModeServer {
		t.Errorf("ParseMode() mode = %q, want %q", mode, ModeServer)
	}
	if len(clean) != 3 {
		t.Errorf("ParseMode() clean = %v, want 3 elements", clean)
	}
}

// --- Select: local delegation ---

func TestSelectLocalDelegatesToAppOpen(t *testing.T) {
	ctx := context.Background()
	rt, err := Select(ModeLocal, ctx, app.Options{InMemory: true})
	if err != nil {
		t.Fatalf("Select(ModeLocal) error = %v", err)
	}
	defer func() { _ = rt.Close() }()

	if rt.App == nil {
		t.Fatal("Select(ModeLocal) returned nil App — did not delegate to app.Open")
	}
	if rt.App.Stores == nil {
		t.Fatal("Select(ModeLocal) returned nil Stores — app.Open not fully wired")
	}
	if rt.App.Stores.Observations == nil {
		t.Fatal("Select(ModeLocal) returned nil Observations store")
	}
	if rt.App.Config == nil {
		t.Fatal("Select(ModeLocal) returned nil Config")
	}
}

// --- Select: server inert ---

func TestSelectServerReturnsError(t *testing.T) {
	ctx := context.Background()
	_, err := Select(ModeServer, ctx, app.Options{InMemory: true})
	if err == nil {
		t.Fatal("Select(ModeServer) should return an error in W1")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "not yet implemented") {
		t.Errorf("Select(ModeServer) error = %q, want 'not yet implemented'", err.Error())
	}
}

func TestSelectServerReturnsNilRuntime(t *testing.T) {
	ctx := context.Background()
	rt, _ := Select(ModeServer, ctx, app.Options{InMemory: true})
	if rt != nil {
		t.Error("Select(ModeServer) should return nil Runtime in W1")
	}
}

func TestSelectServerDoesNotStartGoroutines(t *testing.T) {
	// Settle background goroutines
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	before := runtime.NumGoroutine()

	ctx := context.Background()
	_, _ = Select(ModeServer, ctx, app.Options{InMemory: true})

	// Allow any potential goroutine time to spin up
	time.Sleep(100 * time.Millisecond)
	runtime.GC()
	after := runtime.NumGoroutine()

	if after > before {
		t.Errorf("Select(ModeServer) started goroutines: before=%d after=%d", before, after)
	}
}

func TestSelectUnknownModeReturnsError(t *testing.T) {
	ctx := context.Background()
	_, err := Select(Mode("bogus"), ctx, app.Options{InMemory: true})
	if err == nil {
		t.Fatal("Select(unknown mode) should return an error")
	}
}

// --- Mode constants ---

func TestModeStringValues(t *testing.T) {
	if string(ModeLocal) != "local" {
		t.Errorf("ModeLocal = %q, want \"local\"", ModeLocal)
	}
	if string(ModeServer) != "server" {
		t.Errorf("ModeServer = %q, want \"server\"", ModeServer)
	}
}
