package retriever

import (
	"math"
	"testing"
)

// ========== cosineSimilarity ==========

func TestCosineSimilarity_IdenticalVectors(t *testing.T) {
	a := []float32{1, 2, 3}
	got := cosineSimilarity(a, a)
	if math.Abs(got-1.0) > 1e-6 {
		t.Errorf("identical vectors: got %f, want 1.0", got)
	}
}

func TestCosineSimilarity_OrthogonalVectors(t *testing.T) {
	a := []float32{1, 0, 0}
	b := []float32{0, 1, 0}
	got := cosineSimilarity(a, b)
	if math.Abs(got) > 1e-6 {
		t.Errorf("orthogonal vectors: got %f, want 0.0", got)
	}
}

func TestCosineSimilarity_OppositeVectors(t *testing.T) {
	a := []float32{1, 2, 3}
	b := []float32{-1, -2, -3}
	got := cosineSimilarity(a, b)
	if math.Abs(got-(-1.0)) > 1e-6 {
		t.Errorf("opposite vectors: got %f, want -1.0", got)
	}
}

func TestCosineSimilarity_DifferentLengths(t *testing.T) {
	a := []float32{1, 2}
	b := []float32{1, 2, 3}
	got := cosineSimilarity(a, b)
	if got != 0 {
		t.Errorf("different lengths: got %f, want 0", got)
	}
}

func TestCosineSimilarity_ZeroVector(t *testing.T) {
	a := []float32{0, 0, 0}
	b := []float32{1, 2, 3}
	got := cosineSimilarity(a, b)
	if got != 0 {
		t.Errorf("zero vector: got %f, want 0", got)
	}
}

func TestCosineSimilarity_EmptyVectors(t *testing.T) {
	got := cosineSimilarity([]float32{}, []float32{})
	if got != 0 {
		t.Errorf("empty vectors: got %f, want 0", got)
	}
}

func TestCosineSimilarity_SingleElement(t *testing.T) {
	a := []float32{3}
	b := []float32{5}
	got := cosineSimilarity(a, b)
	if math.Abs(got-1.0) > 1e-6 {
		t.Errorf("single positive elements: got %f, want 1.0", got)
	}
}

func TestCosineSimilarity_KnownAngle(t *testing.T) {
	// Vectors at 45 degrees: cos(45°) ≈ 0.7071
	a := []float32{1, 0}
	b := []float32{1, 1}
	got := cosineSimilarity(a, b)
	expected := 1.0 / math.Sqrt(2)
	if math.Abs(got-expected) > 1e-4 {
		t.Errorf("45-degree angle: got %f, want %f", got, expected)
	}
}

// ========== NewRetriever ==========

func TestNewRetriever_NilIndex(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic with nil index, got none")
		}
	}()
	NewRetriever(nil)
}
