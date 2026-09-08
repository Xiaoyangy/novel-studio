package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func legacyOutlineAllStructurePhaseFixture(t *testing.T) (*store.Store, *store.Store, *domain.OutlineAllExecutionReceipt) {
	t.Helper()
	live := store.NewStore(outlineAllGateLiveDir(t))
	candidate := store.NewStore(outlineAllGateLiveDir(t))
	receipt := writeOutlineAllGateCompleteReceipt(t, live.Dir(), candidate.Dir(), outlineAllGateDigest)
	progress, err := live.Progress.Load()
	if err != nil {
		t.Fatal(err)
	}
	progress.Phase = domain.PhaseInit
	progress.Layered = true
	if err := live.Progress.Save(progress); err != nil {
		t.Fatal(err)
	}
	receipt.ProtectedCanonRoot, err = pipelineOutlineAllProtectedCanonRoot(live.Dir())
	if err != nil {
		t.Fatal(err)
	}
	receipt.StableProgressRoot, err = pipelineOutlineAllStableProgressRoot(live.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if err := copyProjectAllWorkspace(live.Dir(), candidate.Dir()); err != nil {
		t.Fatal(err)
	}
	before, err := live.Outline.LoadLayeredOutline()
	if err != nil {
		t.Fatal(err)
	}
	skeleton := []domain.VolumeOutline{{Index: 1, Title: "Whole book", Theme: "an irreversible outcome", Arcs: []domain.ArcOutline{{Index: 1, Title: "resolve the obligation", Goal: "complete the decision", EstimatedChapters: 8}}}}
	if err := candidate.Outline.SaveLayeredOutline(skeleton); err != nil {
		t.Fatal(err)
	}
	flatDigest, err := repairPipelineOutlineAllDerivedArtifacts(candidate, skeleton)
	if err != nil {
		t.Fatal(err)
	}
	action := domain.OutlineAllPendingAction{Type: domain.OutlineAllActionPlanStructure, Operation: 1, BeforeLayeredDigest: pipelineOutlineAllLayeredDigest(before)}
	intent, err := signPipelineOutlineAllOperationIntent(pipelineOutlineAllOperationIntent{
		Version: "outline-all-operation-intent.v1", AttemptID: receipt.AttemptID, Action: action, BeforeVolumes: before, BeforeLayeredDigest: action.BeforeLayeredDigest,
		ContextRoot: pipelineOutlineAllOperationContextRoot(receipt.FoundationContextRoot, action.BeforeLayeredDigest), ModelIdentityDigest: receipt.ModelIdentityDigest,
		PromptProtocolDigest: receipt.PromptProtocolDigest, VisibleContextDigest: outlineAllGateDigest, VisibleContextBytes: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	op, err := signPipelineOutlineAllOperationReceipt(pipelineOutlineAllOperationReceipt{
		Version: "outline-all-operation-receipt.v1", AttemptID: receipt.AttemptID, Action: action, IntentDigest: intent.IntentDigest,
		BeforeLayeredDigest: action.BeforeLayeredDigest, AfterLayeredDigest: pipelineOutlineAllLayeredDigest(skeleton), DerivedFlatDigest: flatDigest,
		ModelIdentityDigest: receipt.ModelIdentityDigest, PromptProtocolDigest: receipt.PromptProtocolDigest,
		ContextRoot: intent.ContextRoot, VisibleContextDigest: intent.VisibleContextDigest, VisibleContextBytes: intent.VisibleContextBytes,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writePipelinePlanningJSON(filepath.Join(candidate.Dir(), pipelineOutlineAllOperationIntentPath(1)), intent); err != nil {
		t.Fatal(err)
	}
	if _, err := writePipelinePlanningJSON(filepath.Join(candidate.Dir(), pipelineOutlineAllOperationReceiptPath(1)), op); err != nil {
		t.Fatal(err)
	}
	receipt.Status = domain.OutlineAllExecutionBuilding
	receipt.CompletedActionCount = 1
	receipt, err = domain.SignOutlineAllExecutionReceipt(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := candidate.SaveOutlineAllExecutionReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	if err := candidate.Progress.UpdatePhase(domain.PhaseOutline); err != nil {
		t.Fatal(err)
	}
	return live, candidate, &receipt
}

func TestPipelineOutlineAllLegacyStructurePhaseRecovery(t *testing.T) {
	live, candidate, receipt := legacyOutlineAllStructurePhaseFixture(t)
	before, err := os.ReadFile(filepath.Join(candidate.Dir(), pipelineOutlineAllOperationReceiptPath(1)))
	if err != nil {
		t.Fatal(err)
	}
	restored, err := recoverPipelineOutlineAllLegacyStructurePhase(live, candidate, receipt, receipt.ProtectedCanonRoot, receipt.StableProgressRoot)
	if err != nil || !restored {
		t.Fatalf("legacy phase did not recover: restored=%t err=%v", restored, err)
	}
	root, err := pipelineOutlineAllProtectedCanonRoot(candidate.Dir())
	if err != nil || root != receipt.ProtectedCanonRoot {
		t.Fatalf("canon baseline not restored: root=%s err=%v", root, err)
	}
	after, err := os.ReadFile(filepath.Join(candidate.Dir(), pipelineOutlineAllOperationReceiptPath(1)))
	if err != nil || string(after) != string(before) {
		t.Fatal("recovery rewrote the paid proposal receipt")
	}
	restored, err = recoverPipelineOutlineAllLegacyStructurePhase(live, candidate, receipt, receipt.ProtectedCanonRoot, receipt.StableProgressRoot)
	if err != nil || restored {
		t.Fatalf("recovery is not idempotent: restored=%t err=%v", restored, err)
	}
}

func TestPipelineOutlineAllLegacyStructurePhaseRecoveryRefusesOtherDrift(t *testing.T) {
	for _, drift := range []string{"canon", "progress", "receipt"} {
		t.Run(drift, func(t *testing.T) {
			live, candidate, receipt := legacyOutlineAllStructurePhaseFixture(t)
			switch drift {
			case "canon":
				if err := candidate.Outline.SavePremise("changed protected premise"); err != nil {
					t.Fatal(err)
				}
			case "progress":
				path := filepath.Join(candidate.Dir(), "meta", "progress.json")
				raw, _ := os.ReadFile(path)
				var fields map[string]json.RawMessage
				_ = json.Unmarshal(raw, &fields)
				fields["unrelated_future_state"] = json.RawMessage(`true`)
				if _, err := writePipelinePlanningJSON(path, fields); err != nil {
					t.Fatal(err)
				}
			case "receipt":
				if err := os.WriteFile(filepath.Join(candidate.Dir(), pipelineOutlineAllOperationReceiptPath(1)), []byte(`{}`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			restored, err := recoverPipelineOutlineAllLegacyStructurePhase(live, candidate, receipt, receipt.ProtectedCanonRoot, receipt.StableProgressRoot)
			if err == nil || restored {
				t.Fatalf("recovery accepted %s drift: restored=%t err=%v", drift, restored, err)
			}
			progress, err := candidate.Progress.Load()
			if err != nil || progress.Phase != domain.PhaseOutline {
				t.Fatal("rejected recovery still modified candidate")
			}
		})
	}
}
