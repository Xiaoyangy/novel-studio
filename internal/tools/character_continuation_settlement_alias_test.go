package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

const continuationSettlementFuelID = "res_0000000000000001"

func continuationSettlementArgs(t *testing.T, scope domain.VerifiedCharacterArbitrationSourcesV1, owner string) map[string]any {
	t.Helper()
	args := artifactFlowArbiterArgs(scope)
	resolution := artifactFlowResolution(t, args, owner)
	resolution["completion_state"] = "in_progress"
	executions := resolution["self_executions"].([]domain.CharacterSelfExecutionV2)
	executions[0].Status = "in_progress"
	proposal := artifactFlowProposal(t, scope, owner)
	before := *artifactFlowResource(t, *scope.Input().Stimulus.PhysicalState, continuationSettlementFuelID).ActualAmount
	args["resource_settlements"] = []domain.ResourceSettlementV2{{ResourceID: continuationSettlementFuelID, Before: artifactFlowNumber(before), Delta: artifactFlowNumber(-0.2), After: artifactFlowNumber(before - 0.2), EvidenceRefs: []string{proposal.Digest}}}
	return args
}

func newContinuationSettlementFixture(t *testing.T) *artifactFlowFixture {
	t.Helper()
	f := newArtifactFlowFixture(t)
	view := f.prepare(t)
	author := artifactFlowSubmitArgs(f.observation(t, f.author), "continued_inspection")
	task := &author["self_tasks"].([]domain.CharacterSelfTaskV2)[0]
	task.ProgressTarget = artifactFlowNumber(8)
	task.ResourceIDs = []string{continuationSettlementFuelID}
	author["work_continuations"] = []domain.CharacterWorkContinuationAuthorizationV1{{TaskID: task.TaskID, UntilTarget: true}}
	f.submit(t, view, f.author, author)
	f.submit(t, view, f.peer, artifactFlowSubmitArgs(f.observation(t, f.peer), "peer_1"))
	scope, err := view.Sources(1)
	artifactFlowMust(t, err)
	tool, err := NewResolveCharacterArbitrationV3Tool(f.st, f.session, view, 1, artifactFlowProtocol)
	artifactFlowMust(t, err)
	commitContinuationSettlementFixture(t, f, view, tool, continuationSettlementArgs(t, scope, f.author))
	return f
}

func commitContinuationSettlementFixture(t *testing.T, f *artifactFlowFixture, view *store.CharacterArbitrationV3, tool *ResolveChapterWorldTool, args map[string]any) {
	t.Helper()
	f.commit(t, view, tool, args)
	// Continuation's verified-feedback filter uses the original arbitration
	// digest. Keep that allowed canonical memory source, as the Store's V3
	// continuation fixture does, instead of the artifact fixture's display alias.
	for i := range f.input.Observations {
		o := &f.input.Observations[i]
		for _, memory := range f.input.Memories {
			if memory.AgentID != o.AgentID {
				continue
			}
			for j := range o.Memory {
				for _, fact := range memory.Facts {
					if fact.ID == o.Memory[j].ID {
						o.Memory[j].SourceDigest = fact.SourceDigest
					}
				}
			}
		}
		var err error
		*o, err = domain.FinalizeCharacterObservationPacket(*o)
		artifactFlowMust(t, err)
		for j := range f.input.Activation.Entries {
			if f.input.Activation.Entries[j].AgentID == o.AgentID {
				f.input.Activation.Entries[j].ObservationDigest = o.Digest
			}
		}
	}
	var err error
	f.input.Activation, err = domain.FinalizeCharacterAgentActivation(f.input.Activation)
	artifactFlowMust(t, err)
	f.input, err = domain.FinalizeCharacterActivationInputSet(f.input)
	artifactFlowMust(t, err)
}

func prepareContinuationSettlementRound(t *testing.T, f *artifactFlowFixture) (*store.CharacterArbitrationV3, domain.VerifiedCharacterArbitrationSourcesV1, *ResolveChapterWorldTool) {
	t.Helper()
	proofs, err := f.st.CharacterAgents.ForActivationCycle(f.session)
	artifactFlowMust(t, err)
	artifactFlowMust(t, proofs.PublishActivationInputs(f.input))
	prefix, err := f.st.LoadVerifiedCharacterActivationPrefix(f.session.GenerationID, f.session.Chapter)
	artifactFlowMust(t, err)
	eligible, err := domain.EvaluateCharacterWorkContinuationForRoundV1(*prefix, f.input, f.author)
	artifactFlowMust(t, err)
	if !eligible.Eligible || eligible.Receipt == nil {
		t.Fatalf("fixture has no verified current continuation: %+v", eligible)
	}
	view, err := f.st.PrepareCharacterArbitrationV3(f.session, []domain.CharacterWorkContinuationReceiptV1{*eligible.Receipt}, artifactFlowProtocol)
	artifactFlowMust(t, err)
	f.submit(t, view, f.peer, artifactFlowSubmitArgs(f.observation(t, f.peer), fmt.Sprintf("peer_%d", eligible.Receipt.Cycle)))
	scope, err := view.Sources(1)
	artifactFlowMust(t, err)
	tool, err := NewResolveCharacterArbitrationV3Tool(f.st, f.session, view, 1, artifactFlowProtocol)
	artifactFlowMust(t, err)
	return view, scope, tool
}

func TestContinuationSettlementAliasRealToolCanonicalizesAndRetriesWithoutChangingEvidence(t *testing.T) {
	for _, aliasFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(aliasFirst), func(t *testing.T) {
			f := newContinuationSettlementFixture(t)
			view, scope, tool := prepareContinuationSettlementRound(t, f)
			grant := scope.Continuations()[0]
			canonical := continuationSettlementArgs(t, scope, f.author)
			canonicalRaw, _ := json.Marshal(canonical)
			alias := continuationSettlementArgs(t, scope, f.author)
			alias["resource_settlements"].([]domain.ResourceSettlementV2)[0].EvidenceRefs = []string{grant.Digest, grant.OriginProposalDigest, grant.Digest}
			aliasRaw, _ := json.Marshal(alias)
			scopeBefore, _ := json.Marshal([]any{scope.Input(), scope.Sources(), scope.Continuations(), scope.EffectiveProposals()})
			first, second := canonicalRaw, aliasRaw
			if aliasFirst {
				first, second = aliasRaw, canonicalRaw
			}
			result, err := tool.Execute(context.Background(), first)
			artifactFlowMust(t, err)
			receipt, err := view.LoadArbitration(1)
			artifactFlowMust(t, err)
			if receipt == nil || !reflect.DeepEqual(receipt.ResourceSettlements[0].EvidenceRefs, []string{grant.OriginProposalDigest}) {
				t.Fatalf("grant escaped into canonical receipt: %+v", receipt)
			}
			receiptBefore, _ := json.Marshal(receipt)
			filesBefore := arbitrationReferenceFiles(t, f.st.Dir())
			for _, raw := range []json.RawMessage{second, aliasRaw, aliasRaw} {
				retry, err := tool.Execute(context.Background(), raw)
				artifactFlowMust(t, err)
				if string(retry) != string(result) || !reflect.DeepEqual(filesBefore, arbitrationReferenceFiles(t, f.st.Dir())) {
					t.Fatal("alias/canonical retry changed the original receipt or source files")
				}
			}
			replayed, err := view.LoadArbitration(1)
			artifactFlowMust(t, err)
			replayedRaw, _ := json.Marshal(replayed)
			if string(receiptBefore) != string(replayedRaw) {
				t.Fatal("canonical replay changed receipt bytes")
			}
			scopeAfter, _ := json.Marshal([]any{scope.Input(), scope.Sources(), scope.Continuations(), scope.EffectiveProposals()})
			if string(scopeBefore) != string(scopeAfter) {
				t.Fatal("alias normalization changed verified source/proposal snapshots")
			}
			candidate := domain.WorldArbitrationReceipt{ResourceSettlements: []domain.ResourceSettlementV2{{EvidenceRefs: []string{grant.Digest}}}}
			normalized := tool.normalizeContinuationSettlementEvidence(candidate)
			if candidate.ResourceSettlements[0].EvidenceRefs[0] != grant.Digest || normalized.ResourceSettlements[0].EvidenceRefs[0] != grant.OriginProposalDigest {
				t.Fatal("normalizer did not detach its settlement/evidence slices")
			}
		})
	}
}

func TestContinuationSettlementAliasRejectsForeignExpiredAndNonSettlementReferences(t *testing.T) {
	f := newContinuationSettlementFixture(t)
	view, scope, tool := prepareContinuationSettlementRound(t, f)
	grant := scope.Continuations()[0]
	for _, mode := range []string{"unknown", "knowledge", "measurement_mechanism", "reading", "no_verified_scope"} {
		t.Run(mode, func(t *testing.T) {
			args := continuationSettlementArgs(t, scope, f.author)
			resolution := artifactFlowResolution(t, args, f.author)
			switch mode {
			case "unknown":
				args["resource_settlements"].([]domain.ResourceSettlementV2)[0].EvidenceRefs = []string{"sha256:" + strings.Repeat("f", 64)}
			case "knowledge":
				resolution["post_state"] = map[string]any{"location": scope.EffectiveProposals()[0].Location, "resource_updates": []map[string]any{{"resource_id": continuationSettlementFuelID, "perception": map[string]any{"kind": "last_observed", "amount": 11.6, "as_of_chapter": 1, "evidence_refs": []string{grant.Digest}}}}}
			case "measurement_mechanism":
				resolution["mechanism_refs"] = []string{grant.Digest}
			case "reading":
				resolution["post_state"] = map[string]any{"location": artifactFlowProposal(t, scope, f.author).Location, "received_facts": []map[string]any{{"source_type": "resource_read", "resource_id": continuationSettlementFuelID, "source_id": grant.Digest}}}
			case "no_verified_scope":
				args["resource_settlements"].([]domain.ResourceSettlementV2)[0].EvidenceRefs = []string{grant.Digest}
			}
			candidateTool := tool
			if mode == "no_verified_scope" {
				candidateTool = NewResolveChapterWorldTool(f.st, scope.Input().Stimulus, scope.Input().Activation, scope.EffectiveProposals(), artifactFlowProtocol, nil, 1)
			}
			f.rejectArbitration(t, candidateTool, args)
		})
	}
	// Advance through the original legal call. The previous grant must not be
	// usable in the next cycle, even though it names the same original proposal.
	commitContinuationSettlementFixture(t, f, view, tool, continuationSettlementArgs(t, scope, f.author))
	_, next, nextTool := prepareContinuationSettlementRound(t, f)
	if next.Continuations()[0].OriginProposalDigest != grant.OriginProposalDigest || next.Continuations()[0].Digest == grant.Digest {
		t.Fatal("fixture did not create a later grant for the same original proposal")
	}
	stale := continuationSettlementArgs(t, next, f.author)
	stale["resource_settlements"].([]domain.ResourceSettlementV2)[0].EvidenceRefs = []string{grant.Digest}
	f.rejectArbitration(t, nextTool, stale)
	// The old tool's captured private scope cannot bypass the Store's CAS.
	oldArgs := continuationSettlementArgs(t, scope, f.author)
	oldArgs["resource_settlements"].([]domain.ResourceSettlementV2)[0].EvidenceRefs = []string{grant.Digest}
	f.rejectArbitration(t, tool, oldArgs)
}

func TestContinuationSettlementAliasRejectsSupersededR2AdmissionGrant(t *testing.T) {
	f := newContinuationSettlementFixture(t)
	view, scope, tool := prepareContinuationSettlementRound(t, f)
	grant := scope.Continuations()[0]
	r1 := artifactFlowArbiterArgs(scope)
	clock := r1["story_time"].(domain.StoryTimeChapterSchedule)
	clock.EndDay = clock.StartDay
	r1["story_time"], r1["finalized"] = clock, false
	r1["conflicts"] = []domain.WorldArbitrationConflict{{ID: "revise-order", Kind: "time", AffectedAgentIDs: []string{f.author}, Feedback: "请本人确认本次工序顺序"}}
	for _, r := range r1["resolutions"].([]map[string]any) {
		r["self_executions"] = []domain.CharacterSelfExecutionV2{}
	}
	raw, _ := json.Marshal(r1)
	_, err := tool.Execute(context.Background(), raw)
	artifactFlowMust(t, err)
	prior, err := view.LoadArbitration(1)
	artifactFlowMust(t, err)
	o := f.observation(t, f.author)
	o.Round = 2
	o.ConflictFeedback = domain.CharacterArbitrationFeedbackForOwnerV1(*prior, f.author, scope.Input().Stimulus.Sources)
	o, err = domain.FinalizeCharacterObservationPacket(o)
	artifactFlowMust(t, err)
	artifactFlowMust(t, view.SaveObservation(o))
	submit, err := NewSubmitCharacterActivationV3DecisionTool(f.st, f.session, o, view)
	artifactFlowMust(t, err)
	raw, _ = json.Marshal(artifactFlowSubmitArgs(o, "revised_work"))
	_, err = submit.Execute(context.Background(), raw)
	artifactFlowMust(t, err)
	r2, err := view.Sources(2)
	artifactFlowMust(t, err)
	if len(r2.AdmissionContinuations()) != 1 || len(r2.Continuations()) != 0 {
		t.Fatal("fixture lost the distinction between admission history and current authority")
	}
	tool2, err := NewResolveCharacterArbitrationV3Tool(f.st, f.session, view, 2, artifactFlowProtocol)
	artifactFlowMust(t, err)
	args := artifactFlowArbiterArgs(r2)
	before := *artifactFlowResource(t, *r2.Input().Stimulus.PhysicalState, continuationSettlementFuelID).ActualAmount
	args["resource_settlements"] = []domain.ResourceSettlementV2{{ResourceID: continuationSettlementFuelID, Before: artifactFlowNumber(before), Delta: artifactFlowNumber(-0.2), After: artifactFlowNumber(before - 0.2), EvidenceRefs: []string{grant.Digest}}}
	f.rejectArbitration(t, tool2, args)
	if receipt, err := view.LoadArbitration(2); err != nil || receipt != nil {
		t.Fatalf("superseded grant wrote an R2 receipt: %v", err)
	}
}
