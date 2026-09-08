package domain

import (
	"fmt"
	"math"
	"sort"
)

type workArtifactTransitionV1 struct {
	created          map[string]string
	authored         map[string][]CharacterArtifactKnowledgeV1
	claimedKnowledge map[string][]CharacterArtifactKnowledgeV1
}

func artifactStateActorV1(state WorldPhysicalStateV2, id string) (CharacterPhysicalStateV2, bool) {
	for _, actor := range state.Actors {
		if actor.AgentID == id {
			return actor, true
		}
	}
	return CharacterPhysicalStateV2{}, false
}

func artifactStateResourceV1(state WorldPhysicalStateV2, id string) (WorldResourceBalanceV2, bool) {
	for _, resource := range state.Resources {
		if resource.ResourceID == id {
			return resource, true
		}
	}
	return WorldResourceBalanceV2{}, false
}

func artifactHoldingV1(actor CharacterPhysicalStateV2, id string) (CharacterResourceHoldingV2, bool) {
	for _, holding := range actor.Resources {
		if holding.ResourceID == id {
			return holding, true
		}
	}
	return CharacterResourceHoldingV2{}, false
}

func artifactCreatedByProposalV1(before, after WorldPhysicalStateV2, id string, proposal CharacterDecisionProposal) bool {
	if _, exists := artifactStateResourceV1(before, id); exists {
		return false
	}
	resource, exists := artifactStateResourceV1(after, id)
	return exists && resource.Artifact != nil && resource.Artifact.CreatorAgentID == proposal.AgentID && resource.Artifact.OriginProposalDigest == proposal.Digest
}

func artifactCustodianAtOriginV1(state WorldPhysicalStateV2, id, owner string) bool {
	resource, exists := artifactStateResourceV1(state, id)
	actor, known := artifactStateActorV1(state, owner)
	return exists && known && resource.Artifact != nil && resource.Artifact.Placement.CustodianAgentID == owner && resource.Artifact.Placement.Location == actor.Location
}

func artifactSourceLineageV1(state WorldPhysicalStateV2, owner string, refs []string) ([]string, error) {
	actor, ok := artifactStateActorV1(state, owner)
	if !ok {
		return nil, fmt.Errorf("artifact lineage owner is absent")
	}
	lineages := map[string][]string{}
	for _, knowledge := range actor.ArtifactKnowledge {
		for _, claim := range knowledge.Claims {
			lineages[CharacterArtifactClaimFactIDV1(owner, knowledge.ResourceID, knowledge.VersionDigest, claim.ClaimID)] = claim.LineageRoots
		}
	}
	for _, fact := range actor.ReceivedFacts {
		if fact.SourceType == "resource_read" {
			resource, _ := artifactStateResourceV1(state, fact.ResourceID)
			if resource.Artifact != nil {
				return nil, fmt.Errorf("artifact text cannot enter lineage through an unversioned document read")
			}
			lineages[fact.ID] = []string{artifactLineageRootV1("resource", fact.ResourceID)}
		} else if fact.SourceType == "communication" {
			lineages[fact.ID] = []string{artifactLineageRootV1("speaker", fact.FromAgentID)}
		}
	}
	var roots []string
	for _, ref := range refs {
		if inherited, ok := lineages[ref]; ok {
			roots = append(roots, inherited...)
		} else {
			roots = append(roots, artifactLineageRootV1("knowledge", ref))
		}
	}
	return normalizeV2Strings(roots), nil
}

func validateArtifactClaimSourceKindsV1(state WorldPhysicalStateV2, owner string, claim CharacterWorkArtifactClaimV1) error {
	if claim.EpistemicKind == "self_statement" || claim.EpistemicKind == "pending" {
		return nil
	}
	actor, ok := artifactStateActorV1(state, owner)
	if !ok {
		return fmt.Errorf("artifact source owner is absent")
	}
	for _, ref := range claim.SourceRefs {
		matched := false
		for _, knowledge := range actor.ArtifactKnowledge {
			for _, source := range knowledge.Claims {
				if CharacterArtifactClaimFactIDV1(owner, knowledge.ResourceID, knowledge.VersionDigest, source.ClaimID) == ref {
					if source.EpistemicKind != claim.EpistemicKind || source.AttributedTo != claim.AttributedTo {
						return fmt.Errorf("derived artifact claim cannot upgrade or reattribute its source statement")
					}
					matched = true
				}
			}
		}
		for _, source := range actor.ReceivedFacts {
			if source.ID == ref {
				if source.SourceType == "resource_read" && claim.EpistemicKind == "document_statement" {
					matched = true
				}
				if source.SourceType == "communication" && claim.EpistemicKind == "attributed_statement" {
					speaker, _ := artifactStateActorV1(state, source.FromAgentID)
					matched = speaker.Character == claim.AttributedTo
				}
			}
		}
		if !matched {
			return fmt.Errorf("artifact document/attributed claim requires a direct actually known source of the same category and speaker")
		}
	}
	return nil
}

func artifactReceiptTimeV1(receipt WorldArbitrationReceipt, at float64) bool {
	return receipt.StoryTime != nil && finiteStoryDay(at) && at >= receipt.StoryTime.StartDay-1e-12 && at <= receipt.StoryTime.EndDay+1e-12
}

func artifactActualWorkTimeV1(receipt WorldArbitrationReceipt, resolution CharacterDecisionResolution, proposal CharacterDecisionProposal, at float64) bool {
	if !receipt.Finalized || resolution.Outcome == "blocked" || resolution.CompletionState == "blocked" || !artifactReceiptTimeV1(receipt, at) {
		return false
	}
	for _, execution := range resolution.SelfExecutions {
		if !selfExecutionActiveV2(execution.Status) || execution.StartDay == nil || execution.EndDay == nil || at < *execution.StartDay-1e-12 || at > *execution.EndDay+1e-12 {
			continue
		}
		for _, task := range proposal.SelfTasks {
			if task.TaskID == execution.TaskID && task.Kind == "work" {
				return true
			}
		}
	}
	return false
}

func newArtifactKnowledgeV1(receipt WorldArbitrationReceipt, stimulus WorldStimulusPacket, proposal CharacterDecisionProposal, artifact CharacterWorkArtifactV1, kind string, claims []CharacterWorkArtifactClaimV1, at float64, signatures []CharacterArtifactSignatureReceiptV1) (CharacterArtifactKnowledgeV1, error) {
	knowledge := CharacterArtifactKnowledgeV1{ResourceID: artifact.ResourceID, VersionDigest: artifact.VersionDigest, Kind: kind, Claims: continuationCloneV1(claims), AtDay: at, GenerationID: receipt.GenerationID, Chapter: receipt.Chapter, Cycle: stimulus.SelfEvaluationContext.Cycle, SourceProposalDigest: proposal.Digest, Status: artifact.Status, Revision: artifact.Revision, Placement: artifact.Placement, Signatures: continuationCloneV1(signatures)}
	var err error
	knowledge.KnowledgeDigest, err = ComputeCharacterArtifactKnowledgeDigestV1(proposal.AgentID, knowledge)
	return knowledge, err
}

func prepareCharacterWorkArtifactsV1(receipt WorldArbitrationReceipt, stimulus WorldStimulusPacket, before WorldPhysicalStateV2, after *WorldPhysicalStateV2, proposals map[string]CharacterDecisionProposal, continuationSources map[string]string) (*workArtifactTransitionV1, error) {
	present := false
	for _, p := range proposals {
		present = present || workArtifactIntentPresentV1(p)
	}
	for _, r := range receipt.Resolutions {
		present = present || len(r.ArtifactReadResults)+len(r.ArtifactSignatures) > 0
		for _, execution := range r.SelfExecutions {
			present = present || len(execution.OutputResults) > 0
		}
	}
	if !HasCharacterWorkArtifactPolicyV1(stimulus.Sources) {
		if present {
			return nil, fmt.Errorf("artifact outcomes require explicit v3 work-artifact policy")
		}
		return nil, nil
	}
	if err := validateWorkArtifactStimulusV1(stimulus); err != nil {
		return nil, err
	}
	transition := &workArtifactTransitionV1{created: map[string]string{}, authored: map[string][]CharacterArtifactKnowledgeV1{}, claimedKnowledge: map[string][]CharacterArtifactKnowledgeV1{}}
	materials := map[string]float64{}
	changed := map[string]bool{}
	count := 0
	for _, resource := range before.Resources {
		if resource.Artifact != nil {
			count++
		}
	}
	for _, resolution := range receipt.Resolutions {
		proposal, ok := proposals[resolution.AgentID]
		if !ok {
			return nil, fmt.Errorf("artifact result lacks its bound owner proposal")
		}
		if len(resolution.ArtifactReadResults)+len(resolution.ArtifactSignatures) > 0 && !receipt.Finalized {
			return nil, fmt.Errorf("provisional arbitration cannot publish artifact reading/signature")
		}
		owner, ok := artifactStateActorV1(before, proposal.AgentID)
		if !ok {
			return nil, fmt.Errorf("artifact owner is absent")
		}
		tasks := map[string]CharacterSelfTaskV2{}
		for _, task := range proposal.SelfTasks {
			tasks[task.TaskID] = task
		}
		activeOutputs, reportedOutputs := map[string]bool{}, map[string]bool{}
		for _, execution := range resolution.SelfExecutions {
			task, ok := tasks[execution.TaskID]
			if !ok && len(execution.OutputResults) > 0 {
				return nil, fmt.Errorf("artifact result is not part of its original task")
			}
			requests := map[string]CharacterWorkOutputRequestV1{}
			for _, request := range task.OutputRequests {
				if selfExecutionActiveV2(execution.Status) {
					activeOutputs[request.OutputKey] = true
				}
				if requests[request.OutputKey].OutputKey != "" {
					return nil, fmt.Errorf("duplicate artifact request")
				}
				requests[request.OutputKey] = request
			}
			for _, result := range execution.OutputResults {
				if reportedOutputs[result.OutputKey] {
					return nil, fmt.Errorf("artifact output result is duplicated across execution segments")
				}
				reportedOutputs[result.OutputKey] = true
				request, ok := requests[result.OutputKey]
				if !ok {
					return nil, fmt.Errorf("artifact result lacks the owner's exact declared output")
				}
				delete(requests, result.OutputKey)
				if result.Status == "blocked" {
					if len(result.ClaimIDs) > 0 || result.Complete || !artifactReceiptTimeV1(receipt, result.AtDay) {
						return nil, fmt.Errorf("blocked output cannot write claims or completion")
					}
					continue
				}
				if task.Kind != "work" || !selfExecutionActiveV2(execution.Status) || !artifactActualWorkTimeV1(receipt, resolution, proposal, result.AtDay) || execution.EndDay == nil || !physicalAmountsCloseV2(result.AtDay, *execution.EndDay) || len(result.ClaimIDs) == 0 || len(normalizeV2Strings(result.ClaimIDs)) != len(result.ClaimIDs) {
					return nil, fmt.Errorf("artifact creation/update requires explicit actual work at the closed endpoint and nonduplicate written claims")
				}
				if resolution.PostState == nil || owner.Location != resolution.PostState.Location {
					return nil, fmt.Errorf("artifact writing requires a stationary actual owner endpoint; creation cannot invent an intermediate location")
				}
				var artifact CharacterWorkArtifactV1
				id := request.ResourceID
				if continuationSources[proposal.AgentID] == proposal.Digest {
					candidateID := id
					if candidateID == "" {
						candidateID = CharacterWorkArtifactResourceIDV1(receipt.GenerationID, proposal.AgentID, task.TaskID, request.OutputKey)
					}
					resource, exists := artifactStateResourceV1(before, candidateID)
					if exists && resource.Artifact != nil {
						previous := resource.Artifact
						ownVersion := false
						for _, knowledge := range owner.ArtifactKnowledge {
							if knowledge.ResourceID == candidateID && knowledge.VersionDigest == previous.VersionDigest && knowledge.Kind == "authored" && knowledge.SourceProposalDigest == proposal.Digest {
								ownVersion = true
							}
						}
						currentSigned := false
						for _, signature := range previous.Signatures {
							currentSigned = currentSigned || signature.VersionDigest == previous.VersionDigest
						}
						if (id == "" && (previous.OriginProposalDigest != proposal.Digest || previous.OriginTaskID != task.TaskID || previous.OutputKey != request.OutputKey)) || previous.CreatorAgentID != proposal.AgentID || !ownVersion || currentSigned {
							return nil, fmt.Errorf("continued output cannot follow an externally changed/signed artifact")
						}
						id = candidateID
						request.ExpectedVersionDigest = previous.VersionDigest
						request.MaterialInputs = nil
					}
				}
				if id == "" {
					id = CharacterWorkArtifactResourceIDV1(receipt.GenerationID, proposal.AgentID, task.TaskID, request.OutputKey)
					if _, exists := artifactStateResourceV1(*after, id); exists || result.Status != "created" || request.ExpectedVersionDigest != "" || len(request.MaterialInputs) == 0 {
						return nil, fmt.Errorf("artifact creation must allocate one new stable object, never recreate or reuse stock")
					}
					count++
					if count > CharacterWorkArtifactLimitV1 {
						return nil, fmt.Errorf("artifact directory exceeds its bounded protocol limit")
					}
					artifact = CharacterWorkArtifactV1{Version: CharacterWorkArtifactPolicyV1, ResourceID: id, CreatorAgentID: proposal.AgentID, OriginGenerationID: receipt.GenerationID, OriginChapter: receipt.Chapter, OriginCycle: stimulus.SelfEvaluationContext.Cycle, OriginTaskID: task.TaskID, OutputKey: request.OutputKey, OriginProposalDigest: proposal.Digest, Revision: 1, Materials: continuationCloneV1(request.MaterialInputs), CreatedAtDay: result.AtDay, Placement: CharacterArtifactPlacementV1{Kind: "with_actor", Location: resolution.PostState.Location, CustodianAgentID: proposal.AgentID}}
					for _, material := range request.MaterialInputs {
						stock, exists := artifactStateResourceV1(before, material.ResourceID)
						holding, known := artifactHoldingV1(owner, material.ResourceID)
						if !exists || stock.Artifact != nil || stock.ActualAmount == nil || stock.Unit == "" || !known || holding.Access == "none" || holding.Perception.Kind == "unaware" || math.IsNaN(material.Amount) || math.IsInf(material.Amount, 0) || material.Amount <= 0 || !physicalContainsRefV2(task.ResourceIDs, material.ResourceID) {
							return nil, fmt.Errorf("artifact creation lacks accessible finite material stock")
						}
						materials[material.ResourceID] += material.Amount
					}
					transition.created[id] = proposal.AgentID
				} else {
					resource, exists := artifactStateResourceV1(before, id)
					if !exists || resource.Artifact == nil || result.Status != "updated" || resource.Artifact.VersionDigest != request.ExpectedVersionDigest || resource.Artifact.CreatorAgentID != proposal.AgentID || resource.Artifact.Placement.CustodianAgentID != proposal.AgentID || resource.Artifact.Placement.Location != owner.Location || owner.Location != resolution.PostState.Location || resource.Name != request.Label || len(request.MaterialInputs) > 0 {
						return nil, fmt.Errorf("artifact update lacks creator/location/version CAS or repeats material consumption")
					}
					artifact = continuationCloneV1(*resource.Artifact)
					artifact.PreviousVersionDigest = artifact.VersionDigest
					artifact.Revision++
					if artifact.Revision > CharacterWorkArtifactRevisionLimitV1 {
						return nil, fmt.Errorf("artifact revision bound exceeded")
					}
				}
				if changed[id] {
					return nil, fmt.Errorf("one artifact cannot receive competing updates in a cycle")
				}
				changed[id] = true
				declared := map[string]CharacterWorkArtifactClaimV1{}
				for _, claim := range request.Claims {
					if err := validateWorkArtifactClaimIntentShapeV1(claim); err != nil {
						return nil, err
					}
					if len(claim.LineageRoots) > 0 || declared[claim.ClaimID].ClaimID != "" {
						return nil, fmt.Errorf("artifact request changed Host lineage or repeats claims")
					}
					for _, ref := range claim.SourceRefs {
						if !physicalContainsRefV2(proposal.KnowledgeRefs, ref) {
							return nil, fmt.Errorf("artifact claim references undeclared owner knowledge")
						}
					}
					declared[claim.ClaimID] = claim
				}
				merged := map[string]CharacterWorkArtifactClaimV1{}
				for _, claim := range artifact.Claims {
					merged[claim.ClaimID] = claim
				}
				for _, claimID := range result.ClaimIDs {
					claim, exists := declared[claimID]
					if !exists {
						return nil, fmt.Errorf("arbiter wrote an undeclared artifact claim")
					}
					if err := validateArtifactClaimSourceKindsV1(before, proposal.AgentID, claim); err != nil {
						return nil, err
					}
					claim = continuationCloneV1(claim)
					var err error
					claim.LineageRoots, err = artifactSourceLineageV1(before, proposal.AgentID, claim.SourceRefs)
					if err != nil {
						return nil, err
					}
					if len(claim.SourceRefs) == 0 {
						claim.LineageRoots = []string{artifactLineageRootV1("self_statement", proposal.AgentID+"/"+proposal.Digest)}
					}
					merged[claimID] = claim
				}
				artifact.Claims = nil
				for _, claim := range merged {
					artifact.Claims = append(artifact.Claims, claim)
				}
				sort.Slice(artifact.Claims, func(i, j int) bool { return artifact.Claims[i].ClaimID < artifact.Claims[j].ClaimID })
				if len(artifact.Claims) > CharacterWorkArtifactClaimLimitV1 {
					return nil, fmt.Errorf("artifact claim bound exceeded")
				}
				artifact.Status = "draft"
				if result.Complete {
					for id, claim := range declared {
						actual, exists := merged[id]
						actual.LineageRoots = nil
						if !exists || !samePhysicalValueV2(actual, claim) {
							return nil, fmt.Errorf("artifact cannot complete with unwritten or changed declared claims")
						}
					}
					artifact.Status = "complete"
				}
				artifact.UpdatedAtDay = result.AtDay
				var err error
				artifact.VersionDigest, err = ComputeCharacterWorkArtifactVersionDigestV1(artifact)
				if err != nil {
					return nil, err
				}
				row := WorldResourceBalanceV2{ResourceID: id, Name: request.Label, Artifact: &artifact, ReadableFacts: artifactReadableFactsV1(artifact)}
				if err := validateCharacterWorkArtifactV1(row); err != nil {
					return nil, err
				}
				found := false
				for i := range after.Resources {
					if after.Resources[i].ResourceID == id {
						after.Resources[i] = row
						found = true
						break
					}
				}
				if !found {
					after.Resources = append(after.Resources, row)
				}
				knowledge, err := newArtifactKnowledgeV1(receipt, stimulus, proposal, artifact, "authored", artifact.Claims, result.AtDay, nil)
				if err != nil {
					return nil, err
				}
				transition.authored[proposal.AgentID] = append(transition.authored[proposal.AgentID], knowledge)
			}
		}
		for key := range activeOutputs {
			if !reportedOutputs[key] {
				return nil, fmt.Errorf("executed output task must explicitly report created/updated/blocked output, not just elapsed work")
			}
		}
	}
	for id, amount := range materials {
		stock, _ := artifactStateResourceV1(before, id)
		if stock.ActualAmount == nil || amount > *stock.ActualAmount+1e-12 {
			return nil, fmt.Errorf("artifact materials exceed actual finite stock")
		}
		settled := false
		for _, s := range receipt.ResourceSettlements {
			if s.ResourceID == id && s.Delta != nil && physicalAmountsCloseV2(-*s.Delta, amount) {
				settled = true
			}
		}
		if !settled {
			return nil, fmt.Errorf("artifact material allocation must settle exactly once in the global stock ledger")
		}
	}
	return transition, nil
}

func (transition *workArtifactTransitionV1) bindOwnerPost(receipt WorldArbitrationReceipt, after WorldPhysicalStateV2, old CharacterPhysicalStateV2, post *CharacterPhysicalStateV2) error {
	if transition == nil {
		if !samePhysicalValueV2(post.ArtifactKnowledge, old.ArtifactKnowledge) {
			return fmt.Errorf("artifact knowledge cannot change without its explicit policy")
		}
		return nil
	}
	transition.claimedKnowledge[post.AgentID] = continuationCloneV1(post.ArtifactKnowledge)
	post.ArtifactKnowledge = continuationCloneV1(old.ArtifactKnowledge)
	post.ArtifactKnowledge = append(post.ArtifactKnowledge, continuationCloneV1(transition.authored[post.AgentID])...)
	for id, owner := range transition.created {
		if owner != post.AgentID {
			continue
		}
		resource, _ := artifactStateResourceV1(after, id)
		holding := CharacterResourceHoldingV2{ResourceID: id, PerceivedName: resource.Name, PerceivedLabel: resource.Name, Access: "exclusive", Perception: ResourcePerceptionV2{Kind: "unknown", AsOfChapter: receipt.Chapter, EvidenceRefs: []string{resource.Artifact.OriginProposalDigest}}, EvidenceRefs: []string{resource.Artifact.OriginProposalDigest}}
		if actual, exists := artifactHoldingV1(*post, id); exists {
			if !samePhysicalValueV2(actual, holding) {
				return fmt.Errorf("created artifact custody cannot be supplied or changed by arbiter post_state")
			}
		} else {
			post.Resources = append(post.Resources, holding)
		}
	}
	return nil
}

func (transition *workArtifactTransitionV1) creationGain(owner, id string) bool {
	return transition != nil && transition.created[id] == owner
}
