package store

import (
	"os"
	"sync"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func readinessBoundaryReuseFixture(t *testing.T, count int) (*Store, string, domain.VerifiedCharacterActivationPrefix, []domain.CharacterReadinessReviewAudit) {
	t.Helper()
	st, context, prefix, input := arbitrationV3Setup(t)
	var audits []domain.CharacterReadinessReviewAudit
	for index := 1; index <= count; index++ {
		view := arbitrationV3Prepare(t, st, prefix, input)
		scope, err := view.Sources(1)
		verifiedStoreMust(t, err)
		verifiedStoreMust(t, view.SaveArbitration(arbitrationV3Receipt(t, scope, nil, false, false)))
		cycle, err := view.FinalizeCycle(nil)
		verifiedStoreMust(t, err)
		_, err = st.AppendVerifiedCharacterActivationCycle(prefix.Session().Digest, cycle)
		verifiedStoreMust(t, err)
		pending, err := st.LoadVerifiedCharacterActivationPrefix(cycle.GenerationID, cycle.Chapter)
		verifiedStoreMust(t, err)
		prefix = *pending
		audit := arbitrationV3Audit(t, context, prefix, "continue")
		audits = append(audits, audit)
		if index < count {
			verifiedStoreMust(t, st.SaveVerifiedCharacterReadinessReviewAudit(audit))
			_, err = st.ApplyVerifiedCharacterChapterReadiness(prefix.Session().Digest, audit.Receipt)
			verifiedStoreMust(t, err)
			ready, err := st.LoadVerifiedCharacterActivationPrefix(cycle.GenerationID, cycle.Chapter)
			verifiedStoreMust(t, err)
			prefix = *ready
			input = arbitrationV3Next(t, prefix)
		}
	}
	root, err := characterActivationSessionDir(prefix.Session().GenerationID, prefix.Session().Chapter)
	verifiedStoreMust(t, err)
	return st, root, prefix, audits
}

func TestVerifiedReadinessBoundaryReusePreservesCurrentAndHistoricalAudits(t *testing.T) {
	st, root, prefix, audits := readinessBoundaryReuseFixture(t, 3)
	session := prefix.Session()
	current := audits[2]
	before, err := DirectoryContentRoot(st.Dir())
	verifiedStoreMust(t, err)
	missing, err := st.LoadCharacterReadinessReviewAudit(session.GenerationID, session.Chapter, 3)
	verifiedStoreMust(t, err)
	if missing != nil {
		t.Fatal("missing pending audit was fabricated")
	}
	after, err := DirectoryContentRoot(st.Dir())
	verifiedStoreMust(t, err)
	if before != after {
		t.Fatal("missing audit read changed the verified source tree")
	}
	verifiedStoreMust(t, st.SaveVerifiedCharacterReadinessReviewAudit(current))
	loaded, err := NewStore(st.Dir()).LoadCharacterReadinessReviewAudit(session.GenerationID, session.Chapter, 3)
	verifiedStoreMust(t, err)
	if loaded == nil || !jsonValuesEqual(*loaded, current) {
		t.Fatal("exact assessing-boundary audit changed")
	}
	// Compare against the pre-optimization full replay, not a re-signed value.
	oldBoundary, err := st.rebuildVerifiedCharacterActivationPrefix(root, session, 3, 2)
	verifiedStoreMust(t, err)
	wanted, err := st.loadVerifiedReadinessAudit(root, oldBoundary)
	verifiedStoreMust(t, err)
	if !jsonValuesEqual(loaded, wanted) {
		t.Fatal("reuse changed input/receipt/protocol bytes")
	}

	_, err = st.ApplyVerifiedCharacterChapterReadiness(session.Digest, current.Receipt)
	verifiedStoreMust(t, err)
	before, err = DirectoryContentRoot(st.Dir())
	verifiedStoreMust(t, err)
	for _, index := range []int{1, 3} {
		// Historical and already-applied retries must reconstruct the original
		// assessing boundary, not validate against the newer collecting cursor.
		verifiedStoreMust(t, st.SaveVerifiedCharacterReadinessReviewAudit(audits[index-1]))
		audit, err := NewStore(st.Dir()).LoadCharacterReadinessReviewAudit(session.GenerationID, session.Chapter, index)
		verifiedStoreMust(t, err)
		if audit == nil || !jsonValuesEqual(*audit, audits[index-1]) {
			t.Fatal("historical audit lost its original boundary")
		}
	}
	after, err = DirectoryContentRoot(st.Dir())
	verifiedStoreMust(t, err)
	if before != after {
		t.Fatal("idempotent historical retry changed stored bytes")
	}
	if _, err := st.LoadCharacterReadinessReviewAudit(session.GenerationID, session.Chapter, 4); err == nil {
		t.Fatal("future boundary acquired source authority")
	}
}

func TestVerifiedReadinessBoundaryReuseStillAuthenticatesSourcesAndAudit(t *testing.T) {
	for _, corrupt := range []string{"missing_baseline", "changed_cycle", "changed_audit"} {
		t.Run(corrupt, func(t *testing.T) {
			st, root, prefix, audits := readinessBoundaryReuseFixture(t, 2)
			audit := audits[1]
			if corrupt == "changed_audit" {
				verifiedStoreMust(t, st.SaveVerifiedCharacterReadinessReviewAudit(audit))
				verifiedStoreMust(t, os.WriteFile(st.CharacterAgents.io.path(characterReadinessAuditPath(root, 2)), []byte(`{}`), 0600))
			} else if corrupt == "missing_baseline" {
				verifiedStoreMust(t, os.Remove(st.CharacterAgents.io.path(activationBaselinePath(root))))
			} else {
				verifiedStoreMust(t, os.WriteFile(st.CharacterAgents.io.path(activationCyclePath(root, 1)), []byte(`{}`), 0600))
			}
			before, err := DirectoryContentRoot(st.Dir())
			verifiedStoreMust(t, err)
			session := prefix.Session()
			if _, err := st.LoadCharacterReadinessReviewAudit(session.GenerationID, session.Chapter, 2); err == nil {
				t.Fatal("current boundary skipped source/audit verification")
			}
			if err := st.SaveVerifiedCharacterReadinessReviewAudit(audit); err == nil {
				t.Fatal("current boundary overwrote corrupted evidence")
			}
			after, err := DirectoryContentRoot(st.Dir())
			verifiedStoreMust(t, err)
			if before != after {
				t.Fatal("rejected read/write repaired or rewrote evidence")
			}
		})
	}
}

func TestVerifiedReadinessBoundaryReuseConcurrentRetries(t *testing.T) {
	st, _, prefix, audits := readinessBoundaryReuseFixture(t, 2)
	audit, session := audits[1], prefix.Session()
	verifiedStoreMust(t, st.SaveVerifiedCharacterReadinessReviewAudit(audit))
	before, err := DirectoryContentRoot(st.Dir())
	verifiedStoreMust(t, err)
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < cap(errs); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			local := NewStore(st.Dir())
			if err := local.SaveVerifiedCharacterReadinessReviewAudit(audit); err != nil {
				errs <- err
				return
			}
			loaded, err := local.LoadCharacterReadinessReviewAudit(session.GenerationID, session.Chapter, 2)
			if err == nil && (loaded == nil || !jsonValuesEqual(*loaded, audit)) {
				t.Error("concurrent retry changed audit")
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		verifiedStoreMust(t, err)
	}
	after, err := DirectoryContentRoot(st.Dir())
	verifiedStoreMust(t, err)
	if before != after {
		t.Fatal("concurrent retries changed immutable evidence")
	}
}

// Run this focused test with -v for a deterministic-fixture before/after
// comparison. No timing threshold is asserted; both paths validate the same
// frozen proof and audit. Fixture construction is outside the measured loop.
func TestVerifiedReadinessBoundaryReuseWorkComparison(t *testing.T) {
	if os.Getenv("NOVEL_STUDIO_READINESS_BENCHMARK") != "1" {
		t.Skip("set NOVEL_STUDIO_READINESS_BENCHMARK=1 to run the isolated before/after microbenchmark")
	}
	st, root, prefix, audits := readinessBoundaryReuseFixture(t, 3)
	verifiedStoreMust(t, st.SaveVerifiedCharacterReadinessReviewAudit(audits[2]))
	session := prefix.Session()
	for _, optimized := range []bool{false, true} {
		result := testing.Benchmark(func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				var audit *domain.CharacterReadinessReviewAudit
				var err error
				if optimized {
					audit, err = st.loadVerifiedReadinessAuditAt(root, session.GenerationID, session.Chapter, 3)
				} else {
					var current *domain.VerifiedCharacterActivationPrefix
					current, err = st.loadVerifiedCharacterActivationPrefix(root, session.GenerationID, session.Chapter)
					if err == nil {
						var boundary domain.VerifiedCharacterActivationPrefix
						boundary, err = st.rebuildVerifiedCharacterActivationPrefix(root, current.Session(), 3, 2)
						if err == nil {
							audit, err = st.loadVerifiedReadinessAudit(root, boundary)
						}
					}
				}
				if err != nil || audit == nil || !jsonValuesEqual(*audit, audits[2]) {
					b.Fatalf("benchmark lost proof equivalence: %v", err)
				}
			}
		})
		t.Logf("same_boundary_reuse=%v %s %s", optimized, result.String(), result.MemString())
	}
}
