package domain

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// A claim fact is scoped to one owner's actually known resource version. It
// is a reference handle, never a certificate that the claim is true.
func CharacterArtifactClaimFactIDV1(owner, resourceID, versionDigest, claimID string) string {
	digest, _ := characterAgentDigest(struct{ Policy, Owner, ResourceID, VersionDigest, ClaimID string }{CharacterWorkArtifactPolicyV1, owner, resourceID, versionDigest, claimID})
	return "fact_" + strings.TrimPrefix(digest, "sha256:")
}

func artifactShapeIdentityV1(value string) bool {
	return physicalIdentityV2(value) && utf8.ValidString(value) && utf8.RuneCountInString(value) <= 256 && strings.IndexFunc(value, unicode.IsControl) < 0
}

func artifactPlacementShapeV1(placement CharacterArtifactPlacementV1) bool {
	return (placement.Kind == "with_actor" || placement.Kind == "stored") && artifactShapeIdentityV1(placement.Location) && artifactShapeIdentityV1(placement.CustodianAgentID)
}

func validateArtifactSignatureShapeV1(signature CharacterArtifactSignatureReceiptV1) error {
	if !physicalResourceIDV2(signature.ResourceID) || !artifactShapeIdentityV1(signature.Signer) || !artifactShapeIdentityV1(signature.GenerationID) || !artifactShapeIdentityV1(signature.SignerAgentID) || signature.Chapter < 1 || signature.Cycle < 1 || !finiteStoryDay(signature.AtDay) || signature.AtDay < 0 || strings.TrimSpace(signature.Scope) == "" || !utf8.ValidString(signature.Scope) || utf8.RuneCountInString(signature.Scope) > 500 {
		return fmt.Errorf("artifact signature source/scope/time is invalid")
	}
	if validatePlanningV2Digest("artifact signed version", signature.VersionDigest) != nil || validatePlanningV2Digest("artifact signature proposal", signature.SourceProposalDigest) != nil || len(signature.ClaimIDs) == 0 || len(signature.ClaimIDs) > CharacterWorkArtifactClaimLimitV1 {
		return fmt.Errorf("artifact signature lacks its exact version and bounded claims")
	}
	ids := map[string]bool{}
	for _, id := range signature.ClaimIDs {
		if !artifactShapeIdentityV1(id) || ids[id] {
			return fmt.Errorf("artifact signature claim scope is invalid")
		}
		ids[id] = true
	}
	digest, err := ComputeCharacterArtifactSignatureDigestV1(signature)
	if err != nil || digest != signature.SignatureDigest {
		return fmt.Errorf("artifact signature digest is invalid")
	}
	return nil
}

func validateArtifactClaimsShapeV1(claims []CharacterWorkArtifactClaimV1) error {
	if len(claims) == 0 || len(claims) > CharacterWorkArtifactClaimLimitV1 {
		return fmt.Errorf("artifact requires bounded nonempty claims")
	}
	seen := map[string]bool{}
	for _, claim := range claims {
		if err := validateWorkArtifactClaimIntentShapeV1(claim); err != nil {
			return err
		}
		if seen[claim.ClaimID] || !utf8.ValidString(claim.Text) || !utf8.ValidString(claim.AttributedTo) || len(claim.LineageRoots) == 0 || len(claim.LineageRoots) > CharacterWorkArtifactClaimLimitV1 {
			return fmt.Errorf("artifact claims require unique identities and bounded opaque lineage")
		}
		seen[claim.ClaimID] = true
		roots := map[string]bool{}
		for _, root := range claim.LineageRoots {
			if !characterSourceDigestPatternV2.MatchString(root) || roots[root] {
				return fmt.Errorf("artifact lineage must be unique opaque source digests")
			}
			roots[root] = true
		}
	}
	return nil
}

func artifactClaimStatementV1(claim CharacterWorkArtifactClaimV1) string {
	label := "派生文档声明（" + claim.EpistemicKind
	if claim.AttributedTo != "" {
		label += "/归属于" + claim.AttributedTo
	}
	return label + "；不自动等于世界真相）：" + claim.Text
}

func artifactReadableFactsV1(a CharacterWorkArtifactV1) []ResourceReadableFactV2 {
	var out []ResourceReadableFactV2
	if a.Claims != nil {
		out = make([]ResourceReadableFactV2, 0, len(a.Claims))
	}
	for _, claim := range a.Claims {
		out = append(out, ResourceReadableFactV2{ID: claim.ClaimID, Text: artifactClaimStatementV1(claim)})
	}
	return out
}

// Shape/digest checks only. Complete source/intent/execution authorization is
// enforced by the artifact transition, not by a self-reported content digest.
func validateCharacterWorkArtifactV1(balance WorldResourceBalanceV2) error {
	if balance.Artifact == nil {
		return nil
	}
	a := *balance.Artifact
	if a.Version != CharacterWorkArtifactPolicyV1 || balance.Unit != "" || balance.ActualAmount != nil || balance.ResourceID != a.ResourceID || !physicalResourceIDV2(a.ResourceID) {
		return fmt.Errorf("artifact must be a typed non-quantity resource with its exact identity")
	}
	if !artifactShapeIdentityV1(a.CreatorAgentID) || !artifactShapeIdentityV1(a.OriginGenerationID) || !artifactShapeIdentityV1(a.OriginTaskID) || !artifactShapeIdentityV1(a.OutputKey) || a.OriginChapter < 1 || a.OriginCycle < 1 {
		return fmt.Errorf("artifact creator/origin/task identity is incomplete")
	}
	if a.ResourceID != CharacterWorkArtifactResourceIDV1(a.OriginGenerationID, a.CreatorAgentID, a.OriginTaskID, a.OutputKey) {
		return fmt.Errorf("artifact resource id is not bound to its original creator/task")
	}
	if validatePlanningV2Digest("artifact origin proposal", a.OriginProposalDigest) != nil || a.Revision < 1 || a.Revision > CharacterWorkArtifactRevisionLimitV1 || (a.Status != "draft" && a.Status != "complete") {
		return fmt.Errorf("artifact original proposal/revision/status is invalid")
	}
	if a.Revision == 1 && a.PreviousVersionDigest != "" || a.Revision > 1 && (validatePlanningV2Digest("artifact previous version", a.PreviousVersionDigest) != nil || a.PreviousVersionDigest == a.VersionDigest) {
		return fmt.Errorf("artifact revision lacks its exact prior version")
	}
	if !finiteStoryDay(a.CreatedAtDay) || !finiteStoryDay(a.UpdatedAtDay) || a.CreatedAtDay < 0 || a.UpdatedAtDay < a.CreatedAtDay {
		return fmt.Errorf("artifact creation/update time is invalid")
	}
	if !artifactPlacementShapeV1(a.Placement) {
		return fmt.Errorf("artifact placement requires an explicit location and custodian")
	}
	if len(a.Materials) == 0 || len(a.Materials) > CharacterWorkArtifactLimitV1 {
		return fmt.Errorf("artifact requires bounded original material allocation")
	}
	materials := map[string]bool{}
	for _, material := range a.Materials {
		if !physicalResourceIDV2(material.ResourceID) || material.ResourceID == a.ResourceID || materials[material.ResourceID] || !finiteStoryDay(material.Amount) || material.Amount <= 0 {
			return fmt.Errorf("artifact material allocation must be unique, finite and positive")
		}
		materials[material.ResourceID] = true
	}
	if err := validateArtifactClaimsShapeV1(a.Claims); err != nil {
		return err
	}
	digest, err := ComputeCharacterWorkArtifactVersionDigestV1(a)
	if err != nil || a.VersionDigest != digest {
		return fmt.Errorf("artifact content version digest mismatch")
	}
	if !samePhysicalValueV2(balance.ReadableFacts, artifactReadableFactsV1(a)) {
		return fmt.Errorf("artifact readable facts differ from its exact attributed claims")
	}
	if len(a.Signatures) > CharacterWorkArtifactClaimLimitV1*CharacterWorkArtifactRevisionLimitV1 {
		return fmt.Errorf("artifact signature history exceeds bounded capacity")
	}
	seen := map[string]bool{}
	claims := map[string]bool{}
	for _, claim := range a.Claims {
		claims[claim.ClaimID] = true
	}
	for _, signature := range a.Signatures {
		if err := validateArtifactSignatureShapeV1(signature); err != nil {
			return err
		}
		if signature.ResourceID != a.ResourceID || signature.AtDay < a.CreatedAtDay {
			return fmt.Errorf("artifact signature differs from its physical source")
		}
		for _, id := range signature.ClaimIDs {
			if signature.VersionDigest == a.VersionDigest && !claims[id] {
				return fmt.Errorf("artifact signature claim scope is invalid")
			}
		}
		if seen[signature.SignatureDigest] {
			return fmt.Errorf("artifact signature digest is invalid or repeated")
		}
		seen[signature.SignatureDigest] = true
	}
	return nil
}

func ComputeCharacterArtifactKnowledgeDigestV1(owner string, k CharacterArtifactKnowledgeV1) (string, error) {
	if !artifactShapeIdentityV1(owner) {
		return "", fmt.Errorf("artifact knowledge requires a bound owner")
	}
	k.KnowledgeDigest = ""
	return characterAgentDigest(struct {
		Policy, Owner string
		Knowledge     CharacterArtifactKnowledgeV1
	}{CharacterWorkArtifactPolicyV1, owner, k})
}

func ComputeCharacterArtifactSignatureDigestV1(signature CharacterArtifactSignatureReceiptV1) (string, error) {
	signature.SignatureDigest = ""
	return characterAgentDigest(struct {
		Policy    string
		Signature CharacterArtifactSignatureReceiptV1
	}{CharacterWorkArtifactPolicyV1, signature})
}

func validateCharacterArtifactKnowledgeStateV1(actor CharacterPhysicalStateV2, catalog map[string]WorldResourceBalanceV2) error {
	if len(actor.ArtifactKnowledge) > CharacterWorkArtifactLimitV1*CharacterWorkArtifactRevisionLimitV1 {
		return fmt.Errorf("artifact knowledge history exceeds bounded capacity")
	}
	seen := map[string]bool{}
	for _, knowledge := range actor.ArtifactKnowledge {
		resource, exists := catalog[knowledge.ResourceID]
		if !exists || resource.Artifact == nil || !artifactShapeIdentityV1(knowledge.GenerationID) || knowledge.Chapter < 1 || knowledge.Cycle < 1 || !finiteStoryDay(knowledge.AtDay) || knowledge.AtDay < 0 || knowledge.Revision < 1 || knowledge.Revision > resource.Artifact.Revision || (knowledge.Status != "draft" && knowledge.Status != "complete") || (knowledge.Kind != "authored" && knowledge.Kind != "read" && knowledge.Kind != "signed") {
			return fmt.Errorf("artifact knowledge requires a real typed resource and bounded owner receipt")
		}
		if validatePlanningV2Digest("artifact known version", knowledge.VersionDigest) != nil || validatePlanningV2Digest("artifact knowledge proposal", knowledge.SourceProposalDigest) != nil {
			return fmt.Errorf("artifact knowledge version/source is invalid")
		}
		if !artifactPlacementShapeV1(knowledge.Placement) {
			return fmt.Errorf("artifact knowledge requires its actual source placement snapshot")
		}
		if knowledge.Kind == "authored" && resource.Artifact.CreatorAgentID != actor.AgentID {
			return fmt.Errorf("authored artifact knowledge belongs only to its original creator")
		}
		if err := validateArtifactClaimsShapeV1(knowledge.Claims); err != nil {
			return err
		}
		if len(knowledge.Signatures) > CharacterWorkArtifactClaimLimitV1*CharacterWorkArtifactRevisionLimitV1 {
			return fmt.Errorf("known artifact signatures exceed bounded capacity")
		}
		knownClaims := map[string]bool{}
		for _, claim := range knowledge.Claims {
			knownClaims[claim.ClaimID] = true
		}
		seenSignatures := map[string]bool{}
		ownSignature := false
		for _, signature := range knowledge.Signatures {
			if err := validateArtifactSignatureShapeV1(signature); err != nil {
				return err
			}
			if signature.ResourceID != knowledge.ResourceID || signature.VersionDigest != knowledge.VersionDigest || signature.AtDay > knowledge.AtDay || seenSignatures[signature.SignatureDigest] {
				return fmt.Errorf("known signature must belong to the actually known version/time")
			}
			seenSignatures[signature.SignatureDigest] = true
			for _, id := range signature.ClaimIDs {
				if !knownClaims[id] {
					return fmt.Errorf("known signature covers an unread claim")
				}
			}
			if signature.SignerAgentID == actor.AgentID && signature.Signer == actor.Character && signature.SourceProposalDigest == knowledge.SourceProposalDigest && signature.AtDay == knowledge.AtDay && signature.GenerationID == knowledge.GenerationID && signature.Chapter == knowledge.Chapter && signature.Cycle == knowledge.Cycle {
				ownSignature = true
			}
		}
		if knowledge.Kind == "signed" && !ownSignature {
			return fmt.Errorf("signed knowledge requires this owner's actual signature receipt")
		}
		if knowledge.VersionDigest == resource.Artifact.VersionDigest {
			if knowledge.Revision != resource.Artifact.Revision || knowledge.Status != resource.Artifact.Status {
				return fmt.Errorf("artifact current knowledge metadata differs from its exact version")
			}
			current := map[string]CharacterWorkArtifactClaimV1{}
			for _, claim := range resource.Artifact.Claims {
				current[claim.ClaimID] = claim
			}
			for _, claim := range knowledge.Claims {
				source, ok := current[claim.ClaimID]
				if !ok || !samePhysicalValueV2(source, claim) {
					return fmt.Errorf("artifact current knowledge changed its exact claim")
				}
			}
		} else if knowledge.Revision >= resource.Artifact.Revision {
			return fmt.Errorf("artifact historical knowledge must name an earlier revision")
		}
		digest, err := ComputeCharacterArtifactKnowledgeDigestV1(actor.AgentID, knowledge)
		if err != nil || digest != knowledge.KnowledgeDigest || seen[digest] {
			return fmt.Errorf("artifact knowledge owner digest is invalid or repeated")
		}
		seen[digest] = true
	}
	return nil
}

// Builds only the owner's actual knowledge. Current world content is never
// substituted for an old read version. Source transition verification must
// precede use of this shape/digest-checked view at a model boundary.
func BuildCharacterArtifactViewsV1(state WorldPhysicalStateV2, owner string) ([]CharacterArtifactViewV1, error) {
	if err := ValidateWorldPhysicalStateV2(state); err != nil {
		return nil, err
	}
	catalog := map[string]WorldResourceBalanceV2{}
	for _, r := range state.Resources {
		if err := validateCharacterWorkArtifactV1(r); err != nil {
			return nil, err
		}
		catalog[r.ResourceID] = r
	}
	var actor *CharacterPhysicalStateV2
	for i := range state.Actors {
		if state.Actors[i].AgentID == owner {
			actor = &state.Actors[i]
			break
		}
	}
	if actor == nil {
		return nil, fmt.Errorf("artifact views require an existing owner")
	}
	if err := validateCharacterArtifactKnowledgeStateV1(*actor, catalog); err != nil {
		return nil, err
	}
	type group struct {
		view       CharacterArtifactViewV1
		claims     map[string]CharacterWorkArtifactClaimV1
		signatures map[string]CharacterArtifactSignatureReceiptV1
	}
	groups := map[string]*group{}
	for _, knowledge := range actor.ArtifactKnowledge {
		key := knowledge.ResourceID + "/" + knowledge.VersionDigest
		g := groups[key]
		if g == nil {
			placement := knowledge.Placement
			g = &group{view: CharacterArtifactViewV1{Placement: &placement, Status: knowledge.Status, Revision: knowledge.Revision, ResourceID: knowledge.ResourceID, VersionDigest: knowledge.VersionDigest, KnowledgeKind: knowledge.Kind, AtDay: knowledge.AtDay, Chapter: knowledge.Chapter, Cycle: knowledge.Cycle}, claims: map[string]CharacterWorkArtifactClaimV1{}, signatures: map[string]CharacterArtifactSignatureReceiptV1{}}
			groups[key] = g
		}
		if g.view.Status != knowledge.Status || g.view.Revision != knowledge.Revision {
			return nil, fmt.Errorf("artifact known version has conflicting metadata")
		}
		if knowledge.Kind == "authored" || knowledge.Kind == "read" && g.view.KnowledgeKind == "signed" {
			g.view.KnowledgeKind = knowledge.Kind
		}
		if knowledge.AtDay > g.view.AtDay || (knowledge.AtDay == g.view.AtDay && (knowledge.Chapter > g.view.Chapter || knowledge.Chapter == g.view.Chapter && knowledge.Cycle > g.view.Cycle)) {
			g.view.AtDay, g.view.Chapter, g.view.Cycle = knowledge.AtDay, knowledge.Chapter, knowledge.Cycle
			placement := knowledge.Placement
			g.view.Placement = &placement
		}
		for _, claim := range knowledge.Claims {
			if previous, ok := g.claims[claim.ClaimID]; ok && !samePhysicalValueV2(previous, claim) {
				return nil, fmt.Errorf("artifact known version has conflicting claims")
			}
			g.claims[claim.ClaimID] = claim
		}
		if len(g.claims) > CharacterWorkArtifactClaimLimitV1 {
			return nil, fmt.Errorf("artifact known version exceeds its bounded claim capacity")
		}
		for _, signature := range knowledge.Signatures {
			if previous, exists := g.signatures[signature.SignatureDigest]; exists && !samePhysicalValueV2(previous, signature) {
				return nil, fmt.Errorf("artifact known signature has conflicting source fields")
			}
			g.signatures[signature.SignatureDigest] = signature
		}
	}
	for _, holding := range actor.Resources {
		resource := catalog[holding.ResourceID]
		if resource.Artifact == nil || holding.Access == "none" || holding.Perception.Kind == "unaware" {
			continue
		}
		a := resource.Artifact
		ownCarrying := a.Placement.Kind == "with_actor" && a.Placement.CustodianAgentID == owner
		if !ownCarrying && a.Placement.Location != actor.Location {
			continue // Persistent access rights are not remote perception.
		}
		key := a.ResourceID + "/" + a.VersionDigest
		if groups[key] == nil {
			groups[key] = &group{view: CharacterArtifactViewV1{ResourceID: a.ResourceID, VersionDigest: a.VersionDigest, KnowledgeKind: "unread", Claims: []CharacterArtifactClaimViewV1{}}, claims: map[string]CharacterWorkArtifactClaimV1{}}
		}
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var views []CharacterArtifactViewV1
	if actor.ArtifactKnowledge != nil {
		views = []CharacterArtifactViewV1{}
	}
	for _, key := range keys {
		g := groups[key]
		current := catalog[g.view.ResourceID].Artifact
		if g.view.KnowledgeKind != "unread" && current.Placement.Kind == "with_actor" && current.Placement.CustodianAgentID == owner {
			placement := current.Placement
			g.view.Placement = &placement // Only the owner's own actual carrying is current knowledge.
		}
		ids := make([]string, 0, len(g.claims))
		for id := range g.claims {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			claim := g.claims[id]
			roots := append([]string(nil), claim.LineageRoots...)
			g.view.Claims = append(g.view.Claims, CharacterArtifactClaimViewV1{ID: CharacterArtifactClaimFactIDV1(owner, g.view.ResourceID, g.view.VersionDigest, id), ClaimID: id, Text: artifactClaimStatementV1(claim), EpistemicKind: claim.EpistemicKind, AttributedTo: claim.AttributedTo, LineageRoots: roots})
		}
		signatures := make([]string, 0, len(g.signatures))
		for digest := range g.signatures {
			signatures = append(signatures, digest)
		}
		sort.Strings(signatures)
		for _, digest := range signatures {
			signature := g.signatures[digest]
			signature.ClaimIDs = append([]string(nil), signature.ClaimIDs...)
			g.view.Signatures = append(g.view.Signatures, signature)
		}
		views = append(views, g.view)
	}
	return views, nil
}

// Independence is lineage disjointness, not different filenames, resource IDs,
// signatures, or copies. It makes no claim that either source is truthful.
func ArtifactIndependentSourcesV1(state WorldPhysicalStateV2, resourceIDs []string) ([]string, error) {
	if err := ValidateWorldPhysicalStateV2(state); err != nil {
		return nil, err
	}
	catalog := map[string]WorldResourceBalanceV2{}
	for _, resource := range state.Resources {
		catalog[resource.ResourceID] = resource
	}
	seenIDs, usedRoots := map[string]bool{}, map[string]bool{}
	var result []string
	if resourceIDs != nil {
		result = []string{}
	}
	for _, id := range resourceIDs {
		resource, exists := catalog[id]
		if !exists || seenIDs[id] {
			return nil, fmt.Errorf("independence requires distinct existing resources")
		}
		seenIDs[id] = true
		roots := map[string]bool{}
		if resource.Artifact == nil {
			roots[artifactLineageRootV1("resource", id)] = true
		} else {
			if err := validateCharacterWorkArtifactV1(resource); err != nil {
				return nil, err
			}
			for _, claim := range resource.Artifact.Claims {
				if claim.EpistemicKind == "self_statement" || claim.EpistemicKind == "pending" {
					return nil, fmt.Errorf("self statements and pending questions are not independent factual evidence")
				}
				for _, root := range claim.LineageRoots {
					roots[root] = true
				}
			}
		}
		for root := range roots {
			if usedRoots[root] {
				return nil, fmt.Errorf("sources share original lineage and are not independent")
			}
			usedRoots[root] = true
			result = append(result, root)
		}
	}
	sort.Strings(result)
	return result, nil
}
