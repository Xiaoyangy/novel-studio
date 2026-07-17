package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
	"github.com/voocel/agentcore"
)

func TestProjectAllWorldSimulationPromptCarriesDurableRecoveryGaps(t *testing.T) {
	boundary := ProjectedArcBoundary{
		Volume: 1, Arc: 1, Title: "第一弧", Goal: "完成首轮夜市试点",
		FirstChapter: 1, LastChapter: 12, BookLastChapter: 420,
	}
	prompt := projectAllWorldSimulationPrompt(boundary, 1, 2, []string{
		"missing character decision: 马玉芬",
		"grounded protagonist projection available_options omit chosen decision",
	}, []string{
		`"decision_reason 缺少当前可见证据"`,
	})
	for _, required := range []string{
		"第 2/8 个有界 world-simulation 会话",
		"missing character decision: 马玉芬",
		"available_options omit chosen decision",
		"禁止重发 locked/already_present 角色",
		"只有 blocking=true 才放入 authority_contract_characters",
		"project_all_grounded/blocking=false 必须放入 character_decisions",
		"只由服务端绑定主角 chosen_decision",
		"已失败或已过期动作",
		"planning_context_access_receipt.source_token",
		"gaps 清零后立即单独 finalize",
		"上一有界会话最近的 simulate_chapter_world 工具错误",
		"decision_reason 缺少当前可见证据",
		"禁止再次提交同一失败参数",
	} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("recovery prompt missing %q:\n%s", required, prompt)
		}
	}
}

func TestProjectAllPlanningProtocolDigestKeepsRuntimeRecoveryOutOfIdentity(t *testing.T) {
	const plannerFixture = "planner-protocol-fixture.v1"
	const want = "973c424eda31f62ac6c2ffd475a451b02844a11e614bd7dc5327edf827479232"
	if got := ProjectAllPlanningProtocolDigest(plannerFixture); got != want {
		t.Fatalf("project-all planning protocol digest changed: got %s want %s", got, want)
	}
}

func TestWorldSimulatorSystemPromptCarriesGroundedAuthorityContract(t *testing.T) {
	for _, required := range []string{
		"grounded DecisionPolicy",
		"location 必须是不超过 32 个 Unicode 字符",
		"不得包含，。；！？或换行",
		"decision/action 不得整句复制 current_goal",
		"具体 current_action 或本章大纲中的具体行动句",
		"decision_reason 与其余投影必须由至少2个当前因果锚点支持",
		"不得加入后见信息",
	} {
		if !strings.Contains(worldSimulatorSystemPrompt, required) {
			t.Fatalf("world simulator prompt missing grounded authority rule %q", required)
		}
	}
}

func TestProjectAllWorldSimulationTurnBudgetDistinguishesFreshAndRecovery(t *testing.T) {
	if !projectAllWorldSimulationStartsFresh([]string{"missing chapter world simulation"}) {
		t.Fatal("missing simulation must use the initial full turn budget")
	}
	if projectAllWorldSimulationStartsFresh([]string{"missing character decision: 马玉芬"}) ||
		projectAllWorldSimulationStartsFresh(nil) {
		t.Fatal("partial/finalize recovery must use the short turn budget")
	}
	if got := projectAllWorldSimulationTurnCeiling(1, true); got != 12 {
		t.Fatalf("fresh turn ceiling=%d want 12", got)
	}
	for _, tc := range []struct {
		pass        int
		startsFresh bool
	}{
		{pass: 1, startsFresh: false},
		{pass: 2, startsFresh: true},
		{pass: 3, startsFresh: false},
	} {
		if got := projectAllWorldSimulationTurnCeiling(tc.pass, tc.startsFresh); got != 8 {
			t.Fatalf("recovery turn ceiling pass=%d fresh=%v: got %d want 8", tc.pass, tc.startsFresh, got)
		}
	}
	if projectAllWorldSimulationPassLimit != 8 || projectAllWorldSimulationStagnantPassLimit != 3 {
		t.Fatalf(
			"world simulation recovery bounds drifted: total=%d stagnant=%d",
			projectAllWorldSimulationPassLimit,
			projectAllWorldSimulationStagnantPassLimit,
		)
	}
}

func TestProjectAllWorldSimulationProgressRequiresDurablePartialAdvance(t *testing.T) {
	base := projectAllWorldSimulationProgress{
		CharacterDecisions: 14,
		ProjectionFields:   2,
		HasTimeWindow:      true,
		GapCount:           5,
		ContentDigest:      "sha256:before",
	}
	for name, current := range map[string]projectAllWorldSimulationProgress{
		"character": {
			CharacterDecisions: 15,
			ProjectionFields:   2,
			HasTimeWindow:      true,
			GapCount:           4,
			ContentDigest:      "sha256:character",
		},
		"coverage": {
			CharacterDecisions: 14,
			RewriteCoverage:    1,
			ProjectionFields:   2,
			HasTimeWindow:      true,
			GapCount:           4,
			ContentDigest:      "sha256:coverage",
		},
		"projection": {
			CharacterDecisions: 14,
			ProjectionFields:   8,
			HasTimeWindow:      true,
			GapCount:           4,
			ContentDigest:      "sha256:projection",
		},
		"same-shape correction": {
			CharacterDecisions: 14,
			ProjectionFields:   2,
			HasTimeWindow:      true,
			GapCount:           5,
			ContentDigest:      "sha256:corrected-content",
		},
		"gap reduction": {
			CharacterDecisions: 14,
			ProjectionFields:   2,
			HasTimeWindow:      true,
			GapCount:           4,
			ContentDigest:      "sha256:before",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if !current.advancedFrom(base) {
				t.Fatalf("durable %s progress was not detected: before=%+v after=%+v", name, base, current)
			}
		})
	}
	if base.advancedFrom(base) {
		t.Fatal("unchanged partial reset the stagnant-session budget")
	}
	regressed := base
	regressed.CharacterDecisions--
	regressed.GapCount++
	regressed.ContentDigest = "sha256:regressed-content"
	if regressed.advancedFrom(base) {
		t.Fatal("a regressed partial counted as progress")
	}
	withoutTime := base
	withoutTime.HasTimeWindow = false
	if !base.advancedFrom(withoutTime) {
		t.Fatal("first durable time window was not detected as progress")
	}
}

func TestProjectAllWorldSimulationProgressDigestIgnoresLeaseNoiseButTracksCorrections(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	partial := domain.ChapterWorldSimulation{
		Version:     1,
		Chapter:     1,
		TimeWindow:  "当晚",
		Sources:     []string{"context-access:first"},
		GeneratedAt: "2026-07-17T00:00:00Z",
		ProtagonistProjection: domain.ProtagonistDecisionProjection{
			Protagonist:    "林澈",
			ChosenDecision: "先核验",
			DecisionReason: "先看第一份现场证据",
		},
	}
	if err := st.SaveChapterWorldSimulationPartial(partial); err != nil {
		t.Fatal(err)
	}
	first, err := loadProjectAllWorldSimulationProgress(st, 1)
	if err != nil || first.ContentDigest == "" {
		t.Fatalf("load first progress: progress=%+v err=%v", first, err)
	}
	partial.Sources = []string{"context-access:second"}
	partial.GeneratedAt = "2026-07-17T00:01:00Z"
	if err := st.SaveChapterWorldSimulationPartial(partial); err != nil {
		t.Fatal(err)
	}
	leaseRotated, err := loadProjectAllWorldSimulationProgress(st, 1)
	if err != nil {
		t.Fatal(err)
	}
	if leaseRotated.ContentDigest != first.ContentDigest {
		t.Fatalf("source/time noise changed durable digest: first=%s second=%s", first.ContentDigest, leaseRotated.ContentDigest)
	}
	partial.ProtagonistProjection.DecisionReason = "先看第二份现场证据"
	if err := st.SaveChapterWorldSimulationPartial(partial); err != nil {
		t.Fatal(err)
	}
	corrected, err := loadProjectAllWorldSimulationProgress(st, 1)
	if err != nil {
		t.Fatal(err)
	}
	if corrected.ProjectionFields != leaseRotated.ProjectionFields ||
		corrected.ContentDigest == leaseRotated.ContentDigest {
		t.Fatalf("same-shape semantic correction was not detected: before=%+v after=%+v", leaseRotated, corrected)
	}
}

func TestLoadCurrentProjectedSimulationFailsClosedOnUncheckpointedArtifact(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveChapterWorldSimulation(domain.ChapterWorldSimulation{
		Version:      1,
		Chapter:      1,
		SimulationID: "uncheckpointed",
		TimeWindow:   "当天",
	}); err != nil {
		t.Fatal(err)
	}
	if simulation, checkpoint, err := loadCurrentProjectedSimulation(st, 1); err == nil {
		t.Fatalf(
			"uncheckpointed formal simulation was treated as absent: simulation=%+v checkpoint=%+v",
			simulation,
			checkpoint,
		)
	}
}

func TestLoadCurrentProjectedPlanDistinguishesFreshFromUncheckpointedArtifact(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if plan, checkpoint, err := loadCurrentProjectedPlan(st, 1); err != nil ||
		plan != nil || checkpoint != nil {
		t.Fatalf("fresh chapter must remain plannable: plan=%+v checkpoint=%+v err=%v", plan, checkpoint, err)
	}
	if err := st.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "未完成 checkpoint"}); err != nil {
		t.Fatal(err)
	}
	if plan, checkpoint, err := loadCurrentProjectedPlan(st, 1); err == nil {
		t.Fatalf("uncheckpointed plan was treated as absent: plan=%+v checkpoint=%+v", plan, checkpoint)
	}
}

func TestRecentProjectAllSimulationToolErrorsFiltersDeduplicatesAndBounds(t *testing.T) {
	toolResult := func(callID, toolName, content string, isError bool) agentcore.Message {
		msg := agentcore.ToolResultMsg(callID, json.RawMessage(content), isError)
		msg.Metadata["tool_name"] = toolName
		return msg
	}
	longError := strings.Repeat("错", projectAllWorldSimulationToolErrorRuneLimit+20)
	messages := []agentcore.AgentMessage{
		toolResult("context-error", "novel_context", `"context should be ignored"`, true),
		toolResult("old-sim", "simulate_chapter_world", `"old simulation error"`, true),
		toolResult("success-sim", "simulate_chapter_world", `"successful result"`, false),
		toolResult("multiline", "simulate_chapter_world", `"line one\nline two"`, true),
		toolResult("latest-long", "simulate_chapter_world", fmt.Sprintf("%q", longError), true),
		toolResult("duplicate-1", "simulate_chapter_world", `"duplicate simulation error"`, true),
		toolResult("duplicate-2", "simulate_chapter_world", `"duplicate simulation error"`, true),
	}

	errors := recentProjectAllSimulationToolErrors(messages)
	if len(errors) != projectAllWorldSimulationToolErrorLimit {
		t.Fatalf("recent simulation errors=%#v", errors)
	}
	if errors[0] != "duplicate simulation error" {
		t.Fatalf("duplicate recent error was not kept once: %#v", errors)
	}
	if !strings.HasPrefix(errors[1], strings.Repeat("错", 20)) || !strings.HasSuffix(errors[1], "…") {
		t.Fatalf("latest long error was not kept and bounded: %q", errors[1])
	}
	if got := len([]rune(strings.TrimSuffix(errors[1], "…"))); got != projectAllWorldSimulationToolErrorRuneLimit {
		t.Fatalf("bounded error runes=%d want %d", got, projectAllWorldSimulationToolErrorRuneLimit)
	}
	if errors[2] != "line one line two" {
		t.Fatalf("errors are not newest-first or normalized: %#v", errors)
	}
	for _, forbidden := range []string{"context should be ignored", "successful result", "old simulation error"} {
		if strings.Contains(strings.Join(errors, "|"), forbidden) {
			t.Fatalf("unexpected error %q in %#v", forbidden, errors)
		}
	}
}

func TestCompactProjectAllPromptGapsIsBounded(t *testing.T) {
	var gaps []string
	for i := 0; i < 20; i++ {
		gaps = append(gaps, strings.Repeat("x", i+1))
	}
	compact := compactProjectAllPromptGaps(gaps)
	if len(compact) != 13 || !strings.Contains(compact[len(compact)-1], "另有8项") {
		t.Fatalf("unexpected compact gaps: %#v", compact)
	}
}

func TestFinalizeReadyProjectedWorldSimulationStopsBeforeCanceledWrite(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("test", 3); err != nil {
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
	decision := func(name, choice string, visible bool) domain.CharacterWorldDecision {
		return domain.CharacterWorldDecision{
			Character:         name,
			Location:          "青山县",
			CurrentGoal:       "推进本人当天目标",
			Pressure:          "时间与关系同时施压",
			KnowledgeBoundary: "只依据亲见事实与合法通信行动",
			AvailableOptions:  []string{"立即行动", "继续观察"},
			Decision:          choice,
			DecisionReason:    "现有证据只支持这一步",
			Action:            "落实选择并承担结果",
			ActionDuration:    "两小时",
			CompletionState:   "completed",
			ImmediateResult:   "现场条件发生可追踪变化",
			StateAfter:        "进入下一步但没有提前完成长期目标",
			VisibleToPOV:      visible,
			ButterflyEffects: []domain.DecisionButterflyEffect{{
				Effect:            "改变下一次接触时的资源条件",
				Targets:           []string{"林澈"},
				TransmissionPath:  "亲见或延迟通信",
				ArrivalChapter:    1,
				Visibility:        map[bool]string{true: "visible", false: "delayed"}[visible],
				ProtagonistImpact: "改变林澈后续可选行动",
			}},
		}
	}
	partial := domain.ChapterWorldSimulation{
		Version:    1,
		Chapter:    1,
		TimeWindow: "当晚两小时",
		CharacterDecisions: []domain.CharacterWorldDecision{
			decision("林澈", "立即行动", true),
			decision("沈知遥", "继续观察", false),
		},
		ProtagonistProjection: domain.ProtagonistDecisionProjection{
			Protagonist:       "林澈",
			ObservableEffects: []string{"现场条件可以核验"},
			HiddenPressures:   []string{"离屏检查尚未传回"},
			AvailableOptions:  []string{"立即行动", "继续观察"},
			ChosenDecision:    "立即行动",
			DecisionReason:    "现有证据只支持这一步",
			PlanConstraints:   []string{"只写主角已知事实"},
			CausalChain:       []string{"压力到场", "证据收窄选项", "立即行动"},
		},
	}
	if err := st.SaveChapterWorldSimulationPartial(partial); err != nil {
		t.Fatal(err)
	}
	if _, _, gaps := tools.ChapterWorldSimulationStatus(st, 1); len(gaps) != 0 {
		t.Fatalf("test fixture is not ready to finalize: %#v", gaps)
	}
	before, err := json.Marshal(partial)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	finalized, err := finalizeReadyProjectedWorldSimulation(
		ctx,
		tools.NewContextTool(st, tools.References{}, ""),
		tools.NewSimulateChapterWorldTool(st),
		st,
		1,
	)
	if finalized || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled host finalize was not stopped: finalized=%v err=%v", finalized, err)
	}
	if formal, loadErr := st.LoadChapterWorldSimulation(1); loadErr != nil || formal != nil {
		t.Fatalf("canceled finalize wrote formal simulation: sim=%+v err=%v", formal, loadErr)
	}
	if cp := st.Checkpoints.LatestByStep(domain.ChapterScope(1), "chapter_world_simulation"); cp != nil {
		t.Fatalf("canceled finalize appended checkpoint: %+v", cp)
	}
	afterPartial, loadErr := st.LoadChapterWorldSimulationPartial(1)
	if loadErr != nil || afterPartial == nil {
		t.Fatalf("canceled finalize lost partial: sim=%+v err=%v", afterPartial, loadErr)
	}
	after, err := json.Marshal(afterPartial)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("canceled finalize changed partial:\nbefore=%s\nafter=%s", before, after)
	}
}

type projectAllWorldSimulationExecutorFunc func(context.Context, json.RawMessage) (json.RawMessage, error)

func (f projectAllWorldSimulationExecutorFunc) Execute(
	ctx context.Context,
	args json.RawMessage,
) (json.RawMessage, error) {
	return f(ctx, args)
}

func TestPrefillProjectedWorldSimulationAuthorityBatchesOnlyBlockingGaps(t *testing.T) {
	entries := make([]map[string]any, 0, 22)
	wantCharacters := make([]string, 0, 18)
	for i := 1; i <= 18; i++ {
		name := fmt.Sprintf("角色%02d", i)
		wantCharacters = append(wantCharacters, name)
		entries = append(entries, map[string]any{
			"character":         name,
			"blocking":          true,
			"simulation_status": "missing",
		})
	}
	entries = append(entries,
		map[string]any{
			"character":         "角色01",
			"blocking":          true,
			"simulation_status": "missing",
		},
		map[string]any{
			"character":         "已经落盘",
			"blocking":          true,
			"simulation_status": "already_present",
		},
		map[string]any{
			"character":         "模型推演",
			"blocking":          false,
			"simulation_status": "missing",
		},
		map[string]any{
			"character":         "   ",
			"blocking":          true,
			"simulation_status": "missing",
		},
	)
	contextPayload, err := json.Marshal(map[string]any{
		"planning_context_access_receipt": map[string]any{
			"source_token": "planning-access-current",
		},
		"project_all_state_source_token": "project-all-state-current",
		"simulation_character_authority": map[string]any{
			"format":  "layered_v1",
			"entries": entries,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	contextCalls := 0
	contextExecutor := projectAllWorldSimulationExecutorFunc(func(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
		contextCalls++
		var args struct {
			Chapter int    `json:"chapter"`
			Profile string `json:"profile"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			t.Fatal(err)
		}
		if args.Chapter != 7 || args.Profile != "world_simulation" {
			t.Fatalf("unexpected context args: %+v", args)
		}
		return contextPayload, nil
	})

	var calls []map[string]json.RawMessage
	simulationExecutor := projectAllWorldSimulationExecutorFunc(func(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var args map[string]json.RawMessage
		if err := json.Unmarshal(raw, &args); err != nil {
			t.Fatal(err)
		}
		calls = append(calls, args)
		return json.Marshal(map[string]any{
			"gaps": []string{fmt.Sprintf("after batch %d", len(calls))},
		})
	})
	finalGaps := []string{
		"missing character decision: 模型推演",
		"incomplete protagonist_projection",
	}
	result, err := prefillProjectedWorldSimulationAuthority(
		context.Background(),
		contextExecutor,
		simulationExecutor,
		7,
		func() []string { return finalGaps },
	)
	if err != nil {
		t.Fatal(err)
	}
	if contextCalls != 1 {
		t.Fatalf("novel_context calls=%d want 1", contextCalls)
	}
	if strings.Join(result.Characters, "|") != strings.Join(wantCharacters, "|") {
		t.Fatalf("prefill characters mismatch:\n got=%#v\nwant=%#v", result.Characters, wantCharacters)
	}
	if len(result.Batches) != 3 || len(result.Batches[0]) != 8 ||
		len(result.Batches[1]) != 8 || len(result.Batches[2]) != 2 {
		t.Fatalf("unexpected prefill batches: %#v", result.Batches)
	}
	if strings.Join(result.RemainingGaps, "|") != strings.Join(finalGaps, "|") {
		t.Fatalf("remaining gaps=%#v want %#v", result.RemainingGaps, finalGaps)
	}
	for i, call := range calls {
		var chapter int
		if err := json.Unmarshal(call["chapter"], &chapter); err != nil || chapter != 7 {
			t.Fatalf("batch %d chapter=%d err=%v", i+1, chapter, err)
		}
		var batch []string
		if err := json.Unmarshal(call["authority_contract_characters"], &batch); err != nil {
			t.Fatal(err)
		}
		if len(batch) == 0 || len(batch) > projectAllAuthorityPrefillBatchLimit {
			t.Fatalf("batch %d has invalid size %d: %#v", i+1, len(batch), batch)
		}
		for _, forbidden := range []string{"character_decisions", "protagonist_projection", "finalize"} {
			if _, exists := call[forbidden]; exists {
				t.Fatalf("batch %d unexpectedly supplied %s: %s", i+1, forbidden, call[forbidden])
			}
		}
		if i == 0 {
			var sources []string
			if err := json.Unmarshal(call["sources"], &sources); err != nil {
				t.Fatal(err)
			}
			if strings.Join(sources, "|") != "project-all-state-current|planning-access-current" {
				t.Fatalf("first batch sources=%#v", sources)
			}
		} else if _, exists := call["sources"]; exists {
			t.Fatalf("batch %d must not reuse first-batch sources", i+1)
		}
	}
}

func TestPrefillProjectedWorldSimulationAuthorityStopsAfterContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	contextExecutor := projectAllWorldSimulationExecutorFunc(func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
		cancel()
		return json.Marshal(map[string]any{
			"planning_context_access_receipt": map[string]any{
				"source_token": "planning-access-current",
			},
			"project_all_state_source_token": "project-all-state-current",
			"simulation_character_authority": []map[string]any{{
				"character":         "沈知遥",
				"blocking":          true,
				"simulation_status": "missing",
			}},
		})
	})
	simulationCalls := 0
	simulationExecutor := projectAllWorldSimulationExecutorFunc(func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
		simulationCalls++
		return json.Marshal(map[string]any{"gaps": []string{"unexpected"}})
	})
	_, err := prefillProjectedWorldSimulationAuthority(
		ctx,
		contextExecutor,
		simulationExecutor,
		1,
		nil,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("prefill cancellation error=%v", err)
	}
	if simulationCalls != 0 {
		t.Fatalf("prefill wrote %d batches after context cancellation", simulationCalls)
	}
}
