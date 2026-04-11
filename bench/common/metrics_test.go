package common

import (
	"math"
	"testing"
)

func TestF1Score(t *testing.T) {
	tests := []struct {
		pred, ref string
		want      float64
	}{
		{"the cat sat", "the cat sat", 1.0},
		{"the cat sat", "the dog sat", 0.667},
		{"", "", 1.0},
		{"hello", "", 0.0},
		{"", "hello", 0.0},
		{"a b c d", "a b e f", 0.5},
	}

	for _, tt := range tests {
		got := F1Score(tt.pred, tt.ref)
		if math.Abs(got-tt.want) > 0.01 {
			t.Errorf("F1(%q, %q) = %.3f, want %.3f", tt.pred, tt.ref, got, tt.want)
		}
	}
}

func TestRougeL(t *testing.T) {
	tests := []struct {
		pred, ref string
		wantMin   float64
	}{
		{"the cat sat on the mat", "the cat sat on the mat", 1.0},
		{"the cat sat", "the dog sat on mat", 0.3},
		{"", "", 1.0},
		{"hello", "", 0.0},
	}

	for _, tt := range tests {
		got := RougeL(tt.pred, tt.ref)
		if got < tt.wantMin-0.01 {
			t.Errorf("RougeL(%q, %q) = %.3f, want >= %.3f", tt.pred, tt.ref, got, tt.wantMin)
		}
	}
}

func TestAggregate(t *testing.T) {
	results := []QuestionResult{
		{ID: "1", Type: "single-hop", Score: 0.9, Correct: true},
		{ID: "2", Type: "single-hop", Score: 0.8, Correct: true},
		{ID: "3", Type: "multi-hop", Score: 0.3, Correct: false},
	}

	agg := Aggregate(results)
	if agg.Total != 3 {
		t.Fatalf("total = %d", agg.Total)
	}
	if agg.Correct != 2 {
		t.Fatalf("correct = %d", agg.Correct)
	}
	if math.Abs(agg.Overall-0.667) > 0.01 {
		t.Fatalf("overall = %.3f", agg.Overall)
	}
	if _, ok := agg.ByType["single-hop"]; !ok {
		t.Fatal("missing single-hop type")
	}
}
