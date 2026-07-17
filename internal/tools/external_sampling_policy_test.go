package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func setExternalSamplingPolicyTestMarker(t *testing.T, st *store.Store, chapter int, hard bool) {
	t.Helper()
	detector := "zhuque"
	mode := "novel-whole-text-single-segment"
	if hard {
		detector = "automated-detector"
		mode = "whole"
	}
	if err := SetDraftExternalRerenderRequirement(st.Dir(), DraftExternalRerenderRequirement{
		Chapter:             chapter,
		EvaluatedBodySHA256: strings.Repeat("a", 64),
		Source:              "registered_external_detection",
		Evaluator:           draftExternalEvaluatorRegistered,
		RequiredDetector:    detector,
		RequiredMode:        mode,
		ExternalRetestPolicy: func() DraftExternalRetestPolicy {
			if hard {
				return DraftExternalRetestPolicyAutomatedHard
			}
			return DraftExternalRetestPolicySamplingOptional
		}(),
		BlockUntilExternalRetest: hard,
		PassExclusivePercent:     4,
		AdviceComplete:           true,
	}); err != nil {
		t.Fatal(err)
	}
}

func writeCorruptExternalSamplingLog(t *testing.T, st *store.Store) {
	t.Helper()
	path := filepath.Join(st.Dir(), "meta", "external_detection_log.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not-json}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setCurrentExternalSamplingReview(t *testing.T, st *store.Store, body string) {
	t.Helper()
	bodySHA := reviewreport.BodySHA256(body)
	if err := SetDraftExternalRerenderRequirement(st.Dir(), DraftExternalRerenderRequirement{
		Chapter:                1,
		EvaluatedBodySHA256:    bodySHA,
		InitialDraftBodySHA256: bodySHA,
		Source:                 "registered_external_detection",
		Evaluator:              draftExternalEvaluatorRegistered,
		RequiredDetector:       "zhuque",
		RequiredMode:           "whole",
		AIProbabilityPercent:   86,
		PassExclusivePercent:   4,
		Summary:                "zhuque/whole 抽查 86%，当前正文必须整章重渲染并以同 detector/mode 复测新哈希。",
		Evidence: []string{
			"registered_external_detection:zhuque/whole",
			"body_sha256:" + bodySHA,
			"registered_external_retest_required:zhuque/whole",
			"对白承压证据仍需保留",
		},
		RevisionPlan: []string{
			"保留金额，重做对白承压。",
			"新 SHA 必须用同一 detector/mode 复测，严格 <4% 才可提交。",
		},
		AdviceComplete: true,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestExternalSamplingPolicySanitizesLegacyPlanWithoutLosingRewriteEvidence(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	setExternalSamplingPolicyTestMarker(t, st, 1, false)
	optional, err := loadDraftExternalRerenderRequirement(st.Dir(), 1)
	if err != nil || optional == nil ||
		RequiresRegisteredExternalRetest(optional) ||
		!strings.Contains(strings.Join(RegisteredExternalRetestLabels(optional), ","), "zhuque/novel-whole-text-single-segment") {
		t.Fatalf("manual zhuque sampling marker became a blocking automated gate: requirement=%+v err=%v", optional, err)
	}
	plan := domain.ChapterPlan{Chapter: 1}
	loop := &plan.CausalSimulation.ReviewRefinement
	loop.FailureModes = []string{"朱雀抽查旧 SHA 结果 86%，该高分触发整章重写"}
	loop.LocalizedTargets = []string{"保留旧稿金额与结果，重做对白承压和主角选择链"}
	loop.AcceptanceChecks = []string{"改后朱雀同一 detector/mode 复测严格 <4% 才可提交"}
	loop.StopCondition = "所有 acceptance_checks 通过且外部 AIGC 复测采用同一 detector/mode 后才可提交；用户手工外部抽查不作为逐章阻塞项；若同一 failure_mode 第三次出现则改为局部 edit"

	sanitizeChapterPlanExternalSamplingPolicy(st, &plan)

	joined := strings.Join(append(append([]string{}, loop.FailureModes...), loop.LocalizedTargets...), "\n")
	if !strings.Contains(joined, "旧 SHA") || !strings.Contains(joined, "86%") || !strings.Contains(joined, "对白承压") {
		t.Fatalf("sampling migration lost high-score provenance or prose repair advice: %+v", loop)
	}
	if strings.Contains(strings.Join(loop.AcceptanceChecks, "\n"), "同一 detector/mode") ||
		strings.Contains(loop.StopCondition, "外部 AIGC 复测") {
		t.Fatalf("legacy human retest still blocks the plan: %+v", loop)
	}
	if !strings.Contains(strings.Join(loop.AcceptanceChecks, "\n"), "同哈希 DeepSeek") ||
		!strings.Contains(loop.StopCondition, "用户手工外部抽查不作为逐章阻塞项") ||
		!strings.Contains(loop.StopCondition, "第三次出现") {
		t.Fatalf("automated release contract or retry policy missing: %+v", loop)
	}
}

func TestExternalSamplingPolicyLeavesExplicitAutomatedHardPlanUntouched(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	setExternalSamplingPolicyTestMarker(t, st, 1, true)
	automated, err := loadDraftExternalRerenderRequirement(st.Dir(), 1)
	if err != nil || automated == nil ||
		!RequiresRegisteredExternalRetest(automated) ||
		!strings.Contains(strings.Join(RegisteredExternalRetestLabels(automated), ","), "automated-detector/whole") ||
		strings.Contains(strings.Join(RegisteredExternalRetestLabels(automated), ","), "zhuque/") {
		t.Fatalf("automated_hard marker did not stay separate from manual zhuque sampling: requirement=%+v err=%v", automated, err)
	}
	plan := domain.ChapterPlan{Chapter: 1}
	plan.CausalSimulation.ReviewRefinement = domain.ReviewRefinementLoop{
		AcceptanceChecks: []string{
			"新 SHA 必须用 automated-detector/whole 做同哈希复测并严格 <4%",
			"显式自动检测服务严格 <4% 才可提交",
		},
		StopCondition: "显式 automated_hard 门禁合格后方可交付",
	}
	before := plan.CausalSimulation.ReviewRefinement

	sanitizeChapterPlanExternalSamplingPolicy(st, &plan)

	if !reflect.DeepEqual(before, plan.CausalSimulation.ReviewRefinement) {
		t.Fatalf("explicit automated hard policy was rewritten: before=%+v after=%+v", before, plan.CausalSimulation.ReviewRefinement)
	}
}

func TestExternalSamplingPolicySanitizesNoRetestReleaseClausesInContext(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	setExternalSamplingPolicyTestMarker(t, st, 1, false)
	result := map[string]any{
		"working_memory": map[string]any{
			"rewrite_source": map[string]any{
				"brief_markdown": strings.Join([]string{
					"# 返工单",
					"- 当前 SHA 朱雀抽查 86%，已触发整章重写。",
					"- 新 SHA 朱雀严格 <4% 才可提交。",
					"- 同哈希 DeepSeek 严格 <4% 才可提交。",
					"- 保留金额与对白承压证据。",
				}, "\n"),
			},
			"rewrite_brief": map[string]any{
				"acceptance_checks": []any{"外部平台合格后方可交付"},
			},
		},
	}

	sanitizeExternalSamplingPolicyContext(st, 1, result)

	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	payload := strings.ReplaceAll(string(raw), `\u003c`, "<")
	for _, retired := range []string{
		"新 SHA 朱雀严格 <4% 才可提交",
		"外部平台合格后方可交付",
	} {
		if strings.Contains(payload, retired) {
			t.Fatalf("context leaked no-retest external release gate %q: %s", retired, payload)
		}
	}
	for _, retained := range []string{
		"当前 SHA 朱雀抽查 86%",
		"已触发整章重写",
		"同哈希 DeepSeek 严格 <4% 才可提交",
		"保留金额与对白承压证据",
		"用户手工外部抽查不作为逐章阻塞项",
	} {
		if !strings.Contains(payload, retained) {
			t.Fatalf("context lost required evidence or automated gate %q: %s", retained, payload)
		}
	}
}

func TestExternalSamplingPolicyStillSanitizesAfterLocalGateReplacesMarkerSource(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := SetDraftExternalRerenderRequirement(st.Dir(), DraftExternalRerenderRequirement{
		Chapter:                  1,
		EvaluatedBodySHA256:      strings.Repeat("b", 64),
		Source:                   "local_mechanical_gate",
		AIProbabilityPercent:     8,
		PassExclusivePercent:     4,
		BlockUntilExternalRetest: false,
		AdviceComplete:           true,
	}); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{Chapter: 1}
	plan.CausalSimulation.ReviewRefinement.StopCondition = "同一 detector/mode 外部 AIGC 复测通过后才可提交"

	sanitizeChapterPlanExternalSamplingPolicy(st, &plan)

	got := plan.CausalSimulation.ReviewRefinement.StopCondition
	if strings.Contains(got, "外部 AIGC 复测") || !strings.Contains(got, "同哈希 DeepSeek") {
		t.Fatalf("local gate marker source allowed legacy human retest to leak: %q", got)
	}
}

func TestNormalizeRewriteBriefRefinementMigratesLegacySamplingContract(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("sampling brief", 2); err != nil {
		t.Fatal(err)
	}
	prepareRewriteSourceTest(t, st,
		"第一章\n\n旧稿金额与结果已经成立。",
		"# brief\n\n## 必须修正\n\n- 保留金额，重做对白承压。\n\n## 最新整篇单段门禁\n\n- zhuque/whole 当前旧 SHA 为 86%，新 SHA 必须复测。\n\n## 验收条件\n\n- 改后外部 AIGC 复测采用同一 detector/mode 严格低于 4% 才可提交。\n",
	)
	setExternalSamplingPolicyTestMarker(t, st, 1, false)
	plan := domain.ChapterPlan{Chapter: 1}
	plan.CausalSimulation.ReviewRefinement.StopCondition = "外部 AIGC 复测采用同一 detector/mode 后通过；第三次失败改为局部 edit"

	normalizeRewriteBriefRefinement(st, &plan)

	loop := plan.CausalSimulation.ReviewRefinement
	targets := strings.Join(loop.LocalizedTargets, "\n")
	checks := strings.Join(loop.AcceptanceChecks, "\n")
	if !strings.Contains(targets, "旧 SHA 为 86%") || !strings.Contains(targets, "重做对白承压") {
		t.Fatalf("brief evidence or repair target was lost: %+v", loop)
	}
	for _, retired := range []string{"新 SHA 必须复测", "同一 detector/mode 严格低于 4%", "外部 AIGC 复测采用"} {
		if strings.Contains(targets+"\n"+checks+"\n"+loop.StopCondition, retired) {
			t.Fatalf("retired sampling gate %q survived plan projection: %+v", retired, loop)
		}
	}
	if !strings.Contains(targets+"\n"+checks+"\n"+loop.StopCondition, "同哈希 DeepSeek") {
		t.Fatalf("automated release contract missing after projection: %+v", loop)
	}
}

func TestNovelContextSanitizesPersistedPlanAndBriefBeforePlanningMerge(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("sampling context", 2); err != nil {
		t.Fatal(err)
	}
	const body = "第一章\n\n旧稿金额与结果已经成立。"
	prepareRewriteSourceTest(t, st,
		body,
		"# brief\n\n## 必须修正\n\n- 保留金额，重做对白承压。\n\n## 最新整篇单段门禁\n\n- zhuque/whole 当前旧 SHA 为 86%，新 SHA 必须复测。\n\n## 验收条件\n\n- 外部 AIGC 同一 detector/mode 复测通过后才可提交。\n",
	)
	plan := domain.ChapterPlan{Chapter: 1, Title: "旧持久化计划"}
	plan.CausalSimulation.ReviewRefinement.StopCondition = "所有 acceptance_checks 通过且外部 AIGC 同一 detector/mode 复测后通过"
	if err := st.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	setCurrentExternalSamplingReview(t, st, body)

	raw, err := NewContextTool(st, References{}, "default").Execute(
		context.Background(), json.RawMessage(`{"chapter":1,"profile":"planning"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	payload := strings.ReplaceAll(string(raw), `\u003c`, "<")
	for _, retired := range []string{
		"新 SHA 必须复测",
		"外部 AIGC 同一 detector/mode 复测通过后才可提交",
		"复测后通过",
		"同 detector/mode 复测新哈希",
		"registered_external_retest_required",
		"严格 <4% 才可提交",
	} {
		if strings.Contains(payload, retired) {
			t.Fatalf("novel_context leaked retired sampling gate %q:\n%s", retired, payload)
		}
	}
	for _, retained := range []string{
		"当前旧 SHA 为 86%",
		"重做对白承压",
		"同哈希 DeepSeek",
		"用户手工外部抽查不作为逐章阻塞项",
		"zhuque/whole 抽查 86%",
		"当前精确 SHA 的高分已触发一次整章重渲染",
		"registered_external_sampling_trigger:zhuque/whole",
		"对白承压证据仍需保留",
	} {
		if !strings.Contains(payload, retained) {
			t.Fatalf("novel_context lost current contract %q:\n%s", retained, payload)
		}
	}
}

func TestNovelContextDraftProfileConsumesOnlyFrozenPlanNotLiveSamplingReview(t *testing.T) {
	st := newRewriteCraftTestStore(t, true)
	receipt, err := ensureRewriteCraftReceipt(st, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveChapterPlan(receiptBackedCraftPlan(receipt)); err != nil {
		t.Fatal(err)
	}
	body, err := st.Drafts.LoadChapterText(1)
	if err != nil {
		t.Fatal(err)
	}
	setCurrentExternalSamplingReview(t, st, body)

	raw, err := NewContextTool(st, References{}, "default").Execute(
		context.Background(), json.RawMessage(`{"chapter":1,"profile":"draft"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	payload := string(raw)
	for _, retired := range []string{
		"同 detector/mode 复测新哈希",
		"registered_external_retest_required",
		"严格 <4% 才可提交",
	} {
		if strings.Contains(payload, retired) {
			t.Fatalf("draft novel_context leaked retired sampling gate %q:\n%s", retired, payload)
		}
	}
	for _, liveOverlay := range []string{
		"zhuque/whole 抽查 86%",
		"registered_external_sampling_trigger:zhuque/whole",
	} {
		if strings.Contains(payload, liveOverlay) {
			t.Fatalf("draft novel_context retained post-freeze live overlay %q:\n%s", liveOverlay, payload)
		}
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, liveKey := range []string{"draft_external_ai_review", "rewrite_brief"} {
		if hasContextKey(decoded, liveKey) {
			t.Fatalf("draft novel_context retained post-freeze live key %q:\n%s", liveKey, payload)
		}
	}
	if !strings.Contains(payload, `"render_packet"`) || !strings.Contains(payload, `"formal_plan_receipt"`) {
		t.Fatalf("draft novel_context lost frozen plan authorities:\n%s", payload)
	}
}

func TestExternalSamplingPolicySanitizesDraftExternalReviewReceipt(t *testing.T) {
	review := map[string]any{
		"source":   "registered_external_detection",
		"blocking": true,
		"summary":  "zhuque/whole 抽查 86%，当前正文必须整章重渲染并以同 detector/mode 复测新哈希。",
		"evidence": []any{
			"registered_external_detection:zhuque/whole",
			"registered_external_retest_required:zhuque/whole",
			"对白承压证据仍需保留",
		},
		"revision_plan": []any{
			"保留金额，重做对白承压。",
			"新 SHA 必须用同一 detector/mode 复测，严格 <4% 才可提交。",
		},
	}
	container := map[string]any{"draft_external_ai_review": review}

	sanitizeExternalSamplingContextContainer(container)

	raw, err := json.Marshal(container)
	if err != nil {
		t.Fatal(err)
	}
	payload := string(raw)
	for _, retired := range []string{
		"同 detector/mode 复测新哈希",
		"registered_external_retest_required",
		"严格 <4% 才可提交",
	} {
		if strings.Contains(payload, retired) {
			t.Fatalf("draft external review leaked retired sampling gate %q: %s", retired, payload)
		}
	}
	for _, retained := range []string{
		"zhuque/whole 抽查 86%",
		"当前精确 SHA 的高分已触发一次整章重渲染",
		"registered_external_sampling_trigger:zhuque/whole",
		"对白承压证据仍需保留",
		"同哈希 DeepSeek",
	} {
		if !strings.Contains(payload, retained) {
			t.Fatalf("draft external review lost current sampling contract %q: %s", retained, payload)
		}
	}
}

func TestExternalSamplingPolicySanitizesNoRetestReleaseClausesInReceipt(t *testing.T) {
	review := map[string]any{
		"source":   "registered_external_detection",
		"blocking": true,
		"summary":  "朱雀/whole 当前 SHA 抽查 86%，该高分已触发整章重写；新 SHA 朱雀严格 <4% 才可提交。",
		"evidence": []any{
			"registered_external_detection:zhuque/whole",
			"当前 SHA 得分 86%",
			"对白承压证据仍需保留",
		},
		"revision_plan": []any{
			"外部平台合格后方可交付。",
			"同哈希 DeepSeek 严格 <4% 才可提交。",
		},
	}
	container := map[string]any{"draft_external_ai_review": review}

	sanitizeExternalSamplingContextContainer(container)

	raw, err := json.Marshal(container)
	if err != nil {
		t.Fatal(err)
	}
	payload := strings.ReplaceAll(string(raw), `\u003c`, "<")
	for _, retired := range []string{
		"新 SHA 朱雀严格 <4% 才可提交",
		"外部平台合格后方可交付",
	} {
		if strings.Contains(payload, retired) {
			t.Fatalf("receipt leaked no-retest external release gate %q: %s", retired, payload)
		}
	}
	for _, retained := range []string{
		"朱雀/whole 当前 SHA 抽查 86%",
		"已触发整章重写",
		"当前 SHA 得分 86%",
		"对白承压证据仍需保留",
		"同哈希 DeepSeek 严格 <4% 才可提交",
		"用户手工外部抽查不作为逐章阻塞项",
	} {
		if !strings.Contains(payload, retained) {
			t.Fatalf("receipt lost required evidence or automated gate %q: %s", retained, payload)
		}
	}
}

func TestSamplingDetectionLogReadErrorDoesNotBlockPlanOrBrief(t *testing.T) {
	t.Run("sampling optional", func(t *testing.T) {
		st := store.NewStore(t.TempDir())
		if err := st.Init(); err != nil {
			t.Fatal(err)
		}
		if err := st.Progress.Init("sampling log", 2); err != nil {
			t.Fatal(err)
		}
		const body = "第一章\n\n当前正文。"
		if err := st.Drafts.SaveFinalChapter(1, body); err != nil {
			t.Fatal(err)
		}
		setExternalSamplingPolicyTestMarker(t, st, 1, false)
		writeCorruptExternalSamplingLog(t, st)

		required, reason, err := chapterRequiresFocusedAntiAIExecutionPlan(st, 1, true)
		if err != nil || required {
			t.Fatalf("corrupt sampling log blocked planning: required=%v reason=%q err=%v", required, reason, err)
		}
		path, err := refreshRewriteBriefFromReview(st, domain.ReviewEntry{
			Chapter: 1, Scope: "chapter", Verdict: "rewrite", Summary: "重做对白承压。",
		}, "rewrite")
		if err != nil || path == "" {
			t.Fatalf("corrupt sampling log blocked brief refresh: path=%q err=%v", path, err)
		}
	})

	t.Run("explicit automated hard", func(t *testing.T) {
		st := store.NewStore(t.TempDir())
		if err := st.Init(); err != nil {
			t.Fatal(err)
		}
		if err := st.Progress.Init("hard external log", 2); err != nil {
			t.Fatal(err)
		}
		if err := st.Drafts.SaveFinalChapter(1, "第一章\n\n当前正文。"); err != nil {
			t.Fatal(err)
		}
		setExternalSamplingPolicyTestMarker(t, st, 1, true)
		writeCorruptExternalSamplingLog(t, st)

		required, reason, err := chapterRequiresFocusedAntiAIExecutionPlan(st, 1, true)
		if err != nil || !required || !strings.Contains(reason, "显式自动外部复测合同") {
			t.Fatalf("explicit hard plan did not fail closed: required=%v reason=%q err=%v", required, reason, err)
		}
		if _, err := refreshRewriteBriefFromReview(st, domain.ReviewEntry{
			Chapter: 1, Scope: "chapter", Verdict: "rewrite", Summary: "重做对白承压。",
		}, "rewrite"); err == nil || !strings.Contains(err.Error(), "load current registered external detections") {
			t.Fatalf("explicit hard brief refresh must fail closed on corrupt log: %v", err)
		}
	})
}
