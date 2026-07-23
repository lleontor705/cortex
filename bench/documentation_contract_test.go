package bench

import (
	"os"
	"path/filepath"
	"regexp"
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

	benchmarkPath := filepath.Join(repositoryRoot, "docs", "BENCHMARKS.md")
	benchmarkDocument, err := os.ReadFile(benchmarkPath)
	if err != nil {
		t.Fatalf("read %s: %v", benchmarkPath, err)
	}
	benchmarkText := string(benchmarkDocument)

	legacyStart := strings.Index(benchmarkText, "## Results Summary")
	legacyEnd := strings.Index(benchmarkText, "## Methodology")
	if legacyStart == -1 || legacyEnd == -1 || legacyEnd <= legacyStart {
		t.Fatal("legacy results must remain in a distinct Results Summary section before Methodology")
	}
	legacyResults := benchmarkText[legacyStart:legacyEnd]
	for _, marker := range []string{
		"**Evidence classification:**",
		"**Evidence identity:**",
		"**Evaluator classification:**",
		"**Comparability:**",
	} {
		if !strings.Contains(legacyResults, marker) {
			t.Errorf("legacy results are missing structural evidence marker %q", marker)
		}
	}

	forbiddenClaims := []struct {
		name    string
		pattern *regexp.Regexp
	}{
		{name: "unsupported external LOCOMO accuracy", pattern: regexp.MustCompile(`(?i)Engram reports\s+80%\s+LOCOMO`)},
		{name: "unsupported retrieval multiplier", pattern: regexp.MustCompile(`(?i)(vector search improves retrieval|overall improvement).{0,30}12\s*[-–]\s*37x`)},
		{name: "unsupported provider parity", pattern: regexp.MustCompile(`(?i)Ollama.{0,20}matches OpenAI`)},
		{name: "unsupported dimensionality cause", pattern: regexp.MustCompile(`(?i)due to higher[- ]dimensional embeddings`)},
		{name: "unsupported provider speed claim", pattern: regexp.MustCompile(`(?i)Ollama is\s+1[.]7x faster`)},
		{name: "unsupported network-latency cause", pattern: regexp.MustCompile(`(?i)no network latency for inference`)},
		{name: "unsupported absolute temporal limitation", pattern: regexp.MustCompile(`(?i)FTS5 cannot answer temporal questions at all`)},
	}
	for _, claim := range forbiddenClaims {
		if claim.pattern.MatchString(benchmarkText) {
			t.Errorf("benchmark documentation contains %s; remove it or replace it with explicitly scoped evidence", claim.name)
		}
	}
}
