//go:build !linux && !windows

package cortex

import (
	"errors"
	"testing"
)

func TestUnsupportedPlatformRefusesRepresentativeMeasurement(t *testing.T) {
	collector, err := NewResourceCollector()
	if collector != nil {
		t.Fatalf("NewResourceCollector() collector = %T, want nil before representative output", collector)
	}
	if !errors.Is(err, ErrMeasurementUnavailable) {
		t.Fatalf("NewResourceCollector() error = %v, want ErrMeasurementUnavailable", err)
	}

	var unavailable *MeasurementUnavailableError
	if !errors.As(err, &unavailable) {
		t.Fatalf("NewResourceCollector() error type = %T, want *MeasurementUnavailableError", err)
	}
	if unavailable.Collector != (ResourceCollectorIdentity{Method: "unsupported-platform", Version: "v1"}) {
		t.Errorf("collector identity = %+v, want stable unsupported-platform/v1 identity", unavailable.Collector)
	}
	if unavailable.Resource != "representative process resources" {
		t.Errorf("unavailable resource = %q, want representative process resources", unavailable.Resource)
	}
}
