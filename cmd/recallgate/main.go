// Command recallgate runs the P8 recall regression gate (issue #162): it
// measures recall@10 / nDCG@10 of the production fusion semantics (position-
// only RRF k=60) over the golden deterministic corpus and compares against
// the pinned thresholds.
//
// Fully offline and deterministic: no network, no live embedding provider,
// no wall-clock influence on the verdict. Exit code 0 = thresholds met,
// 1 = regression, 2 = operational failure (bad thresholds, harness error).
//
// Flags:
//
//	-thresholds path   thresholds JSON (default: embedded, must equal
//	                   bench/recall/thresholds.json — enforced by a test)
//	-json path         optional path to write the full JSON report; when it
//	                   matches a checked-in golden file, the report is the
//	                   baseline artifact (bench/recall/golden-report.json)
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/lleontor705/cortex/v2/bench/recall"
)

func main() {
	var (
		thresholds = flag.String("thresholds", "", "path to thresholds JSON (default: embedded gate contract)")
		out        = flag.String("json", "", "optional path to write the full JSON report")
	)
	flag.Parse()

	th := recall.DefaultThresholds()
	if *thresholds != "" {
		loaded, err := recall.LoadThresholds(*thresholds)
		if err != nil {
			fmt.Fprintf(os.Stderr, "recallgate: %v\n", err)
			os.Exit(2)
		}
		th = loaded
	}

	result, err := recall.Gate(th)
	if err != nil {
		fmt.Fprintf(os.Stderr, "recallgate: %v\n", err)
		os.Exit(2)
	}
	_, report, err := recall.Measure()
	if err != nil {
		fmt.Fprintf(os.Stderr, "recallgate: %v\n", err)
		os.Exit(2)
	}

	if *out != "" {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "recallgate: marshal report: %v\n", err)
			os.Exit(2)
		}
		if err := os.WriteFile(*out, append(data, '\n'), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "recallgate: write report: %v\n", err)
			os.Exit(2)
		}
	}

	fmt.Printf("Recall Gate — production fusion (%s) over the golden deterministic corpus\n", th.Fusion)
	fmt.Printf("  corpus: %d docs/query, %d queries, top-k %d, seed %d\n",
		th.CorpusSize, th.QueryCount, th.TopK, th.Seed)
	fmt.Printf("  measured recall@%d: %.4f (floor %.4f)\n", th.TopK, result.MeasuredRecallAtK, th.MinRecallAtK)
	fmt.Printf("  measured nDCG@%d:   %.4f (floor %.4f)\n", th.TopK, result.MeasuredNDCGAtK, th.MinNDCGAtK)
	if result.Passed {
		fmt.Println("  verdict: PASS — no retrieval-quality regression against the pinned thresholds")
		return
	}
	fmt.Println("  verdict: FAIL — retrieval quality regressed below the pinned thresholds:")
	for _, reason := range result.Reasons {
		fmt.Printf("    - %s\n", reason)
	}
	fmt.Println("  This gate is advisory until promoted to required; see bench/recall/gate.go for the promotion path.")
	os.Exit(1)
}
