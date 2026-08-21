package vectorhydration

import (
	"math"
	"math/rand"
	"sort"
)

// bcaLower computes the one-sided BCa lower confidence bound.  The single
// bootstrap distribution is intentionally reused for both bias correction and
// the final quantile.
func bcaLower(v []float64, seed uint64) float64 {
	if len(v) != PairedBlocksPerCell {
		return math.NaN()
	}
	obs := quantile(v, .5)
	dist := bootstrapMedians(v, seed)
	less := 0
	for _, median := range dist {
		if median < obs {
			less++
		}
	}
	z0 := normalInv((float64(less) + .5) / float64(BootstrapResamples+1))
	jack := make([]float64, len(v))
	for i := range v {
		q := append([]float64{}, v[:i]...)
		q = append(q, v[i+1:]...)
		jack[i] = quantile(q, .5)
	}
	// BCa acceleration is defined from the arithmetic mean of the jackknife
	// estimates, not their median.
	mean := 0.0
	for _, x := range jack {
		mean += x
	}
	mean /= float64(len(jack))
	var s2, s3 float64
	for _, x := range jack {
		d := mean - x
		s2 += d * d
		s3 += d * d * d
	}
	if s2 == 0 {
		return math.NaN()
	}
	a := s3 / (6 * math.Pow(s2, 1.5))
	if !finite(a) {
		return math.NaN()
	}
	za := normalInv(.05)
	adj := normalCDF(z0 + (z0+za)/(1-a*(z0+za)))
	if !finite(adj) || adj <= 0 || adj >= 1 {
		return math.NaN()
	}
	return quantile(dist, adj)
}

func bootstrapMedians(v []float64, seed uint64) []float64 {
	r := rand.New(rand.NewSource(int64(seed)))
	out := make([]float64, BootstrapResamples)
	sample := make([]float64, len(v))
	for i := range out {
		for j := range sample {
			sample[j] = v[r.Intn(len(v))]
		}
		out[i] = quantile(sample, .5)
	}
	sort.Float64s(out)
	return out
}
