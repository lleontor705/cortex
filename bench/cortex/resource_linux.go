//go:build linux

package cortex

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	linuxProcfsMethod  = "linux-procfs"
	linuxProcfsVersion = "v1"
	// defaultClockTicksPerSec is the standard Linux CLK_TCK value.
	defaultClockTicksPerSec int64 = 100
)

// linuxResourceCollector reads process CPU and peak RSS from /proc/[pid]/stat
// and /proc/[pid]/status. It never shells to GNU tools; all parsing is done
// in-process from file contents.
type linuxResourceCollector struct {
	lifecycle   CollectorLifecycle
	identity    ResourceCollectorIdentity
	startTime   time.Time
	pid         int
	statData    func() ([]byte, error)
	statusData  func() ([]byte, error)
	ticksPerSec int64
}

// NewResourceCollector returns a Linux /proc-based resource collector.
func NewResourceCollector() (ResourceCollector, error) {
	pid := os.Getpid()
	return &linuxResourceCollector{
		identity: ResourceCollectorIdentity{
			Method:  linuxProcfsMethod,
			Version: linuxProcfsVersion,
		},
		pid:         pid,
		statData:    func() ([]byte, error) { return os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)) },
		statusData:  func() ([]byte, error) { return os.ReadFile(fmt.Sprintf("/proc/%d/status", pid)) },
		ticksPerSec: defaultClockTicksPerSec,
	}, nil
}

// Start begins the collector lifecycle and records the wall-clock start time.
func (c *linuxResourceCollector) Start(_ context.Context) error {
	c.startTime = time.Now()
	return c.lifecycle.Start()
}

// Snapshot reads /proc and runtime metrics into a ProcessResources sample.
// Missing or malformed /proc data produces a typed MeasurementUnavailableError,
// never a silent zero-as-free value.
func (c *linuxResourceCollector) Snapshot(_ context.Context) (ProcessResources, error) {
	resources := NewProcessResources(c.identity)
	resources.Availability = ResourceAvailability{
		Wall:       true,
		CPU:        true,
		PeakRSS:    true,
		HeapAlloc:  true,
		TotalAlloc: true,
	}
	resources.Wall = time.Since(c.startTime)

	statBytes, err := c.statData()
	if err != nil {
		return ProcessResources{}, &MeasurementUnavailableError{
			Collector: c.identity,
			Resource:  "cpu",
			Cause:     err,
		}
	}
	cpu, err := parseProcStatCPU(statBytes, c.ticksPerSec)
	if err != nil {
		return ProcessResources{}, &MeasurementUnavailableError{
			Collector: c.identity,
			Resource:  "cpu",
			Cause:     err,
		}
	}
	resources.CPU = cpu

	statusBytes, err := c.statusData()
	if err != nil {
		return ProcessResources{}, &MeasurementUnavailableError{
			Collector: c.identity,
			Resource:  "peak_rss",
			Cause:     err,
		}
	}
	peakRSS, err := parseProcStatusPeakRSS(statusBytes)
	if err != nil {
		return ProcessResources{}, &MeasurementUnavailableError{
			Collector: c.identity,
			Resource:  "peak_rss",
			Cause:     err,
		}
	}
	resources.PeakRSSBytes = peakRSS

	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	resources.HeapAllocBytes = memStats.HeapAlloc
	resources.TotalAllocBytes = memStats.TotalAlloc

	if err := c.lifecycle.Observe(resources); err != nil {
		return ProcessResources{}, err
	}
	return resources, nil
}

// parseProcStatCPU extracts utime + stime from /proc/[pid]/stat and converts
// clock ticks to nanoseconds. The comm field (field 2) is enclosed in
// parentheses and may contain spaces and additional parentheses, so the
// parser finds the last ')' before splitting the remaining fields.
func parseProcStatCPU(data []byte, ticksPerSec int64) (time.Duration, error) {
	if ticksPerSec <= 0 {
		return 0, fmt.Errorf("clock ticks per second must be positive, got %d", ticksPerSec)
	}
	s := string(data)
	lastParen := strings.LastIndexByte(s, ')')
	if lastParen < 0 {
		return 0, fmt.Errorf("malformed /proc stat: missing closing parenthesis in comm field")
	}
	fields := strings.Fields(s[lastParen+1:])
	// After comm, field 3 (state) is at index 0.
	// utime (field 14) is at index 11, stime (field 15) at index 12.
	const minFields = 13
	if len(fields) < minFields {
		return 0, fmt.Errorf("malformed /proc stat: expected at least %d fields after comm, got %d", minFields, len(fields))
	}
	utime, err := strconv.ParseInt(fields[11], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("malformed /proc stat: utime field: %w", err)
	}
	stime, err := strconv.ParseInt(fields[12], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("malformed /proc stat: stime field: %w", err)
	}
	totalTicks := utime + stime
	nanoseconds := totalTicks * int64(time.Second) / ticksPerSec
	return time.Duration(nanoseconds), nil
}

// parseProcStatusPeakRSS extracts VmHWM from /proc/[pid]/status and converts
// kB to bytes. Returns a typed error if the line is missing or malformed.
func parseProcStatusPeakRSS(data []byte) (int64, error) {
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "VmHWM:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, fmt.Errorf("malformed /proc status VmHWM line: %q", line)
		}
		kb, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("malformed /proc status VmHWM value: %w", err)
		}
		if kb < 0 {
			return 0, fmt.Errorf("malformed /proc status VmHWM: negative value %d", kb)
		}
		return kb * 1024, nil
	}
	return 0, fmt.Errorf("malformed /proc status: VmHWM line not found")
}
