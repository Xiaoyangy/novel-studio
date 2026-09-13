package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/stylestat"
)

func chapterDeliveryTestNow() time.Time { return time.Date(2026, 9, 13, 1, 0, 0, 0, time.UTC) }

func chapterDeliveryTestGeneration(t *testing.T, st *Store, attempt string, enabled bool) domain.PlanningGenerationV2 {
	t.Helper()
	g, source, registry, _ := projectedStoreV2FixtureWithAttempt(t, 3, attempt)
	if enabled {
		g.ChapterDeliveryBudget = &domain.ChapterDeliveryBudgetV1{Policy: domain.ChapterDeliveryBudgetPolicyV1, LimitSeconds: 1200}
	}
	var err error
	g.GenerationDigest, err = domain.ComputePlanningGenerationV2Digest(g)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ProjectedV2().CreateBuildingGeneration(g, source, registry); err != nil {
		t.Fatal(err)
	}
	return g
}

func TestChapterDeliveryOriginalDeadlineSurvivesRetryReloadAndLaterWindowChapter(t *testing.T) {
	st := NewStore(t.TempDir())
	g := chapterDeliveryTestGeneration(t, st, "delivery-original", true)
	now := chapterDeliveryTestNow()
	if _, err := st.BeginChapterDelivery(g, 4, now); err == nil {
		t.Fatal("enabled generation silently armed during Begin")
	}
	if err := st.ArmChapterDeliveryBudget(g, now); err != nil {
		t.Fatal(err)
	}
	first, err := st.BeginChapterDelivery(g, 4, now)
	if err != nil || first == nil || !first.DeadlineAt.Equal(now.Add(20*time.Minute)) {
		t.Fatalf("original start: %+v %v", first, err)
	}
	reloaded := NewStore(st.Dir())
	again, err := reloaded.BeginChapterDelivery(g, 4, now.Add(10*time.Minute))
	if err != nil || !reflect.DeepEqual(first, again) {
		t.Fatalf("retry reset original timing: %+v %v", again, err)
	}
	if _, err := reloaded.BeginChapterDelivery(g, 5, now.Add(19*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := reloaded.CheckChapterDeliveryBudget(g.GenerationID, now.Add(20*time.Minute)); !errors.Is(err, ErrChapterDeliveryDeadline) || !strings.Contains(err.Error(), "chapter 4") {
		t.Fatalf("later chapter hid earliest open deadline: %v", err)
	}
	if _, err := reloaded.BeginChapterDelivery(g, 6, now.Add(21*time.Minute)); !errors.Is(err, ErrChapterDeliveryDeadline) {
		t.Fatalf("new chapter work bypassed original expired budget: %v", err)
	}
	if timing, err := reloaded.LoadChapterDeliveryTiming(g.GenerationID, 6); err != nil || timing != nil {
		t.Fatalf("expired guard started chapter 6: %+v %v", timing, err)
	}
	if err := reloaded.ArmChapterDeliveryBudget(g, now.Add(22*time.Minute)); err == nil {
		t.Fatal("rearming reset the ledger")
	}
}

func TestChapterDeliveryBeginRefusesMissingOriginalStartAfterProjectionWithoutWrites(t *testing.T) {
	for _, mode := range []string{"projected", "sealed"} {
		for _, hadStart := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/original_start_%v", mode, hadStart), func(t *testing.T) {
				f := newPlanningWindowStoreFixture(t, 6, true)
				f.generation.ChapterDeliveryBudget = &domain.ChapterDeliveryBudgetV1{Policy: domain.ChapterDeliveryBudgetPolicyV1, LimitSeconds: 1200}
				planningWindowStoreRebind(t, &f)
				if err := f.st.ProjectedV2().CreateBuildingGeneration(f.generation, f.source, f.registry); err != nil {
					t.Fatal(err)
				}
				now := chapterDeliveryTestNow()
				if err := f.st.ArmChapterDeliveryBudget(f.generation, now); err != nil {
					t.Fatal(err)
				}
				var original *ChapterDeliveryTimingV1
				if hadStart {
					var err error
					original, err = f.st.BeginChapterDelivery(f.generation, 1, now)
					if err != nil {
						t.Fatal(err)
					}
				}
				bundles := planningWindowStoreBundles(t, f)
				if mode == "projected" {
					bundles = bundles[:1]
				}
				planningWindowStorePublish(t, f, bundles)
				if mode == "sealed" {
					if _, err := f.st.ProjectedV2().SealGeneration(f.generation.GenerationID); err != nil {
						t.Fatal(err)
					}
				}
				reloaded := NewStore(f.st.Dir())
				current, err := reloaded.loadChapterDeliveryGeneration(f.generation.GenerationID)
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(f.st.Dir(), chapterDeliveryLedgerPath)
				before, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				timing, beginErr := reloaded.BeginChapterDelivery(*current, 1, now.Add(time.Minute))
				if hadStart {
					if beginErr != nil || !reflect.DeepEqual(original, timing) {
						t.Fatalf("valid preprojection original start failed recovery: %+v %v", timing, beginErr)
					}
					return
				}
				if beginErr == nil || timing != nil || !strings.Contains(beginErr.Error(), "original start is missing") {
					t.Fatalf("postprojection Begin fabricated missing original start: %+v %v", timing, beginErr)
				}
				after, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(before, after) {
					t.Fatalf("rejected postprojection Begin rewrote ledger: %v", err)
				}
				if mode == "projected" {
					next, err := reloaded.BeginChapterDelivery(*current, 2, now.Add(time.Minute))
					if err != nil || next == nil || !next.StartedAt.Equal(now.Add(time.Minute)) {
						t.Fatalf("unprojected next chapter lost legitimate first Begin: %+v %v", next, err)
					}
				}
			})
		}
	}
}

func TestChapterDeliveryClockMissingCorruptAndPolicyFailClosed(t *testing.T) {
	for _, mode := range []string{"backwards", "zero-clock", "missing", "corrupt", "missing-row", "policy", "forged-generation"} {
		t.Run(mode, func(t *testing.T) {
			st := NewStore(t.TempDir())
			g := chapterDeliveryTestGeneration(t, st, "delivery-integrity", true)
			now := chapterDeliveryTestNow()
			if err := st.ArmChapterDeliveryBudget(g, now); err != nil {
				t.Fatal(err)
			}
			if _, err := st.BeginChapterDelivery(g, 4, now); err != nil {
				t.Fatal(err)
			}
			checkAt := now.Add(time.Second)
			switch mode {
			case "backwards":
				checkAt = now.Add(-time.Nanosecond)
			case "zero-clock":
				checkAt = time.Time{}
			case "missing":
				if err := os.Remove(filepath.Join(st.Dir(), chapterDeliveryLedgerPath)); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				if err := os.WriteFile(filepath.Join(st.Dir(), chapterDeliveryLedgerPath), []byte("{}"), 0o644); err != nil {
					t.Fatal(err)
				}
			case "missing-row":
				ledger, err := st.readChapterDeliveryLedger()
				if err != nil {
					t.Fatal(err)
				}
				entry := ledger.Generations[g.GenerationID]
				entry.Chapters = entry.Chapters[1:]
				ledger.Generations[g.GenerationID] = entry
				if err := newIO(st.Dir()).WriteJSON(chapterDeliveryLedgerPath, ledger); err != nil {
					t.Fatal(err)
				}
			case "policy":
				g.ChapterDeliveryBudget = nil
				g.GenerationDigest, _ = domain.ComputePlanningGenerationV2Digest(g)
			case "forged-generation":
				g.CreatedAt = now.Add(time.Hour).Format(time.RFC3339)
				g.GenerationDigest, _ = domain.ComputePlanningGenerationV2Digest(g)
			}
			if _, err := st.BeginChapterDelivery(g, 4, checkAt); err == nil {
				t.Fatalf("%s silently reset or bypassed budget", mode)
			}
		})
	}
}

func TestChapterDeliveryRejectsReplacementIncludingLegacyAndKeepsLegacyReadOnly(t *testing.T) {
	st := NewStore(t.TempDir())
	now := chapterDeliveryTestNow()
	legacy := chapterDeliveryTestGeneration(t, st, "legacy-delivery", false)
	if err := st.ArmChapterDeliveryBudget(legacy, now); err != nil {
		t.Fatal(err)
	}
	if timing, err := st.BeginChapterDelivery(legacy, 4, now); err != nil || timing != nil {
		t.Fatalf("legacy behavior: %+v %v", timing, err)
	}
	if err := st.CheckChapterDeliveryBudget(legacy.GenerationID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(st.Dir(), chapterDeliveryRoot)); !os.IsNotExist(err) {
		t.Fatal("legacy nil policy created runtime timing files")
	}
	g := chapterDeliveryTestGeneration(t, st, "enabled-delivery", true)
	if err := st.ArmChapterDeliveryBudget(g, now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.BeginChapterDelivery(g, 4, now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.BeginChapterDelivery(legacy, 4, now.Add(time.Second)); err == nil {
		t.Fatal("legacy replacement erased existing enabled budget")
	}
	replacement := chapterDeliveryTestGeneration(t, st, "replacement-delivery", true)
	if err := st.ArmChapterDeliveryBudget(replacement, now.Add(time.Second)); err == nil {
		t.Fatal("new generation replaced an open chapter deadline")
	}
	if err := os.Remove(filepath.Join(st.Dir(), chapterDeliveryLedgerPath)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.BeginChapterDelivery(legacy, 4, now.Add(time.Second)); err == nil {
		t.Fatal("missing old enabled ledger allowed legacy replacement")
	}
}

func chapterDeliveryAcceptedFixture(t *testing.T) (*Store, domain.PlanningGenerationV2, []domain.ChapterAcceptanceReceipt) {
	t.Helper()
	f := newPlanningWindowStoreFixture(t, 6, true)
	f.generation.ChapterDeliveryBudget = &domain.ChapterDeliveryBudgetV1{Policy: domain.ChapterDeliveryBudgetPolicyV1, LimitSeconds: 1200}
	planningWindowStoreRebind(t, &f)
	if err := f.st.ProjectedV2().CreateBuildingGeneration(f.generation, f.source, f.registry); err != nil {
		t.Fatal(err)
	}
	now := chapterDeliveryTestNow()
	if err := f.st.ArmChapterDeliveryBudget(f.generation, now); err != nil {
		t.Fatal(err)
	}
	for chapter := 1; chapter <= 3; chapter++ {
		if _, err := f.st.BeginChapterDelivery(f.generation, chapter, now); err != nil {
			t.Fatal(err)
		}
	}
	arcWindowAggregateAcceptWindow(t, f)
	g, err := f.st.ProjectedV2().LoadSealedGeneration(f.generation.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	acceptances, err := f.st.ArcCycle().ListChapterAcceptanceReceipts(g.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	return f.st, *g, acceptances
}

func TestChapterDeliveryClosesOnlyExactPersistedAcceptanceAndPreservesFinish(t *testing.T) {
	st, g, acceptances := chapterDeliveryAcceptedFixture(t)
	now := chapterDeliveryTestNow().Add(10 * time.Minute)
	fake := acceptances[0]
	fake.AcceptedAt = now.Format(time.RFC3339)
	fake, _ = domain.SignChapterAcceptanceReceipt(fake)
	if _, err := st.CompleteChapterDelivery(g, fake, now); err == nil {
		t.Fatal("unpersisted replacement acceptance closed timing")
	}
	closed, err := st.CompleteChapterDelivery(g, acceptances[0], now)
	if err != nil || !closed.ClosedAt.Equal(now) || closed.Recovered || closed.TimingUnknown || closed.RecoveredAfterDeadline || closed.AcceptanceReceiptDigest != acceptances[0].ReceiptDigest {
		t.Fatalf("verified close: %+v %v", closed, err)
	}
	again, err := NewStore(st.Dir()).CompleteChapterDelivery(g, acceptances[0], now.Add(time.Hour))
	if err != nil || !reflect.DeepEqual(closed, again) {
		t.Fatalf("retry changed original finish: %+v %v", again, err)
	}
}

func TestChapterDeliveryRecoveryAfterAcceptanceRecordsUnknownLateCloseWithoutBlocking(t *testing.T) {
	st, g, acceptances := chapterDeliveryAcceptedFixture(t)
	now := chapterDeliveryTestNow().Add(time.Hour)
	reloaded := NewStore(st.Dir())
	if err := reloaded.CheckChapterDeliveryBudget(g.GenerationID, now); err != nil {
		t.Fatalf("real accepted chapters permanently blocked mechanical recovery: %v", err)
	}
	for _, acceptance := range acceptances {
		timing, err := reloaded.LoadChapterDeliveryTiming(g.GenerationID, acceptance.Chapter)
		if err != nil || timing == nil || !timing.StartedAt.Equal(chapterDeliveryTestNow()) || !timing.ClosedAt.Equal(now) || !timing.Recovered || !timing.TimingUnknown || !timing.RecoveredAfterDeadline {
			t.Fatalf("recovery fabricated start/finish or reported compliant sample: %+v %v", timing, err)
		}
	}
}

func TestChapterDeliveryOutcomeAloneNeverClosesEvenWithEarlyAcceptedAt(t *testing.T) {
	st, g, acceptances := chapterDeliveryAcceptedFixture(t)
	for _, acceptance := range acceptances {
		path := filepath.Join(st.Dir(), arcCycleAcceptancePath(g.GenerationID, acceptance.Chapter, acceptance.ReceiptDigest))
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Dir(path)); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.CheckChapterDeliveryBudget(g.GenerationID, chapterDeliveryTestNow().Add(time.Hour)); !errors.Is(err, ErrChapterDeliveryDeadline) {
		t.Fatalf("early outcome timestamp replaced formal acceptance: %v", err)
	}
	timing, err := st.LoadChapterDeliveryTiming(g.GenerationID, 1)
	if err != nil || timing == nil || !timing.ClosedAt.IsZero() || timing.AcceptanceReceiptDigest != "" {
		t.Fatalf("outcome-only chapter was closed: %+v %v", timing, err)
	}
}

func TestChapterDeliveryConcurrentStoresKeepOneOriginalStart(t *testing.T) {
	st := NewStore(t.TempDir())
	g := chapterDeliveryTestGeneration(t, st, "concurrent-delivery", true)
	now := chapterDeliveryTestNow()
	if err := st.ArmChapterDeliveryBudget(g, now); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			timing, err := NewStore(st.Dir()).BeginChapterDelivery(g, 4, now)
			if err != nil || timing == nil || !timing.StartedAt.Equal(now) || !timing.DeadlineAt.Equal(now.Add(20*time.Minute)) {
				t.Errorf("concurrent begin lost original timing: %+v %v", timing, err)
			}
		}()
	}
	wg.Wait()
}

func TestChapterDeliveryCheckSamplesClockOnlyInsideLedgerTransaction(t *testing.T) {
	st := NewStore(t.TempDir())
	g := chapterDeliveryTestGeneration(t, st, "clock-admission-delivery", true)
	now := chapterDeliveryTestNow()
	if err := st.ArmChapterDeliveryBudget(g, now); err != nil {
		t.Fatal(err)
	}
	st.crossMu.Lock()
	sampled, finished := make(chan struct{}), make(chan error, 1)
	go func() {
		finished <- st.checkChapterDeliveryBudget(g.GenerationID, func() time.Time {
			close(sampled)
			return now.Add(2 * time.Second)
		})
	}()
	select {
	case <-sampled:
		st.crossMu.Unlock()
		t.Fatal("waiting caller sampled wall clock before entering ledger transaction")
	case <-time.After(30 * time.Millisecond):
	}
	ledger, err := st.readChapterDeliveryLedger()
	if err == nil {
		err = chapterDeliveryObserve(ledger, now.Add(time.Second))
	}
	if err == nil {
		err = st.writeChapterDeliveryLedger(ledger)
	}
	st.crossMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := <-finished; err != nil {
		t.Fatalf("lock admission order falsely reported clock rollback: %v", err)
	}
}

func TestChapterDeliveryMutationsSampleClockOnlyInsideLedgerTransaction(t *testing.T) {
	for _, operation := range []string{"arm", "begin", "complete"} {
		t.Run(operation, func(t *testing.T) {
			st := NewStore(t.TempDir())
			now := chapterDeliveryTestNow()
			g := chapterDeliveryTestGeneration(t, st, "clock-mutation-anchor", true)
			if err := st.ArmChapterDeliveryBudget(g, now); err != nil {
				t.Fatal(err)
			}
			var acceptance domain.ChapterAcceptanceReceipt
			if operation == "arm" {
				g = chapterDeliveryTestGeneration(t, st, "clock-mutation-new", true)
			} else if operation == "complete" {
				var acceptances []domain.ChapterAcceptanceReceipt
				st, g, acceptances = chapterDeliveryAcceptedFixture(t)
				acceptance = acceptances[0]
			}
			sampled, finished := make(chan struct{}), make(chan error, 1)
			clock := func() time.Time {
				close(sampled)
				return now.Add(2 * time.Second)
			}
			st.crossMu.Lock()
			go func() {
				var err error
				switch operation {
				case "arm":
					err = st.armChapterDeliveryBudget(g, clock)
				case "begin":
					_, err = st.beginChapterDelivery(g, g.FirstProjectedChapter, clock)
				case "complete":
					_, err = st.completeChapterDelivery(g, acceptance, clock)
				}
				finished <- err
			}()
			select {
			case <-sampled:
				st.crossMu.Unlock()
				t.Fatal("mutation sampled clock before entering ledger transaction")
			case <-time.After(30 * time.Millisecond):
			}
			ledger, err := st.readChapterDeliveryLedger()
			if err == nil {
				err = chapterDeliveryObserve(ledger, now.Add(time.Second))
			}
			if err == nil {
				err = st.writeChapterDeliveryLedger(ledger)
			}
			st.crossMu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if err := <-finished; err != nil {
				t.Fatalf("mutation lock admission caused false clock rollback: %v", err)
			}
		})
	}
}

func TestChapterDeliveryNewGenerationCannotHideEarlierOpenChapterDeadline(t *testing.T) {
	st := NewStore(t.TempDir())
	now := chapterDeliveryTestNow()
	first := chapterDeliveryTestGeneration(t, st, "earlier-window-delivery", true)
	if err := st.ArmChapterDeliveryBudget(first, now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.BeginChapterDelivery(first, 4, now); err != nil {
		t.Fatal(err)
	}
	second, source, registry, _ := projectedStoreV2FixtureWithAttempt(t, 3, "later-window-delivery")
	second.BaseCanonChapter, source.BaseCanonChapter = 6, 6
	second.FirstProjectedChapter, second.LastProjectedChapter = 7, 9
	registry.FirstChapter, registry.LastChapter = 7, 9
	registry.RegistryRoot, _ = domain.ComputeObligationRegistryV2Root(registry)
	second.ObligationRegistryRoot = registry.RegistryRoot
	second.ChapterDeliveryBudget = &domain.ChapterDeliveryBudgetV1{Policy: domain.ChapterDeliveryBudgetPolicyV1, LimitSeconds: 1200}
	second.GenerationDigest, _ = domain.ComputePlanningGenerationV2Digest(second)
	source.SnapshotDigest, _ = domain.ComputePlanningSourceSnapshotV2Digest(source)
	if err := st.ProjectedV2().CreateBuildingGeneration(second, source, registry); err != nil {
		t.Fatal(err)
	}
	if err := st.ArmChapterDeliveryBudget(second, now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := st.CheckChapterDeliveryBudget(second.GenerationID, now.Add(20*time.Minute)); !errors.Is(err, ErrChapterDeliveryDeadline) || !strings.Contains(err.Error(), first.GenerationID) {
		t.Fatalf("checking later generation hid earlier deadline: %v", err)
	}
	if _, err := st.BeginChapterDelivery(second, 7, now.Add(21*time.Minute)); !errors.Is(err, ErrChapterDeliveryDeadline) {
		t.Fatalf("later generation began despite earlier open expired chapter: %v", err)
	}
}

func TestChapterDeliveryClosedTimingRevalidatesItsOriginalEvidence(t *testing.T) {
	for _, mode := range []string{"acceptance", "outcome", "body", "review"} {
		t.Run(mode, func(t *testing.T) {
			st, g, acceptances := chapterDeliveryAcceptedFixture(t)
			now := chapterDeliveryTestNow().Add(time.Minute)
			if err := st.CheckChapterDeliveryBudget(g.GenerationID, now); err != nil {
				t.Fatal(err)
			}
			acceptance := acceptances[0]
			var rel string
			switch mode {
			case "acceptance":
				rel = arcCycleAcceptancePath(g.GenerationID, 1, acceptance.ReceiptDigest)
			case "outcome":
				rel = projectedActualOutcomePath(g.GenerationID, 1, acceptance.OutcomeReceiptDigest)
			case "body":
				rel = "chapters/01.md"
			case "review":
				rel = acceptance.ReviewArtifacts[0].Path
			}
			if err := os.Remove(filepath.Join(st.Dir(), rel)); err != nil {
				t.Fatal(err)
			}
			if mode == "acceptance" {
				if err := os.Remove(filepath.Dir(filepath.Join(st.Dir(), rel))); err != nil {
					t.Fatal(err)
				}
			}
			if err := st.CheckChapterDeliveryBudget(g.GenerationID, now.Add(time.Minute)); err == nil {
				t.Fatalf("ClosedAt hid deleted %s evidence", mode)
			}
		})
	}
}

func TestChapterDeliveryClosedProofFilesReloadAndDetectEveryByteChange(t *testing.T) {
	st, g, acceptances := chapterDeliveryAcceptedFixture(t)
	now := chapterDeliveryTestNow().Add(time.Minute)
	if err := st.CheckChapterDeliveryBudget(g.GenerationID, now); err != nil {
		t.Fatal(err)
	}
	timing, err := st.LoadChapterDeliveryTiming(g.GenerationID, 1)
	if err != nil || timing == nil || len(timing.ProofFiles) < 10 {
		t.Fatalf("closed timing lacks its exact authenticated file inventory: %+v %v", timing, err)
	}
	for _, proof := range timing.ProofFiles {
		t.Run(proof.Path, func(t *testing.T) {
			path := filepath.Join(st.Dir(), proof.Path)
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := os.WriteFile(path, original, 0o644); err != nil {
					t.Error(err)
				}
			}()
			if err := os.WriteFile(path, append(append([]byte(nil), original...), '\n'), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := NewStore(st.Dir()).CheckChapterDeliveryBudget(g.GenerationID, now.Add(time.Second)); err == nil {
				t.Fatalf("closed receipt trusted changed bytes at %s", proof.Path)
			}
		})
	}
	if err := st.verifyChapterDeliveryProofFiles(acceptances[0], timing.ProofFiles[1:]); err == nil {
		t.Fatal("closed proof accepted an omitted exact path")
	}
	foreign := append([]ChapterDeliveryProofFileV1(nil), timing.ProofFiles...)
	foreign[0].Path = "chapters/99.md"
	if err := st.verifyChapterDeliveryProofFiles(acceptances[0], foreign); err == nil {
		t.Fatal("closed proof accepted a substituted chapter path")
	}
}

func TestChapterDeliveryOldClosedTimingBindsProofWithoutChangingOriginalClose(t *testing.T) {
	st, g, acceptances := chapterDeliveryAcceptedFixture(t)
	now := chapterDeliveryTestNow().Add(time.Minute)
	original, err := st.CompleteChapterDelivery(g, acceptances[0], now)
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := st.readChapterDeliveryLedger()
	if err != nil {
		t.Fatal(err)
	}
	entry := ledger.Generations[g.GenerationID]
	entry.Chapters[0].ProofFiles = nil
	ledger.Generations[g.GenerationID] = entry
	if err := st.writeChapterDeliveryLedger(ledger); err != nil {
		t.Fatal(err)
	}
	if err := NewStore(st.Dir()).CheckChapterDeliveryBudget(g.GenerationID, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	bound, err := st.LoadChapterDeliveryTiming(g.GenerationID, 1)
	if err != nil || bound == nil || len(bound.ProofFiles) == 0 || !reflect.DeepEqual(original, bound) {
		t.Fatalf("old closed timing changed original timing or failed full proof binding: %+v %v", bound, err)
	}
}

func TestChapterDeliveryProofInventoryPreservesArcMetadataHardlinkContract(t *testing.T) {
	st, g, acceptances := chapterDeliveryAcceptedFixture(t)
	for _, path := range []string{arcCycleAcceptancePath(g.GenerationID, 1, acceptances[0].ReceiptDigest), arcCycleManifestPath(g.GenerationID, acceptances[0].ArcManifestDigest)} {
		if err := os.Link(filepath.Join(st.Dir(), path), filepath.Join(t.TempDir(), "metadata.json")); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.CheckChapterDeliveryBudget(g.GenerationID, chapterDeliveryTestNow().Add(time.Minute)); err != nil {
		t.Fatalf("legitimate ArcCycle metadata hard links blocked first close: %v", err)
	}
	if err := NewStore(st.Dir()).CheckChapterDeliveryBudget(g.GenerationID, chapterDeliveryTestNow().Add(2*time.Minute)); err != nil {
		t.Fatalf("legitimate ArcCycle metadata hard links blocked closed check: %v", err)
	}
}

func TestChapterDeliveryClosedEvidenceChecksUseLessAllocationThanFullReplay(t *testing.T) {
	st, g, acceptances := chapterDeliveryAcceptedFixture(t)
	timing, err := st.CompleteChapterDelivery(g, acceptances[0], chapterDeliveryTestNow().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	full := testing.AllocsPerRun(1, func() {
		if err := st.authenticateChapterDeliveryAcceptance(acceptances[0]); err != nil {
			t.Fatal(err)
		}
	})
	exact := testing.AllocsPerRun(1, func() {
		if err := st.verifyChapterDeliveryProofFiles(acceptances[0], timing.ProofFiles); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("single closed chapter exact-file check %.0f allocations; full acceptance replay %.0f allocations", exact, full)
	if exact >= full {
		t.Fatalf("closed evidence check did not remove full replay allocation cost: exact %.0f >= full %.0f", exact, full)
	}
}

func TestChapterDeliveryClosedStyleProofKeepsExplicitEarlierSourceBodies(t *testing.T) {
	st, g, acceptances := chapterDeliveryAcceptedFixture(t)
	acceptance := acceptances[1]
	bundles, err := st.ProjectedV2().LoadProjectedChapterBundles(g.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := st.ProjectedV2().LoadActualOutcomeReceipt(g.GenerationID, 2, acceptance.OutcomeReceiptDigest)
	if err != nil {
		t.Fatal(err)
	}
	sourceBody, err := os.ReadFile(filepath.Join(st.Dir(), "chapters/01.md"))
	if err != nil {
		t.Fatal(err)
	}
	contract := json.RawMessage(`{"version":3,"usage_policy":"surface-only"}`)
	style := renderPermitEffectiveStyleContractReceipt{
		Version: renderPermitEffectiveStyleContractVersion, GenerationID: g.GenerationID, Chapter: 2,
		PlanDigest: arcCycleStoreTestDigest("delivery-style-plan"), PlanCheckpointSeq: 1,
		BaseRenderContextSHA256: arcCycleStoreTestDigest("delivery-style-context"), PipelineRenderInputDigest: arcCycleStoreTestDigest("delivery-style-input"),
		ProjectedBundleDigest: bundles[1].BundleDigest, PromotionReceiptDigest: outcome.PromotionReceiptDigest,
		CandidateID: "delivery-style-source", StyleID: "delivery-style", StyleAssetSHA256: arcCycleStoreTestDigest("delivery-style-asset"),
		StyleContractProtocol: renderPermitStyleContractProtocolVersion, StyleContract: contract, StyleContractSHA256: renderPermitEffectiveStyleSHA256(contract),
		SerialMemoryCompletedSet: []int{1}, SourceChapterBodies: []renderPermitEffectiveStyleSourceBody{{Chapter: 1, BodySHA256: renderPermitEffectiveStyleSHA256(sourceBody)}},
		SerialMemoryStopwords: []string{}, SerialMemoryCompiler: stylestat.SerialMemoryCompilerProtocolVersion, CreatedAt: projectedStoreV2Time(),
	}
	style.SerialMemoryCompilerRoot = stylestat.SerialMemoryCompilerRoot(style.SerialMemoryCompletedSet, style.SourceChapterBodies, style.SerialMemoryStopwords)
	style.ReceiptDigest, err = renderPermitEffectiveStyleReceiptDigest(style)
	if err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join("meta/planning/effective_render_style_contracts/ch0002", style.CandidateID, strings.TrimPrefix(style.ReceiptDigest, "sha256:")+".json")
	archiveRaw, err := json.Marshal(style)
	if err != nil {
		t.Fatal(err)
	}
	if err := newIO(st.Dir()).WriteFileUnlocked(archivePath, archiveRaw); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{".md", "_ai_voice_redflags.json", "_deepseek_ai_judge.json", "_model_provenance.json"} {
		path, raw := "reviews/02"+suffix, []byte(`{"chapter":2}`)
		if err := newIO(st.Dir()).WriteFileUnlocked(path, raw); err != nil {
			t.Fatal(err)
		}
		acceptance.ReviewArtifacts = append(acceptance.ReviewArtifacts, domain.ChapterReviewArtifactBinding{Path: path, Digest: domain.ComputeArcArtifactSHA256(raw)})
	}
	acceptance.Version = domain.ChapterAcceptanceReceiptVersion
	acceptance.ReviewArtifacts = domain.CanonicalChapterReviewArtifacts(acceptance.ReviewArtifacts)
	acceptance.EffectiveStyleReceiptPath, acceptance.EffectiveStyleReceiptDigest, acceptance.EffectiveStyleArtifactSHA256 = archivePath, style.ReceiptDigest, domain.ComputeArcArtifactSHA256(archiveRaw)
	acceptance, err = domain.SignChapterAcceptanceReceipt(acceptance)
	if err != nil {
		t.Fatal(err)
	}
	// Rebuild only this temporary fixture's acceptance suffix through the real
	// Store publisher, now with the complete v3 style evidence.
	for _, old := range acceptances[1:] {
		path := filepath.Join(st.Dir(), arcCycleAcceptancePath(g.GenerationID, old.Chapter, old.ReceiptDigest))
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Dir(path)); err != nil {
			t.Fatal(err)
		}
	}
	for _, current := range []domain.ChapterAcceptanceReceipt{acceptance, acceptances[2]} {
		if _, err := st.ArcCycle().SaveChapterAcceptanceReceipt(current); err != nil {
			t.Fatal(err)
		}
	}
	timing, err := st.CompleteChapterDelivery(g, acceptance, chapterDeliveryTestNow().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Dir(), "chapters/01.md"), []byte(fmt.Sprintf("%s\nchanged source", sourceBody)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := NewStore(st.Dir()).verifyChapterDeliveryProofFiles(acceptance, timing.ProofFiles); err == nil {
		t.Fatal("chapter 2 closed proof lost its explicit earlier style source chapter 1")
	}
}

func TestChapterDeliveryRuntimeRedirectFailsBeforeWriting(t *testing.T) {
	st := NewStore(t.TempDir())
	g := chapterDeliveryTestGeneration(t, st, "symlink-delivery", true)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(st.Dir(), "meta/runtime")); err != nil {
		t.Fatal(err)
	}
	if err := st.ArmChapterDeliveryBudget(g, chapterDeliveryTestNow()); err == nil {
		t.Fatal("redirected runtime directory accepted")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("redirected runtime wrote outside the store: %v %v", entries, err)
	}
}

func TestChapterDeliveryRejectsAcceptanceWithMissingOrDriftingRealEvidence(t *testing.T) {
	for _, mode := range []string{"body", "review", "outcome", "acceptance"} {
		t.Run(mode, func(t *testing.T) {
			st, g, acceptances := chapterDeliveryAcceptedFixture(t)
			acceptance := acceptances[0]
			var rel string
			switch mode {
			case "body":
				rel = "chapters/01.md"
			case "review":
				rel = acceptance.ReviewArtifacts[0].Path
			case "outcome":
				rel = projectedActualOutcomePath(g.GenerationID, acceptance.Chapter, acceptance.OutcomeReceiptDigest)
			case "acceptance":
				rel = arcCycleAcceptancePath(g.GenerationID, acceptance.Chapter, acceptance.ReceiptDigest)
			}
			if err := os.Remove(filepath.Join(st.Dir(), rel)); err != nil {
				t.Fatal(err)
			}
			if _, err := st.CompleteChapterDelivery(g, acceptance, chapterDeliveryTestNow().Add(time.Minute)); err == nil {
				t.Fatalf("missing %s counted as delivery", mode)
			}
			timing, err := st.LoadChapterDeliveryTiming(g.GenerationID, 1)
			if err != nil || timing == nil || !timing.ClosedAt.IsZero() {
				t.Fatalf("failed evidence check closed timing: %+v %v", timing, err)
			}
		})
	}
}
