package store

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/testutil"
)

func seedReadinessAudit(t *testing.T) (*Store, domain.CharacterActivationSession, domain.CharacterReadinessReviewInput, domain.CharacterReadinessReviewAudit) {
	t.Helper()
	context, session, cycle, input := testutil.CharacterReadiness(t, false)
	st := NewStore(t.TempDir())
	if err := st.SaveCharacterReadinessContext(context); err != nil {
		t.Fatal(err)
	}
	baseline, err := domain.NewCharacterActivationSession(cycle.GenerationID, 1, context.Digest, *cycle.Evidence.Stimulus.PhysicalState, 0, session.MaxCycles)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateCharacterActivationSession(baseline); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendCharacterActivationCycle(baseline.Digest, cycle); err != nil {
		t.Fatal(err)
	}
	r, err := domain.FinalizeCharacterReadinessReview(input, testutil.ReadyVerdict(input))
	if err != nil {
		t.Fatal(err)
	}
	return st, session, input, domain.CharacterReadinessReviewAudit{Input: input, Receipt: r}
}

func TestReadinessAuditMustExistBeforeAdvancingAndRemainsVerifiable(t *testing.T) {
	st, session, _, audit := seedReadinessAudit(t)
	if _, err := st.ApplyCharacterChapterReadiness(session.Digest, audit.Receipt); err == nil {
		t.Fatal("readiness advanced without its exact input audit")
	}
	if err := st.SaveCharacterReadinessReviewAudit(audit); err != nil {
		t.Fatal(err)
	}
	loaded, err := st.LoadCharacterActivationSession(session.GenerationID, 1)
	if err != nil || loaded.Phase != "assessing" {
		t.Fatal("saving paid audit prematurely advanced the session")
	}
	ready, err := st.ApplyCharacterChapterReadiness(session.Digest, audit.Receipt)
	if err != nil || ready.Phase != "ready" {
		t.Fatalf("audited verdict did not advance: %v", err)
	}
	if err := st.SaveCharacterReadinessReviewAudit(audit); err != nil {
		t.Fatalf("identical late retry changed audit: %v", err)
	}
	root, err := characterActivationSessionDir(session.GenerationID, 1)
	if err != nil {
		t.Fatal(err)
	}
	audit.Receipt.Reason = "篡改已付审查"
	raw, _ := json.Marshal(audit)
	if err := os.WriteFile(st.CharacterAgents.io.path(characterReadinessAuditPath(root, 1)), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := st.LoadCharacterActivationSession(session.GenerationID, 1); err == nil {
		t.Fatal("session trusted a ready cursor despite damaged review evidence")
	}
}

func TestReadinessAuditCannotInventAnEventProjection(t *testing.T) {
	st, _, input, _ := seedReadinessAudit(t)
	input.Trace.Cycles[0].Actions[0].ImmediateResult = "伪造全部义务已经完成"
	r, err := domain.FinalizeCharacterReadinessReview(input, testutil.ReadyVerdict(input))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveCharacterReadinessReviewAudit(domain.CharacterReadinessReviewAudit{Input: input, Receipt: r}); err == nil {
		t.Fatal("self-consistently re-signed fake event view was saved as real evidence")
	}
}
