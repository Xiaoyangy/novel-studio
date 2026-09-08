package domain

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

const PlanGroundingPolicyV1 = "plan-grounding:arbitration.v1"

// These are classification findings, not the judge's private reasoning. The
// host verifies that both cited excerpts exist at their exact JSON pointers.
type PlanGroundingFinding struct {
	Kind        string `json:"kind"`
	PlanPath    string `json:"plan_path"`
	PlanQuote   string `json:"plan_quote"`
	SourcePath  string `json:"source_path"`
	SourceQuote string `json:"source_quote"`
	Explanation string `json:"explanation"`
}

type PlanGroundingVerdict struct {
	Pass     bool                   `json:"pass"`
	Findings []PlanGroundingFinding `json:"findings"`
}

type PlanGroundingInput struct {
	Policy         string                         `json:"policy"`
	ReviewProtocol string                         `json:"review_protocol"`
	Plan           ChapterPlan                    `json:"plan"`
	Simulation     PlanGroundingSimulation        `json:"simulation"`
	POVObservation CharacterObservationPacket     `json:"pov_observation"`
	Arbitration    WorldArbitrationReceipt        `json:"arbitration"`
	Activation     *PlanGroundingActivationSource `json:"activation,omitempty"`
}

// The full simulation digest remains bound, but its duplicated actor/resource
// post-state is already present in arbitration. Do not bill it a second time.
type PlanGroundingSimulation struct {
	SimulationID          string                        `json:"simulation_id"`
	GenerationID          string                        `json:"generation_id"`
	Chapter               int                           `json:"chapter"`
	TimeWindow            string                        `json:"time_window"`
	StoryTime             *StoryTimeChapterSchedule     `json:"story_time,omitempty"`
	ProtagonistProjection ProtagonistDecisionProjection `json:"protagonist_projection"`
	Digest                string                        `json:"digest"`
}

type PlanGroundingReceipt struct {
	Policy         string               `json:"policy"`
	ReviewProtocol string               `json:"review_protocol"`
	InputDigest    string               `json:"input_digest"`
	Verdict        PlanGroundingVerdict `json:"verdict"`
	Digest         string               `json:"digest"`
}

type PlanGroundingAudit struct {
	Input   PlanGroundingInput   `json:"input"`
	Receipt PlanGroundingReceipt `json:"receipt"`
}

func HasPlanGroundingPolicy(sim ChapterWorldSimulation) bool {
	return planningV2ContainsExactString(sim.Sources, PlanGroundingPolicyV1) || planningV2ContainsExactString(sim.Sources, PlanGroundingActivationPolicy)
}

func NewPlanGroundingInput(plan ChapterPlan, sim ChapterWorldSimulation, observation CharacterObservationPacket, arbitration WorldArbitrationReceipt, protocol string) (PlanGroundingInput, error) {
	if sim.CharacterActivation != nil || len(sim.CharacterDecisionTrace) > 0 {
		return PlanGroundingInput{}, fmt.Errorf("whole-chapter activation cannot be grounded against only one cycle's arbitration")
	}
	plan.GroundingReview = nil // The receipt must not attest to itself.
	simulationDigest, err := characterAgentDigest(sim)
	if err != nil {
		return PlanGroundingInput{}, err
	}
	view := PlanGroundingSimulation{sim.SimulationID, sim.GenerationID, sim.Chapter, sim.TimeWindow, sim.StoryTime, sim.ProtagonistProjection, simulationDigest}
	input := PlanGroundingInput{Policy: PlanGroundingPolicyV1, ReviewProtocol: protocol, Plan: plan, Simulation: view, POVObservation: observation, Arbitration: arbitration}
	if err := validatePlanningV2Digest("review_protocol", protocol); err != nil {
		return input, err
	}
	if sim.Version != 2 || sim.CharacterAgentProtocol == nil || plan.Chapter != sim.Chapter ||
		plan.CausalSimulation.WorldSimulationID != sim.SimulationID ||
		plan.CausalSimulation.ProtagonistDecision != sim.ProtagonistProjection.ChosenDecision {
		return input, fmt.Errorf("plan grounding requires the exact independent simulation and protagonist decision")
	}
	if !arbitration.Finalized || arbitration.Digest != sim.CharacterAgentProtocol.ArbitrationDigest ||
		arbitration.GenerationID != sim.GenerationID || arbitration.Chapter != sim.Chapter ||
		observation.GenerationID != sim.GenerationID || observation.Chapter != sim.Chapter ||
		observation.Character != sim.ProtagonistProjection.Protagonist {
		return input, fmt.Errorf("plan grounding requires final arbitration and this chapter's POV observation")
	}
	found := false
	for _, resolution := range arbitration.Resolutions {
		if resolution.AgentID == observation.AgentID && resolution.Character == observation.Character {
			found = true
		}
	}
	if !found {
		return input, fmt.Errorf("plan grounding POV has no final resolution")
	}
	return input, nil
}

func PlanGroundingInputDigest(input PlanGroundingInput) (string, error) {
	if (input.Policy != PlanGroundingPolicyV1 && input.Policy != PlanGroundingActivationPolicy) || input.Plan.GroundingReview != nil {
		return "", fmt.Errorf("invalid plan grounding input policy or recursive receipt")
	}
	if input.Policy == PlanGroundingActivationPolicy {
		if input.Activation == nil || len(input.Activation.Cycles) == 0 || input.Arbitration.Version != "" {
			return "", fmt.Errorf("activation grounding requires the full trace, not one arbitration")
		}
	} else if input.Activation != nil {
		return "", fmt.Errorf("legacy grounding cannot mix an activation trace")
	}
	return characterAgentDigest(input)
}

func FinalizePlanGroundingReceipt(input PlanGroundingInput, verdict PlanGroundingVerdict) (PlanGroundingReceipt, error) {
	if len(verdict.Findings) > 8 || verdict.Pass != (len(verdict.Findings) == 0) {
		return PlanGroundingReceipt{}, fmt.Errorf("grounding verdict must pass with no findings or fail with 1-8 findings")
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return PlanGroundingReceipt{}, err
	}
	var document any
	if err := json.Unmarshal(raw, &document); err != nil {
		return PlanGroundingReceipt{}, err
	}
	for i, finding := range verdict.Findings {
		switch finding.Kind {
		case "time", "location", "knowledge", "intent", "outcome":
		default:
			return PlanGroundingReceipt{}, fmt.Errorf("grounding finding %d has unknown kind", i)
		}
		if strings.TrimSpace(finding.Explanation) == "" || utf8.RuneCountInString(finding.Explanation) > 500 {
			return PlanGroundingReceipt{}, fmt.Errorf("grounding finding %d explanation must be concise", i)
		}
		if !strings.HasPrefix(finding.PlanPath, "/plan/") ||
			!(strings.HasPrefix(finding.SourcePath, "/simulation/") || strings.HasPrefix(finding.SourcePath, "/arbitration/") || strings.HasPrefix(finding.SourcePath, "/pov_observation/") || (input.Activation != nil && strings.HasPrefix(finding.SourcePath, "/activation/"))) {
			return PlanGroundingReceipt{}, fmt.Errorf("grounding finding %d must cite plan and authoritative source separately", i)
		}
		for path, quote := range map[string]string{finding.PlanPath: finding.PlanQuote, finding.SourcePath: finding.SourceQuote} {
			value, ok := planGroundingPointer(document, path)
			if !ok || strings.TrimSpace(quote) == "" || utf8.RuneCountInString(quote) > 600 || !strings.Contains(value, quote) {
				return PlanGroundingReceipt{}, fmt.Errorf("grounding finding %d has unverifiable excerpt at %s", i, path)
			}
		}
	}
	digest, err := PlanGroundingInputDigest(input)
	if err != nil {
		return PlanGroundingReceipt{}, err
	}
	receipt := PlanGroundingReceipt{Policy: input.Policy, ReviewProtocol: input.ReviewProtocol, InputDigest: digest, Verdict: verdict}
	receipt.Digest, err = characterAgentDigest(receipt)
	return receipt, err
}

func ValidatePlanGroundingAudit(audit PlanGroundingAudit) error {
	receipt, err := FinalizePlanGroundingReceipt(audit.Input, audit.Receipt.Verdict)
	if err != nil {
		return err
	}
	want, err := characterAgentDigest(receipt)
	if err != nil {
		return err
	}
	got, err := characterAgentDigest(audit.Receipt)
	if err != nil || got != want {
		return fmt.Errorf("plan grounding receipt binding mismatch")
	}
	return nil
}

func planGroundingPointer(root any, path string) (string, bool) {
	if !strings.HasPrefix(path, "/") {
		return "", false
	}
	value := root
	for _, part := range strings.Split(path[1:], "/") {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		switch object := value.(type) {
		case map[string]any:
			var ok bool
			value, ok = object[part]
			if !ok {
				return "", false
			}
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(object) {
				return "", false
			}
			value = object[i]
		default:
			return "", false
		}
	}
	if text, ok := value.(string); ok {
		return text, true
	}
	raw, err := json.Marshal(value)
	return string(raw), err == nil
}

func GroundingPOVObservation(sim ChapterWorldSimulation, observations []CharacterObservationPacket, proposals []CharacterDecisionProposal, arbitration WorldArbitrationReceipt) (CharacterObservationPacket, error) {
	for _, resolution := range arbitration.Resolutions {
		if resolution.Character != sim.ProtagonistProjection.Protagonist {
			continue
		}
		for _, proposal := range proposals {
			if proposal.AgentID != resolution.AgentID || proposal.Digest != resolution.ProposalDigest {
				continue
			}
			for _, observation := range observations {
				if observation.AgentID == proposal.AgentID && observation.Digest == proposal.ObservationDigest {
					return observation, nil
				}
			}
		}
	}
	return CharacterObservationPacket{}, fmt.Errorf("grounding lacks the POV observation used by the accepted proposal")
}

// Bundle verification is offline. It proves exact evidence binding, not that
// a probabilistic judge is infallible; adversarial/live evaluations test that.
func ValidatePlanGroundingBundle(plan ChapterPlan, sim ChapterWorldSimulation, evidence *CharacterAgentEvidenceBundle, activationEvidence ...*CharacterActivationChapterEvidence) error {
	if sim.CharacterActivation != nil {
		if len(activationEvidence) != 1 || activationEvidence[0] == nil || evidence != nil || plan.GroundingReview == nil || !plan.GroundingReview.Verdict.Pass {
			return fmt.Errorf("activation plan grounding requires a passing full-chapter evidence review")
		}
		input, err := NewActivationPlanGroundingInput(plan, sim, *activationEvidence[0], plan.GroundingReview.ReviewProtocol)
		if err != nil {
			return err
		}
		return ValidatePlanGroundingAudit(PlanGroundingAudit{Input: input, Receipt: *plan.GroundingReview})
	}
	if len(activationEvidence) > 0 && activationEvidence[0] != nil {
		return fmt.Errorf("non-activation simulation cannot carry activation grounding evidence")
	}
	if !HasPlanGroundingPolicy(sim) && plan.GroundingReview == nil {
		return nil
	}
	if plan.GroundingReview == nil || !plan.GroundingReview.Verdict.Pass || evidence == nil {
		return fmt.Errorf("plan grounding requires a passing exact-plan receipt and character evidence")
	}
	for _, arbitration := range evidence.Arbitrations {
		if sim.CharacterAgentProtocol == nil || arbitration.Digest != sim.CharacterAgentProtocol.ArbitrationDigest {
			continue
		}
		observation, err := GroundingPOVObservation(sim, evidence.Observations, evidence.Proposals, arbitration)
		if err != nil {
			return err
		}
		input, err := NewPlanGroundingInput(plan, sim, observation, arbitration, plan.GroundingReview.ReviewProtocol)
		if err != nil {
			return err
		}
		return ValidatePlanGroundingAudit(PlanGroundingAudit{input, *plan.GroundingReview})
	}
	return fmt.Errorf("plan grounding final arbitration is missing")
}

func ValidateGroundedRenderContract(bundle ProjectedChapterBundle) error {
	if !HasPlanGroundingPolicy(bundle.ChapterWorldSimulation) {
		return nil
	}
	plan, contract := bundle.ChapterPlan, bundle.HardRenderContract
	preserve := normalizeV2Strings(plan.Contract.ContinuityChecks)
	if len(preserve) == 0 {
		preserve = []string{"保持 projected_pre_state_root 已确认的前态事实与知识边界"}
	}
	for _, check := range []struct {
		name      string
		got, want []string
	}{
		{"must_occur", contract.MustOccur, normalizeV2Strings(plan.Contract.RequiredBeats)},
		{"must_not_occur", contract.MustNotOccur, normalizeV2Strings(plan.Contract.ForbiddenMoves)},
		{"must_preserve", contract.MustPreserve, preserve},
	} {
		if !slices.Equal(normalizeV2Strings(check.got), check.want) {
			return fmt.Errorf("grounded render contract %s differs from reviewed plan", check.name)
		}
	}
	if len(contract.RevealBudget) != len(plan.CausalSimulation.ReaderRetentionPlan.RevealBudget) {
		return fmt.Errorf("grounded render reveal budget differs from reviewed plan")
	}
	for i, item := range contract.RevealBudget {
		if item.Action != "limit" || item.Limit != plan.CausalSimulation.ReaderRetentionPlan.RevealBudget[i] {
			return fmt.Errorf("grounded render reveal budget[%d] differs from reviewed plan", i)
		}
	}
	return nil
}
