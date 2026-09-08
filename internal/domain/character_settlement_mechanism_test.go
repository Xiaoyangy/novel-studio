package domain

import (
	"strings"
	"testing"
)

func settlementMechanismFixture(t *testing.T) physicalProtocolFixture {
	t.Helper()
	f := newPhysicalProtocolFixture(t)
	f.stimulus.Mechanisms = append(f.stimulus.Mechanisms, CodexMechanism{ID: "M_RESOURCE", Name: "资源分账消耗", Visibility: "formal"})
	rebindPhysicalTestStimulus(t, &f)
	f.receipt.Resolutions[0].MechanismRefs = []string{"M_RESOURCE"}
	settlePhysicalFuel(&f)
	f.receipt.ResourceSettlements[0].EvidenceRefs = []string{"M_RESOURCE"}
	return f
}

func TestResourceSettlementAcceptsOnlyExistingInvokedWorldMechanism(t *testing.T) {
	f := settlementMechanismFixture(t)
	receipt, err := finalizePhysicalFixture(f)
	if err != nil {
		t.Fatalf("the documented actual-mechanism settlement source was rejected: %v", err)
	}
	state, err := ApplyArbitrationPhysicalStateV2(receipt, f.stimulus, f.proposals...)
	if err != nil {
		t.Fatal(err)
	}
	if *state.Resources[0].ActualAmount != 11.8 || *state.Actors[0].Resources[0].Perception.Amount != 12 {
		t.Fatal("world settlement was lost or refreshed unmeasured private knowledge")
	}
	replayed, err := FinalizeWorldArbitrationReceipt(receipt, f.stimulus, f.activation, f.proposals, 1)
	if err != nil || replayed.Digest != receipt.Digest {
		t.Fatal("mechanism settlement replay changed the original receipt")
	}
	for _, mode := range []string{"unknown", "known-but-unused", "wrong-balance", "actor-knowledge"} {
		t.Run(mode, func(t *testing.T) {
			bad := settlementMechanismFixture(t)
			switch mode {
			case "unknown":
				bad.receipt.ResourceSettlements[0].EvidenceRefs = []string{"M_NOT_IN_WORLD"}
			case "known-but-unused":
				bad.receipt.Resolutions[0].MechanismRefs = nil
			case "wrong-balance":
				bad.receipt.ResourceSettlements[0].After = physicalTestNumber(999)
			case "actor-knowledge":
				p := &bad.receipt.Resolutions[0].PostState.Resources[0].Perception
				p.Amount, p.AsOfChapter, p.EvidenceRefs = physicalTestNumber(11.8), 1, []string{"M_RESOURCE"}
			}
			_, err := finalizePhysicalFixture(bad)
			if err == nil {
				t.Fatal("unbound mechanism changed world/actor state")
			}
			if mode == "actor-knowledge" && !strings.Contains(err.Error(), "perception.evidence_refs") {
				t.Fatalf("private-knowledge rejection lost its exact field: %v", err)
			}
		})
	}
}
