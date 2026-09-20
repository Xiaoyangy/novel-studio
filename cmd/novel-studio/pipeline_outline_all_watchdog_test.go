package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

type outlineAllWatchdogFixture struct {
	cfg       bootstrap.Config
	bundle    assets.Bundle
	live      *store.Store
	candidate *store.Store
	owner     string
	compass   domain.StoryCompass
	target    domain.BookScaleTarget
	receipt   *domain.OutlineAllExecutionReceipt
	skeleton  []domain.VolumeOutline
}

func newOutlineAllWatchdogFixture(t *testing.T) outlineAllWatchdogFixture {
	t.Helper()
	cfg, bundle, _, candidatePath := outlinePolicyCandidateFixture(t, "", true, "")
	f := outlineAllWatchdogFixture{cfg: cfg, bundle: bundle, live: store.NewStore(cfg.OutputDir), candidate: store.NewStore(candidatePath), owner: "outline-watchdog-test"}
	f.cfg.OutputDir = candidatePath
	receipt, err := f.candidate.LoadOutlineAllExecutionReceipt()
	publicationArtifactMust(t, err)
	if receipt == nil {
		t.Fatal("prepared candidate has no receipt")
	}
	for _, st := range []*store.Store{f.live, f.candidate} {
		publicationArtifactMust(t, st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{Mode: domain.PipelineExecutionOutlineAll, TargetChapter: 1, PlanDigest: receipt.ModelIdentityDigest, Owner: f.owner, ExpiresAt: time.Now().UTC().Add(time.Hour)}))
		t.Cleanup(func() { _ = st.Runtime.ReleasePipelineExecution(f.owner) })
	}
	lock, err := f.candidate.Runtime.LoadPipelineExecution()
	publicationArtifactMust(t, err)
	before, err := f.candidate.Outline.LoadLayeredOutline()
	publicationArtifactMust(t, err)
	compass, err := f.candidate.Outline.LoadCompass()
	publicationArtifactMust(t, err)
	if lock == nil || compass == nil {
		t.Fatal("candidate lacks actual lock or compass")
	}
	f.compass = *compass
	f.target, err = domain.ResolveBookScaleTarget(compass.EstimatedScale, domain.RealVolumeCount(before), domain.TotalChapters(before))
	publicationArtifactMust(t, err)
	action := domain.OutlineAllPendingAction{Type: domain.OutlineAllActionPlanStructure, Operation: 1, BeforeLayeredDigest: pipelineOutlineAllLayeredDigest(before)}
	f.receipt, err = f.candidate.UpdateOutlineAllExecutionReceipt(receipt.ReceiptDigest, func(current *domain.OutlineAllExecutionReceipt) error {
		if err := domain.BindOutlineAllExecutionLock(current, *lock); err != nil {
			return err
		}
		current.PendingAction = &action
		current.UpdatedAt = time.Now().UTC()
		return nil
	})
	publicationArtifactMust(t, err)
	foundation, err := loadPipelineOutlineAllFrozenFoundation(candidatePath)
	publicationArtifactMust(t, err)
	_, visible, digest, err := buildPipelineOutlineAllModelVisibleContext(before, *compass, f.target, action, foundation, bundle.References, f.receipt.ContractEvidencePolicy)
	publicationArtifactMust(t, err)
	_, err = createPipelineOutlineAllOperationIntent(candidatePath, f.receipt.AttemptID, action, before, foundation.Root, f.receipt.ModelIdentityDigest, f.receipt.PromptProtocolDigest, digest, len(visible))
	publicationArtifactMust(t, err)
	f.skeleton = []domain.VolumeOutline{{Index: 1, Title: "第一卷", Theme: "验证规则", Arcs: []domain.ArcOutline{{Index: 1, Title: "起步弧", Goal: "完成第一次真实改变", EstimatedChapters: 8}}}}
	return f
}

func (f outlineAllWatchdogFixture) run() (*domain.OutlineAllExecutionReceipt, error) {
	return recoverOrRunPipelineOutlineAllOperation(f.cfg, f.bundle, f.live, f.candidate, f.owner, f.compass, f.target, f.receipt, f.receipt.ModelIdentityDigest, f.receipt.PromptProtocolDigest)
}

func (f outlineAllWatchdogFixture) saveStructure() error {
	raw, err := json.Marshal(map[string]any{"type": "plan_structure", "content": f.skeleton})
	if err != nil {
		return err
	}
	_, err = tools.NewSaveFoundationTool(f.candidate).Execute(context.Background(), raw)
	return err
}

func TestOutlineAllCommittedOperationAdvancesWatchdog(t *testing.T) {
	f := newOutlineAllWatchdogFixture(t)
	start := time.Date(2026, 9, 20, 15, 0, 0, 0, time.UTC)
	now := start
	w, err := newPipelineWatchdog(pipelineWatchdogConfig{OutputDir: f.live.Dir(), InvocationID: "outline-operation", Stage: "outline-all", Now: func() time.Time { return now }, HeartbeatInterval: time.Hour})
	publicationArtifactMust(t, err)
	publicationArtifactMust(t, w.Start())
	t.Cleanup(func() { _ = w.Stop() })
	release, err := bindCurrentPipelineWatchdog(w)
	publicationArtifactMust(t, err)
	t.Cleanup(release)
	original := runPipelineOutlineAllArchitect
	t.Cleanup(func() { runPipelineOutlineAllArchitect = original })
	calls := 0
	runPipelineOutlineAllArchitect = func(cfg bootstrap.Config, _ assets.Bundle, prompt, live string) error {
		calls++
		if cfg.OutputDir != f.candidate.Dir() || live != f.live.Dir() {
			return fmt.Errorf("dispatch escaped its prepared candidate")
		}
		if _, err := domain.ParseOutlineAllIntent(prompt); err != nil {
			return err
		}
		return f.saveStructure()
	}
	now = start.Add(4 * time.Minute)
	committed, err := f.run()
	publicationArtifactMust(t, err)
	if committed == nil || committed.CompletedActionCount != 1 || committed.PendingAction != nil || calls != 1 {
		t.Fatalf("actual operation did not commit once: receipt=%+v calls=%d", committed, calls)
	}
	now = start.Add(5 * time.Minute)
	publicationArtifactMust(t, w.EvaluateAt(now))
	state := loadPipelineWatchdogStateForTest(t, f.live.Dir())
	if state.ProgressSeq != 1 || state.Status != pipelineWatchdogRunning || state.LastProgressAt != start.Add(4*time.Minute).Format(time.RFC3339Nano) {
		t.Fatalf("durable candidate operation was invisible to watchdog: completed=%d seq=%d status=%s last_progress=%s", committed.CompletedActionCount, state.ProgressSeq, state.Status, state.LastProgressAt)
	}
	if _, err := f.run(); err == nil {
		t.Fatal("replaying the stale pending receipt bypassed its CAS")
	}
	if after := loadPipelineWatchdogStateForTest(t, f.live.Dir()); calls != 1 || after.ProgressSeq != state.ProgressSeq || after.LastProgressAt != state.LastProgressAt {
		t.Fatal("failed stale-receipt replay dispatched again or refreshed progress")
	}
}

func TestOutlineAllFailedOperationDoesNotAdvanceWatchdog(t *testing.T) {
	for _, kind := range []string{"no-mutation", "invalid-structure"} {
		t.Run(kind, func(t *testing.T) {
			f := newOutlineAllWatchdogFixture(t)
			observer := &recordingPipelineWatchdog{}
			release, err := bindCurrentPipelineWatchdog(observer)
			publicationArtifactMust(t, err)
			t.Cleanup(release)
			original := runPipelineOutlineAllArchitect
			t.Cleanup(func() { runPipelineOutlineAllArchitect = original })
			calls := 0
			runPipelineOutlineAllArchitect = func(bootstrap.Config, assets.Bundle, string, string) error {
				calls++
				if kind == "invalid-structure" {
					f.skeleton[0].Arcs[0].EstimatedChapters = 7
					return f.saveStructure()
				}
				return nil
			}
			if _, err := f.run(); err == nil || calls != 1 {
				t.Fatalf("failed mutation escaped the real validation boundary: calls=%d err=%v", calls, err)
			}
			_, _, events, _ := observer.snapshot()
			if len(events) != 0 {
				t.Fatalf("failed operation created progress: %v", events)
			}
			persisted, err := f.candidate.LoadOutlineAllExecutionReceipt()
			publicationArtifactMust(t, err)
			if persisted.CompletedActionCount != 0 || persisted.PendingAction == nil {
				t.Fatal("failed operation became a completed checkpoint")
			}
			if _, err := os.Stat(filepath.Join(f.candidate.Dir(), pipelineOutlineAllOperationReceiptPath(1))); !os.IsNotExist(err) {
				t.Fatalf("failed mutation published an immutable completion receipt: %v", err)
			}
		})
	}
}

func TestOutlineAllWatchdogFailureCannotUndoSuccessfulOperation(t *testing.T) {
	f := newOutlineAllWatchdogFixture(t)
	observer := &recordingPipelineWatchdog{progressErr: errors.New("monitor write failure")}
	release, err := bindCurrentPipelineWatchdog(observer)
	publicationArtifactMust(t, err)
	t.Cleanup(release)
	original := runPipelineOutlineAllArchitect
	t.Cleanup(func() { runPipelineOutlineAllArchitect = original })
	calls := 0
	runPipelineOutlineAllArchitect = func(bootstrap.Config, assets.Bundle, string, string) error {
		calls++
		return f.saveStructure()
	}
	committed, err := f.run()
	publicationArtifactMust(t, err)
	if committed == nil || committed.CompletedActionCount != 1 || committed.PendingAction != nil || calls != 1 {
		t.Fatal("observer failure reversed a successful business checkpoint")
	}
	_, _, events, _ := observer.snapshot()
	if len(events) != 1 {
		t.Fatalf("successful checkpoint did not attempt exactly one observation: %v", events)
	}
	if _, err := f.run(); err == nil {
		t.Fatal("stale pending receipt unexpectedly committed twice")
	}
	_, _, events, _ = observer.snapshot()
	if calls != 1 || len(events) != 1 {
		t.Fatal("monitor failure caused a repeated dispatch or progress event")
	}
}

func TestOutlineAllImmutableReceiptNeedsSuccessfulCASToAdvanceWatchdog(t *testing.T) {
	f := newOutlineAllWatchdogFixture(t)
	// Model a crash after the real tool mutation and immutable operation proof,
	// before the execution receipt's completion CAS. All inputs come from the
	// actual prepared candidate and its signed, frozen operation intent.
	publicationArtifactMust(t, f.saveStructure())
	var intent pipelineOutlineAllOperationIntent
	publicationArtifactMust(t, readPipelinePlanningJSON(filepath.Join(f.candidate.Dir(), pipelineOutlineAllOperationIntentPath(1)), &intent))
	after, err := f.candidate.Outline.LoadLayeredOutline()
	publicationArtifactMust(t, err)
	flatDigest, err := repairPipelineOutlineAllDerivedArtifacts(f.candidate, after)
	publicationArtifactMust(t, err)
	op, err := signPipelineOutlineAllOperationReceipt(pipelineOutlineAllOperationReceipt{
		Version: "outline-all-operation-receipt.v1", AttemptID: f.receipt.AttemptID, Action: intent.Action,
		IntentDigest: intent.IntentDigest, ContextRoot: intent.ContextRoot, VisibleContextDigest: intent.VisibleContextDigest, VisibleContextBytes: intent.VisibleContextBytes,
		BeforeLayeredDigest: intent.BeforeLayeredDigest, AfterLayeredDigest: pipelineOutlineAllLayeredDigest(after), DerivedFlatDigest: flatDigest,
		ModelIdentityDigest: f.receipt.ModelIdentityDigest, PromptProtocolDigest: f.receipt.PromptProtocolDigest, CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
	publicationArtifactMust(t, err)
	opPath := filepath.Join(f.candidate.Dir(), pipelineOutlineAllOperationReceiptPath(1))
	_, err = writePipelinePlanningJSON(opPath, op)
	publicationArtifactMust(t, err)
	opBefore, err := os.ReadFile(opPath)
	publicationArtifactMust(t, err)
	current, err := f.candidate.UpdateOutlineAllExecutionReceipt(f.receipt.ReceiptDigest, func(r *domain.OutlineAllExecutionReceipt) error {
		r.UpdatedAt = r.UpdatedAt.Add(time.Second)
		return nil
	})
	publicationArtifactMust(t, err)
	now := time.Date(2026, 9, 20, 15, 0, 0, 0, time.UTC)
	w, err := newPipelineWatchdog(pipelineWatchdogConfig{OutputDir: f.live.Dir(), InvocationID: "outline-recovery", Stage: "outline-all", Now: func() time.Time { return now }, HeartbeatInterval: time.Hour})
	publicationArtifactMust(t, err)
	publicationArtifactMust(t, w.Start())
	t.Cleanup(func() { _ = w.Stop() })
	release, err := bindCurrentPipelineWatchdog(w)
	publicationArtifactMust(t, err)
	t.Cleanup(release)
	original := runPipelineOutlineAllArchitect
	t.Cleanup(func() { runPipelineOutlineAllArchitect = original })
	runPipelineOutlineAllArchitect = func(bootstrap.Config, assets.Bundle, string, string) error {
		t.Error("durable real mutation was sent to the model again")
		return errors.New("unexpected model dispatch")
	}
	if _, err := f.run(); err == nil {
		t.Fatal("stale execution receipt bypassed final completion CAS")
	}
	if state := loadPipelineWatchdogStateForTest(t, f.live.Dir()); state.ProgressSeq != 0 {
		t.Fatal("immutable proof alone counted as a successful completion checkpoint")
	}
	f.receipt = current
	committed, err := f.run()
	publicationArtifactMust(t, err)
	if committed.CompletedActionCount != 1 || committed.PendingAction != nil {
		t.Fatal("verified immutable operation did not recover its first completion CAS")
	}
	if state := loadPipelineWatchdogStateForTest(t, f.live.Dir()); state.ProgressSeq != 1 {
		t.Fatalf("first successful recovered CAS did not advance exactly once: %+v", state)
	}
	if _, err := f.run(); err == nil {
		t.Fatal("replayed pending receipt bypassed its now-stale CAS")
	}
	if state := loadPipelineWatchdogStateForTest(t, f.live.Dir()); state.ProgressSeq != 1 {
		t.Fatal("replayed immutable receipt advanced watchdog again")
	}
	if raw, err := os.ReadFile(opPath); err != nil || string(raw) != string(opBefore) {
		t.Fatal("completion recovery rewrote its immutable operation proof")
	}
}
