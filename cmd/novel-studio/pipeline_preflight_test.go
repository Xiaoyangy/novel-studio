package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/aigc"
	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestPipelinePreflightAcceptsValidSealedDraftBundle(t *testing.T) {
	bundle := pipelinePreflightTestBundle(t)
	planDigest, err := domain.ComputeChapterPlanV2Digest(bundle.ChapterPlan)
	if err != nil {
		t.Fatal(err)
	}
	report := compilePipelinePreflight(pipelinePreflightInput{
		Stage:  pipelinePreflightStageRender,
		Bundle: bundle,
		Expected: &pipelinePreflightSealedIdentity{
			GenerationID:           bundle.GenerationID,
			Chapter:                bundle.Chapter,
			BundleDigest:           bundle.BundleDigest,
			PlanDigest:             planDigest,
			PlanningContextDigest:  bundle.PlanningContextDigest,
			RenderContextSHA256:    bundle.RenderContextSHA256,
			ProjectedPreStateRoot:  bundle.ProjectedPreStateRoot,
			ProjectedPostStateRoot: bundle.ProjectedPostStateRoot,
		},
	})
	if !report.Ready || len(report.Blockers) != 0 {
		t.Fatalf("valid sealed draft bundle was blocked: %+v", report.Blockers)
	}
	if err := report.Require(); err != nil {
		t.Fatalf("Require rejected ready report: %v", err)
	}
	for _, want := range []string{
		"bundle.domain_valid",
		"context.size_within_limit",
		"context.draft_profile",
		"aigc.render_packet_version",
		"aigc.render_packet_chapter",
		"aigc.render_contract_shape",
		"aigc.usage_policy_before_draft",
		"sealed_identity.plan",
	} {
		if !pipelinePreflightTestCheckPassed(report, want) {
			t.Fatalf("ready report did not record passed check %q: %+v", want, report.Checks)
		}
	}
}

func TestPipelinePreflightRejectsRenderPacketForAnotherChapterBeforeCompatibility(t *testing.T) {
	bundle := pipelinePreflightTestBundle(t)
	var payload map[string]any
	if err := json.Unmarshal(bundle.RenderContext, &payload); err != nil {
		t.Fatal(err)
	}
	packet := payload["working_memory"].(map[string]any)["render_packet"].(map[string]any)
	packet["chapter"] = bundle.Chapter + 1
	delete(packet, "anti_ai_render_contract")
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	report := compilePipelinePreflight(pipelinePreflightInput{
		Stage: pipelinePreflightStageRender, Bundle: bundle, RenderContext: raw,
	})
	if report.Ready || !pipelinePreflightTestHasBlocker(report, bundle.Chapter, "aigc.render_packet_chapter") {
		t.Fatalf("cross-chapter legacy packet reached provider boundary: %+v", report)
	}
	if pipelinePreflightTestCheckPassed(report, "aigc.compatibility_injection") {
		t.Fatalf("cross-chapter legacy packet received compatibility admission: %+v", report.Checks)
	}
}

func TestPipelinePreflightAcceptsHistoricalV11ThroughVersionedCompatibilityInjection(t *testing.T) {
	bundle := pipelinePreflightTestBundle(t)
	var payload map[string]any
	if err := json.Unmarshal(bundle.RenderContext, &payload); err != nil {
		t.Fatal(err)
	}
	packet := payload["working_memory"].(map[string]any)["render_packet"].(map[string]any)
	delete(packet, "anti_ai_render_contract")
	legacy, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	original := append(json.RawMessage(nil), legacy...)

	report := compilePipelinePreflight(pipelinePreflightInput{
		Stage:         pipelinePreflightStageRender,
		Bundle:        bundle,
		RenderContext: legacy,
	})
	if !report.Ready || len(report.Blockers) != 0 {
		t.Fatalf("historical v11 context was not admitted through compatibility protocol: %+v", report.Blockers)
	}
	if !pipelinePreflightTestCheckPassed(report, "aigc.compatibility_injection") ||
		!pipelinePreflightTestCheckPassed(report, "aigc.usage_policy_before_draft") {
		t.Fatalf("compatibility admission was not explicitly audited: %+v", report.Checks)
	}
	foundProtocol := false
	for _, check := range report.Checks {
		if check.Code == "aigc.compatibility_injection" &&
			strings.Contains(check.Artifact, aigc.ProseRenderCompatibilityProtocolVersion) {
			foundProtocol = true
		}
	}
	if !foundProtocol {
		t.Fatalf("compatibility audit omitted protocol version %q: %+v", aigc.ProseRenderCompatibilityProtocolVersion, report.Checks)
	}
	if !bytes.Equal(legacy, original) {
		t.Fatal("preflight mutated immutable historical render context bytes")
	}
}

func TestPipelinePreflightDoesNotRelaxMalformedContractOrNonV11Packet(t *testing.T) {
	for name, mutate := range map[string]func(map[string]any){
		"malformed v11 contract": func(packet map[string]any) {
			packet["anti_ai_render_contract"] = "invalid"
		},
		"negated before-draft phrase": func(packet map[string]any) {
			packet["anti_ai_render_contract"] = map[string]any{
				"usage_policy": "不在首稿前执行；章级优先。",
			}
		},
		"prohibited before-draft phrase": func(packet map[string]any) {
			packet["anti_ai_render_contract"] = map[string]any{
				"usage_policy": "首稿前不要执行；章级优先。",
			}
		},
		"missing v10 contract": func(packet map[string]any) {
			packet["version"] = 10
			delete(packet, "anti_ai_render_contract")
		},
		"missing v12 contract": func(packet map[string]any) {
			packet["version"] = 12
			delete(packet, "anti_ai_render_contract")
		},
	} {
		t.Run(name, func(t *testing.T) {
			bundle := pipelinePreflightTestBundle(t)
			var payload map[string]any
			if err := json.Unmarshal(bundle.RenderContext, &payload); err != nil {
				t.Fatal(err)
			}
			packet := payload["working_memory"].(map[string]any)["render_packet"].(map[string]any)
			mutate(packet)
			raw, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			report := compilePipelinePreflight(pipelinePreflightInput{
				Stage:         pipelinePreflightStageRender,
				Bundle:        bundle,
				RenderContext: raw,
			})
			if report.Ready || !pipelinePreflightTestHasBlocker(report, bundle.Chapter, "aigc.usage_policy_before_draft") {
				t.Fatalf("invalid/non-v11 packet was relaxed: %+v", report)
			}
			if pipelinePreflightTestCheckPassed(report, "aigc.compatibility_injection") {
				t.Fatalf("invalid/non-v11 packet received compatibility admission: %+v", report.Checks)
			}
		})
	}
}

func TestPipelinePreflightRejectsCompleteFuturePacketUntilProtocolExists(t *testing.T) {
	bundle := pipelinePreflightTestBundle(t)
	var payload map[string]any
	if err := json.Unmarshal(bundle.RenderContext, &payload); err != nil {
		t.Fatal(err)
	}
	packet := payload["working_memory"].(map[string]any)["render_packet"].(map[string]any)
	packet["version"] = 12
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	report := compilePipelinePreflight(pipelinePreflightInput{
		Stage: pipelinePreflightStageRender, Bundle: bundle, RenderContext: raw,
	})
	if report.Ready || !pipelinePreflightTestHasBlocker(report, bundle.Chapter, "aigc.render_packet_version") {
		t.Fatalf("complete future packet inherited v11 protocol: %+v", report)
	}
	if !pipelinePreflightTestCheckPassed(report, "aigc.render_contract_shape") ||
		!pipelinePreflightTestCheckPassed(report, "aigc.usage_policy_before_draft") {
		t.Fatalf("future-version failure was incorrectly attributed to contract shape: %+v", report.Checks)
	}
}

func TestPipelinePreflightAndPrimingShareRenderPacketLocations(t *testing.T) {
	bundle := pipelinePreflightTestBundle(t)
	var payload map[string]any
	if err := json.Unmarshal(bundle.RenderContext, &payload); err != nil {
		t.Fatal(err)
	}
	working := payload["working_memory"].(map[string]any)
	packet := working["render_packet"]
	delete(working, "render_packet")
	payload["episodic_memory"] = map[string]any{"render_packet": packet}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	report := compilePipelinePreflight(pipelinePreflightInput{
		Stage: pipelinePreflightStageRender, Bundle: bundle, RenderContext: raw,
	})
	if !report.Ready {
		t.Fatalf("shared supported packet location failed preflight: %+v", report.Blockers)
	}

	payload["selected_memory"] = map[string]any{"render_packet": packet}
	duplicated, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	report = compilePipelinePreflight(pipelinePreflightInput{
		Stage: pipelinePreflightStageRender, Bundle: bundle, RenderContext: duplicated,
	})
	if report.Ready || !pipelinePreflightTestHasBlocker(report, bundle.Chapter, "aigc.render_packet_present") {
		t.Fatalf("duplicate shared packet locations passed preflight: %+v", report)
	}
}

func TestPipelinePreflightRejectsCanonicalUsagePolicyWithoutCompleteContractShape(t *testing.T) {
	bundle := pipelinePreflightTestBundle(t)
	var payload map[string]any
	if err := json.Unmarshal(bundle.RenderContext, &payload); err != nil {
		t.Fatal(err)
	}
	packet := payload["working_memory"].(map[string]any)["render_packet"].(map[string]any)
	packet["anti_ai_render_contract"] = map[string]any{
		"usage_policy": string(aigc.ProseRenderUsagePolicyV1),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	report := compilePipelinePreflight(pipelinePreflightInput{
		Stage:         pipelinePreflightStageRender,
		Bundle:        bundle,
		RenderContext: raw,
	})
	if report.Ready || !pipelinePreflightTestHasBlocker(report, bundle.Chapter, "aigc.render_contract_shape") {
		t.Fatalf("usage-only contract passed complete-shape validation: %+v", report)
	}
	if !pipelinePreflightTestCheckPassed(report, "aigc.usage_policy_before_draft") ||
		pipelinePreflightTestCheckPassed(report, "aigc.compatibility_injection") {
		t.Fatalf("usage-only contract was misclassified as legacy omission: %+v", report.Checks)
	}
}

func TestPipelinePreflightAggregatesAndSortsIndependentBlockers(t *testing.T) {
	bundle := pipelinePreflightTestBundle(t)
	var payload map[string]any
	if err := json.Unmarshal(bundle.RenderContext, &payload); err != nil {
		t.Fatal(err)
	}
	payload["_context_profile"] = "planning"
	payload["padding"] = strings.Repeat("x", pipelinePreflightRenderContextHardMaxBytes)
	working := payload["working_memory"].(map[string]any)
	packet := working["render_packet"].(map[string]any)
	packet["version"] = 10
	packet["anti_ai_render_contract"] = map[string]any{"usage_policy": "写完后再检查"}
	sealed := payload["sealed_projection_contract"].(map[string]any)
	sealed["generation_id"] = "pg2_wrong"
	sealed["chapter"] = 99
	corrupt, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	bundle.RenderContext = corrupt

	report := compilePipelinePreflight(pipelinePreflightInput{
		Stage:  pipelinePreflightStagePromote,
		Bundle: bundle,
		Expected: &pipelinePreflightSealedIdentity{
			GenerationID: "pg2_expected_elsewhere",
			Chapter:      7,
			BundleDigest: domain.PlanningV2DigestPrefix + strings.Repeat("0", 64),
		},
	})
	if report.Ready || len(report.Blockers) < 8 {
		t.Fatalf("independent failures were not aggregated: ready=%v blockers=%+v", report.Ready, report.Blockers)
	}
	codes := make([]string, 0, len(report.Blockers))
	for _, blocker := range report.Blockers {
		codes = append(codes, blocker.Code)
	}
	for _, want := range []string{
		"aigc.render_packet_version",
		"aigc.usage_policy_before_draft",
		"bundle.domain_valid",
		"context.draft_profile",
		"context.size_within_limit",
		"sealed_identity.chapter",
		"sealed_identity.expected_bundle",
		"sealed_identity.expected_chapter",
		"sealed_identity.expected_generation",
		"sealed_identity.generation",
	} {
		if !slices.Contains(codes, want) {
			t.Fatalf("aggregate report lost blocker %q: %v", want, codes)
		}
	}
	sorted := append([]string(nil), codes...)
	sort.Strings(sorted)
	if !slices.Equal(codes, sorted) {
		t.Fatalf("blockers are not deterministically sorted:\n got=%v\nwant=%v", codes, sorted)
	}

	err = report.Require()
	if err == nil {
		t.Fatal("Require accepted blocked report")
	}
	var typed *PipelinePreflightError
	if !errors.As(err, &typed) {
		t.Fatalf("Require did not return typed PipelinePreflightError: %T %v", err, err)
	}
	if typed.Report.Ready || !slices.EqualFunc(typed.Report.Blockers, report.Blockers, func(a, b pipelinePreflightBlocker) bool {
		return a.Code == b.Code && a.Artifact == b.Artifact && a.Detail == b.Detail
	}) {
		t.Fatalf("typed error did not retain complete report: %+v", typed.Report)
	}
	for _, code := range codes {
		if !strings.Contains(err.Error(), "["+code+"]") {
			t.Fatalf("typed error summary omitted blocker %q: %v", code, err)
		}
	}
}

func TestPipelinePreflightAcceptsHistoricalSealedContextAboveBuilderTarget(t *testing.T) {
	bundle := pipelinePreflightTestBundle(t)
	renderContext := pipelinePreflightTestSizedRenderContext(t, bundle.RenderContext, 80*1024)
	if len(renderContext) <= 64*1024 || len(renderContext) > pipelinePreflightRenderContextHardMaxBytes {
		t.Fatalf("test context size=%d is not within the compatibility window", len(renderContext))
	}

	report := compilePipelinePreflight(pipelinePreflightInput{
		Stage:         pipelinePreflightStageRender,
		Bundle:        bundle,
		RenderContext: renderContext,
	})
	if !report.Ready || len(report.Blockers) != 0 {
		t.Fatalf("valid immutable 64-96 KiB render context was blocked: %+v", report.Blockers)
	}
	if !pipelinePreflightTestCheckPassed(report, "context.size_within_limit") {
		t.Fatalf("compatibility-sized context did not record a passing size check: %+v", report.Checks)
	}
}

func TestPipelinePreflightAggregatesContextAboveHistoricalHardCeiling(t *testing.T) {
	bundle := pipelinePreflightTestBundle(t)
	renderContext := pipelinePreflightTestSizedRenderContext(
		t,
		bundle.RenderContext,
		pipelinePreflightRenderContextHardMaxBytes+1024,
	)
	var payload map[string]any
	if err := json.Unmarshal(renderContext, &payload); err != nil {
		t.Fatal(err)
	}
	payload["_context_profile"] = "planning"
	renderContext, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(renderContext) <= pipelinePreflightRenderContextHardMaxBytes {
		t.Fatalf("test context size=%d did not exceed hard ceiling", len(renderContext))
	}

	report := compilePipelinePreflight(pipelinePreflightInput{
		Stage:         pipelinePreflightStageRender,
		Bundle:        bundle,
		RenderContext: renderContext,
	})
	if report.Ready ||
		!pipelinePreflightTestHasBlocker(report, bundle.Chapter, "context.size_within_limit") ||
		!pipelinePreflightTestHasBlocker(report, bundle.Chapter, "context.draft_profile") {
		t.Fatalf("hard-ceiling failure did not aggregate independent blockers: %+v", report.Blockers)
	}
}

func TestPipelinePreflightMalformedContextStillReturnsAllDependentBlockers(t *testing.T) {
	bundle := pipelinePreflightTestBundle(t)
	bundle.RenderContext = json.RawMessage(`{"_context_profile":`)
	report := compilePipelinePreflight(pipelinePreflightInput{
		Stage:  pipelinePreflightStageSeal,
		Bundle: bundle,
	})
	if report.Ready {
		t.Fatal("malformed render context passed preflight")
	}
	codes := make([]string, 0, len(report.Blockers))
	for _, blocker := range report.Blockers {
		codes = append(codes, blocker.Code)
	}
	for _, want := range []string{
		"bundle.domain_valid",
		"context.json_object",
		"context.draft_profile",
		"aigc.render_packet_present",
		"aigc.render_packet_version",
		"aigc.usage_policy_before_draft",
		"sealed_identity.contract_present",
	} {
		if !slices.Contains(codes, want) {
			t.Fatalf("malformed context report lost dependent blocker %q: %v", want, codes)
		}
	}
}

func pipelinePreflightTestBundle(t *testing.T) domain.ProjectedChapterBundle {
	t.Helper()
	generation, registry := projectAllCmdTestGenerationAndRegistry(t, 1)
	genesis, err := domain.DeriveProjectedChainGenesisV2(generation)
	if err != nil {
		t.Fatal(err)
	}
	artifacts, outline := projectAllCmdTestArtifacts(t, generation.GenerationID, 1)
	projectAllCmdTestBindPlanningContext(t, artifacts, generation, nil, registry, 1)
	artifacts.RenderContext, err = json.Marshal(map[string]any{
		"_context_profile": "draft",
		"working_memory": map[string]any{
			"render_packet": map[string]any{
				"version": 11,
				"chapter": 1,
				"anti_ai_render_contract": map[string]any{
					"risk_signals":           []string{"对白传送带与流程台账"},
					"counter_moves":          []string{"硬事实并场，选择承担后果"},
					"sentence_rhythm_policy": "句段随人物判断自然换挡。",
					"object_response_budget": "物件只在改变选择时回应。",
					"dialogue_function_plan": "对白只承担冲突或关系位移。",
					"review_checks":          []string{"没有逐项播报流程。"},
					"usage_policy":           string(aigc.ProseRenderUsagePolicyV1),
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle, _, err := buildPipelineProjectedChapterBundle(
		generation,
		outline,
		genesis,
		generation.BaseStateRoot,
		artifacts,
		registry,
	)
	if err != nil {
		t.Fatalf("build valid preflight bundle: %v", err)
	}
	return bundle
}

func pipelinePreflightTestCheckPassed(report pipelinePreflightReport, code string) bool {
	for _, check := range report.Checks {
		if check.Code == code && check.Passed {
			return true
		}
	}
	return false
}

func pipelinePreflightTestSizedRenderContext(
	t *testing.T,
	base json.RawMessage,
	targetBytes int,
) json.RawMessage {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(base, &payload); err != nil {
		t.Fatal(err)
	}
	payload["padding"] = ""
	empty, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if targetBytes <= len(empty) {
		t.Fatalf("target size=%d is not above base context=%d", targetBytes, len(empty))
	}
	payload["padding"] = strings.Repeat("x", targetBytes-len(empty))
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != targetBytes {
		t.Fatalf("sized render context=%d, want %d", len(raw), targetBytes)
	}
	return raw
}
