package domain

import (
	"fmt"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"
)

func artifactLineageRootV1(kind, id string) string {
	digest, _ := characterAgentDigest(struct{ Policy, Kind, ID string }{CharacterWorkArtifactPolicyV1, kind, id})
	return digest
}

func validateWorkArtifactClaimIntentShapeV1(claim CharacterWorkArtifactClaimV1) error {
	if !physicalIdentityV2(claim.ClaimID) || len(claim.ClaimID) > 128 || strings.TrimSpace(claim.Text) == "" || utf8.RuneCountInString(claim.Text) > CharacterWorkArtifactTextLimitV1 || len(claim.SourceRefs) > 16 {
		return fmt.Errorf("artifact claim requires a bounded identity, exact text and visible sources")
	}
	switch claim.EpistemicKind {
	case "document_statement", "self_statement", "pending":
	case "attributed_statement":
		if strings.TrimSpace(claim.AttributedTo) == "" {
			return fmt.Errorf("attributed artifact claim requires its explicit speaker")
		}
	default:
		return fmt.Errorf("artifact claim must preserve its statement/pending kind, not assert independent truth")
	}
	if len(claim.SourceRefs) == 0 && claim.EpistemicKind != "self_statement" && claim.EpistemicKind != "pending" {
		return fmt.Errorf("document/attributed artifact claims require existing visible sources")
	}
	if utf8.RuneCountInString(claim.AttributedTo) > 160 || len(normalizeV2Strings(claim.SourceRefs)) != len(claim.SourceRefs) {
		return fmt.Errorf("artifact claim attribution/sources are invalid")
	}
	for _, ref := range claim.SourceRefs {
		if !physicalIdentityV2(ref) || len(ref) > 256 {
			return fmt.Errorf("artifact claim sources must be opaque visible identities")
		}
	}
	return nil
}

func workArtifactIntentPresentV1(p CharacterDecisionProposal) bool {
	if len(p.ArtifactReads)+len(p.ArtifactSigns)+len(p.ArtifactAccess) > 0 {
		return true
	}
	for _, task := range p.SelfTasks {
		if len(task.OutputRequests) > 0 {
			return true
		}
	}
	return false
}

func ValidateCharacterArtifactIntentV1(p CharacterDecisionProposal, o CharacterObservationPacket) error {
	if !HasCharacterWorkArtifactPolicyV1(o.Sources) {
		if workArtifactIntentPresentV1(p) {
			return fmt.Errorf("work outputs/read versions/signatures require explicit work-artifact policy")
		}
		return nil
	}
	if o.Version != CharacterObservationV2Version {
		return fmt.Errorf("work artifacts require a v2 owner observation")
	}
	if err := artifactPolicySourcesV1(o.Sources); err != nil {
		return err
	}
	allowed := o.AllowedFactIDs()
	resources := map[string]CharacterResourceViewV2{}
	for _, view := range o.ResourceViews {
		resources[view.ResourceID] = view
	}
	views := map[string]CharacterArtifactViewV1{}
	for _, view := range o.ArtifactViews {
		views[view.ResourceID+"/"+view.VersionDigest] = view
	}
	outputs := map[string]bool{}
	for _, task := range p.SelfTasks {
		if len(task.OutputRequests) > 4 || (len(task.OutputRequests) > 0 && task.Kind != "work") {
			return fmt.Errorf("only bounded owner work tasks may declare document outputs")
		}
		for _, output := range task.OutputRequests {
			if !physicalIdentityV2(output.OutputKey) || len(output.OutputKey) > 128 || outputs[output.OutputKey] || strings.TrimSpace(output.Label) == "" || utf8.RuneCountInString(output.Label) > 160 || strings.IndexFunc(output.Label, unicode.IsControl) >= 0 || len(output.Claims) == 0 || len(output.Claims) > CharacterWorkArtifactClaimLimitV1 {
				return fmt.Errorf("artifact output identity/label/claims are missing, duplicate or unbounded")
			}
			outputs[output.OutputKey] = true
			if output.ResourceID == "" {
				if output.ExpectedVersionDigest != "" || len(output.MaterialInputs) == 0 {
					return fmt.Errorf("new artifact requires explicit material allocation and no invented prior version")
				}
			} else {
				view, known := views[output.ResourceID+"/"+output.ExpectedVersionDigest]
				if !physicalResourceIDV2(output.ResourceID) || validatePlanningV2Digest("artifact expected version", output.ExpectedVersionDigest) != nil || len(output.MaterialInputs) > 0 || !known || view.KnowledgeKind != "authored" {
					return fmt.Errorf("artifact update requires its author's known exact version and no repeated material allocation")
				}
			}
			materials := map[string]bool{}
			for _, material := range output.MaterialInputs {
				view, known := resources[material.ResourceID]
				if !known || materials[material.ResourceID] || view.Perception.Kind == "unaware" || view.Access == "none" || view.Unit == "" || math.IsNaN(material.Amount) || math.IsInf(material.Amount, 0) || material.Amount <= 0 || !physicalContainsRefV2(task.ResourceIDs, material.ResourceID) {
					return fmt.Errorf("artifact material must be explicitly used, accessible and positively allocated")
				}
				materials[material.ResourceID] = true
			}
			claims := map[string]bool{}
			for _, claim := range output.Claims {
				if err := validateWorkArtifactClaimIntentShapeV1(claim); err != nil {
					return err
				}
				if claims[claim.ClaimID] || len(claim.LineageRoots) > 0 {
					return fmt.Errorf("artifact intent repeats a claim or supplies Host-derived lineage")
				}
				claims[claim.ClaimID] = true
				for _, ref := range claim.SourceRefs {
					if _, ok := allowed[ref]; !ok || !physicalContainsRefV2(p.KnowledgeRefs, ref) {
						return fmt.Errorf("artifact claim must cite the author's visible declared knowledge")
					}
				}
			}
		}
	}
	if len(outputs) > 16 || len(p.ArtifactReads) > 16 || len(p.ArtifactSigns) > 16 {
		return fmt.Errorf("too many artifact operations")
	}
	seen := map[string]bool{}
	for _, grant := range p.ArtifactAccess {
		resource, known := resources[grant.ResourceID]
		_, versionKnown := views[grant.ResourceID+"/"+grant.VersionDigest]
		key := grant.ResourceID + "/" + grant.ToCharacter
		if !known || resource.Access == "none" || !versionKnown || seen[key] || strings.TrimSpace(grant.ToCharacter) == "" || utf8.RuneCountInString(grant.ToCharacter) > 160 || (grant.Access != "shared" && grant.Access != "exclusive") {
			return fmt.Errorf("artifact access requires a known exact version and explicit recipient/scope")
		}
		seen[key] = true
	}
	seen = map[string]bool{}
	for _, read := range p.ArtifactReads {
		if !artifactIntentTaskV1(p, read.TaskID, read.ResourceID) {
			return fmt.Errorf("artifact read must name its explicit owner work task and resource")
		}
		key := read.ResourceID + "/" + read.VersionDigest
		view, known := views[key]
		resource, accessible := resources[read.ResourceID]
		if !known || !accessible || resource.Access == "none" || resource.Perception.Kind == "unaware" || seen[key] || validatePlanningV2Digest("artifact read version", read.VersionDigest) != nil || len(normalizeV2Strings(read.ClaimIDs)) != len(read.ClaimIDs) || len(read.ClaimIDs) > CharacterWorkArtifactClaimLimitV1 {
			return fmt.Errorf("artifact read requires a visible accessible exact version")
		}
		_ = view // An unread metadata view authorizes requesting, not knowing, its text.
		seen[key] = true
	}
	seen = map[string]bool{}
	for _, sign := range p.ArtifactSigns {
		if !artifactIntentTaskV1(p, sign.TaskID, sign.ResourceID) {
			return fmt.Errorf("artifact signature must name its explicit owner work task and resource")
		}
		key := sign.ResourceID + "/" + sign.VersionDigest
		view, known := views[key]
		if !known || view.KnowledgeKind == "unread" || seen[key] || strings.TrimSpace(sign.Scope) == "" || utf8.RuneCountInString(sign.Scope) > 500 || len(sign.ClaimIDs) == 0 || len(sign.ClaimIDs) > CharacterWorkArtifactClaimLimitV1 || len(normalizeV2Strings(sign.ClaimIDs)) != len(sign.ClaimIDs) {
			return fmt.Errorf("artifact signing requires an actually known version and bounded explicit claim scope")
		}
		knownClaims := map[string]bool{}
		for _, claim := range view.Claims {
			knownClaims[claim.ClaimID] = true
		}
		for _, id := range sign.ClaimIDs {
			if !knownClaims[id] {
				return fmt.Errorf("cannot sign an unread artifact claim")
			}
		}
		seen[key] = true
	}
	return nil
}

func validateWorkArtifactObservationV1(o CharacterObservationPacket) error {
	if !HasCharacterWorkArtifactPolicyV1(o.Sources) {
		if len(o.ArtifactViews) > 0 {
			return fmt.Errorf("artifact views require their explicit v3 policy")
		}
		return nil
	}
	if err := artifactPolicySourcesV1(o.Sources); err != nil {
		return err
	}
	if o.Version != CharacterObservationV2Version {
		return fmt.Errorf("artifact views require v2 observations")
	}
	seen := map[string]bool{}
	for _, view := range o.ArtifactViews {
		key := view.ResourceID + "/" + view.VersionDigest
		if seen[key] || !physicalResourceIDV2(view.ResourceID) || validatePlanningV2Digest("artifact view version", view.VersionDigest) != nil || !finiteStoryDay(view.AtDay) || view.AtDay < 0 {
			return fmt.Errorf("artifact view identity/version is invalid")
		}
		seen[key] = true
		if view.KnowledgeKind == "unread" {
			if len(view.Claims) > 0 || len(view.Signatures) > 0 || view.Status != "" || view.Revision != 0 || view.Placement != nil {
				return fmt.Errorf("unread artifact capability cannot expose unperceived content or state")
			}
			continue
		}
		if view.Revision < 1 || view.Revision > CharacterWorkArtifactRevisionLimitV1 || (view.Status != "draft" && view.Status != "complete") {
			return fmt.Errorf("known artifact view lacks its actual version status")
		}
		if view.KnowledgeKind != "authored" && view.KnowledgeKind != "read" && view.KnowledgeKind != "signed" {
			return fmt.Errorf("artifact view lacks an actual owner knowledge source")
		}
		for _, claim := range view.Claims {
			if claim.ID != CharacterArtifactClaimFactIDV1(o.AgentID, view.ResourceID, view.VersionDigest, claim.ClaimID) {
				return fmt.Errorf("artifact claim knowledge id is not owner/version bound")
			}
		}
	}
	return nil
}

func validateWorkArtifactStimulusV1(s WorldStimulusPacket) error {
	present := false
	if s.PhysicalState != nil {
		for _, resource := range s.PhysicalState.Resources {
			present = present || resource.Artifact != nil
		}
		for _, actor := range s.PhysicalState.Actors {
			present = present || len(actor.ArtifactKnowledge) > 0
		}
	}
	if !HasCharacterWorkArtifactPolicyV1(s.Sources) {
		if present {
			return fmt.Errorf("artifact-bearing world state cannot drop its explicit artifact policy")
		}
		return nil
	}
	if err := artifactPolicySourcesV1(s.Sources); err != nil {
		return err
	}
	if s.Version != WorldStimulusPacketV2Version || s.PhysicalState == nil || s.StoryClock == nil || s.SelfEvaluationContext == nil || !HasCharacterActivationCycleContract(s) {
		return fmt.Errorf("artifact world input requires its exact v3 physical/clock/evaluation context")
	}
	return nil
}
