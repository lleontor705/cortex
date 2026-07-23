//go:build !linux && !windows

package cortex

// NewResourceCollector fails closed on unsupported platforms before a
// representative run can materialize output without measured resources.
func NewResourceCollector() (ResourceCollector, error) {
	return nil, &MeasurementUnavailableError{
		Collector: ResourceCollectorIdentity{
			Method:  "unsupported-platform",
			Version: "v1",
		},
		Resource: "representative process resources",
	}
}
