package tools

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestDraftProfileBuildsSelectiveRenderPacket(t *testing.T) {
	plan := domain.ChapterPlan{
		Chapter:  1,
		Title:    "回县城的第一顿饭",
		Goal:     "让主角拿到第一笔可用额度",
		Conflict: "旧债不能直接偿还",
		Contract: domain.ChapterContract{
			RequiredBeats: []string{
				"主角确认额度真实并完成第一笔改善消费",
				"赵航必须用“呱，”打断饭桌说教",
			},
			ForbiddenMoves: []string{"公开系统", "普通对白不得使用指定设计腔措辞"},
			ContinuityChecks: []string{
				"沈知遥到场时首单已经发生。",
				"章末具体锚点必须是票据、测试记录和九点要求。",
			},
		},
		CausalSimulation: domain.ChapterCausalSimulation{
			VisualDesign: []domain.CharacterVisualDesign{
				{Character: "林澈", Silhouette: "瘦高，肩背还留着通勤族的紧", FaceAndHair: "短发被雨压乱", ClothingStyle: "旧衬衫卷到小臂", BodyLanguage: "难堪时先笑", SignatureObject: "磨白的电脑包", FirstImpression: "刚失业还硬撑体面", StatusWear: "袖口起皱", SceneUse: "夹鱼刺时露出手腕勒痕"},
				{Character: "沈知遥", Silhouette: "高挑利落", FaceAndHair: "低马尾，眉眼清冷", ClothingStyle: "浅色衬衫配深色长裤", BodyLanguage: "说话前先看现场", SignatureObject: "工作证", FirstImpression: "旧同学比记忆里更有压迫感", StatusWear: "鞋边沾灰", SceneUse: "快步进夜市时工作证轻撞衣襟"},
			},
			CharacterKit: []domain.CharacterKitEntry{
				{Character: "林澈", FirstAppearance: true, AppearanceRef: "visual_design:林澈"},
				{Character: "沈知遥", FirstAppearance: true, AppearanceRef: "visual_design:沈知遥"},
			},
			EmotionalLogic: []domain.CharacterEmotionalLogic{{
				Character: "林澈", ImmediateState: "被亲戚追问后表面还能笑", PrimaryEmotion: "难堪和警惕",
				EmotionalTrigger: "父母越替他挡话，他越清楚自己让家里担心", GoalAppraisal: "争辩不能恢复体面，必须做出结果",
				RegulationStrategy: "把翻身冲动压成小额试验", EmotionLedAction: "不炫耀额度，离席验证",
				EvidenceInScene: []string{"护住被添满的碗", "没有在饭桌公开系统", "第三条不得泄露"},
			}},
			ReaderRetentionPlan: domain.ReaderRetentionPlan{
				SurfaceBeats:  []domain.RetentionSurfaceBeat{{MustShow: "饭桌压力"}, {MustShow: "消费见效"}, {MustShow: "项目核验"}},
				LatentContext: []string{"亲戚私下另有打算"},
				CutOrCompress: []string{"安装流程一句带过"},
			},
			TrendLanguage: []domain.TrendLanguagePlan{{Item: "呱，……", CharacterCarrier: "赵航"}},
			VoiceLogic: []domain.CharacterVoiceLogic{{
				Character: "赵航", SpeechPrinciple: "先拆台后护朋友", HiddenSubtext: "替朋友挡话",
				KnowledgeBoundary: "不知道系统", DictionAndRhythm: "短句", SilenceOrAction: "每句先夹菜",
			}},
			DialogueBlueprints: []domain.DialogueSceneBlueprint{{
				SceneID: "dinner", DialogueMode: "group_council", ScenePressure: "失业羞耻", DialogueObjective: "替主角解围",
				TurnProgression: []domain.DialogueTurnDesign{{Speaker: "赵航", SurfaceLineFunction: "反问", ActionBeat: "抬眼夹菜", NextPressure: "亲戚失语"}},
			}},
			EndingContract: domain.EndingConsequenceContract{
				ConcreteAnchor:  "票据和测试记录",
				Consequence:     "林澈接下明早九点的责任",
				NextChapterPull: "下一家摊主主动求助",
				WhyNotUI:        "不要用系统数字收尾",
			},
		},
	}
	working := map[string]any{
		"chapter_plan":             &plan,
		"causal_simulation":        plan.CausalSimulation,
		"causal_simulation_policy": "full",
	}
	result := map[string]any{
		"working_memory":           working,
		"chapter_plan":             &plan,
		"causal_simulation":        plan.CausalSimulation,
		"causal_simulation_policy": "full",
		"chapter_world_simulation": map[string]any{
			"status":              "ready",
			"simulation_id":       "sim-1",
			"character_decisions": []domain.CharacterWorldDecision{{Character: "亲戚"}},
			"protagonist_projection": domain.ProtagonistDecisionProjection{
				Protagonist: "林澈", ObservableEffects: []string{"亲戚当面催工作"}, HiddenPressures: []string{"亲戚私下联络媒人"},
				ChosenDecision: "离席验证额度", DecisionReason: "不想继续被比较；后续分析不进正文", CausalChain: []string{"饭桌受压", "尝试花钱"},
				AvailableOptions: []string{"当场炫耀", "离席验证"}, PlanConstraints: []string{"先点按钮再付款"},
			},
		},
	}

	applyChapterContextProfile(result, "draft")

	packet, ok := result["render_packet"].(draftRenderPacket)
	if !ok {
		t.Fatalf("render_packet type = %T", result["render_packet"])
	}
	if len(packet.MandatoryBeats) != 1 || len(packet.OptionalStyleBeats) != 0 {
		t.Fatalf("mandatory/optional = %#v / %#v", packet.MandatoryBeats, packet.OptionalStyleBeats)
	}
	if len(packet.ForbiddenMoves) != 2 {
		t.Fatalf("hard project prohibitions must survive render projection: %#v", packet.ForbiddenMoves)
	}
	for _, policy := range []string{
		packet.PlanTranslationPolicy,
		packet.ReaderRegisterPolicy,
		packet.InterfaceCompression,
		packet.ScenePurposePolicy,
		packet.SpokenLanguagePolicy,
		packet.EmotionalRenderPolicy,
		packet.ProofFocusPolicy,
		packet.CharacterEntrancePolicy,
	} {
		if strings.TrimSpace(policy) == "" {
			t.Fatalf("render translation policy missing: %#v", packet)
		}
	}
	if len(packet.VisualCards) != 2 || !packet.VisualCards[0].FirstAppearance || packet.VisualCards[1].FaceAndHair == "" {
		t.Fatalf("first-appearance visual cards must reach prose context: %#v", packet.VisualCards)
	}
	if !strings.Contains(packet.CharacterEntrancePolicy, "视觉锚点") || !strings.Contains(packet.CharacterEntrancePolicy, "禁止证件照式罗列") {
		t.Fatalf("character entrance policy must require selective POV-grounded appearance: %q", packet.CharacterEntrancePolicy)
	}
	if strings.Contains(packet.ProofFocusPolicy, "完整看价、付款") || !strings.Contains(packet.ProofFocusPolicy, "不强制跟拍") {
		t.Fatalf("proof policy must not force a repeated customer verification chain: %q", packet.ProofFocusPolicy)
	}
	serializedPolicies := strings.Join([]string{
		packet.ReaderRegisterPolicy,
		packet.ProofFocusPolicy,
		packet.NamedRolePolicy,
		packet.RelationshipPriority,
	}, "\n")
	for _, projectSpecific := range []string{"青山县", "县城居民", "林澈", "摊主", "男女主"} {
		if strings.Contains(serializedPolicies, projectSpecific) {
			t.Fatalf("render policy leaked project-specific assumption %q: %s", projectSpecific, serializedPolicies)
		}
	}
	if strings.Contains(packet.EmotionalRenderPolicy, "必须改变紧接着的选择") || !strings.Contains(packet.EmotionalRenderPolicy, "暂时没有结论") {
		t.Fatalf("emotion policy must allow human hesitation without instant utility: %q", packet.EmotionalRenderPolicy)
	}
	if len(packet.EmotionalLenses) != 0 {
		t.Fatalf("scene evidence must remain in planning, not prose packet: %#v", packet.EmotionalLenses)
	}
	if len(packet.ContinuityChecks) != 1 || packet.ContinuityChecks[0] != "沈知遥到场时首单已经发生。" {
		t.Fatalf("factual continuity must survive while render choreography stays outside: %#v", packet.ContinuityChecks)
	}
	if packet.EndingContract.Consequence != "" || packet.EndingAnchorCandidate != "" {
		t.Fatalf("hook already carries the ending pull; detailed ending choreography must stay out: %#v / %q", packet.EndingContract, packet.EndingAnchorCandidate)
	}
	raw, err := json.Marshal(packet)
	if err != nil {
		t.Fatal(err)
	}
	serialized := string(raw)
	for _, forbidden := range []string{
		"turn_progression", "action_beat", "proof_on_page", "latent_context", "hidden_pressures", "每句先夹菜",
		"scene_objective", "sentence_rhythm_policy", "review_checks", "candidate_beats", "dialogue_scenes",
		"饭桌压力", "被亲戚追问后表面还能笑", "票据和测试记录", "饭桌受压", "当场炫耀", "先点按钮再付款",
	} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("draft render packet leaked %q: %s", forbidden, serialized)
		}
	}
	if len(raw) > 7000 {
		t.Fatalf("prose-facing packet regrew into a planning dossier: %d bytes", len(raw))
	}
	if _, exists := result["causal_simulation"]; exists {
		t.Fatal("draft profile must hide full causal_simulation")
	}
	world := result["chapter_world_simulation"].(map[string]any)
	if _, exists := world["character_decisions"]; exists {
		t.Fatal("draft profile must hide all-character decisions")
	}
	projection := world["protagonist_projection"].(draftProtagonistProjection)
	if len(projection.ObservableEffects) != 1 {
		t.Fatalf("observable effects lost: %#v", projection)
	}
	if len(projection.CausalChain) != 0 || len(projection.AvailableOptions) != 0 || len(projection.PlanConstraints) != 0 || projection.DecisionReason != "不想继续被比较" {
		t.Fatalf("draft projection still carries planning choreography: %#v", projection)
	}
	leanPlan := result["chapter_plan"].(map[string]any)
	if _, exists := leanPlan["causal_simulation"]; exists {
		t.Fatal("lean chapter plan must not embed causal_simulation")
	}
	if _, exists := leanPlan["contract"]; exists {
		t.Fatal("draft chapter plan must not duplicate the canonical render_packet contract")
	}
	if hasContextKey(result, "chapter_contract") {
		t.Fatal("draft profile must not retain a duplicate chapter_contract beside render_packet")
	}
}

func TestDraftProfileHidesRewriteBodyButKeepsImmutableSourceContract(t *testing.T) {
	plan := domain.ChapterPlan{
		Chapter: 1,
		Title:   "回来第一天",
		Contract: domain.ChapterContract{
			RequiredBeats: []string{"林澈完成第一笔真实县内支出"},
		},
	}
	chapter := &domain.ChapterRewriteSource{
		BodyPath:      "chapters/01.md",
		BodySHA256:    "body-sha",
		WordCount:     2311,
		BriefPath:     "reviews/01_rewrite_brief.md",
		BriefSHA256:   "brief-sha",
		PreserveFacts: []string{"首笔支出为4280元"},
	}
	rewrite := map[string]any{
		"chapter":             chapter,
		"current_body":        strings.Repeat("旧稿对白。", 1800),
		"brief_markdown":      strings.Repeat("完整评审示例动作。", 600),
		"required_sources":    []string{"rewrite_source:chapters/01.md#sha256=body-sha"},
		"preservation_policy": "保留金额、关键选择和结果。",
	}
	working := map[string]any{
		"chapter_plan":     &plan,
		"chapter_contract": plan.Contract,
		"rewrite_source":   rewrite,
	}
	result := map[string]any{
		"chapter_plan":     &plan,
		"chapter_contract": plan.Contract,
		"rewrite_source":   rewrite,
		"working_memory":   working,
	}

	applyChapterContextProfile(result, "draft")

	for _, container := range []map[string]any{result, working} {
		compact, ok := container["rewrite_source"].(map[string]any)
		if !ok {
			t.Fatalf("compact rewrite source missing: %#v", container["rewrite_source"])
		}
		if compact["chapter"] == nil || compact["required_sources"] == nil || compact["preservation_policy"] == nil {
			t.Fatalf("immutable rewrite source contract lost: %#v", compact)
		}
		if _, exists := compact["current_body"]; exists {
			t.Fatal("old chapter body leaked into draft profile")
		}
		if _, exists := compact["brief_markdown"]; exists {
			t.Fatal("full review markdown leaked into draft profile")
		}
		if !strings.Contains(compact["source_body_policy"].(string), "按 render_packet 重新讲述") {
			t.Fatalf("rewrite source policy missing: %#v", compact)
		}
	}
	if hasContextKey(result, "chapter_contract") {
		t.Fatal("duplicate chapter contract survived draft projection")
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) >= 12*1024 {
		t.Fatalf("draft profile retained rewrite prose blobs: %d bytes", len(raw))
	}
}

func TestDraftRenderPacketKeepsEveryHardMoveAndFactualContinuity(t *testing.T) {
	forbidden := []string{
		"不得越过第一章范围",
		"不得泄露系统",
		"不得把额度写成存款",
		"不得删除返工后果",
		"不得提前兑现借车",
		"不得展开教程式流程",
		"不得让系统弹窗抢人物反应",
	}
	plan := domain.ChapterPlan{
		Chapter:    1,
		Title:      "回来第一天",
		Goal:       "林澈先做一笔可撤回的小额试验。",
		Conflict:   "钱已经花了，安全隐患仍在。",
		EmotionArc: "难堪——克制——短暂踏实——收紧——愿意承担。",
		Contract: domain.ChapterContract{
			ForbiddenMoves: forbidden,
			ContinuityChecks: []string{
				"专项额度不是个人存款，系统秘密无人知晓。",
				"局部返工源正文 chapters/01.md 的 sha256 必须保持为 old-source。",
			},
			PayoffPoints: []string{"首笔经营结果落到普通顾客身上。"},
			SceneAnchors: []string{"孩子的鞋尖与两碗豆腐脑。"},
		},
	}

	packet := newDraftRenderPacket(plan)

	if !reflect.DeepEqual(packet.ForbiddenMoves, forbidden) {
		t.Fatalf("hard forbidden moves were truncated: %#v", packet.ForbiddenMoves)
	}
	if len(packet.ContinuityChecks) != 1 || packet.ContinuityChecks[0] != plan.Contract.ContinuityChecks[0] {
		t.Fatalf("factual continuity was lost or source metadata leaked: %#v", packet.ContinuityChecks)
	}
	if len(packet.PayoffPoints) != 1 || len(packet.SceneAnchors) != 1 {
		t.Fatalf("reader payoff or concrete scene anchor missing: %#v / %#v", packet.PayoffPoints, packet.SceneAnchors)
	}
	if packet.Goal == "" || packet.Conflict == "" || packet.EmotionArc == "" {
		t.Fatalf("human motive/conflict/emotion contract missing: %#v", packet)
	}
}

func TestDraftRenderPacketProjectsExplicitLiteraryContractWithProvenance(t *testing.T) {
	plan := domain.ChapterPlan{
		Chapter: 7,
		Hook:    "门关上后，杯底那圈冷茶还在桌面慢慢洇开。",
		CausalSimulation: domain.ChapterCausalSimulation{
			LiteraryRendering: &domain.LiteraryRenderingPlan{
				Focalizer:             "许闻溪",
				NarrativeAccess:       domain.LiteraryNarrativeAccessInternal,
				KnowledgeBoundary:     "只写许闻溪亲历、回忆和从会议材料中有依据推断的内容；不得读取梁渡未表达的打算。",
				PerceptualBias:        "她把每次停顿都先误读成对方准备否定自己。",
				SummaryOmissionPolicy: "三周例行复核用一段概述跨过，只把意见第一次分裂的会议留在现场。",
				Afterimage:            "门关后桌面缓慢洇开的冷茶圈。",
				SourceRefs:            []string{"literary-rendering#focalization-boundary", "literary-rendering#scene-summary"},
				SceneModes: []domain.LiterarySceneRenderingMode{{
					Target: "梁渡把签字笔推回来的会议", Mode: domain.LiterarySceneModeScene,
					Distance: domain.LiteraryNarrativeDistanceClose, StateChange: "许闻溪决定不再替旧方案担责",
					RenderMove: "先让她误读停顿，再由笔尖方向迫使她修正判断。",
				}},
				ActiveLenses: []domain.LiteraryRenderingLens{{
					Kind: "free-indirect-discourse", Target: "签字被拒后的三句叙述",
					Move: "去掉她想，让旁白短暂使用她给失败起的内部称呼。",
					Why:  "读者需要先陷入她的误判，随后才能感到修正。", Avoid: "不把梁渡的真实动机写成全知结论。",
					SourceRefs: []string{"literary-rendering#free-indirect-discourse"},
				}},
			},
		},
	}

	packet := newDraftRenderPacket(plan)
	contract := packet.LiteraryRenderContract
	if packet.Version != 6 || contract.Source != "explicit_plan" {
		t.Fatalf("explicit literary contract was not versioned/projected: %#v", contract)
	}
	if contract.Focalizer != "许闻溪" || contract.NarrativeAccess != "internal" || len(contract.SceneModes) != 1 || len(contract.ActiveLenses) != 1 {
		t.Fatalf("literary decisions were lost: %#v", contract)
	}
	joinedRefs := strings.Join(append(append([]string{}, contract.SourceRefs...), contract.ActiveLenses[0].SourceRefs...), "\n")
	for _, want := range []string{"literary-rendering#focalization-boundary", "literary-rendering#scene-summary", "literary-rendering#free-indirect-discourse"} {
		if !strings.Contains(joinedRefs, want) {
			t.Fatalf("literary provenance %q missing: %#v", want, contract)
		}
	}
	if strings.Contains(contract.UsagePolicy, "每章至少") || strings.Contains(contract.UsagePolicy, "固定比例") {
		t.Fatalf("literary contract became a quota checklist: %q", contract.UsagePolicy)
	}
	raw, err := json.Marshal(packet)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "本参考把英文叙事学") {
		t.Fatalf("full research asset leaked into prose packet: %s", raw)
	}
}

func TestDraftRenderPacketProjectsLegacyPlanIntoLiteraryContract(t *testing.T) {
	plan := domain.ChapterPlan{
		Hook: "电话第二声接通，答案还没来。",
		CausalSimulation: domain.ChapterCausalSimulation{
			InitialState: []domain.CharacterSimulationState{{Character: "林澈", Misbeliefs: []string{"做成一件事就能把难堪抹掉"}}},
			VoiceLogic: []domain.CharacterVoiceLogic{{
				Character: "林澈", KnowledgeBoundary: "不知道沈知遥为何到场，也不知道贺骁会不会答应。",
				HiddenSubtext: "不愿再让父母替他接住后果", SubtextStrategy: "被追问时只答能承担的那一半。",
			}},
			EmotionalLogic: []domain.CharacterEmotionalLogic{{
				Character: "林澈", GoalAppraisal: "付款成功不等于没有把风险推给摊主。",
				EmotionLedAction: "咽回辩解，停止扩铺并承担返工。", EvidenceInScene: []string{"legacy-evidence-must-not-leak"},
			}},
			OutcomeShift:        []string{"林澈从把付款成功当作验证，转为先承担安全后果。"},
			ReaderRetentionPlan: domain.ReaderRetentionPlan{CutOrCompress: []string{"采购、开票和安装步骤合并带过"}},
			AntiAIPlan: domain.AntiAIExecutionPlan{
				SentenceRhythmPolicy: "盘问处短促，重新评价时允许句子延迟落点，作出选择后收短。",
				RiskSignals:          []string{"legacy-risk-list-must-not-leak"},
			},
		},
	}

	contract := newDraftRenderPacket(plan).LiteraryRenderContract
	if contract.Source != "legacy_projection" || contract.Focalizer != "林澈" || contract.KnowledgeBoundary == "" {
		t.Fatalf("legacy plan did not receive a useful literary contract: %#v", contract)
	}
	serialized, err := json.Marshal(contract)
	if err != nil {
		t.Fatal(err)
	}
	text := string(serialized)
	for _, want := range []string{"literary-rendering#focalization-boundary", "literary-rendering#emotion-appraisal", "literary-rendering#syntax-rhythm", "literary-rendering#scene-summary", "literary-rendering#dialogue-subtext"} {
		if !strings.Contains(text, want) {
			t.Fatalf("legacy projection missing source %q: %s", want, text)
		}
	}
	for _, leaked := range []string{"legacy-evidence-must-not-leak", "legacy-risk-list-must-not-leak"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("legacy projection leaked planning dossier field %q: %s", leaked, text)
		}
	}
}

func TestCompactDraftAIVoiceRulesExcludesPlanningAdvice(t *testing.T) {
	analysis := domain.AIVoiceAnalysis{RedFlags: []domain.AIVoiceRedFlag{
		{Rule: "chapter_function_repetition", Severity: "warning"}, // legacy artifact
		{Rule: "future_scene_shape", Severity: "info"},
		{Rule: "dialogue_conveyor_overuse", Severity: "warning"},
	}}
	got := compactDraftAIVoiceRules(analysis, 4)
	if len(got) != 1 || got[0] != "dialogue_conveyor_overuse" {
		t.Fatalf("rewrite packet should contain only current-chapter actionable rules, got %#v", got)
	}
}

func TestDraftRenderPacketKeepsRelationshipLensButDropsDialogueChoreography(t *testing.T) {
	plan := domain.ChapterPlan{CausalSimulation: domain.ChapterCausalSimulation{
		DialogueBlueprints: []domain.DialogueSceneBlueprint{
			{SceneID: "dinner", DialogueMode: "public_confrontation", RelationshipFrame: "亲友争夺体面"},
			{SceneID: "system", DialogueMode: "mediated_exchange", RelationshipFrame: "用户与系统"},
			{SceneID: "phone", DialogueMode: "mediated_exchange", RelationshipFrame: "朋友隔着电话互相试探"},
			{SceneID: "supplier", DialogueMode: "logistics_under_stress", RelationshipFrame: "付款方与供货方"},
			{SceneID: "review", DialogueMode: "status_report", RelationshipFrame: "旧识在职责与关心之间"},
			{SceneID: "settlement", DialogueMode: "mediated_exchange", RelationshipFrame: "系统结算"},
			{SceneID: "payoff", DialogueMode: "private_reconciliation", RelationshipFrame: "朋友终于停止试探", ExitBeat: "对方替主角留了门"},
		},
	}}

	packet := newDraftRenderPacket(plan)
	if len(packet.DialogueScenes) != 0 {
		t.Fatalf("turn-level dialogue blueprints must stay out of prose context: %#v", packet.DialogueScenes)
	}
	if len(packet.RelationshipLenses) != 1 || !strings.Contains(packet.RelationshipLenses[0].CurrentBond, "朋友") {
		t.Fatalf("one relationship-level intention should survive: %#v", packet.RelationshipLenses)
	}
}

func TestDraftRenderPacketKeepsEightMeaningfulFirstEntrances(t *testing.T) {
	visuals := make([]domain.CharacterVisualDesign, 0, 9)
	kits := make([]domain.CharacterKitEntry, 0, 9)
	for i, name := range []string{"主角", "女主", "父亲", "母亲", "亲戚", "摊主", "年轻亲戚", "师傅", "离屏角色"} {
		visuals = append(visuals, domain.CharacterVisualDesign{
			Character: name, Silhouette: "轮廓", FaceAndHair: "脸发", ClothingStyle: "衣着",
			BodyLanguage: "动作", SignatureObject: "标志物", FirstImpression: "第一印象",
			StatusWear: "状态", SceneUse: "场景用法",
		})
		kits = append(kits, domain.CharacterKitEntry{Character: name, FirstAppearance: i < 8})
	}
	packet := newDraftRenderPacket(domain.ChapterPlan{CausalSimulation: domain.ChapterCausalSimulation{
		VisualDesign: visuals,
		CharacterKit: kits,
	}})
	if len(packet.VisualCards) != 8 {
		t.Fatalf("first chapter entrances were silently dropped: %#v", packet.VisualCards)
	}
	for i, card := range packet.VisualCards {
		if !card.FirstAppearance || card.Character != kits[i].Character {
			t.Fatalf("visual card %d lost first-appearance priority: %#v", i, packet.VisualCards)
		}
	}
}

func TestDraftRenderPacketSamplesCandidateBeatsAcrossChapterArc(t *testing.T) {
	plan := domain.ChapterPlan{CausalSimulation: domain.ChapterCausalSimulation{
		ReaderRetentionPlan: domain.ReaderRetentionPlan{SurfaceBeats: []domain.RetentionSurfaceBeat{
			{MustShow: "opening"},
			{MustShow: "setup"},
			{MustShow: "obstacle"},
			{MustShow: "turn"},
			{MustShow: "payoff"},
		}},
	}}

	packet := newDraftRenderPacket(plan)
	if len(packet.CandidateBeats) != 0 {
		t.Fatalf("candidate menu duplicates mandatory outcomes and should stay out of prose context: %#v", packet.CandidateBeats)
	}
}

func TestDraftRenderPacketKeepsHumorPlanOutOfProseObligations(t *testing.T) {
	plan := domain.ChapterPlan{CausalSimulation: domain.ChapterCausalSimulation{
		EntertainmentPlan: domain.ReaderEntertainmentPlan{
			HumorBeats: []string{"opening joke", "front-loaded echo", "closing callback"},
		},
	}}

	packet := newDraftRenderPacket(plan)
	if len(packet.EntertainmentPlan.HumorBeats) != 0 {
		t.Fatalf("prescribed jokes must remain optional planning evidence: %#v", packet.EntertainmentPlan.HumorBeats)
	}
}

func TestDraftRenderPacketSynthesizesRelationshipLensFromStrongestDialogue(t *testing.T) {
	plan := domain.ChapterPlan{CausalSimulation: domain.ChapterCausalSimulation{
		DialogueBlueprints: []domain.DialogueSceneBlueprint{
			{
				SceneID: "opening", DialogueMode: "public_confrontation", RelationshipFrame: "三人把时间与车辆边界说清",
				DialogueObjective: "立刻出发", ExitBeat: "众人上车",
			},
			{
				SceneID: "supplier", DialogueMode: "logistics_under_stress", RelationshipFrame: "付款方与供货方",
				DialogueObjective: "核对付款流程", ExitBeat: "完成开票",
			},
			{SceneID: "hall", DialogueMode: "public_confrontation", RelationshipFrame: "父子还在互相防备"},
			{
				SceneID: "door", DialogueMode: "private_reconciliation", RelationshipFrame: "旧友一边试探一边护短",
				DialogueObjective: "确认对方是否还愿意信任自己", ExitBeat: "对方没有追问，却替他留了门",
				TurnProgression: []domain.DialogueTurnDesign{{Speaker: "旧友", ActionBeat: "relationship-action-leak"}},
			},
		},
	}}

	packet := newDraftRenderPacket(plan)
	if len(packet.RelationshipLenses) != 1 {
		t.Fatalf("expected one synthesized relationship lens: %#v", packet.RelationshipLenses)
	}
	lens := packet.RelationshipLenses[0]
	if len(lens.Pair) != 0 || lens.CurrentBond != "旧友一边试探一边护短" ||
		lens.EmotionalWant != "确认对方是否还愿意信任自己" || lens.NextEmotionalBeat != "对方没有追问，却替他留了门" {
		t.Fatalf("relationship lens should use only the strongest dialogue's relationship fields: %#v", lens)
	}
	raw, err := json.Marshal(packet)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "turn_progression") || strings.Contains(string(raw), "relationship-action-leak") {
		t.Fatalf("relationship fallback leaked dialogue choreography: %s", raw)
	}
}

func TestRenderRequiredOutcomesCollapsesProcessRecipesAndOutlineDuplicates(t *testing.T) {
	plan := domain.ChapterPlan{
		Goal: "完整兑现本章大纲核心事件：林澈返乡饭桌被亲戚阴阳失业",
		Hook: "手机弹出县城花钱系统绑定提示",
		Contract: domain.ChapterContract{RequiredBeats: []string{
			"手机向林澈显示县城花钱系统绑定成功，额度一百万元，限定用于青山县内新增、真实、合规、可核验的消费；林澈用旧债等两至三个短动作验证边界，旧债测试被明确拒绝，并保留一次真实迟疑。",
			"系统给出河畔夜市不低于三千元真实改善消费的首笔任务；林澈亲自到场复核入口照明、价目辨识和安全用电缺口。",
			"系统依据真实改善与成交完成阶段核验，解锁五万元夜市小额改善额度，并追加二十四小时内取得十家摊主同意、完成至少五十笔真实交易的目标。",
			"必须完整兑现大纲核心事件：林澈返乡饭桌被亲戚阴阳失业",
			"必须兑现大纲钩子；若现有章节契约已将其前移，则作为中段转折而非强行改写章末：手机弹出县城花钱系统绑定提示",
			"系统绑定，额度一百万元，只限青山县真实合规消费。",
			"首笔任务为河畔夜市不低于3000元真实改善消费。",
			"系统解锁5万元夜市小额改善额度，追加24小时十家摊主、至少五十笔交易目标。",
		}},
	}

	got := RenderRequiredOutcomes(plan)
	if len(got) != 3 {
		t.Fatalf("required outcomes should collapse to 3 result beats, got %d: %#v", len(got), got)
	}
	joined := strings.Join(got, "\n")
	for _, unwanted := range []string{"两至三个短动作", "必须完整兑现", "若现有章节契约"} {
		if strings.Contains(joined, unwanted) {
			t.Fatalf("process/meta instruction leaked into prose outcomes: %s", joined)
		}
	}
	for _, want := range []string{"系统绑定", "首笔任务", "系统解锁"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("result beat %q missing: %s", want, joined)
		}
	}
}

func TestRenderRequiredOutcomesKeepsEventWhenTrendLiteralIsEmbedded(t *testing.T) {
	plan := domain.ChapterPlan{
		Contract: domain.ChapterContract{RequiredBeats: []string{
			"饭桌上亲戚挤兑林澈；赵航以‘呱，’起头吐槽，林澈最终离席。",
		}},
		CausalSimulation: domain.ChapterCausalSimulation{
			TrendLanguage: []domain.TrendLanguagePlan{{Item: "呱，……"}},
		},
	}
	got := RenderRequiredOutcomes(plan)
	if len(got) != 1 || got[0] != "饭桌上亲戚挤兑林澈" || strings.Contains(got[0], "呱") {
		t.Fatalf("prose outcome should keep the event but drop prescribed style/choreography: %#v", got)
	}
}

func TestRenderRequiredOutcomesCapsLegacyChecklistPlans(t *testing.T) {
	plan := domain.ChapterPlan{Contract: domain.ChapterContract{RequiredBeats: []string{
		"开场结果", "运输结果", "摊主甲结果", "系统奖励到账并推动关系变化", "逐笔票据核查", "章末结果",
	}}}
	got := RenderRequiredOutcomes(plan)
	if len(got) != 3 || got[0] != "开场结果" || got[1] != "系统奖励到账并推动关系变化" || got[len(got)-1] != "章末结果" {
		t.Fatalf("legacy checklist should keep opening, human payoff and ending: %#v", got)
	}
}

func TestNonDraftProfileKeepsPlanningContext(t *testing.T) {
	plan := &domain.ChapterPlan{Chapter: 2, CausalSimulation: domain.ChapterCausalSimulation{ProtagonistDecision: "继续经营"}}
	result := map[string]any{"chapter_plan": plan, "causal_simulation": plan.CausalSimulation}
	applyChapterContextProfile(result, "planning")
	if result["chapter_plan"] != plan {
		t.Fatal("planning profile was unexpectedly rewritten")
	}
	if _, exists := result["causal_simulation"]; !exists {
		t.Fatal("planning profile lost causal_simulation")
	}
}

func TestPlanningProfileUsesProjectionWithoutFullCharacterDecisions(t *testing.T) {
	result := map[string]any{
		"outline": []string{"full outline"},
		"working_memory": map[string]any{
			"current_chapter_outline": "keep",
			"progression_snapshot":    "drop",
			"character_stage_records": "keep",
		},
		"chapter_world_simulation": map[string]any{
			"status":                 "ready",
			"simulation_id":          "sim-2",
			"character_decisions":    []domain.CharacterWorldDecision{{Character: "沈知遥"}},
			"protagonist_projection": domain.ProtagonistDecisionProjection{Protagonist: "林澈", ChosenDecision: "继续试点"},
		},
	}
	applyChapterContextProfile(result, "planning")
	if _, exists := result["outline"]; exists {
		t.Fatal("planning profile should use current/future outline window, not the full outline")
	}
	world := result["chapter_world_simulation"].(map[string]any)
	if _, exists := world["character_decisions"]; exists {
		t.Fatal("planning profile leaked full off-screen character decisions")
	}
	if _, exists := world["protagonist_projection"]; !exists {
		t.Fatal("planning profile lost protagonist projection")
	}
	working := result["working_memory"].(map[string]any)
	if _, exists := working["progression_snapshot"]; exists {
		t.Fatal("planning profile kept duplicate project progression")
	}
	if _, exists := working["character_stage_records"]; exists {
		t.Fatal("planning profile should trust the finalized protagonist projection instead of repeating all character stages")
	}
}

func TestWorldSimulationProfileKeepsCharacterStateAndDropsWritingMaterial(t *testing.T) {
	result := map[string]any{
		"outline":                  "drop",
		"references":               "drop",
		"literary_rendering_cards": "drop",
		"reference_pack": map[string]any{
			"literary_rendering_cards": "drop",
			"other":                    "keep",
		},
		"working_memory": map[string]any{
			"current_chapter_outline": "keep",
			"character_stage_records": "keep",
			"chapter_world_deltas":    "drop",
			"side_character_journeys": "drop",
		},
		"episodic_memory": map[string]any{"characters": "keep"},
	}
	applyChapterContextProfile(result, "world_simulation")
	for _, key := range []string{"outline", "references", "literary_rendering_cards"} {
		if _, exists := result[key]; exists {
			t.Fatalf("world simulation profile kept %s", key)
		}
	}
	pack := result["reference_pack"].(map[string]any)
	if _, exists := pack["literary_rendering_cards"]; exists {
		t.Fatal("world simulation profile kept compact prose craft catalog")
	}
	if pack["other"] != "keep" {
		t.Fatal("world simulation profile removed unrelated reference_pack state")
	}
	working := result["working_memory"].(map[string]any)
	if working["current_chapter_outline"] != "keep" || working["character_stage_records"] != "keep" {
		t.Fatal("world simulation profile lost current chapter or character state")
	}
	if _, exists := working["chapter_world_deltas"]; exists {
		t.Fatal("world simulation profile kept duplicate chapter deltas")
	}
}
