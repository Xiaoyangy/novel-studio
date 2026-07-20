package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/host/flow"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

func TestPipelineStaleCausalCandidateSkipsJudgeAndDispatchesWorldResimulation(t *testing.T) {
	st := newPipelineCausalPreflightFixture(t)
	if err := tools.ValidateReusableCausalPlanForRerender(st, 1); err != nil {
		t.Fatalf("baseline fixture must be a judgeable current causal candidate: %v", err)
	}
	judgeCalls := 0
	judged, err := pipelineJudgePendingDraftHashWithJudge(
		cliOptions{},
		st.Dir(),
		&domain.Progress{PendingRewrites: []int{1}},
		func(cliOptions, []string) error {
			judgeCalls++
			return os.ErrDeadlineExceeded
		},
	)
	if !judged || err == nil || judgeCalls != 1 {
		t.Fatalf("fresh current causal candidate did not reach judge probe: judged=%v calls=%d err=%v", judged, judgeCalls, err)
	}
	oldDraft, err := st.Drafts.LoadDraft(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := tools.SetRegisteredExternalRerenderRequirement(st.Dir(), reviewreport.RegisteredExternalDetection{
		Chapter: 1, Detector: "zhuque", Mode: "novel-whole-text-single-segment",
		BodySHA256: reviewreport.BodySHA256(oldDraft), NormalizedScorePercent: 86,
	}); err != nil {
		t.Fatal(err)
	}

	// The retained draft hash itself does not change, but every causal input
	// that matters to a rewrite does: canonical result ledger, preserve-fact
	// brief and the live pipeline instruction. This is the production failure
	// mode that used to send obsolete bytes to DeepSeek first.
	writePipelinePreflightLedger(t, st, "仍未完成真实营业")
	mustWriteFile(t, filepath.Join(st.Dir(), "reviews", "01_rewrite_brief.md"), "# rewrite brief\n\n## 保留事实\n\n- 安全回撤在检查者到场前已经完成。\n- 首次结算必须晚于普通顾客真实受益。\n")
	writePipelinePreflightPrompt(t, st, "新增硬合同：普通顾客受益、自纠完成、检查者离场后，林澈才主动查看结算。")

	judged, err = pipelineJudgePendingDraftHashWithJudge(
		cliOptions{},
		st.Dir(),
		&domain.Progress{PendingRewrites: []int{1}},
		func(cliOptions, []string) error {
			judgeCalls++
			return nil
		},
	)
	if err != nil || judged || judgeCalls != 1 {
		t.Fatalf("stale causal candidate reached judge: judged=%v calls=%d err=%v", judged, judgeCalls, err)
	}
	for _, rel := range []string{
		"drafts/01.draft.md",
		"drafts/01.plan.json",
		"meta/chapter_simulations/001.json",
	} {
		if _, err := os.Stat(filepath.Join(st.Dir(), filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Fatalf("stale artifact was not quarantined: %s err=%v", rel, err)
		}
	}

	manifests, err := filepath.Glob(filepath.Join(st.Dir(), "meta", "quarantine", "causal_preflight", "ch001", "*", "manifest.json"))
	if err != nil || len(manifests) != 1 {
		t.Fatalf("causal quarantine manifest missing: matches=%v err=%v", manifests, err)
	}
	var manifest pipelineCausalQuarantineManifest
	raw, err := os.ReadFile(manifests[0])
	if err != nil || json.Unmarshal(raw, &manifest) != nil {
		t.Fatalf("read causal quarantine manifest: err=%v body=%s", err, raw)
	}
	if !manifest.PlanInvalidated || !manifest.WorldInvalidated || manifest.DraftBodySHA256 == "" ||
		!strings.Contains(manifest.Reason, "canonical chapter_progress") ||
		!strings.Contains(manifest.Reason, "chapter_pipeline_instruction") {
		t.Fatalf("quarantine did not record exact stale causal evidence: %+v", manifest)
	}

	// Sampling provenance is deliberately outside the quarantine set. It must
	// survive as the rewrite reason without becoming a replacement-hash retest
	// obligation.
	requirementRaw, err := os.ReadFile(filepath.Join(st.Dir(), "reviews", "drafts", "01_full_rerender_required.json"))
	var requirement tools.DraftExternalRerenderRequirement
	if err != nil || json.Unmarshal(requirementRaw, &requirement) != nil || tools.RequiresRegisteredExternalRetest(&requirement) ||
		!strings.Contains(strings.Join(tools.RegisteredExternalRetestLabels(&requirement), ","), "zhuque/novel-whole-text-single-segment") {
		t.Fatalf("sampling trigger provenance was lost or became a hard obligation: requirement=%+v err=%v body=%s", requirement, err, requirementRaw)
	}

	// A retained external-rejudge requirement must not mask the now-missing
	// causal artifacts. The next Host instruction is world simulation, not a
	// judge pause and not prose generation.
	state := flow.LoadState(store.NewStore(st.Dir()))
	next := flow.Route(state)
	if next == nil || next.Agent != "world_simulator" || next.Chapter != 1 ||
		!strings.Contains(next.Task, "simulate_chapter_world") {
		t.Fatalf("stale candidate did not dispatch fresh world simulation: state=%+v next=%+v", state, next)
	}
}

func TestRenderOnlyCandidateJudgesRetainedExactDraftBeforeNextHostTurn(t *testing.T) {
	st := newPipelineCausalPreflightFixture(t)
	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		t.Fatalf("load candidate progress: progress=%+v err=%v", progress, err)
	}
	before, err := st.Drafts.LoadDraft(1)
	if err != nil || strings.TrimSpace(before) == "" {
		t.Fatalf("fixture has no retained exact-body draft: err=%v body=%q", err, before)
	}
	beforeCheckpoint, err := tools.CurrentChapterBodyCheckpoint(st, 1)
	if err != nil {
		t.Fatalf("fixture draft is not checkpoint-bound: %v", err)
	}
	resumable, reason, err := pipelineRenderOnlyCandidateResumeStatus(st, 1)
	if err != nil || !resumable || strings.TrimSpace(reason) == "" {
		t.Fatalf("managed render candidate was not resumable: resumable=%v reason=%q err=%v", resumable, reason, err)
	}

	judgeCalls := 0
	var judgeArgs []string
	var judgeDir string
	judged, err := pipelineJudgePendingRenderOnlyDraftHashWithJudge(
		cliOptions{},
		st.Dir(),
		progress,
		func(opts cliOptions, args []string) error {
			judgeCalls++
			judgeDir = opts.Dir
			judgeArgs = append([]string(nil), args...)
			return os.ErrDeadlineExceeded
		},
	)
	if !judged || err == nil || judgeCalls != 1 {
		t.Fatalf("retained candidate did not reach its exact-body judge: judged=%v calls=%d err=%v", judged, judgeCalls, err)
	}
	if judgeDir != st.Dir() || !slices.Contains(judgeArgs, "--no-sediment") ||
		!slices.Contains(judgeArgs, "--primary-only") ||
		!slices.Contains(judgeArgs, "--chapter") || !slices.Contains(judgeArgs, "1") ||
		!strings.Contains(strings.Join(judgeArgs, " "), "--budget 3m0s") {
		t.Fatalf("candidate judge lost exact output/chapter isolation: dir=%q args=%v", judgeDir, judgeArgs)
	}
	after, err := st.Drafts.LoadDraft(1)
	if err != nil || after != before {
		t.Fatalf("judge boundary regenerated or discarded the candidate draft: err=%v before=%q after=%q", err, before, after)
	}
	afterCheckpoint, err := tools.CurrentChapterBodyCheckpoint(st, 1)
	if err != nil || afterCheckpoint.Seq != beforeCheckpoint.Seq || afterCheckpoint.Digest != beforeCheckpoint.Digest {
		t.Fatalf("judge boundary changed the exact-body epoch: before=%+v after=%+v err=%v", beforeCheckpoint, afterCheckpoint, err)
	}
}

func TestPipelineCausalPreflightKeepsPlanPreservingFormalReviewSeed(t *testing.T) {
	st := newPipelineCausalPreflightFixture(t)
	sim, err := st.LoadChapterWorldSimulation(1)
	if err != nil || sim == nil {
		t.Fatalf("load simulation: sim=%+v err=%v", sim, err)
	}
	// Match the sealed first-render case: the approved simulation predates any
	// rewrite_source.  The formal review arrived only after commit.
	sim.RewriteSource = nil
	sim.RewriteFactCoverage = nil
	if err := st.SaveChapterWorldSimulation(*sim); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(
		domain.ChapterScope(1), "chapter_world_simulation", "meta/chapter_simulations/001.json",
	); err != nil {
		t.Fatal(err)
	}
	plan, err := st.Drafts.LoadChapterPlan(1)
	if err != nil || plan == nil {
		t.Fatalf("load plan: plan=%+v err=%v", plan, err)
	}
	plan.Goal = "保留同一因果计划，只处理正式评审指出的表达问题"
	if err := st.Drafts.SaveChapterPlan(*plan); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(
		domain.ChapterScope(1), "plan", "drafts/01.plan.json",
	); err != nil {
		t.Fatal(err)
	}
	finalBody, err := st.Drafts.LoadChapterText(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveDraft(1, finalBody); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(
		domain.ChapterScope(1), "draft", "drafts/01.draft.md",
	); err != nil {
		t.Fatal(err)
	}
	bodySHA := reviewreport.BodySHA256(finalBody)
	review := domain.ReviewEntry{
		Chapter: 1, Scope: "chapter", BodySHA256: bodySHA, Verdict: "rewrite", ContractStatus: "met",
		Summary: "删掉清单感并补主角判断。",
		Issues:  []domain.ConsistencyIssue{{Type: "aesthetic", Severity: "warning", Description: "catalog stuffing"}},
		Dimensions: []domain.DimensionScore{
			{Dimension: "consistency", Verdict: "pass"},
			{Dimension: "character", Verdict: "pass"},
			{Dimension: "pacing", Verdict: "pass"},
			{Dimension: "continuity", Verdict: "pass"},
			{Dimension: "foreshadow", Verdict: "pass"},
			{Dimension: "hook", Verdict: "pass"},
		},
	}
	if err := st.World.SaveReview(review); err != nil {
		t.Fatal(err)
	}
	gateRaw, err := json.Marshal(reviewreport.MechanicalGatePayload{
		Chapter: 1, BodySHA256: bodySHA,
		RuleViolations: []rules.Violation{{Rule: "pov_interiority_thin", Severity: rules.SeverityWarning}},
	})
	if err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(st.Dir(), "reviews", "01_ai_gate.json"), string(gateRaw))
	if err := tools.ValidateReusableCausalPlanForRerender(st, 1); err == nil ||
		!strings.Contains(err.Error(), "world simulation 缺少当前 rewrite source") {
		t.Fatalf("fixture did not reproduce mutable rewrite-source mismatch: %v", err)
	}

	preflight, err := pipelinePreflightManagedDraftCausal(st, 1, true)
	if err != nil || preflight.Invalidated {
		t.Fatalf("plan-preserving exact formal review seed was quarantined: %+v err=%v", preflight, err)
	}
	if _, err := tools.CurrentChapterPlanCausalCheckpoint(st, 1); err != nil {
		t.Fatalf("preflight lost exact sealed plan: %v", err)
	}
	if _, err := tools.CurrentChapterBodyCheckpoint(st, 1); err != nil {
		t.Fatalf("preflight lost exact rejected body checkpoint: %v", err)
	}
	judgeCalls := 0
	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		t.Fatalf("load semantic seed progress: %+v err=%v", progress, err)
	}
	judged, err := pipelineJudgePendingDraftHashWithJudge(
		cliOptions{}, st.Dir(), progress,
		func(cliOptions, []string) error { judgeCalls++; return nil },
	)
	if err != nil || judged || judgeCalls != 0 {
		t.Fatalf("fresh formal review seed reached static/provider rejudge instead of Host rerender: judged=%v calls=%d err=%v", judged, judgeCalls, err)
	}
	if got, err := st.Drafts.LoadDraft(1); err != nil || got != finalBody {
		t.Fatalf("fresh formal review seed was removed before Host rerender: body=%q err=%v", got, err)
	}

	review.Dimensions[1].Verdict = "warning"
	if err := st.World.SaveReview(review); err != nil {
		t.Fatal(err)
	}
	preflight, err = pipelinePreflightManagedDraftCausal(st, 1, true)
	if err != nil || !preflight.Invalidated {
		t.Fatalf("character-failing review bypassed causal quarantine: %+v err=%v", preflight, err)
	}
}

func TestRetainedExternalRejudgeDoesNotMaskStaleRewriteSimulation(t *testing.T) {
	next := flow.Route(flow.State{
		Progress: &domain.Progress{
			Phase: domain.PhaseWriting, Flow: domain.FlowRewriting,
			PendingRewrites: []int{1},
		},
		NextActionPlanReady:                   false,
		NextActionWorldSimulationRequired:     true,
		NextActionWorldSimulationReady:        false,
		NextActionWorldSimulationGaps:         []string{"rewrite source stale"},
		NextActionDraftExternalRejudgePending: true,
	})
	if next == nil || next.Agent != "world_simulator" || next.Chapter != 1 {
		t.Fatalf("registered rejudge masked stale causal inputs: %+v", next)
	}
}

func TestPipelineOrdinaryNextChapterStaleBodyEpochNeverReachesJudge(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	progress := &domain.Progress{NovelName: "ordinary-preflight", Phase: domain.PhaseWriting, Flow: domain.FlowWriting, CurrentChapter: 1, TotalChapters: 2}
	if err := st.Progress.Save(progress); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{Chapter: 1, Title: "晨雾进城"}
	if err := st.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "plan", "drafts/01.plan.json"); err != nil {
		t.Fatal(err)
	}
	body := "第一章 晨雾进城\n\n许闻溪带着旧包走进城门。"
	if err := st.Drafts.SaveDraft(1, body); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "draft", "drafts/01.draft.md"); err != nil {
		t.Fatal(err)
	}
	// A formal plan mutation without its matching finalize checkpoint makes the
	// plan itself stale. The retained body is not a judgeable candidate even
	// though it still exists and has its own exact draft checkpoint.
	plan.Hook = "新计划改为先去桥头"
	if err := st.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	if err := tools.SetRegisteredExternalRerenderRequirement(st.Dir(), reviewreport.RegisteredExternalDetection{
		Chapter: 1, Detector: "zhuque", Mode: "novel-whole-text-single-segment",
		BodySHA256: reviewreport.BodySHA256(body), NormalizedScorePercent: 86,
	}); err != nil {
		t.Fatal(err)
	}

	judgeCalls := 0
	judged, err := pipelineJudgePendingDraftHashWithJudge(
		cliOptions{}, st.Dir(), progress,
		func(cliOptions, []string) error { judgeCalls++; return nil },
	)
	if err != nil || judged || judgeCalls != 0 {
		t.Fatalf("ordinary stale body epoch reached judge: judged=%v calls=%d err=%v", judged, judgeCalls, err)
	}
	if _, err := os.Stat(filepath.Join(st.Dir(), "drafts", "01.draft.md")); !os.IsNotExist(err) {
		t.Fatalf("ordinary stale draft was not quarantined: %v", err)
	}
	if _, err := os.Stat(filepath.Join(st.Dir(), "drafts", "01.plan.json")); !os.IsNotExist(err) {
		t.Fatalf("stale formal plan was not quarantined for replanning: %v", err)
	}
	requirementRaw, err := os.ReadFile(filepath.Join(st.Dir(), "reviews", "drafts", "01_full_rerender_required.json"))
	var requirement tools.DraftExternalRerenderRequirement
	if err != nil || json.Unmarshal(requirementRaw, &requirement) != nil ||
		tools.RequiresRegisteredExternalRetest(&requirement) || requirement.Source != "registered_external_detection" ||
		!strings.Contains(strings.Join(tools.RegisteredExternalRetestLabels(&requirement), ","), "zhuque/novel-whole-text-single-segment") {
		t.Fatalf("ordinary preflight lost sampling trigger provenance or created a hard requirement: requirement=%+v err=%v body=%s", requirement, err, requirementRaw)
	}
	next := flow.Route(flow.LoadState(st))
	if next == nil || next.Agent != "writer" || next.Chapter != 1 {
		t.Fatalf("ordinary stale causal plan did not route to replanning: %+v", next)
	}
}

func TestPipelineStaticBodyPreflightBlocksHardFactsTitleAndWordsBeforeJudge(t *testing.T) {
	st := newPipelineCausalPreflightFixture(t)
	plan, err := st.Drafts.LoadChapterPlan(1)
	if err != nil || plan == nil {
		t.Fatalf("load fixture plan: plan=%+v err=%v", plan, err)
	}
	plan.CausalSimulation.ReviewRefinement.PreserveConstraints = append(
		plan.CausalSimulation.ReviewRefinement.PreserveConstraints,
		"回头客的两碗里必须有一碗少糖。",
	)
	if err := st.Drafts.SaveChapterPlan(*plan); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "plan", "drafts/01.plan.json"); err != nil {
		t.Fatal(err)
	}
	if err := st.UserRules.Save(&rules.Snapshot{Structured: rules.Structured{
		ChapterWords: &rules.WordRange{Min: 200, Max: 300},
	}}); err != nil {
		t.Fatal(err)
	}
	body := "第一章 错误标题\n\n林澈收回线盘，试点继续营业。"
	if err := st.Drafts.SaveDraft(1, body); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "draft", "drafts/01.draft.md"); err != nil {
		t.Fatal(err)
	}
	if err := tools.SetRegisteredExternalRerenderRequirement(st.Dir(), reviewreport.RegisteredExternalDetection{
		Chapter: 1, Detector: "zhuque", Mode: "novel-whole-text-single-segment",
		BodySHA256: reviewreport.BodySHA256(body), NormalizedScorePercent: 86,
	}); err != nil {
		t.Fatal(err)
	}

	judgeCalls := 0
	judged, err := pipelineJudgePendingDraftHashWithJudge(
		cliOptions{}, st.Dir(), &domain.Progress{PendingRewrites: []int{1}},
		func(cliOptions, []string) error { judgeCalls++; return nil },
	)
	if err != nil || judged || judgeCalls != 0 {
		t.Fatalf("static-invalid candidate reached judge: judged=%v calls=%d err=%v", judged, judgeCalls, err)
	}
	manifests, err := filepath.Glob(filepath.Join(st.Dir(), "meta", "quarantine", "causal_preflight", "ch001", "*", "manifest.json"))
	if err != nil || len(manifests) != 1 {
		t.Fatalf("static quarantine manifest missing: matches=%v err=%v", manifests, err)
	}
	raw, err := os.ReadFile(manifests[0])
	if err != nil {
		t.Fatal(err)
	}
	var manifest pipelineCausalQuarantineManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"hard-fact anchors", "chapter title mismatch", "chapter word count out of range"} {
		if !strings.Contains(manifest.Reason, want) {
			t.Fatalf("static manifest missing %q: %+v", want, manifest)
		}
	}
	if manifest.PlanInvalidated || manifest.WorldInvalidated {
		t.Fatalf("body-only failure must keep current plan/simulation reusable: %+v", manifest)
	}
	if _, err := os.Stat(filepath.Join(st.Dir(), "drafts", "01.plan.json")); err != nil {
		t.Fatalf("static body failure removed current plan: %v", err)
	}
	requirementRaw, err := os.ReadFile(filepath.Join(st.Dir(), "reviews", "drafts", "01_full_rerender_required.json"))
	var requirement tools.DraftExternalRerenderRequirement
	if err != nil || json.Unmarshal(requirementRaw, &requirement) != nil ||
		tools.RequiresRegisteredExternalRetest(&requirement) || requirement.Source != "registered_external_detection" ||
		!strings.Contains(strings.Join(tools.RegisteredExternalRetestLabels(&requirement), ","), "zhuque/novel-whole-text-single-segment") {
		t.Fatalf("static body failure lost sampling trigger provenance or created a hard requirement: requirement=%+v err=%v body=%s", requirement, err, requirementRaw)
	}
	next := flow.Route(flow.LoadState(st))
	if next == nil || next.Agent != "drafter" || next.Chapter != 1 {
		t.Fatalf("body-only quarantine did not return the current plan to Drafter: %+v", next)
	}
}

func newPipelineCausalPreflightFixture(t *testing.T) *store.Store {
	t.Helper()
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("causal-preflight", 2); err != nil {
		t.Fatal(err)
	}
	if err := st.Characters.Save([]domain.Character{
		{Name: "林澈", Role: "主角", Tier: "core"},
		{Name: "沈知遥", Role: "女主", Tier: "core"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.WorldSim.SaveSimulationCast(domain.SimulationCast{Assignments: []domain.TierAssignment{
		{Name: "林澈", Tier: domain.TierProtagonistCircle},
		{Name: "沈知遥", Tier: domain.TierProtagonistCircle},
	}}); err != nil {
		t.Fatal(err)
	}

	finalBody := "第一章\n\n林澈把越界线路收回，夜市试点恢复营业。"
	if err := st.Progress.MarkChapterComplete(1, utf8.RuneCountInString(finalBody), "", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.SetPendingRewritesAndFlow([]int{1}, "朱雀整篇单段返工", domain.FlowRewriting); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveFinalChapter(1, finalBody); err != nil {
		t.Fatal(err)
	}
	brief := "# rewrite brief\n\n## 保留事实\n\n- 安全回撤在后续检查前已经完成。\n"
	mustWriteFile(t, filepath.Join(st.Dir(), "reviews", "01_rewrite_brief.md"), brief)
	writePipelinePreflightLedger(t, st, "已完成真实营业")

	oldInstruction := "林澈先完成安全回撤，沈知遥后确认边界。"
	instructionSum := sha256.Sum256([]byte(oldInstruction))
	instructionSHA := hex.EncodeToString(instructionSum[:])
	request := domain.ChapterRerenderRequest{
		Version: 1, Chapter: 1, Instruction: oldInstruction, InstructionSHA256: instructionSHA,
		Reason: "test causal preflight", RequestedAt: time.Now().Format(time.RFC3339),
	}
	requestRaw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	requestRel := "drafts/01.rerender_request.json"
	mustWriteFile(t, filepath.Join(st.Dir(), filepath.FromSlash(requestRel)), string(requestRaw))
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "rerender-request", requestRel); err != nil {
		t.Fatal(err)
	}
	writePipelinePreflightPrompt(t, st, oldInstruction)

	ledgerRaw, err := os.ReadFile(filepath.Join(st.Dir(), "meta", "chapter_progress.json"))
	if err != nil {
		t.Fatal(err)
	}
	finalSum := sha256.Sum256([]byte(finalBody))
	briefSum := sha256.Sum256([]byte(brief))
	ledgerSum := sha256.Sum256(ledgerRaw)
	canonicalFact := "已提交状态结果：夜市试点.status = 已完成真实营业"
	preserveFacts := []string{"安全回撤在后续检查前已经完成。", canonicalFact}
	source := &domain.ChapterRewriteSource{
		BodyPath: "chapters/01.md", BodySHA256: hex.EncodeToString(finalSum[:]), WordCount: utf8.RuneCountInString(finalBody),
		BriefPath: "reviews/01_rewrite_brief.md", BriefSHA256: hex.EncodeToString(briefSum[:]),
		CanonicalStatePath: "meta/chapter_progress.json", CanonicalStateSHA256: hex.EncodeToString(ledgerSum[:]),
		PreserveFacts: preserveFacts,
	}
	protagonistDecision := "先完成安全回撤再继续营业"
	sim := domain.ChapterWorldSimulation{
		Version: 1, Chapter: 1, SimulationID: "ch001-preflight-current", TimeWindow: "当晚两小时",
		CharacterDecisions: []domain.CharacterWorldDecision{
			pipelinePreflightDecision("林澈", protagonistDecision, true),
			pipelinePreflightDecision("沈知遥", "保持检查边界", false),
		},
		ProtagonistProjection: domain.ProtagonistDecisionProjection{
			Protagonist: "林澈", ObservableEffects: []string{"越界线路已经收回"}, HiddenPressures: []string{"后续检查仍会到场"},
			AvailableOptions: []string{"先回撤", "继续营业"}, ChosenDecision: protagonistDecision, DecisionReason: "先消除现实风险",
			PlanConstraints: []string{"检查者不能替代主角发现风险"}, CausalChain: []string{"发现越界", "主动回撤", "继续营业"},
		},
		RewriteSource: source,
		RewriteFactCoverage: []domain.ChapterRewriteFactCoverage{
			{Fact: preserveFacts[0], SimulationEvidence: []string{"主角决定先回撤"}},
			{Fact: preserveFacts[1], SimulationEvidence: []string{"营业状态进入结果台账"}},
		},
		Sources: []string{
			"rewrite_source:chapters/01.md#sha256=" + source.BodySHA256,
			"rewrite_brief:reviews/01_rewrite_brief.md#sha256=" + source.BriefSHA256,
			"chapter_pipeline_instruction:sha256:" + instructionSHA,
		},
	}
	if err := st.SaveChapterWorldSimulation(sim); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "chapter_world_simulation", "meta/chapter_simulations/001.json"); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{
		Chapter: 1, Title: "夜市试点",
		Contract: domain.ChapterContract{RequiredBeats: []string{"安全选择产生真实营业结果"}},
		CausalSimulation: domain.ChapterCausalSimulation{
			WorldSimulationID: sim.SimulationID, ProtagonistDecision: protagonistDecision,
			ProjectPromise:  "主角用现实选择把试点做成生意",
			ChapterFunction: "建立试点的第一个真实结果",
			ContextSources: []string{
				"chapter_world_simulation:" + sim.SimulationID,
				"chapter_pipeline_instruction:sha256:" + instructionSHA,
				"rewrite_source:chapters/01.md#sha256=" + source.BodySHA256,
				"rewrite_brief:reviews/01_rewrite_brief.md#sha256=" + source.BriefSHA256,
				"world_foundation:test", "character_dossiers:test", "current_chapter_outline:test", "chapter_progress:test",
			},
			InitialState: []domain.CharacterSimulationState{{
				Character: "林澈", CurrentGoal: "保住试点", Pressure: "线路越界", ActionTendency: "先回撤再营业",
			}},
			VoiceLogic: []domain.CharacterVoiceLogic{{
				Character: "林澈", SceneObjective: "做出安全选择", HiddenSubtext: "不暴露秘密",
				KnowledgeBoundary: "只知道自己亲见的线路问题", DictionAndRhythm: "短句、先做后说",
				DialogueFunctions: []string{"拒绝冒险"}, ForbiddenMoves: []string{"解释系统"},
			}},
			EmotionalLogic: []domain.CharacterEmotionalLogic{{
				Character: "林澈", ImmediateState: "紧绷", PrimaryEmotion: "警惕", EmotionalTrigger: "发现线路越界",
				GoalAppraisal: "不回撤就会丢掉试点", RegulationStrategy: "先做可逆动作", EmotionLedAction: "收回线路",
				EvidenceInScene: []string{"手指扣住线盘"},
			}},
			CausalBeats: []domain.CausalSimulationBeat{{
				Cause: "线路越界", CharacterChoice: protagonistDecision, WorldResponse: "风险消除", StoryResult: "试点继续",
			}},
			DecisionPoints: []string{"是继续冒险还是先回撤"},
			OutcomeShift:   []string{"试点从风险中恢复为真实营业"},
			ReviewRefinement: domain.ReviewRefinementLoop{
				TriggerSources: []string{"rewrite_brief"}, FailureModes: []string{"旧候选因果过时"},
				PreserveConstraints: preserveFacts, AcceptanceChecks: []string{"事实结果不丢失"}, StopCondition: "门禁通过", IterationLimit: 3,
			},
			AntiAIPlan: domain.AntiAIExecutionPlan{
				RiskSignals: []string{"流程播报"}, CounterMoves: []string{"让选择先于解释"},
				SentenceRhythmPolicy: "按场景压力自然变化", ObjectResponseBudget: "关键结果后才响应一次",
				DialogueFunctionPlan: "对白承担漏答与权力变化", ReviewChecks: []string{"检查主观因果链"},
			},
			LiteraryRendering: &domain.LiteraryRenderingPlan{
				Focalizer: "林澈", NarrativeAccess: domain.LiteraryNarrativeAccessInternal,
				KnowledgeBoundary: "只写林澈可感知的信息", PerceptualBias: "先注意现实风险",
				SummaryOmissionPolicy: "省略无状态变化的流程", Afterimage: "收回的线盘留在摊脚",
				SourceRefs: []string{"human-feel-craft"},
				SceneModes: []domain.LiterarySceneRenderingMode{{
					Target: "线路回撤", Mode: domain.LiterarySceneModeScene, Distance: domain.LiteraryNarrativeDistanceClose,
					StateChange: "风险被主角主动消除", RenderMove: "通过触觉和选择呈现",
				}},
				ActiveLenses: []domain.LiteraryRenderingLens{{
					Kind: "subjective-causality", Target: "主角选择", Move: "从误判压到现实动作", Why: "建立选择后果", Avoid: "解释结论",
					SourceRefs: []string{"human-feel-craft"},
				}},
			},
		},
	}
	if err := st.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "plan", "drafts/01.plan.json"); err != nil {
		t.Fatal(err)
	}
	oldDraft := "第一章 夜市试点\n\n旧候选仍沿用旧推演和旧事实源，但将被错误的低分外判放行。"
	if err := st.Drafts.SaveDraft(1, oldDraft); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "draft", "drafts/01.draft.md"); err != nil {
		t.Fatal(err)
	}
	return st
}

func pipelinePreflightDecision(name, decision string, visible bool) domain.CharacterWorldDecision {
	visibility := "delayed"
	if visible {
		visibility = "visible"
	}
	return domain.CharacterWorldDecision{
		Character: name, Location: "青山县", CurrentGoal: "推进当天目标", Pressure: "时间和关系共同施压",
		KnowledgeBoundary: "只知道亲见和合法通信所得信息", AvailableOptions: []string{"现在行动", "继续观察"},
		Decision: decision, DecisionReason: "当前证据只支持这一步", Action: "落实选择并承担结果", ActionDuration: "两小时",
		CompletionState: "completed", ImmediateResult: "世界状态发生可追踪变化", StateAfter: "进入下一步但未提前完成长期目标",
		VisibleToPOV: visible,
		ButterflyEffects: []domain.DecisionButterflyEffect{{
			Effect: "改变下一次接触的资源和信息条件", Targets: []string{"林澈"}, TransmissionPath: "亲见或延迟通信",
			ArrivalChapter: 1, Visibility: visibility, ProtagonistImpact: "改变林澈后续可选择的行动",
		}},
	}
}

func writePipelinePreflightLedger(t *testing.T, st *store.Store, result string) {
	t.Helper()
	ledger := domain.ChapterProgressLedger{Version: 1, Entries: []domain.ChapterProgressEntry{{
		Chapter:      1,
		StateChanges: []domain.StateChange{{Chapter: 1, Entity: "夜市试点", Field: "status", NewValue: result}},
	}}}
	raw, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(st.Dir(), "meta", "chapter_progress.json"), string(raw))
}

func writePipelinePreflightPrompt(t *testing.T, st *store.Store, prompt string) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"prompt": prompt})
	if err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(st.Dir(), "meta", "pipeline.json"), string(raw))
}
