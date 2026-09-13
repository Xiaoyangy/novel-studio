package agents

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// Keep these adjacent top-level names so deterministic race shards distribute
// the deep cases instead of accumulating them in one shard entry.
func TestContinuationProducerRuntimeRecoversOldAndNewFrozenWorkSurfaceInspection(t *testing.T) {
	testContinuationProducerRuntimeRecovery(t, "surface-inspection", characterActivationProtocolV3Digest(), true, true)
}

func TestContinuationProducerRuntimeRecoversOldAndNewFrozenWorkCompletionView(t *testing.T) {
	testContinuationProducerRuntimeRecovery(t, "completion-view", characterActivationProtocolV3CompletionDigest(), true, true)
}

func TestContinuationProducerRuntimeRecoversOldAndNewFrozenWorkFullOwner(t *testing.T) {
	testContinuationProducerRuntimeRecovery(t, "full-owner", characterActivationProtocolV3HistoryDigest(), true, false)
}

func TestContinuationProducerRuntimeRecoversOldAndNewFrozenWorkLegacy(t *testing.T) {
	testContinuationProducerRuntimeRecovery(t, "legacy", characterActivationProtocolV3LegacyDigest(), false, false)
}

func testContinuationProducerRuntimeRecovery(t *testing.T, name, producer string, full, completions bool) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		st, cfg, boundary := activationV3RuntimeFixture(t)
		cfg.CharacterAgents.FrozenActivationProducer = producer
		model := &activationV3RuntimeModel{reviseAll: true, failSecondRevision: true}
		models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "v3-producer", model)}
		const generation = "pg2_producer_recovery"
		_, err := runCharacterActivationChapter(context.Background(), cfg, st, models, generation, 1, boundary, domain.ProjectedPlanningContextV2{}, nil, 4)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected durable partial-R2 interruption: %v", err)
		}
		prefix, err := st.LoadVerifiedCharacterActivationPrefix(generation, 1)
		selectionMust(t, err)
		if prefix == nil || len(prefix.Steps()) != 1 {
			t.Fatal("test did not save real source-bound grants")
		}
		view, err := st.LoadCharacterArbitrationV3(generation, 1)
		selectionMust(t, err)
		if view == nil || len(view.Continuations()) != 2 {
			t.Fatal("partial recovery lacks admitted continuers")
		}
		if domain.HasCharacterWorkContinuationHistoryPolicyV1(view.Input().Stimulus.Sources) != full {
			t.Fatal("initial source selected the wrong generation strategy")
		}
		if domain.HasCharacterSelfCompletionViewPolicyV1(view.Input().Stimulus.Sources) != completions {
			t.Fatal("initial source changed its frozen completion strategy")
		}
		wantSurface := producer == characterActivationProtocolV3Digest() || producer == characterActivationProtocolV3IncomingReadDigest()
		if domain.HasCharacterSurfaceInspectionPolicyV1(view.Input().Stimulus.Sources) != wantSurface {
			t.Fatal("initial source changed its frozen surface strategy")
		}
		proof, err := runCharacterActivationChapter(context.Background(), cfg, store.NewStore(st.Dir()), models, generation, 1, boundary, domain.ProjectedPlanningContextV2{}, nil, 4)
		selectionMust(t, err)
		if proof.ProtocolDigest != producer || model.actorCalls != 8 || model.revisionCalls != 3 || model.arbiterCalls != 4 {
			t.Fatalf("recovery changed producer or re-ran saved work: %s %d/%d/%d", proof.ProtocolDigest, model.actorCalls, model.revisionCalls, model.arbiterCalls)
		}
		for _, cycle := range proof.Cycles {
			if cycle.Evidence.ProtocolDigest != producer || domain.HasCharacterWorkContinuationHistoryPolicyV1(cycle.Evidence.Stimulus.Sources) != full || domain.HasCharacterSelfCompletionViewPolicyV1(cycle.Evidence.Stimulus.Sources) != completions {
				t.Fatal("cycle evidence upgraded or downgraded its frozen producer")
			}
			if domain.HasCharacterSurfaceInspectionPolicyV1(cycle.Evidence.Stimulus.Sources) != wantSurface {
				t.Fatal("cycle evidence changed its frozen surface strategy")
			}
		}
		calls := model.actorCalls + model.arbiterCalls + int(model.readiness.readiness.Load())
		cached, err := runCharacterActivationChapter(context.Background(), cfg, store.NewStore(st.Dir()), models, generation, 1, boundary, domain.ProjectedPlanningContextV2{}, nil, 4)
		selectionMust(t, err)
		if cached.Digest != proof.Digest || calls != model.actorCalls+model.arbiterCalls+int(model.readiness.readiness.Load()) {
			t.Fatal("completed old/new cache invoked a model or changed evidence")
		}
		wrong := cfg
		wrong.CharacterAgents.FrozenActivationProducer = characterActivationProtocolV3Digest()
		if full {
			wrong.CharacterAgents.FrozenActivationProducer = characterActivationProtocolV3LegacyDigest()
		}
		if _, err := runCharacterActivationChapter(context.Background(), wrong, st, models, generation, 1, boundary, domain.ProjectedPlanningContextV2{}, nil, 4); err == nil {
			t.Fatal("cached chapter accepted a different producer")
		}
		continuationProducerNextChapterInput(t, st, boundary, proof, producer)
	})
}

func continuationProducerNextChapterInput(t *testing.T, st *store.Store, boundary ProjectedArcBoundary, proof *domain.CharacterActivationChapterEvidence, producer string) {
	t.Helper()
	prefix, err := st.LoadVerifiedCharacterActivationPrefix(proof.Session.GenerationID, 1)
	selectionMust(t, err)
	steps := prefix.Steps()
	last := steps[len(steps)-1]
	physical := last.AfterState()
	encoded, err := domain.EncodeWorldPhysicalStateV2(physical)
	selectionMust(t, err)
	// Only the actual verified predecessor's physical state and clock enter the
	// next-input fixture. This test does not invent a chapter outcome or prose.
	projected := domain.ProjectedPlanningContextV2{Version: domain.ProjectedPlanningContextV2Version, GenerationID: proof.Session.GenerationID, NextChapter: 2, ThroughChapter: 1, StateRoot: last.Cycle().AfterPhysicalRoot, CumulativeState: []domain.ProjectedPlanningStateFactV2{
		{Category: "character_state", StableID: "actual-physical", Subject: "world", Field: domain.WorldPhysicalStateV2Field, Value: encoded, ThroughChapter: 1},
		{Category: "timeline", StableID: "actual-clock", Subject: "world", Field: "story_day", Value: fmt.Sprint(last.Cycle().EndDay), ThroughChapter: 1},
	}}
	projected.ContextDigest, err = domain.ComputeProjectedPlanningContextV2Digest(projected)
	selectionMust(t, err)
	selectionMust(t, domain.ValidateProjectedPlanningContextV2(projected))
	boundary.LastChapter, boundary.FrozenActivationProducer = 2, producer
	selectionMust(t, st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "各自检查", CoreEvent: "甲、乙、丙各自检查"}, {Chapter: 2, Title: "继续检查", CoreEvent: "甲、乙、丙继续各自检查"}}))
	opening, err := buildWorldStimulusDraft(st, proof.Session.GenerationID, 2, boundary, projected, nil, time.Now().UTC().Format(time.RFC3339Nano), domain.CharacterAgentDecisionProtocolV2Version)
	selectionMust(t, err)
	readiness, err := buildCharacterReadinessContext(st, proof.Session.GenerationID, 2, boundary, projected, opening)
	selectionMust(t, err)
	session, err := domain.NewCharacterActivationSession(proof.Session.GenerationID, 2, readiness.Digest, physical, opening.StoryClock.CurrentDay, 4)
	selectionMust(t, err)
	input, err := prepareInitialCharacterActivationInputs(st, session, boundary, projected, nil)
	selectionMust(t, err)
	selectionMust(t, validateCharacterActivationInputPolicy(input, session, domain.CharacterActivationCyclePolicyV3, producer))
	if characterActivationProtocolForStimulus(input.Stimulus) != producer {
		t.Fatal("later chapter silently selected the current producer")
	}
}
