package cortex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	resourceUnitNanoseconds = "nanoseconds"
	resourceUnitBytes       = "bytes"
)

// ResourceCollector captures process resources without changing the measured
// process lifecycle. Platform implementations provide the measurements.
type ResourceCollector interface {
	Start(context.Context) error
	Snapshot(context.Context) (ProcessResources, error)
}

// ResourceCollectorIdentity versions the operating-system measurement method.
// It is serialized with each sample so run and hardware identities cannot
// silently compare different collectors.
type ResourceCollectorIdentity struct {
	Method  string `json:"method"`
	Version string `json:"version"`
}

// ResourceUnits makes every serialized resource unit explicit.
type ResourceUnits struct {
	Wall       string `json:"wall"`
	CPU        string `json:"cpu"`
	PeakRSS    string `json:"peak_rss"`
	HeapAlloc  string `json:"heap_alloc"`
	TotalAlloc string `json:"total_alloc"`
}

// ResourceAvailability distinguishes an available zero measurement from a
// metric the collector could not measure.
type ResourceAvailability struct {
	Wall       bool `json:"wall"`
	CPU        bool `json:"cpu"`
	PeakRSS    bool `json:"peak_rss"`
	HeapAlloc  bool `json:"heap_alloc"`
	TotalAlloc bool `json:"total_alloc"`
}

// ProcessResources is one cumulative process resource sample. Durations are
// serialized as nanoseconds and memory/allocation values as bytes.
type ProcessResources struct {
	Collector       ResourceCollectorIdentity `json:"collector"`
	Units           ResourceUnits             `json:"units"`
	Availability    ResourceAvailability      `json:"availability"`
	Wall            time.Duration             `json:"wall_nanoseconds"`
	CPU             time.Duration             `json:"cpu_nanoseconds"`
	PeakRSSBytes    int64                     `json:"peak_rss_bytes"`
	HeapAllocBytes  uint64                    `json:"heap_alloc_bytes"`
	TotalAllocBytes uint64                    `json:"total_alloc_bytes"`
}

// NewProcessResources initializes a sample with the canonical evidence units.
func NewProcessResources(identity ResourceCollectorIdentity) ProcessResources {
	return ProcessResources{
		Collector: identity,
		Units: ResourceUnits{
			Wall:       resourceUnitNanoseconds,
			CPU:        resourceUnitNanoseconds,
			PeakRSS:    resourceUnitBytes,
			HeapAlloc:  resourceUnitBytes,
			TotalAlloc: resourceUnitBytes,
		},
	}
}

// Validate rejects ambiguous, unversioned, or unit-inconsistent samples.
func (r ProcessResources) Validate() error {
	if strings.TrimSpace(r.Collector.Method) == "" || strings.TrimSpace(r.Collector.Version) == "" {
		return fmt.Errorf("resource collector method and version are required")
	}
	wantUnits := NewProcessResources(r.Collector).Units
	if r.Units != wantUnits {
		return fmt.Errorf("resource units = %+v, want %+v", r.Units, wantUnits)
	}
	if r.Wall < 0 || r.CPU < 0 || r.PeakRSSBytes < 0 {
		return fmt.Errorf("resource values must be non-negative")
	}
	if !r.Availability.Wall && r.Wall != 0 {
		return fmt.Errorf("wall is unavailable but non-zero")
	}
	if !r.Availability.CPU && r.CPU != 0 {
		return fmt.Errorf("CPU is unavailable but non-zero")
	}
	if !r.Availability.PeakRSS && r.PeakRSSBytes != 0 {
		return fmt.Errorf("peak RSS is unavailable but non-zero")
	}
	if !r.Availability.HeapAlloc && r.HeapAllocBytes != 0 {
		return fmt.Errorf("heap allocation is unavailable but non-zero")
	}
	if !r.Availability.TotalAlloc && r.TotalAllocBytes != 0 {
		return fmt.Errorf("total allocation is unavailable but non-zero")
	}
	return nil
}

// CollectorLifecycle enforces start-before-snapshot and cumulative monotonic
// evidence. Platform collectors may embed it and call Start and Observe.
type CollectorLifecycle struct {
	mu      sync.Mutex
	started bool
	last    *ProcessResources
}

// Start begins one collector lifecycle and rejects reuse.
func (l *CollectorLifecycle) Start() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.started {
		return fmt.Errorf("resource collector is already started")
	}
	l.started = true
	return nil
}

// Observe validates a snapshot against the collector lifecycle and prior
// cumulative sample. HeapAllocBytes is intentionally excluded because it is a
// current gauge rather than a cumulative counter.
func (l *CollectorLifecycle) Observe(next ProcessResources) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.started {
		return fmt.Errorf("resource collector has not started")
	}
	if err := next.Validate(); err != nil {
		return err
	}
	if l.last != nil {
		previous := *l.last
		if next.Collector != previous.Collector {
			return fmt.Errorf("resource collector identity changed during sampling")
		}
		if next.Availability != previous.Availability {
			return fmt.Errorf("resource availability changed during sampling")
		}
		if next.Wall < previous.Wall || next.CPU < previous.CPU || next.PeakRSSBytes < previous.PeakRSSBytes || next.TotalAllocBytes < previous.TotalAllocBytes {
			return fmt.Errorf("cumulative process resources are not monotonic")
		}
	}
	copy := next
	l.last = &copy
	return nil
}

// FreshProcessRequest describes exactly one independent process invocation.
type FreshProcessRequest struct {
	Executable string
	Args       []string
	Dir        string
	Env        []string
}

// RunFreshProcess creates and waits for a new operating-system process for
// every call. It never reuses an in-process runner, database, or app instance.
func RunFreshProcess(ctx context.Context, request FreshProcessRequest) error {
	return runFreshProcess(ctx, request, executeFreshProcess)
}

type freshProcessExecutor func(context.Context, FreshProcessRequest) ([]byte, error)

func runFreshProcess(ctx context.Context, request FreshProcessRequest, execute freshProcessExecutor) error {
	if strings.TrimSpace(request.Executable) == "" {
		return fmt.Errorf("fresh process executable is required")
	}
	if strings.TrimSpace(request.Dir) == "" {
		return fmt.Errorf("fresh process working directory is required")
	}
	info, err := os.Stat(request.Dir)
	if err != nil {
		return fmt.Errorf("stat fresh process working directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("fresh process working directory %q is not a directory", request.Dir)
	}
	output, err := execute(ctx, request)
	if err != nil {
		return fmt.Errorf("fresh process failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func executeFreshProcess(ctx context.Context, request FreshProcessRequest) ([]byte, error) {
	command := exec.CommandContext(ctx, request.Executable, request.Args...)
	command.Dir = request.Dir
	command.Env = append(os.Environ(), request.Env...)
	return command.CombinedOutput()
}

// ErrMeasurementUnavailable identifies a platform measurement that cannot be
// provided without representing absence as a zero value.
var ErrMeasurementUnavailable = errors.New("process resource measurement unavailable")

// MeasurementUnavailableError carries the versioned collector identity and
// unavailable resource for fail-closed representative evidence.
type MeasurementUnavailableError struct {
	Collector ResourceCollectorIdentity
	Resource  string
	Cause     error
}

func (e *MeasurementUnavailableError) Error() string {
	message := ErrMeasurementUnavailable.Error()
	if e.Resource != "" {
		message += ": " + e.Resource
	}
	if e.Cause != nil {
		message += ": " + e.Cause.Error()
	}
	return message
}

// Is allows errors.Is(err, ErrMeasurementUnavailable).
func (e *MeasurementUnavailableError) Is(target error) bool {
	return target == ErrMeasurementUnavailable
}
