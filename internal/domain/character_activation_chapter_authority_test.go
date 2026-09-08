package domain_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/testutil"
)

func activationAuthorityJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	continuationMust(t, err)
	return raw
}

func activationAuthorityPlan(sim domain.ChapterWorldSimulation) domain.ChapterPlan {
	return domain.ChapterPlan{Chapter: sim.Chapter, Goal: "保留真实逐周期结果", CausalSimulation: domain.ChapterCausalSimulation{WorldSimulationID: sim.SimulationID, ProtagonistDecision: sim.ProtagonistProjection.ChosenDecision}}
}

func TestActivationChapterAuthorityEquivalentDetachedAndConcurrent(t *testing.T) {
	for _, mode := range []string{"legacy", "mixed_continuation"} {
		t.Run(mode, func(t *testing.T) {
			chapter := testutil.CharacterActivationChapter(t)
			if mode == "mixed_continuation" {
				chapter, _ = mixedReadinessChapterFixture(t)
			}
			before := activationAuthorityJSON(t, chapter)
			verified, err := domain.VerifyCharacterActivationChapter(chapter)
			continuationMust(t, err)
			sim, err := domain.BuildCharacterActivationSimulation(chapter, "tick_fixture", nil)
			continuationMust(t, err)
			plan := activationAuthorityPlan(sim)
			input, err := domain.NewActivationPlanGroundingInput(plan, sim, chapter, chapter.ProtocolDigest)
			continuationMust(t, err)
			simJSON, inputJSON := activationAuthorityJSON(t, sim), activationAuthorityJSON(t, input)
			// Captured through an isolated pre-authority algorithm overlay:
			// original validation/rebuild sequence and un-cloned projections.
			golden := map[string][2]string{
				"legacy":             {"d369a4f4e9c2b696fb515cc5de229cae961ca89a6dd3b57b92d652449341c8e1", "284c69fb19be57e004c81a3fe247b3ade457a27aede9a1a3fcb04cfe37f7aeac"},
				"mixed_continuation": {"b29b6c3ff9b28f86df19d84f9b7ea502fa1284f2371992c684e04869abcb9e97", "692f21edf0976d9161133aefcb6c9e5fafb2803cd73ac4838b32325fe4ef71d8"},
			}[mode]
			if fmt.Sprintf("%x", sha256.Sum256(simJSON)) != golden[0] || fmt.Sprintf("%x", sha256.Sum256(inputJSON)) != golden[1] {
				t.Fatal("simulation or grounding changed pre-authority JSON golden")
			}
			if !bytes.Equal(before, activationAuthorityJSON(t, chapter)) || !bytes.Equal(before, activationAuthorityJSON(t, verified.Evidence())) {
				t.Fatal("verification or public projection mutated original evidence")
			}
			// Mutation of caller input, an exported evidence copy, or a projection
			// must not become authority for a later consumer.
			chapter.Inputs[0].Observations[0].Location = "caller mutation"
			copy := verified.Evidence()
			copy.Context.HardContracts = []string{"copy mutation"}
			copy.Cycles[0].Evidence.Arbitrations[0].StoryTime.EndDay = 999
			projected, err := verified.BuildSimulation("tick_fixture", nil)
			continuationMust(t, err)
			projected.StoryTime.EndDay = 999
			projected.CharacterDecisionTrace[0].StoryTime.StartDay = 999
			view, err := verified.NewPlanGroundingInput(plan, sim, verified.Evidence().ProtocolDigest)
			continuationMust(t, err)
			view.POVObservation.Location = "view mutation"
			view.Activation.Cycles[0].StoryTime.EndDay = 999
			view.Simulation.ProtagonistProjection.ObservableEffects = []string{"view mutation"}
			protocol := verified.Evidence().ProtocolDigest
			var wg sync.WaitGroup
			errors := make(chan error, 4)
			for range 4 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					actual, err := verified.BuildSimulation("tick_fixture", nil)
					if err != nil {
						errors <- err
						return
					}
					actualInput, err := verified.NewPlanGroundingInput(plan, actual, protocol)
					if err != nil {
						errors <- err
						return
					}
					a, _ := json.Marshal(actual)
					b, _ := json.Marshal(actualInput)
					if !bytes.Equal(a, simJSON) || !bytes.Equal(b, inputJSON) {
						errors <- fmt.Errorf("authority consumer changed exact JSON/hash")
					}
					actual.StoryTime.EndDay = 123 // no shared projection pointers
					actualInput.POVObservation.Location = "own copy"
				}()
			}
			wg.Wait()
			close(errors)
			for err := range errors {
				t.Error(err)
			}
			if !bytes.Equal(before, activationAuthorityJSON(t, verified.Evidence())) {
				t.Fatal("consumer changed private verified source")
			}
		})
	}
}

func TestActivationChapterAuthorityRejectsBadSourcesAndSerializedBadges(t *testing.T) {
	chapter, _ := mixedReadinessChapterFixture(t)
	sim, err := domain.BuildCharacterActivationSimulation(chapter, "tick_fixture", nil)
	continuationMust(t, err)
	plan := activationAuthorityPlan(sim)
	for _, raw := range []string{`{}`, `{"verified":true,"evidence":{"digest":"claimed"},"steps":[{}]}`} {
		var fake domain.VerifiedCharacterActivationChapter
		continuationMust(t, json.Unmarshal([]byte(raw), &fake))
		if _, err := fake.BuildSimulation("", nil); err == nil {
			t.Fatal("JSON granted simulation authority")
		}
		if err := fake.ValidateSimulation(sim); err == nil {
			t.Fatal("JSON granted validation authority")
		}
		if _, err := fake.NewPlanGroundingInput(plan, sim, chapter.ProtocolDigest); err == nil {
			t.Fatal("JSON granted grounding authority")
		}
	}
	for _, resign := range []bool{false, true} {
		var bad domain.CharacterActivationChapterEvidence
		continuationMust(t, json.Unmarshal(activationAuthorityJSON(t, chapter), &bad))
		bad.Reviews[1].Input.Trace.Cycles[0].Actions[0].Decision = "invented paid result"
		if resign {
			bad.Digest = ""
			hash, err := domain.DeterministicPlanningHash(bad)
			continuationMust(t, err)
			bad.Digest = "sha256:" + hash
		}
		if _, err := domain.VerifyCharacterActivationChapter(bad); err == nil {
			t.Fatal("bad audit accepted through digest badge")
		}
		if _, err := domain.BuildCharacterActivationSimulation(bad, "", nil); err == nil {
			t.Fatal("public build skipped source verification")
		}
		if err := domain.ValidateCharacterActivationSimulation(sim, bad); err == nil {
			t.Fatal("public validation skipped source verification")
		}
		if _, err := domain.NewActivationPlanGroundingInput(plan, sim, bad, chapter.ProtocolDigest); err == nil {
			t.Fatal("public grounding skipped source verification")
		}
	}
	verified, err := domain.VerifyCharacterActivationChapter(chapter)
	continuationMust(t, err)
	// A forged trace can carry valid readiness/audit/view hashes of its own;
	// authority must still compare it to the complete replayed source state.
	bad := verified.Evidence()
	audit := &bad.Reviews[1]
	audit.Input.Trace.Actors[0].Location = "self-signed imaginary post-state"
	audit.Receipt, err = domain.FinalizeCharacterReadinessReview(audit.Input, domain.CharacterReadinessVerdict{Decision: audit.Receipt.Decision, Reason: audit.Receipt.Reason,
		EvidenceRefs: audit.Receipt.EvidenceRefs, ContractChecks: audit.Receipt.ContractChecks})
	continuationMust(t, err)
	codec, err := domain.NewCharacterReadinessModelCodecV1(audit.Input)
	continuationMust(t, err)
	binding := codec.Binding()
	audit.ModelView = &binding
	continuationMust(t, domain.ValidateCharacterReadinessReviewAudit(*audit))
	bad.Digest = ""
	hash, err := domain.DeterministicPlanningHash(bad)
	continuationMust(t, err)
	bad.Digest = "sha256:" + hash
	if _, err := domain.VerifyCharacterActivationChapter(bad); err == nil {
		t.Fatal("self-consistent audit hashes replaced original post-state authority")
	}
	sim.CharacterDecisionTrace[0].Decision.Decision = "changed simulation intent"
	if err := verified.ValidateSimulation(sim); err == nil {
		t.Fatal("verified evidence excused altered simulation")
	}
	if _, err := verified.NewPlanGroundingInput(plan, sim, chapter.ProtocolDigest); err == nil {
		t.Fatal("grounding accepted altered simulation")
	}
}
