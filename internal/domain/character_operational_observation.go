package domain

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const CharacterOperationalAvailabilityPolicyV1 = "character-operational-availability:receipt.v1"
const CharacterOperationalObservationRequestLimitV1 = 8
const CharacterOperationalObservationLimitV1 = 32
const CharacterOperationalObservationByteLimitV1 = 16 * 1024

// Purpose is the owner's unverified intended use, never a newly observed fact.
type CharacterOperationalObservationRequestV1 struct {
	Surface       string   `json:"surface,omitempty"`
	RequestID     string   `json:"request_id"`
	ResourceID    string   `json:"resource_id"`
	Purpose       string   `json:"purpose"`
	MechanismRef  string   `json:"mechanism_ref"`
	KnowledgeRefs []string `json:"knowledge_refs"`
}
type CharacterOperationalObservationResultV1 struct {
	Surface       string   `json:"surface,omitempty"`
	RequestID     string   `json:"request_id"`
	Result        string   `json:"result"`
	ObservedAtDay *float64 `json:"observed_at_day"`
}

// This host-derived owner receipt does not modify physical balances, access,
// capacity, or task completion. Its conclusion applies only at ObservedAtDay.
type CharacterOperationalObservationV1 struct {
	Surface              string   `json:"surface,omitempty"`
	ID                   string   `json:"id"`
	AgentID              string   `json:"agent_id"`
	GenerationID         string   `json:"generation_id"`
	Chapter              int      `json:"chapter"`
	TaskID               string   `json:"task_id"`
	RequestID            string   `json:"request_id"`
	ResourceID           string   `json:"resource_id"`
	ResourceLabel        string   `json:"resource_label"`
	Purpose              string   `json:"purpose"`
	MechanismRef         string   `json:"mechanism_ref"`
	Result               string   `json:"result"`
	ObservedAtDay        *float64 `json:"observed_at_day"`
	SourceProposalDigest string   `json:"source_proposal_digest"`
	SourceExperienceID   string   `json:"source_experience_id"`
}

func HasCharacterOperationalAvailabilityPolicyV1(sources []string) bool {
	for _, source := range sources {
		if strings.TrimSpace(source) == CharacterOperationalAvailabilityPolicyV1 {
			return true
		}
	}
	return false
}
func operationalBoundedV1(value string, limit int) bool {
	return strings.TrimSpace(value) != "" && utf8.RuneCountInString(value) <= limit && strings.IndexFunc(value, unicode.IsControl) < 0
}
func operationalResultV1(value string) bool {
	return value == "available" || value == "unavailable" || value == "inconclusive"
}
func operationalResourceV1(resource WorldResourceBalanceV2) bool {
	return resource.Unit == "" && resource.ActualAmount == nil && len(resource.ReadableFacts) == 0
}

func validateCharacterOperationalRequestsV1(task CharacterSelfTaskV2) error {
	if len(task.ObservationRequests) == 0 {
		return nil
	}
	if task.Kind != "work" || len(task.ObservationRequests) > CharacterOperationalObservationRequestLimitV1 {
		return fmt.Errorf("operational observation requests require a bounded work task")
	}
	seen := map[string]bool{}
	for _, request := range task.ObservationRequests {
		if request.Surface != "" && !IsCharacterInspectableSurfaceV1(request.Surface) {
			return fmt.Errorf("surface inspection request names an unsupported exterior facet")
		}
		if request.Surface != "" && !physicalContainsRefV2(task.ResourceIDs, request.ResourceID) {
			return fmt.Errorf("surface inspection work must explicitly include the inspected resource_id")
		}
		if !physicalIdentityV2(request.RequestID) || !operationalBoundedV1(request.RequestID, 128) || seen[request.RequestID] || !physicalResourceIDV2(request.ResourceID) || !operationalBoundedV1(request.Purpose, 200) || !operationalBoundedV1(request.MechanismRef, 128) || len(request.KnowledgeRefs) == 0 || len(request.KnowledgeRefs) > 16 {
			return fmt.Errorf("operational observation request identity, purpose or evidence is invalid")
		}
		seen[request.RequestID] = true
	}
	return nil
}
func validateCharacterOperationalResultsV1(execution CharacterSelfExecutionV2) error {
	if len(execution.ObservationResults) == 0 {
		return nil
	}
	if !selfExecutionActiveV2(execution.Status) || !physicalAmountV2(execution.StartDay) || !physicalAmountV2(execution.EndDay) || len(execution.ObservationResults) > CharacterOperationalObservationRequestLimitV1 {
		return fmt.Errorf("unexecuted task cannot publish operational observations")
	}
	seen := map[string]bool{}
	for _, result := range execution.ObservationResults {
		if !physicalIdentityV2(result.RequestID) || seen[result.RequestID] || !validOperationalResultForSurfaceV1(result.Surface, result.Result) || !physicalAmountV2(result.ObservedAtDay) || *result.ObservedAtDay < *execution.StartDay || *result.ObservedAtDay > *execution.EndDay || (result.Surface != "" && *execution.StartDay >= *execution.EndDay) {
			return fmt.Errorf("operational observation result must be unique, restricted and inside its actual execution interval")
		}
		seen[result.RequestID] = true
	}
	return nil
}

func ValidateCharacterOperationalObservationIntentV1(proposal CharacterDecisionProposal, observation CharacterObservationPacket) error {
	count := 0
	for _, task := range proposal.SelfTasks {
		count += len(task.ObservationRequests)
	}
	if count == 0 {
		return nil
	}
	if !HasCharacterOperationalAvailabilityPolicyV1(observation.Sources) || !HasCharacterSelfExperiencePolicyV2(observation.Sources) || observation.Version != CharacterObservationV2Version || count > CharacterOperationalObservationRequestLimitV1 {
		return fmt.Errorf("operational observation requests require the explicit bounded v1 policy")
	}
	views := map[string]CharacterResourceViewV2{}
	for _, view := range observation.ResourceViews {
		views[view.ResourceID] = view
	}
	known, mechanisms := observation.AllowedFactIDs(), observation.AllowedMechanismIDs()
	seen := map[string]bool{}
	for _, task := range proposal.SelfTasks {
		if err := validateCharacterOperationalRequestsV1(task); err != nil {
			return err
		}
		for _, request := range task.ObservationRequests {
			view, visible := views[request.ResourceID]
			_, public := mechanisms[request.MechanismRef]
			if request.Surface != "" && (!HasCharacterSurfaceInspectionPolicyV1(observation.Sources) || !physicalContainsRefV2(view.InspectableSurfaces, request.Surface)) {
				return fmt.Errorf("surface inspection requires the explicit policy and the exact visible inspectable facet")
			}
			if seen[request.RequestID] || !visible || view.Access == "none" || view.Perception.Kind == "unaware" || (request.Surface == "" && (view.Unit != "" || perceptionHasNumberV2(view.Perception))) || !public || !physicalContainsRefV2(proposal.MechanismRefs, request.MechanismRef) {
				return fmt.Errorf("operational observation requires a unique known accessible nonquantitative resource and invoked public mechanism")
			}
			seen[request.RequestID] = true
			for _, ref := range request.KnowledgeRefs {
				if _, ok := known[ref]; !ok {
					return fmt.Errorf("operational observation uses unavailable owner knowledge")
				}
			}
		}
	}
	return nil
}

// ValidateCharacterOperationalObservationSourcesV1 is a host-only preflight.
// A redacted resource view intentionally omits document contents and unknown
// actual quantities, so view-only intent validation cannot establish this
// capability. Call with the already verified frozen stimulus, never model data.
// Replaying an older proposal still uses its original view-only validator.
func ValidateCharacterOperationalObservationSourcesV1(proposal CharacterDecisionProposal, stimulus WorldStimulusPacket) error {
	requested := false
	for _, task := range proposal.SelfTasks {
		requested = requested || len(task.ObservationRequests) > 0
	}
	if !requested {
		return nil
	}
	if stimulus.PhysicalState == nil || proposal.GenerationID != stimulus.GenerationID || proposal.Chapter != stimulus.Chapter {
		return fmt.Errorf("operational request lacks its frozen host input")
	}
	catalog := map[string]WorldResourceBalanceV2{}
	for _, resource := range stimulus.PhysicalState.Resources {
		catalog[resource.ResourceID] = resource
	}
	holdings := map[string]CharacterResourceHoldingV2{}
	for _, actor := range stimulus.PhysicalState.Actors {
		if actor.AgentID == proposal.AgentID && actor.Character == proposal.Character {
			for _, holding := range actor.Resources {
				holdings[holding.ResourceID] = holding
			}
		}
	}
	mechanisms := map[string]CodexMechanism{}
	for _, mechanism := range stimulus.Mechanisms {
		mechanisms[mechanism.ID] = mechanism
	}
	seen := map[string]bool{}
	for _, task := range proposal.SelfTasks {
		for _, request := range task.ObservationRequests {
			resource, known := catalog[request.ResourceID]
			holding, visible := holdings[request.ResourceID]
			mechanism, public := mechanisms[request.MechanismRef]
			if request.Surface != "" && !HasCharacterSurfaceInspectionPolicyV1(stimulus.Sources) {
				return fmt.Errorf("surface inspection requires its explicit frozen policy")
			}
			if seen[request.RequestID] || !known || !operationalRequestResourceV1(resource, request.Surface) || !visible || holding.Access == "none" || holding.Perception.Kind == "unaware" || !public || CodexMechanismVisibility(mechanism) == "secret" || !physicalContainsRefV2(proposal.MechanismRefs, request.MechanismRef) {
				// Only repeat identifiers supplied by this owner. Do not reveal
				// which hidden resource property failed or any readable facts.
				return fmt.Errorf("operational request %q for resource %q is not supported by this observation API; submit an owner-chosen task without this request or use the dedicated reading/measurement protocol when applicable", request.RequestID, request.ResourceID)
			}
			seen[request.RequestID] = true
		}
	}
	return nil
}

// An already saved, view-valid request can be ineligible in the hidden world
// catalog. Permit only a genuine no-op R1 revision, not a fabricated physical
// failure or an operational result. The ordinary arbiter/prefix validators
// still authenticate all proposals, conflicts and the unchanged state.
func operationalRequestCanReviseV1(receipt WorldArbitrationReceipt, stimulus WorldStimulusPacket, before, after WorldPhysicalStateV2, owner string) bool {
	if receipt.Finalized || receipt.Round != 1 || receipt.HardContractStatus != "feasible" ||
		!physicalContainsRefV2(stimulus.Sources, CharacterActivationCyclePolicyV3) || !physicalContainsRefV2(stimulus.Sources, CharacterArbitrationRoundSourcesPolicyV1) ||
		receipt.StoryTime == nil || stimulus.StoryClock == nil || receipt.StoryTime.StartDay != stimulus.StoryClock.CurrentDay || receipt.StoryTime.EndDay != stimulus.StoryClock.CurrentDay ||
		len(receipt.ResourceSettlements)+len(receipt.ResourceDeliveries)+len(receipt.PassiveReceptions) != 0 ||
		!samePhysicalValueV2(before, after) {
		return false
	}
	for _, resolution := range receipt.Resolutions {
		if len(resolution.SelfExecutions)+len(resolution.ArtifactReadResults)+len(resolution.ArtifactSignatures) != 0 {
			return false
		}
	}
	for _, conflict := range receipt.Conflicts {
		if !conflict.Resolved && (conflict.Kind == "rule" || conflict.Kind == "resource") && physicalContainsRefV2(conflict.AffectedAgentIDs, owner) {
			return true
		}
	}
	return false
}

func CharacterOperationalObservationIDV1(observation CharacterOperationalObservationV1) string {
	observation.ID = ""
	policy := CharacterOperationalAvailabilityPolicyV1
	if observation.Surface != "" {
		policy = CharacterSurfaceInspectionPolicyV1
	}
	digest, _ := characterAgentDigest(struct {
		Policy      string                            `json:"policy"`
		Observation CharacterOperationalObservationV1 `json:"observation"`
	}{policy, observation})
	return "oper_" + strings.TrimPrefix(digest, "sha256:")
}

func validateCharacterOperationalStateV1(actor CharacterPhysicalStateV2, catalog map[string]WorldResourceBalanceV2) error {
	experiences := map[string]CharacterSelfExperienceV2{}
	for _, experience := range actor.SelfExperiences {
		experiences[experience.ID] = experience
	}
	seen := map[string]bool{}
	for _, observation := range actor.OperationalObservations {
		if observation.AgentID != actor.AgentID || !operationalBoundedV1(observation.GenerationID, 256) || observation.Chapter <= 0 || !physicalIdentityV2(observation.TaskID) || !physicalIdentityV2(observation.RequestID) || !physicalResourceIDV2(observation.ResourceID) || !operationalBoundedV1(observation.Purpose, 200) || !operationalBoundedV1(observation.MechanismRef, 128) || validateCharacterPerceivedLabelV2(observation.ResourceLabel) != nil || strings.TrimSpace(observation.ResourceLabel) == "" || !validOperationalResultForSurfaceV1(observation.Surface, observation.Result) || !physicalAmountV2(observation.ObservedAtDay) || !characterSourceDigestPatternV2.MatchString(observation.SourceProposalDigest) || seen[observation.ID] || observation.ID != CharacterOperationalObservationIDV1(observation) {
			return fmt.Errorf("owner operational observation identity or restricted result is invalid")
		}
		seen[observation.ID] = true
		if catalog != nil {
			resource, exists := catalog[observation.ResourceID]
			if !exists || !operationalRequestResourceV1(resource, observation.Surface) {
				return fmt.Errorf("operational observation cannot attest quantities or document contents")
			}
		}
		experience, exists := experiences[observation.SourceExperienceID]
		if observation.Surface != "" && (!exists || !physicalContainsRefV2(experience.ResourceIDs, observation.ResourceID) || experience.StartDay == nil || experience.EndDay == nil || *experience.StartDay >= *experience.EndDay) {
			return fmt.Errorf("surface observation lacks its owner's positive work on that exact target")
		}
		if !exists || experience.Kind != "work" || !selfExecutionActiveV2(experience.Status) || experience.Chapter != observation.Chapter || experience.TaskID != observation.TaskID || experience.SourceProposalDigest != observation.SourceProposalDigest || experience.StartDay == nil || experience.EndDay == nil || *observation.ObservedAtDay < *experience.StartDay || *observation.ObservedAtDay > *experience.EndDay {
			return fmt.Errorf("operational observation lacks its owner's actual work execution")
		}
	}
	return nil
}

func applyCharacterOperationalObservationsV1(receipt WorldArbitrationReceipt, stimulus WorldStimulusPacket, before WorldPhysicalStateV2, after *WorldPhysicalStateV2, proposals map[string]CharacterDecisionProposal, resolutions map[string]CharacterDecisionResolution) error {
	policy := HasCharacterOperationalAvailabilityPolicyV1(stimulus.Sources)
	surfacePolicy := HasCharacterSurfaceInspectionPolicyV1(stimulus.Sources)
	if policy && !HasCharacterSelfExperiencePolicyV2(stimulus.Sources) {
		return fmt.Errorf("operational observations require self-experience execution policy")
	}
	for _, proposal := range proposals {
		for _, task := range proposal.SelfTasks {
			for _, request := range task.ObservationRequests {
				if request.Surface != "" && !surfacePolicy {
					return fmt.Errorf("surface requests require the explicit frozen surface policy")
				}
			}
			if len(task.ObservationRequests) > 0 && !policy {
				return fmt.Errorf("operational requests are forbidden without the explicit policy")
			}
		}
	}
	for _, resolution := range resolutions {
		for _, execution := range resolution.SelfExecutions {
			for _, result := range execution.ObservationResults {
				if result.Surface != "" && !surfacePolicy {
					return fmt.Errorf("surface results require the explicit frozen surface policy")
				}
			}
			if len(execution.ObservationResults) > 0 && !policy {
				return fmt.Errorf("operational results are forbidden without the explicit policy")
			}
		}
	}
	oldActors := map[string]CharacterPhysicalStateV2{}
	for _, actor := range before.Actors {
		oldActors[actor.AgentID] = actor
	}
	catalog := map[string]WorldResourceBalanceV2{}
	for _, resource := range before.Resources {
		catalog[resource.ResourceID] = resource
	}
	mechanisms := map[string]CodexMechanism{}
	for _, mechanism := range stimulus.Mechanisms {
		mechanisms[mechanism.ID] = mechanism
	}
	for i := range after.Actors {
		actor := &after.Actors[i]
		old := oldActors[actor.AgentID]
		resolution, resolved := resolutions[actor.AgentID]
		if !policy || !resolved {
			if !samePhysicalValueV2(actor.OperationalObservations, old.OperationalObservations) {
				return fmt.Errorf("unexecuted owner operational observations cannot change")
			}
			continue
		}
		proposal, exists := proposals[actor.AgentID]
		if !exists {
			return fmt.Errorf("operational results lack an owner proposal")
		}
		oldHoldings := map[string]CharacterResourceHoldingV2{}
		for _, holding := range old.Resources {
			oldHoldings[holding.ResourceID] = holding
		}
		requests := map[string]CharacterOperationalObservationRequestV1{}
		owners := map[string]string{}
		for _, task := range proposal.SelfTasks {
			if err := validateCharacterOperationalRequestsV1(task); err != nil {
				return err
			}
			for _, request := range task.ObservationRequests {
				resource, known := catalog[request.ResourceID]
				holding, visible := oldHoldings[request.ResourceID]
				mechanism, public := mechanisms[request.MechanismRef]
				if owners[request.RequestID] != "" || !known || !visible || holding.Access == "none" || holding.Perception.Kind == "unaware" || !public || CodexMechanismVisibility(mechanism) == "secret" || !physicalContainsRefV2(proposal.MechanismRefs, request.MechanismRef) {
					return fmt.Errorf("operational request is not a unique known accessible qualitative resource/public mechanism")
				}
				if request.Surface != "" && !operationalRequestResourceV1(resource, request.Surface) {
					return fmt.Errorf("surface request requires the exact explicitly defined inspectable facet")
				}
				if request.Surface == "" && !operationalResourceV1(resource) && !operationalRequestCanReviseV1(receipt, stimulus, before, *after, actor.AgentID) {
					return fmt.Errorf("operational request is not a unique known accessible qualitative resource/public mechanism; owner %q request %q requires an unchanged zero-time nonfinal R1 rule/resource conflict before owner revision; no operational result or physical failure may be invented", actor.AgentID, request.RequestID)
				}
				requests[request.RequestID], owners[request.RequestID] = request, task.TaskID
			}
		}
		if len(requests) > CharacterOperationalObservationRequestLimitV1 {
			return fmt.Errorf("too many owner operational observation requests")
		}
		derived := append([]CharacterOperationalObservationV1(nil), old.OperationalObservations...)
		covered := map[string]bool{}
		activeTasks := map[string]bool{}
		for _, execution := range resolution.SelfExecutions {
			if err := validateCharacterOperationalResultsV1(execution); err != nil {
				return err
			}
			if selfExecutionActiveV2(execution.Status) {
				activeTasks[execution.TaskID] = true
			}
			for _, result := range execution.ObservationResults {
				request, exists := requests[result.RequestID]
				if !receipt.Finalized || !exists || owners[result.RequestID] != execution.TaskID || covered[result.RequestID] || result.Surface != request.Surface || !physicalContainsRefV2(resolution.MechanismRefs, request.MechanismRef) {
					return fmt.Errorf("operational result must bind one executed owner request and invoked mechanism")
				}
				covered[result.RequestID] = true
				sourceID := ""
				for _, experience := range actor.SelfExperiences {
					if experience.Chapter == receipt.Chapter && experience.TaskID == execution.TaskID && experience.SourceProposalDigest == proposal.Digest && samePhysicalNumberV2(experience.StartDay, execution.StartDay) && samePhysicalNumberV2(experience.EndDay, execution.EndDay) {
						sourceID = experience.ID
						break
					}
				}
				if sourceID == "" {
					return fmt.Errorf("operational result has no exact derived execution source")
				}
				holding := oldHoldings[request.ResourceID]
				label := holding.PerceivedLabel
				if label == "" {
					label = holding.PerceivedName
				}
				if label == "" {
					label = UnidentifiedResourceNameV2
				}
				observation := CharacterOperationalObservationV1{Surface: request.Surface, AgentID: actor.AgentID, GenerationID: receipt.GenerationID, Chapter: receipt.Chapter, TaskID: execution.TaskID, RequestID: request.RequestID, ResourceID: request.ResourceID, ResourceLabel: label, Purpose: request.Purpose, MechanismRef: request.MechanismRef, Result: result.Result, ObservedAtDay: physicalNumberCopyV2(result.ObservedAtDay), SourceProposalDigest: proposal.Digest, SourceExperienceID: sourceID}
				observation.ID = CharacterOperationalObservationIDV1(observation)
				derived = append(derived, observation)
			}
		}
		if receipt.Finalized {
			for id, taskID := range owners {
				if activeTasks[taskID] && !covered[id] {
					return fmt.Errorf("executed operational request requires an explicit available/unavailable/inconclusive result")
				}
			}
		}
		sort.Slice(derived, func(i, j int) bool { return derived[i].ID < derived[j].ID })
		if len(actor.OperationalObservations) > 0 && !samePhysicalValueV2(actor.OperationalObservations, old.OperationalObservations) && !samePhysicalValueV2(actor.OperationalObservations, derived) {
			return fmt.Errorf("arbiter cannot invent or rewrite owner operational observations")
		}
		actor.OperationalObservations = derived
	}
	return nil
}

func selectCharacterOperationalObservationsV1(history []CharacterOperationalObservationV1) ([]CharacterOperationalObservationV1, error) {
	latest := map[string]CharacterOperationalObservationV1{}
	for _, observation := range history {
		key := observation.ResourceID + "\x00" + observation.Purpose
		if observation.Surface != "" {
			key = observation.ResourceID + "\x00surface\x00" + observation.Surface
		}
		previous, exists := latest[key]
		if !exists || *observation.ObservedAtDay > *previous.ObservedAtDay || (*observation.ObservedAtDay == *previous.ObservedAtDay && observation.ID > previous.ID) {
			latest[key] = observation
		}
	}
	if len(latest) > CharacterOperationalObservationLimitV1 {
		return nil, fmt.Errorf("operational observation summary exceeds count budget; no latest observations dropped")
	}
	var out []CharacterOperationalObservationV1
	for _, observation := range latest {
		out = append(out, observation)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	raw, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	if len(raw) > CharacterOperationalObservationByteLimitV1 {
		return nil, fmt.Errorf("operational observation summary exceeds byte budget; no results silently dropped")
	}
	var clone []CharacterOperationalObservationV1
	if err := json.Unmarshal(raw, &clone); err != nil {
		return nil, err
	}
	return clone, nil
}

func BuildCharacterOperationalObservationsV1(state WorldPhysicalStateV2, agentID string) ([]CharacterOperationalObservationV1, error) {
	state, err := FinalizeWorldPhysicalStateV2(state)
	if err != nil {
		return nil, err
	}
	for _, actor := range state.Actors {
		if actor.AgentID == agentID {
			return selectCharacterOperationalObservationsV1(actor.OperationalObservations)
		}
	}
	return nil, fmt.Errorf("operational observation owner is absent")
}

func validateCharacterOperationalObservationPacketV1(observation CharacterObservationPacket) error {
	if err := validateSurfaceInspectionObservationV1(observation); err != nil {
		return err
	}
	if !HasCharacterOperationalAvailabilityPolicyV1(observation.Sources) {
		if len(observation.OperationalObservations) > 0 {
			return fmt.Errorf("owner operational observations require the explicit policy")
		}
		return nil
	}
	if observation.Version != CharacterObservationV2Version || !HasCharacterSelfExperiencePolicyV2(observation.Sources) {
		return fmt.Errorf("operational observation requires v2 owner self-experience context")
	}
	actor := CharacterPhysicalStateV2{AgentID: observation.AgentID, SelfExperiences: observation.SelfExperiences, OperationalObservations: observation.OperationalObservations}
	if err := validateCharacterOperationalStateV1(actor, nil); err != nil {
		return err
	}
	selected, err := selectCharacterOperationalObservationsV1(observation.OperationalObservations)
	if err != nil {
		return err
	}
	if len(selected) != len(observation.OperationalObservations) {
		return fmt.Errorf("operational observation view must contain only latest per-resource/purpose results")
	}
	for _, result := range observation.OperationalObservations {
		if result.Chapter > observation.Chapter || (result.Chapter == observation.Chapter && (observation.CycleContext == nil || observation.CycleContext.Index < 2 || *result.ObservedAtDay > observation.CycleContext.CurrentDay)) {
			return fmt.Errorf("operational observation cannot reveal unexecuted current/future results")
		}
	}
	return nil
}

func validateCharacterOperationalObservationBindingV1(stimulus WorldStimulusPacket, observation CharacterObservationPacket) error {
	if HasCharacterSurfaceInspectionPolicyV1(stimulus.Sources) != HasCharacterSurfaceInspectionPolicyV1(observation.Sources) {
		return fmt.Errorf("surface observation policy differs from stimulus")
	}
	policy := HasCharacterOperationalAvailabilityPolicyV1(stimulus.Sources)
	if policy != HasCharacterOperationalAvailabilityPolicyV1(observation.Sources) {
		return fmt.Errorf("operational observation policy differs from stimulus")
	}
	if err := validateCharacterOperationalObservationPacketV1(observation); err != nil {
		return err
	}
	if !policy {
		return nil
	}
	if stimulus.PhysicalState == nil {
		return fmt.Errorf("operational observation lacks physical state")
	}
	expected, err := BuildCharacterOperationalObservationsV1(*stimulus.PhysicalState, observation.AgentID)
	if err != nil {
		return err
	}
	if !samePhysicalValueV2(expected, observation.OperationalObservations) && !(len(expected) == 0 && len(observation.OperationalObservations) == 0) {
		return fmt.Errorf("operational observation differs from the exact owner's confirmed state")
	}
	return nil
}

func FormatCharacterOperationalObservationsV1(observations []CharacterOperationalObservationV1) []string {
	var out []string
	for _, observation := range observations {
		if observation.ObservedAtDay == nil {
			continue
		}
		if observation.Surface != "" {
			out = append(out, formatSurfaceInspectionV1(observation))
			continue
		}
		label := map[string]string{"available": "局部可操作", "unavailable": "局部不可操作", "inconclusive": "未能确定局部可操作性"}[observation.Result]
		out = append(out, fmt.Sprintf("本人操作性观察（%s；第%d章T+%.6g分钟；%s）：%s；仅限当时对本人任务用途的操作性，不证明持续可用、容量或整项检查合格；本人申请用途（其中数量和命题未由此观察证实）：%s", observation.ResourceLabel, observation.Chapter, *observation.ObservedAtDay*1440, observation.ID, label, observation.Purpose))
	}
	return out
}
