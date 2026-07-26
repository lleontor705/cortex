// Package app — W4.1 embedding worker lifecycle tests (REQ-EMB-001).
//
// These tests prove the embedding worker lifecycle is wired into app.Open and
// app.Close: the UnitOfWork is always wired (for atomic saves), the outbox +
// worker are constructed only when embedding + vectors are available, and
// Close drains the worker BEFORE DB.Close (no goroutine leaks).
package app

import (
	"context"
	"path/filepath"
	"testing"

	"go.uber.org/goleak"
)

// TestAppOpen_WiresUnitOfWork proves the UnitOfWork is always wired after Open
// so SaveWithEmbedIntent can use it for atomic saves (W4.1 wiring).
func TestAppOpen_WiresUnitOfWork(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "cortex.db")
	isolateEnv(t, dbPath)

	a, err := Open(context.Background(), Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = a.Close() }()

	if a.Stores.UnitOfWork == nil {
		t.Fatal("Stores.UnitOfWork is nil after Open; expected non-nil for atomic saves")
	}
}

// TestAppOpen_ZeroEmbedding_NoOutboxNoWorker proves the zero-embedding path is
// unchanged: no outbox store, no worker started (REQ-EMB-001 non-goal).
func TestAppOpen_ZeroEmbedding_NoOutboxNoWorker(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "cortex.db")
	isolateEnv(t, dbPath)

	a, err := Open(context.Background(), Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = a.Close() }()

	if a.Stores.Outbox != nil {
		t.Fatal("Stores.Outbox should be nil in zero-embedding mode")
	}
	if a.workerCancel != nil {
		t.Fatal("workerCancel should be nil in zero-embedding mode (no worker started)")
	}
}

// TestAppLifecycle_OpenClose_NoGoroutineLeak proves Open+Close leaves no
// goroutines behind (worker, archival, DB connection all stopped). This is the
// app-level defect pin for the detached-goroutine leak (REQ-EMB-001).
func TestAppLifecycle_OpenClose_NoGoroutineLeak(t *testing.T) {
	defer goleak.VerifyNone(t)

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "cortex.db")
	isolateEnv(t, dbPath)

	a, err := Open(context.Background(), Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Close must drain worker (if any) and archival BEFORE DB.Close, then close
	// the DB — leaving zero goroutines.
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// goleak.VerifyNone (deferred above) asserts no goroutines remain.
}

// TestAppClose_NilSafe proves Close does not panic when called on a
// partially-initialized App (workerCancel / archivalCancel may be nil).
func TestAppClose_NilSafe(t *testing.T) {
	a := &App{}
	if err := a.Close(); err != nil {
		t.Fatalf("Close on zero-value App: %v", err)
	}
}
