package domain

import (
	"fmt"
	"sort"
)

// The ordinary physical helper has no authority to advance an old proposal's
// output CAS. Only this already-verified round source supplies that privilege.
func ApplyArbitrationPhysicalStateWithVerifiedSourcesV1(s VerifiedCharacterArbitrationSourcesV1, receipt WorldArbitrationReceipt) (WorldPhysicalStateV2, error) {
	if !s.verified || s.digest == "" || receipt.Round != s.CurrentRound() {
		return WorldPhysicalStateV2{}, fmt.Errorf("artifact continuation physics requires verified round sources")
	}
	prior := map[string]string{}
	for owner, source := range s.owners {
		if source.source.Kind == "continuation" {
			prior[owner] = source.proposal.Digest
		}
	}
	return applyArbitrationPhysicalStateWithArtifactSourcesV1(receipt, s.input.Stimulus, s.EffectiveProposals(), prior)
}

// This is an information projection only, not a source verifier. Its callers
// already hold the V3 prefix. It suppresses only the exact expected products
// of the immutable original authorization, never every same-owner artifact.
func continuationArtifactInformationViewV1(o CharacterObservationPacket, original CharacterDecisionProposal, initial, current WorldPhysicalStateV2) (CharacterObservationPacket, []CharacterArtifactViewV1, error) {
	o = continuationCloneV1(o)
	if !HasCharacterWorkArtifactPolicyV1(o.Sources) {
		return o, nil, nil
	}
	baselineViews, err := BuildCharacterArtifactViewsV1(initial, o.AgentID)
	if err != nil {
		return o, nil, err
	}
	currentActor, ok := artifactStateActorV1(current, o.AgentID)
	if !ok {
		return o, nil, fmt.Errorf("artifact continuation owner missing")
	}
	filtered := map[string]bool{}
	for _, task := range original.SelfTasks {
		for _, request := range task.OutputRequests {
			id := request.ResourceID
			if id == "" {
				id = CharacterWorkArtifactResourceIDV1(original.GenerationID, original.AgentID, task.TaskID, request.OutputKey)
			}
			resource, exists := artifactStateResourceV1(current, id)
			if !exists || resource.Artifact == nil {
				continue
			}
			a := resource.Artifact
			if a.CreatorAgentID != original.AgentID || a.Placement.CustodianAgentID != original.AgentID {
				continue
			}
			if request.ResourceID == "" && (a.OriginProposalDigest != original.Digest || a.OriginTaskID != task.TaskID || a.OutputKey != request.OutputKey) {
				continue
			}
			base, existed := artifactStateResourceV1(initial, id)
			if request.ResourceID != "" && (!existed || base.Artifact == nil || base.Artifact.VersionDigest != request.ExpectedVersionDigest) {
				continue
			}
			if existed && base.Artifact != nil {
				if !samePhysicalValueV2(base.Artifact.Signatures, a.Signatures) {
					continue
				}
			} else if len(a.Signatures) > 0 {
				continue
			}
			own := false
			for _, knowledge := range currentActor.ArtifactKnowledge {
				if knowledge.ResourceID == id && knowledge.VersionDigest == a.VersionDigest && knowledge.Kind == "authored" && knowledge.SourceProposalDigest == original.Digest {
					own = true
				}
			}
			if !own {
				continue
			}
			declared := map[string]CharacterWorkArtifactClaimV1{}
			for _, claim := range request.Claims {
				declared[claim.ClaimID] = claim
			}
			valid := true
			for _, claim := range a.Claims {
				claimed, found := declared[claim.ClaimID]
				if !found && existed && base.Artifact != nil {
					for _, prior := range base.Artifact.Claims {
						if prior.ClaimID == claim.ClaimID {
							claimed = prior
							claimed.LineageRoots = nil
							found = true
						}
					}
				}
				actual := claim
				actual.LineageRoots = nil
				if !found || !samePhysicalValueV2(actual, claimed) {
					valid = false
					break
				}
			}
			if valid {
				filtered[id] = true
			}
		}
	}
	baselineResources, err := BuildCharacterResourceViewsV2(initial, o.AgentID)
	if err != nil {
		return o, nil, err
	}
	resources := []CharacterResourceViewV2{}
	for _, view := range o.ResourceViews {
		if !filtered[view.ResourceID] {
			resources = append(resources, view)
		}
	}
	for _, view := range baselineResources {
		if filtered[view.ResourceID] {
			resources = append(resources, view)
		}
	}
	// Resource views are canonically ID ordered at both sides of the root.
	sort.Slice(resources, func(i, j int) bool { return resources[i].ResourceID < resources[j].ResourceID })
	o.ResourceViews = resources
	var artifacts []CharacterArtifactViewV1
	for _, view := range o.ArtifactViews {
		if !filtered[view.ResourceID] && view.KnowledgeKind != "unread" {
			artifacts = append(artifacts, view)
		}
	}
	for _, view := range baselineViews {
		if filtered[view.ResourceID] && view.KnowledgeKind != "unread" {
			artifacts = append(artifacts, view)
		}
	}
	o.ArtifactViews = artifacts
	sort.Slice(artifacts, func(i, j int) bool {
		if artifacts[i].ResourceID != artifacts[j].ResourceID {
			return artifacts[i].ResourceID < artifacts[j].ResourceID
		}
		return artifacts[i].VersionDigest < artifacts[j].VersionDigest
	})
	return o, artifacts, nil
}
