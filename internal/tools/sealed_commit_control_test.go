package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestSealedCommitMechanicallyRestoresHiddenCharacterLedger(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{
			name: "model omits character ledger",
		},
		{
			name: "model forges hidden character ledger",
			mutate: func(args map[string]any) {
				forged := testCharacterStageRecords("protagonist", "hidden_operator")
				forged[1].Location = "正文可见的屋顶"
				forged[1].CurrentAction = "当面向主角泄露全部秘密"
				forged[1].KnowledgeBoundary = "主角已经知道隐藏换锁记录"
				forged[1].VisibleInChapter = true
				args["character_stage_records"] = forged
				args["state_changes"] = []domain.StateChange{{
					Entity:   "hidden_operator",
					Field:    "status",
					NewValue: "模型伪造状态",
				}}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st, bundle, hiddenName, hiddenFact := sealedCommitControlTestFixture(t)
			renderContext, err := NewContextTool(st, References{}, "").Execute(
				context.Background(),
				json.RawMessage(`{"chapter":1,"profile":"draft"}`),
			)
			if err != nil {
				t.Fatalf("load frozen sealed prose context: %v", err)
			}
			if strings.Contains(string(renderContext), hiddenName) ||
				strings.Contains(string(renderContext), hiddenFact) {
				t.Fatalf("hidden server-side state leaked into prose context: %s", renderContext)
			}

			args := map[string]any{
				"chapter":    1,
				"summary":    "主角先确认欠费单，再完成钥匙交接。",
				"characters": []string{"模型伪造角色"},
				"key_events": []string{"模型伪造事件"},
				"timeline_events": []domain.TimelineEvent{{
					Time:       "错误时间",
					Event:      "模型伪造时间线",
					Characters: []string{"模型伪造角色"},
				}},
			}
			if tt.mutate != nil {
				tt.mutate(args)
			}
			rawArgs, err := json.Marshal(args)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := NewCommitChapterTool(st).Execute(context.Background(), rawArgs)
			if err != nil {
				t.Fatalf("sealed commit should use server control plane: %v", err)
			}
			var committed domain.CommitResult
			if err := json.Unmarshal(raw, &committed); err != nil || !committed.Committed {
				t.Fatalf("sealed commit result=%+v err=%v body=%s", committed, err, raw)
			}

			stages, err := st.LoadCharacterStageRecords(1)
			if err != nil {
				t.Fatal(err)
			}
			hidden, ok := sealedCommitControlTestStage(stages, hiddenName)
			if !ok {
				t.Fatalf("server did not sediment hidden character: %+v", stages)
			}
			if hidden.Location != "地下钥匙档案室" ||
				hidden.CurrentAction != "把备用钥匙移入未公开封袋" ||
				hidden.KnowledgeBoundary != hiddenFact ||
				hidden.VisibleInChapter {
				t.Fatalf("model args overrode sealed hidden state: %+v", hidden)
			}
			if _, ok := sealedCommitControlTestStage(stages, "模型伪造角色"); ok {
				t.Fatalf("model-invented character survived sealed override: %+v", stages)
			}

			timeline, err := st.World.LoadTimeline()
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range timeline {
				if strings.Contains(event.Event, "模型伪造") {
					t.Fatalf("model-authored timeline survived sealed override: %+v", timeline)
				}
			}
			delta, err := st.LoadChapterWorldDelta(1)
			if err != nil || delta == nil {
				t.Fatalf("load committed world delta: delta=%+v err=%v", delta, err)
			}
			if delta.GenerationID != bundle.GenerationID ||
				!sealedCommitControlTestContains(delta.Sources, "server-sealed-control:"+bundle.BundleDigest) {
				t.Fatalf("world delta lacks exact sealed control provenance: %+v", delta)
			}
			hiddenDelta := false
			hiddenOffscreen := false
			for _, character := range delta.CharacterDeltas {
				if character.Character == hiddenName &&
					character.Location == "地下钥匙档案室" &&
					character.KnowledgeBoundary == hiddenFact {
					hiddenDelta = true
				}
			}
			for _, world := range delta.WorldDeltas {
				if world.Kind == "offscreen_character" &&
					world.Entity == hiddenName &&
					strings.Contains(world.Change, "备用钥匙") {
					hiddenOffscreen = true
				}
			}
			if !hiddenDelta || !hiddenOffscreen {
				t.Fatalf("hidden exact-bundle state did not reach durable ledgers: %+v", delta)
			}
			body, err := st.Drafts.LoadChapterText(1)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(body, hiddenName) || strings.Contains(body, hiddenFact) {
				t.Fatalf("hidden ledger fact leaked into committed prose: %q", body)
			}
		})
	}
}

func TestSealedCommitSchemaOmitsBundleControlledModelPayload(t *testing.T) {
	st, _, hiddenName, _ := sealedCommitControlTestFixture(t)
	tool := NewCommitChapterTool(st)
	props, ok := tool.Schema()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("sealed commit schema properties missing")
	}
	for _, field := range []string{
		"characters", "key_events", "timeline_events", "foreshadow_updates",
		"relationship_changes", "state_changes", "character_stage_records",
		"resource_updates", "resource_proposals",
	} {
		if _, exists := props[field]; exists {
			t.Fatalf("sealed commit schema still asks model for server-controlled %s", field)
		}
	}
	for _, field := range []string{"chapter", "summary", "hook_type", "pov"} {
		if _, exists := props[field]; !exists {
			t.Fatalf("sealed commit schema lost body-derived field %s", field)
		}
	}
	if description := tool.Description(); !strings.Contains(description, "服务端精确填充") ||
		!strings.Contains(description, "完整提交路径复验") {
		t.Fatalf("sealed commit description does not explain server control plane: %q", description)
	}
	if _, err := tool.Execute(
		context.Background(),
		json.RawMessage(`{"chapter":1,"summary":"主角确认欠费单后接过钥匙。"}`),
	); err != nil {
		t.Fatalf("minimal sealed schema payload did not pass the full commit path: %v", err)
	}
	stages, err := st.LoadCharacterStageRecords(1)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := sealedCommitControlTestStage(stages, hiddenName); !ok {
		t.Fatalf("minimal sealed payload was not hydrated from projected bundle: %+v", stages)
	}
}

func TestNonSealedCommitUsesModelLedgerArguments(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("ordinary commit", 2); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveDraft(
		1,
		"# 第一章 普通提交\n\n许闻溪把登记表交给邱梅，两人核对完当天记录。",
	); err != nil {
		t.Fatal(err)
	}
	stages := testCharacterStageRecords("许闻溪", "邱梅")
	stages[1].Location = "模型参数里的值班室"
	stages[1].CurrentAction = "模型参数里的核对动作"
	args, err := json.Marshal(map[string]any{
		"chapter":                 1,
		"summary":                 "许闻溪和邱梅核对登记表。",
		"characters":              []string{"许闻溪", "邱梅"},
		"key_events":              []string{"核对登记表"},
		"character_stage_records": stages,
		"timeline_events": []domain.TimelineEvent{{
			Time:       "模型参数里的当晚",
			Event:      "模型参数里的登记完成",
			Characters: []string{"许闻溪", "邱梅"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewCommitChapterTool(st).Execute(context.Background(), args); err != nil {
		t.Fatalf("ordinary commit rejected model ledger: %v", err)
	}
	saved, err := st.LoadCharacterStageRecords(1)
	if err != nil {
		t.Fatal(err)
	}
	side, ok := sealedCommitControlTestStage(saved, "邱梅")
	if !ok ||
		side.Location != "模型参数里的值班室" ||
		side.CurrentAction != "模型参数里的核对动作" {
		t.Fatalf("ordinary commit did not preserve model args: %+v", saved)
	}
	timeline, err := st.World.LoadTimeline()
	if err != nil || len(timeline) != 1 ||
		timeline[0].Event != "模型参数里的登记完成" {
		t.Fatalf("ordinary timeline was mechanically replaced: %+v err=%v", timeline, err)
	}
	delta, err := st.LoadChapterWorldDelta(1)
	if err != nil || delta == nil {
		t.Fatalf("load ordinary delta: delta=%+v err=%v", delta, err)
	}
	if sealedCommitControlTestContains(delta.Sources, "server-sealed-control:") {
		t.Fatalf("ordinary commit falsely claimed sealed provenance: %+v", delta.Sources)
	}
}

func TestSealedWritingModeRequiresPhaseCorrectExecutionLocks(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("sealed lock guard", 2); err != nil {
		t.Fatal(err)
	}
	activateSealedWritingModeForTest(t, st)

	if _, err := NewPlanChapterTool(st).Execute(
		context.Background(),
		json.RawMessage(`{"chapter":1,"title":"越权","goal":"越权","conflict":"越权","hook":"越权"}`),
	); err == nil ||
		!strings.Contains(err.Error(), "sealed_two_pass_v2") ||
		!strings.Contains(err.Error(), "project-all execution lock") {
		t.Fatalf("legacy planner escaped sealed mode without lock: %v", err)
	}
	if _, err := NewDraftChapterTool(st).Execute(
		context.Background(),
		json.RawMessage(`{"chapter":1,"mode":"write","content":"# 第一章 越权\n\n这段正文不应写入。"}`),
	); err == nil ||
		!strings.Contains(err.Error(), "sealed_two_pass_v2") ||
		!strings.Contains(err.Error(), "promote/render execution lock") {
		t.Fatalf("legacy writer escaped sealed mode without lock: %v", err)
	}

	const projectOwner = "sealed-project-all-lock-test"
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionProjectAll,
		TargetChapter: 1,
		Owner:         projectOwner,
	}); err != nil {
		t.Fatal(err)
	}
	if err := guardPipelinePlanningExecution(st, 1, "plan_chapter"); err != nil {
		t.Fatalf("correct project-all planning lock was rejected: %v", err)
	}
	if err := guardPipelineProseExecution(st, 1, "draft_chapter"); err == nil ||
		!strings.Contains(err.Error(), "preplan execution lock") {
		t.Fatalf("project-all lock permitted prose mutation: %v", err)
	}
	if err := st.Runtime.ReleasePipelineExecution(projectOwner); err != nil {
		t.Fatal(err)
	}

	if err := st.Drafts.SaveChapterPlan(domain.ChapterPlan{
		Chapter: 1,
		Title:   "冻结计划",
	}); err != nil {
		t.Fatal(err)
	}
	planCheckpoint, err := st.Checkpoints.AppendArtifactLatest(
		domain.ChapterScope(1),
		"plan",
		"drafts/01.plan.json",
	)
	if err != nil {
		t.Fatal(err)
	}
	const renderOwner = "sealed-render-lock-test"
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionRender,
		TargetChapter: 1,
		PlanDigest:    planCheckpoint.Digest,
		Owner:         renderOwner,
	}); err != nil {
		t.Fatal(err)
	}
	if err := guardPipelineProseExecution(st, 1, "draft_chapter"); err != nil {
		t.Fatalf("correct render prose lock was rejected: %v", err)
	}
	if err := guardPipelinePlanningExecution(st, 1, "plan_chapter"); err == nil ||
		!strings.Contains(err.Error(), "render execution lock") {
		t.Fatalf("render lock permitted planning mutation: %v", err)
	}
}

func sealedCommitControlTestFixture(
	t *testing.T,
) (*store.Store, domain.ProjectedChapterBundle, string, string) {
	t.Helper()
	st, plan, _ := sealedRAGGuardFixture(t, false)
	receipt, err := st.RAG.LoadLatestRAGFactReceipt(1)
	if err != nil || receipt == nil {
		t.Fatalf("load sealed RAG receipt: receipt=%+v err=%v", receipt, err)
	}
	fullPlan, err := decodeChapterPlanArgs(planArgs(1))
	if err != nil {
		t.Fatal(err)
	}
	fullPlan.Chapter = plan.Chapter
	fullPlan.Title = plan.Title
	fullPlan.Goal = plan.Goal
	fullPlan.Conflict = plan.Conflict
	fullPlan.Hook = plan.Hook
	fullPlan.Contract = plan.Contract
	fullPlan.CausalSimulation.WorldSimulationID =
		plan.CausalSimulation.WorldSimulationID
	fullPlan.CausalSimulation.ProtagonistDecision =
		plan.CausalSimulation.ProtagonistDecision
	fullPlan.CausalSimulation.ContextSources = append(
		fullPlan.CausalSimulation.ContextSources,
		plan.CausalSimulation.ContextSources...,
	)
	fullPlan.CausalSimulation.ExternalRefs =
		append(fullPlan.CausalSimulation.ExternalRefs, plan.CausalSimulation.ExternalRefs...)
	fullPlan.CausalSimulation.GroundingDetails =
		append(fullPlan.CausalSimulation.GroundingDetails, plan.CausalSimulation.GroundingDetails...)
	plan = fullPlan
	const (
		hiddenName = "hidden_operator"
		hiddenFact = "只知道备用钥匙被移入未公开封袋，主角尚不知情"
	)
	simulation := sealedRAGGuardSimulation("sealed-rag-sim-1")
	simulation.CharacterDecisions = append(
		simulation.CharacterDecisions,
		domain.CharacterWorldDecision{
			Character:         hiddenName,
			Time:              "上午同一时段",
			Location:          "地下钥匙档案室",
			CurrentGoal:       "隐藏备用钥匙去向",
			Pressure:          "公开记录即将核查",
			KnowledgeBoundary: hiddenFact,
			AvailableOptions:  []string{"继续封存", "立即公开"},
			Decision:          "继续封存",
			DecisionReason:    "公开会暴露未完成的内部交接",
			Action:            "把备用钥匙移入未公开封袋",
			ActionDuration:    "十分钟",
			CompletionState:   "completed",
			ImmediateResult:   "备用钥匙暂时离开公开登记链",
			StateAfter:        "继续隐匿并等待下一次核查",
			VisibleToPOV:      false,
			ButterflyEffects: []domain.DecisionButterflyEffect{{
				Effect:            "下一次钥匙核查会出现缺口",
				Targets:           []string{"protagonist"},
				TransmissionPath:  "通过后续登记差异延迟传回",
				ArrivalChapter:    1,
				Visibility:        "hidden",
				ProtagonistImpact: "本章主角尚不可见",
			}},
		},
	)
	simulation.ProtagonistProjection.HiddenPressures = append(
		simulation.ProtagonistProjection.HiddenPressures,
		"备用钥匙登记存在主角尚不可见的缺口",
	)
	generation, source, registry, bundle := sealedRAGGuardProjectedFixture(
		t,
		plan,
		simulation,
		*receipt,
	)
	projected := st.ProjectedV2()
	if err := projected.CreateBuildingGeneration(generation, source, registry); err != nil {
		t.Fatal(err)
	}
	cursor, err := projected.InitializeProjectionCursor(generation.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projected.ProjectBundleAndAdvance(
		generation.GenerationDigest,
		generation.ChainTailRoot,
		*cursor,
		bundle,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := projected.SealGeneration(generation.GenerationID); err != nil {
		t.Fatal(err)
	}
	_, realization, err := projected.ActivateSealedGeneration(
		generation.GenerationID,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	promotion := sealedRAGGuardPromotion(t, bundle)
	if _, err := projected.Promote(*realization, promotion); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveChapterWorldSimulation(bundle.ChapterWorldSimulation); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatest(
		domain.ChapterScope(1),
		"chapter_world_simulation",
		"meta/chapter_simulations/001.json",
	); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveChapterPlan(bundle.ChapterPlan); err != nil {
		t.Fatal(err)
	}
	planCheckpoint, err := st.Checkpoints.AppendArtifactLatest(
		domain.ChapterScope(1),
		"plan",
		"drafts/01.plan.json",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionRender,
		TargetChapter: 1,
		PlanDigest:    planCheckpoint.Digest,
		Owner:         "sealed-commit-control-test",
	}); err != nil {
		t.Fatal(err)
	}
	planDigest, err := domain.ComputeChapterPlanV2Digest(bundle.ChapterPlan)
	if err != nil {
		t.Fatal(err)
	}
	writeSealedRAGGuardMarker(t, st, sealedV2FrozenPlanMarker{
		Version:                "pipeline-planning.v1",
		Chapter:                1,
		PlanDigest:             planCheckpoint.Digest,
		PlanningGenerationID:   generation.GenerationID,
		ProjectionBinding:      sealedV2ProjectionBinding,
		ProjectedPlanSHA256:    planDigest,
		ProjectedBundleDigest:  bundle.BundleDigest,
		PromotionReceiptDigest: promotion.ReceiptDigest,
	})
	if _, err := PublishFrozenDraftRenderContext(
		st,
		1,
		planCheckpoint.Digest,
		bundle.RenderContext,
	); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveDraft(
		1,
		"# 第一章\n\n林澈把欠费单推回桌面，逐项确认后才接过钥匙。先确认欠费单，再交接钥匙，这个顺序没有被跳过。",
	); err != nil {
		t.Fatal(err)
	}
	return st, bundle, hiddenName, hiddenFact
}

func sealedCommitControlTestStage(
	records []domain.CharacterStageRecord,
	name string,
) (domain.CharacterStageRecord, bool) {
	for _, record := range records {
		if record.Character == name {
			return record, true
		}
	}
	return domain.CharacterStageRecord{}, false
}

func sealedCommitControlTestContains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected || strings.HasPrefix(value, expected) {
			return true
		}
	}
	return false
}

func activateSealedWritingModeForTest(t *testing.T, st *store.Store) {
	t.Helper()
	receipt := domain.WritingPipelineModeReceipt{
		Version:     domain.WritingPipelineModeReceiptVersion,
		Mode:        domain.WritingPipelineModeSealedTwoPassV2,
		ActivatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	var err error
	receipt.ReceiptDigest, err =
		domain.ComputeWritingPipelineModeReceiptDigest(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveWritingPipelineMode(receipt); err != nil {
		t.Fatal(err)
	}
}
