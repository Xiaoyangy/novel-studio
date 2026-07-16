package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
)

const (
	// PlanningStoreVersion is the on-disk schema version for meta/planning.
	PlanningStoreVersion = "planning-store.v1"
)

// PlanningAuthority deliberately has no canonical value. Planning artifacts
// describe a speculative future or a projected state only; neither may be read
// as committed story canon. Canon continues to live in the existing committed
// chapter/world/progress stores.
type PlanningAuthority string

const (
	PlanningAuthoritySpeculative PlanningAuthority = "speculative"
	PlanningAuthorityProjected   PlanningAuthority = "projected"
)

// IsCanonical is always false for every supported planning authority.
func (a PlanningAuthority) IsCanonical() bool { return false }

func ValidatePlanningAuthority(a PlanningAuthority) error {
	switch a {
	case PlanningAuthoritySpeculative, PlanningAuthorityProjected:
		return nil
	default:
		return fmt.Errorf("planning authority %q is unsupported; planning artifacts must remain speculative/projected and non-canonical", a)
	}
}

// PlanningRealization records whether a staged plan has produced prose. Even a
// rendered planning artifact remains non-canonical until the normal commit
// pipeline independently validates and commits that prose.
type PlanningRealization string

const (
	PlanningRealizationStaged      PlanningRealization = "staged"
	PlanningRealizationRendered    PlanningRealization = "rendered"
	PlanningRealizationInvalidated PlanningRealization = "invalidated"
)

func ValidatePlanningRealization(r PlanningRealization) error {
	switch r {
	case PlanningRealizationStaged, PlanningRealizationRendered, PlanningRealizationInvalidated:
		return nil
	default:
		return fmt.Errorf("unsupported planning realization %q", r)
	}
}

// PlanningDependency is one exact input used to derive a planning generation.
type PlanningDependency struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	SHA256 string `json:"sha256"`
}

// DependencyFingerprint binds a speculative generation to the exact committed
// canon root and source inputs from which it was projected.
type DependencyFingerprint struct {
	GenerationID  string               `json:"generation_id"`
	BaseCanonRoot string               `json:"base_canon_root"`
	Dependencies  []PlanningDependency `json:"dependencies"`
	RootSHA256    string               `json:"root_sha256"`
}

// NewDependencyFingerprint normalizes dependency order and computes the stable
// root used by all artifacts in one planning generation.
func NewDependencyFingerprint(generationID, baseCanonRoot string, dependencies []PlanningDependency) (DependencyFingerprint, error) {
	out := DependencyFingerprint{
		GenerationID:  strings.TrimSpace(generationID),
		BaseCanonRoot: strings.TrimSpace(baseCanonRoot),
		Dependencies:  normalizedPlanningDependencies(dependencies),
	}
	root, err := ComputeDependencyRoot(out.GenerationID, out.BaseCanonRoot, out.Dependencies)
	if err != nil {
		return DependencyFingerprint{}, err
	}
	out.RootSHA256 = root
	return out, nil
}

// ComputeDependencyRoot returns a deterministic root independent of caller
// slice order.
func ComputeDependencyRoot(generationID, baseCanonRoot string, dependencies []PlanningDependency) (string, error) {
	generationID = strings.TrimSpace(generationID)
	baseCanonRoot = strings.TrimSpace(baseCanonRoot)
	dependencies = normalizedPlanningDependencies(dependencies)
	if generationID == "" {
		return "", fmt.Errorf("generation_id is required")
	}
	if baseCanonRoot == "" {
		return "", fmt.Errorf("base_canon_root is required")
	}
	if len(dependencies) == 0 {
		return "", fmt.Errorf("at least one planning dependency is required")
	}
	seen := make(map[string]struct{}, len(dependencies))
	for i, dep := range dependencies {
		if dep.Kind == "" || dep.ID == "" || dep.SHA256 == "" {
			return "", fmt.Errorf("dependencies[%d] requires kind, id, and sha256", i)
		}
		key := dep.Kind + "\x00" + dep.ID
		if _, exists := seen[key]; exists {
			return "", fmt.Errorf("duplicate planning dependency %s/%s", dep.Kind, dep.ID)
		}
		seen[key] = struct{}{}
	}
	return DeterministicPlanningHash(struct {
		GenerationID  string               `json:"generation_id"`
		BaseCanonRoot string               `json:"base_canon_root"`
		Dependencies  []PlanningDependency `json:"dependencies"`
	}{
		GenerationID:  generationID,
		BaseCanonRoot: baseCanonRoot,
		Dependencies:  dependencies,
	})
}

func ValidateDependencyFingerprint(f DependencyFingerprint) error {
	if strings.TrimSpace(f.RootSHA256) == "" {
		return fmt.Errorf("dependency root_sha256 is required")
	}
	want, err := ComputeDependencyRoot(f.GenerationID, f.BaseCanonRoot, f.Dependencies)
	if err != nil {
		return err
	}
	if f.RootSHA256 != want {
		return fmt.Errorf("dependency fingerprint root mismatch: got %s want %s", f.RootSHA256, want)
	}
	return nil
}

func normalizedPlanningDependencies(in []PlanningDependency) []PlanningDependency {
	out := make([]PlanningDependency, len(in))
	for i, dep := range in {
		out[i] = PlanningDependency{
			Kind:   strings.TrimSpace(dep.Kind),
			ID:     strings.TrimSpace(dep.ID),
			SHA256: strings.TrimSpace(dep.SHA256),
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		return out[i].SHA256 < out[j].SHA256
	})
	return out
}

// CausalSkeletonNode is deliberately prose-free: it records only a causal
// obligation and its predecessor/successor relationships.
type CausalSkeletonNode struct {
	ID          string   `json:"id"`
	Cause       string   `json:"cause"`
	Effect      string   `json:"effect"`
	DependsOn   []string `json:"depends_on,omitempty"`
	ChapterFrom int      `json:"chapter_from,omitempty"`
	ChapterTo   int      `json:"chapter_to,omitempty"`
}

type BookCausalSkeleton struct {
	Version               string                `json:"version"`
	GenerationID          string                `json:"generation_id"`
	BaseCanonChapter      int                   `json:"base_canon_chapter"`
	BaseCanonRoot         string                `json:"base_canon_root"`
	DependencyFingerprint DependencyFingerprint `json:"dependency_fingerprint"`
	Authority             PlanningAuthority     `json:"authority"`
	Realization           PlanningRealization   `json:"realization"`
	Nodes                 []CausalSkeletonNode  `json:"nodes"`
	CreatedAt             string                `json:"created_at,omitempty"`
}

type VolumeCausalSkeleton struct {
	Version               string                `json:"version"`
	GenerationID          string                `json:"generation_id"`
	BaseCanonChapter      int                   `json:"base_canon_chapter"`
	BaseCanonRoot         string                `json:"base_canon_root"`
	DependencyFingerprint DependencyFingerprint `json:"dependency_fingerprint"`
	Authority             PlanningAuthority     `json:"authority"`
	Realization           PlanningRealization   `json:"realization"`
	Volume                int                   `json:"volume"`
	ChapterFrom           int                   `json:"chapter_from"`
	ChapterTo             int                   `json:"chapter_to"`
	Nodes                 []CausalSkeletonNode  `json:"nodes"`
	CreatedAt             string                `json:"created_at,omitempty"`
}

// ProjectedStateReceipt proves how one speculative chapter transforms the
// projected state root. It is not a world-state snapshot and must never be
// merged into canonical ledgers before normal chapter commit.
type ProjectedStateReceipt struct {
	Version        string              `json:"version"`
	Chapter        int                 `json:"chapter"`
	GenerationID   string              `json:"generation_id"`
	BaseCanonRoot  string              `json:"base_canon_root"`
	DependencyRoot string              `json:"dependency_root"`
	Authority      PlanningAuthority   `json:"authority"`
	Realization    PlanningRealization `json:"realization"`
	PreStateRoot   string              `json:"pre_state_root"`
	ProjectionRoot string              `json:"projection_root"`
	PostStateRoot  string              `json:"post_state_root"`
}

// StagedChapterPlanManifest is the durable hand-off between batch causal
// planning and later chapter-by-chapter prose rendering.
type StagedChapterPlanManifest struct {
	Version               string                `json:"version"`
	Chapter               int                   `json:"chapter"`
	Volume                int                   `json:"volume,omitempty"`
	GenerationID          string                `json:"generation_id"`
	BaseCanonChapter      int                   `json:"base_canon_chapter"`
	BaseCanonRoot         string                `json:"base_canon_root"`
	DependencyFingerprint DependencyFingerprint `json:"dependency_fingerprint"`
	Authority             PlanningAuthority     `json:"authority"`
	Realization           PlanningRealization   `json:"realization"`
	PlanPath              string                `json:"plan_path"`
	PlanSHA256            string                `json:"plan_sha256"`
	ProjectedState        ProjectedStateReceipt `json:"projected_state"`
	CreatedAt             string                `json:"created_at,omitempty"`
}

// PlanningInvalidationRecord is appended to meta/planning/invalidations.jsonl.
// It records why staged projections became stale without mutating canon or
// rewriting historical invalidation entries.
type PlanningInvalidationRecord struct {
	Version               string                `json:"version"`
	ID                    string                `json:"id"`
	GenerationID          string                `json:"generation_id"`
	BaseCanonRoot         string                `json:"base_canon_root"`
	DependencyFingerprint DependencyFingerprint `json:"dependency_fingerprint"`
	TargetKind            string                `json:"target_kind"`
	TargetID              string                `json:"target_id"`
	InvalidatedRoot       string                `json:"invalidated_root"`
	Reason                string                `json:"reason"`
	CreatedAt             string                `json:"created_at"`
	PreviousRecordRoot    string                `json:"previous_record_root,omitempty"`
	RecordRoot            string                `json:"record_root"`
}

// DeterministicPlanningHash hashes JSON using encoding/json's deterministic
// string-key ordering. Planning domain structs avoid floats and unordered
// semantic lists; callers should normalize set-like slices before hashing.
func DeterministicPlanningHash(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// DeriveProjectedStateRoot deterministically binds a projected transition to
// its generation, dependency root, predecessor root, and projection payload.
func DeriveProjectedStateRoot(chapter int, generationID, baseCanonRoot, dependencyRoot, preStateRoot, projectionRoot string) (string, error) {
	if chapter <= 0 {
		return "", fmt.Errorf("chapter must be > 0")
	}
	values := map[string]string{
		"generation_id":   strings.TrimSpace(generationID),
		"base_canon_root": strings.TrimSpace(baseCanonRoot),
		"dependency_root": strings.TrimSpace(dependencyRoot),
		"pre_state_root":  strings.TrimSpace(preStateRoot),
		"projection_root": strings.TrimSpace(projectionRoot),
	}
	for name, value := range values {
		if value == "" {
			return "", fmt.Errorf("%s is required", name)
		}
	}
	return DeterministicPlanningHash(struct {
		Chapter        int    `json:"chapter"`
		GenerationID   string `json:"generation_id"`
		BaseCanonRoot  string `json:"base_canon_root"`
		DependencyRoot string `json:"dependency_root"`
		PreStateRoot   string `json:"pre_state_root"`
		ProjectionRoot string `json:"projection_root"`
	}{
		Chapter:        chapter,
		GenerationID:   values["generation_id"],
		BaseCanonRoot:  values["base_canon_root"],
		DependencyRoot: values["dependency_root"],
		PreStateRoot:   values["pre_state_root"],
		ProjectionRoot: values["projection_root"],
	})
}

func ValidateBookCausalSkeleton(s BookCausalSkeleton) error {
	if err := validatePlanningEnvelope(s.Version, s.GenerationID, s.BaseCanonRoot, s.DependencyFingerprint, s.Authority, s.Realization); err != nil {
		return fmt.Errorf("book causal skeleton: %w", err)
	}
	if s.BaseCanonChapter < 0 {
		return fmt.Errorf("book causal skeleton: base_canon_chapter must be >= 0")
	}
	return validateCausalNodes(s.Nodes)
}

func ValidateVolumeCausalSkeleton(s VolumeCausalSkeleton) error {
	if err := validatePlanningEnvelope(s.Version, s.GenerationID, s.BaseCanonRoot, s.DependencyFingerprint, s.Authority, s.Realization); err != nil {
		return fmt.Errorf("volume causal skeleton: %w", err)
	}
	if s.BaseCanonChapter < 0 || s.Volume <= 0 || s.ChapterFrom <= 0 || s.ChapterTo < s.ChapterFrom {
		return fmt.Errorf("volume causal skeleton: invalid base chapter, volume, or chapter range")
	}
	return validateCausalNodes(s.Nodes)
}

func ValidateProjectedStateReceipt(r ProjectedStateReceipt) error {
	if r.Version != PlanningStoreVersion {
		return fmt.Errorf("projected state receipt: unsupported version %q", r.Version)
	}
	if r.Chapter <= 0 {
		return fmt.Errorf("projected state receipt: chapter must be > 0")
	}
	if strings.TrimSpace(r.GenerationID) == "" || strings.TrimSpace(r.BaseCanonRoot) == "" || strings.TrimSpace(r.DependencyRoot) == "" {
		return fmt.Errorf("projected state receipt: generation_id, base_canon_root, and dependency_root are required")
	}
	if r.Authority != PlanningAuthorityProjected {
		return fmt.Errorf("projected state receipt: authority must be projected, never canonical")
	}
	if err := ValidatePlanningRealization(r.Realization); err != nil {
		return fmt.Errorf("projected state receipt: %w", err)
	}
	if strings.TrimSpace(r.PreStateRoot) == "" || strings.TrimSpace(r.ProjectionRoot) == "" || strings.TrimSpace(r.PostStateRoot) == "" {
		return fmt.Errorf("projected state receipt: pre_state_root, projection_root, and post_state_root are required")
	}
	want, err := DeriveProjectedStateRoot(r.Chapter, r.GenerationID, r.BaseCanonRoot, r.DependencyRoot, r.PreStateRoot, r.ProjectionRoot)
	if err != nil {
		return fmt.Errorf("projected state receipt: %w", err)
	}
	if r.PostStateRoot != want {
		return fmt.Errorf("projected state receipt: post_state_root mismatch: got %s want %s", r.PostStateRoot, want)
	}
	return nil
}

func ValidateStagedChapterPlanManifest(m StagedChapterPlanManifest) error {
	if err := validatePlanningEnvelope(m.Version, m.GenerationID, m.BaseCanonRoot, m.DependencyFingerprint, m.Authority, m.Realization); err != nil {
		return fmt.Errorf("staged chapter plan: %w", err)
	}
	if m.Authority != PlanningAuthoritySpeculative {
		return fmt.Errorf("staged chapter plan: authority must be speculative, never canonical")
	}
	if m.Chapter <= 0 || m.BaseCanonChapter < 0 || m.Chapter <= m.BaseCanonChapter {
		return fmt.Errorf("staged chapter plan: chapter must be after base_canon_chapter")
	}
	if strings.TrimSpace(m.PlanPath) == "" || strings.TrimSpace(m.PlanSHA256) == "" {
		return fmt.Errorf("staged chapter plan: plan_path and plan_sha256 are required")
	}
	cleanPlanPath := path.Clean(strings.TrimSpace(m.PlanPath))
	if cleanPlanPath == "meta/planning" || !strings.HasPrefix(cleanPlanPath, "meta/planning/") {
		return fmt.Errorf("staged chapter plan: plan_path must stay under meta/planning")
	}
	if err := ValidateProjectedStateReceipt(m.ProjectedState); err != nil {
		return err
	}
	r := m.ProjectedState
	if r.Chapter != m.Chapter || r.GenerationID != m.GenerationID || r.BaseCanonRoot != m.BaseCanonRoot || r.DependencyRoot != m.DependencyFingerprint.RootSHA256 {
		return fmt.Errorf("staged chapter plan: projected state identity does not match manifest")
	}
	if r.Realization != m.Realization {
		return fmt.Errorf("staged chapter plan: projected state realization does not match manifest")
	}
	return nil
}

// ValidateStagedChapterPlanChain rejects gaps and state-root discontinuities.
// The first staged chapter must be rooted directly in the committed base canon;
// every later chapter's pre_state_root must equal its predecessor's
// post_state_root.
func ValidateStagedChapterPlanChain(manifests []StagedChapterPlanManifest) error {
	if len(manifests) == 0 {
		return nil
	}
	ordered := append([]StagedChapterPlanManifest(nil), manifests...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Chapter < ordered[j].Chapter })
	for i := range ordered {
		if err := ValidateStagedChapterPlanManifest(ordered[i]); err != nil {
			return err
		}
		if i == 0 {
			if ordered[i].Chapter != ordered[i].BaseCanonChapter+1 {
				return fmt.Errorf("planning chain starts at chapter %d but base canon ends at chapter %d", ordered[i].Chapter, ordered[i].BaseCanonChapter)
			}
			if ordered[i].ProjectedState.PreStateRoot != ordered[i].BaseCanonRoot {
				return fmt.Errorf("chapter %d pre_state_root does not match base_canon_root", ordered[i].Chapter)
			}
			continue
		}
		prev, current := ordered[i-1], ordered[i]
		if current.Chapter != prev.Chapter+1 {
			return fmt.Errorf("planning chain gap between chapters %d and %d", prev.Chapter, current.Chapter)
		}
		if current.GenerationID != prev.GenerationID || current.BaseCanonChapter != prev.BaseCanonChapter ||
			current.BaseCanonRoot != prev.BaseCanonRoot || current.DependencyFingerprint.RootSHA256 != prev.DependencyFingerprint.RootSHA256 {
			return fmt.Errorf("chapter %d planning generation/base canon/dependencies differ from predecessor", current.Chapter)
		}
		if current.ProjectedState.PreStateRoot != prev.ProjectedState.PostStateRoot {
			return fmt.Errorf("chapter %d pre_state_root %s does not match chapter %d post_state_root %s",
				current.Chapter, current.ProjectedState.PreStateRoot, prev.Chapter, prev.ProjectedState.PostStateRoot)
		}
	}
	return nil
}

func ValidatePlanningInvalidationRecord(r PlanningInvalidationRecord) error {
	if r.Version != PlanningStoreVersion {
		return fmt.Errorf("planning invalidation: unsupported version %q", r.Version)
	}
	if strings.TrimSpace(r.ID) == "" || strings.TrimSpace(r.GenerationID) == "" || strings.TrimSpace(r.BaseCanonRoot) == "" {
		return fmt.Errorf("planning invalidation: id, generation_id, and base_canon_root are required")
	}
	if err := ValidateDependencyFingerprint(r.DependencyFingerprint); err != nil {
		return fmt.Errorf("planning invalidation: %w", err)
	}
	if r.DependencyFingerprint.GenerationID != r.GenerationID || r.DependencyFingerprint.BaseCanonRoot != r.BaseCanonRoot {
		return fmt.Errorf("planning invalidation: dependency identity does not match record")
	}
	if strings.TrimSpace(r.TargetKind) == "" || strings.TrimSpace(r.TargetID) == "" ||
		strings.TrimSpace(r.InvalidatedRoot) == "" || strings.TrimSpace(r.Reason) == "" || strings.TrimSpace(r.CreatedAt) == "" {
		return fmt.Errorf("planning invalidation: target_kind, target_id, invalidated_root, reason, and created_at are required")
	}
	if strings.TrimSpace(r.RecordRoot) == "" {
		return fmt.Errorf("planning invalidation: record_root is required")
	}
	want, err := PlanningInvalidationRecordRoot(r)
	if err != nil {
		return err
	}
	if r.RecordRoot != want {
		return fmt.Errorf("planning invalidation: record_root mismatch: got %s want %s", r.RecordRoot, want)
	}
	return nil
}

func PlanningInvalidationRecordRoot(r PlanningInvalidationRecord) (string, error) {
	r.RecordRoot = ""
	return DeterministicPlanningHash(r)
}

func validatePlanningEnvelope(version, generationID, baseCanonRoot string, dependency DependencyFingerprint, authority PlanningAuthority, realization PlanningRealization) error {
	if version != PlanningStoreVersion {
		return fmt.Errorf("unsupported version %q", version)
	}
	if strings.TrimSpace(generationID) == "" || strings.TrimSpace(baseCanonRoot) == "" {
		return fmt.Errorf("generation_id and base_canon_root are required")
	}
	if err := ValidateDependencyFingerprint(dependency); err != nil {
		return err
	}
	if dependency.GenerationID != generationID || dependency.BaseCanonRoot != baseCanonRoot {
		return fmt.Errorf("dependency fingerprint does not match generation/base canon")
	}
	if err := ValidatePlanningAuthority(authority); err != nil {
		return err
	}
	return ValidatePlanningRealization(realization)
}

func validateCausalNodes(nodes []CausalSkeletonNode) error {
	if len(nodes) == 0 {
		return fmt.Errorf("causal skeleton requires at least one node")
	}
	seen := make(map[string]struct{}, len(nodes))
	for i, node := range nodes {
		if strings.TrimSpace(node.ID) == "" || strings.TrimSpace(node.Cause) == "" || strings.TrimSpace(node.Effect) == "" {
			return fmt.Errorf("causal node[%d] requires id, cause, and effect", i)
		}
		if _, exists := seen[node.ID]; exists {
			return fmt.Errorf("duplicate causal node id %q", node.ID)
		}
		seen[node.ID] = struct{}{}
	}
	return nil
}
