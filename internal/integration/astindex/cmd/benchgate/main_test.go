package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const hdr, v2c = "# benchgate: variant=legacy scenario=full-cold", "# benchgate: variant=v2 scenario=full-cold"

func mkFile(t *testing.T, lines ...string) string {
	p := filepath.Join(t.TempDir(), "b.txt")
	_ = os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	return p
}
func file(h, name string, n int, base float64) (out []string) {
	out = append(out, h)
	for i := 0; i < n; i++ {
		out = append(out, fmt.Sprintf("%s-8\t100\t%.3f ns/op", name, base+float64(i%3)))
	}
	return
}
func eval(t *testing.T, aL, bL []string, maxRatio, alpha float64) int {
	a, _ := parseBenchFile(mkFile(t, aL...))
	b, _ := parseBenchFile(mkFile(t, bL...))
	v, _ := evaluate(a, b, maxRatio, alpha)
	return v
}
func chk(t *testing.T, label string, got, want int) {
	if got != want {
		t.Fatalf("%s: got %v, want %v", label, got, want)
	}
}
func warm(extra string) (l []string) {
	l = []string{"# benchgate: variant=v2 scenario=full-warm"}
	for i := 0; i < 10; i++ {
		l = append(l, fmt.Sprintf("%s-8\t100\t%d.0 ns/op%s", benchName, 500+i, extra))
	}
	return
}
func TestBenchgate_SameName(t *testing.T) {
	chk(t, "both-other-name", eval(t, file(hdr, "BenchmarkOtherE2E", 10, 900), file(hdr, "BenchmarkOtherE2E", 10, 400), 0.5, 0.05), V_FAIL)
	chk(t, "legacy-legacy", eval(t, file(hdr, benchName, 10, 1000), file(hdr, benchName, 10, 400), 0.5, 0.05), V_FAIL)
}
func TestBenchgate_Samples(t *testing.T) {
	chk(t, "9 samples", eval(t, file(hdr, benchName, 10, 1000), file(v2c, benchName, 9, 400), 0.5, 0.05), V_FAIL)
}
func TestBenchgate_Ratio(t *testing.T) {
	chk(t, "ratio 2.0 > 0.5", eval(t, file(hdr, benchName, 10, 1000), file(v2c, benchName, 10, 2000), 0.5, 1.0), V_FAIL)
	chk(t, "ratio 0.4", eval(t, file(hdr, benchName, 10, 1000), file(v2c, benchName, 10, 400), 0.5, 1.0), V_PASS)
	chk(t, "ratio cap>1 usage", run([]string{"-manifest", "m", "-pair", "a,b", "-max-ratio", "2", "-alpha", "0.05"}), 3)
}
func TestBenchgate_Significance(t *testing.T) {
	chk(t, "clear separation", eval(t, file(hdr, benchName, 10, 1000), file(v2c, benchName, 10, 100), 0.5, 0.05), V_PASS)
	na, nb := []string{hdr}, []string{v2c}
	for i, v := range []int{100, 101, 102, 103, 104, 105, 1005, 1006, 1007, 1008, 1009} {
		na, nb = append(na, fmt.Sprintf("BenchmarkASTE2E-8\t100\t%d ns/op", 1000+i)), append(nb, fmt.Sprintf("BenchmarkASTE2E-8\t100\t%d ns/op", v))
	}
	chk(t, "noise", eval(t, na, nb, 0.5, 0.05), V_INCONCLUSIVE)
	chk(t, "all-ties", eval(t, append([]string{hdr}, strings.Split(strings.Repeat("BenchmarkASTE2E-8\t100\t1000.000 ns/op\n", 10), "\n")...), append([]string{v2c}, strings.Split(strings.Repeat("BenchmarkASTE2E-8\t100\t1000.000 ns/op\n", 10), "\n")...), 1.0, 0.05), V_INCONCLUSIVE)
	x, y := []float64{1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000}, []float64{2000, 2000, 2000, 2000, 2000, 2000, 2000, 2000, 2000, 2000}
	if p, q := mannWhitney(x, y), mannWhitney(y, x); p != q {
		t.Fatalf("bilateral asymmetry: p(fwd)=%g != p(rev)=%g", p, q)
	}
}
func TestBenchgate_MissingFails(t *testing.T) {
	chk(t, "nil pair", func() (v int) { v, _ = evaluate(nil, nil, 0.5, 0.05); return }(), V_FAIL)
	chk(t, "warm parse1", eval(t, file(v2c, benchName, 10, 1000), warm("\t1 parse/op\t0 writes/op\t100 hit%"), 0.6, 1.0), V_FAIL)
	chk(t, "warm benchmem-extras", eval(t, file(v2c, benchName, 10, 1000), warm("\t0 parse/op\t0 writes/op\t100 hit%\t512 B/op\t8 allocs/op"), 0.6, 1.0), V_PASS)
}
func TestBenchgate_FrozenDigest(t *testing.T) {
	m := map[string]any{"commit": "c1", "corpus": "k", "blobs": "b", "cpu": "x", "power_plan": "p", "evaluator_sha256": "e", "go_version": "g", "goos": "w", "goarch": "a", "gomaxprocs": "8"}
	want := frozenDigest(m)
	raw, _ := json.Marshal(m)
	if err := checkManifest(raw, envInfo{procs: 8}, want); err != nil {
		t.Fatal(err)
	}
	for _, k := range [5]string{"blobs", "commit", "corpus", "cpu", "power_plan"} {
		m[k] = "wrong"
		raw, _ = json.Marshal(m)
		if err := checkManifest(raw, envInfo{procs: 8}, want); err == nil {
			t.Fatalf("wrong %s must be blocked", k)
		}
	}
}
func TestBenchgate_UnitAndFiniteMetrics(t *testing.T) {
	mixed := append(file(hdr, benchName, 1, 1000), strings.Split(strings.TrimSuffix(strings.Repeat("BenchmarkASTE2E-8\t100\t400.000 s/op\t8 allocs/op\n", 9), "\n"), "\n")...)
	nan := strings.Split(strings.ReplaceAll(strings.Join(file(v2c, benchName, 10, 400), "\n"), " ns/op", " ns/op\tNaN B/op"), "\n")
	inf := strings.Split(strings.ReplaceAll(strings.Join(file(v2c, benchName, 10, 400), "\n"), " ns/op", " ns/op\t+Inf B/op"), "\n")
	for name, lines := range map[string][]string{"mixed-unit": mixed, "nan-metric": nan, "inf-metric": inf, "nan-ns/op": {hdr, "BenchmarkASTE2E-8\t100\tNaN ns/op"}, "empty": {}} {
		if _, err := parseBenchFile(mkFile(t, lines...)); err == nil {
			t.Fatalf("%s must fail-closed at parse", name)
		}
	}
	base, _ := parseBenchFile(mkFile(t, file(hdr, benchName, 10, 1000)...))
	tgt, _ := parseBenchFile(mkFile(t, mixed...))
	chk(t, "mixed-unit target cannot PASS", func() (v int) { v, _ = evaluate(base, tgt, 1, 1); return }(), V_FAIL)
}
