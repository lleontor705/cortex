package code

import (
	"testing"
)

func TestComputeAnalytics(t *testing.T) {
	graph := &CodeGraph{
		Project: "cortex-core",
		Symbols: []Symbol{
			{ID: "sym1", Name: "Server", Kind: KindStruct, FilePath: "server/server.go", PackageName: "server"},
			{ID: "sym2", Name: "HandleHTTP", Kind: KindFunc, FilePath: "server/http.go", PackageName: "server"},
			{ID: "sym3", Name: "Store", Kind: KindInterface, FilePath: "store/store.go", PackageName: "store"},
			{ID: "sym4", Name: "NewStore", Kind: KindFunc, FilePath: "store/store.go", PackageName: "store"},
		},
		Relations: []Relation{
			{SourceID: "sym2", TargetID: "sym1", Relation: RelationCalls},
			{SourceID: "sym1", TargetID: "sym3", Relation: RelationUses},
			{SourceID: "sym4", TargetID: "sym3", Relation: RelationImplements},
			{SourceID: "sym2", TargetID: "sym3", Relation: RelationCalls},
		},
	}

	report := ComputeAnalytics(graph)

	if report.TotalSymbols != 4 {
		t.Fatalf("expected 4 symbols, got %d", report.TotalSymbols)
	}
	if report.TotalRelations != 4 {
		t.Fatalf("expected 4 relations, got %d", report.TotalRelations)
	}
	if report.TotalFiles != 3 {
		t.Fatalf("expected 3 files, got %d", report.TotalFiles)
	}
	if len(report.GodNodes) == 0 {
		t.Fatalf("expected at least 1 god node")
	}
	// sym3 has in-degree 3, should be the top god node
	if report.GodNodes[0].ID != "sym3" {
		t.Errorf("expected top god node to be sym3, got %s", report.GodNodes[0].ID)
	}
}

func TestComputeAnalyticsCycles(t *testing.T) {
	graph := &CodeGraph{
		Project: "cycle-test",
		Symbols: []Symbol{
			{ID: "a", Name: "FuncA", Kind: KindFunc, FilePath: "pkg/a.go", PackageName: "pkgA"},
			{ID: "b", Name: "FuncB", Kind: KindFunc, FilePath: "pkg/b.go", PackageName: "pkgB"},
		},
		Relations: []Relation{
			{SourceID: "a", TargetID: "b", Relation: RelationCalls},
			{SourceID: "b", TargetID: "a", Relation: RelationCalls},
		},
	}

	report := ComputeAnalytics(graph)
	if len(report.ImportCycles) == 0 {
		t.Fatalf("expected at least 1 cycle detected")
	}
}
