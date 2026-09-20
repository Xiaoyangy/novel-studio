package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

// The unchanged-candidate control must publish. The second case differs only
// by the same live-bound BeforeProviderCall check used by sealed render; no
// provider or prose/review simulator is invoked in either case.
func TestRenderBudgetCheckDoesNotInvalidatePreparedPublication(t *testing.T) {
	for _, check := range []bool{false, true} {
		t.Run(map[bool]string{false: "without-check-control", true: "with-live-budget-check"}[check], func(t *testing.T) {
			st, generation := chapterDeliverySealedDispatchFixture(t, true)
			frozen, _ := pipelineRenderV3StyleEpochTestFrozen(t, st.Dir())
			frozen.PlanningGenerationID = generation.GenerationID
			guard := pipelineGenerationDeliveryGuard(st, generation, 1)
			if guard == nil {
				t.Fatal("fixture did not bind the 1200-second generation budget")
			}
			cfg := bootstrap.Config{OutputDir: st.Dir(), BeforeProviderCall: guard.Check}
			candidate, err := preparePipelineRenderCandidate(st.Dir(), frozen)
			publicationArtifactMust(t, err)
			before := pipelineRenderCandidateTestSnapshot(t, st.Dir())
			rootBefore, err := store.DirectoryContentRoot(st.Dir())
			publicationArtifactMust(t, err)
			if rootBefore != candidate.SourceLiveRoot {
				t.Fatal("preparation already had unrelated live drift")
			}
			if check {
				publicationArtifactMust(t, cfg.BeforeProviderCall())
			}
			after := pipelineRenderCandidateTestSnapshot(t, st.Dir())
			var changed []string
			for name, raw := range before {
				if after[name] != raw {
					changed = append(changed, name)
				}
			}
			for name := range after {
				if _, ok := before[name]; !ok {
					changed = append(changed, name)
				}
			}
			rootAfter, err := store.DirectoryContentRoot(st.Dir())
			publicationArtifactMust(t, err)
			if check {
				if !reflect.DeepEqual(changed, []string{"meta/runtime/chapter_delivery/ledger.json"}) {
					t.Fatalf("budget check changed unexpected files: %v", changed)
				}
				t.Logf("budget check changed only %s; prepared root=%s checked root=%s", changed[0], rootBefore, rootAfter)
			} else if len(changed) != 0 {
				t.Fatalf("control acquired unrelated writes: %v", changed)
			}
			receipt, err := publishPipelineRenderCandidate(st.Dir(), candidate)
			if err != nil {
				if _, statErr := os.Stat(filepath.Join(candidate.OutputDir, "meta/planning/render_candidate.json")); statErr != nil {
					t.Fatalf("failed publish also lost candidate: %v", statErr)
				}
				t.Fatalf("a permitted budget check must not make a prepared sealed candidate unpublishable (changed=%v): %v", changed, err)
			}
			published := pipelineRenderCandidateTestSnapshot(t, st.Dir())
			if published["meta/runtime/chapter_delivery/ledger.json"] != after["meta/runtime/chapter_delivery/ledger.json"] {
				t.Fatal("publication rolled back or rewrote the last successful live budget observation")
			}
			manifest, err := loadPipelineRenderCandidateManifest(st.Dir())
			publicationArtifactMust(t, err)
			if manifest.SourceLiveRoot != receipt.BeforeLiveRoot {
				t.Fatal("published manifest no longer binds the actual publication receipt")
			}
			// The candidate directory was renamed away. A durable transaction must
			// still recover without trying to synchronize a now-missing directory.
			_, err = publishPipelineRenderCandidate(st.Dir(), candidate)
			publicationArtifactMust(t, err)
		})
	}
}

func TestRenderBudgetSynchronizationStillRequiresFinalPublishCAS(t *testing.T) {
	st, generation := chapterDeliverySealedDispatchFixture(t, true)
	frozen, _ := pipelineRenderV3StyleEpochTestFrozen(t, st.Dir())
	frozen.PlanningGenerationID = generation.GenerationID
	candidate, err := preparePipelineRenderCandidate(st.Dir(), frozen)
	publicationArtifactMust(t, err)
	guard := pipelineGenerationDeliveryGuard(st, generation, 1)
	publicationArtifactMust(t, guard.Check())
	root, err := st.SyncChapterDeliveryBudgetForPublish(candidate.OutputDir, candidate.SourceLiveRoot)
	publicationArtifactMust(t, err)
	rootAgain, err := st.SyncChapterDeliveryBudgetForPublish(candidate.OutputDir, root)
	publicationArtifactMust(t, err)
	if rootAgain != root {
		t.Fatal("synchronization invented a live observation")
	}
	publicationArtifactMust(t, guard.Check())
	before := pipelineRenderCandidateTestSnapshot(t, st.Dir())
	_, err = store.NewDirectoryPublishStore(candidate.TransactionRoot).PublishDirectory(store.PublishDirectoryRequest{
		TransactionID: candidate.ID, LiveDir: st.Dir(), CandidateDir: candidate.OutputDir, ExpectedLiveRoot: root,
	})
	if err == nil || !strings.Contains(err.Error(), "live directory changed after candidate snapshot") {
		t.Fatalf("a later concurrent budget update must still fail the original CAS: %v", err)
	}
	if !reflect.DeepEqual(before, pipelineRenderCandidateTestSnapshot(t, st.Dir())) {
		t.Fatal("failed CAS changed live data")
	}
	// A later explicit attempt can synchronize again; it does not replace or
	// suppress the ordinary CAS used above.
	root, err = st.SyncChapterDeliveryBudgetForPublish(candidate.OutputDir, root)
	publicationArtifactMust(t, err)
	_, err = store.NewDirectoryPublishStore(candidate.TransactionRoot).PublishDirectory(store.PublishDirectoryRequest{
		TransactionID: candidate.ID, LiveDir: st.Dir(), CandidateDir: candidate.OutputDir, ExpectedLiveRoot: root,
	})
	publicationArtifactMust(t, err)
	if got := pipelineRenderCandidateTestSnapshot(t, st.Dir()); got["meta/runtime/chapter_delivery/ledger.json"] != before["meta/runtime/chapter_delivery/ledger.json"] {
		t.Fatal("retry published stale timing")
	}
}

func TestRenderBudgetPublishAdvancesExistingPublishedProof(t *testing.T) {
	t.Run("direct", func(t *testing.T) { testRenderBudgetPublishedProof(t, false) })
	t.Run("crash-after-ledger-sync", func(t *testing.T) { testRenderBudgetPublishedProof(t, true) })
}

func testRenderBudgetPublishedProof(t *testing.T, crashAfterSync bool) {
	t.Helper()
	st, generation := chapterDeliverySealedDispatchFixture(t, true)
	live := st.Dir()
	frozen, plan := pipelineRenderV3StyleEpochTestFrozen(t, live)
	st = store.NewStore(live)
	frozen.PlanningGenerationID = generation.GenerationID
	frozen.EffectiveStyleProtocol = "" // Explicit existing legacy style fixture.
	frozen.PlanPath = "drafts/01.plan.json"
	frozen.PlanningDependencyRoot = pipelineBytesSHA([]byte("planning-dependencies"))
	frozen.ProjectedPlanSHA256 = pipelineBytesSHA([]byte("projected-plan"))
	frozen.ProjectedPreStateRoot = pipelineBytesSHA([]byte("projected-pre-state"))
	frozen.ProjectedPostStateRoot = pipelineBytesSHA([]byte("projected-post-state"))
	renderContext := freezeTestDraftRenderContext(t, st, 1, plan.Digest)
	var err error
	frozen.RenderDependencySHA256, err = capturePipelineFrozenRenderDependencies(live)
	publicationArtifactMust(t, err)
	frozen.RenderContextPath = tools.FrozenDraftRenderContextPath
	frozen.RenderContextSHA256 = renderContext.PayloadSHA256
	frozen.FrozenAt = renderContext.FrozenAt
	_, err = writePipelinePlanningJSON(filepath.Join(live, pipelineFrozenPlanPath), frozen)
	publicationArtifactMust(t, err)
	id, err := pipelineRenderTransactionID(frozen)
	publicationArtifactMust(t, err)
	candidate, err := prepareFreshPipelineRenderCandidateForStyleEpoch(live, frozen, id, filepath.Join(pipelineRenderCandidateRoot(live), id), false)
	publicationArtifactMust(t, err)
	mustUseLegacyPipelineRenderCandidateForTest(t, candidate, frozen)
	const body = "第一章\n\n门外脚步停住。林澈把录音时间写在纸角，随后关掉了灯。"
	cs := store.NewStore(candidate.OutputDir)
	publicationArtifactMust(t, cs.Drafts.SaveDraft(1, body))
	_, err = cs.Checkpoints.AppendArtifactLatestAcross(domain.ChapterScope(1), "draft", "drafts/01.draft.md", "plan", "rerender-request", "draft", "edit")
	publicationArtifactMust(t, err)
	publicationArtifactMust(t, cs.Drafts.SaveFinalChapter(1, body))
	_, err = cs.Checkpoints.AppendArtifactLatestAcross(domain.ChapterScope(1), "commit", "chapters/01.md", "plan", "draft", "edit", "commit")
	publicationArtifactMust(t, err)
	publicationArtifactMust(t, cs.Progress.Save(&domain.Progress{GenerationID: generation.GenerationID, CompletedChapters: []int{1}}))
	snapshot, err := loadPipelineRenderedChapterSnapshot(candidate.OutputDir, frozen, plan)
	publicationArtifactMust(t, err)
	_, err = pipelineEnsureChapterRenderCommitted(live, candidate.OutputDir, frozen, plan, snapshot)
	publicationArtifactMust(t, err)
	mustWriteCurrentReviewArtifactsWithVerdict(t, candidate.OutputDir, 1, "accept")
	publicationArtifactMust(t, pipelineAdvanceChapterRenderFormal(live, candidate.OutputDir, frozen, plan, snapshot, true))
	publicationArtifactMust(t, savePipelineSealedActualMatch(candidate.OutputDir, pipelineChapterRenderTransactionTestMatch()))
	publicationArtifactMust(t, pipelineAdvanceChapterRenderActualMatch(live, candidate.OutputDir, frozen, snapshot.BodySHA256))
	publicationArtifactMust(t, pipelineGenerationDeliveryGuard(st, generation, 1).Check())
	if crashAfterSync {
		beforeLive := pipelineRenderCandidateTestSnapshot(t, live)
		oldRoot := candidate.SourceLiveRoot
		newRoot, err := st.SyncChapterDeliveryBudgetForPublish(candidate.OutputDir, oldRoot)
		publicationArtifactMust(t, err)
		manifest, err := loadPipelineRenderCandidateManifest(candidate.OutputDir)
		publicationArtifactMust(t, err)
		if newRoot == oldRoot || manifest.SourceLiveRoot != oldRoot {
			t.Fatal("fixture did not stop between ledger sync and manifest update")
		}
		if !reflect.DeepEqual(beforeLive, pipelineRenderCandidateTestSnapshot(t, live)) {
			t.Fatal("isolated ledger sync changed live")
		}
		// A new process follows the normal candidate recovery path; it must
		// authenticate and retain the committed body/review without a model.
		candidate, err = preparePipelineRenderCandidate(live, frozen)
		publicationArtifactMust(t, err)
		if candidate.RecoveredChapterTransactionPhase != domain.ChapterRenderPhaseActualMatched || candidate.SourceLiveRoot != newRoot {
			t.Fatalf("crash did not recover the original accepted candidate: %+v", candidate)
		}
		manifest, err = loadPipelineRenderCandidateManifest(candidate.OutputDir)
		publicationArtifactMust(t, err)
		if manifest.SourceLiveRoot != newRoot {
			t.Fatal("recovery did not repair the hosting root binding")
		}
		if got, err := store.NewStore(candidate.OutputDir).Drafts.LoadDraft(1); err != nil || got != body {
			t.Fatalf("recovery changed original prose: %v", err)
		}
	}
	_, err = publishPipelineRenderCandidate(live, candidate)
	publicationArtifactMust(t, err)
	publicationArtifactMust(t, pipelineAdvanceChapterRenderPublished(live, frozen, plan, snapshot.BodySHA256))
	identity, err := pipelineChapterRenderBodyIdentity(frozen, snapshot.BodySHA256)
	publicationArtifactMust(t, err)
	latest, err := store.NewChapterRenderTransactionStore(live).LoadLatest(identity)
	publicationArtifactMust(t, err)
	if latest == nil || latest.Phase != domain.ChapterRenderPhasePublished {
		t.Fatal("budget synchronization did not reach the existing verified Published transition")
	}
}

func TestRenderBudgetPublishRejectsUnrelatedOrForgedDrift(t *testing.T) {
	for _, change := range []string{"other-live-file", "other-runtime-file", "new-empty-directory", "deadline", "start-and-deadline", "close", "backwards-clock", "future-clock", "corrupt-digest", "missing-live-ledger", "missing-candidate-ledger", "candidate-deadline"} {
		t.Run(change, func(t *testing.T) {
			st, generation := chapterDeliverySealedDispatchFixture(t, true)
			frozen, _ := pipelineRenderV3StyleEpochTestFrozen(t, st.Dir())
			frozen.PlanningGenerationID = generation.GenerationID
			publicationArtifactMust(t, pipelineGenerationDeliveryGuard(st, generation, 1).Check())
			candidate, err := preparePipelineRenderCandidate(st.Dir(), frozen)
			publicationArtifactMust(t, err)
			publicationArtifactMust(t, pipelineGenerationDeliveryGuard(st, generation, 1).Check())
			ledgerPath := filepath.Join(st.Dir(), "meta/runtime/chapter_delivery/ledger.json")
			switch change {
			case "other-live-file", "other-runtime-file":
				rel := "unrelated-author-source.txt"
				if change == "other-runtime-file" {
					rel = "meta/runtime/unrelated.json"
				}
				publicationArtifactMust(t, os.WriteFile(filepath.Join(st.Dir(), rel), []byte("unrelated drift"), 0o644))
			case "new-empty-directory":
				publicationArtifactMust(t, os.Mkdir(filepath.Join(st.Dir(), "unrelated-empty-dir"), 0o755))
			case "missing-live-ledger":
				publicationArtifactMust(t, os.Remove(ledgerPath))
			case "missing-candidate-ledger":
				publicationArtifactMust(t, os.Remove(filepath.Join(candidate.OutputDir, "meta/runtime/chapter_delivery/ledger.json")))
			default:
				if change == "candidate-deadline" {
					ledgerPath = filepath.Join(candidate.OutputDir, "meta/runtime/chapter_delivery/ledger.json")
				}
				mutateRenderBudgetLedgerForTest(t, ledgerPath, generation.GenerationID, change)
			}
			beforeLive := pipelineRenderCandidateTestSnapshot(t, st.Dir())
			beforeCandidate := pipelineRenderCandidateTestSnapshot(t, candidate.OutputDir)
			_, publishErr := publishPipelineRenderCandidate(st.Dir(), candidate)
			if publishErr == nil {
				t.Fatal("unauthorized source/timing drift was published")
			}
			if change != "corrupt-digest" && strings.Contains(publishErr.Error(), "integrity digest mismatch") {
				t.Fatalf("test mutation was not validly rehashed: %v", publishErr)
			}
			if !reflect.DeepEqual(beforeLive, pipelineRenderCandidateTestSnapshot(t, st.Dir())) || !reflect.DeepEqual(beforeCandidate, pipelineRenderCandidateTestSnapshot(t, candidate.OutputDir)) {
				t.Fatal("rejected publication changed source or candidate files")
			}
		})
	}
}

func mutateRenderBudgetLedgerForTest(t *testing.T, path, generationID, change string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	publicationArtifactMust(t, err)
	// Preserve the actual ledger field order for its integrity digest. This
	// proves validly rehashed malicious fields still cannot be merged.
	type generationWire struct {
		GenerationID string                          `json:"generation_id"`
		CreatedAt    string                          `json:"created_at"`
		FirstChapter int                             `json:"first_chapter"`
		LastChapter  int                             `json:"last_chapter"`
		Policy       string                          `json:"policy"`
		LimitSeconds int                             `json:"limit_seconds"`
		ArmedAt      time.Time                       `json:"armed_at"`
		Chapters     []store.ChapterDeliveryTimingV1 `json:"chapters"`
	}
	var ordered struct {
		Version      string                    `json:"version"`
		LastObserved time.Time                 `json:"last_observed"`
		Generations  map[string]generationWire `json:"generations"`
		Digest       string                    `json:"digest"`
	}
	publicationArtifactMust(t, json.Unmarshal(raw, &ordered))
	entry := ordered.Generations[generationID]
	switch change {
	case "backwards-clock":
		ordered.LastObserved = entry.Chapters[0].StartedAt
	case "future-clock":
		ordered.LastObserved = ordered.LastObserved.Add(time.Hour)
	case "close":
		entry.Chapters[0].ClosedAt = ordered.LastObserved
		entry.Chapters[0].AcceptanceReceiptDigest = domain.ComputeArcArtifactSHA256([]byte("forged acceptance"))
	case "start-and-deadline":
		entry.Chapters[0].StartedAt = entry.ArmedAt.Add(entry.Chapters[0].StartedAt.Sub(entry.ArmedAt) / 2)
		entry.Chapters[0].DeadlineAt = entry.Chapters[0].StartedAt.Add(1200 * time.Second)
	case "deadline", "candidate-deadline":
		entry.Chapters[0].DeadlineAt = entry.Chapters[0].DeadlineAt.Add(time.Second)
	}
	ordered.Generations[generationID] = entry
	ordered.Digest = ""
	unsigned, err := json.Marshal(ordered)
	publicationArtifactMust(t, err)
	ordered.Digest = domain.ComputeArcArtifactSHA256(unsigned)
	if change == "corrupt-digest" {
		ordered.Digest = "sha256:" + strings.Repeat("0", 64)
	}
	updated, err := json.MarshalIndent(ordered, "", "  ")
	publicationArtifactMust(t, err)
	if bytes.Equal(raw, updated) {
		t.Fatal("mutation did not change ledger")
	}
	publicationArtifactMust(t, os.WriteFile(path, append(updated, '\n'), 0o644))
}
