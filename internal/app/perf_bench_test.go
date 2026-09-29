package app

import (
	"context"
	"testing"
)

// BenchmarkOpen captures the W5 boot-latency baseline for the full app.Open
// composition path — config load, database manager creation, the v2 schema
// baseline apply, and store wiring — against an in-memory database. It is a
// pure measurement harness: no production code participates beyond Open itself.
func BenchmarkOpen(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		a, err := Open(context.Background(), Options{InMemory: true})
		if err != nil {
			b.Fatalf("Open() error = %v", err)
		}
		if err := a.Close(); err != nil {
			b.Fatalf("Close() error = %v", err)
		}
	}
}
