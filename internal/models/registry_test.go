package models

import "testing"

func TestGeneratedRegistryContainsGPT56Sol(t *testing.T) {
	entry, ok := NewModelRegistry().Resolve("gpt-5.6-sol")
	if !ok {
		t.Fatal("gpt-5.6-sol missing from generated model registry")
	}
	if entry.ContextWindow != 1050000 {
		t.Fatalf("gpt-5.6-sol API context window = %d, want 1050000", entry.ContextWindow)
	}
}
