//go:build windows

package cortex

import (
	"context"
	"fmt"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	windowsCollectorMethod  = "windows-process-api"
	windowsCollectorVersion = "v1"
)

// processMemoryCounters mirrors the Windows PROCESS_MEMORY_COUNTERS struct
// from psapi.dll. On 64-bit Windows SIZE_T is uintptr (8 bytes) and the struct
// is 72 bytes with no padding after the two leading DWORD fields.
type processMemoryCounters struct {
	cb                         uint32
	pageFaultCount             uint32
	peakWorkingSetSize         uintptr
	workingSetSize             uintptr
	quotaPeakPagedPoolUsage    uintptr
	quotaPagedPoolUsage        uintptr
	quotaPeakNonPagedPoolUsage uintptr
	quotaNonPagedPoolUsage     uintptr
	pagefileUsage              uintptr
	peakPagefileUsage          uintptr
}

var (
	psapiDLL                 = windows.NewLazySystemDLL("psapi.dll")
	procGetProcessMemoryInfo = psapiDLL.NewProc("GetProcessMemoryInfo")
)

// windowsProcessAPI is a seam for Windows process APIs, enabling deterministic
// tests without real OS handles.
type windowsProcessAPI interface {
	OpenProcess(access uint32, inherit bool, pid uint32) (windows.Handle, error)
	GetProcessTimes(handle windows.Handle, creation, exit, kernel, user *windows.Filetime) error
	GetProcessMemoryInfo(handle windows.Handle) (processMemoryCounters, error)
	CloseHandle(handle windows.Handle) error
}

// realWindowsAPI calls the actual Windows process and memory APIs.
type realWindowsAPI struct{}

func (realWindowsAPI) OpenProcess(access uint32, inherit bool, pid uint32) (windows.Handle, error) {
	return windows.OpenProcess(access, inherit, pid)
}

func (realWindowsAPI) GetProcessTimes(handle windows.Handle, creation, exit, kernel, user *windows.Filetime) error {
	return windows.GetProcessTimes(handle, creation, exit, kernel, user)
}

func (realWindowsAPI) GetProcessMemoryInfo(handle windows.Handle) (processMemoryCounters, error) {
	var counters processMemoryCounters
	counters.cb = uint32(unsafe.Sizeof(counters))
	r1, _, callErr := procGetProcessMemoryInfo.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(&counters)),
		uintptr(counters.cb),
	)
	if r1 == 0 {
		return counters, fmt.Errorf("GetProcessMemoryInfo failed: %w", callErr)
	}
	return counters, nil
}

func (realWindowsAPI) CloseHandle(handle windows.Handle) error {
	return windows.CloseHandle(handle)
}

// windowsResourceCollector measures the current process using Windows
// process-times and process-memory APIs, versioned as windows-process-api/v1.
// Every Snapshot opens a real process handle via OpenProcess and closes it
// before returning, even on error.
type windowsResourceCollector struct {
	lifecycle CollectorLifecycle
	startTime time.Time
	api       windowsProcessAPI
	pid       uint32
}

// newWindowsResourceCollectorWithAPI creates a collector with a custom API
// implementation for testing.
func newWindowsResourceCollectorWithAPI(api windowsProcessAPI) *windowsResourceCollector {
	return &windowsResourceCollector{
		api: api,
		pid: windows.GetCurrentProcessId(),
	}
}

// NewResourceCollector returns a Windows process resource collector that uses
// the real Windows APIs. It returns nil error on Windows because all required
// APIs are available.
func NewResourceCollector() (ResourceCollector, error) {
	return &windowsResourceCollector{
		api: realWindowsAPI{},
		pid: windows.GetCurrentProcessId(),
	}, nil
}

// Start begins the collector lifecycle and records the wall-clock start time.
func (c *windowsResourceCollector) Start(_ context.Context) error {
	if err := c.lifecycle.Start(); err != nil {
		return err
	}
	c.startTime = time.Now()
	return nil
}

// Snapshot captures one cumulative process resource sample. It opens a real
// process handle, queries process times and memory, closes the handle, and
// validates the result through the collector lifecycle. Any API failure
// returns a typed *MeasurementUnavailableError so unavailable metrics are never
// represented as zero-as-free.
func (c *windowsResourceCollector) Snapshot(_ context.Context) (ProcessResources, error) {
	identity := ResourceCollectorIdentity{
		Method:  windowsCollectorMethod,
		Version: windowsCollectorVersion,
	}

	handle, err := c.api.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, c.pid)
	if err != nil {
		return ProcessResources{}, &MeasurementUnavailableError{
			Collector: identity,
			Resource:  "process handle",
			Cause:     err,
		}
	}
	defer func() { _ = c.api.CloseHandle(handle) }()

	var creation, exit, kernel, user windows.Filetime
	if err := c.api.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return ProcessResources{}, &MeasurementUnavailableError{
			Collector: identity,
			Resource:  "process times",
			Cause:     err,
		}
	}

	memCounters, err := c.api.GetProcessMemoryInfo(handle)
	if err != nil {
		return ProcessResources{}, &MeasurementUnavailableError{
			Collector: identity,
			Resource:  "process memory",
			Cause:     err,
		}
	}

	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	resources := NewProcessResources(identity)
	resources.Availability = ResourceAvailability{
		Wall:       true,
		CPU:        true,
		PeakRSS:    true,
		HeapAlloc:  true,
		TotalAlloc: true,
	}
	resources.Wall = time.Since(c.startTime)
	// Process times are elapsed durations stored as FILETIME (100ns intervals),
	// not absolute timestamps. Compute directly from raw fields to avoid the
	// epoch conversion in Filetime.Nanoseconds() which overflows for durations.
	kernel100ns := int64(kernel.HighDateTime)<<32 + int64(kernel.LowDateTime)
	user100ns := int64(user.HighDateTime)<<32 + int64(user.LowDateTime)
	resources.CPU = time.Duration((kernel100ns + user100ns) * 100)
	resources.PeakRSSBytes = int64(memCounters.peakWorkingSetSize)
	resources.HeapAllocBytes = memStats.HeapAlloc
	resources.TotalAllocBytes = memStats.TotalAlloc

	if err := c.lifecycle.Observe(resources); err != nil {
		return ProcessResources{}, err
	}
	return resources, nil
}
