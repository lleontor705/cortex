package code

import (
	"testing"
)

func TestIsTestFilePath(t *testing.T) {
	cases := []struct {
		path     string
		expected bool
	}{
		{"internal/domain/code/repomap_test.go", true},
		{"internal/domain/code/repomap.go", false},
		{"tests/test_api.py", true},
		{"src/api.py", false},
		{"web/components/Button.test.tsx", true},
		{"web/components/Button.spec.ts", true},
		{"web/components/Button.tsx", false},
		{"src/test/java/com/example/AppTest.java", true},
	}

	for _, c := range cases {
		got := IsTestFilePath(c.path)
		if got != c.expected {
			t.Errorf("IsTestFilePath(%q) = %v; want %v", c.path, got, c.expected)
		}
	}
}

func TestFindImpactedTests_DirectAndTransitive(t *testing.T) {
	graph := &CodeGraph{
		Project: "test-proj",
		Symbols: []Symbol{
			{ID: "func:pkg.TargetFunc", Name: "TargetFunc", Kind: KindFunc, FilePath: "pkg/target.go"},
			{ID: "func:pkg.HelperFunc", Name: "HelperFunc", Kind: KindFunc, FilePath: "pkg/helper.go"},
			{ID: "func:pkg.TestDirect", Name: "TestDirect", Kind: KindFunc, FilePath: "pkg/target_test.go", LineNumber: 10},
			{ID: "func:pkg.TestIndirect", Name: "TestIndirect", Kind: KindFunc, FilePath: "pkg/helper_test.go", LineNumber: 25},
			{ID: "func:pkg.UnrelatedTest", Name: "TestUnrelated", Kind: KindFunc, FilePath: "pkg/other_test.go", LineNumber: 50},
		},
		Relations: []Relation{
			{SourceID: "func:pkg.HelperFunc", TargetID: "func:pkg.TargetFunc", Relation: RelationCalls},
			{SourceID: "func:pkg.TestDirect", TargetID: "func:pkg.TargetFunc", Relation: RelationCalls},
			{SourceID: "func:pkg.TestIndirect", TargetID: "func:pkg.HelperFunc", Relation: RelationCalls},
		},
	}

	res := FindImpactedTests(graph, "TargetFunc", 3)
	if res.TargetKind != "symbol" {
		t.Fatalf("expected TargetKind symbol, got %s", res.TargetKind)
	}

	if len(res.ImpactedTestFiles) != 2 {
		t.Fatalf("expected 2 test files, got %d: %v", len(res.ImpactedTestFiles), res.ImpactedTestFiles)
	}

	if len(res.ImpactedTestFuncs) != 2 {
		t.Fatalf("expected 2 test functions, got %d", len(res.ImpactedTestFuncs))
	}

	// Verify direct test is ranked first
	if !res.ImpactedTestFuncs[0].Direct || res.ImpactedTestFuncs[0].Name != "TestDirect" {
		t.Errorf("expected TestDirect to be first (direct), got %+v", res.ImpactedTestFuncs[0])
	}
	if res.ImpactedTestFuncs[1].Direct || res.ImpactedTestFuncs[1].Name != "TestIndirect" {
		t.Errorf("expected TestIndirect to be second (indirect), got %+v", res.ImpactedTestFuncs[1])
	}

	// Verify recommended commands
	if len(res.RecommendedCommands) == 0 {
		t.Fatalf("expected at least 1 recommended command")
	}
	cmd := res.RecommendedCommands[0]
	if cmd != "go test -v -count=1 -run '^(TestDirect|TestIndirect)$' ./pkg" {
		t.Errorf("unexpected command: %s", cmd)
	}
}

func TestFindImpactedTests_ByFilePath(t *testing.T) {
	graph := &CodeGraph{
		Project: "test-proj",
		Symbols: []Symbol{
			{ID: "func:pkg.TargetFunc", Name: "TargetFunc", Kind: KindFunc, FilePath: "pkg/target.go"},
			{ID: "func:pkg.TestTarget", Name: "TestTarget", Kind: KindFunc, FilePath: "pkg/target_test.go"},
		},
		Relations: []Relation{
			{SourceID: "func:pkg.TestTarget", TargetID: "func:pkg.TargetFunc", Relation: RelationCalls},
		},
	}

	res := FindImpactedTests(graph, "pkg/target.go", 2)
	if res.TargetKind != "file" {
		t.Fatalf("expected TargetKind file, got %s", res.TargetKind)
	}
	if len(res.ImpactedTestFuncs) != 1 || res.ImpactedTestFuncs[0].Name != "TestTarget" {
		t.Fatalf("expected TestTarget function, got %+v", res.ImpactedTestFuncs)
	}
}
