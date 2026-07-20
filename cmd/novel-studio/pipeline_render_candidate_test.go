package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestPreparePipelineRenderCandidateDeepCopiesWithoutTouchingLive(t *testing.T) {
	live := pipelineRenderCandidateTestLive(t)
	frozen := pipelineRenderCandidateTestFrozen()
	before := pipelineRenderCandidateTestSnapshot(t, live)

	candidate, err := preparePipelineRenderCandidate(live, frozen)
	if err != nil {
		t.Fatal(err)
	}
	liveFile := filepath.Join(live, "meta", "state.json")
	candidateFile := filepath.Join(candidate.OutputDir, "meta", "state.json")
	liveInfo, err := os.Stat(liveFile)
	if err != nil {
		t.Fatal(err)
	}
	candidateInfo, err := os.Stat(candidateFile)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(liveInfo, candidateInfo) {
		t.Fatal("render candidate reused a live inode through a hard link")
	}
	if err := os.WriteFile(candidateFile, []byte("candidate-only mutation\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	after := pipelineRenderCandidateTestSnapshot(t, live)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("candidate mutation touched live output:\nbefore=%v\nafter=%v", before, after)
	}
	if body, err := os.ReadFile(liveFile); err != nil || string(body) != "live-state\n" {
		t.Fatalf("live file changed through candidate inode: body=%q err=%v", body, err)
	}
}

func TestPipelineRenderTransactionIDBindsExactFrozenInputs(t *testing.T) {
	base := pipelineRenderCandidateTestFrozen()
	want, err := pipelineRenderTransactionID(base)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := pipelineRenderTransactionID(base)
	if err != nil {
		t.Fatal(err)
	}
	if replayed != want {
		t.Fatalf("exact frozen binding produced unstable transaction IDs: %s != %s", replayed, want)
	}

	cases := []struct {
		name   string
		mutate func(*pipelineFrozenPlan)
	}{
		{"generation", func(value *pipelineFrozenPlan) { value.PlanningGenerationID += "-drift" }},
		{"chapter", func(value *pipelineFrozenPlan) { value.Chapter++ }},
		{"plan", func(value *pipelineFrozenPlan) { value.PlanDigest += "-drift" }},
		{"bundle", func(value *pipelineFrozenPlan) { value.ProjectedBundleDigest += "-drift" }},
		{"promotion", func(value *pipelineFrozenPlan) { value.PromotionReceiptDigest += "-drift" }},
		{"render input", func(value *pipelineFrozenPlan) { value.PipelineRunInputDigest += "-drift" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			drifted := *base
			tc.mutate(&drifted)
			got, err := pipelineRenderTransactionID(&drifted)
			if err != nil {
				t.Fatal(err)
			}
			if got == want {
				t.Fatalf("%s drift reused transaction ID %s", tc.name, want)
			}
		})
	}
}

func TestPipelineAtomicWriteTempNameIsNarrow(t *testing.T) {
	for _, name := range []string{"usage.json.tmp-2326855527", ".planning-12345.tmp", ".receipt.json.tmp-987"} {
		if !pipelineAtomicWriteTempName(name) {
			t.Fatalf("reserved atomic temp %q was not recognized", name)
		}
	}
	for _, name := range []string{"usage.json", "durable.tmp-backup", ".planning-final.tmp", "story.tmp-abc"} {
		if pipelineAtomicWriteTempName(name) {
			t.Fatalf("durable/non-reserved name %q was classified as atomic temp", name)
		}
	}
}

func TestPreparePipelineRenderCandidateRecoversDurableActiveAndStaleDraft(t *testing.T) {
	live := pipelineRenderCandidateTestLive(t)
	st := store.NewStore(live)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "冻结计划"}); err != nil {
		t.Fatal(err)
	}
	plan, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "plan", "drafts/01.plan.json",
		"plan", "chapter_world_simulation", "review", "draft-structural-block",
	)
	if err != nil {
		t.Fatal(err)
	}
	frozen := pipelineRenderCandidateTestFrozen()
	frozen.PlanDigest = plan.Digest
	frozen.PlanCheckpointSeq = plan.Seq

	candidate, err := preparePipelineRenderCandidate(live, frozen)
	if err != nil {
		t.Fatal(err)
	}
	candidateStore := store.NewStore(candidate.OutputDir)
	const body = "第一章\n\n候选草稿带着精确 checkpoint 跨进程恢复。"
	if err := candidateStore.Drafts.SaveDraft(1, body); err != nil {
		t.Fatal(err)
	}
	if _, err := candidateStore.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "edit", "drafts/01.draft.md",
		"plan", "rerender-request", "draft", "edit",
	); err != nil {
		t.Fatal(err)
	}

	activeRecovered, err := preparePipelineRenderCandidate(live, frozen)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := store.NewStore(activeRecovered.OutputDir).Drafts.LoadDraft(1); err != nil || got != body {
		t.Fatalf("active exact candidate was not reused: body=%q err=%v", got, err)
	}
	if err := retirePipelineRenderCandidate(activeRecovered.ContainerDir, "stale"); err != nil {
		t.Fatal(err)
	}
	staleRecovered, err := preparePipelineRenderCandidate(live, frozen)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := store.NewStore(staleRecovered.OutputDir).Drafts.LoadDraft(1); err != nil || got != body {
		t.Fatalf("stale exact candidate was not restored: body=%q err=%v", got, err)
	}
}

func TestPreparePipelineRenderCandidateDoesNotRecoverDurablyRejectedDraft(t *testing.T) {
	live := pipelineRenderCandidateTestLive(t)
	st := store.NewStore(live)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "冻结计划"}); err != nil {
		t.Fatal(err)
	}
	plan, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "plan", "drafts/01.plan.json",
		"plan", "chapter_world_simulation", "review", "draft-structural-block",
	)
	if err != nil {
		t.Fatal(err)
	}
	frozen := pipelineRenderCandidateTestFrozen()
	frozen.PlanDigest = plan.Digest
	frozen.PlanCheckpointSeq = plan.Seq
	candidate, err := preparePipelineRenderCandidate(live, frozen)
	if err != nil {
		t.Fatal(err)
	}
	candidateStore := store.NewStore(candidate.OutputDir)
	const rejectedBody = "第一章\n\n已被语义拒绝的候选。"
	if err := candidateStore.Drafts.SaveDraft(1, rejectedBody); err != nil {
		t.Fatal(err)
	}
	bodyCheckpoint, err := candidateStore.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "draft", "drafts/01.draft.md",
		"plan", "rerender-request", "draft", "edit",
	)
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := syncPipelineRenderConvergence(candidateStore)
	if err != nil {
		t.Fatal(err)
	}
	record := pipelineRenderConvergenceRecordFor(
		ledger,
		strings.TrimPrefix(bodyCheckpoint.Digest, domain.PlanningV2DigestPrefix),
	)
	record.SemanticReject = true
	if err := savePipelineRenderConvergenceLedger(live, ledger); err != nil {
		t.Fatal(err)
	}
	if err := retirePipelineRenderCandidate(candidate.ContainerDir, "rejected"); err != nil {
		t.Fatal(err)
	}
	fresh, err := preparePipelineRenderCandidate(live, frozen)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := store.NewStore(fresh.OutputDir).Drafts.LoadDraft(1); strings.TrimSpace(got) != "" {
		t.Fatalf("durably rejected candidate was incorrectly recovered: %q", got)
	}

	// Recovery must also fail closed when the sibling ledger exists but cannot
	// be authenticated. Treating a decode/read error as "no rejection" would
	// recreate the same resurrection bug through a corrupted control plane.
	if err := retirePipelineRenderCandidate(fresh.ContainerDir, "stale"); err != nil {
		t.Fatal(err)
	}
	ledgerPath, err := pipelineRenderConvergenceLedgerPath(live, candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ledgerPath, []byte("{\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if recovered, err := preparePipelineRenderCandidate(live, frozen); err == nil || recovered != nil ||
		!strings.Contains(err.Error(), "load exact render convergence rejection") {
		t.Fatalf("malformed durable rejection ledger did not fail closed: candidate=%+v err=%v", recovered, err)
	}
}

func TestPreparePipelineRenderCandidateReplaysDraftOntoCurrentLiveRoot(t *testing.T) {
	live := pipelineRenderCandidateTestLive(t)
	st := store.NewStore(live)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "冻结计划"}); err != nil {
		t.Fatal(err)
	}
	plan, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "plan", "drafts/01.plan.json",
		"plan", "chapter_world_simulation", "review", "draft-structural-block",
	)
	if err != nil {
		t.Fatal(err)
	}
	frozen := pipelineRenderCandidateTestFrozen()
	frozen.PlanDigest = plan.Digest
	frozen.PlanCheckpointSeq = plan.Seq
	candidate, err := preparePipelineRenderCandidate(live, frozen)
	if err != nil {
		t.Fatal(err)
	}
	const body = "第一章\n\n跨进程恢复只重放这一份精确正文。"
	candidateStore := store.NewStore(candidate.OutputDir)
	if err := candidateStore.Drafts.SaveDraft(1, body); err != nil {
		t.Fatal(err)
	}
	if _, err := candidateStore.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "edit", "drafts/01.draft.md",
		"plan", "rerender-request", "draft", "edit",
	); err != nil {
		t.Fatal(err)
	}
	oldJudge := filepath.Join(candidate.OutputDir, "reviews", "drafts", "01_deepseek_ai_judge.json")
	if err := os.MkdirAll(filepath.Dir(oldJudge), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldJudge, []byte("obsolete judge\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const currentMarker = "current invocation metadata\n"
	if err := os.WriteFile(filepath.Join(live, "meta", "run.json"), []byte(currentMarker), 0o644); err != nil {
		t.Fatal(err)
	}
	wantLiveRoot, err := store.DirectoryContentRoot(live)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := preparePipelineRenderCandidate(live, frozen)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.SourceLiveRoot != wantLiveRoot {
		t.Fatalf("recovered source root=%s want current live=%s", recovered.SourceLiveRoot, wantLiveRoot)
	}
	if got, err := recoveredStoreDraft(recovered.OutputDir, 1); err != nil || got != body {
		t.Fatalf("replayed body=%q err=%v", got, err)
	}
	if cp := store.NewStore(recovered.OutputDir).Checkpoints.LatestByStep(domain.ChapterScope(1), "edit"); cp == nil {
		t.Fatal("recovery lost the source edit checkpoint semantics")
	}
	if got, err := os.ReadFile(filepath.Join(recovered.OutputDir, "meta", "run.json")); err != nil || string(got) != currentMarker {
		t.Fatalf("fresh candidate lost current live marker: body=%q err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(recovered.OutputDir, "reviews", "drafts", "01_deepseek_ai_judge.json")); !os.IsNotExist(err) {
		t.Fatalf("recovery copied obsolete judge instead of requiring exact-body rejudge: %v", err)
	}
	if _, err := os.Stat(filepath.Join(recovered.OutputDir, "meta", "planning", "render_candidate_recovery.json")); err != nil {
		t.Fatalf("recovery receipt missing: %v", err)
	}
	receipt, err := publishPipelineRenderCandidate(live, recovered)
	if err != nil {
		t.Fatalf("current-root candidate failed CAS publish: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(live, "meta", "run.json")); err != nil || string(got) != currentMarker {
		t.Fatalf("publish rolled back current live marker: body=%q err=%v", got, err)
	}
	if err := finalizePipelineRenderCandidate(live, receipt.TransactionID); err != nil {
		t.Fatal(err)
	}
}

func TestPreparePipelineRenderCandidateRejectsLivePlanSequenceDrift(t *testing.T) {
	live := pipelineRenderCandidateTestLive(t)
	st := store.NewStore(live)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "冻结计划"}); err != nil {
		t.Fatal(err)
	}
	plan, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "plan", "drafts/01.plan.json",
		"plan", "chapter_world_simulation", "review", "draft-structural-block",
	)
	if err != nil {
		t.Fatal(err)
	}
	frozen := pipelineRenderCandidateTestFrozen()
	frozen.PlanDigest = plan.Digest
	frozen.PlanCheckpointSeq = plan.Seq
	candidate, err := preparePipelineRenderCandidate(live, frozen)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.NewStore(candidate.OutputDir).Drafts.SaveDraft(1, "第一章\n\n旧 epoch 草稿。"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.NewStore(candidate.OutputDir).Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "draft", "drafts/01.draft.md",
		"plan", "rerender-request", "draft", "edit",
	); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "中间漂移计划"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "plan", "drafts/01.plan.json",
		"plan", "chapter_world_simulation", "review", "draft-structural-block",
	); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "冻结计划"}); err != nil {
		t.Fatal(err)
	}
	newPlan, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "plan", "drafts/01.plan.json",
		"plan", "chapter_world_simulation", "review", "draft-structural-block",
	)
	if err != nil {
		t.Fatal(err)
	}
	if newPlan.Digest != plan.Digest || newPlan.Seq == plan.Seq {
		t.Fatalf("fixture did not create same-digest new plan epoch: old=%+v new=%+v", plan, newPlan)
	}
	if _, err := preparePipelineRenderCandidate(live, frozen); err == nil || !strings.Contains(err.Error(), "requires live plan") {
		t.Fatalf("same-digest plan sequence drift was not rejected: %v", err)
	}
}

func TestBindCurrentRenderExecutionToCandidate(t *testing.T) {
	live := pipelineRenderCandidateTestLive(t)
	frozen := pipelineRenderCandidateTestFrozen()
	owner := "render-candidate-bind-test"
	liveStore := store.NewStore(live)
	if err := liveStore.Init(); err != nil {
		t.Fatal(err)
	}
	if err := liveStore.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionRender,
		TargetChapter: frozen.Chapter,
		PlanDigest:    frozen.PlanDigest,
		Owner:         owner,
		ExpiresAt:     time.Now().UTC().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = liveStore.Runtime.ReleasePipelineExecution(owner) })
	candidate, err := preparePipelineRenderCandidate(live, frozen)
	if err != nil {
		t.Fatal(err)
	}
	reservation, reused, err := reservePipelineWholeBodyDispatch(candidate.OutputDir, "stale-before-recovery", 1)
	if err != nil || reused || reservation == nil {
		t.Fatalf("reserve stale recovery permit: reservation=%+v reused=%v err=%v", reservation, reused, err)
	}
	if err := store.NewStore(candidate.OutputDir).Runtime.ArmPipelineRenderProsePermit(
		reservation.AuthorizationDigest,
		reservation.Attempt,
	); err != nil {
		t.Fatal(err)
	}
	if err := store.NewStore(candidate.OutputDir).Runtime.ReleasePipelineExecution(owner); err != nil {
		t.Fatal(err)
	}
	if err := bindCurrentRenderExecutionToCandidate(live, candidate, frozen); err != nil {
		t.Fatal(err)
	}
	bound, err := store.NewStore(candidate.OutputDir).Runtime.LoadPipelineExecution()
	if err != nil || bound == nil || bound.Owner != owner || bound.ProcessID != os.Getpid() ||
		bound.Mode != domain.PipelineExecutionRender || bound.TargetChapter != frozen.Chapter ||
		bound.PlanDigest != frozen.PlanDigest {
		t.Fatalf("candidate render lock not rebound exactly: lock=%+v err=%v", bound, err)
	}
	if err := store.NewStore(candidate.OutputDir).Runtime.ConsumePipelineRenderProsePermit(frozen.Chapter); err == nil {
		t.Fatal("stale pre-recovery permit survived mandatory candidate bind")
	}
	if _, err := os.Stat(filepath.Join(candidate.OutputDir, "meta", "runtime", "render_prose_permit.json")); !os.IsNotExist(err) {
		t.Fatalf("stale permit remains publishable after recovery bind: %v", err)
	}
}

func recoveredStoreDraft(outputDir string, chapter int) (string, error) {
	return store.NewStore(outputDir).Drafts.LoadDraft(chapter)
}

func TestPublishAndFinalizePipelineRenderCandidatePreservesProtocolEvidence(t *testing.T) {
	live := pipelineRenderCandidateTestLive(t)
	before := pipelineRenderCandidateTestSnapshot(t, live)
	candidate, err := preparePipelineRenderCandidate(live, pipelineRenderCandidateTestFrozen())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(candidate.OutputDir, "chapters", "01.md"),
		[]byte("candidate accepted body\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	candidateBeforePublish := pipelineRenderCandidateTestSnapshot(t, candidate.OutputDir)

	receipt, err := publishPipelineRenderCandidate(live, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if got := pipelineRenderCandidateTestSnapshot(t, live); !reflect.DeepEqual(got, candidateBeforePublish) {
		t.Fatalf("published live tree differs from candidate:\ngot=%v\nwant=%v", got, candidateBeforePublish)
	}
	if got := pipelineRenderCandidateTestSnapshot(t, receipt.ArchiveDir); !reflect.DeepEqual(got, before) {
		t.Fatalf("publish archive did not preserve prior live:\ngot=%v\nwant=%v", got, before)
	}
	publisher := store.NewDirectoryPublishStore(candidate.TransactionRoot)
	state, err := publisher.LoadDirectoryPublishState(candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state == nil || state.Phase != store.DirectoryPublishReceiptWritten {
		t.Fatalf("publish state=%+v, want receipt_written", state)
	}

	if err := finalizePipelineRenderCandidate(live, candidate.ID); err != nil {
		t.Fatal(err)
	}
	state, err = publisher.LoadDirectoryPublishState(candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state == nil || state.Phase != store.DirectoryPublishFinalized ||
		state.Receipt == nil || state.Receipt.ReceiptDigest != receipt.ReceiptDigest {
		t.Fatalf("finalized state lost publish evidence: %+v", state)
	}
	if _, err := os.Stat(receipt.ArchiveDir); !os.IsNotExist(err) {
		t.Fatalf("finalize retained rollback archive: %v", err)
	}
	if got := pipelineRenderCandidateTestSnapshot(t, live); !reflect.DeepEqual(got, candidateBeforePublish) {
		t.Fatalf("finalize changed committed live tree:\ngot=%v\nwant=%v", got, candidateBeforePublish)
	}
}

func TestRecoverAllDirectoryPublishesRestoresLiveArchivedRenderCandidate(t *testing.T) {
	live := pipelineRenderCandidateTestLive(t)
	candidate, err := preparePipelineRenderCandidate(live, pipelineRenderCandidateTestFrozen())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(candidate.OutputDir, "chapters", "01.md"),
		[]byte("candidate crash-recovery body\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	wantLive := pipelineRenderCandidateTestSnapshot(t, candidate.OutputDir)
	receipt, err := publishPipelineRenderCandidate(live, candidate)
	if err != nil {
		t.Fatal(err)
	}

	// Recreate the exact live_archived crash window: intent and archive are
	// durable, candidate still exists, live is absent, receipt is not written.
	if err := os.Rename(live, candidate.OutputDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(candidate.TransactionRoot, candidate.ID, "receipt.json")); err != nil {
		t.Fatal(err)
	}
	publisher := store.NewDirectoryPublishStore(candidate.TransactionRoot)
	state, err := publisher.LoadDirectoryPublishState(candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state == nil || state.Phase != store.DirectoryPublishLiveArchived {
		t.Fatalf("simulated crash state=%+v, want live_archived", state)
	}
	watchdog, err := newPipelineWatchdog(pipelineWatchdogConfig{
		OutputDir: live, InvocationID: "render-live-archived-recovery", Stage: "render",
		HeartbeatInterval: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := watchdog.Start(); err != nil {
		t.Fatal(err)
	}
	releaseWatchdog, err := bindCurrentPipelineWatchdog(watchdog)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		releaseWatchdog()
		_ = watchdog.Stop()
	})
	if _, err := os.Stat(live); !os.IsNotExist(err) {
		t.Fatalf("stable watchdog control root recreated live before recovery: %v", err)
	}
	if candidate.TransactionRoot != pipelineRenderTransactionRoot(live) {
		t.Fatalf("startup recovery root=%s want=%s", candidate.TransactionRoot, pipelineRenderTransactionRoot(live))
	}
	releaseControl, err := acquirePipelineOutlineAllControl(live, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := recoverAllDirectoryPublishesWithControlHeld(live); err != nil {
		_ = releaseControl()
		t.Fatal(err)
	}
	if err := releaseControl(); err != nil {
		t.Fatal(err)
	}
	finalized, err := publisher.LoadDirectoryPublishState(candidate.ID)
	if err != nil || finalized == nil || finalized.Phase != store.DirectoryPublishFinalized ||
		finalized.Receipt == nil || finalized.Receipt.IntentDigest != receipt.IntentDigest ||
		finalized.Receipt.CandidateRoot != receipt.CandidateRoot ||
		finalized.Receipt.CommittedLiveRoot != receipt.CommittedLiveRoot {
		t.Fatalf("combined recovery returned wrong state: %+v err=%v", finalized, err)
	}
	if got := pipelineRenderCandidateTestSnapshot(t, live); !reflect.DeepEqual(got, wantLive) {
		t.Fatalf("startup recovery restored wrong live tree:\ngot=%v\nwant=%v", got, wantLive)
	}
}

func TestRejectedPipelineRenderCandidateRetiresWithoutChangingLive(t *testing.T) {
	live := pipelineRenderCandidateTestLive(t)
	before := pipelineRenderCandidateTestSnapshot(t, live)
	candidate, err := preparePipelineRenderCandidate(live, pipelineRenderCandidateTestFrozen())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(candidate.OutputDir, "chapters", "01.md"),
		[]byte("rejected candidate body\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := retirePipelineRenderCandidate(candidate.ContainerDir, "rejected"); err != nil {
		t.Fatal(err)
	}
	if got := pipelineRenderCandidateTestSnapshot(t, live); !reflect.DeepEqual(got, before) {
		t.Fatalf("retiring rejected candidate changed live:\ngot=%v\nwant=%v", got, before)
	}
	if _, err := os.Stat(candidate.ContainerDir); !os.IsNotExist(err) {
		t.Fatalf("rejected candidate remained active: %v", err)
	}
	retiredRoot := filepath.Join(pipelineRenderCandidateRoot(live), "retired")
	entries, err := os.ReadDir(retiredRoot)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), candidate.ID+"-rejected-") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("rejected candidate was not retained under %s", retiredRoot)
	}
}

func pipelineRenderCandidateTestFrozen() *pipelineFrozenPlan {
	return &pipelineFrozenPlan{
		Version:                "pipeline-planning.v1",
		Chapter:                1,
		PlanDigest:             "sha256:" + strings.Repeat("1", 64),
		PlanCheckpointSeq:      1,
		PlanningGenerationID:   "pg2_render_candidate_test",
		ProjectionBinding:      "sealed_v2",
		ProjectedBundleDigest:  "sha256:" + strings.Repeat("2", 64),
		PromotionReceiptDigest: "sha256:" + strings.Repeat("3", 64),
		PipelineRunInputDigest: "sha256:" + strings.Repeat("4", 64),
	}
}

func pipelineRenderCandidateTestLive(t *testing.T) string {
	t.Helper()
	live := filepath.Join(t.TempDir(), "output", "novel")
	for rel, body := range map[string]string{
		"chapters/01.md":             "live chapter body\n",
		"meta/state.json":            "live-state\n",
		"nested/ledger/events.jsonl": "{\"event\":\"live\"}\n",
	} {
		path := filepath.Join(live, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return live
}

func pipelineRenderCandidateTestSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := make(map[string]string)
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = string(body)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}
