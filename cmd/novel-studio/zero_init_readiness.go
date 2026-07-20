package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
	buildversion "github.com/chenhongyang/novel-studio/internal/version"
)

func assessZeroInitReadiness(dir string, ragStats zeroInitRAGStats) zeroInitReadiness {
	required := []string{
		"premise.md", "outline.json", "characters.json", "world_rules.json", "book_world.json", "book_world.md",
		"meta/user_rules.json",
		"meta/simulation_restart_policy.json", "meta/simulation_restart_policy.md",
		"meta/world_foundation.json", "meta/world_foundation.md", "meta/zero_chapter_context_manifest.json", "meta/initial_character_dynamics.json", "meta/initial_resource_ledger.json",
		"relationship_state.initial.json", "foreshadow_ledger.initial.json", "meta/initial_review_lessons.md",
		"meta/character_return_plan.json", "meta/crowd_role_policy.json", "meta/prewrite_storycraft_plan.json", "meta/prewrite_storycraft_plan.md",
		"meta/world_background_plan.json", "meta/world_background_plan.md",
		"meta/story_time_contract.json", "meta/story_calendar.json",
		"drafts/01.zero_init.plan.json", "meta/ch01_zero_init_plan.md",
	}
	var missing []string
	for _, rel := range required {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			missing = append(missing, rel)
		}
	}
	var issues []string
	var warnings []string
	st := store.NewStore(dir)
	if policy, err := st.LoadSimulationRestartPolicy(); err == nil && policy != nil && policy.Active {
		if !zeroRestartPolicyHasTimeSources(policy) {
			issues = append(issues, "simulation_restart_policy.allowed_seed_sources 缺少 story_time_contract/story_calendar")
		}
		if progress, perr := st.Progress.Load(); perr == nil && progress != nil {
			if len(progress.CompletedChapters) > 0 && strings.TrimSpace(progress.GenerationID) != strings.TrimSpace(policy.GenerationID) {
				warnings = append(warnings, fmt.Sprintf("meta/progress.json 仍有旧 completed_chapters=%d 且 generation_id=%q；正式重启写第1章前请运行 --zero-init --reset-simulation-state，或确认已手动切换活动状态。", len(progress.CompletedChapters), progress.GenerationID))
			}
		}
	} else if err != nil {
		issues = append(issues, "meta/simulation_restart_policy.json 读取失败")
	}
	if entries, err := os.ReadDir(filepath.Join(dir, "meta", "characters")); err != nil {
		issues = append(issues, "meta/characters 角色独立档案目录缺失")
	} else {
		hasDossier := false
		for _, entry := range entries {
			if entry.IsDir() {
				if _, err := os.Stat(filepath.Join(dir, "meta", "characters", entry.Name(), "dossier.json")); err == nil {
					hasDossier = true
					break
				}
			}
		}
		if !hasDossier {
			issues = append(issues, "meta/characters 下缺少角色独立 dossier.json")
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "meta", "prewrite_storycraft_plan.json")); err == nil && !zeroExistingStorycraftReady(dir) {
		issues = append(issues, "meta/prewrite_storycraft_plan.json 缺少人物弧测试/声口卡/对话场景蓝图/读者奖励/证据回收/章末后果/休眠角色/现实支撑/情感逻辑/关系情感/视觉设计")
	}
	if _, err := os.Stat(filepath.Join(dir, "meta", "world_background_plan.json")); err == nil && !zeroExistingWorldBackgroundReady(dir) {
		issues = append(issues, "meta/world_background_plan.json 缺少世界背景层/信息差/潜规则/谣言/仪式日历/结构资源/宇宙观/矛盾网/叙事张力矩阵")
	}
	var plan domain.ChapterPlan
	if data, err := os.ReadFile(filepath.Join(dir, "drafts", "01.zero_init.plan.json")); err == nil {
		if err := json.Unmarshal(data, &plan); err != nil {
			issues = append(issues, "drafts/01.zero_init.plan.json 不是有效 ChapterPlan JSON")
		} else {
			issues = append(issues, zeroValidateChapterPlan(plan)...)
		}
	}
	if ragStats.Enabled && ragStats.Chunks == 0 {
		issues = append(issues, "RAG 已启用但没有生成 chunk")
	}
	// Task 051：initial_character_dynamics 必须覆盖 主角∪FirstCast∪core/important 全员（阻塞）。
	issues = append(issues, zeroCheckDynamicsCoverage(dir)...)
	issues = append(issues, zeroCheckStoryTimeContract(dir)...)
	if _, err := tools.ValidateZeroInitUserRules(dir); err != nil {
		issues = append(issues, fmt.Sprintf("user_rules 写前合同无效：%v", err))
	}
	issues = append(issues, zeroCheckPacingWordContract(dir)...)
	issues = append(issues, zeroCheckReturnPlanAlignment(dir)...)
	issues = append(issues, zeroCheckForbiddenTopicContamination(dir)...)
	// Task 053：模板同质与声口细字段缺失（warning，不阻塞——特化由 Architect 做）。
	// Task 054：core/important 角色 psych 缺失（warning，保持 psych 可选口径）。
	warnings = append(warnings, zeroCheckTemplateHomogeneity(dir)...)
	warnings = append(warnings, zeroCheckPsychCoverage(dir)...)

	readiness := zeroInitReadiness{
		Ready:            len(missing) == 0 && len(issues) == 0,
		SchemaVersion:    zeroReadinessSchemaVersion,
		GeneratorVersion: buildversion.Resolve(buildversion.Info{Version: version}).Version,
		Missing:          missing,
		Issues:           issues,
		Warnings:         warnings,
		StoryTime:        zeroStoryTimeEvidence(dir),
		RAG:              ragStats,
		GeneratedAt:      time.Now().Format(time.RFC3339),
		Path:             filepath.Join(dir, "meta", "first_chapter_generation_readiness.md"),
	}
	return readiness
}

func zeroCheckPacingWordContract(dir string) []string {
	st := store.NewStore(dir)
	snap, err := st.UserRules.Load()
	if err != nil {
		return []string{"meta/user_rules.json 读取失败，无法校验唯一字数口径"}
	}
	if snap == nil || snap.Structured.ChapterWords == nil {
		return nil
	}
	contract, err := st.Methodology.LoadPacingContract()
	if err != nil {
		return []string{"meta/pacing_contract.json 读取失败，无法校验唯一字数口径"}
	}
	if contract == nil {
		return nil
	}
	want := snap.Structured.ChapterWords
	if contract.ChapterWordMin != want.Min || contract.ChapterWordMax != want.Max {
		return []string{fmt.Sprintf(
			"pacing_contract 字数=%d-%d 与 user_rules.chapter_words=%d-%d 冲突；user_rules 必须是唯一字数口径",
			contract.ChapterWordMin, contract.ChapterWordMax, want.Min, want.Max,
		)}
	}
	return nil
}

func zeroCheckReturnPlanAlignment(dir string) []string {
	st := store.NewStore(dir)
	chars, err := st.Characters.Load()
	if err != nil || len(chars) == 0 {
		return nil
	}
	outline, _ := zeroAuthoritativeOutline(st)
	project := zeroInitProject{
		Characters:    chars,
		Outline:       outline,
		FirstMentions: zeroCharacterFirstMentions(outline, chars),
	}
	data, err := os.ReadFile(filepath.Join(dir, "meta", "character_return_plan.json"))
	if err != nil {
		return nil // required/missing reports absence
	}
	var actual map[string]domain.CharacterReturnPlan
	if err := json.Unmarshal(data, &actual); err != nil {
		return []string{"meta/character_return_plan.json 不是有效 JSON"}
	}
	var issues []string
	for _, c := range chars {
		name := strings.TrimSpace(c.Name)
		if name == "" {
			continue
		}
		first := project.FirstMentions[name]
		wantChapter := first
		if wantChapter == 0 && zeroIsPrimaryProtagonist(project, c) {
			wantChapter = 1
		}
		wantPriority := zeroReturnPriority(project, c, first)
		got, ok := actual[name]
		if !ok {
			issues = append(issues, fmt.Sprintf("character_return_plan 缺少角色 %s", name))
			continue
		}
		if got.SuggestedChapter != wantChapter || got.ReturnPriority != wantPriority {
			issues = append(issues, fmt.Sprintf(
				"character_return_plan[%s] 与当前大纲首次实际使用不一致：chapter=%d priority=%s，期望 chapter=%d priority=%s",
				name, got.SuggestedChapter, got.ReturnPriority, wantChapter, wantPriority,
			))
		}
	}
	return issues
}

func zeroAuthoritativeOutline(st *store.Store) ([]domain.OutlineEntry, error) {
	if st == nil {
		return nil, fmt.Errorf("outline store is nil")
	}
	if layered, err := st.Outline.LoadLayeredOutline(); err == nil && len(layered) > 0 {
		if flat := domain.FlattenOutline(layered); len(flat) > 0 {
			return flat, nil
		}
	}
	return st.Outline.LoadOutline()
}

func zeroCheckForbiddenTopicContamination(dir string) []string {
	st := store.NewStore(dir)
	premise, _ := st.Outline.LoadPremise()
	source := premise
	if snap, err := st.UserRules.Load(); err == nil && snap != nil {
		source += "\n" + snap.Preferences
	}
	markers := []string{"古代", "官署", "旧案", "黑市", "导师", "诡异", "恐怖", "末世", "克系", "邪神", "收容", "灵异", "死亡", "失踪", "异化", "附身", "传送"}
	var forbidden []string
	for _, marker := range markers {
		if zeroSourceExplicitlyForbids(source, marker) {
			forbidden = append(forbidden, marker)
		}
	}
	if len(forbidden) == 0 {
		return nil
	}
	files := []string{
		"meta/world_foundation.json",
		"meta/world_background_plan.json",
		"meta/prewrite_storycraft_plan.json",
		"drafts/01.zero_init.plan.json",
	}
	var issues []string
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			continue
		}
		text := string(data)
		for _, marker := range forbidden {
			if zeroHasPositiveTopicUse(text, marker) {
				issues = append(issues, fmt.Sprintf("%s 主动注入了用户明确排除的题材元素 %q", rel, marker))
			}
		}
	}
	return issues
}

func zeroSourceExplicitlyForbids(text, marker string) bool {
	for _, clause := range strings.FieldsFunc(text, func(r rune) bool {
		return strings.ContainsRune("。；!?！？\n", r)
	}) {
		if strings.Contains(clause, marker) && zeroContainsAny(clause, "不写", "不要", "避免", "严禁", "禁止", "不得", "排除", "非本书题材") {
			return true
		}
	}
	return false
}

func zeroHasPositiveTopicUse(text, marker string) bool {
	for _, line := range strings.Split(text, "\n") {
		idx := strings.Index(line, marker)
		for idx >= 0 {
			prefix := line[:idx]
			if !zeroContainsAny(strings.ToLower(prefix), "forbidden", "do_not_use", "boundary") &&
				!zeroContainsAny(prefix, "不写", "不要", "避免", "严禁", "禁止", "不得", "不能", "不制造", "无") {
				return true
			}
			next := idx + len(marker)
			if next >= len(line) {
				break
			}
			rest := line[next:]
			rel := strings.Index(rest, marker)
			if rel < 0 {
				break
			}
			idx = next + rel
		}
	}
	return false
}

func zeroStoryTimeEvidence(dir string) zeroInitStoryTimeEvidence {
	st := store.NewStore(dir)
	contract, err := st.WorldSim.LoadStoryTimeContract()
	if err != nil || contract == nil {
		return zeroInitStoryTimeEvidence{}
	}
	evidence := zeroInitStoryTimeEvidence{
		Validated:              true,
		Source:                 contract.Source,
		TargetChapters:         contract.TargetChapters,
		DurationDaysMin:        contract.DurationDaysMin,
		DurationDaysMax:        contract.DurationDaysMax,
		NominalDaysPerChapter:  contract.NominalDaysPerChapter,
		ArcScheduleEntries:     len(contract.ArcSchedule),
		ChapterScheduleEntries: len(contract.ChapterSchedule),
		CoreDigest:             contract.CoreDigest,
		ScheduleDigest:         contract.ScheduleDigest,
	}
	if calendar, loadErr := st.WorldSim.LoadStoryCalendar(); loadErr == nil && calendar != nil {
		evidence.CalendarSynced = math.Abs(calendar.DaysPerChapter-contract.NominalDaysPerChapter) <= 1e-9
	}
	return evidence
}

func zeroCheckStoryTimeContract(dir string) []string {
	st := store.NewStore(dir)
	contract, err := st.WorldSim.LoadStoryTimeContract()
	if err != nil {
		return []string{"meta/story_time_contract.json 校验失败：" + err.Error()}
	}
	if contract == nil {
		// 文件缺失由 required/missing 精确报告。
		return nil
	}
	progress, err := st.Progress.Load()
	if err != nil {
		return []string{"读取 meta/progress.json 以校验故事时间合同失败"}
	}
	target := zeroStoryTimeTargetChapters(st, progress, nil)
	var issues []string
	if target > 0 && contract.TargetChapters != target {
		issues = append(issues, fmt.Sprintf(
			"story_time_contract.target_chapters=%d 与全书大纲/进度目标=%d 不一致",
			contract.TargetChapters,
			target,
		))
	}
	calendar, err := st.WorldSim.LoadStoryCalendar()
	if err != nil {
		issues = append(issues, "meta/story_calendar.json 读取失败")
	} else if calendar != nil && math.Abs(calendar.DaysPerChapter-contract.NominalDaysPerChapter) > 1e-9 {
		issues = append(issues, fmt.Sprintf(
			"story_calendar.days_per_chapter=%.6f 未从 story_time_contract.nominal_days_per_chapter=%.6f 导出",
			calendar.DaysPerChapter,
			contract.NominalDaysPerChapter,
		))
	}
	return issues
}

func zeroValidateChapterPlan(plan domain.ChapterPlan) []string {
	var issues []string
	if plan.Chapter != 1 {
		issues = append(issues, "zero-init plan 必须是第 1 章")
	}
	sim := plan.CausalSimulation
	if len(sim.ContextSources) == 0 {
		issues = append(issues, "causal_simulation.context_sources 不能为空")
	}
	if !zeroContextSourceContains(sim.ContextSources, "world_foundation") {
		issues = append(issues, "causal_simulation.context_sources 缺少 world_foundation")
	}
	if !zeroContextSourceContains(sim.ContextSources, "story_time_contract") {
		issues = append(issues, "causal_simulation.context_sources 缺少 story_time_contract")
	}
	if !zeroContextSourceContains(sim.ContextSources, "character_dossiers") {
		issues = append(issues, "causal_simulation.context_sources 缺少 character_dossiers")
	}
	if !zeroContextSourceContains(sim.ContextSources, "simulation_restart_policy") {
		issues = append(issues, "causal_simulation.context_sources 缺少 simulation_restart_policy")
	}
	if !zeroContextSourceContains(sim.ContextSources, "world_background_plan") {
		issues = append(issues, "causal_simulation.context_sources 缺少 world_background_plan")
	}
	if !zeroContextSourceContains(sim.ContextSources, "dialogue_writing") {
		issues = append(issues, "causal_simulation.context_sources 缺少 dialogue_writing")
	}
	if len(sim.WritingNorms) == 0 {
		issues = append(issues, "causal_simulation.writing_norms_applied 不能为空")
	}
	for i, norm := range sim.WritingNorms {
		if norm.Source == "" || len(norm.RuleFocus) == 0 || norm.ChapterApplication == "" || len(norm.ProofTargets) == 0 || norm.FailureRisk == "" {
			issues = append(issues, fmt.Sprintf("causal_simulation.writing_norms_applied[%d] 未补足", i))
		}
	}
	if !zeroAntiAIPlanReady(sim.AntiAIPlan) {
		issues = append(issues, "causal_simulation.anti_ai_execution_plan 未补足")
	}
	if len(sim.ExternalRefs) == 0 {
		issues = append(issues, "causal_simulation.external_reference_plan 不能为空")
	}
	for i, ref := range sim.ExternalRefs {
		if ref.QueryOrNeed == "" || ref.SourceType == "" || len(ref.SourceRefs) == 0 || ref.RetrievedAt == "" || ref.FreshnessRequirement == "" || len(ref.UsableDetails) == 0 || ref.TransformationRule == "" || len(ref.DoNotUse) == 0 {
			issues = append(issues, fmt.Sprintf("causal_simulation.external_reference_plan[%d] 未补足", i))
		}
	}
	if len(sim.TrendLanguage) == 0 {
		issues = append(issues, "causal_simulation.trend_language_plan 不能为空")
	}
	for i, item := range sim.TrendLanguage {
		if item.Item == "" || item.SourceContext == "" || item.CharacterCarrier == "" || item.SceneFunction == "" || item.UsageBudget == "" || item.ForbiddenUsage == "" {
			issues = append(issues, fmt.Sprintf("causal_simulation.trend_language_plan[%d] 未补足", i))
		}
	}
	if !domain.CompleteReaderEntertainmentPlan(sim.EntertainmentPlan) {
		issues = append(issues, "causal_simulation.reader_entertainment_plan 未补足")
	}
	if len(sim.GroundingDetails) == 0 {
		issues = append(issues, "causal_simulation.grounding_details 不能为空")
	}
	for i, detail := range sim.GroundingDetails {
		if detail.Detail == "" || detail.SourceRef == "" || detail.TransformedAs == "" || detail.SceneAnchor == "" {
			issues = append(issues, fmt.Sprintf("causal_simulation.grounding_details[%d] 未补足", i))
		}
	}
	if len(sim.OffscreenStage) == 0 {
		issues = append(issues, "causal_simulation.offscreen_character_stage 不能为空")
	}
	for i, stage := range sim.OffscreenStage {
		if stage.Character == "" || stage.Location == "" || stage.Environment == "" || stage.CurrentAction == "" || stage.Pressure == "" || stage.Decision == "" || stage.KnowledgeBoundary == "" || stage.TimelineConsistency == "" {
			issues = append(issues, fmt.Sprintf("causal_simulation.offscreen_character_stage[%d] 未补足", i))
		}
	}
	if len(sim.InitialState) == 0 {
		issues = append(issues, "causal_simulation.initial_state 不能为空")
	}
	for _, state := range sim.InitialState {
		prefix := "initial_state"
		if state.Character != "" {
			prefix = "initial_state[" + state.Character + "]"
		}
		if !zeroKnowledgeLedgerReady(state.KnowledgeLedger) {
			issues = append(issues, prefix+".knowledge_ledger 未补足")
		}
		if state.DecisionFrame.DecisionRule == "" || len(state.DecisionFrame.AvailableOptions) == 0 {
			issues = append(issues, prefix+".decision_frame 未补足")
		}
		if state.EmotionAppraisal.TriggerEvent == "" || state.EmotionAppraisal.ActionPressure == "" {
			issues = append(issues, prefix+".emotion_appraisal 未补足")
		}
		if state.ArcAxis.Want == "" || state.ArcAxis.PressureTest == "" {
			issues = append(issues, prefix+".arc_axis 未补足")
		}
		if state.CompetenceStage == "" || len(state.SkillLimits) == 0 || len(state.PlausibleMistakes) == 0 || len(state.CorrectionTriggers) == 0 {
			issues = append(issues, prefix+".competence/mistake/correction 未补足")
		}
	}
	if len(sim.VoiceLogic) == 0 {
		issues = append(issues, "causal_simulation.voice_logic 不能为空")
	}
	for i, voice := range sim.VoiceLogic {
		if voice.Character == "" || voice.SceneObjective == "" || voice.HiddenSubtext == "" || voice.KnowledgeBoundary == "" ||
			voice.DictionAndRhythm == "" || voice.SentenceLength == "" || voice.PunctuationStyle == "" ||
			voice.LineBreakStyle == "" || voice.SubtextStrategy == "" || voice.SilenceOrAction == "" || voice.VoiceContrast == "" ||
			len(voice.DialogueFunctions) == 0 || len(voice.ForbiddenMoves) == 0 {
			issues = append(issues, fmt.Sprintf("causal_simulation.voice_logic[%d] 声口卡未补足", i))
		}
	}
	if len(sim.DialogueBlueprints) == 0 {
		issues = append(issues, "causal_simulation.dialogue_scene_blueprints 不能为空")
	}
	for i, blueprint := range sim.DialogueBlueprints {
		if blueprint.SceneID == "" || blueprint.DialogueMode == "" || blueprint.ModeReason == "" ||
			blueprint.ScenePressure == "" || blueprint.EmotionalTemperature == "" || blueprint.RelationshipFrame == "" ||
			blueprint.Medium == "" || blueprint.AudiencePresence.Present == "" ||
			blueprint.InfoAsymmetry.POVLacks == "" || blueprint.InfoAsymmetry.OtherHolds == "" ||
			blueprint.InfoAsymmetry.ReaderPosition == "" || blueprint.InfoAsymmetry.AsymmetryPlay == "" ||
			blueprint.ValueShift.Value == "" || blueprint.ValueShift.OpeningCharge == "" ||
			blueprint.ValueShift.TurnTrigger == "" || blueprint.ValueShift.ClosingCharge == "" ||
			blueprint.PowerTrajectory.OpeningHolder == "" || blueprint.PowerTrajectory.FlipBeat == "" ||
			blueprint.PowerTrajectory.ClosingHolder == "" ||
			blueprint.OpeningStrategy == "" || blueprint.FirstSpokenMoment == "" ||
			blueprint.LocationAnchor == "" || blueprint.POVState == "" || blueprint.InnerQuestion == "" ||
			blueprint.MemoryBridge == "" || blueprint.IdentityGrounding == "" || blueprint.DialogueObjective == "" ||
			blueprint.InterlocutorAgenda == "" || blueprint.ProtagonistResponseStrategy == "" ||
			len(blueprint.ObjectiveTactics) == 0 || len(blueprint.TurnProgression) == 0 ||
			blueprint.DirectnessPolicy == "" || blueprint.SubtextSource == "" || blueprint.EscalationPattern == "" ||
			blueprint.BeatDensity == "" || blueprint.SilencePolicy == "" || blueprint.InfoReleasePolicy == "" ||
			blueprint.ExpositionBudget == "" ||
			blueprint.SubtextAndPowerShift == "" || blueprint.ExitBeat == "" || len(blueprint.DoNotUse) == 0 {
			issues = append(issues, fmt.Sprintf("causal_simulation.dialogue_scene_blueprints[%d] 未补足", i))
			continue
		}
		for j, tactic := range blueprint.ObjectiveTactics {
			if tactic.Character == "" || tactic.ImmediateObjective == "" || tactic.Tactic == "" ||
				tactic.CounterTactic == "" || tactic.EmotionalLeak == "" || tactic.TurnResult == "" {
				issues = append(issues, fmt.Sprintf("causal_simulation.dialogue_scene_blueprints[%d].objective_tactics[%d] 未补足", i, j))
			}
		}
		for j, turn := range blueprint.TurnProgression {
			if turn.Speaker == "" || turn.SurfaceLineFunction == "" || turn.HiddenSubtext == "" ||
				(turn.NewInformation == "" && turn.PowerMove == "") || turn.ActionBeat == "" || turn.NextPressure == "" {
				issues = append(issues, fmt.Sprintf("causal_simulation.dialogue_scene_blueprints[%d].turn_progression[%d] 未补足", i, j))
			}
		}
	}
	if len(sim.CharacterArcTests) == 0 {
		issues = append(issues, "causal_simulation.character_arc_tests 不能为空")
	}
	if len(sim.EmotionalLogic) > 0 && len(sim.CharacterArcTests) < len(sim.EmotionalLogic) {
		issues = append(issues, "causal_simulation.character_arc_tests 未覆盖 emotional_logic 中的全部角色")
	}
	for i, arc := range sim.CharacterArcTests {
		if arc.Character == "" || arc.Want == "" || arc.CoreLie == "" || arc.Need == "" || arc.Truth == "" ||
			arc.PressureTest == "" || arc.FirstMistake == "" || arc.CorrectionSignal == "" || arc.ChapterEvidence == "" {
			issues = append(issues, fmt.Sprintf("causal_simulation.character_arc_tests[%d] 未补足", i))
		}
	}
	if !zeroReaderRewardReady(sim.ReaderRewardPlan) {
		issues = append(issues, "causal_simulation.reader_reward_plan 未补足")
	}
	if len(sim.EvidenceChains) == 0 {
		issues = append(issues, "causal_simulation.evidence_return_chains 不能为空")
	}
	for i, chain := range sim.EvidenceChains {
		if chain.OffscreenCharacter == "" || chain.Event == "" || chain.Evidence == "" || chain.ProtagonistAccess == "" ||
			chain.ReturnTiming == "" || chain.DistortionOrMisread == "" {
			issues = append(issues, fmt.Sprintf("causal_simulation.evidence_return_chains[%d] 未补足", i))
		}
	}
	if !zeroEndingContractReady(sim.EndingContract) {
		issues = append(issues, "causal_simulation.ending_consequence_contract 未补足")
	}
	if len(sim.DormantPolicy) == 0 {
		issues = append(issues, "causal_simulation.dormant_character_policy 不能为空")
	}
	for i, dormant := range sim.DormantPolicy {
		if dormant.Character == "" || dormant.Status == "" || dormant.Location == "" || dormant.NoChangeReason == "" ||
			dormant.TriggerCondition == "" || dormant.KnowledgeBoundary == "" || dormant.NextCheck == "" {
			issues = append(issues, fmt.Sprintf("causal_simulation.dormant_character_policy[%d] 未补足", i))
		}
	}
	if len(sim.RealitySupport) == 0 {
		issues = append(issues, "causal_simulation.reality_support_plan 不能为空")
	}
	for i, support := range sim.RealitySupport {
		if support.Domain == "" || support.SourceRef == "" || support.UsableDetail == "" || support.TransformedAs == "" ||
			support.ChapterUse == "" || len(support.ForbiddenDirectUse) == 0 {
			issues = append(issues, fmt.Sprintf("causal_simulation.reality_support_plan[%d] 未补足", i))
		}
	}
	if len(sim.EmotionalLogic) == 0 {
		issues = append(issues, "causal_simulation.emotional_logic 不能为空")
	}
	for i, emo := range sim.EmotionalLogic {
		if emo.Character == "" || emo.PhysiologicalState == "" || emo.ImmediateState == "" || emo.PrimaryEmotion == "" ||
			emo.CompositeEmotion == "" || emo.EmotionalTrigger == "" || emo.GoalAppraisal == "" || emo.BoundaryThreat == "" ||
			emo.RegulationStrategy == "" || emo.DefenseMechanism == "" || emo.CognitiveBias == "" || emo.ApproachAvoidance == "" ||
			emo.ShortLongTermTension == "" || emo.SelfRelationshipTension == "" || emo.ConsciousReason == "" || emo.HiddenReason == "" ||
			emo.MeaningNeed == "" || emo.Metacognition == "" || emo.EmotionLedAction == "" || emo.EventCompletionRole == "" ||
			len(emo.EvidenceInScene) == 0 {
			issues = append(issues, fmt.Sprintf("causal_simulation.emotional_logic[%d] 未补足", i))
		}
	}
	if len(sim.RelationshipArcs) == 0 {
		issues = append(issues, "causal_simulation.relationship_emotion_arcs 不能为空")
	}
	for i, arc := range sim.RelationshipArcs {
		if len(arc.Pair) < 2 || arc.RelationshipType == "" || arc.CurrentBond == "" || arc.EmotionalWant == "" ||
			arc.Fear == "" || arc.PowerBalance == "" || arc.IntimacyStage == "" || arc.TrustDebt == "" ||
			arc.ConflictTrigger == "" || arc.AttachmentOrLoveLanguage == "" || arc.Boundary == "" ||
			arc.RomancePotential == "" || arc.NextEmotionalBeat == "" || arc.ProtagonistKnowledgeBoundary == "" {
			issues = append(issues, fmt.Sprintf("causal_simulation.relationship_emotion_arcs[%d] 未补足", i))
		}
	}
	if len(sim.VisualDesign) == 0 {
		issues = append(issues, "causal_simulation.visual_design 不能为空")
	}
	for i, visual := range sim.VisualDesign {
		if visual.Character == "" || visual.Silhouette == "" || visual.FaceAndHair == "" || visual.ClothingStyle == "" ||
			visual.ColorPalette == "" || visual.BodyLanguage == "" || visual.SignatureObject == "" || visual.FirstImpression == "" ||
			visual.StatusWear == "" || visual.ChangeRule == "" || visual.SceneUse == "" || len(visual.DoNotUse) == 0 {
			issues = append(issues, fmt.Sprintf("causal_simulation.visual_design[%d] 未补足", i))
		}
	}
	if !hasWorldBackgroundLayers(sim.WorldLayers) {
		issues = append(issues, "causal_simulation.world_background_layers 未补足")
	}
	if len(sim.InformationLedger) == 0 {
		issues = append(issues, "causal_simulation.information_asymmetry 不能为空")
	}
	for i, info := range sim.InformationLedger {
		if info.Subject == "" || len(info.ReaderKnows) == 0 || len(info.ProtagonistKnows) == 0 ||
			len(info.CharacterKnows) == 0 || len(info.CharacterMistakes) == 0 || len(info.HiddenFromReader) == 0 ||
			info.RevealCondition == "" || info.TensionFunction == "" {
			issues = append(issues, fmt.Sprintf("causal_simulation.information_asymmetry[%d] 未补足", i))
		}
	}
	if len(sim.HiddenRules) == 0 {
		issues = append(issues, "causal_simulation.hidden_rule_pressure 不能为空")
	}
	for i, hidden := range sim.HiddenRules {
		if hidden.Domain == "" || hidden.VisibleRule == "" || hidden.HiddenRule == "" || hidden.CulturalNorm == "" ||
			hidden.WhoBenefits == "" || hidden.WhoPays == "" || hidden.ViolationCost == "" || hidden.SceneEvidence == "" {
			issues = append(issues, fmt.Sprintf("causal_simulation.hidden_rule_pressure[%d] 未补足", i))
		}
	}
	if len(sim.SocialMoodRumors) == 0 {
		issues = append(issues, "causal_simulation.social_mood_rumors 不能为空")
	}
	for i, rumor := range sim.SocialMoodRumors {
		if rumor.Group == "" || rumor.Mood == "" || rumor.Rumor == "" || rumor.Source == "" ||
			rumor.SpreadPath == "" || rumor.Reliability == "" || rumor.BehaviorEffect == "" || rumor.ProtagonistAccess == "" {
			issues = append(issues, fmt.Sprintf("causal_simulation.social_mood_rumors[%d] 未补足", i))
		}
	}
	if len(sim.RitualCalendar) == 0 {
		issues = append(issues, "causal_simulation.ritual_calendar 不能为空")
	}
	for i, window := range sim.RitualCalendar {
		if window.Time == "" || window.CalendarType == "" || window.RitualOrDeadline == "" || window.SocialMeaning == "" ||
			window.PracticalConstraint == "" || window.EmotionalCharge == "" || window.MissedCost == "" || window.SceneUse == "" {
			issues = append(issues, fmt.Sprintf("causal_simulation.ritual_calendar[%d] 未补足", i))
		}
	}
	if len(sim.StructuralResources) == 0 {
		issues = append(issues, "causal_simulation.structural_resources 不能为空")
	}
	for i, resource := range sim.StructuralResources {
		if resource.Resource == "" || resource.Controller == "" || resource.ScarcityReason == "" || resource.AccessRule == "" ||
			resource.BlackMarketOrInformalPath == "" || resource.PriceOrCost == "" || resource.PowerEffect == "" || resource.ChapterPressure == "" {
			issues = append(issues, fmt.Sprintf("causal_simulation.structural_resources[%d] 未补足", i))
		}
	}
	if len(sim.CosmologyChecks) == 0 {
		issues = append(issues, "causal_simulation.cosmology_checks 不能为空")
	}
	for i, check := range sim.CosmologyChecks {
		if check.Layer == "" || check.Rule == "" || check.Cost == "" || check.Boundary == "" ||
			check.ExceptionCondition == "" || check.Evidence == "" || check.FailureMode == "" {
			issues = append(issues, fmt.Sprintf("causal_simulation.cosmology_checks[%d] 未补足", i))
		}
	}
	if len(sim.ConflictWeb) == 0 {
		issues = append(issues, "causal_simulation.conflict_web 不能为空")
	}
	for i, conflict := range sim.ConflictWeb {
		if len(conflict.Parties) < 2 || conflict.ConflictType == "" || conflict.OpenGoal == "" ||
			conflict.HiddenAgenda == "" || conflict.ResourceStake == "" || conflict.InformationGap == "" ||
			conflict.TimePressure == "" || conflict.CurrentBalance == "" || conflict.Destabilizer == "" || conflict.NextEscalation == "" {
			issues = append(issues, fmt.Sprintf("causal_simulation.conflict_web[%d] 未补足", i))
		}
	}
	if !hasNarrativeTensionMatrix(sim.TensionMatrix) {
		issues = append(issues, "causal_simulation.narrative_tension_matrix 未补足")
	}
	if len(sim.CrowdRoles) == 0 {
		issues = append(issues, "causal_simulation.crowd_roles 不能为空")
	}
	if sim.ReviewRefinement.IterationLimit == 0 || len(sim.ReviewRefinement.TriggerSources) == 0 {
		issues = append(issues, "causal_simulation.review_refinement 未补足")
	}
	if len(sim.EnvironmentState) == 0 {
		issues = append(issues, "causal_simulation.environment_state 不能为空")
	}
	if !domain.CompleteLongformOpeningDesign(sim.LongformOpening) {
		issues = append(issues, "causal_simulation.longform_opening 未补足")
	}
	return issues
}

func zeroContextSourceContains(sources []string, needle string) bool {
	for _, src := range sources {
		if strings.Contains(strings.ToLower(src), strings.ToLower(needle)) {
			return true
		}
	}
	return false
}

func zeroExistingPlanReady(dir string) bool {
	var plan domain.ChapterPlan
	data, err := os.ReadFile(filepath.Join(dir, "drafts", "01.zero_init.plan.json"))
	if err != nil || json.Unmarshal(data, &plan) != nil {
		return false
	}
	return len(zeroValidateChapterPlan(plan)) == 0
}

func zeroExistingManifestReady(dir string) bool {
	var manifest struct {
		ContextSources []string       `json:"context_sources"`
		RAGPolicy      map[string]any `json:"rag_source_policy"`
	}
	data, err := os.ReadFile(filepath.Join(dir, "meta", "zero_chapter_context_manifest.json"))
	if err != nil || json.Unmarshal(data, &manifest) != nil {
		return false
	}
	if !zeroContextSourceContains(manifest.ContextSources, "simulation_restart_policy") {
		return false
	}
	if !zeroContextSourceContains(manifest.ContextSources, "prewrite_storycraft_plan") {
		return false
	}
	if !zeroContextSourceContains(manifest.ContextSources, "world_background_plan") {
		return false
	}
	if !zeroContextSourceContains(manifest.ContextSources, "story_time_contract") {
		return false
	}
	if manifest.RAGPolicy == nil {
		return false
	}
	if forbidden, ok := manifest.RAGPolicy["forbidden_dir_markers"].([]any); ok {
		for _, marker := range forbidden {
			if strings.Contains(fmt.Sprint(marker), "meta/resource_ledger") {
				return true
			}
		}
	}
	return false
}

func zeroExistingStorycraftReady(dir string) bool {
	var plan zeroPrewriteStorycraftPlan
	data, err := os.ReadFile(filepath.Join(dir, "meta", "prewrite_storycraft_plan.json"))
	if err != nil || json.Unmarshal(data, &plan) != nil {
		return false
	}
	return plan.Version >= 2 &&
		len(plan.ArcTests) > 0 &&
		len(plan.VoiceCards) > 0 &&
		len(plan.DialogueBlueprints) > 0 &&
		zeroReaderRewardReady(plan.ReaderReward) &&
		len(plan.EvidenceChains) > 0 &&
		zeroEndingContractReady(plan.EndingContract) &&
		len(plan.DormantPolicy) > 0 &&
		len(plan.RealitySupport) > 0 &&
		len(plan.EmotionalLogic) > 0 &&
		len(plan.RelationshipArcs) > 0 &&
		len(plan.VisualDesign) > 0
}

func zeroExistingWorldBackgroundReady(dir string) bool {
	var plan zeroWorldBackgroundPlan
	data, err := os.ReadFile(filepath.Join(dir, "meta", "world_background_plan.json"))
	if err != nil || json.Unmarshal(data, &plan) != nil {
		return false
	}
	return plan.Version >= 1 &&
		hasWorldBackgroundLayers(plan.Layers) &&
		len(plan.InformationLedger) > 0 &&
		len(plan.HiddenRules) > 0 &&
		len(plan.SocialMoodRumors) > 0 &&
		len(plan.RitualCalendar) > 0 &&
		len(plan.StructuralResources) > 0 &&
		len(plan.CosmologyChecks) > 0 &&
		len(plan.ConflictWeb) > 0 &&
		hasNarrativeTensionMatrix(plan.TensionMatrix)
}

func hasWorldBackgroundLayers(layers domain.WorldBackgroundLayersPlan) bool {
	return strings.TrimSpace(layers.PhysicalSpace) != "" &&
		strings.TrimSpace(layers.TimeLayer) != "" &&
		strings.TrimSpace(layers.SocialInstitution) != "" &&
		strings.TrimSpace(layers.CulturalNorm) != "" &&
		strings.TrimSpace(layers.RelationshipNetwork) != "" &&
		strings.TrimSpace(layers.EconomicResource) != "" &&
		strings.TrimSpace(layers.ConflictTension) != "" &&
		strings.TrimSpace(layers.SocialMood) != "" &&
		strings.TrimSpace(layers.CosmologyMetaRule) != "" &&
		strings.TrimSpace(layers.NarrativeMeta) != "" &&
		strings.TrimSpace(layers.EventActivation) != ""
}

func hasNarrativeTensionMatrix(matrix domain.NarrativeTensionMatrix) bool {
	return strings.TrimSpace(matrix.StabilityTurbulence) != "" &&
		strings.TrimSpace(matrix.ExplicitHiddenRules) != "" &&
		strings.TrimSpace(matrix.InformationGap) != "" &&
		strings.TrimSpace(matrix.TimePressurePreparation) != "" &&
		strings.TrimSpace(matrix.WhyEventNow) != "" &&
		strings.TrimSpace(matrix.ReaderQuestion) != "" &&
		strings.TrimSpace(matrix.POVBoundary) != ""
}

func zeroExistingRestartPolicyMatches(dir, generationID string) bool {
	var policy domain.SimulationRestartPolicy
	data, err := os.ReadFile(filepath.Join(dir, "meta", "simulation_restart_policy.json"))
	if err != nil || json.Unmarshal(data, &policy) != nil {
		return false
	}
	return strings.TrimSpace(policy.GenerationID) == strings.TrimSpace(generationID) &&
		zeroRestartPolicyHasTimeSources(&policy)
}

func zeroRestartPolicyHasTimeSources(policy *domain.SimulationRestartPolicy) bool {
	if policy == nil {
		return false
	}
	hasContract := false
	hasCalendar := false
	for _, source := range policy.AllowedSeedSources {
		source = filepath.ToSlash(strings.TrimSpace(source))
		hasContract = hasContract || source == "meta/story_time_contract.json"
		hasCalendar = hasCalendar || source == "meta/story_calendar.json"
	}
	return hasContract && hasCalendar
}

func zeroAntiAIPlanReady(plan domain.AntiAIExecutionPlan) bool {
	return len(plan.RiskSignals) > 0 &&
		len(plan.CounterMoves) > 0 &&
		plan.SentenceRhythmPolicy != "" &&
		plan.ObjectResponseBudget != "" &&
		plan.DialogueFunctionPlan != "" &&
		len(plan.ReviewChecks) > 0
}

func zeroReaderRewardReady(plan domain.ReaderRewardPlan) bool {
	return plan.ChapterWindow != "" &&
		plan.FirstChapterSmallWin != "" &&
		plan.NewDebtOrCost != "" &&
		plan.PayoffVisibility != "" &&
		plan.TrafficRisk != "" &&
		len(plan.RewardLadder) > 0
}

func zeroEndingContractReady(contract domain.EndingConsequenceContract) bool {
	return contract.EndingMode != "" &&
		contract.ConcreteAnchor != "" &&
		contract.Consequence != "" &&
		contract.NextChapterPull != "" &&
		contract.WhyNotUI != "" &&
		len(contract.ForbiddenEndings) > 0
}

func writeZeroInitReadiness(dir string, readiness zeroInitReadiness, _ bool) error {
	dependencies, err := tools.CaptureZeroInitFoundationDependencies(dir)
	if err != nil && readiness.Ready {
		return fmt.Errorf("capture zero-init foundation dependencies: %w", err)
	}
	receipt := struct {
		zeroInitReadiness
		FoundationDependencies map[string]string `json:"foundation_dependencies,omitempty"`
	}{
		zeroInitReadiness:      readiness,
		FoundationDependencies: dependencies,
	}
	if err := writeZeroJSON(filepath.Join(dir, "meta", "first_chapter_generation_readiness.json"), receipt, true); err != nil {
		return err
	}
	return writeZeroText(filepath.Join(dir, "meta", "first_chapter_generation_readiness.md"), renderZeroReadiness(readiness), true)
}

func writeZeroJSON(path string, v any, overwrite bool) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeZeroBytes(path, data, overwrite)
}

func writeZeroText(path, text string, overwrite bool) error {
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return writeZeroBytes(path, []byte(text), overwrite)
}

func writeZeroBytes(path string, data []byte, overwrite bool) error {
	if !overwrite {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func zeroShouldWriteArtifact(dir string, overwrite bool, rels ...string) bool {
	if overwrite {
		return true
	}
	for _, rel := range rels {
		if _, err := os.Stat(filepath.Join(dir, rel)); os.IsNotExist(err) {
			return true
		}
	}
	return false
}

func characterDossierRel(name, file string) string {
	return filepath.Join("meta", "characters", zeroSafePathName(name), file)
}

func zeroSafePathName(name string) string {
	name = strings.TrimSpace(name)
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_")
	name = replacer.Replace(name)
	if name == "" {
		return "unknown"
	}
	return name
}

func zeroRequiredDynamicFields() []string {
	return []string{"knowledge_ledger", "decision_frame", "relationship_contract", "emotion_appraisal", "arc_axis"}
}

func zeroKnowledgeLedgerReady(v domain.CharacterKnowledgeLedger) bool {
	return len(v.KnownFacts) > 0 && len(v.UnknownFacts) > 0 && len(v.EvidenceSeen) > 0 && len(v.ForbiddenKnowledge) > 0
}

func zeroInitialCharacters(project zeroInitProject) []domain.Character {
	protagonist := zeroProtagonist(project.Characters)
	var out []domain.Character
	seen := map[string]bool{}
	add := func(c domain.Character) {
		name := strings.TrimSpace(c.Name)
		if name == "" || seen[name] {
			return
		}
		out = append(out, c)
		seen[name] = true
	}
	add(protagonist)
	for _, c := range project.Characters {
		if zeroIsProtagonist(c) {
			add(c)
		}
	}
	for _, c := range project.Characters {
		if zeroIsProtagonist(c) {
			continue
		}
		if project.FirstCast[strings.TrimSpace(c.Name)] {
			add(c)
		}
	}
	// Task 051：覆盖集合扩到全部 core/important 角色（tier 为空按 important 口径）。
	// 配角没被第一章大纲点名也必须有 dynamics/voice——否则清跑项目 11 人只覆盖 1 人。
	for _, c := range project.Characters {
		if zeroIsProtagonist(c) {
			continue
		}
		switch strings.TrimSpace(c.Tier) {
		case "core", "important", "":
			add(c)
		}
	}
	// A secondary actor who is explicitly reserved in a later outline is still
	// part of the full-book simulation. If zero-init omits that actor, the
	// project-all authority guard can only freeze them forever when their first
	// real chapter arrives. Seed every named future participant now; dormant
	// actors remain off-screen until their first_mention boundary.
	for _, c := range project.Characters {
		if project.FirstMentions[strings.TrimSpace(c.Name)] > 0 {
			add(c)
		}
	}
	if len(out) == 0 && len(project.Characters) > 0 {
		add(project.Characters[0])
	}
	return out
}

func zeroFirstChapterCast(first domain.OutlineEntry, chars []domain.Character) map[string]bool {
	out := map[string]bool{}
	for _, c := range chars {
		name := strings.TrimSpace(c.Name)
		if name == "" {
			continue
		}
		if zeroOutlineEntryUsesCharacter(first, c) {
			out[name] = true
		}
	}
	return out
}

func zeroCharacterFirstMentions(outline []domain.OutlineEntry, chars []domain.Character) map[string]int {
	out := map[string]int{}
	for i, entry := range outline {
		for _, c := range chars {
			name := strings.TrimSpace(c.Name)
			if name == "" || out[name] != 0 {
				continue
			}
			if zeroOutlineEntryUsesCharacter(entry, c) {
				ch := entry.Chapter
				if ch <= 0 {
					ch = i + 1
				}
				out[name] = ch
			}
		}
	}
	primary := strings.TrimSpace(zeroProtagonist(chars).Name)
	if primary != "" && out[primary] == 0 {
		out[primary] = 1
	}
	return out
}

func zeroOutlineEntryUsesCharacter(entry domain.OutlineEntry, c domain.Character) bool {
	names := append([]string{strings.TrimSpace(c.Name)}, c.Aliases...)
	texts := append([]string{entry.Title, entry.CoreEvent, entry.Hook}, entry.Scenes...)
	for _, text := range texts {
		for _, clause := range strings.FieldsFunc(text, func(r rune) bool {
			return strings.ContainsRune("。；！？!?\n", r)
		}) {
			matched := false
			for _, name := range names {
				name = strings.TrimSpace(name)
				if name != "" && strings.Contains(clause, name) {
					matched = true
					break
				}
			}
			if !matched || zeroFutureAuthorOnlyClause(clause) {
				continue
			}
			return true
		}
	}
	return false
}

func zeroFutureAuthorOnlyClause(clause string) bool {
	return zeroContainsAny(clause, "后续", "将来", "日后", "未来", "下一章") &&
		zeroContainsAny(clause, "入场", "登场", "出场", "回归", "承接", "消费", "交给", "铺垫", "安排")
}

func zeroOutlineEntryText(entry domain.OutlineEntry) string {
	var parts []string
	parts = append(parts, entry.Title, entry.CoreEvent, entry.Hook)
	parts = append(parts, entry.Scenes...)
	return strings.Join(parts, "\n")
}

func zeroCounterpartForCharacter(project zeroInitProject, c domain.Character) string {
	protagonist := zeroProtagonist(project.Characters)
	if !zeroIsProtagonist(c) {
		return strings.TrimSpace(protagonist.Name)
	}
	for _, other := range project.Characters {
		name := strings.TrimSpace(other.Name)
		if name == "" || name == strings.TrimSpace(c.Name) {
			continue
		}
		if project.FirstCast[name] {
			return name
		}
	}
	return ""
}

// zeroCounterpartsForCharacter 返回角色的零章关系契约对手集：
//   - 非主角：契约对手是主角（单条）。
//   - 主角：对手是关键配角集合——优先 FirstCast（第一章点名出场），
//     再补 core/important 层配角，至多 5 个。主角是关系枢纽，不应因第一章
//     大纲未点名配角而契约为空（旧单对手逻辑的缺陷）。
func zeroCounterpartsForCharacter(project zeroInitProject, c domain.Character) []string {
	if !zeroIsProtagonist(c) {
		if cp := strings.TrimSpace(zeroProtagonist(project.Characters).Name); cp != "" {
			return []string{cp}
		}
		return nil
	}
	const maxProtagonistContracts = 5
	self := strings.TrimSpace(c.Name)
	seen := map[string]bool{}
	var out []string
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" || name == self || seen[name] || len(out) >= maxProtagonistContracts {
			return
		}
		seen[name] = true
		out = append(out, name)
	}
	// 第一优先：第一章点名出场的配角。
	for _, other := range project.Characters {
		if zeroIsProtagonist(other) {
			continue
		}
		if project.FirstCast[strings.TrimSpace(other.Name)] {
			add(other.Name)
		}
	}
	// 补充：core/important 层配角（tier 空按 important 口径）。
	for _, other := range project.Characters {
		if zeroIsProtagonist(other) {
			continue
		}
		switch strings.TrimSpace(other.Tier) {
		case "core", "important", "":
			add(other.Name)
		}
	}
	return out
}

func zeroOpeningPressureName(project zeroInitProject) string {
	if !zeroIsHorrorProject(project) {
		if title := strings.TrimSpace(project.FirstChapter.Title); title != "" {
			return title + "中的现实压力"
		}
		if zeroIsSecondAlgorithmProject(project) {
			return "第一章岗位、资源与选择压力"
		}
		return "第一章目标、资源与执行压力"
	}
	text := zeroOutlineEntryText(project.FirstChapter)
	switch {
	case strings.Contains(text, "夜租") || strings.Contains(text, "欠费"):
		return "夜租欠费单与门牌规则"
	case strings.Contains(text, "黑卡"):
		return "冥府黑卡交易规则"
	case strings.Contains(text, "合同") || strings.Contains(text, "契约"):
		return "第一章契约压力"
	default:
		return "第一章规则压力"
	}
}

func zeroReturnPriority(project zeroInitProject, c domain.Character, firstMention int) string {
	if zeroIsPrimaryProtagonist(project, c) || firstMention == 1 {
		return "required"
	}
	if firstMention > 1 && firstMention <= 8 {
		return "near_future"
	}
	if firstMention > 8 {
		return "planned_later"
	}
	if strings.EqualFold(c.Tier, "decorative") || strings.Contains(c.Role, "捧场") || strings.Contains(c.Role, "凑数") {
		return "dormant"
	}
	if c.Tier == "core" || c.Tier == "important" || c.Tier == "" {
		return "background_active"
	}
	return "optional"
}

func zeroReturnDueReason(project zeroInitProject, c domain.Character, firstMention int) string {
	if zeroIsPrimaryProtagonist(project, c) {
		return "主角必须进入第一章动态推演；其选择负责驱动本章规则压力。"
	}
	if firstMention == 1 {
		return "第一章大纲明确命名或安排其承担目标、压力、关系或信息功能。"
	}
	if firstMention > 1 {
		return fmt.Sprintf("第%d章大纲首次明确牵引；此前不得提前当作第一章关键人物使用。", firstMention)
	}
	return "当前规划窗未明确出场；仅保留为可升级候选，不进入第一章关键人物台账。"
}

func zeroInitProjectName(dir string) string {
	root := ragProjectRoot(dir)
	name := filepath.Base(root)
	if name == "." || name == string(filepath.Separator) || strings.TrimSpace(name) == "" {
		return "未命名项目"
	}
	return name
}

func sortZeroInitCharacters(chars []domain.Character) {
	score := func(c domain.Character) int {
		if zeroIsProtagonist(c) {
			return 0
		}
		if c.Tier == "core" {
			return 1
		}
		if strings.Contains(c.Role, "重要") || c.Tier == "important" {
			return 2
		}
		if c.Tier == "decorative" || strings.Contains(c.Role, "捧场") || strings.Contains(c.Role, "凑数") {
			return 4
		}
		return 3
	}
	sort.SliceStable(chars, func(i, j int) bool {
		if score(chars[i]) == score(chars[j]) {
			return chars[i].Name < chars[j].Name
		}
		return score(chars[i]) < score(chars[j])
	})
}

func zeroProtagonist(chars []domain.Character) domain.Character {
	for _, c := range chars {
		if zeroIsProtagonist(c) {
			return c
		}
	}
	if len(chars) > 0 {
		return chars[0]
	}
	return domain.Character{Name: "主角", Role: "主角"}
}

func zeroFirstNonProtagonistName(chars []domain.Character) string {
	for _, c := range chars {
		if !zeroIsProtagonist(c) && strings.TrimSpace(c.Name) != "" {
			return strings.TrimSpace(c.Name)
		}
	}
	return ""
}

func zeroIsProtagonist(c domain.Character) bool {
	role := strings.ToLower(strings.TrimSpace(c.Role))
	if strings.Contains(role, "主角团") || strings.Contains(role, "配角") ||
		strings.Contains(role, "父亲") || strings.Contains(role, "母亲") ||
		strings.Contains(role, "朋友") || strings.Contains(role, "闺蜜") {
		return false
	}
	return role == "主角" ||
		strings.Contains(role, "男主") ||
		strings.Contains(role, "女主") ||
		strings.Contains(role, "主人公") ||
		strings.Contains(role, "protagonist")
}

func zeroActionBias(c domain.Character) string {
	if len(c.Traits) == 0 && c.Description == "" {
		return "先观察、再试探、最后用可见证据做选择。"
	}
	src := strings.Join(append([]string{c.Description}, c.Traits...), "；")
	if len([]rune(src)) > 80 {
		src = string([]rune(src)[:80])
	}
	return "由角色卡推出：" + src + "；行动时先保留边界，再换取新信息。"
}

func zeroFirstScene(entry domain.OutlineEntry) string {
	for _, s := range entry.Scenes {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func zeroFirstSceneForProject(project zeroInitProject) string {
	entryText := zeroOutlineEntryText(project.FirstChapter)
	if project.BookWorld != nil {
		for _, place := range project.BookWorld.Places {
			name := strings.TrimSpace(place.Name)
			if name != "" && strings.Contains(entryText, name) {
				return name
			}
		}
	}
	scene := zeroFirstScene(project.FirstChapter)
	if len([]rune(scene)) <= 40 && !strings.ContainsAny(scene, "。；！？\n") {
		return scene
	}
	return ""
}

func zeroWorldRuleTexts(rules []domain.WorldRule, limit int) []string {
	var out []string
	for _, r := range rules {
		text := strings.TrimSpace(r.Rule)
		if text == "" {
			text = strings.TrimSpace(r.Boundary)
		}
		if text == "" {
			continue
		}
		out = append(out, text)
		if len(out) >= limit {
			return out
		}
	}
	if len(out) == 0 {
		out = append(out, "第一章必须让至少一条世界规则以可见后果施压。")
	}
	return out
}

func zeroFirstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func firstString(values []string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func zeroFirstNonZero(values ...int) int {
	for _, v := range values {
		if v > 0 {
			return v
		}
	}
	return 0
}

func zeroSimulationGenerationID(generatedAt string) string {
	value := strings.TrimSpace(generatedAt)
	if value == "" {
		value = time.Now().Format(time.RFC3339)
	}
	replacer := strings.NewReplacer(
		":", "", "-", "", "T", "-", "+", "-", "Z", "", ".", "",
	)
	value = replacer.Replace(value)
	value = strings.Trim(value, "-")
	if value == "" {
		value = "unknown"
	}
	return "simulation-" + value
}

func zeroInitDisplaySources(outputDir string) []string {
	var out []string
	for _, src := range zeroInitRAGSources(outputDir) {
		out = append(out, displayRAGSourcePath(src, outputDir))
	}
	sort.Strings(out)
	return out
}

func renderZeroBookWorld(world domain.BookWorld) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# 本书世界资产（零章初始化）\n\n%s\n\n", world.Summary)
	for _, p := range world.Places {
		fmt.Fprintf(&b, "## 地点：%s\n\n%s\n\n", p.Name, p.Description)
	}
	for _, f := range world.Factions {
		fmt.Fprintf(&b, "## 势力/行动方：%s\n\n目标：%s\n\n", f.Name, f.Goal)
	}
	if len(world.MapNotes) > 0 {
		b.WriteString("## 地图注记\n\n")
		for _, n := range world.MapNotes {
			fmt.Fprintf(&b, "- %s\n", n)
		}
	}
	return b.String()
}

func renderZeroDynamics(doc zeroInitCharacterDynamicsDoc) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# 初始人物动态推演\n\nscope: %s\nchapter: %d\n\n", doc.Scope, doc.Chapter)
	b.WriteString("必选字段：knowledge_ledger、decision_frame、relationship_contract、emotion_appraisal、arc_axis。\n\n")
	for _, c := range doc.Characters {
		fmt.Fprintf(&b, "## %s\n\n", c.Character)
		fmt.Fprintf(&b, "- 当前目标：%s\n", c.CurrentGoal)
		fmt.Fprintf(&b, "- 压力：%s\n", c.Pressure)
		fmt.Fprintf(&b, "- 行动倾向：%s\n", c.ActionTendency)
		fmt.Fprintf(&b, "- 合理下一步：%s\n", c.LikelyAction)
		fmt.Fprintf(&b, "- 知识边界：%s\n", strings.Join(c.KnowledgeLedger.ForbiddenKnowledge, "；"))
		fmt.Fprintf(&b, "- 决策规则：%s\n", c.DecisionFrame.DecisionRule)
		if len(c.RelationshipContract) > 0 {
			fmt.Fprintf(&b, "- 关系契约对象：%s\n", c.RelationshipContract[0].Counterpart)
		}
		fmt.Fprintf(&b, "- 情绪压力：%s\n", c.EmotionAppraisal.ActionPressure)
		fmt.Fprintf(&b, "- 长期弧线测试：%s\n\n", c.ArcAxis.PressureTest)
	}
	b.WriteString("## 声口逻辑\n\n")
	for _, v := range doc.VoiceLogic {
		fmt.Fprintf(&b, "- %s：%s；句长=%s；标点=%s；断行=%s；禁用=%s\n", v.Character, v.SpeechPrinciple, v.SentenceLength, v.PunctuationStyle, v.LineBreakStyle, strings.Join(v.ForbiddenMoves, "、"))
	}
	return b.String()
}

func renderZeroResourceLedger(ledger domain.ResourceLedger) string {
	var b strings.Builder
	b.WriteString("# 初始资源账本\n\n")
	for _, c := range ledger.Claims {
		fmt.Fprintf(&b, "- %s（%s）：owner=%s status=%s risk=%s\n", c.Name, c.Kind, c.Owner, c.Status, c.Risk)
	}
	return b.String()
}

func renderZeroReviewLessons() string {
	return "# 初始审核回路\n\n- 第一章写完后必须执行 check_consistency、commit_chapter 机械门禁和 Editor 审核。\n- 若人设、声口、RAG 来源或 AI 味不过关，下一轮推演必须引用审核结论重建 knowledge_ledger、decision_frame、voice_logic 和 review_refinement。\n- 禁止只按审核意见随机润色句子；先修角色系统、场景承载和可见事实，再改正文。\n"
}

func renderZeroStorycraftPlan(plan zeroPrewriteStorycraftPlan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# 写前故事工艺计划\n\nproject: %s\nchapter: %d\n\n%s\n\n", plan.Project, plan.Chapter, plan.UsagePolicy)
	if !plan.ThematicQuestion.IsEmpty() {
		b.WriteString("## 主题命题\n\n")
		if plan.ThematicQuestion.Question != "" {
			fmt.Fprintf(&b, "- 核心命题：%s\n", plan.ThematicQuestion.Question)
		}
		if plan.ThematicQuestion.AuthorStance != "" {
			fmt.Fprintf(&b, "- 作者立场：%s\n", plan.ThematicQuestion.AuthorStance)
		}
		if plan.ThematicQuestion.PrimaryReaderQuestion != "" {
			fmt.Fprintf(&b, "- 读者追问：%s\n", plan.ThematicQuestion.PrimaryReaderQuestion)
		}
		if len(plan.ThematicQuestion.VariationsPerVolume) > 0 {
			vols := make([]string, 0, len(plan.ThematicQuestion.VariationsPerVolume))
			for vol := range plan.ThematicQuestion.VariationsPerVolume {
				vols = append(vols, vol)
			}
			sort.Strings(vols)
			for _, vol := range vols {
				fmt.Fprintf(&b, "- 第%s卷变奏：%s\n", vol, plan.ThematicQuestion.VariationsPerVolume[vol])
			}
		}
		b.WriteString("\n")
	}
	b.WriteString("## 人物弧测试\n\n")
	for _, arc := range plan.ArcTests {
		fmt.Fprintf(&b, "- %s：Want=%s；Lie=%s；Need=%s；首个错误=%s；纠错=%s\n", arc.Character, arc.Want, arc.CoreLie, arc.Need, arc.FirstMistake, arc.CorrectionSignal)
	}
	b.WriteString("\n## 声口卡\n\n")
	for _, voice := range plan.VoiceCards {
		fmt.Fprintf(&b, "- %s：%s；句长=%s；标点=%s；断行=%s；潜台词=%s\n", voice.Character, voice.SpeechPrinciple, voice.SentenceLength, voice.PunctuationStyle, voice.LineBreakStyle, voice.SubtextStrategy)
	}
	b.WriteString("\n## 对话场景蓝图\n\n")
	for _, blueprint := range plan.DialogueBlueprints {
		fmt.Fprintf(&b, "- %s：模式=%s；开场策略=%s；压力=%s；情绪=%s\n", blueprint.SceneID, blueprint.DialogueMode, blueprint.OpeningStrategy, blueprint.ScenePressure, blueprint.EmotionalTemperature)
		fmt.Fprintf(&b, "  - 第一句时机：%s；定场=%s；退出=%s\n", blueprint.FirstSpokenMoment, blueprint.LocationAnchor, blueprint.ExitBeat)
		fmt.Fprintf(&b, "  - 价值翻转：%s：%s -> %s（触发=%s）\n", blueprint.ValueShift.Value, blueprint.ValueShift.OpeningCharge, blueprint.ValueShift.ClosingCharge, blueprint.ValueShift.TurnTrigger)
		fmt.Fprintf(&b, "  - 权力走向：%s -> 易手于%s -> %s；观众=%s\n", blueprint.PowerTrajectory.OpeningHolder, blueprint.PowerTrajectory.FlipBeat, blueprint.PowerTrajectory.ClosingHolder, blueprint.AudiencePresence.Present)
		for _, tactic := range blueprint.ObjectiveTactics {
			fmt.Fprintf(&b, "  - 策略 %s：目标=%s；话术=%s；反制=%s\n", tactic.Character, tactic.ImmediateObjective, tactic.Tactic, tactic.CounterTactic)
		}
		for _, turn := range blueprint.TurnProgression {
			fmt.Fprintf(&b, "  - %s：%s；潜台词=%s；动作=%s\n", turn.Speaker, turn.SurfaceLineFunction, turn.HiddenSubtext, turn.ActionBeat)
		}
	}
	b.WriteString("\n## 读者奖励阶梯\n\n")
	fmt.Fprintf(&b, "- 第一章小胜：%s\n", plan.ReaderReward.FirstChapterSmallWin)
	fmt.Fprintf(&b, "- 新债/代价：%s\n", plan.ReaderReward.NewDebtOrCost)
	for _, step := range plan.ReaderReward.RewardLadder {
		fmt.Fprintf(&b, "- 第%d章：%s；代价：%s；钩子：%s\n", step.Chapter, step.Reward, step.Cost, step.Hook)
	}
	b.WriteString("\n## 证据回收链\n\n")
	for _, chain := range plan.EvidenceChains {
		fmt.Fprintf(&b, "- %s：%s -> 证据=%s；主角得知=%s；回收=%s\n", chain.OffscreenCharacter, chain.Event, chain.Evidence, chain.ProtagonistAccess, chain.ReturnTiming)
	}
	b.WriteString("\n## 章末后果契约\n\n")
	fmt.Fprintf(&b, "- 模式：%s\n", plan.EndingContract.EndingMode)
	fmt.Fprintf(&b, "- 锚点：%s\n", plan.EndingContract.ConcreteAnchor)
	fmt.Fprintf(&b, "- 后果：%s\n", plan.EndingContract.Consequence)
	fmt.Fprintf(&b, "- 禁用：%s\n", strings.Join(plan.EndingContract.ForbiddenEndings, "；"))
	b.WriteString("\n\n## 休眠角色与现实支撑\n\n")
	for _, dormant := range plan.DormantPolicy {
		fmt.Fprintf(&b, "- %s：%s；位置=%s；下次检查=%s\n", dormant.Character, dormant.Status, dormant.Location, dormant.NextCheck)
	}
	for _, support := range plan.RealitySupport {
		fmt.Fprintf(&b, "- %s：%s -> %s\n", support.Domain, support.UsableDetail, support.TransformedAs)
	}
	b.WriteString("\n## 情感逻辑\n\n")
	for _, emo := range plan.EmotionalLogic {
		fmt.Fprintf(&b, "- %s：%s/%s；触发=%s；防御=%s；偏差=%s；情绪行动=%s\n", emo.Character, emo.PrimaryEmotion, emo.CompositeEmotion, emo.EmotionalTrigger, emo.DefenseMechanism, emo.CognitiveBias, emo.EmotionLedAction)
	}
	b.WriteString("\n## 关系情感弧\n\n")
	for _, arc := range plan.RelationshipArcs {
		fmt.Fprintf(&b, "- %s：%s；亲密阶段=%s；恋爱=%s；下一拍=%s\n", strings.Join(arc.Pair, "/"), arc.RelationshipType, arc.IntimacyStage, arc.RomancePotential, arc.NextEmotionalBeat)
	}
	b.WriteString("\n## 视觉设计\n\n")
	for _, visual := range plan.VisualDesign {
		fmt.Fprintf(&b, "- %s：轮廓=%s；发型/脸=%s；服装=%s；标志物=%s\n", visual.Character, visual.Silhouette, visual.FaceAndHair, visual.ClothingStyle, visual.SignatureObject)
	}
	return b.String()
}

func renderZeroWorldBackgroundPlan(plan zeroWorldBackgroundPlan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# 写前世界背景计划\n\nproject: %s\nchapter: %d\n\n%s\n\n", plan.Project, plan.Chapter, plan.UsagePolicy)
	if len(plan.ResearchBasis) > 0 {
		b.WriteString("## 研究依据\n\n")
		for _, basis := range plan.ResearchBasis {
			fmt.Fprintf(&b, "- %s\n", basis)
		}
		b.WriteString("\n")
	}
	b.WriteString("## 事件背景十层\n\n")
	fmt.Fprintf(&b, "- 物理/空间：%s\n", plan.Layers.PhysicalSpace)
	fmt.Fprintf(&b, "- 时间：%s\n", plan.Layers.TimeLayer)
	fmt.Fprintf(&b, "- 社会/制度：%s\n", plan.Layers.SocialInstitution)
	fmt.Fprintf(&b, "- 文化/规范：%s\n", plan.Layers.CulturalNorm)
	fmt.Fprintf(&b, "- 关系/网络：%s\n", plan.Layers.RelationshipNetwork)
	fmt.Fprintf(&b, "- 经济/资源：%s\n", plan.Layers.EconomicResource)
	fmt.Fprintf(&b, "- 冲突/张力：%s\n", plan.Layers.ConflictTension)
	fmt.Fprintf(&b, "- 氛围/情绪：%s\n", plan.Layers.SocialMood)
	fmt.Fprintf(&b, "- 元背景/宇宙观：%s\n", plan.Layers.CosmologyMetaRule)
	fmt.Fprintf(&b, "- 叙事层：%s\n", plan.Layers.NarrativeMeta)
	fmt.Fprintf(&b, "- 事件激活：%s\n\n", plan.Layers.EventActivation)
	b.WriteString("## 信息差结构\n\n")
	for _, info := range plan.InformationLedger {
		fmt.Fprintf(&b, "- %s：主角知道=%s；角色误判=%s；揭示=%s；功能=%s\n", info.Subject, strings.Join(info.ProtagonistKnows, "；"), strings.Join(info.CharacterMistakes, "；"), info.RevealCondition, info.TensionFunction)
	}
	b.WriteString("\n## 潜规则与社会情绪\n\n")
	for _, hidden := range plan.HiddenRules {
		fmt.Fprintf(&b, "- %s：显规则=%s；潜规则=%s；代价=%s；证据=%s\n", hidden.Domain, hidden.VisibleRule, hidden.HiddenRule, hidden.ViolationCost, hidden.SceneEvidence)
	}
	for _, rumor := range plan.SocialMoodRumors {
		fmt.Fprintf(&b, "- %s：情绪=%s；流言=%s；传播=%s；影响=%s\n", rumor.Group, rumor.Mood, rumor.Rumor, rumor.SpreadPath, rumor.BehaviorEffect)
	}
	b.WriteString("\n## 时间窗口与结构资源\n\n")
	for _, window := range plan.RitualCalendar {
		fmt.Fprintf(&b, "- %s：%s；限制=%s；错过代价=%s\n", window.Time, window.RitualOrDeadline, window.PracticalConstraint, window.MissedCost)
	}
	for _, resource := range plan.StructuralResources {
		fmt.Fprintf(&b, "- %s：控制者=%s；准入=%s；成本=%s；权力=%s\n", resource.Resource, resource.Controller, resource.AccessRule, resource.PriceOrCost, resource.PowerEffect)
	}
	b.WriteString("\n## 宇宙观与矛盾网\n\n")
	for _, check := range plan.CosmologyChecks {
		fmt.Fprintf(&b, "- %s：规则=%s；成本=%s；边界=%s；失败=%s\n", check.Layer, check.Rule, check.Cost, check.Boundary, check.FailureMode)
	}
	for _, conflict := range plan.ConflictWeb {
		fmt.Fprintf(&b, "- %s：%s；明面目标=%s；暗线=%s；下一步=%s\n", strings.Join(conflict.Parties, "/"), conflict.ConflictType, conflict.OpenGoal, conflict.HiddenAgenda, conflict.NextEscalation)
	}
	b.WriteString("\n## 叙事张力矩阵\n\n")
	fmt.Fprintf(&b, "- 稳定/动荡：%s\n", plan.TensionMatrix.StabilityTurbulence)
	fmt.Fprintf(&b, "- 显/潜规则：%s\n", plan.TensionMatrix.ExplicitHiddenRules)
	fmt.Fprintf(&b, "- 信息差：%s\n", plan.TensionMatrix.InformationGap)
	fmt.Fprintf(&b, "- 时间压力：%s\n", plan.TensionMatrix.TimePressurePreparation)
	fmt.Fprintf(&b, "- 为什么现在：%s\n", plan.TensionMatrix.WhyEventNow)
	fmt.Fprintf(&b, "- 读者问题：%s\n", plan.TensionMatrix.ReaderQuestion)
	fmt.Fprintf(&b, "- POV边界：%s\n", plan.TensionMatrix.POVBoundary)
	return b.String()
}

func renderZeroChapterPlan(plan domain.ChapterPlan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# 第 %d 章零章推演草案：%s\n\n", plan.Chapter, plan.Title)
	fmt.Fprintf(&b, "- 目标：%s\n", plan.Goal)
	fmt.Fprintf(&b, "- 冲突：%s\n", plan.Conflict)
	fmt.Fprintf(&b, "- 钩子：%s\n", plan.Hook)
	fmt.Fprintf(&b, "- 情绪弧线：%s\n\n", plan.EmotionArc)
	b.WriteString("## 章节契约\n\n")
	for _, beat := range plan.Contract.RequiredBeats {
		fmt.Fprintf(&b, "- 必须完成：%s\n", beat)
	}
	for _, move := range plan.Contract.ForbiddenMoves {
		fmt.Fprintf(&b, "- 禁止动作：%s\n", move)
	}
	b.WriteString("\n## 因果推演\n\n")
	fmt.Fprintf(&b, "- 全书承诺：%s\n", plan.CausalSimulation.ProjectPromise)
	fmt.Fprintf(&b, "- 章节功能：%s\n", plan.CausalSimulation.ChapterFunction)
	fmt.Fprintf(&b, "- 上下文来源：%s\n", strings.Join(plan.CausalSimulation.ContextSources, "、"))
	fmt.Fprintf(&b, "- 连载发动机：%s\n\n", plan.CausalSimulation.LongformOpening.SerialEngine)
	if len(plan.CausalSimulation.WritingNorms) > 0 {
		b.WriteString("## 写作规范执行\n\n")
		for _, norm := range plan.CausalSimulation.WritingNorms {
			fmt.Fprintf(&b, "- %s：%s\n", norm.Source, norm.ChapterApplication)
		}
		b.WriteString("\n")
	}
	if plan.CausalSimulation.AntiAIPlan.SentenceRhythmPolicy != "" {
		b.WriteString("## AI味阻断\n\n")
		fmt.Fprintf(&b, "- 句式节奏：%s\n", plan.CausalSimulation.AntiAIPlan.SentenceRhythmPolicy)
		fmt.Fprintf(&b, "- 物件回应：%s\n", plan.CausalSimulation.AntiAIPlan.ObjectResponseBudget)
		fmt.Fprintf(&b, "- 对白功能：%s\n\n", plan.CausalSimulation.AntiAIPlan.DialogueFunctionPlan)
	}
	if len(plan.CausalSimulation.ExternalRefs) > 0 {
		b.WriteString("## 外部参考与热梗预算\n\n")
		for _, ref := range plan.CausalSimulation.ExternalRefs {
			fmt.Fprintf(&b, "- 资料需求：%s\n", ref.QueryOrNeed)
			fmt.Fprintf(&b, "  - 转化规则：%s\n", ref.TransformationRule)
		}
		for _, trend := range plan.CausalSimulation.TrendLanguage {
			fmt.Fprintf(&b, "- 热梗：%s；载体：%s；预算：%s\n", trend.Item, trend.CharacterCarrier, trend.UsageBudget)
		}
		b.WriteString("\n")
	}
	if len(plan.CausalSimulation.CharacterArcTests) > 0 {
		b.WriteString("## 人物弧与合理犯错\n\n")
		for _, arc := range plan.CausalSimulation.CharacterArcTests {
			fmt.Fprintf(&b, "- %s：Want=%s；Lie=%s；Need=%s；首错=%s；纠错=%s\n", arc.Character, arc.Want, arc.CoreLie, arc.Need, arc.FirstMistake, arc.CorrectionSignal)
		}
		b.WriteString("\n")
	}
	if len(plan.CausalSimulation.DialogueBlueprints) > 0 {
		b.WriteString("## 对话场景蓝图\n\n")
		for _, blueprint := range plan.CausalSimulation.DialogueBlueprints {
			fmt.Fprintf(&b, "- %s：模式=%s；开场策略=%s；压力=%s；情绪=%s；关系=%s\n", blueprint.SceneID, blueprint.DialogueMode, blueprint.OpeningStrategy, blueprint.ScenePressure, blueprint.EmotionalTemperature, blueprint.RelationshipFrame)
			fmt.Fprintf(&b, "  - 第一句时机：%s；目标=%s；退出=%s\n", blueprint.FirstSpokenMoment, blueprint.DialogueObjective, blueprint.ExitBeat)
			fmt.Fprintf(&b, "  - 价值翻转：%s：%s -> %s；权力走向：%s -> %s -> %s\n", blueprint.ValueShift.Value, blueprint.ValueShift.OpeningCharge, blueprint.ValueShift.ClosingCharge, blueprint.PowerTrajectory.OpeningHolder, blueprint.PowerTrajectory.FlipBeat, blueprint.PowerTrajectory.ClosingHolder)
			for _, tactic := range blueprint.ObjectiveTactics {
				fmt.Fprintf(&b, "  - 策略 %s：目标=%s；话术=%s；情绪泄露=%s；结果=%s\n", tactic.Character, tactic.ImmediateObjective, tactic.Tactic, tactic.EmotionalLeak, tactic.TurnResult)
			}
			for _, turn := range blueprint.TurnProgression {
				fmt.Fprintf(&b, "  - %s：%s；潜台词=%s；下一压力=%s\n", turn.Speaker, turn.SurfaceLineFunction, turn.HiddenSubtext, turn.NextPressure)
			}
		}
		b.WriteString("\n")
	}
	if len(plan.CausalSimulation.ReaderRewardPlan.RewardLadder) > 0 {
		b.WriteString("## 读者奖励计划\n\n")
		fmt.Fprintf(&b, "- 第一章小胜：%s\n", plan.CausalSimulation.ReaderRewardPlan.FirstChapterSmallWin)
		fmt.Fprintf(&b, "- 新债/代价：%s\n", plan.CausalSimulation.ReaderRewardPlan.NewDebtOrCost)
		for _, step := range plan.CausalSimulation.ReaderRewardPlan.RewardLadder {
			fmt.Fprintf(&b, "- 第%d章：%s；代价：%s\n", step.Chapter, step.Reward, step.Cost)
		}
		b.WriteString("\n")
	}
	if len(plan.CausalSimulation.EvidenceChains) > 0 {
		b.WriteString("## 证据回收链\n\n")
		for _, chain := range plan.CausalSimulation.EvidenceChains {
			fmt.Fprintf(&b, "- %s：%s -> %s；回收：%s\n", chain.OffscreenCharacter, chain.Event, chain.Evidence, chain.ReturnTiming)
		}
		b.WriteString("\n")
	}
	if plan.CausalSimulation.EndingContract.EndingMode != "" {
		b.WriteString("## 章末后果契约\n\n")
		fmt.Fprintf(&b, "- 模式：%s\n", plan.CausalSimulation.EndingContract.EndingMode)
		fmt.Fprintf(&b, "- 锚点：%s\n", plan.CausalSimulation.EndingContract.ConcreteAnchor)
		fmt.Fprintf(&b, "- 后果：%s\n", plan.CausalSimulation.EndingContract.Consequence)
		fmt.Fprintf(&b, "- 下一章牵引：%s\n\n", plan.CausalSimulation.EndingContract.NextChapterPull)
	}
	if len(plan.CausalSimulation.EmotionalLogic) > 0 {
		b.WriteString("## 情感驱动\n\n")
		for _, emo := range plan.CausalSimulation.EmotionalLogic {
			fmt.Fprintf(&b, "- %s：%s/%s；触发=%s；情绪行动=%s\n", emo.Character, emo.PrimaryEmotion, emo.CompositeEmotion, emo.EmotionalTrigger, emo.EmotionLedAction)
		}
		b.WriteString("\n")
	}
	if len(plan.CausalSimulation.RelationshipArcs) > 0 {
		b.WriteString("## 关系情感弧\n\n")
		for _, arc := range plan.CausalSimulation.RelationshipArcs {
			fmt.Fprintf(&b, "- %s：%s；亲密阶段=%s；恋爱=%s\n", strings.Join(arc.Pair, "/"), arc.RelationshipType, arc.IntimacyStage, arc.RomancePotential)
		}
		b.WriteString("\n")
	}
	if len(plan.CausalSimulation.VisualDesign) > 0 {
		b.WriteString("## 视觉身份\n\n")
		for _, visual := range plan.CausalSimulation.VisualDesign {
			fmt.Fprintf(&b, "- %s：%s；%s；%s\n", visual.Character, visual.Silhouette, visual.FaceAndHair, visual.ClothingStyle)
		}
		b.WriteString("\n")
	}
	if plan.CausalSimulation.WorldLayers.EventActivation != "" {
		b.WriteString("## 世界背景层\n\n")
		fmt.Fprintf(&b, "- 物理/空间：%s\n", plan.CausalSimulation.WorldLayers.PhysicalSpace)
		fmt.Fprintf(&b, "- 时间窗口：%s\n", plan.CausalSimulation.WorldLayers.TimeLayer)
		fmt.Fprintf(&b, "- 潜规则/制度：%s / %s\n", plan.CausalSimulation.WorldLayers.SocialInstitution, plan.CausalSimulation.WorldLayers.CulturalNorm)
		fmt.Fprintf(&b, "- 事件激活：%s\n", plan.CausalSimulation.WorldLayers.EventActivation)
		if len(plan.CausalSimulation.ConflictWeb) > 0 {
			for _, conflict := range plan.CausalSimulation.ConflictWeb {
				fmt.Fprintf(&b, "- 矛盾网：%s -> %s\n", strings.Join(conflict.Parties, "/"), conflict.NextEscalation)
			}
		}
		b.WriteString("\n")
	}
	for _, state := range plan.CausalSimulation.InitialState {
		fmt.Fprintf(&b, "### %s\n\n目标：%s\n\n压力：%s\n\n决策规则：%s\n\n", state.Character, state.CurrentGoal, state.Pressure, state.DecisionFrame.DecisionRule)
	}
	return b.String()
}

func renderZeroGenericDoc(title string, v any) string {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "# " + title + "\n\n" + fmt.Sprint(v) + "\n"
	}
	return "# " + title + "\n\n```json\n" + string(data) + "\n```\n"
}

func renderZeroReadiness(r zeroInitReadiness) string {
	var b strings.Builder
	b.WriteString("# 第一章生成就绪检查\n\n")
	fmt.Fprintf(&b, "- ready: %v\n", r.Ready)
	fmt.Fprintf(&b, "- schema_version: %d\n", r.SchemaVersion)
	if r.GeneratorVersion != "" {
		fmt.Fprintf(&b, "- generator_version: %s\n", r.GeneratorVersion)
	}
	fmt.Fprintf(&b, "- generated_at: %s\n", r.GeneratedAt)
	if len(r.Missing) > 0 {
		b.WriteString("\n## Missing\n\n")
		for _, m := range r.Missing {
			fmt.Fprintf(&b, "- %s\n", m)
		}
	}
	if len(r.Issues) > 0 {
		b.WriteString("\n## Issues\n\n")
		for _, issue := range r.Issues {
			fmt.Fprintf(&b, "- %s\n", issue)
		}
	}
	if len(r.Warnings) > 0 {
		b.WriteString("\n## Warnings\n\n")
		for _, warning := range r.Warnings {
			fmt.Fprintf(&b, "- %s\n", warning)
		}
	}
	b.WriteString("\n## Story time contract\n\n")
	fmt.Fprintf(&b, "- validated: %v\n", r.StoryTime.Validated)
	fmt.Fprintf(&b, "- source: %s\n", r.StoryTime.Source)
	fmt.Fprintf(&b, "- target_chapters: %d\n", r.StoryTime.TargetChapters)
	fmt.Fprintf(&b, "- duration_days: %.3f - %.3f\n", r.StoryTime.DurationDaysMin, r.StoryTime.DurationDaysMax)
	fmt.Fprintf(&b, "- nominal_days_per_chapter: %.6f\n", r.StoryTime.NominalDaysPerChapter)
	fmt.Fprintf(&b, "- schedule_entries: arc=%d chapter=%d\n", r.StoryTime.ArcScheduleEntries, r.StoryTime.ChapterScheduleEntries)
	fmt.Fprintf(&b, "- calendar_synced: %v\n", r.StoryTime.CalendarSynced)
	b.WriteString("\n## RAG\n\n")
	fmt.Fprintf(&b, "- enabled: %v\n", r.RAG.Enabled)
	fmt.Fprintf(&b, "- files: %d\n", r.RAG.Files)
	fmt.Fprintf(&b, "- chunks: %d\n", r.RAG.Chunks)
	if r.RAG.IndexPath != "" {
		fmt.Fprintf(&b, "- index: %s\n", r.RAG.IndexPath)
	}
	return b.String()
}

// zeroReadinessRequiredNames 返回 dynamics/voice 必须覆盖的角色集合：
// 主角 ∪ 大纲点名角色 ∪ 全部 core/important（tier 空按 important）。
func zeroReadinessRequiredNames(dir string) []string {
	st := store.NewStore(dir)
	chars, err := st.Characters.Load()
	if err != nil || len(chars) == 0 {
		return nil
	}
	outline, _ := st.Outline.LoadOutline()
	project := zeroInitProject{
		Characters:    chars,
		FirstMentions: zeroCharacterFirstMentions(outline, chars),
	}
	if len(outline) > 0 {
		project.FirstChapter = outline[0]
		project.FirstCast = zeroFirstChapterCast(outline[0], chars)
	}
	var names []string
	for _, c := range zeroInitialCharacters(project) {
		names = append(names, strings.TrimSpace(c.Name))
	}
	return names
}

// zeroCheckDynamicsCoverage Task 051：dynamics 与 voice_logic 缺人报 issues（阻塞 ready）。
func zeroCheckDynamicsCoverage(dir string) []string {
	required := zeroReadinessRequiredNames(dir)
	if len(required) == 0 {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(dir, "meta", "initial_character_dynamics.json"))
	if err != nil {
		return nil // 文件缺失已由 required 清单报 missing
	}
	var doc zeroInitCharacterDynamicsDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return []string{"meta/initial_character_dynamics.json 不是有效 JSON"}
	}
	have := map[string]bool{}
	for _, s := range doc.Characters {
		have[strings.TrimSpace(s.Character)] = true
	}
	haveVoice := map[string]bool{}
	for _, v := range doc.VoiceLogic {
		haveVoice[strings.TrimSpace(v.Character)] = true
	}
	var missingDyn, missingVoice []string
	for _, name := range required {
		if !have[name] {
			missingDyn = append(missingDyn, name)
		}
		if !haveVoice[name] {
			missingVoice = append(missingVoice, name)
		}
	}
	var issues []string
	if len(missingDyn) > 0 {
		issues = append(issues, fmt.Sprintf("initial_character_dynamics.characters 未覆盖全书推演所需角色：%s", strings.Join(missingDyn, "、")))
	}
	if len(missingVoice) > 0 {
		issues = append(issues, fmt.Sprintf("initial_character_dynamics.voice_logic 未覆盖全书推演所需角色：%s", strings.Join(missingVoice, "、")))
	}
	return issues
}

// zeroCheckTemplateHomogeneity Task 053：零章确定性模板的同质检测（warning）。
func zeroCheckTemplateHomogeneity(dir string) []string {
	data, err := os.ReadFile(filepath.Join(dir, "meta", "initial_character_dynamics.json"))
	if err != nil {
		return nil
	}
	var doc zeroInitCharacterDynamicsDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil
	}
	var warnings []string
	dupPrinciple := map[string]int{}
	for _, v := range doc.VoiceLogic {
		if s := strings.TrimSpace(v.SpeechPrinciple); s != "" {
			dupPrinciple[s]++
		}
	}
	dupGoal := map[string]int{}
	for _, s := range doc.Characters {
		if g := strings.TrimSpace(s.CurrentGoal); g != "" {
			dupGoal[g]++
		}
	}
	dupVoiceFields := map[string]map[string]int{
		"scene_objective":     {},
		"hidden_subtext":      {},
		"relationship_stance": {},
		"diction_and_rhythm":  {},
	}
	for _, v := range doc.VoiceLogic {
		if s := strings.TrimSpace(v.SceneObjective); s != "" {
			dupVoiceFields["scene_objective"][s]++
		}
		if s := strings.TrimSpace(v.HiddenSubtext); s != "" {
			dupVoiceFields["hidden_subtext"][s]++
		}
		if s := strings.TrimSpace(v.RelationshipStance); s != "" {
			dupVoiceFields["relationship_stance"][s]++
		}
		if s := strings.TrimSpace(v.DictionAndRhythm); s != "" {
			dupVoiceFields["diction_and_rhythm"][s]++
		}
	}
	homogeneous := false
	for _, n := range dupPrinciple {
		if n >= 2 {
			homogeneous = true
		}
	}
	for _, n := range dupGoal {
		if n >= 2 {
			homogeneous = true
		}
	}
	if homogeneous {
		warnings = append(warnings, "initial_character_dynamics 仍是零章模板（≥2 个角色 speech_principle/current_goal 完全相同），Architect 需在正式 plan 前特化角色声口与目标")
	}
	for field, counts := range dupVoiceFields {
		for _, n := range counts {
			if n >= 3 {
				warnings = append(warnings, fmt.Sprintf("initial_character_dynamics.%s 同质化（≥3 个角色完全相同），需在 zero-init 特化人物目标/潜台词/关系姿态/语气节奏", field))
				break
			}
		}
	}
	warnings = append(warnings, zeroCheckEmotionTemplateHomogeneity(dir)...)
	var voiceGaps []string
	for _, v := range doc.VoiceLogic {
		if strings.TrimSpace(v.SentenceLength) == "" || strings.TrimSpace(v.PunctuationStyle) == "" ||
			strings.TrimSpace(v.LineBreakStyle) == "" || strings.TrimSpace(v.SubtextStrategy) == "" ||
			strings.TrimSpace(v.SilenceOrAction) == "" || strings.TrimSpace(v.VoiceContrast) == "" {
			voiceGaps = append(voiceGaps, strings.TrimSpace(v.Character))
		}
	}
	if len(voiceGaps) > 0 {
		warnings = append(warnings, fmt.Sprintf("声口卡 6 细字段（句长/标点/断行/潜台词/沉默动作拍/声口对比）不全的角色：%s", strings.Join(voiceGaps, "、")))
	}
	return warnings
}

func zeroCheckEmotionTemplateHomogeneity(dir string) []string {
	var warnings []string
	data, err := os.ReadFile(filepath.Join(dir, "meta", "prewrite_storycraft_plan.json"))
	if err == nil {
		var plan zeroPrewriteStorycraftPlan
		if json.Unmarshal(data, &plan) == nil {
			warnings = append(warnings, zeroEmotionTemplateWarnings("prewrite_storycraft_plan.emotional_logic", plan.EmotionalLogic)...)
		}
	}
	var chapterPlan domain.ChapterPlan
	data, err = os.ReadFile(filepath.Join(dir, "drafts", "01.zero_init.plan.json"))
	if err == nil && json.Unmarshal(data, &chapterPlan) == nil {
		warnings = append(warnings, zeroEmotionTemplateWarnings("drafts/01.zero_init.plan.causal_simulation.emotional_logic", chapterPlan.CausalSimulation.EmotionalLogic)...)
	}
	return warnings
}

func zeroEmotionTemplateWarnings(label string, items []domain.CharacterEmotionalLogic) []string {
	if len(items) < 2 {
		return nil
	}
	dupComposite := map[string]int{}
	dupDefense := map[string]int{}
	dupAction := map[string]int{}
	for _, item := range items {
		if s := strings.TrimSpace(item.CompositeEmotion); s != "" {
			dupComposite[s]++
		}
		if s := strings.TrimSpace(item.DefenseMechanism); s != "" {
			dupDefense[s]++
		}
		if s := strings.TrimSpace(item.EmotionLedAction); s != "" {
			dupAction[s]++
		}
	}
	for _, n := range dupComposite {
		if n >= 3 {
			return []string{label + " 情绪复合项同质化（≥3 个角色完全相同），需回到 Architect/zero-init 特化情绪强度与动机"}
		}
	}
	for _, n := range dupDefense {
		if n >= 3 {
			return []string{label + " 防御机制同质化（≥3 个角色完全相同），需按角色职业/关系/权力位置重建"}
		}
	}
	for _, n := range dupAction {
		if n >= 3 {
			return []string{label + " 情绪推行动作同质化（≥3 个角色完全相同），不能放行写作"}
		}
	}
	return nil
}

// zeroCheckPsychCoverage Task 054：core/important 角色缺 psych 画像（warning，psych 保持可选）。
func zeroCheckPsychCoverage(dir string) []string {
	st := store.NewStore(dir)
	chars, err := st.Characters.Load()
	if err != nil {
		return nil
	}
	var missing []string
	for _, c := range chars {
		tier := strings.TrimSpace(c.Tier)
		if tier != "core" && tier != "important" && tier != "" {
			continue
		}
		if c.Psych == nil {
			missing = append(missing, strings.TrimSpace(c.Name))
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return []string{fmt.Sprintf("core/important 角色缺 psych 画像（至少补 big_five+values+moral_foundations+emotion_vector）：%s", strings.Join(missing, "、"))}
}
