package bench

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRetrievalBaselineDocumentationContract(t *testing.T) {
	t.Parallel()

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve documentation test location")
	}
	repositoryRoot := filepath.Dir(filepath.Dir(currentFile))

	tests := []struct {
		path     string
		required []string
	}{
		{
			path: filepath.Join(repositoryRoot, "docs", "BENCHMARKS.md"),
			required: []string{
				"## What the baseline proves",
				"## What the baseline does not prove",
				"## Versioned retrieval protocol",
				"### Splits and evaluator",
				"### Metrics and uncertainty",
				"### Hardware and resources",
				"### Release gates",
				"## External and Cortex-reproduced evidence",
				"## Reproduce the baseline",
				"## Licences and limitations",
			},
		},
		{
			path: filepath.Join(repositoryRoot, "bench", "README.md"),
			required: []string{
				"## Retrieval baseline quick path",
				"go test -v -count=1 ./bench -run TestRetrievalBaselineDocumentationContract",
				"go test -v -count=1 ./bench/...",
				"LOCOMO, DMR, and LongMemEval",
				"stable-ID retrieval relevance",
			},
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(filepath.Base(testCase.path), func(t *testing.T) {
			document, err := os.ReadFile(testCase.path)
			if err != nil {
				t.Fatalf("read %s: %v", testCase.path, err)
			}
			for _, marker := range testCase.required {
				if !strings.Contains(string(document), marker) {
					t.Errorf("%s is missing required marker %q", testCase.path, marker)
				}
			}
		})
	}
}
