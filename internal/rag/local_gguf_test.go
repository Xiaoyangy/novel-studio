package rag

import (
	"testing"
)

func TestLocalGGUFServerArgsBoundBatchMemory(t *testing.T) {
	args := localGGUFServerArgs(LocalGGUFConfig{
		GGUFPath: "/tmp/qwen.gguf",
		Port:     18434,
		CtxSize:  8192,
	})
	for _, pair := range [][]string{
		{"-c", "8192"},
		{"-b", "512"},
		{"-ub", "128"},
		{"--parallel", "1"},
	} {
		if !containsAdjacentArgs(args, pair[0], pair[1]) {
			t.Fatalf("missing bounded server args %v in %v", pair, args)
		}
	}
	if containsAdjacentArgs(args, "-ub", "8192") {
		t.Fatalf("unified batch must not scale to the full context: %v", args)
	}
}

func containsAdjacentArgs(args []string, key, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == key && args[i+1] == value {
			return true
		}
	}
	return false
}
