// Command fusion runs the REQ-RET-106 fusion A/B decision-gate harness and
// prints the report (human-readable summary plus optional JSON artifact).
//
// The harness is fully offline and deterministic: no network, no model, no
// dataset download. It never mutates production fusion; the recommendation
// field is advisory and requires an explicit decision-gate approval before
// any production change (REQ-RET-002 score-as-rank stays intact).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/lleontor705/cortex/v2/bench/fusion"
)

func main() {
	var (
		corpusSize = flag.Int("corpus", 200, "synthetic corpus size (documents per query)")
		queries    = flag.Int("queries", 60, "number of synthetic queries")
		topK       = flag.Int("topk", fusion.DefaultTopK, "evaluation cutoff k for recall/precision/nDCG")
		seed       = flag.Int64("seed", fusion.DefaultSeed, "deterministic corpus seed")
		tuningFrac = flag.Float64("tuning-fraction", fusion.DefaultTuningFraction, "fraction of queries held out for alpha calibration")
		gridStep   = flag.Float64("alpha-grid-step", fusion.DefaultAlphaGridStep, "alpha calibration grid resolution")
		out        = flag.String("out", "", "optional path to write the full JSON report")
	)
	flag.Parse()

	rep, err := fusion.Run(fusion.Config{
		CorpusSize:     *corpusSize,
		QueryCount:     *queries,
		TopK:           *topK,
		Seed:           *seed,
		TuningFraction: *tuningFrac,
		AlphaGridStep:  *gridStep,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "fusion harness: %v\n", err)
		os.Exit(1)
	}

	if *out != "" {
		data, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "fusion harness: marshal report: %v\n", err)
			os.Exit(1)
		}
		if err := os.WriteFile(*out, append(data, '\n'), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "fusion harness: write report: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("report written to %s\n", *out)
	}

	fmt.Printf("Fusion A/B decision-gate report (schema %s)\n", rep.SchemaVersion)
	fmt.Printf("  corpus: %d docs/query, %d queries (tuning %d, eval %d), top-k %d, seed %d\n",
		rep.Config.CorpusSize, rep.Config.QueryCount+rep.Config.QueryCount*0,
		rep.TuningQueries, rep.EvalQueries, rep.Config.TopK, rep.Config.Seed)
	for _, s := range rep.Strategies {
		fmt.Printf("  %-20s recall@k=%.4f precision@k=%.4f ndcg@k=%.4f mrr=%.4f latency=%dns/query\n",
			s.Name, s.RecallAtK, s.PrecisionAtK, s.NDCGAtK, s.MRR, int64(s.LatencyNSPerQuery))
	}
	fmt.Printf("  calibrated alpha: %.2f\n", rep.CalibratedAlpha)
	fmt.Printf("  recommendation:   %s\n", rep.Recommendation)
	fmt.Printf("  rationale:        %s\n", rep.Rationale)
	fmt.Printf("  contract:         %s\n", rep.ContractNote)
}
