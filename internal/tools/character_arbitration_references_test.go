package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
)

func arbitrationReferenceFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		files[path] = string(raw)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return files
}

func TestWorldToolCombinesStaticReferenceFailuresBeforeAnyWrite(t *testing.T) {
	st, session, cycle, proofs := activationToolFixture(t, true)
	e := cycle.Evidence
	tool, err := NewResolveCharacterActivationTool(st, session, e.Stimulus, e.Activation, e.Proposals, e.ProtocolDigest, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	valid := activationArbiterArgs(t, cycle)
	var args map[string]json.RawMessage
	_ = json.Unmarshal(valid, &args)
	settlements := append([]domain.ResourceSettlementV2(nil), e.Arbitrations[0].ResourceSettlements...)
	settlements[0].EvidenceRefs = []string{"sha256:" + strings.Repeat("f", 64)}
	args["resource_settlements"], _ = json.Marshal(settlements)
	p := e.Proposals[0]
	args["resource_deliveries"], _ = json.Marshal([]domain.ResourceDeliveryV2{{ResourceID: settlements[0].ResourceID, FromAgentID: p.AgentID, ToAgentID: p.AgentID,
		SourceProposalDigest: p.Digest, Access: "none", ReceivedFields: []string{"name"}, EvidenceRefs: []string{"identify_supporting_record", "M_TIME"}}})
	raw, _ := json.Marshal(args)
	before := arbitrationReferenceFiles(t, st.Dir())
	_, err = tool.Execute(context.Background(), raw)
	if !errors.Is(err, errs.ErrToolPrecondition) {
		t.Fatalf("static failures lost precondition classification: %v", err)
	}
	for _, path := range []string{"resource_settlements[0].evidence_refs[0]", "resource_deliveries[0].evidence_refs[0]", "resource_deliveries[0].evidence_refs[1]", "resource_deliveries[0].received_fields[0]"} {
		if !strings.Contains(err.Error(), path) {
			t.Fatalf("missing same-call feedback for %s: %v", path, err)
		}
	}
	if !reflect.DeepEqual(before, arbitrationReferenceFiles(t, st.Dir())) {
		t.Fatal("rejected preflight changed proposal, arbitration, checkpoint or other source files")
	}
	if receipt, err := proofs.LoadArbitration(e.GenerationID, e.Chapter, 1); err != nil || receipt != nil {
		t.Fatalf("precheck published a rejected arbitration: %v", err)
	}
	if _, err := tool.Execute(context.Background(), valid); err != nil {
		t.Fatalf("original legal tool call no longer succeeds: %v", err)
	}
	receipt, err := proofs.LoadArbitration(e.GenerationID, e.Chapter, 1)
	if err != nil || receipt == nil {
		t.Fatalf("legal arbitration was not persisted: %v", err)
	}
	expected := e.Arbitrations[0]
	expected.GeneratedAt = receipt.GeneratedAt
	expected, err = domain.FinalizeWorldArbitrationReceipt(expected, e.Stimulus, e.Activation, e.Proposals, 1)
	if err != nil || expected.Digest != receipt.Digest {
		t.Fatalf("legal receipt hash changed after the new precheck: %v", err)
	}
}
