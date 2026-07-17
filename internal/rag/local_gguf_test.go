package rag

import (
	"testing"
)

func TestLocalGGUFServerArgsBoundBatchMemory(t *testing.T) {
	tests := []struct {
		name      string
		ctxSize   int
		wantCtx   string
		wantBatch string
	}{
		{name: "default context", ctxSize: 0, wantCtx: "8192", wantBatch: "2048"},
		{name: "small context", ctxSize: 1024, wantCtx: "1024", wantBatch: "1024"},
		{name: "batch ceiling", ctxSize: 2048, wantCtx: "2048", wantBatch: "2048"},
		{name: "large context", ctxSize: 16384, wantCtx: "16384", wantBatch: "2048"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := localGGUFServerArgs(LocalGGUFConfig{
				GGUFPath: "/tmp/qwen.gguf",
				Port:     18434,
				CtxSize:  tt.ctxSize,
			})
			for _, pair := range [][]string{
				{"-c", tt.wantCtx},
				{"-b", tt.wantBatch},
				{"-ub", tt.wantBatch},
				{"--parallel", "1"},
			} {
				if !containsAdjacentArgs(args, pair[0], pair[1]) {
					t.Fatalf("missing bounded server args %v in %v", pair, args)
				}
			}
			if tt.wantCtx != tt.wantBatch && containsAdjacentArgs(args, "-ub", tt.wantCtx) {
				t.Fatalf("unified batch must not scale to the full context: %v", args)
			}
		})
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
