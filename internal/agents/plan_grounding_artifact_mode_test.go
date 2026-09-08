package agents

import (
	"context"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
)

// This tests the review transport/policy binding, not a synthetic story's
// qualification for publication. Physical artifact proofs have domain tests.
func TestArtifactGroundingPinsAdditionalKnowledgeRulesWithoutChangingLegacyMode(t *testing.T) {
	model := &groundingProbeModel{args: `{"pass":true,"findings":[]}`}
	models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "grounding", model)}
	base := NewPlanGroundingReviewer(bootstrap.Config{}, models, nil)
	plainSim := domain.ChapterWorldSimulation{CharacterActivation: &domain.CharacterActivationSimulationBinding{}}
	plain, err := base.ResolveForSimulation(plainSim)
	if err != nil {
		t.Fatal(err)
	}
	artifactSim := plainSim
	artifactSim.Sources = []string{domain.CharacterWorkArtifactPolicyV1}
	artifacts, err := base.ResolveForSimulation(artifactSim)
	if err != nil {
		t.Fatal(err)
	}
	if artifacts.Protocol == plain.Protocol {
		t.Fatal("artifact rules reused the old review identity")
	}
	input := groundingBudgetInput("ACTUAL-PLAN")
	input.ReviewProtocol = plain.Protocol
	if _, err := plain.Review(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if model.messages[0].TextContent() != planGroundingPrompt+activationGroundingPrompt {
		t.Fatal("plain activation prompt changed")
	}
	input.ReviewProtocol = artifacts.Protocol
	input.POVObservation.Sources = []string{domain.CharacterWorkArtifactPolicyV1}
	input.Activation.Cycles[0].POVAfter.ArtifactViews = []domain.CharacterArtifactViewV1{{KnowledgeKind: "unread", ResourceID: "UNREAD-CAPABILITY"}}
	if _, err := artifacts.Review(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(model.messages[0].TextContent(), artifactGroundingPrompt) || !strings.Contains(model.messages[1].TextContent(), "UNREAD-CAPABILITY") {
		t.Fatal("reviewer did not receive the actual artifact view and knowledge rules")
	}
	calls := model.calls
	input.ReviewProtocol = plain.Protocol
	if _, err := plain.Review(context.Background(), input); err == nil || model.calls != calls {
		t.Fatal("old reviewer accepted artifact inputs or made a paid mismatch call")
	}
	input.ReviewProtocol = artifacts.Protocol
	input.POVObservation.Sources = nil
	if _, err := artifacts.Review(context.Background(), input); err == nil || model.calls != calls {
		t.Fatal("artifact reviewer accepted missing policy or made a paid mismatch call")
	}
	again, err := base.ResolveForSimulation(plainSim)
	if err != nil || again.Protocol != plain.Protocol {
		t.Fatal("artifact mode contaminated the plain reviewer identity")
	}
}
