package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/testutil"
)

func TestDurablePlanningProgressFollowsPersistedCycleResultsAndIgnoresObserverFailure(t *testing.T) {
	st := chapterActivationStore(t)
	model := &activationChapterProbeModel{}
	models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "progress", model)}
	cfg := bootstrap.Config{}
	cfg.CharacterAgents.MaxRevisionRounds = 1
	var mu sync.Mutex
	counts := map[string]map[string]bool{}
	hook := func(event DurablePlanningProgress) error {
		key, err := event.EventID()
		if err != nil {
			t.Error(err)
			return err
		}
		root := filepath.Join(st.Dir(), "meta/character_agents/activation_sessions", event.GenerationID, fmt.Sprintf("%06d", event.Chapter))
		proof := filepath.Join(root, "work", fmt.Sprintf("%06d", event.Cycle), "proof")
		var pattern string
		switch event.Kind {
		case PlanningContextBound:
			pattern = filepath.Join(proof, "activation.json")
		case PlanningProposalCommitted:
			pattern = filepath.Join(proof, "proposals", fmt.Sprintf("round-%02d", event.Round), "*.json")
		case PlanningArbitrationCommitted:
			pattern = filepath.Join(proof, fmt.Sprintf("arbitration-round-%02d.json", event.Round))
		case PlanningCycleCommitted:
			pattern = filepath.Join(root, "cycles", fmt.Sprintf("%06d.json", event.Cycle))
		case PlanningReadinessCommitted:
			pattern = filepath.Join(root, "readiness_audits", fmt.Sprintf("%06d.json", event.Cycle))
		default:
			t.Errorf("unexpected event %s", event.Kind)
		}
		paths, _ := filepath.Glob(pattern)
		found := false
		for _, path := range paths {
			raw, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			var value map[string]any
			if json.Unmarshal(raw, &value) != nil {
				continue
			}
			if event.Kind == PlanningReadinessCommitted {
				value, _ = value["receipt"].(map[string]any)
			}
			found = found || value["digest"] == event.ArtifactDigest
		}
		if !found {
			t.Errorf("progress preceded durable source: %+v", event)
		}
		mu.Lock()
		if counts[event.Kind] == nil {
			counts[event.Kind] = map[string]bool{}
		}
		counts[event.Kind][key] = true
		mu.Unlock()
		return errors.New("PRIVATE observer failure must not rerun paid calls")
	}
	ctx := context.WithValue(context.Background(), projectedPlanningAccountingKey{}, ProjectedPlanningAccounting{DurableProgress: hook})
	boundary := ProjectedArcBoundary{FirstChapter: 1, LastChapter: 1, BookLastChapter: 3}
	evidence, err := runCharacterActivationChapter(ctx, cfg, st, models, "pg2_progress", 1, boundary, domain.ProjectedPlanningContextV2{}, nil, 4)
	if err != nil {
		t.Fatal(err)
	}
	if err := domain.ValidateCharacterActivationChapterEvidence(*evidence); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{PlanningContextBound, PlanningProposalCommitted, PlanningArbitrationCommitted, PlanningCycleCommitted, PlanningReadinessCommitted} {
		if len(counts[kind]) != 2 {
			t.Fatalf("%s distinct durable events=%d, want 2", kind, len(counts[kind]))
		}
	}
	if model.characterCalls.Load() != 2 || model.arbiterCalls.Load() != 2 || model.readiness.calls.Load() != 2 {
		t.Fatal("monitor failure altered provider attempts")
	}
	before, _ := json.Marshal(counts)
	if _, err := runCharacterActivationChapter(ctx, cfg, st, models, "pg2_progress", 1, boundary, domain.ProjectedPlanningContextV2{}, nil, 4); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(counts)
	if string(before) != string(after) || model.characterCalls.Load() != 2 {
		t.Fatal("cached chapter replay fabricated progress or repeated paid calls")
	}
}

func TestDurablePlanningProgressDoesNotReportRejectedProposal(t *testing.T) {
	st := store.NewStore(t.TempDir())
	cycle := testutil.CharacterCycle(t, 1, "", nil, 0)
	session, err := domain.NewCharacterActivationSession(cycle.GenerationID, 1, cycle.ChapterContextDigest, *cycle.Evidence.Stimulus.PhysicalState, 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	seedActivationExecutionInputs(t, st, session, cycle)
	cycle.Evidence.Proposals[0].KnowledgeRefs = []string{"foreign-secret"}
	model := &activationExecutionModel{cycle: cycle}
	count := 0
	ctx := context.WithValue(context.Background(), projectedPlanningAccountingKey{}, ProjectedPlanningAccounting{DurableProgress: func(DurablePlanningProgress) error { count++; return nil }})
	if err := runOneCharacterAgent(ctx, bootstrap.Config{}, st, model, cycle.Evidence.Observations[0], &session); err == nil {
		t.Fatal("invalid proposal was accepted")
	}
	if count != 0 {
		t.Fatal("rejected model/tool work counted as durable business progress")
	}
}

func TestDurablePlanningProgressIdentityHasNoFreeTextChannel(t *testing.T) {
	event := DurablePlanningProgress{GenerationID: "pg2_test", Chapter: 1, Cycle: 1, Round: 1, Kind: PlanningProposalCommitted, ArtifactDigest: "sha256:" + strings.Repeat("a", 64)}
	first, err := event.EventID()
	if err != nil {
		t.Fatal(err)
	}
	second, _ := event.EventID()
	if first != second {
		t.Fatal("unstable progress identity")
	}
	for _, bad := range []DurablePlanningProgress{
		{GenerationID: "PRIVATE 世界真相", Chapter: 1, Kind: event.Kind, ArtifactDigest: event.ArtifactDigest},
		{GenerationID: event.GenerationID, Chapter: 1, Kind: "token_stream", ArtifactDigest: event.ArtifactDigest},
		{GenerationID: event.GenerationID, Chapter: 1, Kind: event.Kind, ArtifactDigest: "PRIVATE result"},
	} {
		if _, err := bad.EventID(); err == nil {
			t.Fatal("unsafe/non-durable event accepted")
		}
	}
}
