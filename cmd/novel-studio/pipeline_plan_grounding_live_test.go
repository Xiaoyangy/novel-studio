package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/agents"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// Explicit paid evaluation. Default go test never calls a provider. The input
// bundle is read-only, and evaluation receipts are isolated from production.
func TestLivePlanGroundingRegression(t *testing.T) {
	root := os.Getenv("NOVEL_STUDIO_GROUNDING_EVAL_ROOT")
	path := os.Getenv("NOVEL_STUDIO_GROUNDING_EVAL_BUNDLE")
	if root == "" || path == "" {
		t.Skip("requires explicit isolated evaluation root and known-negative bundle")
	}
	cfg, err := bootstrap.LoadConfig(filepath.Join(root, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.FillDefaults()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var bundle domain.ProjectedChapterBundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		t.Fatal(err)
	}
	if bundle.CharacterAgentEvidence == nil {
		t.Fatal("needs independent character evidence")
	}
	// This diagnostic deliberately accepts an old ungrounded bundle: it must
	// discover the semantic defect, not re-sign or formalize the source artifact.
	evidence := bundle.CharacterAgentEvidence
	var arbitration domain.WorldArbitrationReceipt
	for _, receipt := range evidence.Arbitrations {
		if receipt.Digest == bundle.ChapterWorldSimulation.CharacterAgentProtocol.ArbitrationDigest {
			arbitration = receipt
		}
	}
	obs, err := domain.GroundingPOVObservation(bundle.ChapterWorldSimulation, evidence.Observations, evidence.Proposals, arbitration)
	if err != nil {
		t.Fatal(err)
	}
	output := store.NewStore(filepath.Join(root, "grounding-regression"))
	if err := output.Init(); err != nil {
		t.Fatal(err)
	}
	live := store.NewStore(filepath.Join(root, "run", "output", "novel"))
	a, err := newPipelineProjectAllAccounting(context.Background(), cfg, live, output, bundle.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := a.close(); err != nil {
			t.Error(err)
		}
	})
	models, err := bootstrap.NewModelSet(cfg)
	if err != nil {
		t.Fatal(err)
	}
	reviewer := agents.NewPlanGroundingReviewer(cfg, models, a.record)
	ctx := agents.WithDirectUsageLifecycle(a.ctx, agents.DirectUsageLifecycle{StartCall: a.startCall, SkipCall: a.meter.SkipCall})
	control := domain.ChapterPlan{Chapter: bundle.Chapter, Title: "受限正例", Goal: "林澄仍留在油料仓库柜台前核对手边R01；截至本轮结束，不进入值班室，不读取未交付的R03。", CausalSimulation: domain.ChapterCausalSimulation{WorldSimulationID: bundle.ChapterWorldSimulation.SimulationID, ProtagonistDecision: bundle.ChapterWorldSimulation.ProtagonistProjection.ChosenDecision}}
	for _, tc := range []struct {
		name     string
		plan     domain.ChapterPlan
		wantPass bool
	}{{"actual_invalid_plan", bundle.ChapterPlan, false}, {"grounded_control", control, true}} {
		t.Run(tc.name, func(t *testing.T) {
			input, err := domain.NewPlanGroundingInput(tc.plan, bundle.ChapterWorldSimulation, obs, arbitration, reviewer.Protocol)
			if err != nil {
				t.Fatal(err)
			}
			digest, err := domain.PlanGroundingInputDigest(input)
			if err != nil {
				t.Fatal(err)
			}
			audit, err := output.LoadPlanGroundingAudit(digest)
			if err != nil {
				t.Fatal(err)
			}
			if audit == nil {
				if err := a.beforeAgent(); err != nil {
					t.Fatal(err)
				}
				verdict, err := reviewer.Review(ctx, input)
				if err != nil {
					t.Fatal(err)
				}
				if err := a.afterAgent(); err != nil {
					t.Fatal(err)
				}
				receipt, err := domain.FinalizePlanGroundingReceipt(input, verdict)
				if err != nil {
					t.Fatal(err)
				}
				audit = &domain.PlanGroundingAudit{Input: input, Receipt: receipt}
				if err := output.SavePlanGroundingAudit(*audit); err != nil {
					t.Fatal(err)
				}
			}
			encoded, _ := json.Marshal(audit.Receipt)
			t.Log(string(encoded))
			if audit.Receipt.Verdict.Pass != tc.wantPass {
				t.Fatalf("classification mismatch: got %t want %t", audit.Receipt.Verdict.Pass, tc.wantPass)
			}
			if !tc.wantPass {
				kinds := map[string]bool{}
				for _, finding := range audit.Receipt.Verdict.Findings {
					kinds[finding.Kind] = true
				}
				if !kinds["time"] || !kinds["knowledge"] {
					t.Fatalf("actual time/knowledge defects not both detected: %v", kinds)
				}
			}
		})
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != string(after) {
		t.Fatal("evaluation changed source bundle")
	}
}
