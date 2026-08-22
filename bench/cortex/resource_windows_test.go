//go:build windows

package cortex

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// fakeWindowsAPI implements windowsProcessAPI for deterministic testing without
// real OS handles.
type fakeWindowsAPI struct {
	mu                sync.Mutex
	openedHandles     []windows.Handle
	closedHandles     []windows.Handle
	nextHandle        windows.Handle
	openProcessErr    error
	processTimesErr   error
	processMemoryErr  error
	kernelNanoseconds int64
	userNanoseconds   int64
	peakWorkingSet    uintptr
}

func (f *fakeWindowsAPI) OpenProcess(access uint32, inherit bool, pid uint32) (windows.Handle, error) {
	if f.openProcessErr != nil {
		return 0, f.openProcessErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextHandle++
	h := windows.Handle(0x1000 + int(f.nextHandle))
	f.openedHandles = append(f.openedHandles, h)
	return h, nil
}

func (f *fakeWindowsAPI) GetProcessTimes(handle windows.Handle, creation, exit, kernel, user *windows.Filetime) error {
	if f.processTimesErr != nil {
		return f.processTimesErr
	}
	// Process times are elapsed durations stored as FILETIME (100ns intervals),
	// not absolute timestamps. Set raw fields directly to avoid epoch conversion.
	kernel100ns := f.kernelNanoseconds / 100
	user100ns := f.userNanoseconds / 100
	*kernel = windows.Filetime{
		LowDateTime:  uint32(kernel100ns & 0xffffffff),
		HighDateTime: uint32(kernel100ns >> 32),
	}
	*user = windows.Filetime{
		LowDateTime:  uint32(user100ns & 0xffffffff),
		HighDateTime: uint32(user100ns >> 32),
	}
	return nil
}

func (f *fakeWindowsAPI) GetProcessMemoryInfo(handle windows.Handle) (processMemoryCounters, error) {
	if f.processMemoryErr != nil {
		return processMemoryCounters{}, f.processMemoryErr
	}
	return processMemoryCounters{
		peakWorkingSetSize: f.peakWorkingSet,
	}, nil
}

func (f *fakeWindowsAPI) CloseHandle(handle windows.Handle) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closedHandles = append(f.closedHandles, handle)
	return nil
}

func (f *fakeWindowsAPI) openedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.openedHandles)
}

func (f *fakeWindowsAPI) closedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.closedHandles)
}

func (f *fakeWindowsAPI) allHandlesClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.openedHandles) != len(f.closedHandles) {
		return false
	}
	closedSet := make(map[windows.Handle]bool, len(f.closedHandles))
	for _, h := range f.closedHandles {
		closedSet[h] = true
	}
	for _, h := range f.openedHandles {
		if !closedSet[h] {
			return false
		}
	}
	return true
}

func newTestWindowsCollector(api *fakeWindowsAPI) *windowsResourceCollector {
	return newWindowsResourceCollectorWithAPI(api)
}

func TestWindowsResourceCollectorIdentity(t *testing.T) {
	api := &fakeWindowsAPI{
		kernelNanoseconds: 1_000_000,
		userNanoseconds:   2_000_000,
		peakWorkingSet:    4096,
	}
	collector := newTestWindowsCollector(api)
	if err := collector.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	resources, err := collector.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	wantIdentity := ResourceCollectorIdentity{
		Method:  "windows-process-api",
		Version: "v1",
	}
	if resources.Collector != wantIdentity {
		t.Errorf("Collector = %+v, want %+v", resources.Collector, wantIdentity)
	}
}

func TestWindowsResourceCollectorLifecycle(t *testing.T) {
	api := &fakeWindowsAPI{}
	collector := newTestWindowsCollector(api)

	if _, err := collector.Snapshot(context.Background()); err == nil {
		t.Fatal("Snapshot() before Start() error = nil, want lifecycle error")
	}

	if err := collector.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if err := collector.Start(context.Background()); err == nil {
		t.Fatal("second Start() error = nil, want lifecycle error")
	}
}

func TestWindowsResourceCollectorSnapshot(t *testing.T) {
	api := &fakeWindowsAPI{
		kernelNanoseconds: 5_000_000,
		userNanoseconds:   3_000_000,
		peakWorkingSet:    8192,
	}
	collector := newTestWindowsCollector(api)
	if err := collector.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	resources, err := collector.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}

	if err := resources.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	wantAvailability := ResourceAvailability{
		Wall: true, CPU: true, PeakRSS: true, HeapAlloc: true, TotalAlloc: true,
	}
	if resources.Availability != wantAvailability {
		t.Errorf("Availability = %+v, want %+v", resources.Availability, wantAvailability)
	}

	wantUnits := NewProcessResources(resources.Collector).Units
	if resources.Units != wantUnits {
		t.Errorf("Units = %+v, want %+v", resources.Units, wantUnits)
	}

	wantCPU := time.Duration(5_000_000+3_000_000) * time.Nanosecond
	if resources.CPU != wantCPU {
		t.Errorf("CPU = %v, want %v (kernel+user)", resources.CPU, wantCPU)
	}

	if resources.PeakRSSBytes != int64(api.peakWorkingSet) {
		t.Errorf("PeakRSSBytes = %d, want %d (peak working set in bytes)", resources.PeakRSSBytes, api.peakWorkingSet)
	}

	if resources.Wall < 0 {
		t.Errorf("Wall = %v, want non-negative", resources.Wall)
	}
}

func TestWindowsResourceCollectorMonotonic(t *testing.T) {
	api := &fakeWindowsAPI{
		kernelNanoseconds: 1_000_000,
		userNanoseconds:   1_000_000,
		peakWorkingSet:    4096,
	}
	collector := newTestWindowsCollector(api)
	if err := collector.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	first, err := collector.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("first Snapshot() error = %v", err)
	}

	api.kernelNanoseconds += 500_000
	api.userNanoseconds += 500_000
	api.peakWorkingSet += 1024

	second, err := collector.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("second Snapshot() error = %v", err)
	}

	if second.Wall < first.Wall {
		t.Errorf("Wall not monotonic: first = %v, second = %v", first.Wall, second.Wall)
	}
	if second.CPU < first.CPU {
		t.Errorf("CPU not monotonic: first = %v, second = %v", first.CPU, second.CPU)
	}
	if second.PeakRSSBytes < first.PeakRSSBytes {
		t.Errorf("PeakRSS not monotonic: first = %d, second = %d", first.PeakRSSBytes, second.PeakRSSBytes)
	}
}

func TestWindowsResourceCollectorHandlesClosed(t *testing.T) {
	tests := []struct {
		name             string
		processTimesErr  error
		processMemoryErr error
		wantSnapshotErr  bool
	}{
		{
			name:            "success path closes handle",
			wantSnapshotErr: false,
		},
		{
			name:            "GetProcessTimes failure still closes handle",
			processTimesErr: errors.New("GetProcessTimes failed"),
			wantSnapshotErr: true,
		},
		{
			name:             "GetProcessMemoryInfo failure still closes handle",
			processMemoryErr: errors.New("GetProcessMemoryInfo failed"),
			wantSnapshotErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &fakeWindowsAPI{
				kernelNanoseconds: 1_000_000,
				userNanoseconds:   1_000_000,
				peakWorkingSet:    4096,
				processTimesErr:   tt.processTimesErr,
				processMemoryErr:  tt.processMemoryErr,
			}
			collector := newTestWindowsCollector(api)
			if err := collector.Start(context.Background()); err != nil {
				t.Fatalf("Start() error = %v", err)
			}

			_, err := collector.Snapshot(context.Background())
			if (err != nil) != tt.wantSnapshotErr {
				t.Fatalf("Snapshot() error = %v, wantSnapshotErr %v", err, tt.wantSnapshotErr)
			}

			if api.openedCount() == 0 {
				t.Fatalf("expected at least one opened handle")
			}
			if !api.allHandlesClosed() {
				t.Errorf("opened %d handles but closed %d; not all handles were closed",
					api.openedCount(), api.closedCount())
			}
		})
	}
}

func TestWindowsResourceCollectorErrorBehavior(t *testing.T) {
	t.Run("OpenProcess failure returns typed unavailable error", func(t *testing.T) {
		api := &fakeWindowsAPI{
			openProcessErr: errors.New("access denied"),
		}
		collector := newTestWindowsCollector(api)
		if err := collector.Start(context.Background()); err != nil {
			t.Fatalf("Start() error = %v", err)
		}

		_, err := collector.Snapshot(context.Background())
		if err == nil {
			t.Fatal("Snapshot() error = nil, want error")
		}
		if !errors.Is(err, ErrMeasurementUnavailable) {
			t.Fatalf("Snapshot() error = %v, want ErrMeasurementUnavailable", err)
		}
		var unavailable *MeasurementUnavailableError
		if !errors.As(err, &unavailable) {
			t.Fatalf("Snapshot() error type = %T, want *MeasurementUnavailableError", err)
		}
		if unavailable.Collector.Method != "windows-process-api" || unavailable.Collector.Version != "v1" {
			t.Errorf("unavailable collector = %+v, want windows-process-api/v1", unavailable.Collector)
		}
	})

	t.Run("GetProcessTimes failure returns typed unavailable error", func(t *testing.T) {
		api := &fakeWindowsAPI{
			processTimesErr: errors.New("GetProcessTimes failed"),
		}
		collector := newTestWindowsCollector(api)
		if err := collector.Start(context.Background()); err != nil {
			t.Fatalf("Start() error = %v", err)
		}

		_, err := collector.Snapshot(context.Background())
		if !errors.Is(err, ErrMeasurementUnavailable) {
			t.Fatalf("Snapshot() error = %v, want ErrMeasurementUnavailable", err)
		}
		var unavailable *MeasurementUnavailableError
		if !errors.As(err, &unavailable) {
			t.Fatalf("Snapshot() error type = %T, want *MeasurementUnavailableError", err)
		}
	})

	t.Run("GetProcessMemoryInfo failure returns typed unavailable error", func(t *testing.T) {
		api := &fakeWindowsAPI{
			processMemoryErr: errors.New("GetProcessMemoryInfo failed"),
		}
		collector := newTestWindowsCollector(api)
		if err := collector.Start(context.Background()); err != nil {
			t.Fatalf("Start() error = %v", err)
		}

		_, err := collector.Snapshot(context.Background())
		if !errors.Is(err, ErrMeasurementUnavailable) {
			t.Fatalf("Snapshot() error = %v, want ErrMeasurementUnavailable", err)
		}
		var unavailable *MeasurementUnavailableError
		if !errors.As(err, &unavailable) {
			t.Fatalf("Snapshot() error type = %T, want *MeasurementUnavailableError", err)
		}
	})
}

func TestWindowsResourceCollectorNewResourceCollector(t *testing.T) {
	collector, err := NewResourceCollector()
	if err != nil {
		t.Fatalf("NewResourceCollector() error = %v, want nil on Windows", err)
	}
	if collector == nil {
		t.Fatal("NewResourceCollector() collector = nil, want non-nil on Windows")
	}
	if err := collector.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	resources, err := collector.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if err := resources.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	wantIdentity := ResourceCollectorIdentity{
		Method:  "windows-process-api",
		Version: "v1",
	}
	if resources.Collector != wantIdentity {
		t.Errorf("Collector = %+v, want %+v", resources.Collector, wantIdentity)
	}
}
