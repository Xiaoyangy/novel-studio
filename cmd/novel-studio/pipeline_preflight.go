package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/aigc"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

const (
	pipelinePreflightVersion = "pipeline-preflight.v1"
	// 64 KiB is the draft-context builder's normal delivery target, not a
	// migration rejection boundary. Historical sealed contexts and bounded
	// convergence feedback may legitimately use the existing 96 KiB hard
	// ceiling; preflight must remain compatible with those immutable bytes.
	pipelinePreflightRenderContextHardMaxBytes = 96 * 1024
	pipelinePreflightReportDir                 = "meta/planning/preflight"
)

type pipelinePreflightStage string

const (
	pipelinePreflightStageSeal    pipelinePreflightStage = "seal"
	pipelinePreflightStagePromote pipelinePreflightStage = "promote"
	pipelinePreflightStageRender  pipelinePreflightStage = "render"
)

type pipelinePreflightDomain string

const (
	pipelinePreflightDomainBundle         pipelinePreflightDomain = "bundle"
	pipelinePreflightDomainContext        pipelinePreflightDomain = "context"
	pipelinePreflightDomainAIGC           pipelinePreflightDomain = "aigc"
	pipelinePreflightDomainSealedIdentity pipelinePreflightDomain = "sealed_identity"
)

// pipelinePreflightSealedIdentity is the caller's independently loaded control
// plane identity. Empty fields are not asserted, which lets seal validate a
// bundle before a promotion/frozen-plan receipt exists while promote/render can
// bind every identity they already possess.
type pipelinePreflightSealedIdentity struct {
	GenerationID           string
	Chapter                int
	BundleDigest           string
	PlanDigest             string
	PlanningContextDigest  string
	RenderContextSHA256    string
	ProjectedPreStateRoot  string
	ProjectedPostStateRoot string
}

type pipelinePreflightInput struct {
	Stage pipelinePreflightStage
	// Bundle is always the immutable projected bundle whose own domain
	// identity is validated. RenderContext, when non-empty, is the separately
	// frozen prose payload that will actually be sent to the model. This lets a
	// convergence successor validate its exact frozen bytes without mutating or
	// pretending to re-sign the original projected bundle.
	Bundle        domain.ProjectedChapterBundle
	RenderContext json.RawMessage
	Expected      *pipelinePreflightSealedIdentity
}

type pipelinePreflightCheck struct {
	Code     string                  `json:"code"`
	Domain   pipelinePreflightDomain `json:"domain"`
	Chapter  int                     `json:"chapter,omitempty"`
	Artifact string                  `json:"artifact,omitempty"`
	Passed   bool                    `json:"passed"`
}

type pipelinePreflightBlocker struct {
	Code     string                  `json:"code"`
	Domain   pipelinePreflightDomain `json:"domain"`
	Chapter  int                     `json:"chapter,omitempty"`
	Artifact string                  `json:"artifact,omitempty"`
	Expected string                  `json:"expected,omitempty"`
	Actual   string                  `json:"actual,omitempty"`
	Detail   string                  `json:"detail"`
	Repair   string                  `json:"repair"`
}

type pipelinePreflightReport struct {
	Version      string                     `json:"version"`
	Stage        pipelinePreflightStage     `json:"stage"`
	GenerationID string                     `json:"generation_id,omitempty"`
	Chapter      int                        `json:"chapter,omitempty"`
	Ready        bool                       `json:"ready"`
	Checks       []pipelinePreflightCheck   `json:"checks"`
	Blockers     []pipelinePreflightBlocker `json:"blockers"`
}

// PipelinePreflightError is returned by Report.Require. It retains the full
// typed report so callers can render one actionable response without parsing
// an error string or rerunning fail-fast validators.
type PipelinePreflightError struct {
	Report pipelinePreflightReport
}

func (e *PipelinePreflightError) Error() string {
	if e == nil {
		return "pipeline preflight blocked"
	}
	parts := make([]string, 0, len(e.Report.Blockers))
	for _, blocker := range e.Report.Blockers {
		where := blocker.Artifact
		if blocker.Chapter > 0 {
			where = fmt.Sprintf("chapter=%d %s", blocker.Chapter, where)
		}
		where = strings.TrimSpace(where)
		if where != "" {
			where = " (" + where + ")"
		}
		parts = append(parts, fmt.Sprintf("[%s]%s %s", blocker.Code, where, blocker.Detail))
	}
	if len(parts) == 0 {
		return fmt.Sprintf("pipeline %s preflight blocked", e.Report.Stage)
	}
	return fmt.Sprintf("pipeline %s preflight blocked: %s", e.Report.Stage, strings.Join(parts, "; "))
}

func (r pipelinePreflightReport) Require() error {
	if r.Ready && len(r.Blockers) == 0 {
		return nil
	}
	clone := r
	clone.Checks = append([]pipelinePreflightCheck(nil), r.Checks...)
	clone.Blockers = append([]pipelinePreflightBlocker(nil), r.Blockers...)
	return &PipelinePreflightError{Report: clone}
}

type pipelinePreflightCollector struct {
	report pipelinePreflightReport
}

func compilePipelinePreflight(input pipelinePreflightInput) pipelinePreflightReport {
	c := &pipelinePreflightCollector{report: pipelinePreflightReport{
		Version:      pipelinePreflightVersion,
		Stage:        input.Stage,
		GenerationID: strings.TrimSpace(input.Bundle.GenerationID),
		Chapter:      input.Bundle.Chapter,
		Checks:       []pipelinePreflightCheck{},
		Blockers:     []pipelinePreflightBlocker{},
	}}

	c.check(
		"bundle.domain_valid",
		pipelinePreflightDomainBundle,
		input.Bundle.Chapter,
		"projected_chapter_bundle",
		domain.ValidateProjectedChapterBundle(input.Bundle),
		"完整且 digest 自洽的 projected chapter bundle",
		"重新生成或重新封存该章 bundle；不要在 render 阶段修补 bundle",
	)

	renderContext := input.Bundle.RenderContext
	if len(input.RenderContext) > 0 {
		renderContext = input.RenderContext
	}
	contextSizeErr := error(nil)
	if len(renderContext) > pipelinePreflightRenderContextHardMaxBytes {
		contextSizeErr = fmt.Errorf(
			"render_context=%d bytes exceeds %d-byte limit",
			len(renderContext),
			pipelinePreflightRenderContextHardMaxBytes,
		)
	}
	c.check(
		"context.size_within_limit",
		pipelinePreflightDomainContext,
		input.Bundle.Chapter,
		"render_context",
		contextSizeErr,
		fmt.Sprintf("<=%d bytes", pipelinePreflightRenderContextHardMaxBytes),
		"在 project-all 阶段压缩 prose-facing render_packet，禁止渲染时临时删硬合同",
	)

	contextDigest, contextDigestErr := domain.ComputePlanningV2JSONDigest(renderContext)
	if input.Expected != nil && strings.TrimSpace(input.Expected.RenderContextSHA256) != "" {
		if contextDigestErr == nil && contextDigest != input.Expected.RenderContextSHA256 {
			contextDigestErr = fmt.Errorf(
				"render_context digest=%s, want %s",
				contextDigest,
				input.Expected.RenderContextSHA256,
			)
		}
		c.check(
			"context.frozen_digest",
			pipelinePreflightDomainContext,
			input.Bundle.Chapter,
			"render_context",
			contextDigestErr,
			input.Expected.RenderContextSHA256,
			"重新加载同一冻结计划绑定的 exact render context；禁止回退 live context builder",
		)
	}

	var payload map[string]any
	contextErr := json.Unmarshal(renderContext, &payload)
	if contextErr == nil && payload == nil {
		contextErr = fmt.Errorf("render_context must be a JSON object")
	}
	c.check(
		"context.json_object",
		pipelinePreflightDomainContext,
		input.Bundle.Chapter,
		"render_context",
		contextErr,
		"valid JSON object",
		"重新物化并封存 canonical draft render context",
	)

	if contextErr == nil {
		profile, _ := payload["_context_profile"].(string)
		var profileErr error
		if strings.TrimSpace(profile) != "draft" {
			profileErr = fmt.Errorf("_context_profile=%q, want draft", profile)
		}
		c.check(
			"context.draft_profile",
			pipelinePreflightDomainContext,
			input.Bundle.Chapter,
			"render_context._context_profile",
			profileErr,
			"draft",
			"用 profile=draft 重新构建并封存正文上下文",
		)

		c.checkRenderPacket(input.Bundle.Chapter, payload)
		c.checkSealedContract(input.Bundle, payload)
	} else {
		c.blockSkippedContextChecks(input.Bundle.Chapter)
	}

	c.checkExpectedIdentity(input.Bundle, contextDigest, input.Expected)
	c.finish()
	return c.report
}

// compilePipelinePreflightBatch preserves every chapter blocker in one seal
// report. Seal must never stop at the first malformed chapter and hide the
// remaining repair set from the operator.
func compilePipelinePreflightBatch(
	stage pipelinePreflightStage,
	generationID string,
	inputs []pipelinePreflightInput,
) pipelinePreflightReport {
	c := &pipelinePreflightCollector{report: pipelinePreflightReport{
		Version:      pipelinePreflightVersion,
		Stage:        stage,
		GenerationID: strings.TrimSpace(generationID),
		Checks:       []pipelinePreflightCheck{},
		Blockers:     []pipelinePreflightBlocker{},
	}}
	if len(inputs) == 0 {
		c.block(
			"bundle.none",
			pipelinePreflightDomainBundle,
			0,
			"projected_chapter_bundles",
			"at least one projected chapter bundle",
			"none",
			"seal preflight 没有可验证的 projected chapter bundle",
			"完成 project-all 后重新执行 seal",
		)
	}
	for _, input := range inputs {
		input.Stage = stage
		report := compilePipelinePreflight(input)
		c.report.Checks = append(c.report.Checks, report.Checks...)
		c.report.Blockers = append(c.report.Blockers, report.Blockers...)
	}
	c.finish()
	return c.report
}

func pipelinePreflightReportPath(outputDir string, report pipelinePreflightReport) string {
	name := string(report.Stage)
	if strings.TrimSpace(name) == "" {
		name = "unknown"
	}
	if report.Chapter > 0 {
		name += fmt.Sprintf("-ch%04d", report.Chapter)
	}
	return filepath.Join(outputDir, filepath.FromSlash(pipelinePreflightReportDir), name+".json")
}

// persistAndRequirePipelinePreflight writes audit evidence outside canon and
// then enforces the typed result. A report-write failure is itself a blocker:
// production must not proceed without leaving the zero-model preflight trail.
func persistAndRequirePipelinePreflight(
	outputDir string,
	report pipelinePreflightReport,
) error {
	if _, err := writePipelinePlanningJSON(
		pipelinePreflightReportPath(outputDir, report),
		report,
	); err != nil {
		clone := report
		clone.Checks = append([]pipelinePreflightCheck(nil), report.Checks...)
		clone.Blockers = append([]pipelinePreflightBlocker(nil), report.Blockers...)
		c := &pipelinePreflightCollector{report: clone}
		c.block(
			"preflight.report_persist",
			pipelinePreflightDomainContext,
			report.Chapter,
			pipelinePreflightReportDir,
			"durable non-canon preflight report",
			err.Error(),
			"preflight report 无法落盘",
			"修复 meta/planning/preflight 写入权限或磁盘错误后重试",
		)
		c.finish()
		return c.report.Require()
	}
	return report.Require()
}

// requirePipelineSealedRenderPreflight loads only content-addressed sealed
// control evidence plus the already-frozen prose payload. It never invokes a
// context builder or consults current live RAG membership.
func requirePipelineSealedRenderPreflight(
	st *store.Store,
	frozen *pipelineFrozenPlan,
	postCommitRecovery bool,
) (*pipelineSealedRenderBinding, error) {
	if st == nil || frozen == nil {
		return nil, fmt.Errorf("sealed render preflight requires store and frozen plan")
	}
	binding, err := validatePipelineSealedRenderBinding(st, frozen, postCommitRecovery)
	if err != nil {
		return nil, err
	}
	renderContext, envelope, err := tools.LoadFrozenDraftRenderContext(
		st,
		frozen.Chapter,
		frozen.PlanDigest,
	)
	if err != nil {
		return nil, fmt.Errorf("sealed render preflight load frozen context: %w", err)
	}
	if envelope.PayloadSHA256 != frozen.RenderContextSHA256 {
		return nil, fmt.Errorf("sealed render preflight frozen context identity drift")
	}
	report := compilePipelinePreflight(pipelinePreflightInput{
		Stage:         pipelinePreflightStageRender,
		Bundle:        binding.Bundle,
		RenderContext: renderContext,
		Expected: &pipelinePreflightSealedIdentity{
			GenerationID:           frozen.PlanningGenerationID,
			Chapter:                frozen.Chapter,
			BundleDigest:           frozen.ProjectedBundleDigest,
			PlanDigest:             frozen.ProjectedPlanSHA256,
			PlanningContextDigest:  binding.Bundle.PlanningContextDigest,
			RenderContextSHA256:    frozen.RenderContextSHA256,
			ProjectedPreStateRoot:  frozen.ProjectedPreStateRoot,
			ProjectedPostStateRoot: frozen.ProjectedPostStateRoot,
		},
	})
	if err := persistAndRequirePipelinePreflight(st.Dir(), report); err != nil {
		return nil, err
	}
	return binding, nil
}

func (c *pipelinePreflightCollector) checkRenderPacket(chapter int, payload map[string]any) {
	packet, artifact, packetErr := pipelinePreflightRenderPacket(payload)
	c.check(
		"aigc.render_packet_present",
		pipelinePreflightDomainAIGC,
		chapter,
		artifact,
		packetErr,
		"exactly one prose-facing render_packet",
		"在 project-all 的 draft context 中投影唯一 render_packet",
	)
	if packetErr != nil {
		c.block(
			"aigc.render_packet_version",
			pipelinePreflightDomainAIGC,
			chapter,
			artifact+".version",
			"exact v11",
			"unavailable",
			"render_packet 缺失，无法验证 AIGC 协议版本",
			"重新生成 exact v11 render_packet",
		)
		c.block(
			"aigc.render_packet_chapter",
			pipelinePreflightDomainAIGC,
			chapter,
			artifact+".chapter",
			fmt.Sprint(chapter),
			"unavailable",
			"render_packet 缺失，无法验证章节绑定",
			"重新投影当前章节的 render_packet",
		)
		c.block(
			"aigc.render_contract_shape",
			pipelinePreflightDomainAIGC,
			chapter,
			artifact+".anti_ai_render_contract",
			"complete typed v11 prospective contract",
			"unavailable",
			"render_packet 缺失，无法验证 prospective AIGC 合同完整结构",
			"重新生成完整 v11 anti_ai_render_contract",
		)
		c.block(
			"aigc.usage_policy_before_draft",
			pipelinePreflightDomainAIGC,
			chapter,
			artifact+".anti_ai_render_contract.usage_policy",
			string(aigc.ProseRenderUsagePolicyV1),
			"unavailable",
			"render_packet 缺失，无法证明 AIGC 规则会在首稿前生效",
			"重新投影 anti_ai_render_contract，禁止渲染模型临场发挥",
		)
		return
	}

	version, ok := aigc.ProseRenderPacketVersion(packet)
	var versionErr error
	if !ok || version != aigc.ProseRenderCompatibilityPacketVersion {
		versionErr = fmt.Errorf(
			"render_packet.version=%v, want exact %d",
			packet["version"],
			aigc.ProseRenderCompatibilityPacketVersion,
		)
	}
	c.check(
		"aigc.render_packet_version",
		pipelinePreflightDomainAIGC,
		chapter,
		artifact+".version",
		versionErr,
		"exact v11",
		"用当前 exact v11 prose packet 重新封存上下文",
	)
	chapterErr := aigc.ValidateProseRenderPacketChapter(packet, chapter)
	c.check(
		"aigc.render_packet_chapter",
		pipelinePreflightDomainAIGC,
		chapter,
		artifact+".chapter",
		chapterErr,
		fmt.Sprint(chapter),
		"重新投影与 bundle actionable chapter 精确一致的 render_packet",
	)

	contractValue, contractPresent := packet["anti_ai_render_contract"]
	antiAI, contractObject := contractValue.(map[string]any)
	if ok && chapterErr == nil && !contractPresent && aigc.SupportsProseRenderCompatibility(version) {
		c.pass(
			"aigc.compatibility_injection",
			pipelinePreflightDomainAIGC,
			chapter,
			artifact+".anti_ai_render_contract@"+aigc.ProseRenderCompatibilityProtocolVersion,
		)
		c.pass(
			"aigc.render_contract_shape",
			pipelinePreflightDomainAIGC,
			chapter,
			artifact+".anti_ai_render_contract@"+aigc.ProseRenderCompatibilityProtocolVersion,
		)
		c.pass(
			"aigc.usage_policy_before_draft",
			pipelinePreflightDomainAIGC,
			chapter,
			artifact+".anti_ai_render_contract.usage_policy",
		)
		return
	}
	c.check(
		"aigc.render_contract_shape",
		pipelinePreflightDomainAIGC,
		chapter,
		artifact+".anti_ai_render_contract",
		aigc.ValidateProseRenderContractV11(contractValue),
		"complete typed v11 prospective contract",
		"重新投影包含 risks/counter-moves/rhythm/object/dialogue/review checks 的完整 prospective 合同",
	)

	usage, _ := antiAI["usage_policy"].(string)
	var usageErr error
	if !contractObject || antiAI == nil || usage != string(aigc.ProseRenderUsagePolicyV1) {
		usageErr = fmt.Errorf(
			"anti_ai_render_contract.usage_policy=%q, want exact typed policy %q",
			usage,
			aigc.ProseRenderUsagePolicyV1,
		)
	}
	c.check(
		"aigc.usage_policy_before_draft",
		pipelinePreflightDomainAIGC,
		chapter,
		artifact+".anti_ai_render_contract.usage_policy",
		usageErr,
		string(aigc.ProseRenderUsagePolicyV1),
		"在冻结上下文中写入 prospective anti_ai_render_contract；不得等生成后才读取规则",
	)
}

func pipelinePreflightRenderPacket(payload map[string]any) (map[string]any, string, error) {
	packet, path, err := aigc.FindUniqueProseRenderPacket(payload)
	return packet, "render_context." + path, err
}

func (c *pipelinePreflightCollector) checkSealedContract(
	bundle domain.ProjectedChapterBundle,
	payload map[string]any,
) {
	contract, ok := payload["sealed_projection_contract"].(map[string]any)
	if !ok {
		c.block(
			"sealed_identity.contract_present",
			pipelinePreflightDomainSealedIdentity,
			bundle.Chapter,
			"render_context.sealed_projection_contract",
			domain.SealedProjectionRenderContractV2Version,
			"missing",
			"render context 未携带 sealed projection identity",
			"从 exact bundle 重新绑定 render context",
		)
		return
	}
	c.pass("sealed_identity.contract_present", pipelinePreflightDomainSealedIdentity, bundle.Chapter, "render_context.sealed_projection_contract")

	checks := []struct {
		code     string
		field    string
		expected string
		actual   string
	}{
		{"sealed_identity.version", "version", domain.SealedProjectionRenderContractV2Version, pipelinePreflightString(contract["version"])},
		{"sealed_identity.generation", "generation_id", bundle.GenerationID, pipelinePreflightString(contract["generation_id"])},
		{"sealed_identity.chapter", "chapter", fmt.Sprint(bundle.Chapter), pipelinePreflightIntString(contract["chapter"])},
		{"sealed_identity.planning_context", "planning_context_digest", bundle.PlanningContextDigest, pipelinePreflightString(contract["planning_context_digest"])},
		{"sealed_identity.pre_state", "projected_pre_state", bundle.ProjectedPreStateRoot, pipelinePreflightString(contract["projected_pre_state"])},
		{"sealed_identity.post_state", "projected_post_state", bundle.ProjectedPostStateRoot, pipelinePreflightString(contract["projected_post_state"])},
	}
	if digest, err := domain.ComputeChapterPlanV2Digest(bundle.ChapterPlan); err == nil {
		checks = append(checks, struct {
			code, field, expected, actual string
		}{"sealed_identity.plan", "chapter_plan_digest", digest, pipelinePreflightString(contract["chapter_plan_digest"])})
	} else {
		c.block("sealed_identity.plan", pipelinePreflightDomainSealedIdentity, bundle.Chapter, "render_context.sealed_projection_contract.chapter_plan_digest", "valid plan digest", err.Error(), "无法计算 bundle plan identity", "修复 formal plan 后重新封存 bundle")
	}
	if digest, err := domain.DeterministicPlanningHash(bundle.ChapterWorldSimulation); err == nil {
		checks = append(checks, struct {
			code, field, expected, actual string
		}{"sealed_identity.simulation", "world_simulation_digest", digest, pipelinePreflightString(contract["world_simulation_digest"])})
	} else {
		c.block("sealed_identity.simulation", pipelinePreflightDomainSealedIdentity, bundle.Chapter, "render_context.sealed_projection_contract.world_simulation_digest", "valid simulation digest", err.Error(), "无法计算 bundle simulation identity", "修复 world simulation 后重新封存 bundle")
	}
	for _, item := range checks {
		if item.actual == item.expected {
			c.pass(item.code, pipelinePreflightDomainSealedIdentity, bundle.Chapter, "render_context.sealed_projection_contract."+item.field)
			continue
		}
		c.block(
			item.code,
			pipelinePreflightDomainSealedIdentity,
			bundle.Chapter,
			"render_context.sealed_projection_contract."+item.field,
			item.expected,
			item.actual,
			"sealed projection identity 与 bundle 不一致",
			"从当前 exact bundle 重新绑定并封存 render context",
		)
	}
}

func (c *pipelinePreflightCollector) checkExpectedIdentity(
	bundle domain.ProjectedChapterBundle,
	renderContextSHA256 string,
	expected *pipelinePreflightSealedIdentity,
) {
	if expected == nil {
		return
	}
	planDigest, planErr := domain.ComputeChapterPlanV2Digest(bundle.ChapterPlan)
	if planErr != nil {
		planDigest = "invalid: " + planErr.Error()
	}
	checks := []struct {
		code     string
		artifact string
		expected string
		actual   string
	}{
		{"sealed_identity.expected_generation", "control.generation_id", expected.GenerationID, bundle.GenerationID},
		{"sealed_identity.expected_bundle", "control.bundle_digest", expected.BundleDigest, bundle.BundleDigest},
		{"sealed_identity.expected_plan", "control.plan_digest", expected.PlanDigest, planDigest},
		{"sealed_identity.expected_planning_context", "control.planning_context_digest", expected.PlanningContextDigest, bundle.PlanningContextDigest},
		{"sealed_identity.expected_render_context", "control.render_context_sha256", expected.RenderContextSHA256, renderContextSHA256},
		{"sealed_identity.expected_pre_state", "control.projected_pre_state_root", expected.ProjectedPreStateRoot, bundle.ProjectedPreStateRoot},
		{"sealed_identity.expected_post_state", "control.projected_post_state_root", expected.ProjectedPostStateRoot, bundle.ProjectedPostStateRoot},
	}
	for _, item := range checks {
		if strings.TrimSpace(item.expected) == "" {
			continue
		}
		if item.expected == item.actual {
			c.pass(item.code, pipelinePreflightDomainSealedIdentity, bundle.Chapter, item.artifact)
			continue
		}
		c.block(item.code, pipelinePreflightDomainSealedIdentity, bundle.Chapter, item.artifact, item.expected, item.actual, "caller control identity 与 bundle 不一致", "重新加载同一 generation/chapter 的 exact control receipts")
	}
	if expected.Chapter > 0 {
		if expected.Chapter == bundle.Chapter {
			c.pass("sealed_identity.expected_chapter", pipelinePreflightDomainSealedIdentity, bundle.Chapter, "control.chapter")
		} else {
			c.block("sealed_identity.expected_chapter", pipelinePreflightDomainSealedIdentity, bundle.Chapter, "control.chapter", fmt.Sprint(expected.Chapter), fmt.Sprint(bundle.Chapter), "caller control chapter 与 bundle 不一致", "重新加载当前 actionable chapter 的 bundle")
		}
	}
}

func (c *pipelinePreflightCollector) blockSkippedContextChecks(chapter int) {
	for _, item := range []struct {
		code     string
		domain   pipelinePreflightDomain
		artifact string
		expected string
		detail   string
		repair   string
	}{
		{"context.draft_profile", pipelinePreflightDomainContext, "render_context._context_profile", "draft", "render_context 非法，无法验证 draft profile", "重新物化 canonical draft context"},
		{"aigc.render_packet_present", pipelinePreflightDomainAIGC, "render_context.render_packet", "present", "render_context 非法，无法读取 render_packet", "重新生成 exact v11 render_packet"},
		{"aigc.render_packet_version", pipelinePreflightDomainAIGC, "render_context.render_packet.version", "exact v11", "render_context 非法，无法验证 AIGC 协议版本", "重新生成 exact v11 render_packet"},
		{"aigc.render_packet_chapter", pipelinePreflightDomainAIGC, "render_context.render_packet.chapter", "exact actionable chapter", "render_context 非法，无法验证章节绑定", "重新投影当前章节的 render_packet"},
		{"aigc.render_contract_shape", pipelinePreflightDomainAIGC, "render_context.render_packet.anti_ai_render_contract", "complete typed v11 prospective contract", "render_context 非法，无法验证 prospective AIGC 合同完整结构", "重新投影完整 prospective anti_ai_render_contract"},
		{"aigc.usage_policy_before_draft", pipelinePreflightDomainAIGC, "render_context.render_packet.anti_ai_render_contract.usage_policy", string(aigc.ProseRenderUsagePolicyV1), "render_context 非法，无法证明 AIGC 规则在首稿前生效", "重新投影 prospective anti_ai_render_contract"},
		{"sealed_identity.contract_present", pipelinePreflightDomainSealedIdentity, "render_context.sealed_projection_contract", "present", "render_context 非法，无法读取 sealed identity", "从 exact bundle 重新绑定 render context"},
	} {
		c.block(item.code, item.domain, chapter, item.artifact, item.expected, "unavailable", item.detail, item.repair)
	}
}

func (c *pipelinePreflightCollector) check(
	code string,
	domainName pipelinePreflightDomain,
	chapter int,
	artifact string,
	err error,
	expected string,
	repair string,
) {
	if err == nil {
		c.pass(code, domainName, chapter, artifact)
		return
	}
	c.block(code, domainName, chapter, artifact, expected, err.Error(), err.Error(), repair)
}

func (c *pipelinePreflightCollector) pass(code string, domainName pipelinePreflightDomain, chapter int, artifact string) {
	c.report.Checks = append(c.report.Checks, pipelinePreflightCheck{
		Code: code, Domain: domainName, Chapter: chapter, Artifact: artifact, Passed: true,
	})
}

func (c *pipelinePreflightCollector) block(
	code string,
	domainName pipelinePreflightDomain,
	chapter int,
	artifact string,
	expected string,
	actual string,
	detail string,
	repair string,
) {
	c.report.Checks = append(c.report.Checks, pipelinePreflightCheck{
		Code: code, Domain: domainName, Chapter: chapter, Artifact: artifact, Passed: false,
	})
	c.report.Blockers = append(c.report.Blockers, pipelinePreflightBlocker{
		Code: code, Domain: domainName, Chapter: chapter, Artifact: artifact,
		Expected: expected, Actual: actual, Detail: detail, Repair: repair,
	})
}

func (c *pipelinePreflightCollector) finish() {
	sort.SliceStable(c.report.Checks, func(i, j int) bool {
		left, right := c.report.Checks[i], c.report.Checks[j]
		if left.Code != right.Code {
			return left.Code < right.Code
		}
		if left.Chapter != right.Chapter {
			return left.Chapter < right.Chapter
		}
		return left.Artifact < right.Artifact
	})
	sort.SliceStable(c.report.Blockers, func(i, j int) bool {
		left, right := c.report.Blockers[i], c.report.Blockers[j]
		if left.Code != right.Code {
			return left.Code < right.Code
		}
		if left.Chapter != right.Chapter {
			return left.Chapter < right.Chapter
		}
		if left.Artifact != right.Artifact {
			return left.Artifact < right.Artifact
		}
		return left.Detail < right.Detail
	})
	c.report.Ready = len(c.report.Blockers) == 0
}

func pipelinePreflightJSONInt(value any) (int, bool) {
	switch number := value.(type) {
	case int:
		return number, true
	case int64:
		return int(number), int64(int(number)) == number
	case float64:
		integer := int(number)
		return integer, float64(integer) == number
	case json.Number:
		parsed, err := number.Int64()
		return int(parsed), err == nil && int64(int(parsed)) == parsed
	default:
		return 0, false
	}
}

func pipelinePreflightString(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func pipelinePreflightIntString(value any) string {
	if number, ok := pipelinePreflightJSONInt(value); ok {
		return fmt.Sprint(number)
	}
	return fmt.Sprint(value)
}
