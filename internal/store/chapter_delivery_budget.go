package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

const chapterDeliveryRoot = "meta/runtime/chapter_delivery"
const chapterDeliveryLedgerPath = chapterDeliveryRoot + "/ledger.json"

// ErrChapterDeliveryDeadline means an originally armed, unfinished chapter has
// reached its wall-clock deadline. It never cancels an in-flight operation.
var ErrChapterDeliveryDeadline = errors.New("chapter delivery wall-time deadline exceeded")

// ChapterDeliveryTimingV1 records host observations, never model timestamps.
// A recovery observation cannot establish the original acceptance completion
// time, even if an outcome contains an earlier AcceptedAt value.
type ChapterDeliveryTimingV1 struct {
	GenerationID            string                       `json:"generation_id"`
	Chapter                 int                          `json:"chapter"`
	Policy                  string                       `json:"policy"`
	LimitSeconds            int                          `json:"limit_seconds"`
	StartedAt               time.Time                    `json:"started_at"`
	DeadlineAt              time.Time                    `json:"deadline_at"`
	ClosedAt                time.Time                    `json:"closed_at"`
	AcceptanceReceiptDigest string                       `json:"acceptance_receipt_digest,omitempty"`
	Recovered               bool                         `json:"recovered,omitempty"`
	TimingUnknown           bool                         `json:"timing_unknown,omitempty"`
	RecoveredAfterDeadline  bool                         `json:"recovered_after_deadline,omitempty"`
	ProofFiles              []ChapterDeliveryProofFileV1 `json:"proof_files,omitempty"`
}

// ChapterDeliveryProofFileV1 pins the exact files already authenticated at
// close. It is host timing evidence, never authority for accepting a chapter.
type ChapterDeliveryProofFileV1 struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type chapterDeliveryGenerationV1 struct {
	GenerationID string                    `json:"generation_id"`
	CreatedAt    string                    `json:"created_at"`
	FirstChapter int                       `json:"first_chapter"`
	LastChapter  int                       `json:"last_chapter"`
	Policy       string                    `json:"policy"`
	LimitSeconds int                       `json:"limit_seconds"`
	ArmedAt      time.Time                 `json:"armed_at"`
	Chapters     []ChapterDeliveryTimingV1 `json:"chapters"`
}

type chapterDeliveryLedgerV1 struct {
	Version      string                                 `json:"version"`
	LastObserved time.Time                              `json:"last_observed"`
	Generations  map[string]chapterDeliveryGenerationV1 `json:"generations"`
	Digest       string                                 `json:"digest"`
}

// ArmChapterDeliveryBudget is exclusively for the new-generation creation
// branch, immediately after CreateBuildingGeneration. Resume must not call it:
// an enabled generation with a missing ledger requires explicit intervention.
func (s *Store) ArmChapterDeliveryBudget(generation domain.PlanningGenerationV2, now time.Time) error {
	return s.armChapterDeliveryBudget(generation, func() time.Time { return now })
}

func (s *Store) ArmChapterDeliveryBudgetNow(generation domain.PlanningGenerationV2) error {
	return s.armChapterDeliveryBudget(generation, time.Now)
}

func (s *Store) armChapterDeliveryBudget(generation domain.PlanningGenerationV2, clock func() time.Time) error {
	actual, err := s.authenticateChapterDeliveryGeneration(generation)
	if err != nil || actual.ChapterDeliveryBudget == nil {
		return err
	}
	return s.withChapterDeliveryLock(func() error {
		now := clock()
		ledger, err := s.readChapterDeliveryLedger()
		if err != nil {
			return err
		}
		if ledger == nil {
			ledger = &chapterDeliveryLedgerV1{Version: "chapter-delivery-ledger.v1", Generations: map[string]chapterDeliveryGenerationV1{}}
		}
		if err := s.requireArmedChapterDeliveryGenerations(ledger, actual.GenerationID); err != nil {
			return err
		}
		if err := chapterDeliveryObserve(ledger, now); err != nil {
			return err
		}
		if _, exists := ledger.Generations[actual.GenerationID]; exists {
			return fmt.Errorf("chapter delivery budget generation %s is already armed; resume must preserve its ledger", actual.GenerationID)
		}
		if actual.Status != domain.PlanningGenerationBuildingV2 || actual.ProjectedChapterCount != 0 {
			return fmt.Errorf("chapter delivery budget can only arm a new unprojected building generation")
		}
		entry := chapterDeliveryGenerationV1{GenerationID: actual.GenerationID, CreatedAt: actual.CreatedAt,
			FirstChapter: actual.FirstProjectedChapter, LastChapter: actual.LastProjectedChapter,
			Policy: actual.ChapterDeliveryBudget.Policy, LimitSeconds: actual.ChapterDeliveryBudget.LimitSeconds, ArmedAt: now.UTC().Round(0)}
		for chapter := entry.FirstChapter; chapter <= entry.LastChapter; chapter++ {
			if err := chapterDeliveryRejectReplacement(ledger, actual.GenerationID, chapter); err != nil {
				return err
			}
			entry.Chapters = append(entry.Chapters, ChapterDeliveryTimingV1{GenerationID: actual.GenerationID,
				Chapter: chapter, Policy: entry.Policy, LimitSeconds: entry.LimitSeconds})
		}
		ledger.Generations[actual.GenerationID] = entry
		return s.writeChapterDeliveryLedger(ledger)
	})
}

func (s *Store) BeginChapterDelivery(generation domain.PlanningGenerationV2, chapter int, now time.Time, executionStores ...*Store) (*ChapterDeliveryTimingV1, error) {
	return s.beginChapterDelivery(generation, chapter, func() time.Time { return now }, executionStores...)
}

// executionStores are the already-bound isolated planning workspaces. They
// only add evidence that forbids inventing a missing start; they never grant
// authority, replace the live ledger, or modify an existing start/deadline.
func (s *Store) BeginChapterDeliveryNow(generation domain.PlanningGenerationV2, chapter int, executionStores ...*Store) (*ChapterDeliveryTimingV1, error) {
	return s.beginChapterDelivery(generation, chapter, time.Now, executionStores...)
}

func (s *Store) beginChapterDelivery(generation domain.PlanningGenerationV2, chapter int, clock func() time.Time, executionStores ...*Store) (*ChapterDeliveryTimingV1, error) {
	actual, err := s.authenticateChapterDeliveryGeneration(generation)
	if err != nil {
		return nil, err
	}
	if chapter < actual.FirstProjectedChapter || chapter > actual.LastProjectedChapter {
		return nil, fmt.Errorf("chapter delivery chapter %d is outside stored generation", chapter)
	}
	if actual.ChapterDeliveryBudget == nil {
		// Legacy runs do not create a timing directory, lock file, or ledger.
		ledger, err := s.readChapterDeliveryLedger()
		if err != nil {
			return nil, err
		}
		if err := s.requireArmedChapterDeliveryGenerations(ledger, actual.GenerationID); err != nil {
			return nil, err
		}
		return nil, chapterDeliveryRejectReplacement(ledger, actual.GenerationID, chapter)
	}
	var result *ChapterDeliveryTimingV1
	err = s.withChapterDeliveryLock(func() error {
		now := clock()
		ledger, err := s.readChapterDeliveryLedger()
		if err != nil {
			return err
		}
		if err := chapterDeliveryRejectReplacement(ledger, actual.GenerationID, chapter); err != nil {
			return err
		}
		_, err = chapterDeliveryEntry(ledger, *actual)
		if err != nil {
			return err
		}
		if err := chapterDeliveryObserve(ledger, now); err != nil {
			return err
		}
		if err := s.recoverChapterDeliveryLedger(ledger, now); err != nil {
			return err
		}
		entry := ledger.Generations[actual.GenerationID]
		timing := &entry.Chapters[chapter-entry.FirstChapter]
		deadlineErr := chapterDeliveryDeadline(ledger, now)
		if deadlineErr == nil && timing.StartedAt.IsZero() {
			if err := s.requireUnprojectedChapterDeliveryStart(actual.GenerationID, chapter, executionStores...); err != nil {
				return err
			}
			timing.StartedAt = now.UTC().Round(0)
			timing.DeadlineAt = timing.StartedAt.Add(time.Duration(timing.LimitSeconds) * time.Second)
		}
		ledger.Generations[actual.GenerationID] = entry
		if err := s.writeChapterDeliveryLedger(ledger); err != nil {
			return err
		}
		copy := *timing
		result = &copy
		return deadlineErr
	})
	return result, err
}

// A start can be created only before formal chapter projection. An armed but
// unstarted slot after projection is missing timing evidence, not permission
// to restart the clock at render time.
func (s *Store) requireUnprojectedChapterDeliveryStart(generationID string, chapter int, executionStores ...*Store) error {
	p := s.ProjectedV2()
	return p.withProjectedReadLock(func() error {
		sealed, err := p.generationExistsUnlocked(projectedGenerationsDir, generationID)
		if err != nil {
			return err
		}
		if sealed {
			return fmt.Errorf("chapter delivery original start is missing for sealed generation; refusing to start its clock after projection")
		}
		base := projectedBuildingGenerationPath(generationID)
		var generation domain.PlanningGenerationV2
		if err := s.readChapterDeliveryProofJSON(filepath.Join(base, projectedGenerationManifestFile), &generation); err != nil {
			return err
		}
		if err := domain.ValidatePlanningGenerationV2(generation); err != nil {
			return err
		}
		if generation.GenerationID != generationID || generation.Status != domain.PlanningGenerationBuildingV2 {
			return fmt.Errorf("chapter delivery fresh start requires its actual building generation")
		}
		_, err = os.Lstat(filepath.Join(s.dir, projectedBundlePath(base, chapter)))
		if err == nil || generation.ProjectedChapterCount >= chapter-generation.FirstProjectedChapter+1 {
			return fmt.Errorf("chapter delivery original start is missing for already projected chapter %d; refusing to reset its clock", chapter)
		}
		if !os.IsNotExist(err) {
			return err
		}
		for _, execution := range append([]*Store{s}, executionStores...) {
			if execution == nil {
				return fmt.Errorf("chapter delivery fresh start requires a non-nil execution store")
			}
			if err := execution.requireNoChapterDeliveryExecutionEvidence(generationID, chapter); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) CheckChapterDeliveryBudget(generationID string, now time.Time) error {
	return s.checkChapterDeliveryBudget(generationID, func() time.Time { return now })
}

// CheckChapterDeliveryBudgetNow samples the production wall clock after taking
// the ledger lock, so competing callers cannot appear to move time backwards
// merely because lock admission differs from their dispatch order.
func (s *Store) CheckChapterDeliveryBudgetNow(generationID string) error {
	return s.checkChapterDeliveryBudget(generationID, time.Now)
}

func (s *Store) checkChapterDeliveryBudget(generationID string, clock func() time.Time) error {
	actual, err := s.loadChapterDeliveryGeneration(generationID)
	if err != nil || actual.ChapterDeliveryBudget == nil {
		return err
	}
	return s.withChapterDeliveryLock(func() error {
		now := clock()
		ledger, err := s.readChapterDeliveryLedger()
		if err != nil {
			return err
		}
		_, err = chapterDeliveryEntry(ledger, *actual)
		if err != nil {
			return err
		}
		if err := chapterDeliveryObserve(ledger, now); err != nil {
			return err
		}
		// A real persisted acceptance survives a crash between acceptance and
		// close. Authenticate it and record only this later host observation.
		if err := s.recoverChapterDeliveryLedger(ledger, now); err != nil {
			return err
		}
		if err := s.writeChapterDeliveryLedger(ledger); err != nil {
			return err
		}
		return chapterDeliveryDeadline(ledger, now)
	})
}

func (s *Store) CompleteChapterDelivery(generation domain.PlanningGenerationV2, acceptance domain.ChapterAcceptanceReceipt, now time.Time) (*ChapterDeliveryTimingV1, error) {
	return s.completeChapterDelivery(generation, acceptance, func() time.Time { return now })
}

func (s *Store) CompleteChapterDeliveryNow(generation domain.PlanningGenerationV2, acceptance domain.ChapterAcceptanceReceipt) (*ChapterDeliveryTimingV1, error) {
	return s.completeChapterDelivery(generation, acceptance, time.Now)
}

func (s *Store) completeChapterDelivery(generation domain.PlanningGenerationV2, acceptance domain.ChapterAcceptanceReceipt, clock func() time.Time) (*ChapterDeliveryTimingV1, error) {
	actual, err := s.authenticateChapterDeliveryGeneration(generation)
	if err != nil || actual.ChapterDeliveryBudget == nil {
		return nil, err
	}
	var result *ChapterDeliveryTimingV1
	err = s.withChapterDeliveryLock(func() error {
		now := clock()
		ledger, err := s.readChapterDeliveryLedger()
		if err != nil {
			return err
		}
		entry, err := chapterDeliveryEntry(ledger, *actual)
		if err != nil {
			return err
		}
		if err := chapterDeliveryObserve(ledger, now); err != nil {
			return err
		}
		if acceptance.GenerationID != actual.GenerationID || acceptance.Chapter < entry.FirstChapter || acceptance.Chapter > entry.LastChapter {
			return fmt.Errorf("chapter delivery acceptance differs from stored generation")
		}
		timing := &entry.Chapters[acceptance.Chapter-entry.FirstChapter]
		if err := s.bindChapterDeliveryAcceptanceProof(timing, acceptance); err != nil {
			return err
		}
		if err := closeChapterDelivery(timing, acceptance, now, false); err != nil {
			return err
		}
		ledger.Generations[actual.GenerationID] = entry
		if err := s.writeChapterDeliveryLedger(ledger); err != nil {
			return err
		}
		copy := *timing
		result = &copy
		return nil
	})
	return result, err
}

// LoadChapterDeliveryTiming returns a validated ledger snapshot. Call Check or
// Complete to reauthenticate the current proof files; this read alone is not
// evidence that those external files remain unchanged.
func (s *Store) LoadChapterDeliveryTiming(generationID string, chapter int) (*ChapterDeliveryTimingV1, error) {
	actual, err := s.loadChapterDeliveryGeneration(generationID)
	if err != nil || actual.ChapterDeliveryBudget == nil {
		return nil, err
	}
	var result *ChapterDeliveryTimingV1
	err = s.withChapterDeliveryLock(func() error {
		ledger, err := s.readChapterDeliveryLedger()
		if err != nil {
			return err
		}
		entry, err := chapterDeliveryEntry(ledger, *actual)
		if err != nil {
			return err
		}
		if chapter < entry.FirstChapter || chapter > entry.LastChapter {
			return fmt.Errorf("chapter delivery chapter is outside stored generation")
		}
		copy := entry.Chapters[chapter-entry.FirstChapter]
		if !copy.StartedAt.IsZero() {
			result = &copy
		}
		return nil
	})
	return result, err
}

func (s *Store) loadChapterDeliveryGeneration(id string) (*domain.PlanningGenerationV2, error) {
	if err := validateProjectedPathComponent("generation_id", id); err != nil {
		return nil, err
	}
	// This guard needs the current frozen identity, not a replay of every
	// sealed bundle. Full acceptance authentication still runs before close.
	p := s.ProjectedV2()
	var actual domain.PlanningGenerationV2
	err := p.withProjectedReadLock(func() error {
		for _, location := range []struct {
			root   string
			status domain.PlanningGenerationStatusV2
		}{
			{projectedGenerationsDir, domain.PlanningGenerationSealedV2}, {projectedBuildingDir, domain.PlanningGenerationBuildingV2},
		} {
			exists, err := p.generationExistsUnlocked(location.root, id)
			if err != nil {
				return err
			}
			if !exists {
				continue
			}
			if err := s.readChapterDeliveryProofJSON(filepath.Join(location.root, id, projectedGenerationManifestFile), &actual); err != nil {
				return err
			}
			if err := domain.ValidatePlanningGenerationV2(actual); err != nil {
				return err
			}
			if actual.GenerationID != id || actual.Status != location.status {
				return fmt.Errorf("chapter delivery stored generation has different path identity or status")
			}
			return nil
		}
		return fmt.Errorf("chapter delivery stored generation %s is missing", id)
	})
	if err != nil {
		return nil, err
	}
	return &actual, nil
}

func (s *Store) authenticateChapterDeliveryGeneration(generation domain.PlanningGenerationV2) (*domain.PlanningGenerationV2, error) {
	if err := domain.ValidatePlanningGenerationV2(generation); err != nil {
		return nil, err
	}
	actual, err := s.loadChapterDeliveryGeneration(generation.GenerationID)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(actual.ChapterDeliveryBudget, generation.ChapterDeliveryBudget) ||
		actual.CreatedAt != generation.CreatedAt || actual.FirstProjectedChapter != generation.FirstProjectedChapter || actual.LastProjectedChapter != generation.LastProjectedChapter ||
		actual.BaseCanonRoot != generation.BaseCanonRoot || actual.StableOutlineRoot != generation.StableOutlineRoot ||
		actual.PlanningDependencyRoot != generation.PlanningDependencyRoot || actual.RandomSeedContractRoot != generation.RandomSeedContractRoot {
		return nil, fmt.Errorf("chapter delivery caller differs from stored generation or frozen budget")
	}
	return actual, nil
}

func chapterDeliveryEntry(ledger *chapterDeliveryLedgerV1, generation domain.PlanningGenerationV2) (chapterDeliveryGenerationV1, error) {
	var entry chapterDeliveryGenerationV1
	if ledger != nil {
		entry = ledger.Generations[generation.GenerationID]
	}
	budget := generation.ChapterDeliveryBudget
	if budget == nil || entry.GenerationID != generation.GenerationID || entry.CreatedAt != generation.CreatedAt ||
		entry.FirstChapter != generation.FirstProjectedChapter || entry.LastChapter != generation.LastProjectedChapter ||
		entry.Policy != budget.Policy || entry.LimitSeconds != budget.LimitSeconds {
		return entry, fmt.Errorf("chapter delivery enabled generation has missing or mismatched armed ledger; refusing to reset its start")
	}
	return entry, nil
}

func chapterDeliveryRejectReplacement(ledger *chapterDeliveryLedgerV1, id string, chapter int) error {
	if ledger == nil {
		return nil
	}
	for otherID, entry := range ledger.Generations {
		if otherID == id {
			continue
		}
		for _, timing := range entry.Chapters {
			if timing.Chapter == chapter && !timing.StartedAt.IsZero() && timing.ClosedAt.IsZero() {
				return fmt.Errorf("chapter delivery chapter %d already has an open budget in generation %s; replacement requires explicit direction", chapter, otherID)
			}
		}
	}
	return nil
}

func chapterDeliveryObserve(ledger *chapterDeliveryLedgerV1, now time.Time) error {
	if now.IsZero() || now.Year() < 2000 || now.Year() > 9999 || now.Before(ledger.LastObserved) {
		return fmt.Errorf("chapter delivery clock is invalid or moved backwards; refusing to reset timing")
	}
	ledger.LastObserved = now.UTC().Round(0)
	return nil
}

func chapterDeliveryDeadline(ledger *chapterDeliveryLedgerV1, now time.Time) error {
	var earliest *ChapterDeliveryTimingV1
	for _, entry := range ledger.Generations {
		for i := range entry.Chapters {
			timing := &entry.Chapters[i]
			if !timing.StartedAt.IsZero() && timing.ClosedAt.IsZero() && (earliest == nil || timing.DeadlineAt.Before(earliest.DeadlineAt) ||
				(timing.DeadlineAt.Equal(earliest.DeadlineAt) && timing.Chapter < earliest.Chapter)) {
				earliest = timing
			}
		}
	}
	if earliest != nil && !now.Before(earliest.DeadlineAt) {
		return fmt.Errorf("%w: generation %s chapter %d began %s; deadline %s", ErrChapterDeliveryDeadline,
			earliest.GenerationID, earliest.Chapter, earliest.StartedAt.Format(time.RFC3339Nano), earliest.DeadlineAt.Format(time.RFC3339Nano))
	}
	return nil
}

func closeChapterDelivery(timing *ChapterDeliveryTimingV1, acceptance domain.ChapterAcceptanceReceipt, now time.Time, recovered bool) error {
	if timing.StartedAt.IsZero() {
		return fmt.Errorf("chapter delivery acceptance has no original chapter start")
	}
	if !timing.ClosedAt.IsZero() {
		if timing.AcceptanceReceiptDigest != acceptance.ReceiptDigest {
			return fmt.Errorf("chapter delivery acceptance digest differs from original close")
		}
		return nil
	}
	timing.ClosedAt = now.UTC().Round(0)
	timing.AcceptanceReceiptDigest = acceptance.ReceiptDigest
	timing.Recovered = recovered
	timing.TimingUnknown = recovered
	timing.RecoveredAfterDeadline = !now.Before(timing.DeadlineAt)
	return nil
}

func (s *Store) recoverChapterDeliveryAcceptances(entry *chapterDeliveryGenerationV1, now time.Time) error {
	acceptances, err := s.ArcCycle().ListChapterAcceptanceReceipts(entry.GenerationID)
	if err != nil {
		return err
	}
	seen := make(map[int]bool, len(acceptances))
	for _, acceptance := range acceptances {
		if acceptance.Chapter < entry.FirstChapter || acceptance.Chapter > entry.LastChapter {
			return fmt.Errorf("chapter delivery persisted acceptance is outside armed generation")
		}
		seen[acceptance.Chapter] = true
		timing := &entry.Chapters[acceptance.Chapter-entry.FirstChapter]
		if !timing.ClosedAt.IsZero() && len(timing.ProofFiles) > 0 {
			if timing.AcceptanceReceiptDigest != acceptance.ReceiptDigest {
				return fmt.Errorf("chapter delivery closed acceptance identity changed")
			}
			if err := s.verifyChapterDeliveryProofFiles(acceptance, timing.ProofFiles); err != nil {
				return err
			}
			continue
		}
		if err := s.bindChapterDeliveryAcceptanceProof(timing, acceptance); err != nil {
			return err
		}
		if err := closeChapterDelivery(timing, acceptance, now, true); err != nil {
			return err
		}
	}
	for _, timing := range entry.Chapters {
		if !timing.ClosedAt.IsZero() && !seen[timing.Chapter] {
			return fmt.Errorf("chapter delivery closed chapter %d is missing its original persisted acceptance", timing.Chapter)
		}
	}
	return nil
}

func (s *Store) recoverChapterDeliveryLedger(ledger *chapterDeliveryLedgerV1, now time.Time) error {
	for id, entry := range ledger.Generations {
		generation, err := s.loadChapterDeliveryGeneration(id)
		if err != nil {
			return err
		}
		if _, err := chapterDeliveryEntry(ledger, *generation); err != nil {
			return err
		}
		if err := s.recoverChapterDeliveryAcceptances(&entry, now); err != nil {
			return err
		}
		ledger.Generations[id] = entry
	}
	return nil
}

func (s *Store) authenticateChapterDeliveryAcceptance(acceptance domain.ChapterAcceptanceReceipt) error {
	if err := domain.ValidateChapterAcceptanceReceipt(acceptance); err != nil {
		return err
	}
	persisted, err := s.ArcCycle().LoadChapterAcceptanceReceipt(acceptance.GenerationID, acceptance.Chapter, acceptance.ReceiptDigest)
	if err != nil {
		return err
	}
	if persisted == nil || !reflect.DeepEqual(*persisted, acceptance) {
		return fmt.Errorf("chapter delivery requires the exact persisted chapter acceptance receipt")
	}
	if err := s.ArcCycle().ValidateArcCycle(acceptance.GenerationID); err != nil {
		return err
	}
	outcome, err := s.ProjectedV2().LoadActualOutcomeReceipt(acceptance.GenerationID, acceptance.Chapter, acceptance.OutcomeReceiptDigest)
	if err != nil {
		return err
	}
	if outcome == nil || outcome.ChapterBodySHA256 != acceptance.ChapterBodySHA256 {
		return fmt.Errorf("chapter delivery acceptance has missing or mismatched actual body outcome")
	}
	generation, err := s.loadChapterDeliveryGeneration(acceptance.GenerationID)
	if err != nil {
		return err
	}
	manifest, err := s.ArcCycle().LoadArcPlanningManifest(acceptance.GenerationID, acceptance.ArcManifestDigest)
	if err != nil {
		return err
	}
	if manifest == nil || manifest.FirstChapter != generation.FirstProjectedChapter || manifest.LastChapter != generation.LastProjectedChapter ||
		manifest.FullOutlineDigest != generation.StableOutlineRoot || (generation.ScopeID != "" && manifest.ArcID != generation.ScopeID) {
		return fmt.Errorf("chapter delivery acceptance manifest differs from stored generation")
	}
	bundles, err := s.ProjectedV2().LoadProjectedChapterBundles(acceptance.GenerationID)
	if err != nil {
		return err
	}
	for _, bundle := range bundles {
		if bundle.Chapter != acceptance.Chapter {
			continue
		}
		for _, binding := range manifest.Chapters {
			if binding.Chapter == bundle.Chapter && binding.BundleDigest == bundle.BundleDigest {
				capacity, err := json.Marshal(bundle.ChapterPlan.CausalSimulation.RenderCapacity)
				if err != nil || binding.CapacityDigest != domain.ComputeArcArtifactSHA256(capacity) {
					return fmt.Errorf("chapter delivery acceptance manifest capacity differs from stored bundle")
				}
				return nil
			}
		}
	}
	return fmt.Errorf("chapter delivery acceptance manifest lacks the exact stored bundle")
}

func (s *Store) readChapterDeliveryProofJSON(path string, value any) error {
	raw, err := readArcCycleSealedEvidenceFile(s.dir, path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("chapter delivery proof has trailing JSON content")
	}
	return nil
}

// Reconstruct the exact proof set from the authenticated receipt and its
// explicit references. In particular, style source chapter bodies cannot be
// omitted simply because they precede this chapter.
func (s *Store) chapterDeliveryProofPaths(acceptance domain.ChapterAcceptanceReceipt) ([]string, error) {
	if err := domain.ValidateChapterAcceptanceReceipt(acceptance); err != nil {
		return nil, err
	}
	id, chapter := acceptance.GenerationID, acceptance.Chapter
	outcomePath := projectedActualOutcomePath(id, chapter, acceptance.OutcomeReceiptDigest)
	var outcome domain.ActualOutcomeReceiptV2
	if err := s.readChapterDeliveryProofJSON(outcomePath, &outcome); err != nil {
		return nil, err
	}
	if err := domain.ValidateActualOutcomeReceiptV2(outcome); err != nil {
		return nil, err
	}
	if outcome.GenerationID != id || outcome.Chapter != chapter || outcome.ReceiptDigest != acceptance.OutcomeReceiptDigest || outcome.ChapterBodySHA256 != acceptance.ChapterBodySHA256 {
		return nil, fmt.Errorf("chapter delivery outcome differs from original accepted chapter")
	}
	base := projectedSealedGenerationPath(id)
	paths := []string{arcCycleAcceptancePath(id, chapter, acceptance.ReceiptDigest), outcomePath,
		arcCycleManifestPath(id, acceptance.ArcManifestDigest), fmt.Sprintf("chapters/%02d.md", chapter),
		projectedBundlePath(base, chapter), filepath.Join(base, projectedGenerationManifestFile), filepath.Join(base, projectedSealReceiptFile),
		projectedPromotionReceiptPath(id, chapter, outcome.PromotionReceiptDigest)}
	for _, review := range acceptance.ReviewArtifacts {
		paths = append(paths, review.Path)
	}
	if acceptance.EffectiveStyleReceiptPath != "" {
		raw, err := readArcCycleSealedEvidenceFile(s.dir, acceptance.EffectiveStyleReceiptPath)
		if err != nil {
			return nil, err
		}
		style, err := validateArchivedRenderPermitEffectiveStyleReceipt(raw, chapter, acceptance.EffectiveStyleReceiptDigest, acceptance.EffectiveStyleReceiptPath)
		if err != nil {
			return nil, err
		}
		if style.GenerationID != id || domain.ComputeArcArtifactSHA256(raw) != acceptance.EffectiveStyleArtifactSHA256 {
			return nil, fmt.Errorf("chapter delivery effective style differs from original accepted chapter")
		}
		paths = append(paths, acceptance.EffectiveStyleReceiptPath)
		for _, source := range style.SourceChapterBodies {
			paths = append(paths, fmt.Sprintf("chapters/%02d.md", source.Chapter))
		}
	}
	sort.Strings(paths)
	unique := paths[:0]
	for _, path := range paths {
		if len(unique) == 0 || path != unique[len(unique)-1] {
			unique = append(unique, path)
		}
	}
	return unique, nil
}

func (s *Store) verifyChapterDeliveryProofFiles(acceptance domain.ChapterAcceptanceReceipt, files []ChapterDeliveryProofFileV1) error {
	paths, err := s.chapterDeliveryProofPaths(acceptance)
	if err != nil {
		return err
	}
	if len(paths) != len(files) {
		return fmt.Errorf("chapter delivery closed proof file set is incomplete")
	}
	for i, path := range paths {
		if files[i].Path != path {
			return fmt.Errorf("chapter delivery closed proof path identity changed")
		}
		raw, err := s.readChapterDeliveryProofBytes(path)
		if err != nil {
			return err
		}
		if domain.ComputeArcArtifactSHA256(raw) != files[i].SHA256 {
			return fmt.Errorf("chapter delivery closed proof bytes changed at %s", path)
		}
	}
	return nil
}

func (s *Store) bindChapterDeliveryAcceptanceProof(timing *ChapterDeliveryTimingV1, acceptance domain.ChapterAcceptanceReceipt) error {
	files := timing.ProofFiles
	if len(files) == 0 {
		paths, err := s.chapterDeliveryProofPaths(acceptance)
		if err != nil {
			return err
		}
		for _, path := range paths {
			raw, err := s.readChapterDeliveryProofBytes(path)
			if err != nil {
				return err
			}
			files = append(files, ChapterDeliveryProofFileV1{Path: path, SHA256: domain.ComputeArcArtifactSHA256(raw)})
		}
	}
	if err := s.authenticateChapterDeliveryAcceptance(acceptance); err != nil {
		return err
	}
	// Compare the pre-authentication snapshot again rather than trusting bytes
	// read for the first time after semantic authentication has completed.
	if err := s.verifyChapterDeliveryProofFiles(acceptance, files); err != nil {
		return err
	}
	timing.ProofFiles = files
	return nil
}

func (s *Store) readChapterDeliveryProofBytes(rel string) ([]byte, error) {
	if !strings.HasPrefix(rel, arcCycleAcceptanceDir+"/") && !strings.HasPrefix(rel, arcCycleManifestDir+"/") {
		return readArcCycleSealedEvidenceFile(s.dir, rel)
	}
	// ArcCycle publishes content-addressed metadata with hard links. Preserve
	// that reader contract while rejecting redirected parents and non-files;
	// body/review/style evidence still uses the stricter single-link reader.
	var raw []byte
	err := s.ArcCycle().withReadLock(func() error {
		path := s.dir
		components := strings.Split(filepath.ToSlash(rel), "/")
		for i, component := range components {
			path = filepath.Join(path, component)
			info, err := os.Lstat(path)
			if err != nil {
				return err
			}
			last := i == len(components)-1
			if info.Mode()&os.ModeSymlink != 0 || (!last && !info.IsDir()) || (last && !info.Mode().IsRegular()) {
				return fmt.Errorf("chapter delivery arc proof path is not a real metadata file")
			}
		}
		var err error
		raw, err = os.ReadFile(path)
		return err
	})
	return raw, err
}

// A missing global ledger must not make a legacy replacement appear safe.
func (s *Store) requireArmedChapterDeliveryGenerations(ledger *chapterDeliveryLedgerV1, exempt string) error {
	seen := map[string]bool{}
	for _, root := range []string{projectedBuildingDir, projectedGenerationsDir} {
		entries, err := os.ReadDir(filepath.Join(s.dir, root))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		for _, entry := range entries {
			id := entry.Name()
			if id == exempt || seen[id] {
				continue
			}
			seen[id] = true
			generation, err := s.loadChapterDeliveryGeneration(id)
			if err != nil {
				return err
			}
			if generation.ChapterDeliveryBudget != nil {
				if _, err := chapterDeliveryEntry(ledger, *generation); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *Store) withChapterDeliveryLock(fn func() error) error {
	// Separate lock namespace: never hold Planning/ArcCycle IO locks while
	// calling their public readers, which acquire those locks themselves.
	s.crossMu.Lock()
	defer s.crossMu.Unlock()
	root := filepath.Join(s.dir, chapterDeliveryRoot)
	// Validate before creating descendants so runtime metadata cannot escape
	// through a redirected parent or lock file.
	for _, rel := range []string{".", "meta", "meta/runtime", chapterDeliveryRoot} {
		path := filepath.Join(s.dir, rel)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) && rel != "." {
			if err := os.Mkdir(path, 0o755); err != nil && !os.IsExist(err) {
				return err
			}
			info, err = os.Lstat(path)
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("chapter delivery runtime path must contain only real directories")
		}
	}
	lock, err := os.OpenFile(filepath.Join(root, ".write.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o644)
	if err != nil {
		return err
	}
	defer lock.Close()
	info, err := lock.Stat()
	if err != nil {
		return err
	}
	if err := validateArcCycleSealedEvidenceFileInfo(info); err != nil {
		return err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	return fn()
}

func (s *Store) readChapterDeliveryLedger() (*chapterDeliveryLedgerV1, error) {
	raw, err := readArcCycleSealedEvidenceFile(s.dir, chapterDeliveryLedgerPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ledger chapterDeliveryLedgerV1
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&ledger); err != nil {
		return nil, fmt.Errorf("chapter delivery corrupt ledger: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("chapter delivery ledger contains trailing content")
	}
	digest := ledger.Digest
	ledger.Digest = ""
	canonical, err := json.Marshal(ledger)
	ledger.Digest = digest
	if err != nil || digest != domain.ComputeArcArtifactSHA256(canonical) {
		return nil, fmt.Errorf("chapter delivery ledger integrity digest mismatch")
	}
	if err := validateChapterDeliveryLedger(ledger); err != nil {
		return nil, err
	}
	return &ledger, nil
}

func validateChapterDeliveryLedger(ledger chapterDeliveryLedgerV1) error {
	if ledger.Version != "chapter-delivery-ledger.v1" || ledger.LastObserved.IsZero() || ledger.Generations == nil {
		return fmt.Errorf("chapter delivery ledger version or clock is invalid")
	}
	for id, entry := range ledger.Generations {
		if id != entry.GenerationID || entry.CreatedAt == "" || entry.FirstChapter <= 0 || entry.LastChapter < entry.FirstChapter ||
			entry.Policy != domain.ChapterDeliveryBudgetPolicyV1 || entry.LimitSeconds <= 0 || entry.LimitSeconds > 86400 ||
			entry.ArmedAt.IsZero() || entry.ArmedAt.After(ledger.LastObserved) || len(entry.Chapters) != entry.LastChapter-entry.FirstChapter+1 {
			return fmt.Errorf("chapter delivery ledger generation shape or policy is invalid")
		}
		for i, timing := range entry.Chapters {
			if timing.GenerationID != id || timing.Chapter != entry.FirstChapter+i || timing.Policy != entry.Policy || timing.LimitSeconds != entry.LimitSeconds {
				return fmt.Errorf("chapter delivery ledger chapter identity is invalid")
			}
			if timing.StartedAt.IsZero() {
				if !timing.DeadlineAt.IsZero() || !timing.ClosedAt.IsZero() || timing.AcceptanceReceiptDigest != "" || timing.Recovered || timing.TimingUnknown || timing.RecoveredAfterDeadline || len(timing.ProofFiles) > 0 {
					return fmt.Errorf("chapter delivery ledger has close/deadline without original start")
				}
				continue
			}
			if timing.StartedAt.Before(entry.ArmedAt) || timing.StartedAt.After(ledger.LastObserved) || !timing.DeadlineAt.Equal(timing.StartedAt.Add(time.Duration(entry.LimitSeconds)*time.Second)) {
				return fmt.Errorf("chapter delivery ledger original start/deadline is corrupt")
			}
			if timing.ClosedAt.IsZero() {
				if timing.AcceptanceReceiptDigest != "" || timing.Recovered || timing.TimingUnknown || timing.RecoveredAfterDeadline || len(timing.ProofFiles) > 0 {
					return fmt.Errorf("chapter delivery open ledger has completion evidence")
				}
			} else if timing.ClosedAt.Before(timing.StartedAt) || timing.ClosedAt.After(ledger.LastObserved) || timing.AcceptanceReceiptDigest == "" ||
				timing.TimingUnknown != timing.Recovered || timing.RecoveredAfterDeadline != !timing.ClosedAt.Before(timing.DeadlineAt) {
				return fmt.Errorf("chapter delivery ledger completion clock or evidence is corrupt")
			}
			for i, file := range timing.ProofFiles {
				if file.Path == "" || file.Path == "." || file.Path == ".." || filepath.IsAbs(file.Path) || filepath.Clean(file.Path) != file.Path || strings.HasPrefix(file.Path, "../") || (i > 0 && timing.ProofFiles[i-1].Path >= file.Path) {
					return fmt.Errorf("chapter delivery proof paths are unsafe or not canonical")
				}
				if err := validateArcCycleDigest("chapter_delivery_proof_sha256", file.SHA256); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *Store) writeChapterDeliveryLedger(ledger *chapterDeliveryLedgerV1) error {
	if err := validateChapterDeliveryLedger(*ledger); err != nil {
		return err
	}
	ledger.Digest = ""
	raw, err := json.Marshal(ledger)
	if err != nil {
		return err
	}
	ledger.Digest = domain.ComputeArcArtifactSHA256(raw)
	if err := newIO(s.dir).WriteJSON(chapterDeliveryLedgerPath, ledger); err != nil {
		return err
	}
	return syncProjectedDirs(filepath.Join(s.dir, chapterDeliveryRoot))
}
