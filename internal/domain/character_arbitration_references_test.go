package domain

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestArbitrationStaticReferencesReportsC4ErrorsTogetherWithoutMutation(t *testing.T) {
	f := newPhysicalProtocolFixture(t)
	f.stimulus.Mechanisms = append(f.stimulus.Mechanisms, CodexMechanism{ID: "M_TIME", Name: "时间", Visibility: "formal"})
	rebindPhysicalTestStimulus(t, &f)
	f.receipt.Resolutions[0].MechanismRefs = []string{"M_TIME"}
	settlePhysicalFuel(&f)
	f.receipt.ResourceSettlements[0].EvidenceRefs = []string{"sha256:" + strings.Repeat("f", 64)} // A view/integrity digest is not a source.
	f.receipt.ResourceDeliveries = []ResourceDeliveryV2{{ResourceID: physicalFuelTestID, FromAgentID: f.proposals[0].AgentID, ToAgentID: f.proposals[1].AgentID,
		SourceProposalDigest: f.proposals[0].Digest, Access: "shared", ReceivedFields: []string{"name"}, EvidenceRefs: []string{"identify_supporting_record", "M_TIME"}}}
	before, _ := json.Marshal(f.receipt)
	err := PrecheckWorldArbitrationStaticReferencesV2(f.receipt, f.stimulus, f.proposals)
	var combined *arbitrationStaticReferenceErrors
	if !errors.As(err, &combined) || len(combined.issues) != 4 {
		t.Fatalf("four independent errors were not combined: %v", err)
	}
	for _, path := range []string{"resource_settlements[0].evidence_refs[0]", "resource_deliveries[0].evidence_refs[0]", "resource_deliveries[0].evidence_refs[1]", "resource_deliveries[0].received_fields[0]"} {
		if !strings.Contains(err.Error(), path) {
			t.Fatalf("missing actionable source path %s: %v", path, err)
		}
	}
	after, _ := json.Marshal(f.receipt)
	if string(before) != string(after) {
		t.Fatal("precheck changed a proposed world result")
	}
	if _, err := finalizePhysicalFixture(f); err == nil {
		t.Fatal("the unchanged final kernel no longer rejects these invalid references")
	}
}

func TestArbitrationStaticReferencesKeepLegalMechanismAndOrdinaryEvidenceDomains(t *testing.T) {
	f := newPhysicalProtocolFixture(t)
	f.stimulus.Mechanisms = append(f.stimulus.Mechanisms, CodexMechanism{ID: "M_TIME", Name: "时间", Visibility: "formal"})
	rebindPhysicalTestStimulus(t, &f)
	settlePhysicalFuel(&f)
	f.receipt.ResourceSettlements[0].EvidenceRefs = []string{"M_TIME"}
	// FinalizeWorldArbitrationReceipt normalizes the resolution before the
	// kernel. The precheck must not reject this valid pre-finalized spelling.
	f.receipt.Resolutions[0].MechanismRefs = []string{" M_TIME "}
	before, _ := json.Marshal(f.receipt)
	if err := PrecheckWorldArbitrationStaticReferencesV2(f.receipt, f.stimulus, f.proposals); err != nil {
		t.Fatalf("valid used mechanism was rejected: %v", err)
	}
	after, _ := json.Marshal(f.receipt)
	if string(before) != string(after) {
		t.Fatal("static preview normalized caller-owned resolution refs in place")
	}
	receipt, err := finalizePhysicalFixture(f)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(receipt)
	if err := PrecheckWorldArbitrationStaticReferencesV2(receipt, f.stimulus, f.proposals); err != nil {
		t.Fatal(err)
	}
	verified, err := FinalizeWorldArbitrationReceipt(receipt, f.stimulus, f.activation, f.proposals, 1)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, _ := json.Marshal(verified)
	if string(raw) != string(reencoded) || receipt.Digest != verified.Digest {
		t.Fatal("reference precheck changed an already valid receipt/hash")
	}
	// M_TIME may also independently be an ordinary fact ID. Do not implement
	// a blanket mechanism-name ban that shrinks the kernel's valid set.
	f.stimulus.PublicFacts = []CharacterAgentFact{{ID: "M_TIME", Kind: "public", Text: "独立来源恰好使用此ID"}}
	rebindPhysicalTestStimulus(t, &f)
	settlePhysicalFuel(&f)
	f.receipt.ResourceDeliveries = []ResourceDeliveryV2{{ResourceID: physicalFuelTestID, FromAgentID: f.proposals[0].AgentID, ToAgentID: f.proposals[1].AgentID,
		SourceProposalDigest: f.proposals[0].Digest, Access: "none", EvidenceRefs: []string{"M_TIME"}}}
	if err := PrecheckWorldArbitrationStaticReferencesV2(f.receipt, f.stimulus, f.proposals); err != nil {
		t.Fatalf("ordinary fact source was confused with a settlement-only mechanism: %v", err)
	}
	if _, err := finalizePhysicalFixture(f); err != nil {
		t.Fatal(err)
	}
}

func TestArbitrationStaticReferencesAllowExactReportsAndDoNotAuthorizeDynamicResults(t *testing.T) {
	f := newPhysicalProtocolFixture(t)
	f.proposals[0].ResourceReports = []ResourceReportV2{{ResourceID: physicalFuelTestID, ToCharacter: f.proposals[1].Character, PerceivedName: "燃油", EvidenceRefs: []string{"known-ca_a"}}}
	rebindPhysicalTestProposals(t, &f)
	settlePhysicalFuel(&f)
	f.receipt.ResourceDeliveries = []ResourceDeliveryV2{{ResourceID: physicalFuelTestID, FromAgentID: f.proposals[0].AgentID, ToAgentID: f.proposals[1].AgentID,
		SourceProposalDigest: f.proposals[0].Digest, Access: "none", ReceivedFields: []string{"name"}, EvidenceRefs: []string{f.proposals[0].Digest}}}
	if err := PrecheckWorldArbitrationStaticReferencesV2(f.receipt, f.stimulus, f.proposals); err != nil {
		t.Fatal(err)
	}
	if _, err := finalizePhysicalFixture(f); err != nil {
		t.Fatal(err)
	}
	// Reference validity does not prove a grant, a measurement or conservation.
	f.receipt.ResourceSettlements[0].After = physicalTestNumber(1000)
	if err := PrecheckWorldArbitrationStaticReferencesV2(f.receipt, f.stimulus, f.proposals); err != nil {
		t.Fatal("static precheck attempted to decide a numerical outcome")
	}
	if _, err := finalizePhysicalFixture(f); err == nil {
		t.Fatal("full physics validation was bypassed after static success")
	}
}

func TestArbitrationStaticReferenceDirectoryMatchesLegacyUnionExactly(t *testing.T) {
	f := newPhysicalProtocolFixture(t)
	f.stimulus.Sources = []string{" root ", ""}
	f.stimulus.PublicFacts = []CharacterAgentFact{{ID: "public", Source: "original-public-source"}}
	f.stimulus.CurrentEvents = []CharacterAgentFact{{ID: "event", Source: ""}}
	f.proposals[0].ResourceEstimates = []ResourceEstimateV2{{EvidenceRefs: []string{"estimate-source"}}}
	f.proposals[0].ResourceReports = []ResourceReportV2{{EvidenceRefs: []string{"report-source"}}}
	f.proposals[0].Communications = []CharacterCommunicationV2{{ID: "not-evidence", KnowledgeRefs: []string{"message-knowledge"}}}
	before, err := FinalizeWorldPhysicalStateV2(*f.stimulus.PhysicalState)
	if err != nil {
		t.Fatal(err)
	}
	legacy := map[string]bool{f.stimulus.Digest: true}
	add := func(refs []string) {
		for _, ref := range refs {
			if strings.TrimSpace(ref) != "" {
				legacy[ref] = true
			}
		}
	}
	add(f.stimulus.Sources)
	add(f.receipt.ProposalDigests)
	for _, group := range [][]CharacterAgentFact{f.stimulus.PublicFacts, f.stimulus.CurrentEvents} {
		for _, fact := range group {
			legacy[fact.ID], legacy[fact.Source] = true, true
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
	for _, p := range f.proposals {
		add(p.KnowledgeRefs)
		for _, e := range p.ResourceEstimates {
			add(e.EvidenceRefs)
		}
		for _, r := range p.ResourceReports {
			add(r.EvidenceRefs)
		}
		for _, c := range p.Communications {
			add(c.KnowledgeRefs)
		}
	}
	current := newArbitrationReferenceCatalogV2(f.stimulus, before, f.receipt.ProposalDigests, f.proposals)
	if !reflect.DeepEqual(legacy, current.evidence) || current.evidence["not-evidence"] {
		t.Fatal("directory extraction changed the kernel's exact source union")
	}
}

func TestArbitrationStaticReferencesAreBoundedAndNeverEchoNonIdentifierText(t *testing.T) {
	f := newPhysicalProtocolFixture(t)
	for range 100 {
		f.receipt.ResourceSettlements = append(f.receipt.ResourceSettlements, ResourceSettlementV2{ResourceID: physicalFuelTestID, EvidenceRefs: []string{strings.Repeat("PRIVATE_REASONING ", 1000)}})
	}
	err := PrecheckWorldArbitrationStaticReferencesV2(f.receipt, f.stimulus, f.proposals)
	var combined *arbitrationStaticReferenceErrors
	if !errors.As(err, &combined) || len(combined.issues) > maxArbitrationStaticReferenceIssues || len(err.Error()) > maxArbitrationStaticReferenceErrorBytes || !combined.truncated || strings.Contains(err.Error(), "PRIVATE_REASONING") {
		t.Fatalf("unbounded or private static feedback: %v", err)
	}
	legacy := f.receipt
	legacy.Version = WorldArbitrationReceiptVersion
	if err := PrecheckWorldArbitrationStaticReferencesV2(legacy, f.stimulus, f.proposals); err != nil {
		t.Fatal("legacy receipt was subjected to v2 static rules")
	}
	unknown := f.stimulus
	unknown.PhysicalState = nil
	if err := PrecheckWorldArbitrationStaticReferencesV2(f.receipt, unknown, f.proposals); err != nil {
		t.Fatal("missing authoritative inputs were used to guess static permissions")
	}
}
