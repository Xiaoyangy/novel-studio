package store

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// Compare only the indexed-copy step after one full source verification, not
// complete resume or model latency. All input is an explicitly selected book;
// the before/after content root must remain unchanged.
func BenchmarkCharacterActivationIndexedStepCopy(b *testing.B) {
	output := os.Getenv("NOVEL_STUDIO_ACTIVATION_AUDIT_OUTPUT")
	if output == "" {
		b.Skip("requires an explicit read-only audit book")
	}
	generation := os.Getenv("NOVEL_STUDIO_ACTIVATION_AUDIT_GENERATION")
	chapter, err := strconv.Atoi(os.Getenv("NOVEL_STUDIO_ACTIVATION_AUDIT_CHAPTER"))
	if err != nil || chapter < 1 {
		b.Fatal("requires an explicit chapter")
	}
	lock, err := os.Lstat(filepath.Join(output, projectedWriteLockFile))
	if err != nil || !lock.Mode().IsRegular() {
		b.Fatal("requires an existing regular metadata lock")
	}
	before, err := DirectoryContentRoot(output)
	if err != nil {
		b.Fatal(err)
	}
	prefix, err := NewStore(output).LoadVerifiedCharacterActivationPrefix(generation, chapter)
	if err != nil || prefix == nil {
		b.Fatalf("source verification: %v", err)
	}
	session := prefix.Session()
	index := len(session.CycleDigests) - 1
	if index < 0 {
		b.Fatal("no verified steps")
	}
	for _, method := range []string{"all_history_twice", "one_detached_step"} {
		b.Run(method, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if method == "all_history_twice" {
					if len(prefix.Steps()) <= index {
						b.Fatal("missing step")
					}
					if prefix.Steps()[index].GlobalRoot() != session.CycleDigests[index] {
						b.Fatal("wrong source")
					}
				} else {
					step, ok := prefix.Step(index)
					if !ok || step.GlobalRoot() != session.CycleDigests[index] {
						b.Fatal("wrong source")
					}
				}
			}
		})
	}
	after, err := DirectoryContentRoot(output)
	if err != nil || before != after {
		b.Fatal("benchmark changed source book")
	}
	b.Logf("verified_steps=%d unchanged_content_root=%s", len(session.CycleDigests), before)
}
