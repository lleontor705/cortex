package code

import (
	"testing"
)

func TestCalculateCodeBlastRadius(t *testing.T) {
	// Graph topology:
	//   D (file3) -> C (file2) -> A (file1)
	//   B (file1)
	//   E (file4) [isolated]
	//   Y (cyc2) <-> X (cyc1) -> A (file1)
	graph := &CodeGraph{
		Project: "test-proj",
		Symbols: []Symbol{
			{ID: "func:file1.A", Name: "A", Kind: "func", FilePath: "pkg/file1.go"},
			{ID: "func:file1.B", Name: "B", Kind: "func", FilePath: "pkg/file1.go"},
			{ID: "func:file2.C", Name: "C", Kind: "func", FilePath: "pkg/file2.go"},
			{ID: "func:file3.D", Name: "D", Kind: "func", FilePath: "pkg/file3.go"},
			{ID: "func:file4.E", Name: "E", Kind: "func", FilePath: "pkg/file4.go"},
			{ID: "func:cyc1.X", Name: "X", Kind: "func", FilePath: "pkg/cyc1.go"},
			{ID: "func:cyc2.Y", Name: "Y", Kind: "func", FilePath: "pkg/cyc2.go"},
		},
		Relations: []Relation{
			{SourceID: "func:file2.C", TargetID: "func:file1.A", Relation: "calls"},
			{SourceID: "func:file3.D", TargetID: "func:file2.C", Relation: "calls"},
			{SourceID: "func:cyc1.X", TargetID: "func:file1.A", Relation: "calls"},
			{SourceID: "func:cyc1.X", TargetID: "func:cyc2.Y", Relation: "calls"},
			{SourceID: "func:cyc2.Y", TargetID: "func:cyc1.X", Relation: "calls"},
		},
	}

	t.Run("by symbol name", func(t *testing.T) {
		res := CalculateCodeBlastRadius(graph, "A", 3)
		if res.TargetKind != "symbol" {
			t.Fatalf("expected TargetKind symbol, got %s", res.TargetKind)
		}
		if len(res.DirectCallers) < 2 {
			t.Errorf("expected at least 2 direct callers (C, X), got %v", res.DirectCallers)
		}
		// D is hop 2 caller of C
		foundD := false
		for _, s := range res.TotalImpacted {
			if s == "func:file3.D" {
				foundD = true
				break
			}
		}
		if !foundD {
			t.Errorf("expected D in total impacted, got %v", res.TotalImpacted)
		}
		if len(res.ImpactedFiles) < 2 {
			t.Errorf("expected at least 2 impacted files, got %v", res.ImpactedFiles)
		}
		if res.BlastRadiusPct <= 0 {
			t.Errorf("expected positive blast radius pct, got %f", res.BlastRadiusPct)
		}
	})

	t.Run("by file path", func(t *testing.T) {
		res := CalculateCodeBlastRadius(graph, "pkg/file1.go", 3)
		if res.TargetKind != "file" {
			t.Fatalf("expected TargetKind file, got %s", res.TargetKind)
		}
		if len(res.DirectCallers) < 2 {
			t.Errorf("expected direct callers for file1, got %v", res.DirectCallers)
		}
	})

	t.Run("by file path suffix", func(t *testing.T) {
		res := CalculateCodeBlastRadius(graph, "file1.go", 3)
		if res.TargetKind != "file" {
			t.Fatalf("expected TargetKind file matching suffix, got %s", res.TargetKind)
		}
	})

	t.Run("cycle detection impact", func(t *testing.T) {
		res := CalculateCodeBlastRadius(graph, "X", 3)
		if len(res.AffectedCycles) == 0 {
			t.Errorf("expected affected cycles for symbol X in circular dependency, got 0")
		}
	})

	t.Run("unknown symbol", func(t *testing.T) {
		res := CalculateCodeBlastRadius(graph, "NonExistent", 3)
		if res.TargetKind != "unknown" {
			t.Errorf("expected target kind unknown, got %s", res.TargetKind)
		}
		if len(res.TotalImpacted) != 0 {
			t.Errorf("expected 0 impacted, got %d", len(res.TotalImpacted))
		}
	})
}

func TestCalculateDiffBlastRadius(t *testing.T) {
	graph := &CodeGraph{
		Project: "test-proj",
		Symbols: []Symbol{
			{ID: "func:file1.A", Name: "A", Kind: "func", FilePath: "pkg/file1.go"},
			{ID: "func:file2.B", Name: "B", Kind: "func", FilePath: "pkg/file2.go"},
			{ID: "func:file3.C", Name: "C", Kind: "func", FilePath: "pkg/file3.go"},
		},
		Relations: []Relation{
			{SourceID: "func:file2.B", TargetID: "func:file1.A", Relation: "calls"},
			{SourceID: "func:file3.C", TargetID: "func:file2.B", Relation: "calls"},
		},
	}

	t.Run("diff with single file", func(t *testing.T) {
		res := CalculateDiffBlastRadius(graph, []string{"pkg/file1.go"}, 3)
		if res.TargetKind != "git_diff" {
			t.Fatalf("expected TargetKind git_diff, got %s", res.TargetKind)
		}
		if len(res.DirectCallers) != 1 || res.DirectCallers[0] != "func:file2.B" {
			t.Errorf("expected direct caller B, got %v", res.DirectCallers)
		}
		if len(res.TotalImpacted) != 2 {
			t.Errorf("expected 2 total impacted (B and C), got %v", res.TotalImpacted)
		}
	})

	t.Run("diff with multiple files", func(t *testing.T) {
		res := CalculateDiffBlastRadius(graph, []string{"pkg/file1.go", "pkg/file2.go"}, 3)
		if len(res.TotalImpacted) < 1 {
			t.Errorf("expected impacted symbols, got %v", res.TotalImpacted)
		}
	})

	t.Run("diff with empty files", func(t *testing.T) {
		res := CalculateDiffBlastRadius(graph, []string{}, 3)
		if len(res.TotalImpacted) != 0 {
			t.Errorf("expected 0 impacted, got %d", len(res.TotalImpacted))
		}
	})
}
