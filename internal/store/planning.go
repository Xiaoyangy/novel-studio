package store

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

const (
	planningBookSkeletonPath  = "meta/planning/book_causal_skeleton.json"
	planningInvalidationsPath = "meta/planning/invalidations.jsonl"
)

// PlanningStore owns only speculative/projected artifacts under
// meta/planning/. It intentionally has no API that writes progress, world,
// chapters, or drafts, so batch planning cannot accidentally advance canon.
type PlanningStore struct {
	io *IO
}

func NewPlanningStore(io *IO) *PlanningStore {
	return &PlanningStore{io: io}
}

func planningVolumeSkeletonPath(volume int) string {
	return filepath.Join("meta", "planning", "volumes", fmt.Sprintf("%03d.json", volume))
}

func planningChapterManifestPath(chapter int) string {
	return filepath.Join("meta", "planning", "chapters", fmt.Sprintf("%06d.json", chapter))
}

func (s *PlanningStore) SaveBookCausalSkeleton(skeleton domain.BookCausalSkeleton) error {
	if skeleton.Version == "" {
		skeleton.Version = domain.PlanningStoreVersion
	}
	if err := domain.ValidateBookCausalSkeleton(skeleton); err != nil {
		return err
	}
	return s.io.WriteJSON(planningBookSkeletonPath, skeleton)
}

func (s *PlanningStore) LoadBookCausalSkeleton() (*domain.BookCausalSkeleton, error) {
	var skeleton domain.BookCausalSkeleton
	if err := s.io.ReadJSON(planningBookSkeletonPath, &skeleton); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if err := domain.ValidateBookCausalSkeleton(skeleton); err != nil {
		return nil, err
	}
	return &skeleton, nil
}

func (s *PlanningStore) SaveVolumeCausalSkeleton(skeleton domain.VolumeCausalSkeleton) error {
	if skeleton.Version == "" {
		skeleton.Version = domain.PlanningStoreVersion
	}
	if err := domain.ValidateVolumeCausalSkeleton(skeleton); err != nil {
		return err
	}
	return s.io.WriteJSON(planningVolumeSkeletonPath(skeleton.Volume), skeleton)
}

func (s *PlanningStore) LoadVolumeCausalSkeleton(volume int) (*domain.VolumeCausalSkeleton, error) {
	if volume <= 0 {
		return nil, fmt.Errorf("planning volume must be > 0")
	}
	var skeleton domain.VolumeCausalSkeleton
	if err := s.io.ReadJSON(planningVolumeSkeletonPath(volume), &skeleton); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if err := domain.ValidateVolumeCausalSkeleton(skeleton); err != nil {
		return nil, err
	}
	if skeleton.Volume != volume {
		return nil, fmt.Errorf("planning volume path %d contains volume %d", volume, skeleton.Volume)
	}
	return &skeleton, nil
}

// SaveStagedChapterPlanManifest validates the complete generation before an
// atomic replacement. This rejects both a broken predecessor link and an edit
// that would silently break an already-staged successor.
func (s *PlanningStore) SaveStagedChapterPlanManifest(manifest domain.StagedChapterPlanManifest) error {
	manifest = normalizeStagedChapterPlanManifest(manifest)
	if err := domain.ValidateStagedChapterPlanManifest(manifest); err != nil {
		return err
	}
	return s.io.WithWriteLock(func() error {
		current, err := s.loadStagedChapterPlanManifestsUnlocked()
		if err != nil {
			return err
		}
		replaced := false
		for i := range current {
			if current[i].Chapter == manifest.Chapter {
				current[i] = manifest
				replaced = true
				break
			}
		}
		if !replaced {
			current = append(current, manifest)
		}
		if err := domain.ValidateStagedChapterPlanChain(current); err != nil {
			return err
		}
		return s.io.WriteJSONUnlocked(planningChapterManifestPath(manifest.Chapter), manifest)
	})
}

// ReplaceStagedChapterPlanManifests replaces one complete speculative
// generation under a single planning-store write lock. It validates the new
// chain before touching disk, writes every replacement via atomic file rename,
// then removes manifests absent from the new set. An empty slice clears the
// staged generation. Payload files are intentionally outside this operation.
func (s *PlanningStore) ReplaceStagedChapterPlanManifests(manifests []domain.StagedChapterPlanManifest) error {
	normalized := make([]domain.StagedChapterPlanManifest, len(manifests))
	encoded := make(map[int][]byte, len(manifests))
	for i, manifest := range manifests {
		manifest = normalizeStagedChapterPlanManifest(manifest)
		normalized[i] = manifest
		raw, err := json.MarshalIndent(manifest, "", "  ")
		if err != nil {
			return err
		}
		encoded[manifest.Chapter] = raw
	}
	if err := domain.ValidateStagedChapterPlanChain(normalized); err != nil {
		return err
	}
	return s.io.WithWriteLock(func() error {
		dir := s.io.path(filepath.Join("meta", "planning", "chapters"))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		existing, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		keep := make(map[string]struct{}, len(encoded))
		chapters := make([]int, 0, len(encoded))
		for chapter := range encoded {
			chapters = append(chapters, chapter)
		}
		sort.Ints(chapters)
		for _, chapter := range chapters {
			rel := planningChapterManifestPath(chapter)
			if err := s.io.WriteFileUnlocked(rel, encoded[chapter]); err != nil {
				return err
			}
			keep[filepath.Base(rel)] = struct{}{}
		}
		for _, entry := range existing {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
				continue
			}
			stem := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
			if _, err := strconv.Atoi(stem); err != nil {
				continue
			}
			if _, retained := keep[entry.Name()]; retained {
				continue
			}
			if err := s.io.RemoveFileUnlocked(filepath.Join("meta", "planning", "chapters", entry.Name())); err != nil {
				return err
			}
		}
		return nil
	})
}

func normalizeStagedChapterPlanManifest(manifest domain.StagedChapterPlanManifest) domain.StagedChapterPlanManifest {
	if manifest.Version == "" {
		manifest.Version = domain.PlanningStoreVersion
	}
	if manifest.ProjectedState.Version == "" {
		manifest.ProjectedState.Version = domain.PlanningStoreVersion
	}
	return manifest
}

func (s *PlanningStore) LoadStagedChapterPlanManifest(chapter int) (*domain.StagedChapterPlanManifest, error) {
	if chapter <= 0 {
		return nil, fmt.Errorf("planning chapter must be > 0")
	}
	s.io.mu.RLock()
	defer s.io.mu.RUnlock()
	manifests, err := s.loadStagedChapterPlanManifestsUnlocked()
	if err != nil {
		return nil, err
	}
	for i := range manifests {
		if manifests[i].Chapter == chapter {
			manifest := manifests[i]
			return &manifest, nil
		}
	}
	return nil, nil
}

func (s *PlanningStore) LoadStagedChapterPlanManifests() ([]domain.StagedChapterPlanManifest, error) {
	s.io.mu.RLock()
	defer s.io.mu.RUnlock()
	return s.loadStagedChapterPlanManifestsUnlocked()
}

func (s *PlanningStore) loadStagedChapterPlanManifestsUnlocked() ([]domain.StagedChapterPlanManifest, error) {
	dir := s.io.path(filepath.Join("meta", "planning", "chapters"))
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	manifests := make([]domain.StagedChapterPlanManifest, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		stem := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		if _, err := strconv.Atoi(stem); err != nil {
			continue
		}
		var manifest domain.StagedChapterPlanManifest
		rel := filepath.Join("meta", "planning", "chapters", entry.Name())
		if err := s.io.ReadJSONUnlocked(rel, &manifest); err != nil {
			return nil, err
		}
		manifests = append(manifests, manifest)
	}
	sort.Slice(manifests, func(i, j int) bool { return manifests[i].Chapter < manifests[j].Chapter })
	if err := domain.ValidateStagedChapterPlanChain(manifests); err != nil {
		return nil, err
	}
	return manifests, nil
}

// AppendInvalidation is the only invalidation write API. It adds a hash-chained
// JSONL record and never rewrites or truncates existing history.
func (s *PlanningStore) AppendInvalidation(record domain.PlanningInvalidationRecord) error {
	if record.Version == "" {
		record.Version = domain.PlanningStoreVersion
	}
	return s.io.WithWriteLock(func() error {
		records, err := s.loadInvalidationsUnlocked()
		if err != nil {
			return err
		}
		for i := range records {
			if records[i].ID == record.ID {
				return fmt.Errorf("duplicate planning invalidation id %q", record.ID)
			}
		}
		previousRoot := ""
		if len(records) > 0 {
			previousRoot = records[len(records)-1].RecordRoot
		}
		if record.PreviousRecordRoot != "" && record.PreviousRecordRoot != previousRoot {
			return fmt.Errorf("planning invalidation previous root mismatch: got %s want %s", record.PreviousRecordRoot, previousRoot)
		}
		record.PreviousRecordRoot = previousRoot
		computed, err := domain.PlanningInvalidationRecordRoot(record)
		if err != nil {
			return err
		}
		if record.RecordRoot != "" && record.RecordRoot != computed {
			return fmt.Errorf("planning invalidation record root mismatch: got %s want %s", record.RecordRoot, computed)
		}
		record.RecordRoot = computed
		if err := domain.ValidatePlanningInvalidationRecord(record); err != nil {
			return err
		}
		raw, err := json.Marshal(record)
		if err != nil {
			return err
		}
		raw = append(raw, '\n')
		return s.io.AppendLineUnlocked(planningInvalidationsPath, raw)
	})
}

func (s *PlanningStore) LoadInvalidations() ([]domain.PlanningInvalidationRecord, error) {
	s.io.mu.RLock()
	defer s.io.mu.RUnlock()
	return s.loadInvalidationsUnlocked()
}

func (s *PlanningStore) loadInvalidationsUnlocked() ([]domain.PlanningInvalidationRecord, error) {
	raw, err := s.io.ReadFileUnlocked(planningInvalidationsPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var records []domain.PlanningInvalidationRecord
	previousRoot := ""
	ids := make(map[string]struct{})
	line := 0
	for scanner.Scan() {
		line++
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			continue
		}
		var record domain.PlanningInvalidationRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return nil, fmt.Errorf("decode planning invalidation line %d: %w", line, err)
		}
		if err := domain.ValidatePlanningInvalidationRecord(record); err != nil {
			return nil, fmt.Errorf("validate planning invalidation line %d: %w", line, err)
		}
		if record.PreviousRecordRoot != previousRoot {
			return nil, fmt.Errorf("planning invalidation chain broken at line %d", line)
		}
		if _, exists := ids[record.ID]; exists {
			return nil, fmt.Errorf("duplicate planning invalidation id %q", record.ID)
		}
		ids[record.ID] = struct{}{}
		records = append(records, record)
		previousRoot = record.RecordRoot
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return records, nil
}
