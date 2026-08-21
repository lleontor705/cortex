package vectorhydration

import "testing"

func TestScheduleIsPairedAndInterleaved(t *testing.T) {
	a, err := Schedule(7, "c", "p", "r")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Schedule(7, "c", "p", "r")
	if len(a) != 60 || string(a[0].Order) == "" {
		t.Fatalf("unexpected schedule length: %d", len(a))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatal("schedule is not deterministic")
		}
		if a[i].Block != i/3+1 || a[i].Cell != RequiredCells[i%3] {
			t.Fatalf("schedule is not interleaved at %d", i)
		}
	}
	for _, cell := range RequiredCells {
		counts := map[Order]int{}
		for _, entry := range a {
			if entry.Cell == cell {
				counts[entry.Order]++
			}
		}
		if counts[OrderAB] != 10 || counts[OrderBA] != 10 {
			t.Fatalf("cell %d counts: %#v", cell, counts)
		}
	}
}
