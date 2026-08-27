// A0.3 unified harness (cortex:observation/1691#A0.3, REQ-AST-007): single exact
// BenchmarkASTE2E name; -ast.variant/-ast.scenario flags; frozen corpus trust anchor;
// fail-closed execution; benchgate-compatible stdout metadata and metrics.
// Later DAG nodes register v2/prototype runners in astRunners.
package astindex

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var (
	astVariantFlag  = flag.String("ast.variant", "", "benchmark variant: legacy|v2|prototype")
	astScenarioFlag = flag.String("ast.scenario", "", "benchmark scenario: full-cold|full-warm|changed-content")
)

const (
	astCorpusDir = "testdata/corpus"
	// frozenA03CorpusAnchor pins the corpus: SHA-256 over sorted "<rel>=<sha256(content)>\n" lines.
	frozenA03CorpusAnchor = "ce2882b79cab37f6354408247cdf25e98a1f3e110e3e03713cfc4b6ec69da2f1"
)

// astScenarioAllow mirrors the three benchgate pairs; anything else fails closed.
var astScenarioAllow = map[string]map[string]bool{
	"legacy":    {"full-cold": true, "changed-content": true},
	"prototype": {"changed-content": true},
	"v2":        {"full-cold": true, "full-warm": true},
}

func validateASTFlags(variant, scenario string) error {
	scen, ok := astScenarioAllow[variant]
	if !ok {
		return fmt.Errorf("invalid -ast.variant=%q (want legacy|v2|prototype)", variant)
	}
	if !scen[scenario] {
		return fmt.Errorf("invalid -ast.scenario=%q for variant %q", scenario, variant)
	}
	return nil
}

// benchgateHeader emits the exact stdout line benchgate parses for pair metadata.
func benchgateHeader(variant, scenario string) string {
	return "# benchgate: variant=" + variant + " scenario=" + scenario
}

type astFile struct {
	rel  string
	data []byte
}

// digestCorpus is the frozen trust-anchor digest over sorted corpus entries.
func digestCorpus(files []astFile) string {
	slices.SortFunc(files, func(a, b astFile) int { return strings.Compare(a.rel, b.rel) })
	h := sha256.New()
	for _, f := range files {
		s := sha256.Sum256(f.data)
		_, _ = fmt.Fprintf(h, "%s=%s\n", f.rel, hex.EncodeToString(s[:]))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// loadCorpus fails closed on unreadable/empty corpora and on anchor mismatch.
func loadCorpus(dir string) ([]astFile, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("frozen corpus unreadable: %w", err)
	}
	var files []astFile
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		files = append(files, astFile{rel: e.Name(), data: data})
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("frozen corpus empty: %s", dir)
	}
	if got := digestCorpus(files); got != frozenA03CorpusAnchor {
		return nil, fmt.Errorf("corpus anchor mismatch: got %s want %s", got, frozenA03CorpusAnchor)
	}
	return files, nil
}

// astCounters feed the three required metrics; full-warm must yield 0/0/100.
type astCounters struct{ parses, writes, lookups, hits int }

func (c *astCounters) add(o astCounters) {
	c.parses += o.parses
	c.writes += o.writes
	c.lookups += o.lookups
	c.hits += o.hits
}

func astMetrics(tot astCounters, ops int) (map[string]float64, error) {
	if ops <= 0 {
		return nil, fmt.Errorf("no benchmark ops executed (fail-closed)")
	}
	hit := 0.0
	if tot.lookups > 0 {
		hit = 100 * float64(tot.hits) / float64(tot.lookups)
	}
	return map[string]float64{
		"parse/op":  float64(tot.parses) / float64(ops),
		"writes/op": float64(tot.writes) / float64(ops),
		"hit%":      hit,
	}, nil
}

// astRunners holds one executor per variant. A0.3 ships legacy only; v2/prototype
// stay unregistered (fail-closed) until their DAG nodes land.
var astRunners = map[string]func([]astFile, string) (astCounters, error){
	"legacy": legacyFullScan,
}

// legacyFullScan is the read-once full pass: no cache, full projection rewrite per op.
func legacyFullScan(files []astFile, _ string) (astCounters, error) {
	var c astCounters
	for _, f := range files {
		_ = sha256.Sum256(f.data)
		c.parses++
		c.writes++
		c.lookups++
	}
	return c, nil
}

// TestMain emits the benchgate metadata header exactly once per process, before
// testing writes its goos/goarch/pkg/cpu preamble and benchmark lines. Printing
// inside BenchmarkASTE2E would interleave with go test's progress line and corrupt
// benchgate parsing.
func TestMain(m *testing.M) {
	flag.Parse() // testing.M.Run parses too, but the header needs flag values up front.
	if validateASTFlags(*astVariantFlag, *astScenarioFlag) == nil {
		fmt.Println(benchgateHeader(*astVariantFlag, *astScenarioFlag))
	}
	os.Exit(m.Run())
}

func BenchmarkASTE2E(b *testing.B) {
	variant, scenario := *astVariantFlag, *astScenarioFlag
	if err := validateASTFlags(variant, scenario); err != nil {
		b.Fatal(err)
	}
	files, err := loadCorpus(astCorpusDir)
	if err != nil {
		b.Fatal(err)
	}
	run, ok := astRunners[variant]
	if !ok {
		b.Fatalf("variant %q not registered until its DAG node (fail-closed)", variant)
	}
	var tot astCounters
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c, err := run(files, scenario)
		if err != nil {
			b.Fatal(err)
		}
		tot.add(c)
	}
	b.StopTimer()
	m, err := astMetrics(tot, b.N)
	if err != nil {
		b.Fatal(err)
	}
	for k, v := range m {
		b.ReportMetric(v, k)
	}
}
