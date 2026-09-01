// A0.3 contract oracles: TestBenchmarkContract_(Flags|Name|Metrics|Executed) per cortex:observation/1691#A0.3.
package astindex

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestBenchmarkContract_Flags(t *testing.T) {
	cases := []struct {
		v, s string
		ok   bool
	}{
		{"legacy", "full-cold", true},
		{"legacy", "changed-content", true},
		{"v2", "full-cold", true},
		{"v2", "full-warm", true},
		{"prototype", "changed-content", true},
		{"", "", false},
		{"legacy", "", false},
		{"", "full-cold", false},
		{"bogus", "full-cold", false},
		{"legacy", "full-warm", false},
		{"v2", "changed-content", false},
		{"prototype", "full-cold", false},
		{"LEGACY", "full-cold", false},
		{"legacy", "FULL-COLD", false},
	}
	for _, c := range cases {
		err := validateASTFlags(c.v, c.s)
		if c.ok && err != nil {
			t.Errorf("valid %q/%q rejected: %v", c.v, c.s, err)
		}
		if !c.ok && err == nil {
			t.Errorf("fail-open: %q/%q accepted", c.v, c.s)
		}
	}
}

func TestBenchmarkContract_Name(t *testing.T) {
	f := runtime.FuncForPC(reflect.ValueOf(BenchmarkASTE2E).Pointer())
	if f == nil || !strings.HasSuffix(f.Name(), ".BenchmarkASTE2E") {
		t.Fatalf("single exact name required, got %v", f)
	}
	h := benchgateHeader("legacy", "full-cold")
	if h != "# benchgate: variant=legacy scenario=full-cold" {
		t.Fatalf("header format: %q", h)
	}
	// Mirror benchgate's header grammar (CutPrefix on variant=/scenario= tokens).
	var variant, scenario string
	for _, tok := range strings.Fields(strings.TrimPrefix(h, "# benchgate:")) {
		for i, dst := range []*string{&variant, &scenario} {
			if v, ok := strings.CutPrefix(tok, [...]string{"variant=", "scenario="}[i]); ok && *dst == "" {
				*dst = v
			}
		}
	}
	if variant != "legacy" || scenario != "full-cold" {
		t.Fatalf("benchgate cannot parse header %q -> %q/%q", h, variant, scenario)
	}
}

func TestBenchmarkContract_Metrics(t *testing.T) {
	files, err := loadCorpus(astCorpusDir)
	if err != nil {
		t.Fatal(err)
	}
	n := len(files)
	perOp, err := legacyFullScan(files, "full-cold")
	if err != nil {
		t.Fatal(err)
	}
	m, err := astMetrics(perOp, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 3 || m["parse/op"] != float64(n) || m["writes/op"] != float64(n) || m["hit%"] != 0 {
		t.Fatalf("legacy metrics wrong: %v (files=%d)", m, n)
	}
	// The future v2 full-warm trio must be exactly 0/0/100 (benchgate equality gate).
	warm, err := astMetrics(astCounters{lookups: 10, hits: 10}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if warm["parse/op"] != 0 || warm["writes/op"] != 0 || warm["hit%"] != 100 {
		t.Fatalf("warm trio must be exactly 0/0/100: %v", warm)
	}
	if _, err := astMetrics(perOp, 0); err == nil {
		t.Fatal("zero ops must fail closed")
	}
}

func TestBenchmarkContract_Executed(t *testing.T) {
	files, err := loadCorpus(astCorpusDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 3 {
		t.Fatalf("frozen corpus too small: %d files", len(files))
	}
	// Determinism: both legacy scenarios scan identically.
	a, err := legacyFullScan(files, "full-cold")
	if err != nil {
		t.Fatal(err)
	}
	b, err := legacyFullScan(files, "changed-content")
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("legacy scan must be deterministic: %+v vs %+v", a, b)
	}
	// Exact copy keeps the content-addressed anchor; tampering/missing/empty fail closed.
	dir := t.TempDir()
	if _, err := loadCorpus(dir); err == nil {
		t.Fatal("empty corpus dir must fail closed")
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f.rel), f.data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := loadCorpus(dir); err != nil {
		t.Fatalf("exact corpus copy must keep anchor: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, files[0].rel), append([]byte("x"), files[0].data...), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCorpus(dir); err == nil {
		t.Fatal("tampered corpus must fail closed")
	}
	if _, err := loadCorpus(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing corpus dir must fail closed")
	}
	// A0.3 must not fake v2/prototype runners before their DAG nodes.
	for _, v := range []string{"v2", "prototype"} {
		if _, ok := astRunners[v]; ok {
			t.Fatalf("variant %q registered before its DAG node", v)
		}
	}
	// End-to-end execution through the real benchmark entry point.
	oldV, oldS := *astVariantFlag, *astScenarioFlag
	*astVariantFlag, *astScenarioFlag = "legacy", "full-cold"
	defer func() { *astVariantFlag, *astScenarioFlag = oldV, oldS }()
	res := testing.Benchmark(BenchmarkASTE2E)
	if res.N <= 0 || res.NsPerOp() <= 0 {
		t.Fatalf("benchmark did not execute: N=%d ns/op=%v", res.N, res.NsPerOp())
	}
	if p := res.Extra["parse/op"]; p != float64(len(files)) {
		t.Fatalf("parse/op=%v want %d", p, len(files))
	}
	if _, ok := res.Extra["writes/op"]; !ok {
		t.Fatal("writes/op metric missing")
	}
	if h := res.Extra["hit%"]; h != 0 {
		t.Fatalf("hit%%=%v want 0", h)
	}
}
