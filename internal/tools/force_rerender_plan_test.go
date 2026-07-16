package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/store"
)

type reusableRewriteFixture struct {
	store            *store.Store
	ledger           domain.ChapterProgressLedger
	instructionToken string
}

func newReusableRewriteFixture(t *testing.T) reusableRewriteFixture {
	t.Helper()
	st := newChapterSimulationTestStore(t)
	prepareRewriteSourceTest(t, st,
		"第一章\n\n林澈把越界的线路收回后，才继续夜市试点。",
		"# rewrite brief\n\n## 保留事实\n\n- 安全回撤在后续检查前已经完成。\n")
	ledger := domain.ChapterProgressLedger{Version: 1, Entries: []domain.ChapterProgressEntry{{
		Chapter: 1,
		StateChanges: []domain.StateChange{{
			Chapter: 1, Entity: "夜市试点", Field: "status", NewValue: "已完成真实营业",
		}},
	}}}
	writeReusableRewriteLedger(t, st, ledger)

	const instructionText = "林澈先完成安全回撤，沈知遥后确认边界。"
	checkpointChapterInstruction(t, st, 1, instructionText, true)
	writeReusablePipelinePrompt(t, st, instructionText)
	instruction, err := loadChapterPipelineInstruction(st, 1)
	if err != nil || instruction == nil {
		t.Fatalf("load baseline instruction: value=%+v err=%v", instruction, err)
	}
	source, _, _, err := loadChapterRewriteSource(st, 1)
	if err != nil || source == nil {
		t.Fatalf("load baseline rewrite source: value=%+v err=%v", source, err)
	}
	coverage := make([]domain.ChapterRewriteFactCoverage, 0, len(source.PreserveFacts))
	for _, fact := range source.PreserveFacts {
		coverage = append(coverage, domain.ChapterRewriteFactCoverage{
			Fact: fact, SimulationEvidence: []string{"角色决定与结果状态已经承接该事实"},
		})
	}
	const protagonistDecision = "先完成安全回撤再继续营业"
	sim := domain.ChapterWorldSimulation{
		Version: 1, Chapter: 1, TimeWindow: "当晚两小时",
		CharacterDecisions: []domain.CharacterWorldDecision{
			simulatedDecision("林澈", protagonistDecision, true),
			simulatedDecision("沈知遥", "保持检查边界", false),
		},
		ProtagonistProjection: domain.ProtagonistDecisionProjection{
			Protagonist: "林澈", ObservableEffects: []string{"越界线路已经可见"}, HiddenPressures: []string{"后续检查仍会到场"},
			AvailableOptions: []string{"先回撤", "继续营业"}, ChosenDecision: protagonistDecision, DecisionReason: "先消除现实风险",
			PlanConstraints: []string{"检查者不能替代主角发现风险"}, CausalChain: []string{"发现越界", "主动回撤", "继续营业"},
		},
		RewriteSource: source, RewriteFactCoverage: coverage,
		Sources: []string{rewriteSourceToken(source), rewriteBriefToken(source), instruction.Token},
	}
	sim.SimulationID = chapterWorldSimulationID(sim)
	if err := st.SaveChapterWorldSimulation(sim); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{
		Chapter: 1, Title: "夜市试点",
		Contract: domain.ChapterContract{RequiredBeats: []string{"安全选择产生真实营业结果"}},
		CausalSimulation: domain.ChapterCausalSimulation{
			WorldSimulationID: sim.SimulationID, ProtagonistDecision: protagonistDecision,
			ContextSources: []string{"chapter_world_simulation:" + sim.SimulationID, instruction.Token},
			CausalBeats: []domain.CausalSimulationBeat{{
				Cause: "线路越界", CharacterChoice: protagonistDecision, WorldResponse: "风险消除", StoryResult: "试点继续",
			}},
			OutcomeShift: []string{"试点从风险中恢复为真实营业"},
		},
	}
	if err := st.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	if err := ValidateReusableCausalPlanForRerender(st, 1); err != nil {
		t.Fatalf("baseline reusable plan fixture is invalid: %v", err)
	}
	return reusableRewriteFixture{store: st, ledger: ledger, instructionToken: instruction.Token}
}

func writeReusableRewriteLedger(t *testing.T, st *store.Store, ledger domain.ChapterProgressLedger) {
	t.Helper()
	raw, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Dir(), "meta", "chapter_progress.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeReusablePipelinePrompt(t *testing.T, st *store.Store, prompt string) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"prompt": prompt})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Dir(), "meta", "pipeline.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReusableCausalPlanAllowsOnlyCommittedBodySurfaceDrift(t *testing.T) {
	fixture := newReusableRewriteFixture(t)
	if err := fixture.store.Drafts.SaveFinalChapter(1,
		"第一章\n\n林澈没再解释。他拔掉越界插头，收回线盘，等摊位恢复安全才重新开灯。"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateReusableCausalPlanForRerender(fixture.store, 1); err != nil {
		t.Fatalf("body-only surface drift must keep the causal plan reusable: %v", err)
	}
}

func TestReusableCausalPlanRejectsCanonicalOutcomeMutation(t *testing.T) {
	fixture := newReusableRewriteFixture(t)
	fixture.ledger.Entries[0].StateChanges[0].NewValue = "仍未完成真实营业"
	writeReusableRewriteLedger(t, fixture.store, fixture.ledger)
	if err := ValidateReusableCausalPlanForRerender(fixture.store, 1); err == nil || !strings.Contains(err.Error(), "canonical chapter_progress") {
		t.Fatalf("canonical outcome mutation reused the old simulation/plan: %v", err)
	}
}

func TestReusableCausalPlanRejectsNewPreserveFactWithoutCoverage(t *testing.T) {
	fixture := newReusableRewriteFixture(t)
	briefPath := filepath.Join(fixture.store.Dir(), "reviews", "01_rewrite_brief.md")
	brief := "# rewrite brief\n\n## 保留事实\n\n- 安全回撤在后续检查前已经完成。\n- 结算只能发生在真实营业结果之后。\n"
	if err := os.WriteFile(briefPath, []byte(brief), 0o644); err != nil {
		t.Fatal(err)
	}
	currentSource, _, _, err := loadChapterRewriteSource(fixture.store, 1)
	if err != nil || currentSource == nil {
		t.Fatalf("load expanded preserve facts: source=%+v err=%v", currentSource, err)
	}
	sim, err := fixture.store.LoadChapterWorldSimulation(1)
	if err != nil || sim == nil {
		t.Fatalf("load simulation: sim=%+v err=%v", sim, err)
	}
	// Refresh the source receipt and causal ID while intentionally leaving the
	// old coverage list untouched. This proves coverage is checked directly,
	// rather than the test passing only because the brief hash changed.
	sim.RewriteSource = currentSource
	sim.SimulationID = chapterWorldSimulationID(*sim)
	if err := fixture.store.SaveChapterWorldSimulation(*sim); err != nil {
		t.Fatal(err)
	}
	plan, err := fixture.store.Drafts.LoadChapterPlan(1)
	if err != nil || plan == nil {
		t.Fatalf("load plan: plan=%+v err=%v", plan, err)
	}
	plan.CausalSimulation.WorldSimulationID = sim.SimulationID
	plan.CausalSimulation.ContextSources = []string{"chapter_world_simulation:" + sim.SimulationID, fixture.instructionToken}
	if err := fixture.store.Drafts.SaveChapterPlan(*plan); err != nil {
		t.Fatal(err)
	}
	if err := ValidateReusableCausalPlanForRerender(fixture.store, 1); err == nil || !strings.Contains(err.Error(), "rewrite_fact_coverage") {
		t.Fatalf("new preserve fact without simulation evidence reused the old causal plan: %v", err)
	}
}

func TestReusableCausalPlanRejectsLiveInstructionPromptMutation(t *testing.T) {
	fixture := newReusableRewriteFixture(t)
	writeReusablePipelinePrompt(t, fixture.store, "新增硬合同：结算必须晚于检查者离场。")
	if err := ValidateReusableCausalPlanForRerender(fixture.store, 1); err == nil || !strings.Contains(err.Error(), "world simulation.sources") {
		t.Fatalf("changed live prompt reused simulation with an old instruction token: %v", err)
	}

	currentInstruction, err := loadChapterPipelineInstruction(fixture.store, 1)
	if err != nil || currentInstruction == nil {
		t.Fatalf("load changed live prompt: instruction=%+v err=%v", currentInstruction, err)
	}
	sim, err := fixture.store.LoadChapterWorldSimulation(1)
	if err != nil || sim == nil {
		t.Fatalf("load simulation: sim=%+v err=%v", sim, err)
	}
	sim.Sources = append(sim.Sources, currentInstruction.Token)
	sim.SimulationID = chapterWorldSimulationID(*sim)
	if err := fixture.store.SaveChapterWorldSimulation(*sim); err != nil {
		t.Fatal(err)
	}
	plan, err := fixture.store.Drafts.LoadChapterPlan(1)
	if err != nil || plan == nil {
		t.Fatalf("load plan: plan=%+v err=%v", plan, err)
	}
	plan.CausalSimulation.WorldSimulationID = sim.SimulationID
	plan.CausalSimulation.ContextSources = []string{"chapter_world_simulation:" + sim.SimulationID, fixture.instructionToken}
	if err := fixture.store.Drafts.SaveChapterPlan(*plan); err != nil {
		t.Fatal(err)
	}
	if err := ValidateReusableCausalPlanForRerender(fixture.store, 1); err == nil || !strings.Contains(err.Error(), "plan context_sources") {
		t.Fatalf("changed live prompt reused plan with an old instruction token: %v", err)
	}
}

func TestRenderOnlyReplanEscalationDerivesHistoricalDraftAttempts(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{Chapter: 2, Title: "摊位要先站稳"}
	plan.CausalSimulation.ReviewRefinement.IterationLimit = 2
	if err := s.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoints.AppendArtifact(domain.ChapterScope(2), "plan", "drafts/02.plan.json"); err != nil {
		t.Fatal(err)
	}
	requestPath := filepath.Join(s.Dir(), "drafts", "02.rerender_request.json")
	if err := os.WriteFile(requestPath, []byte(`{"chapter":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoints.AppendArtifact(domain.ChapterScope(2), "rerender-request", "drafts/02.rerender_request.json"); err != nil {
		t.Fatal(err)
	}

	first := strings.Repeat("林澈把价牌放好，然后核对票据，然后走到下一家。", 100)
	if err := s.Drafts.SaveDraft(2, first); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoints.AppendArtifact(domain.ChapterScope(2), "draft", "drafts/02.draft.md"); err != nil {
		t.Fatal(err)
	}
	second := strings.Repeat("林澈核对票据，然后搬运价牌，然后完成下一项。", 100)
	if err := s.Drafts.SaveDraft(2, second); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoints.AppendArtifact(domain.ChapterScope(2), "draft", "drafts/02.draft.md"); err != nil {
		t.Fatal(err)
	}
	if err := SetDraftExternalRerenderRequirement(s.Dir(), DraftExternalRerenderRequirement{
		Chapter: 2, EvaluatedBodySHA256: reviewreport.BodySHA256(second),
		AIProbabilityPercent: 79, PassExclusivePercent: 4,
		AdviceComplete: true, RevisionPlan: []string{"重组整章场景"},
	}); err != nil {
		t.Fatal(err)
	}

	escalation := InspectRenderOnlyReplanEscalation(s, 2)
	if !escalation.Required || escalation.Attempts != 2 || escalation.Limit != 2 {
		t.Fatalf("historical render loop was not escalated: %+v", escalation)
	}
	if RenderOnlyRerenderReady(s, 2) {
		t.Fatal("exhausted render-only loop must not keep reusing the old plan")
	}

	// A newer plan is the durable reset boundary; the new plan gets its own
	// render budget and may proceed through the normal validation path.
	plan.CausalSimulation.ReviewRefinement.IterationLimit = 3
	if err := s.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoints.AppendArtifact(domain.ChapterScope(2), "plan", "drafts/02.plan.json"); err != nil {
		t.Fatal(err)
	}
	reset := InspectRenderOnlyReplanEscalation(s, 2)
	if reset.Required || reset.Attempts != 0 || reset.Limit != 3 {
		t.Fatalf("new plan did not reset structural attempts: %+v", reset)
	}
}

func TestDraftStructuralBlockCheckpointIsDistinctHashIdempotent(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{Chapter: 1, Title: "第一个因果周期"}
	plan.CausalSimulation.ReviewRefinement.IterationLimit = 2
	if err := s.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoints.AppendArtifact(domain.ChapterScope(1), "plan", "drafts/01.plan.json"); err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat("林澈把事情办完。", 180)
	if err := s.Drafts.SaveDraft(1, body); err != nil {
		t.Fatal(err)
	}
	report, gate := inspectDraftAIGCGate(s, 1, body)
	if gate.Passed {
		t.Fatalf("fixture must reproduce a structural draft failure: %+v", gate)
	}
	if err := checkpointDraftStructuralBlock(s, 1, body, report, gate); err != nil {
		t.Fatal(err)
	}
	if err := checkpointDraftStructuralBlock(s, 1, body, report, gate); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, cp := range s.Checkpoints.All() {
		if cp.Scope.Matches(domain.ChapterScope(1)) && cp.Step == "draft-structural-block" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("same blocked hash consumed %d attempts, want 1", count)
	}

	// Global checkpoint de-duplication must not erase the same body's first
	// failure in a genuinely new causal epoch.
	plan.Title = "第二个因果周期"
	if err := s.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	planCheckpoint, err := s.Checkpoints.AppendArtifact(domain.ChapterScope(1), "plan", "drafts/01.plan.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := checkpointDraftStructuralBlock(s, 1, body, report, gate); err != nil {
		t.Fatal(err)
	}
	count = 0
	latestStructuralSeq := int64(0)
	for _, cp := range s.Checkpoints.All() {
		if cp.Scope.Matches(domain.ChapterScope(1)) && cp.Step == "draft-structural-block" {
			count++
			if cp.Seq > latestStructuralSeq {
				latestStructuralSeq = cp.Seq
			}
		}
	}
	if count != 2 || latestStructuralSeq <= planCheckpoint.Seq {
		t.Fatalf("same body was not counted in the new causal epoch: count=%d plan=%+v latest_structural=%d", count, planCheckpoint, latestStructuralSeq)
	}
	if escalation := InspectRenderOnlyReplanEscalation(s, 1); escalation.Attempts != 1 || escalation.Required {
		t.Fatalf("new causal epoch should contain exactly one attempt: %+v", escalation)
	}
}

func TestDraftStructuralBlockUpgradeKeepsLegacyAttemptIdempotent(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{Chapter: 1, Title: "同一因果周期"}
	if err := s.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoints.AppendArtifact(domain.ChapterScope(1), "plan", "drafts/01.plan.json"); err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat("林澈把事情办完。", 180)
	if err := s.Drafts.SaveDraft(1, body); err != nil {
		t.Fatal(err)
	}
	// Simulate the raw body digest emitted by the first implementation.
	if _, err := s.Checkpoints.AppendArtifact(domain.ChapterScope(1), "draft-structural-block", "drafts/01.draft.md"); err != nil {
		t.Fatal(err)
	}
	report, gate := inspectDraftAIGCGate(s, 1, body)
	if gate.Passed {
		t.Fatalf("fixture must reproduce a structural draft failure: %+v", gate)
	}
	if err := checkpointDraftStructuralBlock(s, 1, body, report, gate); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, cp := range s.Checkpoints.All() {
		if cp.Scope.Matches(domain.ChapterScope(1)) && cp.Step == "draft-structural-block" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("same-epoch legacy attempt was double-counted during upgrade: %d", count)
	}
}

func TestReusableRerenderPlanCannotBypassChapterScopedAttractionContract(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	policy := `{
  "version": 1,
  "chapter_trend_language": {
    "1": [{
      "item": "呱，", "source_context": "meta/web_reference_brief.md#第一章热梗落点",
      "character_carrier": "赵航", "scene_function": "饭桌反应",
      "usage_budget": "一次并后接完整台词", "forbidden_usage": "章末禁用"
    }]
  }
}`
	if err := os.WriteFile(filepath.Join(s.Dir(), "meta", "web_reference_brief.json"), []byte(policy), 0o644); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{Chapter: 2, Title: "第二章"}
	plan.CausalSimulation.TrendLanguage = []domain.TrendLanguagePlan{{
		Item: "先借一辆", SourceContext: "meta/web_reference_brief.md 第2章 active project item",
		CharacterCarrier: "系统", SceneFunction: "借车反转", UsageBudget: "一次", ForbiddenUsage: "章末禁用",
	}}
	if err := s.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	err := ValidateReusableCausalPlanForRerender(s, 2)
	if err == nil || !strings.Contains(err.Error(), "attraction contract") {
		t.Fatalf("render-only validation bypassed an ungrounded chapter trend plan: %v", err)
	}
}
