package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func readinessTransactionFixture(t *testing.T, version string) (*Store, string, domain.VerifiedCharacterActivationPrefix, domain.CharacterReadinessReviewAudit) {
	t.Helper()
	if version == "v3" {
		st, root, prefix, audits := readinessBoundaryReuseFixture(t, 2)
		return st, root, prefix, audits[1]
	}
	st, context, _, origin, initial := verifiedStoreSetup(t)
	prefix := verifiedStoreContinue(t, st, context, origin, initial)
	input, cycle := verifiedStoreNext(t, prefix)
	view, err := st.CharacterAgents.ForActivationCycle(prefix.Session())
	verifiedStoreMust(t, err)
	verifiedStoreMust(t, view.PublishActivationInputs(input))
	verifiedStoreProofs(t, view, cycle)
	_, err = st.AppendVerifiedCharacterActivationCycle(prefix.Session().Digest, cycle)
	verifiedStoreMust(t, err)
	pending, err := st.LoadVerifiedCharacterActivationPrefix(initial.GenerationID, initial.Chapter)
	verifiedStoreMust(t, err)
	root, err := characterActivationSessionDir(initial.GenerationID, initial.Chapter)
	verifiedStoreMust(t, err)
	return st, root, *pending, verifiedStoreAudit(t, context, *pending, "continue")
}

func TestVerifiedReadinessTransactionPreservesLegacyBytesAndCachedPreparation(t *testing.T) {
	for _, version := range []string{"v2", "v3"} {
		t.Run(version, func(t *testing.T) {
			st, root, prefix, audit := readinessTransactionFixture(t, version)
			session := prefix.Session()
			before, err := DirectoryContentRoot(st.Dir())
			verifiedStoreMust(t, err)
			input, cached, err := st.PrepareVerifiedCharacterReadinessReview(session, audit.Input.ReviewProtocol)
			verifiedStoreMust(t, err)
			if cached != nil || !jsonValuesEqual(input, audit.Input) {
				t.Fatal("prepared input differs from original source-authenticated domain input")
			}
			after, err := DirectoryContentRoot(st.Dir())
			verifiedStoreMust(t, err)
			if before != after {
				t.Fatal("read-only preparation wrote files")
			}
			oldDir := t.TempDir()
			verifiedStoreMust(t, os.CopyFS(oldDir, os.DirFS(st.Dir())))
			old := NewStore(oldDir)
			verifiedStoreMust(t, old.SaveVerifiedCharacterReadinessReviewAudit(audit))
			oldSession, err := old.ApplyVerifiedCharacterChapterReadiness(session.Digest, audit.Receipt)
			verifiedStoreMust(t, err)
			applied, err := st.CommitVerifiedCharacterReadinessReview(session.Digest, audit)
			verifiedStoreMust(t, err)
			if !jsonValuesEqual(oldSession, applied) {
				t.Fatal("transaction changed the original domain session transition")
			}
			for _, rel := range []string{characterReadinessAuditPath(root, 2), activationReadinessPath(root, 2), filepath.Join(root, "session.json")} {
				want, err := os.ReadFile(filepath.Join(oldDir, rel))
				verifiedStoreMust(t, err)
				got, err := os.ReadFile(filepath.Join(st.Dir(), rel))
				verifiedStoreMust(t, err)
				if !bytes.Equal(want, got) {
					t.Fatalf("transaction changed legacy artifact bytes at %s", rel)
				}
			}
			if _, _, err := st.PrepareVerifiedCharacterReadinessReview(session, audit.Input.ReviewProtocol); err == nil {
				t.Fatal("preparation accepted a stale already-applied caller session")
			}
		})
	}
}

func TestVerifiedReadinessTransactionRecoversEveryPartialWrite(t *testing.T) {
	for _, version := range []string{"v2", "v3"} {
		for _, failure := range []string{"audit_only", "before_cursor", "after_cursor"} {
			t.Run(version+"/"+failure, func(t *testing.T) {
				st, root, prefix, audit := readinessTransactionFixture(t, version)
				session := prefix.Session()
				cursorPath := filepath.Join(root, "session.json")
				oldCursor, err := os.ReadFile(filepath.Join(st.Dir(), cursorPath))
				verifiedStoreMust(t, err)
				injected := errors.New("injected readiness publication failure")
				_, err = st.commitVerifiedCharacterReadinessReview(session.Digest, audit, func(path string, value any, immutable bool) error {
					if (failure == "audit_only" && path == activationReadinessPath(root, 2)) || (failure == "before_cursor" && path == cursorPath) {
						return injected
					}
					if err := st.writeCharacterActivationJSON(path, value, immutable); err != nil {
						return err
					}
					if failure == "after_cursor" && path == cursorPath {
						return injected
					}
					return nil
				})
				if !errors.Is(err, injected) {
					t.Fatalf("test did not cross its real immutable write boundary: %v", err)
				}
				savedAudit, err := os.ReadFile(filepath.Join(st.Dir(), characterReadinessAuditPath(root, 2)))
				verifiedStoreMust(t, err)
				if failure != "after_cursor" {
					currentCursor, err := os.ReadFile(filepath.Join(st.Dir(), cursorPath))
					verifiedStoreMust(t, err)
					if !bytes.Equal(oldCursor, currentCursor) {
						t.Fatal("failed cursor write changed the applied session")
					}
					input, cached, err := NewStore(st.Dir()).PrepareVerifiedCharacterReadinessReview(session, audit.Input.ReviewProtocol)
					verifiedStoreMust(t, err)
					if cached == nil || !jsonValuesEqual(*cached, audit) || !jsonValuesEqual(input, audit.Input) {
						t.Fatal("partial transaction could not reuse the exact paid immutable audit")
					}
					input.Trace.Cycles[0].Actions[0].ImmediateResult = "detached caller mutation"
					if !jsonValuesEqual(*cached, audit) {
						t.Fatal("returned prepared input aliased cached audit memory")
					}
				}
				applied, err := NewStore(st.Dir()).CommitVerifiedCharacterReadinessReview(session.Digest, audit)
				verifiedStoreMust(t, err)
				want, err := domain.ApplyVerifiedCharacterActivationReadiness(prefix, audit.Receipt)
				verifiedStoreMust(t, err)
				if !jsonValuesEqual(*applied, want.Session()) {
					t.Fatal("crash recovery changed the original applied transition")
				}
				afterAudit, err := os.ReadFile(filepath.Join(st.Dir(), characterReadinessAuditPath(root, 2)))
				verifiedStoreMust(t, err)
				if !bytes.Equal(savedAudit, afterAudit) {
					t.Fatal("recovery rewrote paid audit bytes")
				}
				before, err := DirectoryContentRoot(st.Dir())
				verifiedStoreMust(t, err)
				again, err := st.CommitVerifiedCharacterReadinessReview(session.Digest, audit)
				verifiedStoreMust(t, err)
				after, err := DirectoryContentRoot(st.Dir())
				verifiedStoreMust(t, err)
				if before != after || !jsonValuesEqual(applied, again) {
					t.Fatal("applied retry rewrote evidence or advanced twice")
				}
			})
		}
	}
}

func TestVerifiedReadinessTransactionRejectsChangedSourcesWithZeroWrites(t *testing.T) {
	for _, corrupt := range []string{"baseline", "input", "cycle", "context", "audit", "model_view"} {
		t.Run(corrupt, func(t *testing.T) {
			st, root, prefix, audit := readinessTransactionFixture(t, "v3")
			session := prefix.Session()
			path := activationBaselinePath(root)
			switch corrupt {
			case "input":
				path = activationFrozenInputPath(root, 1)
			case "cycle":
				path = activationCyclePath(root, 1)
			case "context":
				path = filepath.Join(root, "chapter_context.json")
			case "audit":
				verifiedStoreMust(t, st.SaveVerifiedCharacterReadinessReviewAudit(audit))
				path = characterReadinessAuditPath(root, 2)
			case "model_view":
				if audit.ModelView == nil {
					t.Fatal("v3 fixture is missing its real model-view binding")
				}
				audit.ModelView.ViewPolicy = "forged model view"
			}
			if corrupt != "model_view" {
				verifiedStoreMust(t, os.WriteFile(filepath.Join(st.Dir(), path), []byte(`{}`), 0o600))
			}
			before, err := DirectoryContentRoot(st.Dir())
			verifiedStoreMust(t, err)
			if corrupt != "model_view" {
				if _, _, err := st.PrepareVerifiedCharacterReadinessReview(session, audit.Input.ReviewProtocol); err == nil {
					t.Fatal("preparation skipped actual source/audit verification")
				}
			}
			if _, err := st.CommitVerifiedCharacterReadinessReview(session.Digest, audit); err == nil {
				t.Fatal("commit accepted changed source or model view")
			}
			after, err := DirectoryContentRoot(st.Dir())
			verifiedStoreMust(t, err)
			if before != after {
				t.Fatal("rejected source boundary modified durable artifacts")
			}
		})
	}
}

func TestVerifiedReadinessTransactionCASAndReadyVerdictAreImmutable(t *testing.T) {
	st, _, prefix, audit := readinessTransactionFixture(t, "v3")
	session := prefix.Session()
	before, err := DirectoryContentRoot(st.Dir())
	verifiedStoreMust(t, err)
	if _, err := st.CommitVerifiedCharacterReadinessReview("sha256:"+strings.Repeat("b", 64), audit); err == nil {
		t.Fatal("incorrect CAS was accepted")
	}
	after, err := DirectoryContentRoot(st.Dir())
	verifiedStoreMust(t, err)
	if before != after {
		t.Fatal("CAS rejection wrote an audit")
	}
	// Retain the model binding while deriving another valid verdict for the
	// same actual input; only the first immutable decision may be applied.
	audit.Receipt, err = domain.FinalizeCharacterReadinessReview(audit.Input, domain.CharacterReadinessVerdict{
		Decision: "ready_for_plan", Reason: audit.Receipt.Reason, EvidenceRefs: audit.Receipt.EvidenceRefs, ContractChecks: audit.Receipt.ContractChecks,
	})
	verifiedStoreMust(t, err)
	applied, err := st.CommitVerifiedCharacterReadinessReview(session.Digest, audit)
	verifiedStoreMust(t, err)
	if applied.Phase != "ready" {
		t.Fatal("valid ready verdict was not applied")
	}
	conflict := verifiedStoreCopy(audit)
	conflict.Receipt, err = domain.FinalizeCharacterReadinessReview(conflict.Input, domain.CharacterReadinessVerdict{
		Decision: "continue", Reason: conflict.Receipt.Reason, EvidenceRefs: conflict.Receipt.EvidenceRefs, ContractChecks: conflict.Receipt.ContractChecks,
	})
	verifiedStoreMust(t, err)
	before, err = DirectoryContentRoot(st.Dir())
	verifiedStoreMust(t, err)
	if _, err := st.CommitVerifiedCharacterReadinessReview(session.Digest, conflict); err == nil {
		t.Fatal("already-ready immutable verdict was replaced")
	}
	if _, err := st.CommitVerifiedCharacterReadinessReview(applied.Digest, audit); err == nil {
		t.Fatal("applied retry accepted a substituted original boundary")
	}
	after, err = DirectoryContentRoot(st.Dir())
	verifiedStoreMust(t, err)
	if before != after {
		t.Fatal("rejected verdict replacement changed durable state")
	}
}

func TestVerifiedReadinessTransactionHistoricalRetryAndConcurrentCommit(t *testing.T) {
	st, _, prefix, audits := readinessBoundaryReuseFixture(t, 3)
	session, audit := prefix.Session(), audits[2]
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < cap(errs); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := NewStore(st.Dir()).CommitVerifiedCharacterReadinessReview(session.Digest, audit)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		verifiedStoreMust(t, err)
	}
	before, err := DirectoryContentRoot(st.Dir())
	verifiedStoreMust(t, err)
	current, err := st.CommitVerifiedCharacterReadinessReview(audits[0].Input.SessionDigest, audits[0])
	verifiedStoreMust(t, err)
	if len(current.ReadinessDigests) != 3 || current.ReadinessDigests[2] != audit.Receipt.Digest {
		t.Fatal("historical exact retry reverted or duplicated later applied state")
	}
	after, err := DirectoryContentRoot(st.Dir())
	verifiedStoreMust(t, err)
	if before != after {
		t.Fatal("historical exact retry wrote files")
	}
}

func TestVerifiedReadinessTransactionCachedProtocolCannotBeReplaced(t *testing.T) {
	st, _, prefix, audit := readinessTransactionFixture(t, "v3")
	verifiedStoreMust(t, st.SaveVerifiedCharacterReadinessReviewAudit(audit))
	input, cached, err := st.PrepareVerifiedCharacterReadinessReview(prefix.Session(), audit.Input.ReviewProtocol)
	verifiedStoreMust(t, err)
	want, _ := json.Marshal(audit.Input)
	got, _ := json.Marshal(input)
	if cached == nil || !bytes.Equal(want, got) || !jsonValuesEqual(audit, *cached) {
		t.Fatal("cached preparation changed the original immutable review")
	}
	if _, _, err := st.PrepareVerifiedCharacterReadinessReview(prefix.Session(), "sha256:"+strings.Repeat("b", 64)); err == nil {
		t.Fatal("cached preparation silently rebound the review protocol")
	}
}
