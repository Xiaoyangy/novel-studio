package domain

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

func ValidateCharacterResourceIntentV2(proposal CharacterDecisionProposal, observation CharacterObservationPacket) error {
	if observation.Version != CharacterObservationV2Version {
		if len(proposal.ResourceEstimates)+len(proposal.ResourceMeasurements)+len(proposal.ResourceReports) != 0 {
			return fmt.Errorf("resource intents require a v2 observation")
		}
		return nil
	}
	if proposal.Location != observation.Location {
		return fmt.Errorf("v2 proposal location must equal its observed origin, not an intended destination")
	}
	views := map[string]CharacterResourceViewV2{}
	for _, view := range observation.ResourceViews {
		views[view.ResourceID] = view
	}
	allowed := observation.AllowedFactIDs()
	require := func(id string, refs []string, quantitative bool) error {
		view, ok := views[id]
		if !ok || view.Perception.Kind == "unaware" || (quantitative && view.Unit == "") {
			return fmt.Errorf("resource intent references invisible/nonquantitative resource %q", id)
		}
		if len(normalizeV2Strings(refs)) == 0 {
			return fmt.Errorf("resource intent %q requires observation evidence", id)
		}
		for _, ref := range refs {
			if _, ok := allowed[ref]; !ok {
				return fmt.Errorf("resource intent references unavailable observation evidence %q", ref)
			}
		}
		return nil
	}
	seen := map[string]bool{}
	for _, estimate := range proposal.ResourceEstimates {
		if seen["estimate:"+estimate.ResourceID] {
			return fmt.Errorf("duplicate resource estimate %q", estimate.ResourceID)
		}
		seen["estimate:"+estimate.ResourceID] = true
		if err := require(estimate.ResourceID, estimate.EvidenceRefs, true); err != nil {
			return err
		}
		if !physicalAmountV2(estimate.EstimateMin) || !physicalAmountV2(estimate.EstimateMax) || *estimate.EstimateMin > *estimate.EstimateMax {
			return fmt.Errorf("resource estimate requires a finite nonnegative complete range")
		}
	}
	mechanisms := observation.AllowedMechanismIDs()
	for _, measurement := range proposal.ResourceMeasurements {
		if err := validateResourceMeasurementTaskV1(measurement, proposal, observation.Sources); err != nil {
			return err
		}
		view, visible := views[measurement.ResourceID]
		_, knownMechanism := mechanisms[measurement.MechanismRef]
		if seen["measurement:"+measurement.ResourceID] || !visible || view.Unit == "" || !knownMechanism || !physicalContainsRefV2(proposal.MechanismRefs, measurement.MechanismRef) {
			return fmt.Errorf("resource measurement requires one visible resource and an explicitly invoked public mechanism")
		}
		seen["measurement:"+measurement.ResourceID] = true
	}
	for _, report := range proposal.ResourceReports {
		key := "report:" + report.ResourceID + ":" + report.ToCharacter
		if seen[key] || strings.TrimSpace(report.ToCharacter) == "" || (report.Amount != nil && !physicalAmountV2(report.Amount)) || (report.Amount == nil && strings.TrimSpace(report.PerceivedName) == "" && strings.TrimSpace(report.PerceivedUnit) == "") {
			return fmt.Errorf("resource report requires one finite amount per resource/recipient")
		}
		seen[key] = true
		if err := require(report.ResourceID, report.EvidenceRefs, report.Amount != nil); err != nil {
			return err
		}
		if report.PerceivedName != "" && report.PerceivedName != views[report.ResourceID].Name {
			return fmt.Errorf("resource report cannot invent an unseen resource name")
		}
		if report.PerceivedUnit != "" && report.PerceivedUnit != views[report.ResourceID].Unit {
			return fmt.Errorf("resource report cannot invent an unknown unit")
		}
		if report.Amount != nil && report.PerceivedUnit != views[report.ResourceID].Unit {
			return fmt.Errorf("numeric resource report must explicitly preserve its perceived unit")
		}
	}
	return nil
}

// Apply validates a complete simultaneous world result. Shared resources are
// settled once in the catalog; actor snapshots only change access/perception.
// Proposals are required whenever new numeric knowledge needs an actor-owned
// estimate, explicit measurement request, or explicit incoming report.
func ApplyArbitrationPhysicalStateV2(receipt WorldArbitrationReceipt, stimulus WorldStimulusPacket, proposals ...CharacterDecisionProposal) (WorldPhysicalStateV2, error) {
	return applyArbitrationPhysicalStateWithArtifactSourcesV1(receipt, stimulus, proposals, nil)
}

func applyArbitrationPhysicalStateWithArtifactSourcesV1(receipt WorldArbitrationReceipt, stimulus WorldStimulusPacket, proposals []CharacterDecisionProposal, artifactSources map[string]string) (WorldPhysicalStateV2, error) {
	if err := validateWorkArtifactStimulusV1(stimulus); err != nil {
		return WorldPhysicalStateV2{}, err
	}
	if receipt.Version != WorldArbitrationReceiptV2Version || stimulus.Version != WorldStimulusPacketV2Version || stimulus.PhysicalState == nil {
		return WorldPhysicalStateV2{}, fmt.Errorf("physical arbitration requires explicit v2 receipt and stimulus physical state")
	}
	if receipt.GenerationID != stimulus.GenerationID || receipt.Chapter != stimulus.Chapter || receipt.StimulusDigest != stimulus.Digest {
		return WorldPhysicalStateV2{}, fmt.Errorf("physical arbitration is not bound to its stimulus")
	}
	if digest, err := ComputeWorldStimulusPacketDigest(stimulus); err != nil || digest != stimulus.Digest {
		return WorldPhysicalStateV2{}, fmt.Errorf("physical arbitration stimulus digest mismatch")
	}
	before, err := FinalizeWorldPhysicalStateV2(*stimulus.PhysicalState)
	if err != nil {
		return before, err
	}
	after, err := FinalizeWorldPhysicalStateV2(before)
	if err != nil {
		return after, err
	}
	resourceIndex, actorIndex := map[string]int{}, map[string]int{}
	for i, resource := range after.Resources {
		resourceIndex[resource.ResourceID] = i
	}
	for i, actor := range after.Actors {
		actorIndex[actor.AgentID] = i
	}
	proposalByAgent := map[string]CharacterDecisionProposal{}
	for _, proposal := range proposals {
		if proposal.GenerationID != receipt.GenerationID || proposal.Chapter != receipt.Chapter || !physicalContainsRefV2(receipt.ProposalDigests, proposal.Digest) || proposalByAgent[proposal.AgentID].AgentID != "" {
			return after, fmt.Errorf("physical arbitration received an unbound or duplicate proposal")
		}
		proposalByAgent[proposal.AgentID] = proposal
	}
	catalogRefs := newArbitrationReferenceCatalogV2(stimulus, before, receipt.ProposalDigests, proposals)
	allowedRefs := catalogRefs.evidence
	requireRefs := func(refs []string) error {
		if len(normalizeV2Strings(refs)) == 0 {
			return fmt.Errorf("physical state change requires bound evidence refs")
		}
		for _, ref := range refs {
			if ref == "" || !allowedRefs[ref] {
				return fmt.Errorf("physical state change has unbound evidence %q", ref)
			}
		}
		return nil
	}
	// Settlement schema explicitly accepts actual world-mechanism references.
	// Keep them separate from actor evidence: a world rule can explain a cost,
	// but is not an owner's measurement, document reading or delivery receipt.
	usedMechanisms := catalogRefs.usedSettlementMechanisms(receipt, proposalByAgent)
	requireSettlementRefs := func(refs []string) error {
		if len(normalizeV2Strings(refs)) == 0 {
			return fmt.Errorf("resource settlement requires bound evidence refs")
		}
		for _, ref := range refs {
			if ref == "" || (!allowedRefs[ref] && !usedMechanisms[ref]) {
				return fmt.Errorf("unbound settlement evidence %q; use a bound source/proposal or a world mechanism actually named in this arbitration's mechanism_refs", ref)
			}
		}
		return nil
	}
	if err := validateIncomingMaterialReadReceiptFieldsV1(receipt, stimulus); err != nil {
		return after, err
	}
	if err := applyResourceObservationSettlementsV1(receipt, stimulus, before, &after, resourceIndex, requireSettlementRefs, proposals); err != nil {
		return after, err
	}
	artifactTransition, err := prepareCharacterWorkArtifactsV1(receipt, stimulus, before, &after, proposalByAgent, artifactSources)
	if err != nil {
		return after, err
	}
	catalog := map[string]WorldResourceBalanceV2{}
	for _, resource := range after.Resources {
		catalog[resource.ResourceID] = resource
	}
	resolved := map[string]CharacterDecisionResolution{}
	for _, resolution := range receipt.Resolutions {
		index, exists := actorIndex[resolution.AgentID]
		if !exists || resolved[resolution.AgentID].AgentID != "" || resolution.PostState == nil {
			return after, fmt.Errorf("v2 arbitration requires exactly one explicit post_state per resolved host actor")
		}
		post := *resolution.PostState
		if post.SelfChronologyBaseline != nil && !samePhysicalValueV2(post.SelfChronologyBaseline, before.Actors[index].SelfChronologyBaseline) {
			return after, fmt.Errorf("arbiter cannot change the host self chronology baseline")
		}
		if HasCharacterSelfExperiencePolicyV2(stimulus.Sources) {
			previousActor := before.Actors[index]
			post.SelfChronologyBaseline = previousActor.SelfChronologyBaseline
			if len(post.SelfExperiences) == 0 {
				post.SelfExperiences = append([]CharacterSelfExperienceV2(nil), previousActor.SelfExperiences...)
			}
			if len(post.TaskProgress) == 0 {
				post.TaskProgress = append([]CharacterTaskProgressV2(nil), previousActor.TaskProgress...)
			}
			// Preserve an explicitly empty resource list; nil is a missing
			// required field and must remain distinguishable from owning none.
			if post.Resources != nil {
				post.Resources = append([]CharacterResourceHoldingV2{}, post.Resources...)
			}
			for j := range post.Resources {
				for _, previous := range previousActor.Resources {
					if previous.ResourceID != post.Resources[j].ResourceID {
						continue
					}
					if post.Resources[j].KnownPlacement == nil {
						post.Resources[j].KnownPlacement = previous.KnownPlacement
					}
					if post.Resources[j].PerceivedLabel == "" {
						post.Resources[j].PerceivedLabel = previous.PerceivedLabel
					}
				}
			}
		}
		if post.AgentID != resolution.AgentID || post.Character != resolution.Character || post.Character != before.Actors[index].Character {
			return after, fmt.Errorf("post_state changed actor identity")
		}
		if err := artifactTransition.bindOwnerPost(receipt, after, before.Actors[index], &post); err != nil {
			return after, err
		}
		if err := validateCharacterPhysicalStateV2(post, catalog); err != nil {
			return after, err
		}
		single, err := FinalizeWorldPhysicalStateV2(WorldPhysicalStateV2{Version: WorldPhysicalStateV2Version, Resources: after.Resources, Actors: []CharacterPhysicalStateV2{post}})
		if err != nil {
			return after, err
		}
		post = single.Actors[0]
		for _, holding := range post.Resources {
			if holding.Perception.AsOfChapter > receipt.Chapter {
				return after, fmt.Errorf("post_state perception comes from a future chapter")
			}
		}
		after.Actors[index] = post
		copy := post
		resolution.PostState = &copy
		resolved[resolution.AgentID] = resolution
	}
	if err := ValidateWorldPhysicalStateV2(after); err != nil {
		return after, err
	}
	if err := validateResourceDeliveriesV2(receipt, before, after, proposalByAgent, resolved, requireRefs); err != nil {
		return after, err
	}
	if err := validateIncomingMaterialReadTransitionsV1(receipt, stimulus, before, after, proposalByAgent); err != nil {
		return after, err
	}
	if err := validateArtifactDeliveriesV1(receipt, stimulus, before, after, proposalByAgent); err != nil {
		return after, err
	}
	// Report independent perception defects together, in a stable order. A
	// model repair should not require another full paid request merely to see
	// the next resource's error. This changes diagnostics only: no invalid
	// candidate reaches the fact or execution transitions below.
	agentIDs := make([]string, 0, len(resolved))
	for agentID := range resolved {
		agentIDs = append(agentIDs, agentID)
	}
	sort.Strings(agentIDs)
	var perceptionErrors []error
	perceptionErrorCount := 0
	addPerceptionError := func(err error) {
		perceptionErrorCount++
		if len(perceptionErrors) < 8 {
			perceptionErrors = append(perceptionErrors, err)
		}
	}
	for _, agentID := range agentIDs {
		resolution := resolved[agentID]
		old := before.Actors[actorIndex[agentID]]
		oldHoldings := map[string]CharacterResourceHoldingV2{}
		for _, holding := range old.Resources {
			oldHoldings[holding.ResourceID] = holding
		}
		holdings := append([]CharacterResourceHoldingV2(nil), resolution.PostState.Resources...)
		sort.Slice(holdings, func(i, j int) bool { return holdings[i].ResourceID < holdings[j].ResourceID })
		for _, holding := range holdings {
			if artifactTransition.creationGain(agentID, holding.ResourceID) {
				continue
			}
			previous, existed := oldHoldings[holding.ResourceID]
			if (holding.Access == "exclusive" || holding.Access == "shared") && (!existed || previous.Access == "none" || (previous.Access == "shared" && holding.Access == "exclusive")) && !hasResourceDeliveryV2(receipt, holding.ResourceID, "", agentID, "", "", holding.Access) {
				return after, fmt.Errorf("actor %s resource %s access gain lacks an actual delivery", agentID, holding.ResourceID)
			}
			if (!existed || previous.Perception.Kind == "unaware") && holding.Perception.Kind != "unaware" && !hasResourceDeliveryV2(receipt, holding.ResourceID, "", agentID, "", "", "") {
				return after, fmt.Errorf("actor %s learned a resource without actual discovery/delivery", agentID)
			}
			if holding.PerceivedUnit != "" && (!existed || previous.Perception.Kind == "unaware" || holding.PerceivedUnit != previous.PerceivedUnit) && !authorizeResourceUnitV2(receipt, holding, resolution, proposalByAgent, resolved) {
				return after, fmt.Errorf("actor %s resource %s unit lacks an independently received report", agentID, holding.ResourceID)
			}
			if holding.Perception.Kind != "unaware" && holding.PerceivedName != UnidentifiedResourceNameV2 && (!existed || previous.Perception.Kind == "unaware" || holding.PerceivedName != previous.PerceivedName) && !authorizeResourceNameV2(receipt, holding, resolution, proposalByAgent, resolved) {
				return after, fmt.Errorf("actor %s resource %s name lacks an independently received report", agentID, holding.ResourceID)
			}
			if !existed || holding.Access != previous.Access {
				if err := requireRefs(holding.EvidenceRefs); err != nil {
					return after, fmt.Errorf("post_state[%s].resources[%s].evidence_refs: %w", agentID, holding.ResourceID, err)
				}
			}
			if existed && sameCharacterResourcePerceptionV2(agentID, previous.Perception, holding.Perception) {
				continue
			}
			if holding.Perception.Kind == "unknown" || holding.Perception.Kind == "unaware" {
				continue
			}
			if err := requireRefs(holding.Perception.EvidenceRefs); err != nil {
				addPerceptionError(fmt.Errorf("post_state[%s].resources[%s].perception.evidence_refs (requires owner estimate/measurement or received-report evidence, not world-mechanism IDs): %w", agentID, holding.ResourceID, err))
				continue
			}
			if !authorizeResourcePerceptionV2(receipt, stimulus, before, after, resolution, previous, existed, holding, proposalByAgent, resolved) {
				addPerceptionError(fmt.Errorf("actor %s resource %s numeric perception lacks an independent estimate, measurement or received report", agentID, holding.ResourceID))
			}
		}
	}
	if perceptionErrorCount > 0 {
		if omitted := perceptionErrorCount - len(perceptionErrors); omitted > 0 {
			perceptionErrors = append(perceptionErrors, fmt.Errorf("%d additional resource perception errors omitted", omitted))
		}
		guidance := "Resource perception repair: world consumption does not update an actor's knowledge. For unchanged perceptions, omit perception from resource_updates (or omit resource_updates entirely); preserve the old observation age and evidence. A new measurement requires the owner's original measurement request and proposal digest as evidence, not a mechanism ID. Only submit a new estimate or reported value when its original intent and actual evidence authorize it; do not invent a measurement to satisfy validation"
		if HasCharacterResourceObservationTimePolicyV1(stimulus.Sources) {
			guidance += ". Timed measurements also require the original measurement task_id, an actual work execution, and observed_at_day equal to that task's end_day. Validate the amount at that instant against settlement intervals; a later cycle-end balance is not an earlier reading"
		}
		return after, fmt.Errorf("%w\n%s", errors.Join(perceptionErrors...), guidance)
	}
	if err := validateReceivedFactsTransitionV2(receipt, before, after, proposalByAgent, resolved); err != nil {
		return after, err
	}
	if err := applyCharacterSelfExecutionsV2(receipt, stimulus, before, &after, proposalByAgent, resolved); err != nil {
		return after, err
	}
	if err := applyCharacterOperationalObservationsV1(receipt, stimulus, before, &after, proposalByAgent, resolved); err != nil {
		return after, err
	}
	if err := applyCharacterPassiveReceptionsV2(receipt, stimulus, before, &after, proposalByAgent, resolved); err != nil {
		return after, err
	}
	if err := artifactTransition.finish(receipt, stimulus, before, &after, proposalByAgent, resolved); err != nil {
		return after, err
	}
	return FinalizeWorldPhysicalStateV2(after)
}

func authorizeResourcePerceptionV2(receipt WorldArbitrationReceipt, stimulus WorldStimulusPacket, before, after WorldPhysicalStateV2, resolution CharacterDecisionResolution, previous CharacterResourceHoldingV2, existed bool, holding CharacterResourceHoldingV2, proposals map[string]CharacterDecisionProposal, resolutions map[string]CharacterDecisionResolution) bool {
	p := holding.Perception
	proposal, hasProposal := proposals[resolution.AgentID]
	if p.Kind == "estimated" {
		if hasProposal && existed && holding.PerceivedUnit == previous.PerceivedUnit && p.Amount == nil && p.AsOfChapter == receipt.Chapter && physicalContainsRefV2(p.EvidenceRefs, proposal.Digest) {
			for _, estimate := range proposal.ResourceEstimates {
				if estimate.ResourceID == holding.ResourceID && samePhysicalNumberV2(estimate.EstimateMin, p.EstimateMin) && samePhysicalNumberV2(estimate.EstimateMax, p.EstimateMax) && characterContainsAllSourceRefsV2(resolution.AgentID, p.EvidenceRefs, estimate.EvidenceRefs) {
					return true
				}
			}
		}
		// Downgrading one's own remembered/reported number to an estimate does
		// not reveal any new number and must retain its original observation age.
		if existed && holding.PerceivedUnit == previous.PerceivedUnit && (previous.Perception.Kind == "last_observed" || previous.Perception.Kind == "reported") && p.AsOfChapter == previous.Perception.AsOfChapter && characterContainsAllSourceRefsV2(resolution.AgentID, p.EvidenceRefs, previous.Perception.EvidenceRefs) {
			point := previous.Perception.Amount
			if point != nil && (p.Amount == nil || samePhysicalNumberV2(p.Amount, point)) && ((p.EstimateMin == nil && p.EstimateMax == nil && p.Amount != nil) || (samePhysicalNumberV2(p.EstimateMin, point) && samePhysicalNumberV2(p.EstimateMax, point))) {
				return true
			}
		}
	}
	if p.Kind == "last_observed" && hasProposal && resolution.Outcome != "blocked" && resolution.CompletionState != "blocked" && p.AsOfChapter == receipt.Chapter && physicalContainsRefV2(p.EvidenceRefs, proposal.Digest) {
		if HasCharacterResourceObservationTimePolicyV1(stimulus.Sources) {
			return authorizeResourceObservationAtTimeV1(receipt, stimulus, before, resolution, previous, existed, holding, proposal)
		}
		for _, measurement := range proposal.ResourceMeasurements {
			if measurement.ResourceID != holding.ResourceID || !physicalContainsRefV2(proposal.MechanismRefs, measurement.MechanismRef) || !physicalContainsRefV2(resolution.MechanismRefs, measurement.MechanismRef) {
				continue
			}
			for _, resource := range after.Resources {
				if resource.ResourceID == holding.ResourceID && existed && previous.PerceivedUnit == resource.Unit && holding.PerceivedUnit == previous.PerceivedUnit && resource.ActualAmount != nil && samePhysicalNumberV2(p.Amount, resource.ActualAmount) {
					return true
				}
			}
		}
	}
	if p.Kind == "reported" && p.AsOfChapter == receipt.Chapter {
		for agentID, sender := range proposals {
			delivery, acted := resolutions[agentID]
			if !acted || delivery.Outcome == "blocked" || delivery.CompletionState == "blocked" || !physicalContainsRefV2(p.EvidenceRefs, sender.Digest) || !hasResourceDeliveryV2(receipt, holding.ResourceID, agentID, resolution.AgentID, sender.Digest, "amount", "") {
				continue
			}
			for _, report := range sender.ResourceReports {
				if report.ResourceID == holding.ResourceID && report.ToCharacter == resolution.Character && report.PerceivedUnit == holding.PerceivedUnit && samePhysicalNumberV2(report.Amount, p.Amount) {
					return true
				}
			}
		}
	}
	// A giver's own old observation is not the recipient's observation. Only
	// the actually delivered report above may introduce that number, reported.
	return false
}

func authorizeResourceNameV2(receipt WorldArbitrationReceipt, holding CharacterResourceHoldingV2, receiver CharacterDecisionResolution, proposals map[string]CharacterDecisionProposal, resolutions map[string]CharacterDecisionResolution) bool {
	for agentID, sender := range proposals {
		delivery, exists := resolutions[agentID]
		if !exists || delivery.Outcome == "blocked" || delivery.CompletionState == "blocked" || !physicalContainsRefV2(holding.EvidenceRefs, sender.Digest) || !hasResourceDeliveryV2(receipt, holding.ResourceID, agentID, receiver.AgentID, sender.Digest, "name", "") {
			continue
		}
		for _, report := range sender.ResourceReports {
			if report.ResourceID == holding.ResourceID && report.ToCharacter == receiver.Character && report.PerceivedName == holding.PerceivedName {
				return true
			}
		}
	}
	return false
}

func authorizeResourceUnitV2(receipt WorldArbitrationReceipt, holding CharacterResourceHoldingV2, receiver CharacterDecisionResolution, proposals map[string]CharacterDecisionProposal, resolutions map[string]CharacterDecisionResolution) bool {
	for agentID, sender := range proposals {
		delivery, exists := resolutions[agentID]
		if !exists || delivery.Outcome == "blocked" || delivery.CompletionState == "blocked" || !physicalContainsRefV2(holding.EvidenceRefs, sender.Digest) || !hasResourceDeliveryV2(receipt, holding.ResourceID, agentID, receiver.AgentID, sender.Digest, "unit", "") {
			continue
		}
		for _, report := range sender.ResourceReports {
			if report.ResourceID == holding.ResourceID && report.ToCharacter == receiver.Character && report.PerceivedUnit == holding.PerceivedUnit {
				return true
			}
		}
	}
	return false
}

func physicalContainsRefV2(refs []string, wanted string) bool {
	if wanted == "" {
		return false
	}
	for _, ref := range refs {
		if ref == wanted {
			return true
		}
	}
	return false
}
func physicalContainsAllRefsV2(refs, wanted []string) bool {
	for _, ref := range wanted {
		if !physicalContainsRefV2(refs, ref) {
			return false
		}
	}
	return len(wanted) > 0
}
func physicalAmountsCloseV2(a, b float64) bool {
	return !math.IsNaN(a) && !math.IsInf(a, 0) && math.Abs(a-b) <= 1e-9*math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
}
