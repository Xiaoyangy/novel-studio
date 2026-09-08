package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strconv"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// Opt-in performance evidence, not a weaker validation path. Use -run '^$'
// -bench '^BenchmarkCharacterActivationVerifiedPrefixReadOnly$' -benchtime=1x.
// CPU/alloc profiles must be directed outside the book by the calling command.
func BenchmarkCharacterActivationVerifiedPrefixReadOnly(b *testing.B) {
	output := os.Getenv("NOVEL_STUDIO_ACTIVATION_AUDIT_OUTPUT")
	if output == "" {
		b.Skip("set NOVEL_STUDIO_ACTIVATION_AUDIT_OUTPUT for one real read-only prefix measurement")
	}
	if b.N != 1 {
		b.Fatal("real evidence benchmark requires -benchtime=1x; no repeated history scan")
	}
	generation := os.Getenv("NOVEL_STUDIO_ACTIVATION_AUDIT_GENERATION")
	chapter, err := strconv.Atoi(os.Getenv("NOVEL_STUDIO_ACTIVATION_AUDIT_CHAPTER"))
	if err != nil || chapter < 1 {
		b.Fatal("set a positive explicit audit chapter")
	}
	if _, err := characterActivationSessionDir(generation, chapter); err != nil {
		b.Fatal(err)
	}
	b.StopTimer()
	// The production read lock uses O_CREATE. Requiring an existing regular
	// lock here prevents this audit from creating even a lock in the live book.
	lock, err := os.Lstat(filepath.Join(output, projectedWriteLockFile))
	if err != nil || !lock.Mode().IsRegular() {
		b.Fatalf("read-only audit requires existing regular projected lock: %v", err)
	}
	before, err := DirectoryContentRoot(output)
	if err != nil {
		b.Fatal(err)
	}
	st := NewStore(output)
	var prefix *domain.VerifiedCharacterActivationPrefix
	b.ReportAllocs()
	b.ResetTimer()
	b.StartTimer()
	pprof.Do(context.Background(), pprof.Labels("operation", "verified-prefix-load"), func(context.Context) {
		prefix, err = st.LoadVerifiedCharacterActivationPrefix(generation, chapter)
	})
	b.StopTimer()
	if err != nil || prefix == nil {
		b.Fatalf("exact verified prefix read failed: %v", err)
	}
	session := prefix.Session()
	after, err := DirectoryContentRoot(output)
	if err != nil {
		b.Fatal(err)
	}
	if before != after {
		b.Fatal("read-only prefix benchmark changed the book content root")
	}
	b.ReportMetric(float64(len(session.CycleDigests)), "cycles/op")
	b.Logf("generation=%s chapter=%d cycles=%d reviews=%d session=%s unchanged_content_root=%s", generation, chapter, len(session.CycleDigests), len(session.ReadinessDigests), session.Digest, before)
	b.Log(fmt.Sprintf("verification=production-source-aware-prefix only_one_load=true phase=%s", session.Phase))
}
