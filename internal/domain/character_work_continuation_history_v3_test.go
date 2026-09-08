package domain_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// Apply a producer policy before any proposal is authored, never to saved
// grants. All observations and the input root are rebound together.
func historyV3Input(t *testing.T, input domain.CharacterActivationInputSet, full bool) domain.CharacterActivationInputSet {
	t.Helper()
	input = continuationCopy(input)
	if full {
		input.Stimulus.Sources = append(input.Stimulus.Sources, domain.CharacterWorkContinuationHistoryPolicyV1)
	}
	var err error
	input.Stimulus, err = domain.FinalizeWorldStimulusPacket(input.Stimulus)
	continuationMust(t, err)
	for i := range input.Observations {
		o := &input.Observations[i]
		if full {
			o.Sources = append(o.Sources, domain.CharacterWorkContinuationHistoryPolicyV1)
		}
		o.StimulusDigest = input.Stimulus.Digest
		*o, err = domain.FinalizeCharacterObservationPacket(*o)
		continuationMust(t, err)
		for j := range input.Activation.Entries {
			a := &input.Activation.Entries[j]
			if a.AgentID == o.AgentID && a.State == domain.CharacterAgentActive {
				a.ObservationDigest = o.Digest
			}
		}
	}
	input.Activation, err = domain.FinalizeCharacterAgentActivation(input.Activation)
	continuationMust(t, err)
	input, err = domain.FinalizeCharacterActivationInputSet(input)
	continuationMust(t, err)
	return input
}

func historyV3Draft(t *testing.T, prefix domain.VerifiedCharacterActivationPrefix, input domain.CharacterActivationInputSet) domain.CharacterActivationCycle {
	t.Helper()
	var admissions []domain.CharacterWorkContinuationReceiptV1
	var fresh []domain.CharacterDecisionProposal
	index := len(prefix.Session().CycleDigests) + 1
	for _, o := range input.Observations {
		eligible, err := domain.EvaluateCharacterWorkContinuationForRoundV1(prefix, input, o.AgentID)
		continuationMust(t, err)
		if eligible.Eligible {
			admissions = append(admissions, *eligible.Receipt)
			continue
		}
		p := v3Fresh(t, o)
		if o.Character != "丙" {
			p.SelfTasks[0].ProgressTarget = continuationNumber(20)
			if index == 1 {
				p.SelfTasks[0].TaskID = "setup"
				p.SelfTasks[0].ProgressTarget = continuationNumber(1)
				p.WorkContinuations = nil
			}
			p, err = domain.FinalizeCharacterDecisionProposal(p, o)
			continuationMust(t, err)
		}
		fresh = append(fresh, p)
	}
	scope, err := domain.ResolveCharacterArbitrationRoundV1(prefix, input, admissions, fresh)
	continuationMust(t, err)
	r := roundSourceReceipt(t, scope, 1)
	if index == 1 {
		for i := range r.Resolutions {
			r.Resolutions[i].CompletionState = "completed"
			r.Resolutions[i].SelfExecutions[0].Status = "completed"
		}
	}
	round, err := domain.FinalizeCharacterArbitrationRoundV1(scope, r)
	continuationMust(t, err)
	session := prefix.Session()
	previous := ""
	if index > 1 {
		previous = session.CycleDigests[index-2]
	}
	e := domain.CharacterAgentEvidenceBundle{Version: domain.CharacterActivationRoundEvidenceV3Version, GenerationID: session.GenerationID, Chapter: 1, Registry: input.Registry, Stimulus: input.Stimulus, Activation: input.Activation, Observations: scope.Observations(), Proposals: scope.SubmittedProposals(), Arbitrations: []domain.WorldArbitrationReceipt{round.Receipt()}, ProtocolDigest: "sha256:" + strings.Repeat("e", 64)}
	for _, o := range scope.Observations() {
		e.MemoryRoots = append(e.MemoryRoots, o.MemoryRoot)
	}
	e.Usage = append(e.Usage, domain.CharacterAgentUsage{GenerationID: session.GenerationID, Chapter: 1, Cycle: index, Round: 1, AgentID: "world_arbiter", Role: "world_arbiter", Input: 1, Output: 1, Attempts: 1, Status: "success", CostSource: "unknown"})
	for _, p := range fresh {
		e.Usage = append(e.Usage, domain.CharacterAgentUsage{GenerationID: session.GenerationID, Chapter: 1, Cycle: index, Round: 1, AgentID: p.AgentID, Role: "character", Input: 1, Output: 1, Attempts: 1, Status: "success", CostSource: "unknown"})
	}
	return domain.CharacterActivationCycle{Version: domain.CharacterActivationCycleV3Version, GenerationID: session.GenerationID, Chapter: 1, Index: index, PreviousDigest: previous, ChapterContextDigest: session.ChapterContextDigest, InputSetDigest: input.Digest, WorkContinuations: admissions, Evidence: e}
}

func TestContinuationHistoryV3DisplayEvictionAndLegacyRestore(t *testing.T) {
	for _, full := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "full-owner"}[full], func(t *testing.T) {
			context, _, input := v3Fixture(t)
			initial, err := domain.NewCharacterActivationSession(input.Stimulus.GenerationID, 1, context.Digest, *input.Stimulus.PhysicalState, 0, 16)
			continuationMust(t, err)
			prefix, err := domain.NewVerifiedCharacterActivationPrefix(initial)
			continuationMust(t, err)
			input = historyV3Input(t, v3RebindInput(t, input, initial, true), full)
			var cycles []domain.CharacterActivationCycle
			var inputs []domain.CharacterActivationInputSet
			var reviews []domain.CharacterReadinessReviewAudit
			owner := input.Observations[0].AgentID
			for index := 1; index <= 10; index++ {
				if index >= 3 {
					eligible, err := domain.EvaluateCharacterWorkContinuationForRoundV1(prefix, input, owner)
					continuationMust(t, err)
					if !eligible.Eligible {
						t.Fatalf("premature wake at cycle %d: %v", index, eligible.WakeReasons)
					}
				}
				draft := historyV3Draft(t, prefix, input)
				cycle, err := domain.FinalizeCharacterActivationCycleV3(prefix, input, draft)
				continuationMust(t, err)
				_, pending, err := domain.VerifyCharacterActivationStepV3(prefix, input, cycle)
				continuationMust(t, err)
				var audit domain.CharacterReadinessReviewAudit
				prefix, audit = v3Assess(t, context, pending, "continue")
				cycles, inputs, reviews = append(cycles, cycle), append(inputs, input), append(reviews, audit)
				input = historyV3Input(t, v3NextInput(t, prefix), full)
			}
			var observation domain.CharacterObservationPacket
			for _, o := range input.Observations {
				if o.AgentID == owner {
					observation = o
				}
			}
			for _, e := range observation.SelfExperiences {
				if e.TaskID == "setup" {
					t.Fatal("fixture did not evict setup from display")
				}
			}
			complete := false
			for _, a := range input.Stimulus.PhysicalState.Actors {
				if a.AgentID == owner {
					for _, e := range a.SelfExperiences {
						complete = complete || e.TaskID == "setup"
					}
				}
			}
			if !complete {
				t.Fatal("full predecessor state lost the real old experience")
			}
			eligible, err := domain.EvaluateCharacterWorkContinuationForRoundV1(prefix, input, owner)
			continuationMust(t, err)
			if eligible.Eligible != full || (!full && !containsV3(eligible.WakeReasons, "owner_information_changed")) {
				t.Fatalf("wrong eviction behavior full=%v: %+v", full, eligible)
			}
			// Restore source-bound grants and receipts from persisted flat evidence.
			raw, err := json.Marshal(struct {
				Cycles  []domain.CharacterActivationCycle
				Inputs  []domain.CharacterActivationInputSet
				Reviews []domain.CharacterReadinessReviewAudit
			}{cycles, inputs, reviews})
			continuationMust(t, err)
			var saved struct {
				Cycles  []domain.CharacterActivationCycle
				Inputs  []domain.CharacterActivationInputSet
				Reviews []domain.CharacterReadinessReviewAudit
			}
			continuationMust(t, json.Unmarshal(raw, &saved))
			restored, err := domain.NewVerifiedCharacterActivationPrefix(initial)
			continuationMust(t, err)
			for i, cycle := range saved.Cycles {
				_, restored, err = domain.VerifyCharacterActivationStepV3(restored, saved.Inputs[i], cycle)
				continuationMust(t, err)
				restored, err = domain.ApplyVerifiedCharacterActivationReadiness(restored, saved.Reviews[i].Receipt)
				continuationMust(t, err)
			}
			again, err := domain.EvaluateCharacterWorkContinuationForRoundV1(restored, input, owner)
			continuationMust(t, err)
			if !reflect.DeepEqual(again, eligible) {
				t.Fatal("restoring persisted grants changed roots or eligibility")
			}
			originalLedger, ok := prefix.ContinuationLedger(owner)
			if !ok {
				t.Fatal("missing authenticated work grant")
			}
			restoredLedger, ok := restored.ContinuationLedger(owner)
			if !ok || !reflect.DeepEqual(originalLedger, restoredLedger) {
				t.Fatal("old grant or receipt information roots changed on restore")
			}
			// Swapping the marker after genesis must fail even for an owner with no grant.
			var ungranted string
			for _, o := range input.Observations {
				if o.Character == "丙" {
					ungranted = o.AgentID
				}
			}
			if _, ok := prefix.ContinuationLedger(ungranted); ok || ungranted == "" {
				t.Fatal("fixture lacks an owner without a continuation grant")
			}
			swapped := historyV3Input(t, v3RebindInput(t, input, prefix.Session(), true), !full)
			if _, err := domain.EvaluateCharacterWorkContinuationForRoundV1(prefix, swapped, ungranted); err == nil || !strings.Contains(err.Error(), "history policy") {
				t.Fatalf("mid-session policy swap accepted: %v", err)
			}
			if full {
				changed := continuationCopy(input)
				for i := range changed.Observations {
					if changed.Observations[i].AgentID == owner {
						changed.Observations[i].KnownFacts[0].Text += "；本人新获知停机通知"
					}
				}
				changed = historyV3Input(t, changed, false)
				wake, err := domain.EvaluateCharacterWorkContinuationForRoundV1(prefix, changed, owner)
				continuationMust(t, err)
				if wake.Eligible || !containsV3(wake.WakeReasons, "owner_information_changed") {
					t.Fatalf("new owner information did not require a fresh decision: %+v", wake)
				}
			}
		})
	}
}
