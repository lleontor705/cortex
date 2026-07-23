package cortex

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestResourceCollectorLifecycle(t *testing.T) {
	identity := ResourceCollectorIdentity{Method: "test-process", Version: "v1"}
	first := NewProcessResources(identity)
	first.Availability = ResourceAvailability{
		Wall: true, CPU: true, PeakRSS: true, HeapAlloc: true, TotalAlloc: true,
	}
	first.Wall = time.Millisecond
	first.CPU = 500 * time.Microsecond
	first.PeakRSSBytes = 1024
	first.HeapAllocBytes = 256
	first.TotalAllocBytes = 512

	var lifecycle CollectorLifecycle
	if err := lifecycle.Observe(first); err == nil {
		t.Fatal("Observe() before Start() error = nil, want lifecycle error")
	}
	if err := lifecycle.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := lifecycle.Start(); err == nil {
		t.Fatal("second Start() error = nil, want lifecycle error")
	}
	if err := lifecycle.Observe(first); err != nil {
		t.Fatalf("Observe(first) error = %v", err)
	}

	second := first
	second.Wall += time.Millisecond
	second.CPU += time.Microsecond
	second.PeakRSSBytes++
	second.TotalAllocBytes++
	second.HeapAllocBytes = 1 // Current heap is a gauge and need not be monotonic.
	if err := lifecycle.Observe(second); err != nil {
		t.Fatalf("Observe(second) error = %v", err)
	}

	nonMonotonic := second
	nonMonotonic.Wall = first.Wall
	if err := lifecycle.Observe(nonMonotonic); err == nil {
		t.Fatal("Observe(nonMonotonic) error = nil, want monotonic wall error")
	}
}

func TestResourceAvailabilityDistinguishesUnavailableFromZero(t *testing.T) {
	identity := ResourceCollectorIdentity{Method: "windows-process-api", Version: "v1"}
	availableZero := NewProcessResources(identity)
	availableZero.Availability = ResourceAvailability{
		Wall: true, CPU: true, PeakRSS: true, HeapAlloc: true, TotalAlloc: true,
	}
	unavailable := NewProcessResources(identity)

	for name, resources := range map[string]ProcessResources{
		"available zero": availableZero,
		"unavailable":    unavailable,
	} {
		t.Run(name, func(t *testing.T) {
			if err := resources.Validate(); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}

	availableJSON, err := json.Marshal(availableZero)
	if err != nil {
		t.Fatalf("Marshal(availableZero) error = %v", err)
	}
	unavailableJSON, err := json.Marshal(unavailable)
	if err != nil {
		t.Fatalf("Marshal(unavailable) error = %v", err)
	}
	if bytes.Equal(availableJSON, unavailableJSON) {
		t.Fatalf("available-zero and unavailable JSON are identical: %s", availableJSON)
	}
	for _, required := range [][]byte{
		[]byte(`"method":"windows-process-api"`),
		[]byte(`"version":"v1"`),
		[]byte(`"wall":"nanoseconds"`),
		[]byte(`"cpu":"nanoseconds"`),
		[]byte(`"peak_rss":"bytes"`),
		[]byte(`"total_alloc":"bytes"`),
	} {
		if !bytes.Contains(availableJSON, required) {
			t.Errorf("serialized resources %s do not contain %s", availableJSON, required)
		}
	}

	invalid := unavailable
	invalid.CPU = time.Nanosecond
	if err := invalid.Validate(); err == nil {
		t.Fatal("Validate() error = nil for unavailable non-zero CPU")
	}
}

func TestResourceFreshProcessBoundary(t *testing.T) {
	if marker := os.Getenv("CORTEX_RESOURCE_HELPER_MARKER"); marker != "" {
		if err := os.WriteFile(marker, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			t.Fatalf("WriteFile(helper marker) error = %v", err)
		}
		return
	}

	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable() error = %v", err)
	}
	dir := t.TempDir()
	for i := 0; i < 2; i++ {
		marker := filepath.Join(dir, "child-"+strconv.Itoa(i)+".pid")
		err := RunFreshProcess(context.Background(), FreshProcessRequest{
			Executable: executable,
			Args:       []string{"-test.run=^TestResourceFreshProcessBoundary$"},
			Dir:        dir,
			Env:        []string{"CORTEX_RESOURCE_HELPER_MARKER=" + marker},
		})
		if err != nil {
			t.Fatalf("RunFreshProcess(%d) error = %v", i, err)
		}
		encodedPID, err := os.ReadFile(marker)
		if err != nil {
			t.Fatalf("ReadFile(%q) error = %v", marker, err)
		}
		pid, err := strconv.Atoi(string(encodedPID))
		if err != nil {
			t.Fatalf("Atoi(%q) error = %v", encodedPID, err)
		}
		if pid == os.Getpid() {
			t.Fatalf("child PID = parent PID %d; command did not cross a process boundary", pid)
		}
	}
}
