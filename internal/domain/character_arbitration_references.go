package domain

import (
	"fmt"
	"strings"
)

// This is a private validation directory, not new evidence or a model view.
// Its members are exactly the existing kernel's sources; reference membership
// alone never grants access, received information, or a successful outcome.
type arbitrationReferenceCatalogV2 struct {
	evidence   map[string]bool
	mechanisms map[string]bool
}

func newArbitrationReferenceCatalogV2(stimulus WorldStimulusPacket, before WorldPhysicalStateV2, proposalDigests []string, proposals []CharacterDecisionProposal) arbitrationReferenceCatalogV2 {
	catalog := arbitrationReferenceCatalogV2{evidence: map[string]bool{stimulus.Digest: true}, mechanisms: map[string]bool{}}
	add := func(refs []string) {
		for _, ref := range refs {
			if strings.TrimSpace(ref) != "" {
				catalog.evidence[ref] = true
			}
		}
	}
	add(stimulus.Sources)
	add(proposalDigests)
	for _, group := range [][]CharacterAgentFact{stimulus.PublicFacts, stimulus.CurrentEvents} {
		for _, fact := range group {
			catalog.evidence[fact.ID], catalog.evidence[fact.Source] = true, true
		}
	}
	for _, actor := range before.Actors {
		for _, holding := range actor.Resources {
			add(holding.EvidenceRefs)
			add(holding.Perception.EvidenceRefs)
			if holding.Perception.Kind != "unaware" {
				add(CharacterSourceRefsV2(actor.AgentID, holding.EvidenceRefs))
				add(CharacterSourceRefsV2(actor.AgentID, holding.Perception.EvidenceRefs))
			}
		}
	}
	for _, proposal := range proposals {
		add(proposal.KnowledgeRefs)
		for _, estimate := range proposal.ResourceEstimates {
			add(estimate.EvidenceRefs)
		}
		for _, report := range proposal.ResourceReports {
			add(report.EvidenceRefs)
		}
		for _, communication := range proposal.Communications {
			add(communication.KnowledgeRefs)
		}
	}
	for _, mechanism := range stimulus.Mechanisms {
		catalog.mechanisms[mechanism.ID] = mechanism.ID != ""
	}
	return catalog
}

func (c arbitrationReferenceCatalogV2) usedSettlementMechanisms(receipt WorldArbitrationReceipt, proposals map[string]CharacterDecisionProposal) map[string]bool {
	used := map[string]bool{}
	for _, resolution := range receipt.Resolutions {
		proposal, bound := proposals[resolution.AgentID]
		if !bound || proposal.Digest != resolution.ProposalDigest {
			continue
		}
		for _, ref := range resolution.MechanismRefs {
			if c.mechanisms[ref] {
				used[ref] = true
			}
		}
	}
	return used
}

func resourceReportContainsFieldV2(sender CharacterDecisionProposal, resourceID, targetCharacter, field string) bool {
	for _, report := range sender.ResourceReports {
		if report.ResourceID == resourceID && report.ToCharacter == targetCharacter &&
			((field == "name" && report.PerceivedName != "") || (field == "unit" && report.PerceivedUnit != "") || (field == "amount" && report.Amount != nil)) {
			return true
		}
	}
	return false
}

const maxArbitrationStaticReferenceIssues = 8
const maxArbitrationStaticReferenceErrorBytes = 2048

type arbitrationStaticReferenceErrors struct {
	issues    []string
	bytes     int
	truncated bool
}

func (e *arbitrationStaticReferenceErrors) Error() string {
	text := "static arbitration reference checks: " + strings.Join(e.issues, "; ")
	if e.truncated {
		text += "; further static reference issues omitted"
	}
	return text
}

func (e *arbitrationStaticReferenceErrors) add(message string) {
	if e.truncated {
		return
	}
	// Reserve room for the fixed prefix/separators/omission marker.
	if len(e.issues) >= maxArbitrationStaticReferenceIssues || e.bytes+len(message)+2 > maxArbitrationStaticReferenceErrorBytes-128 {
		e.truncated = true
		return
	}
	e.issues = append(e.issues, message)
	e.bytes += len(message) + 2
}

func arbitrationReferenceLabel(ref string) string {
	if len(ref) == 0 || len(ref) > 128 {
		return "<non-identifier reference>"
	}
	for _, r := range ref {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && !strings.ContainsRune("_:./@+-=", r) {
			return "<non-identifier reference>"
		}
	}
	return fmt.Sprintf("%q", ref)
}

// PrecheckWorldArbitrationStaticReferencesV2 combines only independently
// provable reference/report-field failures. It is NOT a receipt validator:
// callers must still run FinalizeWorldArbitrationReceipt after it succeeds.
// Dynamic access, discovery, received facts and numerical perception remain
// exclusively under the original full kernel and are not rejected here.
func PrecheckWorldArbitrationStaticReferencesV2(receipt WorldArbitrationReceipt, stimulus WorldStimulusPacket, proposals []CharacterDecisionProposal) error {
	if receipt.Version != WorldArbitrationReceiptV2Version || stimulus.Version != WorldStimulusPacketV2Version || stimulus.PhysicalState == nil {
		return nil // Cannot establish this static directory; leave the original gate in charge.
	}
	before, err := FinalizeWorldPhysicalStateV2(*stimulus.PhysicalState)
	if err != nil {
		return nil
	}
	catalog := newArbitrationReferenceCatalogV2(stimulus, before, normalizeV2Strings(receipt.ProposalDigests), proposals)
	byAgent := map[string]CharacterDecisionProposal{}
	for _, proposal := range proposals {
		byAgent[proposal.AgentID] = proposal
	}
	// The upcoming receipt finalizer trims these fields before the kernel;
	// reflect that normalization without changing the caller's receipt/slices.
	preview := receipt
	preview.Resolutions = append([]CharacterDecisionResolution(nil), receipt.Resolutions...)
	for i := range preview.Resolutions {
		preview.Resolutions[i].MechanismRefs = normalizeV2Strings(preview.Resolutions[i].MechanismRefs)
	}
	used := catalog.usedSettlementMechanisms(preview, byAgent)
	issues := &arbitrationStaticReferenceErrors{}
	checkRefs := func(path string, refs []string, settlement bool) {
		if len(normalizeV2Strings(refs)) == 0 {
			issues.add(path + ": bound evidence references are required")
			return
		}
		for index, ref := range refs {
			if issues.truncated {
				return
			}
			if ref != "" && (catalog.evidence[ref] || (settlement && used[ref])) {
				continue
			}
			if settlement {
				issues.add(fmt.Sprintf("%s[%d]: unbound %s; use bound source/proposal evidence or a world mechanism actually named in resolution.mechanism_refs", path, index, arbitrationReferenceLabel(ref)))
			} else {
				issues.add(fmt.Sprintf("%s[%d]: unbound %s; use bound source/proposal evidence, not a communication identifier or a settlement-only mechanism", path, index, arbitrationReferenceLabel(ref)))
			}
		}
	}
	for index, settlement := range receipt.ResourceSettlements {
		if issues.truncated {
			break
		}
		checkRefs(fmt.Sprintf("resource_settlements[%d].evidence_refs", index), settlement.EvidenceRefs, true)
	}
	characters := map[string]string{}
	for _, actor := range before.Actors {
		characters[actor.AgentID] = actor.Character
	}
	for index, delivery := range receipt.ResourceDeliveries {
		if issues.truncated {
			break
		}
		checkRefs(fmt.Sprintf("resource_deliveries[%d].evidence_refs", index), delivery.EvidenceRefs, false)
		sender, exists := byAgent[delivery.FromAgentID]
		target, known := characters[delivery.ToAgentID]
		if !exists || !known || sender.Digest != delivery.SourceProposalDigest {
			continue // An unresolved source identity cannot prove its report's capabilities.
		}
		var declared []string
		for _, field := range []string{"name", "unit", "amount"} {
			if resourceReportContainsFieldV2(sender, delivery.ResourceID, target, field) {
				declared = append(declared, field)
			}
		}
		for fieldIndex, field := range delivery.ReceivedFields {
			if issues.truncated {
				break
			}
			if field != "name" && field != "unit" && field != "amount" {
				continue // Preserve the original unsupported-field error.
			}
			if !resourceReportContainsFieldV2(sender, delivery.ResourceID, target, field) {
				issues.add(fmt.Sprintf("resource_deliveries[%d].received_fields[%d]: %s was not sent in the matching source resource_report; declared field candidates=[%s], not proof of delivery", index, fieldIndex, field, strings.Join(declared, ",")))
			}
		}
	}
	if len(issues.issues) == 0 {
		return nil
	}
	return issues
}
