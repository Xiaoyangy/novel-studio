package rag

import (
	"context"
	"math"
	"reflect"
	"testing"
)

func TestLocalHashEmbedderDeterministic(t *testing.T) {
	embedder := NewLocalHashEmbedder("local-hash-128")
	a, err := embedder.Embed(context.Background(), "M17 生活服务协同 医疗陪同")
	if err != nil {
		t.Fatal(err)
	}
	b, err := embedder.Embed(context.Background(), "M17 生活服务协同 医疗陪同")
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 128 {
		t.Fatalf("dimension = %d, want 128", len(a))
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatal("local embedding should be deterministic")
	}
	var norm float64
	for _, v := range a {
		norm += float64(v * v)
	}
	if math.Abs(norm-1) > 0.001 {
		t.Fatalf("norm = %.4f, want 1", norm)
	}
}
