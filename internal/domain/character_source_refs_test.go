package domain

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestCharacterSourceRefsV2HideBothProvenanceLayersWithoutMutatingWorld(t *testing.T) {
	f := newPhysicalProtocolFixture(t)
	secret := "world_codex.history: hidden record R02 says sixty litres; future_outline.chapter9"
	state := *f.stimulus.PhysicalState
	state.Actors[0].Resources[0].EvidenceRefs = []string{secret}
	state.Actors[0].Resources[0].Perception.EvidenceRefs = []string{secret + " measured only by another actor"}
	before, _ := json.Marshal(state)
	views, err := BuildCharacterResourceViewsV2(state, "ca_a")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(views)
	if bytes.Contains(raw, []byte("hidden record")) || bytes.Contains(raw, []byte("future_outline")) || bytes.Contains(raw, []byte("actual_amount")) {
		t.Fatalf("view exposed an author-only source or balance: %s", raw)
	}
	for _, v := range views {
		for _, ref := range append(append([]string(nil), v.EvidenceRefs...), v.Perception.EvidenceRefs...) {
			if !IsCharacterSourceRefV2(ref) {
				t.Fatalf("view provenance is not opaque: %q", ref)
			}
		}
	}
	after, _ := json.Marshal(state)
	if !bytes.Equal(before, after) {
		t.Fatal("view projection mutated canonical author sources")
	}
	var restored WorldPhysicalStateV2
	if err := json.Unmarshal(before, &restored); err != nil {
		t.Fatal(err)
	}
	replayed, err := BuildCharacterResourceViewsV2(restored, "ca_a")
	if err != nil || !samePhysicalValueV2(views, replayed) {
		t.Fatal("source handles changed after host state restart")
	}
	handle := CharacterSourceRefV2("ca_a", secret)
	if handle == CharacterSourceRefV2("ca_b", secret) || CharacterSourceRefV2("ca_a", handle) != handle {
		t.Fatal("source handles lost actor binding or were double-wrapped")
	}
}

func TestCharacterSourcePolicyV2RejectsEveryRawObservationProvenancePath(t *testing.T) {
	f := newPhysicalProtocolFixture(t)
	base := f.observations[0]
	base.Sources = []string{CharacterSourceRefPolicyV2}
	secret := "private source: unreceived R02 amount sixty"
	for _, tc := range []struct {
		name string
		edit func(*CharacterObservationPacket)
	}{
		{"holding", func(p *CharacterObservationPacket) { p.ResourceViews[0].EvidenceRefs = []string{secret} }},
		{"perception", func(p *CharacterObservationPacket) { p.ResourceViews[0].Perception.EvidenceRefs = []string{secret} }},
		{"sources", func(p *CharacterObservationPacket) { p.Sources = append(p.Sources, secret) }},
		{"whitespace_policy_cannot_bypass_guard", func(p *CharacterObservationPacket) {
			p.Sources = []string{" " + CharacterSourceRefPolicyV2 + " "}
			p.ResourceViews[0].EvidenceRefs = []string{secret}
		}},
		{"known_source", func(p *CharacterObservationPacket) { p.KnownFacts[0].Source = secret }},
		{"event_source", func(p *CharacterObservationPacket) {
			p.PerceivedEvents = []CharacterAgentFact{{ID: "event", Text: "visible event", Source: secret}}
		}},
		{"rule_source", func(p *CharacterObservationPacket) {
			p.PublicRules = []CharacterAgentFact{{ID: "rule", Text: "public rule", Source: secret}}
		}},
		{"accepted_memory_refs", func(p *CharacterObservationPacket) {
			p.Memory = []CharacterAgentMemoryFact{{ID: "mem_accepted", Text: "safe accepted text", SourceDigest: CharacterSourceRefV2(p.AgentID, "accepted-receipt"), KnowledgeRefs: []string{secret}, Accepted: true}}
		}},
		{"accepted_memory_source", func(p *CharacterObservationPacket) {
			p.Memory = []CharacterAgentMemoryFact{{ID: "mem_accepted", Text: "safe accepted text", SourceDigest: secret, Accepted: true}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := json.Marshal(base)
			var p CharacterObservationPacket
			_ = json.Unmarshal(raw, &p)
			tc.edit(&p)
			if _, err := FinalizeCharacterObservationPacket(p); err == nil || !strings.Contains(err.Error(), "opaque character source") || strings.Contains(err.Error(), secret) {
				t.Fatalf("raw provenance was accepted or echoed by policy error: %v", err)
			}
		})
	}
	base.Memory = []CharacterAgentMemoryFact{{ID: "mem_ok", Text: "safe accepted fact", SourceDigest: "sha256:" + strings.Repeat("a", 64), KnowledgeRefs: CharacterSourceRefsV2(base.AgentID, []string{"old raw provenance"}), Accepted: true}}
	if _, err := FinalizeCharacterObservationPacket(base); err != nil {
		t.Fatalf("opaque accepted-memory projection rejected: %v", err)
	}
}

func TestCharacterSourceRefsV2AuthorizeOwnIntentNotRawOrForeignHandles(t *testing.T) {
	f := newPhysicalProtocolFixture(t)
	observation := f.observations[1]
	observation.Sources = []string{CharacterSourceRefPolicyV2}
	var err error
	observation, err = FinalizeCharacterObservationPacket(observation)
	if err != nil {
		t.Fatal(err)
	}
	owned := CharacterSourceRefV2("ca_b", "seed-b")
	for _, tc := range []struct {
		ref string
		ok  bool
	}{
		{owned, true}, {"seed-b", false}, {CharacterSourceRefV2("ca_a", "seed-b"), false}, {CharacterSourceRefV2("ca_a", "seed-a"), false},
	} {
		p := f.proposals[1]
		p.ObservationDigest = observation.Digest
		p.KnowledgeRefs = []string{tc.ref}
		p.ResourceEstimates = []ResourceEstimateV2{{ResourceID: physicalFuelTestID, EstimateMin: physicalTestNumber(8), EstimateMax: physicalTestNumber(9), EvidenceRefs: []string{tc.ref}}}
		p.ResourceReports = []ResourceReportV2{{ResourceID: physicalFuelTestID, ToCharacter: "甲", PerceivedUnit: "L", Amount: physicalTestNumber(8), EvidenceRefs: []string{tc.ref}}}
		p.ResourceMeasurements = []ResourceMeasurementV2{{ResourceID: physicalFuelTestID, MechanismRef: "gauge"}}
		p.MechanismRefs = []string{"gauge"}
		_, err := FinalizeCharacterDecisionProposal(p, observation)
		if (err == nil) != tc.ok {
			t.Fatalf("owned=%t source/estimate/report/measurement validation: %v", tc.ok, err)
		}
	}
	if _, known := observation.AllowedFactIDs()["seed-b"]; known {
		t.Fatal("opaque source handle was expanded into raw author knowledge")
	}
}

func TestCharacterSourceRefsV2ArbitrationAliasesDoNotUpgradeUnknownTruth(t *testing.T) {
	f := newPhysicalProtocolFixture(t)
	settlePhysicalFuel(&f)
	// The arbiter may preserve a visible perception using exactly the safe
	// representation seen by its owner, without inventing another estimate.
	f.receipt.Resolutions[0].PostState.Resources[0].Perception = f.observations[0].ResourceViews[0].Perception
	f.receipt.ResourceSettlements[0].EvidenceRefs = CharacterSourceRefsV2("ca_a", []string{"seed-a"})
	r, err := finalizePhysicalFixture(f)
	if err != nil {
		t.Fatalf("authenticated view alias rejected: %v", err)
	}
	after, err := ApplyArbitrationPhysicalStateV2(r, f.stimulus, f.proposals...)
	if err != nil || *after.Resources[0].ActualAmount != 11.8 || *after.Actors[0].Resources[0].Perception.Amount != 12 || after.Actors[1].Resources[0].Perception.Amount != nil {
		t.Fatalf("source alias changed truth/knowledge boundary: %v", err)
	}
	f.receipt.Resolutions[1].PostState.Resources[0].Perception = ResourcePerceptionV2{Kind: "estimated", EstimateMin: physicalTestNumber(11.8), EstimateMax: physicalTestNumber(11.8), AsOfChapter: 1, EvidenceRefs: []string{f.proposals[1].Digest, CharacterSourceRefV2("ca_b", "seed-b")}}
	if _, err := finalizePhysicalFixture(f); err == nil {
		t.Fatal("opaque provenance was used as authority to invent a world-informed estimate")
	}
	f = newPhysicalProtocolFixture(t)
	f.receipt.Resolutions[0].PostState.Resources[0].Perception = ResourcePerceptionV2{Kind: "estimated", Amount: physicalTestNumber(12), EvidenceRefs: CharacterSourceRefsV2("ca_a", []string{"seed-a"})}
	if _, err := finalizePhysicalFixture(f); err != nil {
		t.Fatalf("conservative downgrade of remembered reading lost its authenticated source: %v", err)
	}
}

func TestCharacterSourceRefsV2AcceptedMemoryNeverExpandsActorProvenance(t *testing.T) {
	f := newPhysicalProtocolFixture(t)
	f.proposals[0].KnowledgeRefs = CharacterSourceRefsV2("ca_a", []string{"seed-a"})
	rebindPhysicalTestProposals(t, &f)
	receipt, err := finalizePhysicalFixture(f)
	if err != nil {
		t.Fatal(err)
	}
	after, err := ApplyArbitrationPhysicalStateV2(receipt, f.stimulus, f.proposals...)
	if err != nil {
		t.Fatal(err)
	}
	text, err := CharacterPrivateOutcomeV2(f.proposals[0], receipt.Resolutions[0], after)
	if err != nil {
		t.Fatal(err)
	}
	memory, err := FinalizeCharacterAgentMemory(CharacterAgentMemory{AgentID: "ca_a", Character: "甲", State: "canonical", Facts: []CharacterAgentMemoryFact{{ID: "mem_accepted", Chapter: 1, Kind: "accepted_decision_outcome", Text: text, SourceDigest: receipt.Digest, KnowledgeRefs: f.proposals[0].KnowledgeRefs, Accepted: true}}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(memory)
	if bytes.Contains(raw, []byte("seed-a")) || bytes.Contains(raw, []byte("秘密真值只属于裁判")) || !IsCharacterSourceRefV2(memory.Facts[0].KnowledgeRefs[0]) {
		t.Fatal("accepted private memory reintroduced raw provenance or arbiter prose")
	}
	observation := f.observations[0]
	observation.Sources = []string{CharacterSourceRefPolicyV2}
	observation.Memory = memory.Facts
	if _, err := FinalizeCharacterObservationPacket(observation); err != nil {
		t.Fatalf("accepted opaque provenance did not survive owner observation: %v", err)
	}
}

func TestCharacterSourcePolicyV2LegacySealedRawEvidenceRoundTripsUnchanged(t *testing.T) {
	for _, opaque := range []bool{false, true} {
		evidence := characterSourceEvidenceFixtureForTest(t, opaque)
		raw, err := json.Marshal(evidence)
		if err != nil {
			t.Fatal(err)
		}
		var restored CharacterAgentEvidenceBundle
		if err := json.Unmarshal(raw, &restored); err != nil {
			t.Fatal(err)
		}
		if err := ValidateCharacterAgentEvidenceBundle(restored); err != nil {
			t.Fatalf("stored opaque=%t v2 evidence no longer verifies: %v", opaque, err)
		}
		reencoded, _ := json.Marshal(restored)
		if !bytes.Equal(raw, reencoded) || restored.EvidenceRoot != evidence.EvidenceRoot {
			t.Fatal("source-policy compatibility rewrote stored evidence")
		}
		if opaque {
			// Rehashing the bad observation cannot bypass the new policy.
			restored.Observations[0].ResourceViews[0].EvidenceRefs = []string{"seed-a"}
			restored.Observations[0].Digest, _ = ComputeCharacterObservationDigest(restored.Observations[0])
			if _, err := FinalizeCharacterAgentEvidenceBundle(restored); err == nil || !strings.Contains(err.Error(), "opaque character source") {
				t.Fatalf("new source policy fell back to historical raw refs: %v", err)
			}
		}
	}
}

func characterSourceEvidenceFixtureForTest(t *testing.T, opaque bool) CharacterAgentEvidenceBundle {
	t.Helper()
	f := newPhysicalProtocolFixture(t)
	registry, err := FinalizeCharacterAgentRegistry(CharacterAgentRegistry{Entries: []CharacterAgentRecord{{AgentID: "ca_a", Character: "甲", Tier: "core", Status: CharacterAgentActive, MemoryVersion: 1}, {AgentID: "ca_b", Character: "乙", Tier: "core", Status: CharacterAgentActive, MemoryVersion: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	f.activation.RegistryRoot = registry.RegistryRoot
	if opaque {
		f.stimulus.Sources = []string{CharacterSourceRefPolicyV2}
	}
	f.stimulus, err = FinalizeWorldStimulusPacket(f.stimulus)
	if err != nil {
		t.Fatal(err)
	}
	f.receipt.StimulusDigest = f.stimulus.Digest
	for i := range f.observations {
		p := &f.observations[i]
		p.StimulusDigest = f.stimulus.Digest
		p.MemoryRoot = "sha256:memory-" + p.AgentID
		if opaque {
			p.Sources = []string{CharacterSourceRefPolicyV2}
		}
		p.ResourceViews, err = buildCharacterResourceViewsV2(*f.stimulus.PhysicalState, p.AgentID, opaque)
		if err != nil {
			t.Fatal(err)
		}
		*p, err = FinalizeCharacterObservationPacket(*p)
		if err != nil {
			t.Fatal(err)
		}
		f.proposals[i].ObservationDigest = p.Digest
		f.activation.Entries[i].ObservationDigest = p.Digest
	}
	f.activation, err = FinalizeCharacterAgentActivation(f.activation)
	if err != nil {
		t.Fatal(err)
	}
	f.receipt.ActivationDigest = f.activation.Digest
	rebindPhysicalTestProposals(t, &f)
	receipt, err := finalizePhysicalFixture(f)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := FinalizeCharacterAgentEvidenceBundle(CharacterAgentEvidenceBundle{GenerationID: f.stimulus.GenerationID, Chapter: 1, Registry: registry, Stimulus: f.stimulus, Activation: f.activation, Observations: f.observations, Proposals: f.proposals, Arbitrations: []WorldArbitrationReceipt{receipt}, MemoryRoots: []string{f.observations[0].MemoryRoot, f.observations[1].MemoryRoot}, ProtocolDigest: "sha256:" + strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	return evidence
}
