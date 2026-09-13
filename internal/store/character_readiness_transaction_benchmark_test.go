package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// Opt-in end-to-end host readiness measurement over a real completed prefix.
// Copies reconstruct the last pre-assessment boundary; no model is called and
// the original book is never written. The real last verdict is replayed as data,
// not presented as a newly produced or newly accepted chapter.
func BenchmarkCharacterReadinessTransactionReplay(b *testing.B) {
	source := os.Getenv("NOVEL_STUDIO_ACTIVATION_AUDIT_OUTPUT")
	if source == "" {
		b.Skip("set the explicit real-book audit source for a one-shot comparison")
	}
	generation := os.Getenv("NOVEL_STUDIO_ACTIVATION_AUDIT_GENERATION")
	chapter, err := strconv.Atoi(os.Getenv("NOVEL_STUDIO_ACTIVATION_AUDIT_CHAPTER"))
	if err != nil || chapter < 1 {
		b.Fatal("positive audit chapter required")
	}
	root, err := characterActivationSessionDir(generation, chapter)
	if err != nil {
		b.Fatal(err)
	}
	lock, err := os.Lstat(filepath.Join(source, projectedWriteLockFile))
	if err != nil || !lock.Mode().IsRegular() {
		b.Fatalf("source must already have its regular read lock: %v", err)
	}
	before, err := DirectoryContentRoot(source)
	if err != nil {
		b.Fatal(err)
	}
	original := NewStore(source)
	var pending, completed domain.CharacterActivationSession
	var audit domain.CharacterReadinessReviewAudit
	err = original.ProjectedV2().withProjectedReadLock(func() error {
		if err := original.readCharacterActivationJSON(filepath.Join(root, "session.json"), &completed); err != nil {
			return err
		}
		count := len(completed.CycleDigests)
		if count == 0 || count != len(completed.ReadinessDigests) {
			return fmt.Errorf("comparison requires the last cycle to have an actual review")
		}
		prefix, err := original.rebuildVerifiedCharacterActivationPrefix(root, completed, count, count-1)
		if err != nil {
			return err
		}
		pending = prefix.Session()
		stored, err := original.loadVerifiedReadinessAudit(root, prefix)
		if err != nil || stored == nil {
			return fmt.Errorf("last actual review missing: %v", err)
		}
		audit = *stored
		return nil
	})
	if err != nil {
		b.Fatal(err)
	}
	for _, transactional := range []bool{false, true} {
		name := map[bool]string{false: "legacy-six-replays", true: "transaction-two-replays"}[transactional]
		b.Run(name, func(b *testing.B) {
			if b.N != 1 {
				b.Fatal("real-book comparison requires -benchtime=1x")
			}
			b.StopTimer()
			copyRoot := filepath.Join(b.TempDir(), "book-copy")
			if err := os.CopyFS(copyRoot, os.DirFS(source)); err != nil {
				b.Fatal(err)
			}
			st := NewStore(copyRoot)
			index := len(pending.CycleDigests)
			for _, path := range []string{characterReadinessAuditPath(root, index), activationReadinessPath(root, index)} {
				if err := os.Remove(filepath.Join(copyRoot, path)); err != nil {
					b.Fatal(err)
				}
			}
			if err := st.writeCharacterActivationJSON(filepath.Join(root, "session.json"), pending, false); err != nil {
				b.Fatal(err)
			}
			var input domain.CharacterReadinessReviewInput
			var cached *domain.CharacterReadinessReviewAudit
			var applied *domain.CharacterActivationSession
			b.ReportAllocs()
			b.ResetTimer()
			b.StartTimer()
			if transactional {
				input, cached, err = st.PrepareVerifiedCharacterReadinessReview(pending, audit.Input.ReviewProtocol)
				if err == nil && cached == nil {
					applied, err = st.CommitVerifiedCharacterReadinessReview(pending.Digest, audit)
				}
			} else {
				// The previous production controller loaded the last cycle before
				// the runner independently loaded input and the cached verdict.
				_, err = st.LoadVerifiedCharacterActivationPrefix(generation, chapter)
				if err == nil {
					var prefix *domain.VerifiedCharacterActivationPrefix
					prefix, err = st.LoadVerifiedCharacterActivationPrefix(generation, chapter)
					if err == nil {
						input, err = domain.NewCharacterReadinessReviewInputFromSteps(audit.Input.Context, pending, prefix.Steps(), audit.Input.ReviewProtocol)
					}
				}
				if err == nil {
					cached, err = st.LoadCharacterReadinessReviewAudit(generation, chapter, index)
				}
				if err == nil && cached == nil {
					err = st.SaveCharacterReadinessReviewAudit(audit)
				}
				if err == nil {
					_, err = st.LoadCharacterReadinessReviewAudit(generation, chapter, index)
				}
				if err == nil {
					applied, err = st.ApplyVerifiedCharacterChapterReadiness(pending.Digest, audit.Receipt)
				}
			}
			b.StopTimer()
			if err != nil || cached != nil || applied == nil {
				b.Fatalf("host readiness replay failed: cached=%t applied=%t error=%v", cached != nil, applied != nil, err)
			}
			if !jsonValuesEqual(input, audit.Input) || !jsonValuesEqual(*applied, completed) {
				b.Fatal("optimization changed actual review input or resulting session")
			}
			if err := st.requireExactReadinessTransactionFiles(root, index, audit, true); err != nil {
				b.Fatal(err)
			}
			b.ReportMetric(float64(index), "cycles/op")
			b.Logf("input=%s receipt=%s session=%s actual_model_calls=0", audit.Receipt.InputDigest, audit.Receipt.Digest, applied.Digest)
		})
	}
	after, err := DirectoryContentRoot(source)
	if err != nil || after != before {
		b.Fatalf("read-only comparison changed source content: %v", err)
	}
	b.Logf("source_content_root_unchanged=%s", before)
}
