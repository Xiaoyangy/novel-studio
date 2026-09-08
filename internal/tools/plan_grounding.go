package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/store"
)

const planGroundingPartialReceiptKey = "_grounding_review"

var ErrPlanGroundingReviewRequired = errors.New("plan grounding requires an explicit model-review lane")

type PlanGroundingReviewer struct {
	Protocol string
	Review   func(context.Context, domain.PlanGroundingInput) (domain.PlanGroundingVerdict, error)
	// Resolve pins one current model/protocol snapshot before cache lookup.
	// Fixed reviewers and Host-only callers leave it nil.
	Resolve              func() (PlanGroundingReviewer, error)
	ResolveForSimulation func(domain.ChapterWorldSimulation) (PlanGroundingReviewer, error)
}

type planGroundingExecution struct {
	ctx      context.Context
	reviewer PlanGroundingReviewer
}

func (t *PlanChapterTool) WithGroundingReviewer(reviewer PlanGroundingReviewer) *PlanChapterTool {
	t.grounding = reviewer
	return t
}

func (t *PlanDetailsTool) WithGroundingReviewer(reviewer PlanGroundingReviewer) *PlanDetailsTool {
	t.grounding = reviewer
	return t
}

// This gate runs after cheap deterministic validation but before consuming the
// access receipt, saving a formal plan, or advancing its checkpoint. Rejected
// partials remain repairable; identical input (including model/policy) is free.
func reviewChapterPlanGrounding(s *store.Store, plan *domain.ChapterPlan, options ...planGroundingExecution) error {
	sim, err := s.LoadChapterWorldSimulation(plan.Chapter)
	if err != nil {
		return err
	}
	if sim == nil || !domain.HasPlanGroundingPolicy(*sim) {
		return nil
	}
	execution := planGroundingExecution{ctx: context.Background()}
	if len(options) == 1 {
		execution = options[0]
	}
	if execution.ctx == nil {
		execution.ctx = context.Background()
	}
	if err := execution.ctx.Err(); err != nil {
		return err
	}
	if execution.reviewer.ResolveForSimulation != nil {
		resolved, err := execution.reviewer.ResolveForSimulation(*sim)
		if err != nil {
			return fmt.Errorf("resolve exact grounding reviewer: %w", err)
		}
		execution.reviewer = resolved
	} else if execution.reviewer.Resolve != nil {
		resolved, err := execution.reviewer.Resolve()
		if err != nil {
			return fmt.Errorf("resolve exact grounding reviewer: %w", err)
		}
		execution.reviewer = resolved
	}
	// Host-only repair lanes may reuse a previously paid exact pass, but never
	// acquire a new model capability implicitly. The private stamp is written
	// only after a validated review; it cannot make an unreviewed partial valid.
	partial, err := s.Drafts.LoadChapterPlanPartial(plan.Chapter)
	if err != nil {
		return err
	}
	protocol := execution.reviewer.Protocol
	var paidReceipt *domain.PlanGroundingReceipt
	if execution.reviewer.Review == nil {
		receipt := plan.GroundingReview
		if receipt == nil && partial != nil {
			if value, exists := partial[planGroundingPartialReceiptKey]; exists {
				raw, err := json.Marshal(value)
				if err != nil {
					return err
				}
				if err := json.Unmarshal(raw, &receipt); err != nil {
					return fmt.Errorf("invalid partial grounding stamp: %w", err)
				}
			}
		}
		if receipt == nil || !receipt.Verdict.Pass {
			return fmt.Errorf("%w; host-only finalize has no exact paid pass: %w", ErrPlanGroundingReviewRequired, errs.ErrToolPrecondition)
		}
		protocol = receipt.ReviewProtocol
		paidReceipt = receipt
	}
	input, err := currentPlanGroundingInput(s, *plan, *sim, protocol)
	if err != nil {
		return err
	}
	digest, err := domain.PlanGroundingInputDigest(input)
	if err != nil {
		return err
	}
	audit, err := s.LoadPlanGroundingAudit(digest)
	if err != nil {
		return err
	}
	if audit == nil {
		if paidReceipt != nil && paidReceipt.InputDigest == digest {
			candidate := *plan
			candidate.GroundingReview = paidReceipt
			if err := validateCurrentPlanGrounding(s, candidate); err != nil {
				return err
			}
			audit = &domain.PlanGroundingAudit{Input: input, Receipt: *paidReceipt}
		}
	}
	if audit == nil {
		if execution.reviewer.Review == nil {
			return fmt.Errorf("plan changed after its grounding review; %w: %w", ErrPlanGroundingReviewRequired, errs.ErrToolPrecondition)
		}
		verdict, err := execution.reviewer.Review(execution.ctx, input)
		if err != nil {
			return fmt.Errorf("plan grounding review did not complete: %w", err)
		}
		receipt, err := domain.FinalizePlanGroundingReceipt(input, verdict)
		if err != nil {
			return fmt.Errorf("plan grounding review invalid: %w", err)
		}
		audit = &domain.PlanGroundingAudit{Input: input, Receipt: receipt}
		if err := s.SavePlanGroundingAudit(*audit); err != nil {
			return err
		}
	}
	if !audit.Receipt.Verdict.Pass {
		var findings []string
		for _, finding := range audit.Receipt.Verdict.Findings {
			findings = append(findings, fmt.Sprintf("%s: %s（计划 %q；裁决依据 %s: %q）", finding.PlanPath, finding.Explanation, finding.PlanQuote, finding.SourcePath, finding.SourceQuote))
		}
		return fmt.Errorf("计划违背最终角色裁决：%s。只修指出的计划字段，角色选择与裁决不可改；不要仅重绑 simulation_id 或重发未改变的 finalize: %w", strings.Join(findings, "；"), errs.ErrToolPrecondition)
	}
	plan.GroundingReview = &audit.Receipt
	if partial != nil {
		partial[planGroundingPartialReceiptKey] = audit.Receipt
		if err := s.Drafts.SaveChapterPlanPartial(plan.Chapter, partial); err != nil {
			return fmt.Errorf("persist exact grounding stamp before finalize: %w", err)
		}
	}
	return nil
}

func loadPlanGroundingSources(s *store.Store, sim domain.ChapterWorldSimulation) (domain.CharacterObservationPacket, domain.WorldArbitrationReceipt, error) {
	fail := func(err error) (domain.CharacterObservationPacket, domain.WorldArbitrationReceipt, error) {
		return domain.CharacterObservationPacket{}, domain.WorldArbitrationReceipt{}, err
	}
	if sim.CharacterAgentProtocol == nil {
		return fail(fmt.Errorf("grounding has no independent protocol receipt"))
	}
	arbitration, err := s.CharacterAgents.LoadArbitration(sim.GenerationID, sim.Chapter, sim.CharacterAgentProtocol.ArbitrationRound)
	if err != nil {
		return fail(err)
	}
	if arbitration == nil {
		return loadPromotedPlanGroundingSources(s, sim)
	}
	for _, resolution := range arbitration.Resolutions {
		if resolution.Character != sim.ProtagonistProjection.Protagonist {
			continue
		}
		for round := arbitration.Round; round >= 1; round-- {
			proposal, err := s.CharacterAgents.LoadProposal(sim.GenerationID, sim.Chapter, round, resolution.AgentID)
			if err != nil {
				return fail(err)
			}
			if proposal == nil || proposal.Digest != resolution.ProposalDigest {
				continue
			}
			observation, err := s.CharacterAgents.LoadObservation(sim.GenerationID, sim.Chapter, round, resolution.AgentID)
			if err != nil {
				return fail(err)
			}
			if observation == nil || observation.Digest != proposal.ObservationDigest {
				return fail(fmt.Errorf("grounding POV observation does not bind accepted proposal"))
			}
			return *observation, *arbitration, nil
		}
	}
	return fail(fmt.Errorf("grounding POV accepted proposal missing"))
}

func loadPromotedPlanGroundingSources(s *store.Store, sim domain.ChapterWorldSimulation) (domain.CharacterObservationPacket, domain.WorldArbitrationReceipt, error) {
	fail := func(err error) (domain.CharacterObservationPacket, domain.WorldArbitrationReceipt, error) {
		return domain.CharacterObservationPacket{}, domain.WorldArbitrationReceipt{}, err
	}
	matched, err := validatePromotedCharacterSimulation(s, sim)
	if err != nil {
		return fail(err)
	}
	if !matched {
		return fail(fmt.Errorf("grounding final arbitration missing and no exact current promotion"))
	}
	bundles, err := s.ProjectedV2().LoadProjectedChapterBundles(sim.GenerationID)
	if err != nil {
		return fail(err)
	}
	for _, bundle := range bundles {
		if bundle.Chapter != sim.Chapter || bundle.CharacterAgentEvidence == nil {
			continue
		}
		evidence := bundle.CharacterAgentEvidence
		for _, arbitration := range evidence.Arbitrations {
			if sim.CharacterAgentProtocol == nil || arbitration.Digest != sim.CharacterAgentProtocol.ArbitrationDigest {
				continue
			}
			observation, err := domain.GroundingPOVObservation(sim, evidence.Observations, evidence.Proposals, arbitration)
			if err != nil {
				return fail(err)
			}
			return observation, arbitration, nil
		}
	}
	return fail(fmt.Errorf("promoted bundle has no matching grounding sources"))
}

func validateCurrentPlanGrounding(s *store.Store, plan domain.ChapterPlan) error {
	sim, err := s.LoadChapterWorldSimulation(plan.Chapter)
	if err != nil {
		return err
	}
	if sim == nil {
		if plan.GroundingReview != nil {
			return fmt.Errorf("reviewed plan lost its simulation")
		}
		return nil
	}
	if !domain.HasPlanGroundingPolicy(*sim) && plan.GroundingReview == nil {
		return nil
	}
	if plan.GroundingReview == nil || !plan.GroundingReview.Verdict.Pass {
		return fmt.Errorf("formal plan lacks passing grounding receipt: %w", errs.ErrToolPrecondition)
	}
	audit, err := s.LoadPlanGroundingAudit(plan.GroundingReview.InputDigest)
	if err != nil {
		return err
	}
	if audit != nil {
		var input domain.PlanGroundingInput
		if sim.CharacterActivation != nil {
			input, err = currentPlanGroundingInput(s, plan, *sim, plan.GroundingReview.ReviewProtocol)
		} else {
			input, err = domain.NewPlanGroundingInput(plan, *sim, audit.Input.POVObservation, audit.Input.Arbitration, plan.GroundingReview.ReviewProtocol)
		}
		if err != nil {
			return err
		}
		return domain.ValidatePlanGroundingAudit(domain.PlanGroundingAudit{Input: input, Receipt: *plan.GroundingReview})
	}
	// Promotion carries the full sealed proof, not mutable projected sidecars.
	matched, err := validatePromotedCharacterSimulation(s, *sim)
	if err != nil {
		return err
	}
	if !matched {
		return fmt.Errorf("formal plan grounding audit is missing")
	}
	bundles, err := s.ProjectedV2().LoadProjectedChapterBundles(sim.GenerationID)
	if err != nil {
		return err
	}
	for _, bundle := range bundles {
		if bundle.Chapter == plan.Chapter {
			return domain.ValidatePlanGroundingBundle(plan, *sim, bundle.CharacterAgentEvidence, bundle.CharacterActivationEvidence)
		}
	}
	return fmt.Errorf("promoted grounding bundle missing")
}
