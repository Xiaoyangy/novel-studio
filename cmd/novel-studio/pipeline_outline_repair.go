package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

const (
	pipelineOutlineRepairManifestVersion = "chapter-zero-outline-repair.v1"
	pipelineOutlineRepairIntentVersion   = "chapter-zero-outline-repair-intent.v1"
	pipelineOutlineRepairReceiptVersion  = "chapter-zero-outline-repair-receipt.v1"

	pipelineOutlineRepairIntentPath  = "meta/planning/outline_repair/0000.intent.json"
	pipelineOutlineRepairReceiptPath = "meta/planning/outline_repair/0000.receipt.json"
)

var errPipelineOutlineRepairCandidateIncomplete = errors.New("outline repair candidate contains an incomplete operation 0")

// pipelineOutlineRepairManifest is a host-only, chapter-zero patch. It is not
// exposed to Architect as mutation authority: the host applies it once in the
// isolated outline-all candidate, then the normal outline-all validators and
// directory publisher own the result.
type pipelineOutlineRepairManifest struct {
	Version               string                     `json:"version"`
	Reason                string                     `json:"reason"`
	ExpectedLayeredDigest string                     `json:"expected_layered_digest"`
	Arcs                  []pipelineOutlineArcRepair `json:"arcs"`
}

type pipelineOutlineArcRepair struct {
	Volume               int                                 `json:"volume"`
	Arc                  int                                 `json:"arc"`
	ExpectedStartChapter int                                 `json:"expected_start_chapter"`
	ExpectedEndChapter   int                                 `json:"expected_end_chapter"`
	NewGoal              *string                             `json:"new_goal,omitempty"`
	ChapterReplacements  []pipelineOutlineChapterReplacement `json:"chapter_replacements,omitempty"`
}

// ContractRefs and the layered entry's internal Chapter field are deliberately
// absent. The host always carries both forward from the frozen source entry.
type pipelineOutlineChapterReplacement struct {
	Chapter   int      `json:"chapter"`
	Title     string   `json:"title"`
	CoreEvent string   `json:"core_event"`
	Hook      string   `json:"hook"`
	Scenes    []string `json:"scenes"`
}

type pipelineOutlineRepairIntent struct {
	Version             string                        `json:"version"`
	Operation           int                           `json:"operation"`
	AttemptID           string                        `json:"attempt_id"`
	SourceSnapshotRoot  string                        `json:"source_snapshot_root"`
	Manifest            pipelineOutlineRepairManifest `json:"manifest"`
	ManifestDigest      string                        `json:"manifest_digest"`
	BeforeLayeredDigest string                        `json:"before_layered_digest"`
	BeforeFlatDigest    string                        `json:"before_flat_digest"`
	IntentDigest        string                        `json:"intent_digest"`
}

type pipelineOutlineRepairReceipt struct {
	Version              string `json:"version"`
	Operation            int    `json:"operation"`
	AttemptID            string `json:"attempt_id"`
	SourceSnapshotRoot   string `json:"source_snapshot_root"`
	ManifestDigest       string `json:"manifest_digest"`
	IntentDigest         string `json:"intent_digest"`
	BeforeLayeredDigest  string `json:"before_layered_digest"`
	BeforeFlatDigest     string `json:"before_flat_digest"`
	AfterLayeredDigest   string `json:"after_layered_digest"`
	AfterFlatDigest      string `json:"after_flat_digest"`
	AppliedArcCount      int    `json:"applied_arc_count"`
	ReplacedChapterCount int    `json:"replaced_chapter_count"`
	ReceiptDigest        string `json:"receipt_digest"`
}

func loadPipelineOutlineRepairManifest(path string) (pipelineOutlineRepairManifest, string, error) {
	var manifest pipelineOutlineRepairManifest
	path = strings.TrimSpace(path)
	if path == "" {
		return manifest, "", fmt.Errorf("outline repair manifest path is empty")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return manifest, "", fmt.Errorf("read outline repair manifest: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return manifest, "", fmt.Errorf("parse outline repair manifest: %w", err)
	}
	if err := ensurePipelineOutlineRepairJSONEOF(decoder); err != nil {
		return manifest, "", err
	}
	if err := validatePipelineOutlineRepairManifest(manifest); err != nil {
		return manifest, "", err
	}
	digest, err := pipelineProjectAllDigestE(manifest)
	if err != nil {
		return manifest, "", err
	}
	return manifest, digest, nil
}

func ensurePipelineOutlineRepairJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("outline repair manifest contains multiple JSON values")
		}
		return fmt.Errorf("parse outline repair manifest trailing data: %w", err)
	}
	return nil
}

func validatePipelineOutlineRepairManifest(manifest pipelineOutlineRepairManifest) error {
	if manifest.Version != pipelineOutlineRepairManifestVersion {
		return fmt.Errorf("unsupported outline repair manifest version %q", manifest.Version)
	}
	if strings.TrimSpace(manifest.Reason) == "" {
		return fmt.Errorf("outline repair manifest requires reason")
	}
	if err := validatePipelineOutlineRepairDigest("expected_layered_digest", manifest.ExpectedLayeredDigest); err != nil {
		return err
	}
	if len(manifest.Arcs) == 0 {
		return fmt.Errorf("outline repair manifest requires at least one arc")
	}
	type arcKey struct{ volume, arc int }
	seenArcs := make(map[arcKey]struct{}, len(manifest.Arcs))
	seenChapters := make(map[int]struct{})
	for _, repair := range manifest.Arcs {
		if repair.Volume <= 0 || repair.Arc <= 0 {
			return fmt.Errorf("outline repair arc requires positive volume/arc")
		}
		key := arcKey{repair.Volume, repair.Arc}
		if _, exists := seenArcs[key]; exists {
			return fmt.Errorf("outline repair contains duplicate V%dA%d", repair.Volume, repair.Arc)
		}
		seenArcs[key] = struct{}{}
		if repair.ExpectedStartChapter <= 0 || repair.ExpectedEndChapter < repair.ExpectedStartChapter {
			return fmt.Errorf("outline repair V%dA%d has invalid expected chapter boundary %d-%d",
				repair.Volume, repair.Arc, repair.ExpectedStartChapter, repair.ExpectedEndChapter)
		}
		if repair.NewGoal == nil && len(repair.ChapterReplacements) == 0 {
			return fmt.Errorf("outline repair V%dA%d has no requested change", repair.Volume, repair.Arc)
		}
		if repair.NewGoal != nil && strings.TrimSpace(*repair.NewGoal) == "" {
			return fmt.Errorf("outline repair V%dA%d new_goal is empty", repair.Volume, repair.Arc)
		}
		for _, replacement := range repair.ChapterReplacements {
			if replacement.Chapter < repair.ExpectedStartChapter || replacement.Chapter > repair.ExpectedEndChapter {
				return fmt.Errorf("outline repair chapter %d is outside V%dA%d expected boundary %d-%d",
					replacement.Chapter, repair.Volume, repair.Arc,
					repair.ExpectedStartChapter, repair.ExpectedEndChapter)
			}
			if _, exists := seenChapters[replacement.Chapter]; exists {
				return fmt.Errorf("outline repair contains duplicate chapter replacement %d", replacement.Chapter)
			}
			seenChapters[replacement.Chapter] = struct{}{}
			if strings.TrimSpace(replacement.Title) == "" ||
				strings.TrimSpace(replacement.CoreEvent) == "" ||
				strings.TrimSpace(replacement.Hook) == "" || len(replacement.Scenes) == 0 {
				return fmt.Errorf("outline repair chapter %d replacement must provide complete title/core_event/hook/scenes", replacement.Chapter)
			}
			for i, scene := range replacement.Scenes {
				if strings.TrimSpace(scene) == "" {
					return fmt.Errorf("outline repair chapter %d scene %d is empty", replacement.Chapter, i+1)
				}
			}
		}
	}
	return nil
}

func validatePipelineOutlineRepairDigest(name, digest string) error {
	digest = strings.TrimSpace(digest)
	if !strings.HasPrefix(digest, "sha256:") {
		return fmt.Errorf("outline repair %s must be a sha256 digest", name)
	}
	raw := strings.TrimPrefix(digest, "sha256:")
	decoded, err := hex.DecodeString(raw)
	if err != nil || len(decoded) != 32 {
		return fmt.Errorf("outline repair %s is invalid", name)
	}
	return nil
}

func signPipelineOutlineRepairIntent(intent pipelineOutlineRepairIntent) (pipelineOutlineRepairIntent, error) {
	intent.IntentDigest = ""
	digest, err := pipelineProjectAllDigestE(intent)
	if err != nil {
		return intent, err
	}
	intent.IntentDigest = digest
	return intent, nil
}

func signPipelineOutlineRepairReceipt(receipt pipelineOutlineRepairReceipt) (pipelineOutlineRepairReceipt, error) {
	receipt.ReceiptDigest = ""
	digest, err := pipelineProjectAllDigestE(receipt)
	if err != nil {
		return receipt, err
	}
	receipt.ReceiptDigest = digest
	return receipt, nil
}

func validatePipelineOutlineRepairLiveEntry(st *store.Store) error {
	if err := validatePipelineOutlineAllEntry(st); err != nil {
		return err
	}
	if lock, err := st.Runtime.LoadPipelineExecution(); err != nil {
		return err
	} else if lock != nil {
		return fmt.Errorf("outline repair refuses active execution lock: mode=%s owner=%s", lock.Mode, lock.Owner)
	}
	if existing, err := st.LoadOutlineAllExecutionReceipt(); err != nil {
		return err
	} else if existing != nil {
		return fmt.Errorf("outline repair requires fresh outline-all; existing planning receipt must first be retired with --rebase-all-chapters")
	}
	if _, err := os.Lstat(filepath.Join(st.Dir(), filepath.FromSlash(pipelineOutlineRepairIntentPath))); err == nil {
		return fmt.Errorf("outline repair requires fresh outline-all; live canon contains an existing operation 0 intent")
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

// applyPipelineOutlineRepairCandidate applies operation 0 only inside the
// isolated outline-all candidate. The live tree remains byte-identical until
// outline-all's existing recoverable directory publish commits the candidate.
func applyPipelineOutlineRepairCandidate(
	st *store.Store,
	attemptID, sourceSnapshotRoot string,
	manifest pipelineOutlineRepairManifest,
	manifestDigest string,
) (*pipelineOutlineRepairReceipt, error) {
	if st == nil {
		return nil, fmt.Errorf("outline repair requires candidate store")
	}
	if strings.TrimSpace(attemptID) == "" || strings.TrimSpace(sourceSnapshotRoot) == "" {
		return nil, fmt.Errorf("outline repair requires attempt/source identity")
	}
	if err := validatePipelineOutlineRepairManifest(manifest); err != nil {
		return nil, err
	}
	wantManifestDigest, err := pipelineProjectAllDigestE(manifest)
	if err != nil {
		return nil, err
	}
	if manifestDigest != wantManifestDigest {
		return nil, fmt.Errorf("outline repair manifest digest changed before candidate apply")
	}

	if existing, err := loadPipelineOutlineRepairEvidence(st, attemptID, sourceSnapshotRoot); err != nil {
		return nil, err
	} else if existing != nil {
		if existing.ManifestDigest != manifestDigest {
			return nil, fmt.Errorf("outline repair candidate is bound to another manifest")
		}
		outlineReceipt, loadErr := st.LoadOutlineAllExecutionReceipt()
		if loadErr != nil {
			return nil, loadErr
		}
		if outlineReceipt == nil {
			if err := validatePipelineOutlineRepairCurrentTail(st, *existing); err != nil {
				return nil, err
			}
		}
		return existing, nil
	}
	if _, err := os.Lstat(filepath.Join(st.Dir(), filepath.FromSlash(pipelineOutlineRepairIntentPath))); err == nil {
		return nil, errPipelineOutlineRepairCandidateIncomplete
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	currentSourceRoot, err := pipelineOutlineAllSourceSnapshotRoot(st.Dir())
	if err != nil {
		return nil, err
	}
	if currentSourceRoot != sourceSnapshotRoot {
		return nil, fmt.Errorf("outline repair candidate does not match the live source snapshot CAS")
	}

	before, err := st.Outline.LoadLayeredOutline()
	if err != nil || len(before) == 0 {
		return nil, fmt.Errorf("outline repair requires layered outline: %w", err)
	}
	beforeLayeredDigest, err := domain.ComputeLayeredOutlineDigest(before)
	if err != nil {
		return nil, err
	}
	if beforeLayeredDigest != manifest.ExpectedLayeredDigest {
		return nil, fmt.Errorf("outline repair layered CAS failed: got %s want %s",
			beforeLayeredDigest, manifest.ExpectedLayeredDigest)
	}
	beforeFlat, err := st.Outline.LoadOutline()
	if err != nil {
		return nil, err
	}
	derivedBeforeFlat := domain.FlattenOutline(before)
	if !reflect.DeepEqual(beforeFlat, derivedBeforeFlat) {
		return nil, fmt.Errorf("outline repair refuses unsynchronized layered_outline.json and outline.json")
	}
	beforeFlatDigest, err := domain.ComputeFlatOutlineDigest(beforeFlat)
	if err != nil {
		return nil, err
	}
	after, replacedChapters, err := buildPipelineOutlineRepairResult(before, manifest)
	if err != nil {
		return nil, err
	}
	afterLayeredDigest, err := domain.ComputeLayeredOutlineDigest(after)
	if err != nil {
		return nil, err
	}
	afterFlat := domain.FlattenOutline(after)
	afterFlatDigest, err := domain.ComputeFlatOutlineDigest(afterFlat)
	if err != nil {
		return nil, err
	}
	if afterLayeredDigest == beforeLayeredDigest {
		return nil, fmt.Errorf("outline repair manifest produces no layered outline change")
	}

	intent, err := signPipelineOutlineRepairIntent(pipelineOutlineRepairIntent{
		Version: pipelineOutlineRepairIntentVersion, Operation: 0,
		AttemptID: attemptID, SourceSnapshotRoot: sourceSnapshotRoot,
		Manifest: manifest, ManifestDigest: manifestDigest,
		BeforeLayeredDigest: beforeLayeredDigest, BeforeFlatDigest: beforeFlatDigest,
	})
	if err != nil {
		return nil, err
	}
	if _, err := writePipelinePlanningJSON(
		filepath.Join(st.Dir(), filepath.FromSlash(pipelineOutlineRepairIntentPath)), intent,
	); err != nil {
		return nil, fmt.Errorf("write outline repair operation 0 intent: %w", err)
	}
	if err := st.Outline.SaveLayeredOutline(after); err != nil {
		return nil, fmt.Errorf("write repaired layered outline candidate: %w", err)
	}
	if err := st.Outline.SaveOutline(afterFlat); err != nil {
		return nil, fmt.Errorf("write repaired flat outline candidate: %w", err)
	}
	receipt, err := signPipelineOutlineRepairReceipt(pipelineOutlineRepairReceipt{
		Version: pipelineOutlineRepairReceiptVersion, Operation: 0,
		AttemptID: attemptID, SourceSnapshotRoot: sourceSnapshotRoot,
		ManifestDigest: manifestDigest, IntentDigest: intent.IntentDigest,
		BeforeLayeredDigest: beforeLayeredDigest, BeforeFlatDigest: beforeFlatDigest,
		AfterLayeredDigest: afterLayeredDigest, AfterFlatDigest: afterFlatDigest,
		AppliedArcCount: len(manifest.Arcs), ReplacedChapterCount: replacedChapters,
	})
	if err != nil {
		return nil, err
	}
	if err := validatePipelineOutlineRepairCurrentTail(st, receipt); err != nil {
		return nil, err
	}
	if _, err := writePipelinePlanningJSON(
		filepath.Join(st.Dir(), filepath.FromSlash(pipelineOutlineRepairReceiptPath)), receipt,
	); err != nil {
		return nil, fmt.Errorf("write outline repair operation 0 receipt: %w", err)
	}
	return &receipt, nil
}

func loadPipelineOutlineRepairEvidence(
	st *store.Store,
	attemptID, sourceSnapshotRoot string,
) (*pipelineOutlineRepairReceipt, error) {
	receiptPath := filepath.Join(st.Dir(), filepath.FromSlash(pipelineOutlineRepairReceiptPath))
	var receipt pipelineOutlineRepairReceipt
	if err := readPipelinePlanningJSON(receiptPath, &receipt); err != nil {
		if os.IsNotExist(err) {
			if _, intentErr := os.Lstat(filepath.Join(st.Dir(), filepath.FromSlash(pipelineOutlineRepairIntentPath))); intentErr == nil {
				return nil, errPipelineOutlineRepairCandidateIncomplete
			} else if !os.IsNotExist(intentErr) {
				return nil, intentErr
			}
			return nil, nil
		}
		return nil, err
	}
	intentPath := filepath.Join(st.Dir(), filepath.FromSlash(pipelineOutlineRepairIntentPath))
	var intent pipelineOutlineRepairIntent
	if err := readPipelinePlanningJSON(intentPath, &intent); err != nil {
		return nil, fmt.Errorf("outline repair receipt lacks operation 0 intent: %w", err)
	}
	storedIntentDigest := intent.IntentDigest
	validatedIntent, err := signPipelineOutlineRepairIntent(intent)
	if err != nil {
		return nil, err
	}
	manifestDigest, err := pipelineProjectAllDigestE(intent.Manifest)
	if err != nil {
		return nil, err
	}
	storedReceiptDigest := receipt.ReceiptDigest
	validatedReceipt, err := signPipelineOutlineRepairReceipt(receipt)
	if err != nil {
		return nil, err
	}
	if intent.Version != pipelineOutlineRepairIntentVersion || intent.Operation != 0 ||
		intent.AttemptID != attemptID || intent.SourceSnapshotRoot != sourceSnapshotRoot ||
		intent.ManifestDigest != manifestDigest || intent.ManifestDigest == "" ||
		intent.BeforeLayeredDigest != intent.Manifest.ExpectedLayeredDigest ||
		validatedIntent.IntentDigest != storedIntentDigest ||
		receipt.Version != pipelineOutlineRepairReceiptVersion || receipt.Operation != 0 ||
		receipt.AttemptID != attemptID || receipt.SourceSnapshotRoot != sourceSnapshotRoot ||
		receipt.ManifestDigest != intent.ManifestDigest || receipt.IntentDigest != intent.IntentDigest ||
		receipt.BeforeLayeredDigest != intent.BeforeLayeredDigest ||
		receipt.BeforeFlatDigest != intent.BeforeFlatDigest ||
		receipt.AfterLayeredDigest == "" || receipt.AfterFlatDigest == "" ||
		receipt.AppliedArcCount != len(intent.Manifest.Arcs) ||
		receipt.ReplacedChapterCount != pipelineOutlineRepairReplacementCount(intent.Manifest) ||
		validatedReceipt.ReceiptDigest != storedReceiptDigest {
		return nil, fmt.Errorf("outline repair operation 0 evidence is invalid or drifted")
	}
	if err := validatePipelineOutlineRepairManifest(intent.Manifest); err != nil {
		return nil, fmt.Errorf("outline repair operation 0 intent manifest: %w", err)
	}
	return &receipt, nil
}

// pipelineOutlineAllAttemptIDFromReceipt replays the deterministic attempt
// identity from published evidence. SourceSnapshotRoot intentionally remains
// the pre-repair live baseline; operation 0's after digest is the first link
// in the candidate mutation chain, not a replacement source snapshot.
func pipelineOutlineAllAttemptIDFromReceipt(
	st *store.Store,
	receipt *domain.OutlineAllExecutionReceipt,
) (string, error) {
	if st == nil || receipt == nil {
		return "", fmt.Errorf("outline-all attempt replay requires store and receipt")
	}
	executionIdentity := receipt.ModelIdentityDigest + "\n" + receipt.PromptProtocolDigest
	repair, err := loadPipelineOutlineRepairEvidence(st, receipt.AttemptID, receipt.SourceSnapshotRoot)
	if err != nil {
		return "", err
	}
	if repair != nil {
		executionIdentity += "\noutline-repair=" + repair.ManifestDigest
	}
	return outlineAllAttemptID(receipt.SourceSnapshotRoot, executionIdentity), nil
}

func validatePipelineOutlineRepairCurrentTail(st *store.Store, receipt pipelineOutlineRepairReceipt) error {
	volumes, err := st.Outline.LoadLayeredOutline()
	if err != nil {
		return err
	}
	entries, err := st.Outline.LoadOutline()
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(entries, domain.FlattenOutline(volumes)) {
		return fmt.Errorf("outline repair operation 0 candidate views are not synchronized")
	}
	layeredDigest, err := domain.ComputeLayeredOutlineDigest(volumes)
	if err != nil {
		return err
	}
	flatDigest, err := domain.ComputeFlatOutlineDigest(entries)
	if err != nil {
		return err
	}
	if layeredDigest != receipt.AfterLayeredDigest || flatDigest != receipt.AfterFlatDigest {
		return fmt.Errorf("outline repair operation 0 candidate tail drifted")
	}
	return nil
}

func pipelineOutlineRepairReplacementCount(manifest pipelineOutlineRepairManifest) int {
	count := 0
	for _, repair := range manifest.Arcs {
		count += len(repair.ChapterReplacements)
	}
	return count
}

func buildPipelineOutlineRepairResult(
	before []domain.VolumeOutline,
	manifest pipelineOutlineRepairManifest,
) ([]domain.VolumeOutline, int, error) {
	after := clonePipelineOutlineVolumes(before)
	type arcKey struct{ volume, arc int }
	repairs := make(map[arcKey]pipelineOutlineArcRepair, len(manifest.Arcs))
	for _, repair := range manifest.Arcs {
		repairs[arcKey{repair.Volume, repair.Arc}] = repair
	}
	found := make(map[arcKey]struct{}, len(repairs))
	replaced := 0
	cursor := 1
	for vi := range after {
		for ai := range after[vi].Arcs {
			arc := &after[vi].Arcs[ai]
			span := arc.ChapterSpan()
			start, end := cursor, cursor+span-1
			cursor += span
			key := arcKey{after[vi].Index, arc.Index}
			repair, ok := repairs[key]
			if !ok {
				continue
			}
			found[key] = struct{}{}
			if start != repair.ExpectedStartChapter || end != repair.ExpectedEndChapter {
				return nil, 0, fmt.Errorf(
					"outline repair V%dA%d boundary CAS failed: got %d-%d want %d-%d",
					key.volume, key.arc, start, end,
					repair.ExpectedStartChapter, repair.ExpectedEndChapter)
			}
			if repair.NewGoal != nil {
				arc.Goal = *repair.NewGoal
			}
			if len(repair.ChapterReplacements) > 0 && !arc.IsExpanded() {
				return nil, 0, fmt.Errorf("outline repair cannot replace chapters in unexpanded V%dA%d", key.volume, key.arc)
			}
			for _, replacement := range repair.ChapterReplacements {
				local := replacement.Chapter - start
				if local < 0 || local >= len(arc.Chapters) {
					return nil, 0, fmt.Errorf("outline repair chapter %d is not materialized in V%dA%d",
						replacement.Chapter, key.volume, key.arc)
				}
				original := arc.Chapters[local]
				updated := original
				updated.Title = replacement.Title
				updated.CoreEvent = replacement.CoreEvent
				updated.Hook = replacement.Hook
				updated.Scenes = append([]string(nil), replacement.Scenes...)
				arc.Chapters[local] = updated
				replaced++
			}
		}
	}
	if len(found) != len(repairs) {
		for key := range repairs {
			if _, ok := found[key]; !ok {
				return nil, 0, fmt.Errorf("outline repair target V%dA%d does not exist", key.volume, key.arc)
			}
		}
	}
	if domain.TotalChapters(after) != domain.TotalChapters(before) {
		return nil, 0, fmt.Errorf("outline repair changed the full-book chapter span")
	}
	if err := validatePipelineOutlineRepairPreservation(before, after, manifest); err != nil {
		return nil, 0, err
	}
	return after, replaced, nil
}

func clonePipelineOutlineVolumes(before []domain.VolumeOutline) []domain.VolumeOutline {
	if before == nil {
		return nil
	}
	after := make([]domain.VolumeOutline, len(before))
	copy(after, before)
	for vi := range after {
		if before[vi].Arcs == nil {
			after[vi].Arcs = nil
			continue
		}
		after[vi].Arcs = make([]domain.ArcOutline, len(before[vi].Arcs))
		copy(after[vi].Arcs, before[vi].Arcs)
		for ai := range after[vi].Arcs {
			beforeArc := before[vi].Arcs[ai]
			afterArc := &after[vi].Arcs[ai]
			if beforeArc.ContractRefs != nil {
				afterArc.ContractRefs = append(make([]domain.StoryContractRef, 0, len(beforeArc.ContractRefs)), beforeArc.ContractRefs...)
			}
			if beforeArc.Chapters == nil {
				afterArc.Chapters = nil
				continue
			}
			afterArc.Chapters = make([]domain.OutlineEntry, len(beforeArc.Chapters))
			copy(afterArc.Chapters, beforeArc.Chapters)
			for ci := range afterArc.Chapters {
				beforeChapter := beforeArc.Chapters[ci]
				if beforeChapter.Scenes != nil {
					afterArc.Chapters[ci].Scenes = append(make([]string, 0, len(beforeChapter.Scenes)), beforeChapter.Scenes...)
				}
				if beforeChapter.ContractRefs != nil {
					afterArc.Chapters[ci].ContractRefs = append(
						make([]domain.StoryContractRef, 0, len(beforeChapter.ContractRefs)),
						beforeChapter.ContractRefs...,
					)
				}
			}
		}
	}
	return after
}

func validatePipelineOutlineRepairPreservation(
	before, after []domain.VolumeOutline,
	manifest pipelineOutlineRepairManifest,
) error {
	if len(before) != len(after) {
		return fmt.Errorf("outline repair changed volume count")
	}
	type arcKey struct{ volume, arc int }
	targets := make(map[arcKey]map[int]struct{}, len(manifest.Arcs))
	for _, repair := range manifest.Arcs {
		chapters := make(map[int]struct{}, len(repair.ChapterReplacements))
		for _, replacement := range repair.ChapterReplacements {
			chapters[replacement.Chapter] = struct{}{}
		}
		targets[arcKey{repair.Volume, repair.Arc}] = chapters
	}
	cursor := 1
	for vi := range before {
		bv, av := before[vi], after[vi]
		if bv.Index != av.Index || bv.Title != av.Title || bv.Theme != av.Theme || len(bv.Arcs) != len(av.Arcs) {
			return fmt.Errorf("outline repair changed volume identity/title/theme/arcs")
		}
		for ai := range bv.Arcs {
			ba, aa := bv.Arcs[ai], av.Arcs[ai]
			key := arcKey{bv.Index, ba.Index}
			allowedChapters, targeted := targets[key]
			if ba.Index != aa.Index || ba.Title != aa.Title ||
				ba.EstimatedChapters != aa.EstimatedChapters ||
				!reflect.DeepEqual(ba.ContractRefs, aa.ContractRefs) || len(ba.Chapters) != len(aa.Chapters) {
				return fmt.Errorf("outline repair changed V%dA%d title/contracts/span", key.volume, key.arc)
			}
			if !targeted && !reflect.DeepEqual(ba, aa) {
				return fmt.Errorf("outline repair changed non-target V%dA%d", key.volume, key.arc)
			}
			for ci := range ba.Chapters {
				chapter := cursor + ci
				if _, allowed := allowedChapters[chapter]; !allowed && !reflect.DeepEqual(ba.Chapters[ci], aa.Chapters[ci]) {
					return fmt.Errorf("outline repair changed non-target chapter %d", chapter)
				}
				if _, allowed := allowedChapters[chapter]; allowed {
					if ba.Chapters[ci].Chapter != aa.Chapters[ci].Chapter ||
						!reflect.DeepEqual(ba.Chapters[ci].ContractRefs, aa.Chapters[ci].ContractRefs) {
						return fmt.Errorf("outline repair changed chapter %d number/contract_refs", chapter)
					}
				}
			}
			cursor += ba.ChapterSpan()
		}
	}
	return nil
}
