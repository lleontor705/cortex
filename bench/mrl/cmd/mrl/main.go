// Command mrl runs the REQ-RET-103 MRL recall decision-gate spike and prints
// the report (human-readable summary plus optional JSON artifact).
//
// The harness is fully offline and deterministic: no network, no model, no
// dataset download, no database. It never mutates production code paths; the
// Verdict field gates ret-202..205 planning (GO) or routes to ret-206 (NO-GO,
// RaBitQ fallback decision record per REQ-RET-104). The measured corpus is
// synthetic — see the embedded synthetic-data caveat in the report.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/lleontor705/cortex/v2/bench/mrl"
)

func main() {
	var (
		corpus    = flag.Int("corpus", mrl.DefaultCorpusSize, "synthetic corpus size (4096-dim MRL-like documents)")
		randomQ   = flag.Int("random-queries", mrl.DefaultRandomQueries, "number of random evaluation queries")
		hardQ     = flag.Int("hard-queries", mrl.DefaultHardQueries, "number of hard near-duplicate distractor queries")
		shortlist = flag.Int("shortlist", mrl.DefaultShortlistK, "halfvec subvector shortlist K before full-precision re-rank")
		topK      = flag.Int("topk", mrl.DefaultTopK, "evaluation cutoff k (recall@k)")
		seed      = flag.Int64("seed", mrl.DefaultSeed, "deterministic corpus seed")
		passes    = flag.Int("latency-passes", mrl.DefaultLatencyPasses, "timed passes over the query set (latency is informational)")
		out       = flag.String("out", "", "optional path to write the full JSON report")
	)
	flag.Parse()

	rep, err := mrl.Run(mrl.Config{
		CorpusSize:    *corpus,
		RandomQueries: *randomQ,
		HardQueries:   *hardQ,
		ShortlistK:    *shortlist,
		TopK:          *topK,
		Seed:          *seed,
		LatencyPasses: *passes,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "mrl harness: %v\n", err)
		os.Exit(1)
	}

	if *out != "" {
		data, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "mrl harness: marshal report: %v\n", err)
			os.Exit(1)
		}
		if err := os.WriteFile(*out, append(data, '\n'), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "mrl harness: write report: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("report written to %s\n", *out)
	}

	fmt.Printf("MRL recall decision-gate report (REQ-RET-103, schema %s)\n", rep.SchemaVersion)
	fmt.Printf("  corpus: %d docs x %d dims, %d random + %d hard queries, top-k %d, shortlist K %d, seed %d\n",
		rep.Config.CorpusSize, mrl.DefaultDims, rep.Config.RandomQueries, rep.Config.HardQueries,
		rep.Config.TopK, rep.Config.ShortlistK, rep.Config.Seed)
	for _, s := range []mrl.StrategyReport{rep.Baseline, rep.SubvectorRerank, rep.SubvectorDirect} {
		fmt.Printf("  %-24s recall@%d: combined=%.4f random=%.4f hard=%.4f  p50=%dns/query\n",
			s.Name, rep.Config.TopK, s.RecallAt10Combined, s.RecallAt10Random, s.RecallAt10Hard,
			int64(s.P50LatencyNSPerQuery))
	}
	p := rep.Projection
	fmt.Printf("  latency projection: %d rows, ef_search=%d, hop factor %.0f, K=%d -> exact %.0fns vs ANN %.0fns = %.0fx projected (>= %.0fx gate); measured harness scan speedup %.2fx (linear-scan bound)\n",
		p.CorpusRows, p.EFSearch, p.GraphHopFactor, p.ShortlistK,
		p.ProjectedExactNS, p.ProjectedANNNS, p.Speedup, mrl.GoLatencySpeedup, p.MeasuredScanSpeedup)
	fmt.Printf("  gates: recall_pass=%v latency_pass=%v\n", rep.RecallGatePass, rep.LatencyGatePass)
	fmt.Printf("  VERDICT: %s\n", strings.ToUpper(rep.Verdict[:1])+rep.Verdict[1:])
	fmt.Printf("  rationale: %s\n", rep.Rationale)
	fmt.Printf("  caveat: %s\n", rep.SyntheticDataCaveat)
	fmt.Printf("  contract: %s\n", rep.ContractNote)
	fmt.Printf("  migration-114 outlook:\n%s\n", rep.DDLOutlook)
}
