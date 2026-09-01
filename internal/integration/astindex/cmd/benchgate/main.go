// Command benchgate: versioned fail-closed evaluator for exact BenchmarkASTE2E pairs (AST plan A0.2; cortex:observation/1691). Exit 0/1/2/3 = PASS/FAIL/INCONCLUSIVE/BLOCKED.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
)

const benchName, minSamples, V_PASS, V_FAIL, V_INCONCLUSIVE = "BenchmarkASTE2E", 10, 0, 1, 2 // REQ-AST-007: single exact name; >=10 samples

type sample struct {
	ns   float64
	more map[string]float64
}
type benchFile struct {
	name, unit, variant, scenario string
	samples                       []sample
}

func parseBenchFile(path string) (*benchFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	bf := &benchFile{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "# benchgate:"):
			for _, tok := range strings.Fields(strings.TrimPrefix(line, "# benchgate:")) {
				for i, dst := range []*string{&bf.variant, &bf.scenario} {
					if v, ok := strings.CutPrefix(tok, [...]string{"variant=", "scenario="}[i]); ok && *dst == "" {
						*dst = v
					}
				}
			}
		case strings.HasPrefix(line, "Benchmark"):
			fs := strings.Fields(line)
			if len(fs) < 4 {
				return nil, fmt.Errorf("malformed line %q", line)
			}
			name := fs[0]
			if i := strings.LastIndexByte(name, '-'); i > 0 && strings.TrimLeft(name[i+1:], "0123456789") == "" {
				name = name[:i]
			}
			if bf.name == "" {
				bf.name, bf.unit = name, fs[3]
			} else if bf.name != name || bf.unit != fs[3] {
				return nil, fmt.Errorf("name/unit drift %q/%q vs %q/%q", bf.name, bf.unit, name, fs[3])
			}
			nsv, err := strconv.ParseFloat(fs[2], 64)
			if err != nil || !(nsv > 0) || math.IsInf(nsv, 0) {
				return nil, fmt.Errorf("invalid ns/op %q in %q", fs[2], line)
			}
			s := sample{ns: nsv, more: map[string]float64{}}
			for i := 4; i+1 < len(fs); i += 2 {
				x, err := strconv.ParseFloat(fs[i], 64)
				if err != nil || math.IsNaN(x) || math.IsInf(x, 0) {
					return nil, fmt.Errorf("invalid metric %q in %q", fs[i], line)
				}
				s.more[fs[i+1]] = x
			}
			bf.samples = append(bf.samples, s)
		}
	}
	if len(bf.samples) == 0 {
		return nil, fmt.Errorf("no benchmark samples in %s", path)
	}
	return bf, nil
}
func sortedNS(f *benchFile) (xs []float64) {
	xs = make([]float64, len(f.samples))
	for i, s := range f.samples {
		xs[i] = s.ns
	}
	slices.Sort(xs)
	return
}
func mannWhitney(x, y []float64) float64 {
	pooled := append(append([]float64(nil), x...), y...)
	slices.Sort(pooled)
	n1, n2, n := len(x), len(y), len(pooled)
	r1 := 0.0
	for _, xi := range x {
		lo, _ := slices.BinarySearch(pooled, xi)
		hi, _ := slices.BinarySearch(pooled, math.Nextafter(xi, math.Inf(1)))
		r1 += float64(lo) + float64(hi-lo+1)/2
	}
	ties := map[float64]int{}
	for _, p := range pooled {
		ties[p]++
	}
	tieSum := 0.0
	for _, t := range ties {
		tieSum += float64(t*t*t - t)
	}
	sd := math.Sqrt(float64(n1*n2) / 12 * (float64(n+1) - tieSum/(float64(n)*float64(n-1))))
	if sd == 0 {
		return 1
	}
	return math.Min(1, math.Erfc(math.Max(0, math.Abs(r1-float64(n1)*float64(n1+1)/2-float64(n1*n2)/2)-0.5)/sd/math.Sqrt2))
}
func evaluate(a, b *benchFile, maxRatio, alpha float64) (int, string) {
	if a == nil || b == nil {
		return V_FAIL, "missing benchmark"
	}
	if a.name != b.name || a.name != benchName {
		return V_FAIL, fmt.Sprintf("need exact name %q (got %q vs %q)", benchName, a.name, b.name)
	}
	for _, f := range [2]*benchFile{a, b} {
		if f.unit != "ns/op" || len(f.samples) < minSamples {
			return V_FAIL, fmt.Sprintf("need ns/op unit and >=%d samples (got %q, %d)", minSamples, f.unit, len(f.samples))
		}
		if f.scenario == "full-warm" {
			for _, s := range f.samples {
				has := func(k string, want float64) bool { v, ok := s.more[k]; return ok && v == want }
				if !has("parse/op", 0) || !has("writes/op", 0) || !has("hit%", 100) {
					return V_FAIL, "warm requires parse/op=0 writes/op=0 hit%=100 on every sample"
				}
			}
		}
	}
	if p := [4]string{a.variant, a.scenario, b.variant, b.scenario}; p != [4]string{"legacy", "full-cold", "v2", "full-cold"} && p != [4]string{"v2", "full-cold", "v2", "full-warm"} && p != [4]string{"legacy", "changed-content", "prototype", "changed-content"} {
		return V_FAIL, fmt.Sprintf("unexpected pair %s/%s -> %s/%s", a.variant, a.scenario, b.variant, b.scenario)
	}
	xa, xb := sortedNS(a), sortedNS(b)
	ratio := ((xb[(len(xb)-1)/2] + xb[len(xb)/2]) / 2) / ((xa[(len(xa)-1)/2] + xa[len(xa)/2]) / 2)
	if !(ratio <= maxRatio) {
		return V_FAIL, fmt.Sprintf("median ratio %.4f > %.4f", ratio, maxRatio)
	}
	if p := mannWhitney(xa, xb); !(p < alpha) {
		return V_INCONCLUSIVE, fmt.Sprintf("not significant p=%.6g vs alpha=%g (noise/ties, never PASS)", p, alpha)
	}
	return V_PASS, fmt.Sprintf("ratio=%.4f p=%.6g", ratio, mannWhitney(xa, xb))
}

type pairList []string

func (p *pairList) String() string { return fmt.Sprint(*p) }
func (p *pairList) Set(v string) error {
	if ab := strings.Split(v, ","); len(ab) == 2 && ab[0] != "" && ab[1] != "" {
		*p = append(*p, v)
		return nil
	}
	return fmt.Errorf("pair must be fileA,fileB")
}

type floatList []float64

func (f *floatList) String() string { return fmt.Sprint(*f) }
func (f *floatList) Set(v string) error {
	x, err := strconv.ParseFloat(v, 64)
	if err != nil || math.IsNaN(x) || math.IsInf(x, 0) {
		return fmt.Errorf("non-finite value %q", v)
	}
	*f = append(*f, x)
	return nil
}

type envInfo struct {
	self, goVer, goos, goarch string
	procs                     int
}

func runtimeEnv() (e envInfo, err error) {
	exe, err := os.Executable()
	if err == nil {
		var b []byte
		if b, err = os.ReadFile(exe); err == nil {
			s := sha256.Sum256(b)
			return envInfo{hex.EncodeToString(s[:]), runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0)}, nil
		}
	}
	return
}
func frozenDigest(m map[string]any) string {
	h := sha256.New()
	for _, k := range [5]string{"blobs", "commit", "corpus", "cpu", "power_plan"} {
		_, _ = fmt.Fprintf(h, "%s=%v\n", k, m[k])
	}
	return hex.EncodeToString(h.Sum(nil))
}
func checkManifest(raw []byte, e envInfo, frozen string) error {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return err
	}
	for _, kv := range [][2]string{{"commit", ""}, {"corpus", ""}, {"blobs", ""}, {"cpu", ""}, {"power_plan", ""}, {"evaluator_sha256", e.self}, {"go_version", e.goVer}, {"goos", e.goos}, {"goarch", e.goarch}, {"gomaxprocs", strconv.Itoa(e.procs)}} {
		s := m[kv[0]]
		if got := fmt.Sprint(s); s == nil || got == "" || got == "[]" || (kv[1] != "" && got != kv[1]) {
			return fmt.Errorf("manifest %s invalid or mismatch (blocked)", kv[0])
		}
	}
	if frozen == "" || frozenDigest(m) != frozen {
		return fmt.Errorf("manifest frozen provenance digest mismatch (blocked)")
	}
	return nil
}
func run(args []string) int {
	fs := flag.NewFlagSet("benchgate", flag.ContinueOnError)
	mp := fs.String("manifest", "", "bench-manifest.json path")
	fdg := fs.String("frozen-digest", "", "sha256 binding the frozen provenance fields (blobs,commit,corpus,cpu,power_plan)")
	var pairs pairList
	var ratios, alphas floatList
	fs.Var(&pairs, "pair", "fileA,fileB (repeatable, ordered)")
	fs.Var(&ratios, "max-ratio", "per-pair max median ratio")
	fs.Var(&alphas, "alpha", "per-pair significance level")
	ok := fs.Parse(args) == nil && *mp != "" && *fdg != "" && len(pairs) > 0 && len(ratios) == len(pairs) && len(alphas) == len(pairs)
	for i := range pairs {
		ok = ok && ratios[i] > 0 && ratios[i] <= 1 && alphas[i] > 0 && alphas[i] <= 1 // max-ratio bound (0,1]: >1 would authorize regression (gotcha #1736)
	}
	if !ok {
		fmt.Fprintln(os.Stderr, "usage: benchgate -manifest M -frozen-digest D -pair A,B -max-ratio R -alpha P (repeated; R finite in (0,1], P in (0,1], D=sha256 frozen provenance)")
		return 3
	}
	e, err := runtimeEnv()
	if err == nil {
		var raw []byte
		if raw, err = os.ReadFile(*mp); err == nil {
			err = checkManifest(raw, e, *fdg)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "blocked:", err)
		return 3
	}
	status := 0
	for i, pair := range pairs {
		ab := strings.Split(pair, ",")
		a, _ := parseBenchFile(ab[0])
		b, _ := parseBenchFile(ab[1])
		v, msg := evaluate(a, b, ratios[i], alphas[i])
		fmt.Printf("pair %d: %s — %s\n", i+1, [...]string{"PASS", "FAIL", "INCONCLUSIVE"}[v], msg)
		if v == V_FAIL || (v == V_INCONCLUSIVE && status == 0) {
			status = v
		}
	}
	return status
}
func main() { os.Exit(run(os.Args[1:])) }
