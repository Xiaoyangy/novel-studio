package store

import (
	"os"
	"strconv"
	"testing"
)

// Opt-in, read-only verification of actual provider artifacts. This never
// initializes a Store, recovers a cursor, collects evidence, or calls a model.
// In-flight chapters are checked only through their committed cycle boundary.
func TestCharacterActivationLiveEvidenceReadOnly(t *testing.T) {
	output := os.Getenv("NOVEL_STUDIO_ACTIVATION_AUDIT_OUTPUT")
	if output == "" {
		t.Skip("set NOVEL_STUDIO_ACTIVATION_AUDIT_OUTPUT to audit real immutable cycles")
	}
	generation := os.Getenv("NOVEL_STUDIO_ACTIVATION_AUDIT_GENERATION")
	chapter := 1
	if raw := os.Getenv("NOVEL_STUDIO_ACTIVATION_AUDIT_CHAPTER"); raw != "" {
		var err error
		chapter, err = strconv.Atoi(raw)
		if err != nil || chapter < 1 {
			t.Fatal("invalid audit chapter")
		}
	}
	st := NewStore(output)
	session, err := st.LoadCharacterActivationSession(generation, chapter)
	if err != nil || session == nil {
		t.Fatalf("load exact session: %v", err)
	}
	if len(session.CycleDigests) == 0 {
		t.Fatal("no actual committed cycle is available to verify")
	}
	for i := range session.CycleDigests {
		cycle, err := st.LoadCharacterActivationCycle(generation, chapter, i+1)
		if err != nil || cycle == nil || cycle.Digest != session.CycleDigests[i] {
			t.Fatalf("cycle %d is not its exact committed proof: %v", i+1, err)
		}
		input, err := st.CharacterAgents.LoadActivationInputsForCycle(*cycle)
		if err != nil || input == nil || input.Digest != cycle.InputSetDigest {
			t.Fatalf("cycle %d input source validation: %v", i+1, err)
		}
		review := "pending"
		if i < len(session.ReadinessDigests) {
			audit, err := st.LoadCharacterReadinessReviewAudit(generation, chapter, i+1)
			if err != nil || audit == nil || audit.Receipt.Digest != session.ReadinessDigests[i] {
				t.Fatalf("cycle %d readiness source validation: %v", i+1, err)
			}
			review = audit.Receipt.Decision
		}
		// Only public identity/count/time and the structured readiness status,
		// never owner observations, model thinking, or world-side secrets.
		t.Logf("chapter=%d cycle=%d digest=%s observations=%d proposals=%d time_minutes=%.12g..%.12g readiness=%s",
			chapter, i+1, cycle.Digest, len(input.Observations), len(cycle.Evidence.Proposals), cycle.StartDay*1440, cycle.EndDay*1440, review)
	}
	if evidence, err := st.LoadCharacterActivationChapterEvidence(generation, chapter); err != nil {
		t.Fatal(err)
	} else if evidence != nil {
		t.Logf("complete_chapter_evidence=%s", evidence.Digest)
	}
}
