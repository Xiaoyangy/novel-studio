package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"sort"
	"strings"
)

const (
	ArcPlanningManifestVersion            = "arc-planning-manifest.v2"
	ChapterAcceptanceReceiptLegacyVersion = "chapter-acceptance-receipt.v2"
	ChapterAcceptanceReceiptVersion       = "chapter-acceptance-receipt.v3-effective-style"
	ArcCompletionReceiptVersion           = "arc-completion-receipt.v2"
)

// ArcChapterBodyRuneContract freezes the exact Unicode-rune delivery range
// that was active when the arc plan was sealed. SourceUserRulesDigest is
// provenance for that immutable snapshot; later edits to meta/user_rules.json
// cannot weaken or silently tighten already-sealed chapter acceptance.
type ArcChapterBodyRuneContract struct {
	MinRunes              int    `json:"min_runes"`
	MaxRunes              int    `json:"max_runes"`
	SourceUserRulesDigest string `json:"source_user_rules_digest"`
}

// ArcChapterPlanningBinding seals the two chapter-level artifacts that must
// exist before an arc can be rendered: the formal projected bundle and the
// independently validated render-capacity contract.
type ArcChapterPlanningBinding struct {
	Chapter        int    `json:"chapter"`
	BundleDigest   string `json:"bundle_digest"`
	CapacityDigest string `json:"capacity_digest"`
}

// ArcCausalLink records one explicit cross-chapter cause/effect transition.
// ID and Cause are the predecessor plan's exact outgoing consequence ID/text;
// Effect is the successor plan's exact consumed_by_cause. Links are
// forward-only planning evidence, never synthesized from generic goals/hooks.
type ArcCausalLink struct {
	ID          string `json:"id"`
	FromChapter int    `json:"from_chapter"`
	ToChapter   int    `json:"to_chapter"`
	Cause       string `json:"cause"`
	Effect      string `json:"effect"`
}

// ArcNarrativeMarker identifies a turn or payoff and the exact chapter that
// must realize it.
type ArcNarrativeMarker struct {
	ID      string `json:"id"`
	Chapter int    `json:"chapter"`
	Summary string `json:"summary"`
}

// ArcCarriedObligation is a deliberately unresolved long-range obligation.
// Its due chapter must be beyond the current arc, preventing an arc-scoped
// planner from silently clamping a book-length promise to the arc boundary.
type ArcCarriedObligation struct {
	ObligationID     string `json:"obligation_id"`
	OriginChapter    int    `json:"origin_chapter"`
	DueChapter       int    `json:"due_chapter"`
	ObligationDigest string `json:"obligation_digest"`
}

// ArcPlanningManifest is the immutable plan boundary for exactly one arc.
// It binds the frozen full-book outline to one generation and an exact,
// contiguous set of chapter bundle/capacity digests.
type ArcPlanningManifest struct {
	Version            string                      `json:"version"`
	ArcID              string                      `json:"arc_id"`
	GenerationID       string                      `json:"generation_id"`
	Volume             int                         `json:"volume"`
	Arc                int                         `json:"arc"`
	FirstChapter       int                         `json:"first_chapter"`
	LastChapter        int                         `json:"last_chapter"`
	BookLastChapter    int                         `json:"book_last_chapter"`
	FullOutlineDigest  string                      `json:"full_outline_digest"`
	ChapterBodyRunes   ArcChapterBodyRuneContract  `json:"chapter_body_runes"`
	Chapters           []ArcChapterPlanningBinding `json:"chapters"`
	CausalLinks        []ArcCausalLink             `json:"causal_links"`
	Turns              []ArcNarrativeMarker        `json:"turns"`
	Payoffs            []ArcNarrativeMarker        `json:"payoffs"`
	CarriedObligations []ArcCarriedObligation      `json:"carried_obligations"`
	CreatedAt          string                      `json:"created_at"`
	ManifestDigest     string                      `json:"manifest_digest"`
}

// ChapterReviewArtifactBinding binds one formal chapter-level review artifact
// to its exact project-relative path and bytes. Arc-level prose reviews are
// intentionally not represented by this protocol.
type ChapterReviewArtifactBinding struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

// ChapterAcceptanceReceipt is the only immutable evidence that a rendered
// chapter body, its exact review artifacts, and its accepted outcome all refer
// to the same arc generation.
type ChapterAcceptanceReceipt struct {
	Version                      string                         `json:"version"`
	ArcID                        string                         `json:"arc_id"`
	ArcManifestDigest            string                         `json:"arc_manifest_digest"`
	GenerationID                 string                         `json:"generation_id"`
	Chapter                      int                            `json:"chapter"`
	ChapterBodySHA256            string                         `json:"chapter_body_sha256"`
	ChapterBodyRunes             int                            `json:"chapter_body_runes"`
	ReviewArtifacts              []ChapterReviewArtifactBinding `json:"review_artifacts"`
	EffectiveStyleReceiptPath    string                         `json:"effective_style_receipt_path,omitempty"`
	EffectiveStyleReceiptDigest  string                         `json:"effective_style_receipt_digest,omitempty"`
	EffectiveStyleArtifactSHA256 string                         `json:"effective_style_artifact_sha256,omitempty"`
	OutcomeReceiptDigest         string                         `json:"outcome_receipt_digest"`
	AcceptedAt                   string                         `json:"accepted_at"`
	ReceiptDigest                string                         `json:"receipt_digest"`
}

// ArcChapterAcceptanceBinding preserves acceptance order in the completion
// receipt. The chapter field is intentionally redundant so missing, duplicate,
// and reordered acceptance receipts fail without consulting mutable state.
type ArcChapterAcceptanceBinding struct {
	Chapter                 int    `json:"chapter"`
	AcceptanceReceiptDigest string `json:"acceptance_receipt_digest"`
}

// ArcCompletionReceipt is the terminal proof for one arc. A next arc may use
// it as authority only after aggregate validation against the exact manifest
// and every immutable chapter acceptance receipt succeeds.
type ArcCompletionReceipt struct {
	Version                   string                        `json:"version"`
	ArcID                     string                        `json:"arc_id"`
	ArcManifestDigest         string                        `json:"arc_manifest_digest"`
	GenerationID              string                        `json:"generation_id"`
	FirstChapter              int                           `json:"first_chapter"`
	LastChapter               int                           `json:"last_chapter"`
	Acceptances               []ArcChapterAcceptanceBinding `json:"acceptances"`
	FinalOutcomeReceiptDigest string                        `json:"final_outcome_receipt_digest"`
	FinalActualPostStateRoot  string                        `json:"final_actual_post_state_root"`
	CarriedObligationRoot     string                        `json:"carried_obligation_root"`
	CompletedAt               string                        `json:"completed_at"`
	ReceiptDigest             string                        `json:"receipt_digest"`
}

func DeriveArcCycleID(volume, arc, firstChapter, lastChapter int) string {
	return fmt.Sprintf("arc:v%03d:a%03d:c%06d-%06d", volume, arc, firstChapter, lastChapter)
}

// ComputeArcChapterBodySHA256 hashes the exact published chapter bytes in the
// same sha256:<hex> form used by planning-v2 outcome receipts.
func ComputeArcChapterBodySHA256(body []byte) string {
	sum := sha256.Sum256(body)
	return PlanningV2DigestPrefix + hex.EncodeToString(sum[:])
}

// ComputeArcArtifactSHA256 hashes exact immutable artifact bytes. It is kept
// separate from JSON semantic hashing because replacing whitespace or field
// order in a reviewed file must invalidate its acceptance receipt.
func ComputeArcArtifactSHA256(raw []byte) string {
	return ComputeArcChapterBodySHA256(raw)
}

func ComputeArcPlanningManifestDigest(manifest ArcPlanningManifest) (string, error) {
	manifest.ManifestDigest = ""
	return planningV2Digest(manifest)
}

func SignArcPlanningManifest(manifest ArcPlanningManifest) (ArcPlanningManifest, error) {
	digest, err := ComputeArcPlanningManifestDigest(manifest)
	if err != nil {
		return manifest, err
	}
	manifest.ManifestDigest = digest
	if err := ValidateArcPlanningManifest(manifest); err != nil {
		return manifest, err
	}
	return manifest, nil
}

func ValidateArcPlanningManifest(manifest ArcPlanningManifest) error {
	const prefix = "arc planning manifest"
	if manifest.Version != ArcPlanningManifestVersion {
		return fmt.Errorf("%s: unsupported version %q", prefix, manifest.Version)
	}
	if manifest.Volume <= 0 || manifest.Arc <= 0 {
		return fmt.Errorf("%s: volume and arc must be > 0", prefix)
	}
	if manifest.FirstChapter <= 0 || manifest.LastChapter < manifest.FirstChapter ||
		manifest.BookLastChapter < manifest.LastChapter {
		return fmt.Errorf("%s: invalid chapter range %d..%d/book=%d", prefix, manifest.FirstChapter, manifest.LastChapter, manifest.BookLastChapter)
	}
	wantArcID := DeriveArcCycleID(manifest.Volume, manifest.Arc, manifest.FirstChapter, manifest.LastChapter)
	if manifest.ArcID != wantArcID {
		return fmt.Errorf("%s: arc_id mismatch: got %q want %q", prefix, manifest.ArcID, wantArcID)
	}
	if !strings.HasPrefix(manifest.GenerationID, PlanningGenerationIDPrefix) || strings.TrimSpace(manifest.GenerationID) != manifest.GenerationID {
		return fmt.Errorf("%s: invalid generation_id", prefix)
	}
	for name, digest := range map[string]string{
		"full_outline_digest":      manifest.FullOutlineDigest,
		"source_user_rules_digest": manifest.ChapterBodyRunes.SourceUserRulesDigest,
		"manifest_digest":          manifest.ManifestDigest,
	} {
		if err := validatePlanningV2Digest(name, digest); err != nil {
			return fmt.Errorf("%s: %w", prefix, err)
		}
	}
	if err := validatePlanningV2Time("created_at", manifest.CreatedAt); err != nil {
		return fmt.Errorf("%s: %w", prefix, err)
	}
	if err := ValidateArcChapterBodyRuneContract(manifest.ChapterBodyRunes); err != nil {
		return fmt.Errorf("%s: %w", prefix, err)
	}

	wantChapterCount := manifest.LastChapter - manifest.FirstChapter + 1
	if len(manifest.Chapters) != wantChapterCount {
		return fmt.Errorf("%s: missing chapter planning bindings: got %d want %d", prefix, len(manifest.Chapters), wantChapterCount)
	}
	for i, binding := range manifest.Chapters {
		wantChapter := manifest.FirstChapter + i
		if binding.Chapter != wantChapter {
			return fmt.Errorf("%s: chapter bindings are out of order or incomplete at index %d: got %d want %d", prefix, i, binding.Chapter, wantChapter)
		}
		for name, digest := range map[string]string{
			"bundle_digest":   binding.BundleDigest,
			"capacity_digest": binding.CapacityDigest,
		} {
			if err := validatePlanningV2Digest(name, digest); err != nil {
				return fmt.Errorf("%s: chapter %d: %w", prefix, binding.Chapter, err)
			}
		}
	}

	if err := validateArcCausalLinks(manifest); err != nil {
		return fmt.Errorf("%s: %w", prefix, err)
	}
	if err := validateArcNarrativeMarkers("turns", manifest.Turns, manifest.FirstChapter, manifest.LastChapter); err != nil {
		return fmt.Errorf("%s: %w", prefix, err)
	}
	if err := validateArcNarrativeMarkers("payoffs", manifest.Payoffs, manifest.FirstChapter, manifest.LastChapter); err != nil {
		return fmt.Errorf("%s: %w", prefix, err)
	}
	if err := validateArcCarriedObligations(manifest); err != nil {
		return fmt.Errorf("%s: %w", prefix, err)
	}

	want, err := ComputeArcPlanningManifestDigest(manifest)
	if err != nil {
		return err
	}
	if manifest.ManifestDigest != want {
		return fmt.Errorf("%s: manifest_digest mismatch", prefix)
	}
	return nil
}

func validateArcCausalLinks(manifest ArcPlanningManifest) error {
	if manifest.LastChapter > manifest.FirstChapter && len(manifest.CausalLinks) == 0 {
		return fmt.Errorf("causal_links must connect a multi-chapter arc")
	}
	seen := make(map[string]struct{}, len(manifest.CausalLinks))
	incoming := make(map[int]bool, manifest.LastChapter-manifest.FirstChapter)
	lastFrom, lastTo, lastID := 0, 0, ""
	for i, link := range manifest.CausalLinks {
		if strings.TrimSpace(link.ID) == "" || strings.TrimSpace(link.ID) != link.ID {
			return fmt.Errorf("causal_links[%d].id is required and must be trimmed", i)
		}
		if _, duplicate := seen[link.ID]; duplicate {
			return fmt.Errorf("causal_links[%d].id %q is duplicated", i, link.ID)
		}
		seen[link.ID] = struct{}{}
		if link.FromChapter < manifest.FirstChapter || link.FromChapter >= link.ToChapter || link.ToChapter > manifest.LastChapter {
			return fmt.Errorf("causal_links[%d] has invalid forward chapter edge %d->%d", i, link.FromChapter, link.ToChapter)
		}
		if strings.TrimSpace(link.Cause) == "" || strings.TrimSpace(link.Effect) == "" {
			return fmt.Errorf("causal_links[%d] requires cause and effect", i)
		}
		if i > 0 && (link.ToChapter < lastTo ||
			(link.ToChapter == lastTo && link.FromChapter < lastFrom) ||
			(link.ToChapter == lastTo && link.FromChapter == lastFrom && link.ID <= lastID)) {
			return fmt.Errorf("causal_links must be ordered by to_chapter, from_chapter, id")
		}
		lastFrom, lastTo, lastID = link.FromChapter, link.ToChapter, link.ID
		incoming[link.ToChapter] = true
	}
	for chapter := manifest.FirstChapter + 1; chapter <= manifest.LastChapter; chapter++ {
		if !incoming[chapter] {
			return fmt.Errorf("causal_links are missing an incoming cross-chapter cause for chapter %d", chapter)
		}
	}
	return nil
}

func validateArcNarrativeMarkers(name string, markers []ArcNarrativeMarker, firstChapter, lastChapter int) error {
	if len(markers) == 0 {
		return fmt.Errorf("%s must contain at least one marker", name)
	}
	seen := make(map[string]struct{}, len(markers))
	lastMarkerChapter, lastID := 0, ""
	for i, marker := range markers {
		if strings.TrimSpace(marker.ID) == "" || strings.TrimSpace(marker.ID) != marker.ID {
			return fmt.Errorf("%s[%d].id is required and must be trimmed", name, i)
		}
		if _, duplicate := seen[marker.ID]; duplicate {
			return fmt.Errorf("%s[%d].id %q is duplicated", name, i, marker.ID)
		}
		seen[marker.ID] = struct{}{}
		if marker.Chapter < firstChapter || marker.Chapter > lastChapter {
			return fmt.Errorf("%s[%d].chapter=%d is outside arc %d..%d", name, i, marker.Chapter, firstChapter, lastChapter)
		}
		if strings.TrimSpace(marker.Summary) == "" {
			return fmt.Errorf("%s[%d].summary is required", name, i)
		}
		if i > 0 && (marker.Chapter < lastMarkerChapter ||
			(marker.Chapter == lastMarkerChapter && marker.ID <= lastID)) {
			return fmt.Errorf("%s must be ordered by chapter, id", name)
		}
		lastMarkerChapter, lastID = marker.Chapter, marker.ID
	}
	return nil
}

func validateArcCarriedObligations(manifest ArcPlanningManifest) error {
	if manifest.LastChapter == manifest.BookLastChapter && len(manifest.CarriedObligations) != 0 {
		return fmt.Errorf("the final book arc cannot carry unresolved obligations")
	}
	seen := make(map[string]struct{}, len(manifest.CarriedObligations))
	lastID := ""
	for i, obligation := range manifest.CarriedObligations {
		if !strings.HasPrefix(obligation.ObligationID, "obl:") || strings.TrimSpace(obligation.ObligationID) != obligation.ObligationID {
			return fmt.Errorf("carried_obligations[%d] has invalid obligation_id", i)
		}
		if _, duplicate := seen[obligation.ObligationID]; duplicate {
			return fmt.Errorf("carried_obligations[%d].obligation_id %q is duplicated", i, obligation.ObligationID)
		}
		seen[obligation.ObligationID] = struct{}{}
		if i > 0 && obligation.ObligationID <= lastID {
			return fmt.Errorf("carried_obligations must be ordered by obligation_id")
		}
		lastID = obligation.ObligationID
		if obligation.OriginChapter <= 0 || obligation.OriginChapter > manifest.LastChapter {
			return fmt.Errorf("carried_obligations[%d].origin_chapter=%d is invalid", i, obligation.OriginChapter)
		}
		if obligation.DueChapter <= manifest.LastChapter || obligation.DueChapter > manifest.BookLastChapter {
			return fmt.Errorf("carried_obligations[%d].due_chapter=%d must be after the arc and inside the book", i, obligation.DueChapter)
		}
		if err := validatePlanningV2Digest("obligation_digest", obligation.ObligationDigest); err != nil {
			return fmt.Errorf("carried_obligations[%d]: %w", i, err)
		}
	}
	return nil
}

func ComputeArcCarriedObligationRoot(obligations []ArcCarriedObligation) (string, error) {
	return planningV2Digest(struct {
		Version     string                 `json:"version"`
		Obligations []ArcCarriedObligation `json:"obligations"`
	}{
		Version:     "arc-carried-obligations.v1",
		Obligations: append([]ArcCarriedObligation(nil), obligations...),
	})
}

func ComputeChapterAcceptanceReceiptDigest(receipt ChapterAcceptanceReceipt) (string, error) {
	receipt.ReceiptDigest = ""
	receipt.ReviewArtifacts = CanonicalChapterReviewArtifacts(receipt.ReviewArtifacts)
	return planningV2Digest(receipt)
}

func SignChapterAcceptanceReceipt(receipt ChapterAcceptanceReceipt) (ChapterAcceptanceReceipt, error) {
	receipt.ReviewArtifacts = CanonicalChapterReviewArtifacts(receipt.ReviewArtifacts)
	digest, err := ComputeChapterAcceptanceReceiptDigest(receipt)
	if err != nil {
		return receipt, err
	}
	receipt.ReceiptDigest = digest
	if err := ValidateChapterAcceptanceReceipt(receipt); err != nil {
		return receipt, err
	}
	return receipt, nil
}

func ValidateChapterAcceptanceReceipt(receipt ChapterAcceptanceReceipt) error {
	const prefix = "chapter acceptance receipt"
	if receipt.Version != ChapterAcceptanceReceiptVersion &&
		receipt.Version != ChapterAcceptanceReceiptLegacyVersion {
		return fmt.Errorf("%s: unsupported version %q", prefix, receipt.Version)
	}
	if strings.TrimSpace(receipt.ArcID) == "" || strings.TrimSpace(receipt.ArcID) != receipt.ArcID {
		return fmt.Errorf("%s: invalid arc_id", prefix)
	}
	if !strings.HasPrefix(receipt.GenerationID, PlanningGenerationIDPrefix) || strings.TrimSpace(receipt.GenerationID) != receipt.GenerationID || receipt.Chapter <= 0 {
		return fmt.Errorf("%s: invalid generation_id or chapter", prefix)
	}
	if receipt.ChapterBodyRunes <= 0 {
		return fmt.Errorf("%s: chapter_body_runes must be > 0", prefix)
	}
	for name, digest := range map[string]string{
		"arc_manifest_digest":    receipt.ArcManifestDigest,
		"chapter_body_sha256":    receipt.ChapterBodySHA256,
		"outcome_receipt_digest": receipt.OutcomeReceiptDigest,
		"receipt_digest":         receipt.ReceiptDigest,
	} {
		if err := validatePlanningV2Digest(name, digest); err != nil {
			return fmt.Errorf("%s: %w", prefix, err)
		}
	}
	if len(receipt.ReviewArtifacts) == 0 {
		return fmt.Errorf("%s: review_artifacts must not be empty", prefix)
	}
	canonicalReviews := CanonicalChapterReviewArtifacts(receipt.ReviewArtifacts)
	seenPaths := make(map[string]struct{}, len(receipt.ReviewArtifacts))
	for i, artifact := range receipt.ReviewArtifacts {
		if artifact != canonicalReviews[i] {
			return fmt.Errorf("%s: review_artifacts must be sorted by path and digest", prefix)
		}
		if _, duplicate := seenPaths[artifact.Path]; duplicate {
			return fmt.Errorf("%s: review artifact path %q is duplicated", prefix, artifact.Path)
		}
		seenPaths[artifact.Path] = struct{}{}
		if err := ValidateChapterReviewArtifactPath(artifact.Path, receipt.Chapter); err != nil {
			return fmt.Errorf("%s: review_artifacts[%d]: %w", prefix, i, err)
		}
		if err := validatePlanningV2Digest(fmt.Sprintf("review_artifacts[%d].digest", i), artifact.Digest); err != nil {
			return fmt.Errorf("%s: %w", prefix, err)
		}
	}
	styleEvidenceCount := 0
	for _, value := range []string{
		receipt.EffectiveStyleReceiptPath,
		receipt.EffectiveStyleReceiptDigest,
		receipt.EffectiveStyleArtifactSHA256,
	} {
		if strings.TrimSpace(value) != "" {
			styleEvidenceCount++
		}
	}
	if styleEvidenceCount != 0 && styleEvidenceCount != 3 {
		return fmt.Errorf("%s: effective style archive evidence must be all present or all absent", prefix)
	}
	if receipt.Version == ChapterAcceptanceReceiptVersion && styleEvidenceCount != 3 {
		return fmt.Errorf("%s: v3 requires complete effective style archive evidence", prefix)
	}
	if receipt.Version == ChapterAcceptanceReceiptLegacyVersion && styleEvidenceCount != 0 {
		return fmt.Errorf("%s: legacy v2 cannot carry v3 effective style archive evidence", prefix)
	}
	if receipt.Version == ChapterAcceptanceReceiptVersion {
		required := requiredChapterAcceptanceV3ReviewArtifactPaths(receipt.Chapter)
		if len(receipt.ReviewArtifacts) != len(required) {
			return fmt.Errorf("%s: v3 review_artifacts must bind the complete formal review set", prefix)
		}
		for i, artifact := range receipt.ReviewArtifacts {
			if artifact.Path != required[i] {
				return fmt.Errorf("%s: v3 review_artifacts must bind the complete formal review set", prefix)
			}
		}
	}
	if styleEvidenceCount == 3 {
		clean := path.Clean(receipt.EffectiveStyleReceiptPath)
		prefixPath := fmt.Sprintf("meta/planning/effective_render_style_contracts/ch%04d/", receipt.Chapter)
		if clean != receipt.EffectiveStyleReceiptPath || path.IsAbs(clean) ||
			strings.HasPrefix(clean, "../") || !strings.HasPrefix(clean, prefixPath) ||
			!strings.HasSuffix(clean, ".json") {
			return fmt.Errorf("%s: effective_style_receipt_path is unsafe or belongs to another chapter", prefix)
		}
		if err := validatePlanningV2Digest("effective_style_receipt_digest", receipt.EffectiveStyleReceiptDigest); err != nil {
			return fmt.Errorf("%s: %w", prefix, err)
		}
		if err := validatePlanningV2Digest("effective_style_artifact_sha256", receipt.EffectiveStyleArtifactSHA256); err != nil {
			return fmt.Errorf("%s: %w", prefix, err)
		}
	}
	if err := validatePlanningV2Time("accepted_at", receipt.AcceptedAt); err != nil {
		return fmt.Errorf("%s: %w", prefix, err)
	}
	want, err := ComputeChapterAcceptanceReceiptDigest(receipt)
	if err != nil {
		return err
	}
	if receipt.ReceiptDigest != want {
		return fmt.Errorf("%s: receipt_digest mismatch; chapter body or review evidence may have drifted", prefix)
	}
	return nil
}

func requiredChapterAcceptanceV3ReviewArtifactPaths(chapter int) []string {
	return []string{
		fmt.Sprintf("reviews/%02d.json", chapter),
		fmt.Sprintf("reviews/%02d.md", chapter),
		fmt.Sprintf("reviews/%02d_ai_gate.json", chapter),
		fmt.Sprintf("reviews/%02d_ai_voice_redflags.json", chapter),
		fmt.Sprintf("reviews/%02d_deepseek_ai_judge.json", chapter),
		fmt.Sprintf("reviews/%02d_model_provenance.json", chapter),
	}
}

func ValidateChapterAcceptanceReceiptAgainstManifest(receipt ChapterAcceptanceReceipt, manifest ArcPlanningManifest) error {
	if err := ValidateArcPlanningManifest(manifest); err != nil {
		return err
	}
	if err := ValidateChapterAcceptanceReceipt(receipt); err != nil {
		return err
	}
	if receipt.ArcID != manifest.ArcID ||
		receipt.ArcManifestDigest != manifest.ManifestDigest ||
		receipt.GenerationID != manifest.GenerationID ||
		receipt.Chapter < manifest.FirstChapter || receipt.Chapter > manifest.LastChapter {
		return fmt.Errorf("chapter acceptance receipt: receipt does not bind the exact arc manifest/generation/range")
	}
	if err := ValidateAcceptedChapterBodyRunes(receipt.Chapter, receipt.ChapterBodyRunes, manifest.ChapterBodyRunes); err != nil {
		return fmt.Errorf("chapter acceptance receipt: %w", err)
	}
	return nil
}

// ValidateArcChapterBodyRuneContract validates the immutable delivery range
// sealed into an arc manifest. It intentionally does not read current mutable
// user rules.
func ValidateArcChapterBodyRuneContract(contract ArcChapterBodyRuneContract) error {
	if contract.MinRunes <= 0 || contract.MaxRunes < contract.MinRunes {
		return fmt.Errorf(
			"chapter_body_runes requires a positive ordered range, got %d-%d",
			contract.MinRunes,
			contract.MaxRunes,
		)
	}
	if err := validatePlanningV2Digest("chapter_body_runes.source_user_rules_digest", contract.SourceUserRulesDigest); err != nil {
		return err
	}
	return nil
}

// ValidateAcceptedChapterBodyRunes checks one independently measured body
// against the manifest-bound range.
func ValidateAcceptedChapterBodyRunes(chapter, actual int, contract ArcChapterBodyRuneContract) error {
	if err := ValidateArcChapterBodyRuneContract(contract); err != nil {
		return err
	}
	if actual < contract.MinRunes || actual > contract.MaxRunes {
		return fmt.Errorf(
			"chapter %d exact body rune count=%d is outside sealed range=%d-%d",
			chapter,
			actual,
			contract.MinRunes,
			contract.MaxRunes,
		)
	}
	return nil
}

func ComputeArcCompletionReceiptDigest(receipt ArcCompletionReceipt) (string, error) {
	receipt.ReceiptDigest = ""
	return planningV2Digest(receipt)
}

func SignArcCompletionReceipt(receipt ArcCompletionReceipt) (ArcCompletionReceipt, error) {
	digest, err := ComputeArcCompletionReceiptDigest(receipt)
	if err != nil {
		return receipt, err
	}
	receipt.ReceiptDigest = digest
	if err := ValidateArcCompletionReceipt(receipt); err != nil {
		return receipt, err
	}
	return receipt, nil
}

func ValidateArcCompletionReceipt(receipt ArcCompletionReceipt) error {
	const prefix = "arc completion receipt"
	if receipt.Version != ArcCompletionReceiptVersion {
		return fmt.Errorf("%s: unsupported version %q", prefix, receipt.Version)
	}
	if strings.TrimSpace(receipt.ArcID) == "" || strings.TrimSpace(receipt.ArcID) != receipt.ArcID ||
		!strings.HasPrefix(receipt.GenerationID, PlanningGenerationIDPrefix) || strings.TrimSpace(receipt.GenerationID) != receipt.GenerationID {
		return fmt.Errorf("%s: invalid arc_id or generation_id", prefix)
	}
	if receipt.FirstChapter <= 0 || receipt.LastChapter < receipt.FirstChapter {
		return fmt.Errorf("%s: invalid chapter range", prefix)
	}
	for name, digest := range map[string]string{
		"arc_manifest_digest":          receipt.ArcManifestDigest,
		"final_outcome_receipt_digest": receipt.FinalOutcomeReceiptDigest,
		"final_actual_post_state_root": receipt.FinalActualPostStateRoot,
		"carried_obligation_root":      receipt.CarriedObligationRoot,
		"receipt_digest":               receipt.ReceiptDigest,
	} {
		if err := validatePlanningV2Digest(name, digest); err != nil {
			return fmt.Errorf("%s: %w", prefix, err)
		}
	}
	wantCount := receipt.LastChapter - receipt.FirstChapter + 1
	if len(receipt.Acceptances) != wantCount {
		return fmt.Errorf("%s: missing chapter acceptances: got %d want %d", prefix, len(receipt.Acceptances), wantCount)
	}
	for i, binding := range receipt.Acceptances {
		wantChapter := receipt.FirstChapter + i
		if binding.Chapter != wantChapter {
			return fmt.Errorf("%s: acceptances are out of order or incomplete at index %d: got %d want %d", prefix, i, binding.Chapter, wantChapter)
		}
		if err := validatePlanningV2Digest("acceptance_receipt_digest", binding.AcceptanceReceiptDigest); err != nil {
			return fmt.Errorf("%s: chapter %d: %w", prefix, binding.Chapter, err)
		}
	}
	if err := validatePlanningV2Time("completed_at", receipt.CompletedAt); err != nil {
		return fmt.Errorf("%s: %w", prefix, err)
	}
	want, err := ComputeArcCompletionReceiptDigest(receipt)
	if err != nil {
		return err
	}
	if receipt.ReceiptDigest != want {
		return fmt.Errorf("%s: receipt_digest mismatch", prefix)
	}
	return nil
}

// ValidateArcCompletionReceiptAgainstManifest verifies the complete immutable
// proof chain. The acceptance slice is order-sensitive by design: callers may
// not sort away evidence that chapters were accepted out of sequence.
func ValidateArcCompletionReceiptAgainstManifest(
	receipt ArcCompletionReceipt,
	manifest ArcPlanningManifest,
	acceptances []ChapterAcceptanceReceipt,
) error {
	if err := ValidateArcPlanningManifest(manifest); err != nil {
		return err
	}
	if err := ValidateArcCompletionReceipt(receipt); err != nil {
		return err
	}
	if receipt.ArcID != manifest.ArcID ||
		receipt.ArcManifestDigest != manifest.ManifestDigest ||
		receipt.GenerationID != manifest.GenerationID ||
		receipt.FirstChapter != manifest.FirstChapter ||
		receipt.LastChapter != manifest.LastChapter {
		return fmt.Errorf("arc completion receipt: receipt does not bind the exact arc manifest/generation/range")
	}
	wantCount := manifest.LastChapter - manifest.FirstChapter + 1
	if len(acceptances) != wantCount {
		return fmt.Errorf("arc completion receipt: missing chapter acceptance receipts: got %d want %d", len(acceptances), wantCount)
	}
	for i, acceptance := range acceptances {
		wantChapter := manifest.FirstChapter + i
		if acceptance.Chapter != wantChapter {
			return fmt.Errorf("arc completion receipt: acceptance inputs are out of order at index %d: got chapter %d want %d", i, acceptance.Chapter, wantChapter)
		}
		if err := ValidateChapterAcceptanceReceiptAgainstManifest(acceptance, manifest); err != nil {
			return fmt.Errorf("arc completion receipt: chapter %d: %w", acceptance.Chapter, err)
		}
		binding := receipt.Acceptances[i]
		if binding.Chapter != acceptance.Chapter || binding.AcceptanceReceiptDigest != acceptance.ReceiptDigest {
			return fmt.Errorf("arc completion receipt: chapter %d acceptance digest mismatch", wantChapter)
		}
	}
	lastAcceptance := acceptances[len(acceptances)-1]
	if receipt.FinalOutcomeReceiptDigest != lastAcceptance.OutcomeReceiptDigest {
		return fmt.Errorf("arc completion receipt: final outcome digest does not match the last chapter acceptance")
	}
	wantCarriedRoot, err := ComputeArcCarriedObligationRoot(manifest.CarriedObligations)
	if err != nil {
		return err
	}
	if receipt.CarriedObligationRoot != wantCarriedRoot {
		return fmt.Errorf("arc completion receipt: carried obligation root does not match the sealed arc manifest")
	}
	return nil
}

// NewArcCompletionReceipt builds the aggregate receipt in exact chapter order.
// It does not invent the actual post-state root; callers must supply the root
// already verified against the final ActualOutcomeReceipt.
func NewArcCompletionReceipt(
	manifest ArcPlanningManifest,
	acceptances []ChapterAcceptanceReceipt,
	finalActualPostStateRoot string,
	completedAt string,
) (ArcCompletionReceipt, error) {
	bindings := make([]ArcChapterAcceptanceBinding, len(acceptances))
	for i, acceptance := range acceptances {
		bindings[i] = ArcChapterAcceptanceBinding{
			Chapter:                 acceptance.Chapter,
			AcceptanceReceiptDigest: acceptance.ReceiptDigest,
		}
	}
	carriedRoot, err := ComputeArcCarriedObligationRoot(manifest.CarriedObligations)
	if err != nil {
		return ArcCompletionReceipt{}, err
	}
	finalOutcome := ""
	if len(acceptances) > 0 {
		finalOutcome = acceptances[len(acceptances)-1].OutcomeReceiptDigest
	}
	receipt := ArcCompletionReceipt{
		Version:                   ArcCompletionReceiptVersion,
		ArcID:                     manifest.ArcID,
		ArcManifestDigest:         manifest.ManifestDigest,
		GenerationID:              manifest.GenerationID,
		FirstChapter:              manifest.FirstChapter,
		LastChapter:               manifest.LastChapter,
		Acceptances:               bindings,
		FinalOutcomeReceiptDigest: finalOutcome,
		FinalActualPostStateRoot:  finalActualPostStateRoot,
		CarriedObligationRoot:     carriedRoot,
		CompletedAt:               completedAt,
	}
	receipt, err = SignArcCompletionReceipt(receipt)
	if err != nil {
		return receipt, err
	}
	if err := ValidateArcCompletionReceiptAgainstManifest(receipt, manifest, acceptances); err != nil {
		return receipt, err
	}
	return receipt, nil
}

// ValidateChapterReviewArtifactPath confines acceptance evidence to formal,
// chapter-specific review outputs. In particular, reviews/drafts and
// NN-arc/NN-global reviews cannot be smuggled in as chapter acceptance.
func ValidateChapterReviewArtifactPath(artifactPath string, chapter int) error {
	if chapter <= 0 || artifactPath == "" || strings.TrimSpace(artifactPath) != artifactPath || strings.Contains(artifactPath, `\`) {
		return fmt.Errorf("invalid chapter review artifact path %q", artifactPath)
	}
	if path.Clean(artifactPath) != artifactPath || path.IsAbs(artifactPath) || path.Dir(artifactPath) != "reviews" {
		return fmt.Errorf("review artifact path %q must be a direct project-relative file under reviews/", artifactPath)
	}
	base := path.Base(artifactPath)
	prefix := fmt.Sprintf("%02d", chapter)
	if !strings.HasPrefix(base, prefix+".") && !strings.HasPrefix(base, prefix+"_") {
		return fmt.Errorf("review artifact path %q is not chapter %d evidence", artifactPath, chapter)
	}
	switch path.Ext(base) {
	case ".json", ".md":
		return nil
	default:
		return fmt.Errorf("review artifact path %q must be JSON or Markdown", artifactPath)
	}
}

// CanonicalChapterReviewArtifacts is exposed for callers constructing one
// receipt from the independently written chapter review artifacts.
func CanonicalChapterReviewArtifacts(bindings []ChapterReviewArtifactBinding) []ChapterReviewArtifactBinding {
	result := append([]ChapterReviewArtifactBinding(nil), bindings...)
	for i := range result {
		result[i].Path = strings.TrimSpace(result[i].Path)
		result[i].Digest = strings.TrimSpace(result[i].Digest)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Path != result[j].Path {
			return result[i].Path < result[j].Path
		}
		return result[i].Digest < result[j].Digest
	})
	if result == nil {
		return []ChapterReviewArtifactBinding{}
	}
	return result
}
