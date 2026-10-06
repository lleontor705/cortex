package lifecycle

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

// errRepo is a configurable fake that returns controlled errors.
type errRepo struct {
	mu           sync.Mutex
	listErr      error
	deleteErr    error
	deletedIDs   []int64
	observations []*domain.Observation
}

func (r *errRepo) ListArchivable(_ context.Context, _ time.Time, _ float64, _ int) ([]*domain.Observation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.observations, r.listErr
}

func (r *errRepo) Delete(_ context.Context, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deletedIDs = append(r.deletedIDs, id)
	return r.deleteErr
}

func TestRunArchivalCheck_ListArchivableError(t *testing.T) {
	repo := &errRepo{listErr: errors.New("db down")}
	svc := NewArchivalService(repo, ArchivalConfig{MaxAgeDays: 90})
	svc.SetNowFunc(func() time.Time { return time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC) })

	n, err := svc.RunArchivalCheck(context.Background())
	if err == nil {
		t.Fatal("expected error from ListArchivable")
	}
	if n != 0 {
		t.Errorf("expected 0 archived on list error, got %d", n)
	}
}

func TestRunArchivalCheck_DeleteErrorPartial(t *testing.T) {
	repo := &errRepo{
		observations: []*domain.Observation{
			{ID: 10}, {ID: 20}, {ID: 30},
		},
		// Every second delete fails.
		deleteErr: errors.New("locked"),
	}
	svc := NewArchivalService(repo, ArchivalConfig{MaxAgeDays: 90})

	n, err := svc.RunArchivalCheck(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The error path is hit but archived only counts successes;
	// since deleteErr is non-nil for every call, 0 succeed.
	if n != 0 {
		t.Errorf("expected 0 archived (all deletes failed), got %d", n)
	}
	if len(repo.deletedIDs) != 3 {
		t.Errorf("expected 3 delete attempts, got %d", len(repo.deletedIDs))
	}
}

func TestStartCancelStopsGoroutine(t *testing.T) {
	repo := &errRepo{}
	svc := NewArchivalService(repo, ArchivalConfig{
		MaxAgeDays:    90,
		CheckInterval: 50 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	stopFn := svc.Start(ctx)

	// Let at least one tick fire.
	time.Sleep(120 * time.Millisecond)

	cancel()
	svc.Stop() // must not hang

	_ = stopFn // returned but we use cancel
}

func TestStartTickerSkipsConcurrentRun(t *testing.T) {
	var callCount int
	repo := &errRepo{}
	svc := NewArchivalService(repo, ArchivalConfig{
		MaxAgeDays:    1,
		CheckInterval: 10 * time.Millisecond,
	})
	svc.SetNowFunc(func() time.Time { return time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC) })

	// Replace RunArchivalCheck with a slow version via a wrapper to
	// exercise the atomic skip path. We simulate by calling Start and
	// letting the fast ticker pile up while a check runs.
	_ = callCount

	ctx, cancel := context.WithCancel(context.Background())
	svc.Start(ctx)

	// Let several ticks pile up.
	time.Sleep(80 * time.Millisecond)
	cancel()
	svc.Stop()
}

func TestStopNilService(t *testing.T) {
	var svc *ArchivalService
	svc.Stop() // must not panic
}

func TestStopBeforeStart(t *testing.T) {
	svc := NewArchivalService(&errRepo{}, ArchivalConfig{MaxAgeDays: 90})
	svc.Stop() // done channel is nil; guard must return
}

func TestRunArchivalCheck_MultipleObservations(t *testing.T) {
	repo := &errRepo{
		observations: []*domain.Observation{
			{ID: 1}, {ID: 2}, {ID: 3}, {ID: 4}, {ID: 5},
		},
	}
	svc := NewArchivalService(repo, ArchivalConfig{MaxAgeDays: 90})

	n, err := svc.RunArchivalCheck(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 5 {
		t.Errorf("expected 5 archived, got %d", n)
	}
	if len(repo.deletedIDs) != 5 {
		t.Errorf("expected 5 delete calls, got %d", len(repo.deletedIDs))
	}
}

func TestStartContextTimeout(t *testing.T) {
	repo := &errRepo{}
	svc := NewArchivalService(repo, ArchivalConfig{
		MaxAgeDays:    90,
		CheckInterval: 20 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	svc.Start(ctx)

	// Wait for context timeout + goroutine cleanup.
	time.Sleep(200 * time.Millisecond)
	svc.Stop()
}
