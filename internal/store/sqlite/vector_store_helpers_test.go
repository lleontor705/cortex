//go:build cortex_vectors

package sqlite

import (
	"math"
	"testing"
)

// TestSerializeEmbedding tests the embedding serialization/deserialization.
func TestSerializeEmbedding(t *testing.T) {
	t.Run("serialize and deserialize preserves values", func(t *testing.T) {
		original := make([]float32, EmbeddingDimension)
		for i := range original {
			original[i] = float32(i) * 0.001
		}

		data, err := serializeEmbedding(original)
		if err != nil {
			t.Fatalf("serialize failed: %v", err)
		}

		if len(data) != EmbeddingDimension*4 {
			t.Errorf("expected %d bytes, got %d", EmbeddingDimension*4, len(data))
		}

		recovered, err := deserializeEmbedding(data)
		if err != nil {
			t.Fatalf("deserialize failed: %v", err)
		}

		if len(recovered) != len(original) {
			t.Fatalf("expected %d elements, got %d", len(original), len(recovered))
		}

		for i := range original {
			if math.Abs(float64(recovered[i]-original[i])) > 1e-6 {
				t.Errorf("element %d: expected %f, got %f", i, original[i], recovered[i])
			}
		}
	})

	t.Run("empty data returns error", func(t *testing.T) {
		_, err := deserializeEmbedding([]byte{})
		if err == nil {
			t.Error("expected error for empty data")
		}
	})

	t.Run("handles negative values", func(t *testing.T) {
		original := []float32{-1.5, -0.5, 0.0, 0.5, 1.5}
		data, err := serializeEmbedding(original)
		if err != nil {
			t.Fatalf("serialize failed: %v", err)
		}

		recovered, err := deserializeEmbedding(data)
		if err != nil {
			t.Fatalf("deserialize failed: %v", err)
		}

		for i := range original {
			if math.Abs(float64(recovered[i]-original[i])) > 1e-6 {
				t.Errorf("element %d: expected %f, got %f", i, original[i], recovered[i])
			}
		}
	})
}

// TestNormalizeVector tests the vector normalization.
func TestNormalizeVector(t *testing.T) {
	t.Run("normalizes to unit length", func(t *testing.T) {
		v := []float32{3.0, 4.0, 0.0}
		norm := normalizeVector(v)

		// Calculate L2 norm of result
		var sumSq float64
		for _, x := range norm {
			sumSq += x * x
		}
		length := math.Sqrt(sumSq)

		if math.Abs(length-1.0) > 1e-6 {
			t.Errorf("normalized vector should have unit length, got %f", length)
		}
	})

	t.Run("zero vector returns zero vector", func(t *testing.T) {
		v := []float32{0.0, 0.0, 0.0}
		norm := normalizeVector(v)

		for i, x := range norm {
			if x != 0.0 {
				t.Errorf("zero vector element %d should be 0, got %f", i, x)
			}
		}
	})

	t.Run("preserves direction", func(t *testing.T) {
		v := []float32{1.0, 1.0, 0.0}
		norm := normalizeVector(v)

		// Normalized should have same direction
		if norm[0] != norm[1] {
			t.Errorf("normalized should preserve direction: %f != %f", norm[0], norm[1])
		}
	})

	t.Run("handles single element", func(t *testing.T) {
		v := []float32{5.0}
		norm := normalizeVector(v)

		if math.Abs(norm[0]-1.0) > 1e-6 {
			t.Errorf("single element should normalize to 1, got %f", norm[0])
		}
	})
}

// TestComputeCosineSimilarity tests cosine similarity calculation.
func TestComputeCosineSimilarity(t *testing.T) {
	t.Run("identical vectors have similarity 1", func(t *testing.T) {
		v := []float32{1.0, 2.0, 3.0}
		norm := normalizeVector(v)
		sim := computeCosineSimilarity(norm, v)
		if math.Abs(sim-1.0) > 1e-6 {
			t.Errorf("identical vectors should have similarity 1, got %f", sim)
		}
	})

	t.Run("orthogonal vectors have similarity 0", func(t *testing.T) {
		v1 := []float32{1.0, 0.0, 0.0}
		v2 := []float32{0.0, 1.0, 0.0}
		norm := normalizeVector(v1)
		sim := computeCosineSimilarity(norm, v2)
		if math.Abs(sim) > 1e-6 {
			t.Errorf("orthogonal vectors should have similarity 0, got %f", sim)
		}
	})

	t.Run("opposite vectors have similarity -1", func(t *testing.T) {
		v1 := []float32{1.0, 2.0, 3.0}
		v2 := []float32{-1.0, -2.0, -3.0}
		norm := normalizeVector(v1)
		sim := computeCosineSimilarity(norm, v2)
		if math.Abs(sim-(-1.0)) > 1e-6 {
			t.Errorf("opposite vectors should have similarity -1, got %f", sim)
		}
	})

	t.Run("partial similarity", func(t *testing.T) {
		v1 := []float32{1.0, 0.0, 0.0}
		v2 := []float32{1.0, 1.0, 0.0}
		norm := normalizeVector(v1)
		sim := computeCosineSimilarity(norm, v2)
		// Expected: 1 / sqrt(2) - 0.707
		expected := 1.0 / math.Sqrt(2)
		if math.Abs(sim-expected) > 1e-6 {
			t.Errorf("expected similarity %f, got %f", expected, sim)
		}
	})

	t.Run("different length vectors return 0", func(t *testing.T) {
		v1 := []float32{1.0, 2.0}
		v2 := []float32{1.0, 2.0, 3.0}
		norm := normalizeVector(v1)
		sim := cosineSimilarity(norm, v2) // Direct call to cosineSimilarity which checks length
		if sim != 0 {
			t.Errorf("different length vectors should have similarity 0, got %f", sim)
		}
	})
}

// TestEmbeddingDimension tests that the constant is set correctly.
func TestEmbeddingDimension(t *testing.T) {
	if EmbeddingDimension != 384 {
		t.Errorf("EmbeddingDimension should be 384, got %d", EmbeddingDimension)
	}
}
