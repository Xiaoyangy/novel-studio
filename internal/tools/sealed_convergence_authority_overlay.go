package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/store"
)

const (
	// SealedConvergenceAuthorityOverlayPath is written by the narrow
	// convergence-replan controller before it starts a Planner.  It does not
	// replace or refresh the simulation receipt.  Instead it proves that the
	// already-signed simulation is the exact object in the immutable projected
	// bundle currently promoted for this chapter.
	SealedConvergenceAuthorityOverlayPath    = "meta/planning/current_convergence_authority_overlay.json"
	sealedConvergenceReplanFeedbackPath      = "meta/planning/current_convergence_replan_feedback.json"
	sealedConvergenceAuthorityOverlayVersion = "sealed-convergence-authority-overlay.v1"
)

var sealedConvergenceExecutionOwnerPattern = regexp.MustCompile(
	`^pipeline-convergence-replan-ch([0-9]{6})-pid([1-9][0-9]*)-([1-9][0-9]*)$`,
)

// SealedConvergenceAuthorityOverlay is deliberately identity-only.  It cannot
// carry character decisions, prose, canon deltas or a replacement simulation.
// The live validator reloads the immutable bundle and compares the complete
// simulation object before accepting it.
type SealedConvergenceAuthorityOverlay struct {
	Version                   string `json:"version"`
	GenerationID              string `json:"generation_id"`
	Chapter                   int    `json:"chapter"`
	PlanningContextDigest     string `json:"planning_context_digest"`
	ProjectedPreStateRoot     string `json:"projected_pre_state_root"`
	BundleDigest              string `json:"bundle_digest"`
	PromotionReceiptDigest    string `json:"promotion_receipt_digest"`
	SimulationID              string `json:"simulation_id"`
	SimulationDigest          string `json:"simulation_digest"`
	AuthorityReceiptDigest    string `json:"authority_receipt_digest"`
	ImmutableStateContractSHA string `json:"immutable_state_contract_sha256"`
	CreatedAt                 string `json:"created_at"`
	OverlayDigest             string `json:"overlay_digest"`
}

// NewSealedConvergenceAuthorityOverlay builds the content-addressed proof from
// an already validated projected bundle.  Callers still persist it atomically.
func NewSealedConvergenceAuthorityOverlay(
	bundle domain.ProjectedChapterBundle,
	promotionReceiptDigest string,
	immutableStateContractSHA string,
	createdAt string,
) (SealedConvergenceAuthorityOverlay, error) {
	var out SealedConvergenceAuthorityOverlay
	simulation := bundle.ChapterWorldSimulation
	if simulation.AuthorityReceipt == nil {
		return out, fmt.Errorf("sealed convergence authority overlay requires a signed simulation")
	}
	simulationDigest, err := sealedConvergencePlanningDigest(simulation)
	if err != nil {
		return out, fmt.Errorf("sealed convergence authority overlay simulation digest: %w", err)
	}
	out = SealedConvergenceAuthorityOverlay{
		Version:                   sealedConvergenceAuthorityOverlayVersion,
		GenerationID:              bundle.GenerationID,
		Chapter:                   bundle.Chapter,
		PlanningContextDigest:     bundle.PlanningContextDigest,
		ProjectedPreStateRoot:     bundle.ProjectedPreStateRoot,
		BundleDigest:              bundle.BundleDigest,
		PromotionReceiptDigest:    strings.TrimSpace(promotionReceiptDigest),
		SimulationID:              simulation.SimulationID,
		SimulationDigest:          simulationDigest,
		AuthorityReceiptDigest:    simulation.AuthorityReceipt.ReceiptDigest,
		ImmutableStateContractSHA: strings.TrimSpace(immutableStateContractSHA),
		CreatedAt:                 strings.TrimSpace(createdAt),
	}
	out.OverlayDigest, err = computeSealedConvergenceAuthorityOverlayDigest(out)
	if err != nil {
		return SealedConvergenceAuthorityOverlay{}, err
	}
	if err := validateSealedConvergenceAuthorityOverlaySchema(out); err != nil {
		return SealedConvergenceAuthorityOverlay{}, err
	}
	return out, nil
}

func computeSealedConvergenceAuthorityOverlayDigest(
	overlay SealedConvergenceAuthorityOverlay,
) (string, error) {
	overlay.OverlayDigest = ""
	return sealedConvergencePlanningDigest(overlay)
}

func sealedConvergencePlanningDigest(value any) (string, error) {
	digest, err := domain.DeterministicPlanningHash(value)
	if err != nil {
		return "", err
	}
	return domain.PlanningV2DigestPrefix + strings.TrimPrefix(digest, domain.PlanningV2DigestPrefix), nil
}

func validateSealedConvergenceAuthorityOverlaySchema(
	overlay SealedConvergenceAuthorityOverlay,
) error {
	if overlay.Version != sealedConvergenceAuthorityOverlayVersion ||
		strings.TrimSpace(overlay.GenerationID) == "" || overlay.Chapter <= 0 ||
		strings.TrimSpace(overlay.SimulationID) == "" ||
		strings.TrimSpace(overlay.CreatedAt) == "" {
		return fmt.Errorf("sealed convergence authority overlay identity is incomplete")
	}
	for _, digest := range []string{
		overlay.PlanningContextDigest,
		overlay.ProjectedPreStateRoot,
		overlay.BundleDigest,
		overlay.PromotionReceiptDigest,
		overlay.SimulationDigest,
		overlay.AuthorityReceiptDigest,
		overlay.ImmutableStateContractSHA,
		overlay.OverlayDigest,
	} {
		if !validSimulationAuthorityDigest(digest) {
			return fmt.Errorf("sealed convergence authority overlay contains an invalid digest")
		}
	}
	want, err := computeSealedConvergenceAuthorityOverlayDigest(overlay)
	if err != nil || want != overlay.OverlayDigest {
		return fmt.Errorf("sealed convergence authority overlay digest mismatch")
	}
	return nil
}

func loadSealedConvergenceAuthorityOverlay(
	st *store.Store,
) (*SealedConvergenceAuthorityOverlay, error) {
	if st == nil {
		return nil, fmt.Errorf("sealed convergence authority overlay store is nil")
	}
	raw, err := os.ReadFile(filepath.Join(st.Dir(), filepath.FromSlash(SealedConvergenceAuthorityOverlayPath)))
	if err != nil {
		return nil, err
	}
	var overlay SealedConvergenceAuthorityOverlay
	if err := json.Unmarshal(raw, &overlay); err != nil {
		return nil, err
	}
	if err := validateSealedConvergenceAuthorityOverlaySchema(overlay); err != nil {
		return nil, err
	}
	return &overlay, nil
}

func convergenceReplanExecutionLock(lock *domain.PipelineExecutionLock) bool {
	if lock == nil || lock.Mode != domain.PipelineExecutionProjectAll ||
		lock.TargetChapter <= 0 || lock.ProcessID <= 0 {
		return false
	}
	match := sealedConvergenceExecutionOwnerPattern.FindStringSubmatch(strings.TrimSpace(lock.Owner))
	if len(match) != 4 {
		return false
	}
	chapter, chapterErr := strconv.Atoi(match[1])
	processID, processErr := strconv.Atoi(match[2])
	nonce, nonceErr := strconv.ParseInt(match[3], 10, 64)
	return chapterErr == nil && processErr == nil && nonceErr == nil &&
		chapter == lock.TargetChapter && processID == lock.ProcessID && nonce > 0
}

// ensurePlanDetailsProjectAllStateSource preserves the ordinary project-all
// attestation boundary while closing one control-plane gap in a sealed
// convergence replan. The host has already served and signed the exact
// planning context, but a Planner can faithfully return the opaque
// context-access token while overlooking the non-secret project-all-state
// source token in the same packet. Only an exact convergence execution owner
// whose immutable simulation, sealed bundle and promotion all match the
// content-addressed authority overlay may receive that state token from the
// server. The opaque planning access token is intentionally never synthesized
// here and remains subject to the normal single-use receipt consumer.
func ensurePlanDetailsProjectAllStateSource(
	st *store.Store,
	chapter int,
	merged map[string]any,
	simulation *domain.ChapterWorldSimulation,
	projectAllStateSourceToken string,
) error {
	projectAllStateSourceToken = strings.TrimSpace(projectAllStateSourceToken)
	if projectAllStateSourceToken == "" {
		return nil
	}
	if merged == nil {
		return fmt.Errorf(
			"plan_details 缺少 causal_simulation，无法绑定 exact project-all-state authoritative source token: %w",
			errs.ErrToolPrecondition,
		)
	}
	contextSources := stringSliceFromAny(merged["context_sources"])
	if projectAllStateSourcesContain(contextSources, projectAllStateSourceToken) {
		return nil
	}

	lock, err := st.Runtime.LoadPipelineExecution()
	if err != nil {
		return fmt.Errorf("plan_details 读取 project-all execution identity: %w", err)
	}
	if !convergenceReplanExecutionLock(lock) {
		return fmt.Errorf(
			"plan_details 缺少 novel_context 返回的 exact project-all-state authoritative source token；普通 project-all 必须由 Planner 原样提交，服务端不会代签: %w",
			errs.ErrToolPrecondition,
		)
	}
	if err := requireCurrentPipelineExecutionProcess(lock, "sealed convergence plan_details state binding"); err != nil {
		return err
	}
	if lock.TargetChapter != chapter || simulation == nil || simulation.Chapter != chapter {
		return fmt.Errorf(
			"sealed convergence authority overlay cannot authorize plan_details project-all-state injection for chapter %d: %w",
			chapter,
			errs.ErrToolPrecondition,
		)
	}
	if err := validateSealedConvergenceSimulationAuthorityOverlay(st, *simulation); err != nil {
		return fmt.Errorf(
			"sealed convergence authority overlay rejected plan_details project-all-state injection: %v: %w",
			err,
			errs.ErrToolPrecondition,
		)
	}
	merged["context_sources"] = appendUniqueString(
		contextSources,
		projectAllStateSourceToken,
	)
	return nil
}

func (t *ContextTool) attachSealedConvergencePlanningContext(
	result map[string]any,
	chapter int,
	profile string,
) error {
	if t == nil || t.store == nil || result == nil || chapter <= 0 ||
		strings.TrimSpace(profile) != "planning" {
		return nil
	}
	lock, err := t.store.Runtime.LoadPipelineExecution()
	if err != nil {
		return err
	}
	if !convergenceReplanExecutionLock(lock) || lock.TargetChapter != chapter {
		return nil
	}
	overlay, err := loadSealedConvergenceAuthorityOverlay(t.store)
	if err != nil {
		return fmt.Errorf("sealed convergence planning context missing authority overlay: %w", err)
	}
	context, _, err := loadProjectAllStateForExecution(t.store, chapter)
	if err != nil || context == nil {
		return fmt.Errorf("sealed convergence planning context missing projected state: %w", err)
	}
	if overlay.GenerationID != context.GenerationID || overlay.Chapter != chapter ||
		overlay.PlanningContextDigest != context.ContextDigest ||
		overlay.ProjectedPreStateRoot != context.StateRoot {
		return fmt.Errorf("sealed convergence planning context authority overlay drift")
	}
	var feedback struct {
		Chapter             int             `json:"chapter"`
		StateContractDigest string          `json:"state_contract_digest"`
		Diagnostics         json.RawMessage `json:"diagnostics"`
		DiagnosticsDigest   string          `json:"diagnostics_digest"`
	}
	feedbackRaw, err := os.ReadFile(filepath.Join(
		t.store.Dir(),
		filepath.FromSlash(sealedConvergenceReplanFeedbackPath),
	))
	if err != nil {
		return fmt.Errorf("sealed convergence planning context feedback is unavailable: %w", err)
	}
	if err := json.Unmarshal(feedbackRaw, &feedback); err != nil {
		return fmt.Errorf("decode sealed convergence planning context feedback: %w", err)
	}
	diagnosticsDigest, err := sealedConvergencePlanningDigest(feedback.Diagnostics)
	if err != nil || feedback.Chapter != chapter ||
		feedback.StateContractDigest != overlay.ImmutableStateContractSHA ||
		feedback.DiagnosticsDigest != diagnosticsDigest {
		return fmt.Errorf("sealed convergence planning context feedback binding drift")
	}
	result["sealed_convergence_replan_context"] = map[string]any{
		"version":                         overlay.Version,
		"generation_id":                   overlay.GenerationID,
		"chapter":                         overlay.Chapter,
		"planning_context_digest":         overlay.PlanningContextDigest,
		"projected_pre_state_root":        overlay.ProjectedPreStateRoot,
		"bundle_digest":                   overlay.BundleDigest,
		"promotion_receipt_digest":        overlay.PromotionReceiptDigest,
		"simulation_id":                   overlay.SimulationID,
		"simulation_digest":               overlay.SimulationDigest,
		"authority_receipt_digest":        overlay.AuthorityReceiptDigest,
		"immutable_state_contract_sha256": overlay.ImmutableStateContractSHA,
		"diagnostics_digest":              feedback.DiagnosticsDigest,
		"diagnostics":                     feedback.Diagnostics,
		"policy":                          "Host 已验证当前 world simulation 与 immutable sealed bundle 完全相同；本轮只重组 POV plan 的表达/场景执行字段。未来大纲、全量 projected 累计状态和角色 authority 包已安全折叠，不得 simulate、改写事实结果或改变 canon/cursor。",
	}
	return nil
}

func compactSealedConvergencePlanningContext(result map[string]any) {
	if result == nil {
		return
	}
	if _, active := result["sealed_convergence_replan_context"]; !active {
		return
	}
	// The immutable state contract is carried verbatim by the host task, while
	// the exact current projection, predecessor contract, due obligations,
	// receipts and user rules remain in this payload. Broad story snapshots and
	// future outlines cannot change a convergence successor and previously made
	// this 88 KiB critical packet impossible to deliver under the 64 KiB cap.
	for _, key := range []string{
		"outline", "future_outline_window", "next_chapter_outline", "next_plan",
		"characters", "character_dossiers", "character_continuity",
		"character_stage_records", "side_character_journeys",
		"book_world", "book_world_context", "world_codex", "world_foundation",
		"timeline", "recent_state_changes", "chapter_world_deltas",
		"relationship_state", "foreshadow_ledger", "evolution_report",
		"project_progress", "progression_snapshot", "horizon_events",
		"horizon_events_usage", "recent_summaries", "previous_tail",
		"references", "voice_samples", "style_stats",
		"simulation_character_authority", "simulation_character_authority_policy",
		"draft_external_ai_review", "draft_external_ai_review_policy",
		"rewrite_source", "rewrite_brief",
	} {
		deleteContextKey(result, key)
	}
	result["planning_context_policy"] = "sealed convergence successor 只消费当前章 outline、精确 protagonist_projection、immutable state contract、project-all predecessor/open obligations、content-addressed craft/fact receipts、user_rules 与净化 diagnostics；禁止读取未来大纲、失败正文或重建 world simulation。"
}

// validateSealedConvergenceSimulationAuthorityOverlay permits one narrow
// control-plane exception: a promoted sealed simulation may be planned again
// in the live workspace, where the original isolated project-all workspace
// manifest is intentionally absent.  Exact immutable bundle equality replaces
// rebuilding the old authority input root; no simulation field is rewritten.
func validateSealedConvergenceSimulationAuthorityOverlay(
	st *store.Store,
	simulation domain.ChapterWorldSimulation,
) error {
	overlay, err := loadSealedConvergenceAuthorityOverlay(st)
	if err != nil {
		return fmt.Errorf("load sealed convergence authority overlay: %w", err)
	}
	context, _, err := loadProjectAllStateForExecution(st, simulation.Chapter)
	if err != nil || context == nil {
		return fmt.Errorf("load sealed convergence authoritative planning state: %w", err)
	}
	if simulation.AuthorityReceipt == nil ||
		overlay.GenerationID != simulation.GenerationID ||
		overlay.Chapter != simulation.Chapter ||
		overlay.SimulationID != simulation.SimulationID ||
		overlay.AuthorityReceiptDigest != simulation.AuthorityReceipt.ReceiptDigest ||
		overlay.PlanningContextDigest != context.ContextDigest ||
		overlay.ProjectedPreStateRoot != context.StateRoot ||
		context.GenerationID != overlay.GenerationID || context.NextChapter != overlay.Chapter {
		return fmt.Errorf("sealed convergence authority overlay does not bind current simulation/state")
	}
	simulationDigest, err := sealedConvergencePlanningDigest(simulation)
	if err != nil || simulationDigest != overlay.SimulationDigest {
		return fmt.Errorf("sealed convergence authority overlay simulation digest mismatch")
	}

	bundles, err := st.LoadProjectedChapterBundlesV2(overlay.GenerationID)
	if err != nil {
		return fmt.Errorf("load sealed convergence projected bundles: %w", err)
	}
	matched := false
	for _, bundle := range bundles {
		if bundle.Chapter != overlay.Chapter || bundle.BundleDigest != overlay.BundleDigest ||
			bundle.PlanningContextDigest != overlay.PlanningContextDigest ||
			bundle.ProjectedPreStateRoot != overlay.ProjectedPreStateRoot {
			continue
		}
		bundleSimulationDigest, digestErr := sealedConvergencePlanningDigest(bundle.ChapterWorldSimulation)
		if digestErr != nil || bundleSimulationDigest != overlay.SimulationDigest {
			return fmt.Errorf("sealed convergence authority overlay bundle simulation mismatch")
		}
		matched = true
		break
	}
	if !matched {
		return fmt.Errorf("sealed convergence authority overlay has no exact immutable bundle")
	}
	if promotion, loadErr := st.ProjectedV2().LoadPromotionReceipt(
		overlay.GenerationID,
		overlay.Chapter,
		overlay.PromotionReceiptDigest,
	); loadErr != nil || promotion == nil || promotion.BundleDigest != overlay.BundleDigest {
		return fmt.Errorf("sealed convergence authority overlay promotion binding mismatch: %w", loadErr)
	}
	return nil
}
