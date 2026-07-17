package tools

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/store"
)

const planningContextAccessResultKey = "planning_context_access_receipt"

func planningContextAccessArgsMayWrite(args json.RawMessage) bool {
	var request struct {
		Chapter int    `json:"chapter"`
		Profile string `json:"profile"`
	}
	if json.Unmarshal(args, &request) != nil || request.Chapter <= 0 {
		return false
	}
	_, ok := planningContextAccessPhaseForProfile(request.Profile)
	return ok
}

func planningContextAccessPhaseForProfile(
	profile string,
) (domain.PlanningContextAccessPhase, bool) {
	switch strings.TrimSpace(profile) {
	case "world_simulation":
		return domain.PlanningContextAccessSimulate, true
	case "planning":
		return domain.PlanningContextAccessPlan, true
	default:
		return "", false
	}
}

func planningContextAccessProfileForPhase(
	phase domain.PlanningContextAccessPhase,
) string {
	switch phase {
	case domain.PlanningContextAccessSimulate:
		return "world_simulation"
	case domain.PlanningContextAccessPlan:
		return "planning"
	default:
		return ""
	}
}

func (t *ContextTool) finalizeContextWithAccessReceipt(
	result map[string]any,
	chapter int,
	profile string,
) (json.RawMessage, error) {
	receipt, sourceToken, err := t.preparePlanningContextAccessReceipt(chapter, profile)
	if err != nil {
		return nil, err
	}
	if receipt != nil {
		submitIn := "sources"
		if receipt.Phase == domain.PlanningContextAccessPlan {
			submitIn = "causal_simulation.context_sources"
		}
		result[planningContextAccessResultKey] = map[string]any{
			"version":      receipt.Version,
			"phase":        receipt.Phase,
			"source_token": sourceToken,
			"expires_at":   receipt.ExpiresAt.Format(time.RFC3339Nano),
			"submit_in":    submitIn,
			"policy":       "该不透明 token 只证明服务端已成功返回本阶段 exact context；finalize 还会核对服务端 receipt、project-all generation、chapter、planning_context_digest 与 execution owner，复制或跨阶段复用 token 无效。",
		}
	}
	raw, err := finalizeContextResult(result, chapter, profile)
	if err != nil {
		return nil, err
	}
	if receipt != nil {
		if err := t.store.Runtime.SavePlanningContextAccessReceipt(*receipt); err != nil {
			return nil, fmt.Errorf(
				"novel_context 无法签发服务端阶段访问回执: %w: %w",
				err,
				errs.ErrStoreWrite,
			)
		}
	}
	return raw, nil
}

func (t *ContextTool) preparePlanningContextAccessReceipt(
	chapter int,
	profile string,
) (*domain.PlanningContextAccessReceipt, string, error) {
	if t == nil || t.store == nil || chapter <= 0 {
		return nil, "", nil
	}
	phase, ok := planningContextAccessPhaseForProfile(profile)
	if !ok {
		return nil, "", nil
	}
	lock, err := t.store.Runtime.LoadPipelineExecution()
	if err != nil {
		return nil, "", fmt.Errorf("novel_context 读取阶段访问执行身份: %w", err)
	}
	if lock == nil || lock.Mode != domain.PipelineExecutionProjectAll {
		return nil, "", nil
	}
	if err := requireCurrentPipelineExecutionProcess(lock, "novel_context"); err != nil {
		return nil, "", err
	}
	if lock.TargetChapter != chapter {
		return nil, "", fmt.Errorf(
			"novel_context 阶段访问章号与 project-all 执行目标不一致: %w",
			errs.ErrToolPrecondition,
		)
	}
	planningContext, _, err := loadProjectAllStateForExecution(t.store, chapter)
	if err != nil {
		return nil, "", err
	}
	if planningContext == nil {
		return nil, "", fmt.Errorf(
			"novel_context 缺少 project-all authoritative context: %w",
			errs.ErrToolPrecondition,
		)
	}
	sourceToken, tokenSHA, err := newPlanningContextAccessToken()
	if err != nil {
		return nil, "", fmt.Errorf("novel_context 生成阶段访问回执失败: %w", err)
	}
	now := time.Now().UTC()
	receipt := domain.PlanningContextAccessReceipt{
		Version:               domain.PlanningContextAccessReceiptVersion,
		GenerationID:          planningContext.GenerationID,
		Chapter:               chapter,
		Profile:               strings.TrimSpace(profile),
		PlanningContextDigest: planningContext.ContextDigest,
		Phase:                 phase,
		LockMode:              lock.Mode,
		LockOwner:             strings.TrimSpace(lock.Owner),
		LockProcessID:         lock.ProcessID,
		LockAcquiredAt:        lock.AcquiredAt.UTC(),
		IssuedAt:              now,
		ExpiresAt:             lock.ExpiresAt.UTC(),
		TokenSHA256:           tokenSHA,
	}
	receipt.ReceiptDigest, err = domain.ComputePlanningContextAccessReceiptDigest(receipt)
	if err != nil {
		return nil, "", fmt.Errorf("novel_context 计算阶段访问回执失败: %w", err)
	}
	if err := domain.ValidatePlanningContextAccessReceipt(receipt); err != nil {
		return nil, "", fmt.Errorf("novel_context 阶段访问回执不完整: %w", err)
	}
	return &receipt, sourceToken, nil
}

func newPlanningContextAccessToken() (string, string, error) {
	entropy := make([]byte, 32)
	if _, err := rand.Read(entropy); err != nil {
		return "", "", err
	}
	sourceToken := domain.PlanningContextAccessTokenPrefix + hex.EncodeToString(entropy)
	tokenSHA, err := domain.PlanningContextAccessTokenSHA256(sourceToken)
	if err != nil {
		return "", "", err
	}
	return sourceToken, tokenSHA, nil
}

func consumePlanningContextAccessReceipt(
	st *store.Store,
	chapter int,
	phase domain.PlanningContextAccessPhase,
	sources []string,
) error {
	if st == nil || chapter <= 0 {
		return nil
	}
	lock, err := st.Runtime.LoadPipelineExecution()
	if err != nil {
		return fmt.Errorf("读取 context access execution identity: %w", err)
	}
	if lock == nil || lock.Mode != domain.PipelineExecutionProjectAll {
		receipt, loadErr := st.Runtime.LoadPlanningContextAccessReceipt(phase)
		if loadErr != nil {
			return fmt.Errorf("读取非活动 context access receipt 失败: %w", loadErr)
		}
		if receipt != nil || planningContextAccessSourcesPresent(sources) {
			return fmt.Errorf(
				"context access receipt 已失去其 project-all execution identity，禁止在锁过期、释放或换阶段后 finalize: %w",
				errs.ErrToolPrecondition,
			)
		}
		return nil
	}
	if err := requireCurrentPipelineExecutionProcess(lock, "planning finalize"); err != nil {
		return err
	}
	if lock.TargetChapter != chapter {
		return fmt.Errorf(
			"context access receipt 与当前 project-all 章号不一致: %w",
			errs.ErrToolPrecondition,
		)
	}
	planningContext, _, err := loadProjectAllStateForExecution(st, chapter)
	if err != nil {
		return err
	}
	if planningContext == nil {
		return fmt.Errorf(
			"context access receipt 无法绑定 authoritative planning context: %w",
			errs.ErrToolPrecondition,
		)
	}
	receipt, err := st.Runtime.LoadPlanningContextAccessReceipt(phase)
	if err != nil {
		return fmt.Errorf("读取服务端 context access receipt 失败: %w", err)
	}
	if receipt == nil {
		return fmt.Errorf(
			"必须先成功调用对应 profile 的 novel_context，再执行本阶段 finalize: %w",
			errs.ErrToolPrecondition,
		)
	}
	sourceToken, err := extractPlanningContextAccessToken(
		sources,
		receipt.TokenSHA256,
	)
	if err != nil {
		return err
	}
	if receipt.GenerationID != planningContext.GenerationID ||
		receipt.Chapter != chapter ||
		receipt.Profile != planningContextAccessProfileForPhase(phase) ||
		receipt.PlanningContextDigest != planningContext.ContextDigest ||
		receipt.Phase != phase ||
		receipt.LockMode != lock.Mode ||
		receipt.LockOwner != strings.TrimSpace(lock.Owner) ||
		receipt.LockProcessID != lock.ProcessID ||
		!receipt.LockAcquiredAt.Equal(lock.AcquiredAt) ||
		!receipt.ExpiresAt.Equal(lock.ExpiresAt) {
		return fmt.Errorf(
			"服务端 context access receipt 与当前 generation/chapter/phase/execution identity 不一致: %w",
			errs.ErrToolPrecondition,
		)
	}
	if !receipt.ConsumedAt.IsZero() {
		return fmt.Errorf(
			"服务端 context access receipt 已被对应 finalize 消费，必须重新读取 context: %w",
			errs.ErrToolPrecondition,
		)
	}
	if err := st.Runtime.ConsumePlanningContextAccessReceipt(
		*receipt,
		sourceToken,
		time.Now().UTC(),
	); err != nil {
		return fmt.Errorf(
			"服务端 context access receipt 无法消费: %w: %w",
			err,
			errs.ErrToolPrecondition,
		)
	}
	return nil
}

func extractPlanningContextAccessToken(
	sources []string,
	expectedTokenSHA256 string,
) (string, error) {
	var matched string
	for _, source := range sources {
		source = strings.TrimSpace(source)
		if !strings.HasPrefix(source, domain.PlanningContextAccessTokenPrefix) {
			continue
		}
		tokenSHA, err := domain.PlanningContextAccessTokenSHA256(source)
		if err != nil || subtle.ConstantTimeCompare(
			[]byte(tokenSHA),
			[]byte(expectedTokenSHA256),
		) != 1 {
			continue
		}
		if matched != "" && source != matched {
			return "", fmt.Errorf(
				"context access sources 包含多个匹配当前回执的阶段 token: %w",
				errs.ErrToolPrecondition,
			)
		}
		matched = source
	}
	if matched == "" {
		return "", fmt.Errorf(
			"context access sources 缺少与当前服务端回执匹配的阶段 token: %w",
			errs.ErrToolPrecondition,
		)
	}
	return matched, nil
}

func planningContextAccessSourcesPresent(sources []string) bool {
	for _, source := range sources {
		if strings.HasPrefix(
			strings.TrimSpace(source),
			domain.PlanningContextAccessTokenPrefix,
		) {
			return true
		}
	}
	return false
}
