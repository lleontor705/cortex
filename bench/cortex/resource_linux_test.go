//go:build linux

package cortex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

// validProcStat returns a /proc/[pid]/stat byte slice with the given utime
// and stime values (fields 14 and 15, 0-indexed 11 and 12 after comm).
func validProcStat(utime, stime int64) []byte {
	return []byte(fmt.Sprintf(
		"1234 (test) S 1 1234 1234 0 -1 4194304 100 0 0 0 %d %d 0 0 20 0 1 0 1000000 0 0 18446744073709551615 1 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0",
		utime, stime,
	))
}

// validProcStatus returns a /proc/[pid]/status byte slice with the given
// VmHWM value in kB.
func validProcStatus(vmHWM int64) []byte {
	return []byte(fmt.Sprintf("Name:   test\nUmask:  0022\nState:  R\nVmHWM:  %d kB\nVmRSS:  %d kB\n", vmHWM, vmHWM))
}

func TestLinuxResourceCollectorIdentity(t *testing.T) {
	collector, err := NewResourceCollector()
	if err != nil {
		t.Fatalf("NewResourceCollector() error = %v, want nil on Linux", err)
	}
	if collector == nil {
		t.Fatal("NewResourceCollector() collector = nil, want non-nil")
	}
	linux, ok := collector.(*linuxResourceCollector)
	if !ok {
		t.Fatalf("NewResourceCollector() type = %T, want *linuxResourceCollector", collector)
	}
	want := ResourceCollectorIdentity{Method: "linux-procfs", Version: "v1"}
	if linux.identity != want {
		t.Errorf("identity = %+v, want %+v", linux.identity, want)
	}
}

func TestParseProcStatCPU(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		ticksPerSec int64
		wantCPU     time.Duration
		wantErr     bool
	}{
		{
			name:        "valid utime=50 stime=30 at 100 Hz",
			input:       string(validProcStat(50, 30)),
			ticksPerSec: 100,
			wantCPU:     800 * time.Millisecond,
		},
		{
			name:        "valid utime=0 stime=0 at 100 Hz",
			input:       string(validProcStat(0, 0)),
			ticksPerSec: 100,
			wantCPU:     0,
		},
		{
			name:        "valid utime=100 stime=200 at 100 Hz",
			input:       string(validProcStat(100, 200)),
			ticksPerSec: 100,
			wantCPU:     3 * time.Second,
		},
		{
			name:        "valid at 250 Hz",
			input:       string(validProcStat(50, 30)),
			ticksPerSec: 250,
			wantCPU:     320 * time.Millisecond,
		},
		{
			name:        "comm with spaces and parens",
			input:       "1234 (test (prog)) S 1 1234 1234 0 -1 4194304 100 0 0 0 10 20 0 0 20 0 1 0 1000000 0 0 18446744073709551615 1 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0",
			ticksPerSec: 100,
			wantCPU:     300 * time.Millisecond,
		},
		{
			name:        "missing closing parenthesis",
			input:       "1234 test S 1 1234 1234 0 -1",
			ticksPerSec: 100,
			wantErr:     true,
		},
		{
			name:        "too few fields after comm",
			input:       "1234 (test) S 1",
			ticksPerSec: 100,
			wantErr:     true,
		},
		{
			name:        "non-numeric utime",
			input:       "1234 (test) S 1 1234 1234 0 -1 4194304 100 0 0 0 abc 30 0 0 20 0 1 0 1000000 0 0 18446744073709551615 1 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0",
			ticksPerSec: 100,
			wantErr:     true,
		},
		{
			name:        "non-numeric stime",
			input:       "1234 (test) S 1 1234 1234 0 -1 4194304 100 0 0 0 50 xyz 0 0 20 0 1 0 1000000 0 0 18446744073709551615 1 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0",
			ticksPerSec: 100,
			wantErr:     true,
		},
		{
			name:        "zero ticks per second",
			input:       string(validProcStat(50, 30)),
			ticksPerSec: 0,
			wantErr:     true,
		},
		{
			name:        "negative ticks per second",
			input:       string(validProcStat(50, 30)),
			ticksPerSec: -1,
			wantErr:     true,
		},
		{
			name:        "empty input",
			input:       "",
			ticksPerSec: 100,
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseProcStatCPU([]byte(tt.input), tt.ticksPerSec)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseProcStatCPU() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseProcStatCPU() error = %v, want nil", err)
			}
			if got != tt.wantCPU {
				t.Errorf("parseProcStatCPU() = %v, want %v", got, tt.wantCPU)
			}
		})
	}
}

func TestParseProcStatusPeakRSS(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantRSS int64
		wantErr bool
	}{
		{
			name:    "valid VmHWM 1234 kB",
			input:   string(validProcStatus(1234)),
			wantRSS: 1234 * 1024,
		},
		{
			name:    "valid VmHWM 0 kB",
			input:   "Name:   test\nVmHWM:    0 kB\n",
			wantRSS: 0,
		},
		{
			name:    "valid VmHWM 4096 kB",
			input:   "Name:   test\nVmHWM:   4096 kB\n",
			wantRSS: 4096 * 1024,
		},
		{
			name:    "VmHWM with tab indentation",
			input:   "Name:\ttest\nVmHWM:\t 2048 kB\n",
			wantRSS: 2048 * 1024,
		},
		{
			name:    "missing VmHWM line",
			input:   "Name:   test\nState:  R\n",
			wantErr: true,
		},
		{
			name:    "malformed VmHWM non-numeric",
			input:   "Name:   test\nVmHWM:   abc kB\n",
			wantErr: true,
		},
		{
			name:    "malformed VmHWM negative",
			input:   "Name:   test\nVmHWM:   -100 kB\n",
			wantErr: true,
		},
		{
			name:    "malformed VmHWM no value",
			input:   "Name:   test\nVmHWM:\n",
			wantErr: true,
		},
		{
			name:    "empty input",
			input:   "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseProcStatusPeakRSS([]byte(tt.input))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseProcStatusPeakRSS() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseProcStatusPeakRSS() error = %v, want nil", err)
			}
			if got != tt.wantRSS {
				t.Errorf("parseProcStatusPeakRSS() = %d, want %d", got, tt.wantRSS)
			}
		})
	}
}

func TestLinuxResourceCollectorSnapshot(t *testing.T) {
	statData := validProcStat(50, 30)
	statusData := validProcStatus(2048)

	collector := &linuxResourceCollector{
		identity: ResourceCollectorIdentity{
			Method:  linuxProcfsMethod,
			Version: linuxProcfsVersion,
		},
		statData:    func() ([]byte, error) { return statData, nil },
		statusData:  func() ([]byte, error) { return statusData, nil },
		ticksPerSec: 100,
	}

	ctx := context.Background()
	if err := collector.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	resources, err := collector.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot() error = %v, want nil", err)
	}

	// Verify identity
	wantIdentity := ResourceCollectorIdentity{Method: "linux-procfs", Version: "v1"}
	if resources.Collector != wantIdentity {
		t.Errorf("collector identity = %+v, want %+v", resources.Collector, wantIdentity)
	}

	// Verify units are explicit
	if resources.Units.Wall != resourceUnitNanoseconds {
		t.Errorf("wall unit = %q, want %q", resources.Units.Wall, resourceUnitNanoseconds)
	}
	if resources.Units.CPU != resourceUnitNanoseconds {
		t.Errorf("cpu unit = %q, want %q", resources.Units.CPU, resourceUnitNanoseconds)
	}
	if resources.Units.PeakRSS != resourceUnitBytes {
		t.Errorf("peak_rss unit = %q, want %q", resources.Units.PeakRSS, resourceUnitBytes)
	}

	// Verify availability — all true on Linux
	if !resources.Availability.Wall || !resources.Availability.CPU || !resources.Availability.PeakRSS {
		t.Errorf("availability = %+v, want Wall/CPU/PeakRSS all true", resources.Availability)
	}
	if !resources.Availability.HeapAlloc || !resources.Availability.TotalAlloc {
		t.Errorf("availability = %+v, want HeapAlloc/TotalAlloc true", resources.Availability)
	}

	// Verify CPU: (50+30)/100 = 0.8s = 800ms
	wantCPU := 800 * time.Millisecond
	if resources.CPU != wantCPU {
		t.Errorf("CPU = %v, want %v", resources.CPU, wantCPU)
	}

	// Verify peak RSS: 2048 kB = 2,097,152 bytes
	wantRSS := int64(2048 * 1024)
	if resources.PeakRSSBytes != wantRSS {
		t.Errorf("PeakRSSBytes = %d, want %d", resources.PeakRSSBytes, wantRSS)
	}

	// Verify wall is positive
	if resources.Wall <= 0 {
		t.Errorf("Wall = %v, want positive", resources.Wall)
	}

	// Validate the full sample
	if err := resources.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestLinuxResourceCollectorMonotonicSnapshot(t *testing.T) {
	stat1 := validProcStat(50, 30)
	stat2 := validProcStat(60, 40)
	status1 := validProcStatus(1024)
	status2 := validProcStatus(2048)

	callCount := 0
	statCalls := [][]byte{stat1, stat2}
	statusCalls := [][]byte{status1, status2}

	collector := &linuxResourceCollector{
		identity: ResourceCollectorIdentity{
			Method:  linuxProcfsMethod,
			Version: linuxProcfsVersion,
		},
		statData: func() ([]byte, error) {
			if callCount >= len(statCalls) {
				return nil, fmt.Errorf("unexpected stat call %d", callCount)
			}
			return statCalls[callCount], nil
		},
		statusData: func() ([]byte, error) {
			if callCount >= len(statusCalls) {
				return nil, fmt.Errorf("unexpected status call %d", callCount)
			}
			return statusCalls[callCount], nil
		},
		ticksPerSec: 100,
	}

	ctx := context.Background()
	if err := collector.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	first, err := collector.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot(1) error = %v", err)
	}
	callCount++

	time.Sleep(time.Millisecond)

	second, err := collector.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot(2) error = %v", err)
	}

	if second.Wall <= first.Wall {
		t.Errorf("wall not monotonic: first=%v second=%v", first.Wall, second.Wall)
	}
	if second.CPU <= first.CPU {
		t.Errorf("CPU not monotonic: first=%v second=%v", first.CPU, second.CPU)
	}
	if second.PeakRSSBytes < first.PeakRSSBytes {
		t.Errorf("peak RSS not monotonic: first=%d second=%d", first.PeakRSSBytes, second.PeakRSSBytes)
	}
}

func TestLinuxResourceCollectorMissingProcFiles(t *testing.T) {
	tests := []struct {
		name         string
		statData     func() ([]byte, error)
		statusData   func() ([]byte, error)
		wantResource string
	}{
		{
			name:         "stat file missing",
			statData:     func() ([]byte, error) { return nil, os.ErrNotExist },
			statusData:   func() ([]byte, error) { return validProcStatus(1024), nil },
			wantResource: "cpu",
		},
		{
			name:         "status file missing",
			statData:     func() ([]byte, error) { return validProcStat(50, 30), nil },
			statusData:   func() ([]byte, error) { return nil, os.ErrNotExist },
			wantResource: "peak_rss",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			collector := &linuxResourceCollector{
				identity: ResourceCollectorIdentity{
					Method:  linuxProcfsMethod,
					Version: linuxProcfsVersion,
				},
				statData:    tt.statData,
				statusData:  tt.statusData,
				ticksPerSec: 100,
			}

			ctx := context.Background()
			if err := collector.Start(ctx); err != nil {
				t.Fatalf("Start() error = %v", err)
			}

			_, err := collector.Snapshot(ctx)
			if err == nil {
				t.Fatal("Snapshot() error = nil, want measurement unavailable error")
			}

			if !errors.Is(err, ErrMeasurementUnavailable) {
				t.Fatalf("Snapshot() error = %v, want ErrMeasurementUnavailable", err)
			}

			var unavailable *MeasurementUnavailableError
			if !errors.As(err, &unavailable) {
				t.Fatalf("Snapshot() error type = %T, want *MeasurementUnavailableError", err)
			}

			if unavailable.Resource != tt.wantResource {
				t.Errorf("unavailable resource = %q, want %q", unavailable.Resource, tt.wantResource)
			}

			wantCollector := ResourceCollectorIdentity{Method: linuxProcfsMethod, Version: linuxProcfsVersion}
			if unavailable.Collector != wantCollector {
				t.Errorf("collector identity in error = %+v, want %+v", unavailable.Collector, wantCollector)
			}
		})
	}
}

func TestLinuxResourceCollectorMalformedData(t *testing.T) {
	tests := []struct {
		name         string
		statData     func() ([]byte, error)
		statusData   func() ([]byte, error)
		wantResource string
	}{
		{
			name:         "malformed stat data",
			statData:     func() ([]byte, error) { return []byte("garbage"), nil },
			statusData:   func() ([]byte, error) { return validProcStatus(1024), nil },
			wantResource: "cpu",
		},
		{
			name:         "malformed status data",
			statData:     func() ([]byte, error) { return validProcStat(50, 30), nil },
			statusData:   func() ([]byte, error) { return []byte("garbage"), nil },
			wantResource: "peak_rss",
		},
		{
			name:         "stat with no parenthesis",
			statData:     func() ([]byte, error) { return []byte("no parens here"), nil },
			statusData:   func() ([]byte, error) { return validProcStatus(1024), nil },
			wantResource: "cpu",
		},
		{
			name:         "status with no VmHWM",
			statData:     func() ([]byte, error) { return validProcStat(50, 30), nil },
			statusData:   func() ([]byte, error) { return []byte("Name: test\nState: R\n"), nil },
			wantResource: "peak_rss",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			collector := &linuxResourceCollector{
				identity: ResourceCollectorIdentity{
					Method:  linuxProcfsMethod,
					Version: linuxProcfsVersion,
				},
				statData:    tt.statData,
				statusData:  tt.statusData,
				ticksPerSec: 100,
			}

			ctx := context.Background()
			if err := collector.Start(ctx); err != nil {
				t.Fatalf("Start() error = %v", err)
			}

			_, err := collector.Snapshot(ctx)
			if err == nil {
				t.Fatal("Snapshot() error = nil, want measurement unavailable error")
			}

			if !errors.Is(err, ErrMeasurementUnavailable) {
				t.Fatalf("Snapshot() error = %v, want ErrMeasurementUnavailable", err)
			}

			var unavailable *MeasurementUnavailableError
			if !errors.As(err, &unavailable) {
				t.Fatalf("Snapshot() error type = %T, want *MeasurementUnavailableError", err)
			}

			if unavailable.Resource != tt.wantResource {
				t.Errorf("unavailable resource = %q, want %q", unavailable.Resource, tt.wantResource)
			}
		})
	}
}

func TestLinuxResourceCollectorUnits(t *testing.T) {
	statData := validProcStat(100, 200)
	statusData := validProcStatus(4096)

	collector := &linuxResourceCollector{
		identity: ResourceCollectorIdentity{
			Method:  linuxProcfsMethod,
			Version: linuxProcfsVersion,
		},
		statData:    func() ([]byte, error) { return statData, nil },
		statusData:  func() ([]byte, error) { return statusData, nil },
		ticksPerSec: 100,
	}

	ctx := context.Background()
	if err := collector.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	resources, err := collector.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}

	// CPU: (100+200)/100 = 3s = 3,000,000,000 ns
	wantCPU := 3 * time.Second
	if resources.CPU != wantCPU {
		t.Errorf("CPU = %v (%d ns), want %v (%d ns)",
			resources.CPU, int64(resources.CPU), wantCPU, int64(wantCPU))
	}

	// Peak RSS: 4096 kB = 4,194,304 bytes
	wantRSS := int64(4096 * 1024)
	if resources.PeakRSSBytes != wantRSS {
		t.Errorf("PeakRSSBytes = %d, want %d", resources.PeakRSSBytes, wantRSS)
	}

	// Verify units are explicitly set
	if resources.Units.CPU != resourceUnitNanoseconds {
		t.Errorf("CPU unit = %q, want %q", resources.Units.CPU, resourceUnitNanoseconds)
	}
	if resources.Units.PeakRSS != resourceUnitBytes {
		t.Errorf("PeakRSS unit = %q, want %q", resources.Units.PeakRSS, resourceUnitBytes)
	}
	if resources.Units.Wall != resourceUnitNanoseconds {
		t.Errorf("Wall unit = %q, want %q", resources.Units.Wall, resourceUnitNanoseconds)
	}
}

func TestLinuxResourceCollectorStartBeforeSnapshot(t *testing.T) {
	collector := &linuxResourceCollector{
		identity: ResourceCollectorIdentity{
			Method:  linuxProcfsMethod,
			Version: linuxProcfsVersion,
		},
		statData:    func() ([]byte, error) { return validProcStat(50, 30), nil },
		statusData:  func() ([]byte, error) { return validProcStatus(1024), nil },
		ticksPerSec: 100,
	}

	ctx := context.Background()
	_, err := collector.Snapshot(ctx)
	if err == nil {
		t.Fatal("Snapshot() before Start() error = nil, want lifecycle error")
	}
}

func TestLinuxResourceCollectorNeverZeroAsFree(t *testing.T) {
	// When /proc files are unavailable, the error must be a typed
	// MeasurementUnavailableError, never a silent zero value.
	collector := &linuxResourceCollector{
		identity: ResourceCollectorIdentity{
			Method:  linuxProcfsMethod,
			Version: linuxProcfsVersion,
		},
		statData:    func() ([]byte, error) { return nil, os.ErrNotExist },
		statusData:  func() ([]byte, error) { return nil, os.ErrNotExist },
		ticksPerSec: 100,
	}

	ctx := context.Background()
	if err := collector.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	resources, err := collector.Snapshot(ctx)
	if err == nil {
		t.Fatal("Snapshot() error = nil, want measurement unavailable error")
	}
	if resources.CPU != 0 || resources.PeakRSSBytes != 0 {
		t.Errorf("resources should be zero-value on error, got CPU=%v PeakRSS=%d",
			resources.CPU, resources.PeakRSSBytes)
	}

	// The error must be detectable via errors.Is and errors.As
	if !errors.Is(err, ErrMeasurementUnavailable) {
		t.Fatalf("errors.Is(err, ErrMeasurementUnavailable) = false, want true")
	}
	var unavailable *MeasurementUnavailableError
	if !errors.As(err, &unavailable) {
		t.Fatalf("errors.As(err, &MeasurementUnavailableError) = false, want true")
	}
}
