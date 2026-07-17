package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestSimulationCharacterAuthorityCoversRequiredRosterWithoutCap(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	var characters []domain.Character
	for i := 1; i <= 20; i++ {
		name := fmt.Sprintf("角色%02d", i)
		characters = append(characters, domain.Character{
			Name: name, Role: "配角", Tier: "important", Description: name + "的权威角色卡",
		})
		if err := st.SaveCharacterDossier(domain.CharacterDossier{
			Version: 1, Character: name, Role: "配角", Tier: "important",
			Profile: domain.CharacterDossierProfile{Description: name + "的档案描述"},
			CurrentAtStoryStart: domain.CharacterStartState{
				Location: "故事开始前未进入主角视角；正式出场前按角色卡和交通规则补足位置",
				Status:   "存活/状态待正文确认", CurrentAction: "按自身开局目标行动或被自身场景压力困住。",
			},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Characters.Save(characters); err != nil {
		t.Fatal(err)
	}
	if err := st.Cast.Save([]domain.CastEntry{{Name: "临时邻居", BriefRole: "饭桌邻居", LastSeenChapter: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveChapterWorldSimulationPartial(domain.ChapterWorldSimulation{
		Version: 1, Chapter: 1,
		CharacterDecisions: []domain.CharacterWorldDecision{{Character: "角色01"}},
	}); err != nil {
		t.Fatal(err)
	}

	authority := buildSimulationCharacterAuthority(st, 1)
	if len(authority) != 21 {
		t.Fatalf("required roster was capped or lost: got=%d authority=%+v", len(authority), authority)
	}
	if authority[0].Character != "角色01" || authority[0].SimulationStatus != "already_present" || authority[0].AuthorityMode != "reuse_saved_decision" {
		t.Fatalf("saved partial decision must be protected from resubmission: %+v", authority[0])
	}
	second := authority[1]
	if second.Description != "角色02的档案描述" || second.CurrentLocation != "unknown" || second.CurrentAction != "unknown" || !second.Blocking || second.AuthorityMode != "hold_baseline" {
		t.Fatalf("placeholder dossier state must remain an explicit blocked unknown: %+v", second)
	}
	last := authority[len(authority)-1]
	if last.Character != "临时邻居" || last.Role != "饭桌邻居" || !last.Blocking || last.AuthorityMode != "hold_baseline" || !slices.Contains(last.MissingAuthority, "dossier") {
		t.Fatalf("cast-only actor must block invention instead of acquiring a guessed dossier: %+v", last)
	}
}

func TestEffectiveProtagonistDecisionHidesAuthoritySentinel(t *testing.T) {
	projection := domain.ProtagonistDecisionProjection{
		ChosenDecision: simulationAuthorityPreserve,
		AvailableOptions: []string{
			simulationAuthorityPreserve,
			"先守住可撤回试点，再等待尚未确认的运力",
		},
		DecisionReason: "小胜之后仍需承担安全代价",
	}
	if got := effectiveProtagonistDecision(projection); got != "" {
		t.Fatalf("unchosen option replaced an authority sentinel: %q", got)
	}
	projection.ChosenDecision = "先守住可撤回试点，再等待尚未确认的运力"
	projected := planningProtagonistProjection(projection)
	if projected.ChosenDecision != "先守住可撤回试点，再等待尚未确认的运力" ||
		projection.ChosenDecision != "先守住可撤回试点，再等待尚未确认的运力" {
		t.Fatalf("planning projection changed a human-readable canonical choice: projected=%+v source=%+v", projected, projection)
	}

	onlySentinels := domain.ProtagonistDecisionProjection{
		ChosenDecision:   simulationAuthorityPreserve,
		AvailableOptions: []string{simulationAuthorityPreserve, simulationAuthorityNoAlternative},
		DecisionReason:   simulationAuthorityEvidenceOnly,
	}
	if got := effectiveProtagonistDecision(onlySentinels); got != "" {
		t.Fatalf("authority decision_reason must not be laundered into formal plan content: %q", got)
	}
	if got := planningProtagonistProjection(onlySentinels).ChosenDecision; got != "" {
		t.Fatalf("planning context must expose a missing real choice, not an authority sentinel: %q", got)
	}
}

func TestPlaceholderDossierStaysBlockingButCannotEraseHumanProjection(t *testing.T) {
	st := newChapterSimulationTestStore(t)
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{
		Chapter:   1,
		Title:     "额度到账",
		CoreEvent: "林澈先核验专项额度，再完成一笔可撤回的小额县内试验。",
		Hook:      "林澈约定次日继续核验。",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveCharacterDossier(domain.CharacterDossier{
		Version: 1, Character: "林澈", Role: "主角", Tier: "core",
		Profile: domain.CharacterDossierProfile{
			Description: "返乡青年",
			Desires:     []string{"在开局压力中保住自己的目标、资源或关系边界。"},
		},
		CurrentAtStoryStart: domain.CharacterStartState{
			Location:            "故事开始前未进入主角视角；正式出场前按角色卡和交通规则补足位置",
			Status:              "存活/状态待正文确认",
			CurrentAction:       "按自身开局目标行动或被自身场景压力困住。",
			Pressure:            "返乡饭桌上的失业追问正在逼近",
			NextIndependentMove: "进入第一章现场选择，并在章末产生可回填状态变化。",
		},
		Resources: []domain.CharacterResource{{
			ID: "baseline-experience", Name: "故事开始前经验/身份资源",
			Kind: "experience", Status: "baseline",
		}},
		KnowledgeBoundary: "只知道自己经历和合法通信获得的信息",
		DecisionModel:     "按自身目标、恐惧、资源、关系和现场证据选择，不为主角工具化。",
	}); err != nil {
		t.Fatal(err)
	}
	authority := buildSimulationCharacterAuthority(st, 1)
	var protagonist simulationCharacterAuthority
	for _, entry := range authority {
		if entry.Character == "林澈" {
			protagonist = entry
			break
		}
	}
	if !protagonist.Blocking || !slices.Contains(protagonist.MissingAuthority, "current_location") ||
		!slices.Contains(protagonist.MissingAuthority, "decision_model") {
		t.Fatalf("zero-init placeholder dossier must remain non-authoritative: %+v", protagonist)
	}

	dynamics := struct {
		Version    int                               `json:"version"`
		Scope      string                            `json:"scope"`
		Chapter    int                               `json:"chapter"`
		Characters []domain.CharacterSimulationState `json:"characters"`
	}{
		Version: 1, Scope: "zero_chapter", Chapter: 1,
		Characters: []domain.CharacterSimulationState{{
			Character:      "林澈",
			CurrentGoal:    "顶住饭桌上的难堪，先确认异常资金不会连累家人。",
			Pressure:       "亲戚追问失业与下一份工作，异常额度同时出现。",
			Resources:      []string{"角色卡既有经验", "第一章可见事实", "可复核的县内交易票据"},
			ActionTendency: "林澈先缩小风险，再用现场可核验动作换取新证据。",
			LikelyAction:   "先核验专项额度，再做一笔可撤回的县内小额试验。",
			DecisionFrame: domain.CharacterDecisionFrame{
				DecisionRule: "先核验证据，再决定是否交易、承诺或升级投入。",
			},
			KnowledgeLedger: domain.CharacterKnowledgeLedger{
				KnownFacts:         []string{"林澈只知道本人界面与现场证据"},
				ForbiddenKnowledge: []string{"其他角色未传回的时间线"},
			},
		}},
	}
	raw, err := json.Marshal(dynamics)
	if err != nil {
		t.Fatal(err)
	}
	dynamicsPath := filepath.Join(st.Dir(), "meta", "initial_character_dynamics.json")
	if err := os.MkdirAll(filepath.Dir(dynamicsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dynamicsPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	planningContext, _ := installPlanningContextAccessProjectAll(
		t,
		st,
		1,
		"authority-project-all-grounded-test",
	)
	manifestRaw, err := json.Marshal(projectAllAuthorityWorkspaceManifest{
		Version:                "project-all-workspace.v3",
		GenerationID:           planningContext.GenerationID,
		SourceOutput:           st.Dir(),
		BaseChapter:            0,
		Workspace:              st.Dir(),
		IsolatedWrites:         true,
		FoundationSnapshotRoot: sealedRAGGuardDigest(t, "authority-foundation"),
		RAGSnapshotRoot:        sealedRAGGuardDigest(t, "authority-rag"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(st.Dir(), "meta", "project_all_workspace_manifest.json"),
		manifestRaw,
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	authority = buildSimulationCharacterAuthority(st, 1)
	var offscreen simulationCharacterAuthority
	for _, entry := range authority {
		switch entry.Character {
		case "林澈":
			protagonist = entry
		case "沈知遥":
			offscreen = entry
		}
	}
	if protagonist.Blocking || protagonist.AuthorityMode != "project_all_grounded" ||
		protagonist.CurrentGoal != dynamics.Characters[0].CurrentGoal ||
		protagonist.CurrentPressure != dynamics.Characters[0].Pressure ||
		protagonist.CurrentAction != dynamics.Characters[0].ActionTendency ||
		protagonist.DecisionModel != dynamics.Characters[0].DecisionFrame.DecisionRule ||
		!slices.Contains(protagonist.MissingAuthority, "current_location") ||
		!slices.Contains(protagonist.MissingAuthority, "current_status") {
		t.Fatalf("current-PID project-all context did not ground visible placeholder dossier: %+v", protagonist)
	}
	if !offscreen.Blocking || offscreen.AuthorityMode != "hold_baseline" {
		t.Fatalf("offscreen actor without complete grounded dynamics must remain frozen: %+v", offscreen)
	}

	humanDecision := "先核验专项额度，再做一笔可撤回的县内小额试验"
	simulation := domain.ChapterWorldSimulation{
		Chapter: 1,
		CharacterDecisions: []domain.CharacterWorldDecision{{
			Character: "林澈",
			Decision:  humanDecision,
		}},
		ProtagonistProjection: domain.ProtagonistDecisionProjection{
			Protagonist:       "林澈",
			AvailableOptions:  []string{"暂不操作", humanDecision},
			ChosenDecision:    humanDecision,
			DecisionReason:    "饭桌压力不能替代资金真实性核验",
			ObservableEffects: []string{"专项额度已经在本人界面出现"},
			HiddenPressures:   []string{"其他角色尚未知道系统存在"},
			PlanConstraints:   []string{"只使用本人界面与现场可验证结果"},
			CausalChain:       []string{"额度出现", "先排除风险", "选择小额试验"},
		},
	}
	normalizeProtagonistProjection(st, &simulation)
	if simulation.ProtagonistProjection.ChosenDecision != humanDecision {
		t.Fatalf("authority sentinel erased source-bound human projection: %+v", simulation.ProtagonistProjection)
	}
}

func TestNormalizeProtagonistProjectionRejectsSentinelOnlyProjection(t *testing.T) {
	st := newChapterSimulationTestStore(t)
	simulation := domain.ChapterWorldSimulation{
		Chapter: 1,
		CharacterDecisions: []domain.CharacterWorldDecision{
			rewriteSourceOnlySentinelDecision("林澈", 1, nil),
		},
		ProtagonistProjection: domain.ProtagonistDecisionProjection{
			Protagonist:       "林澈",
			AvailableOptions:  []string{simulationAuthorityPreserve, simulationAuthorityNoAlternative},
			ChosenDecision:    simulationAuthorityPreserve,
			DecisionReason:    simulationAuthorityEvidenceOnly,
			ObservableEffects: []string{simulationAuthoritySourceEffect},
			HiddenPressures:   []string{simulationAuthorityBlockedEffect},
			PlanConstraints:   []string{"不得补猜"},
			CausalChain:       []string{"仅物化权威合同"},
		},
	}
	normalizeProtagonistProjection(st, &simulation)
	if simulation.ProtagonistProjection.ChosenDecision != "" {
		t.Fatalf("sentinel-only projection must remain incomplete: %+v", simulation.ProtagonistProjection)
	}
	gaps := chapterWorldSimulationGaps(st, simulation)
	if !slices.Contains(gaps, "incomplete protagonist_projection") {
		t.Fatalf("sentinel-only projection must block finalize, gaps=%#v", gaps)
	}
}

func TestNormalizeGroundedProtagonistProjectionBindsOnlyChosenDecision(t *testing.T) {
	st := newChapterSimulationTestStore(t)
	decision := simulatedDecision("林澈", "先做一笔可核验的小额县内试验", true)
	decision.AvailableOptions = []string{"暂缓操作", "先做一笔可核验的小额县内试验"}
	decision.DecisionReason = "先用现场付款和真实货物排除额度风险"
	simulation := domain.ChapterWorldSimulation{
		Chapter:            1,
		CharacterDecisions: []domain.CharacterWorldDecision{decision},
		AuthorityReceipt: &domain.SimulationAuthorityReceipt{
			Mode:               domain.SimulationAuthorityModeGrounded,
			GroundedCharacters: []string{"林澈"},
		},
		ProtagonistProjection: domain.ProtagonistDecisionProjection{
			Protagonist:       "林澈",
			ObservableEffects: []string{"额度与货物都能现场核验"},
			HiddenPressures:   []string{"监管复看尚未完成"},
			AvailableOptions:  []string{"继续观察现场证据", "模型改写后的另一组选项"},
			ChosenDecision:    "模型改写后的选择",
			DecisionReason:    "模型改写后的理由",
			PlanConstraints:   []string{"只写主角已知事实"},
			CausalChain:       []string{"额度出现", "小额试验", "现场核验"},
		},
	}

	normalizeProtagonistProjection(st, &simulation)

	projection := simulation.ProtagonistProjection
	if projection.ChosenDecision != decision.Decision {
		t.Fatalf("grounded chosen decision was not server-bound: %+v", projection)
	}
	if !slices.Equal(projection.AvailableOptions, []string{"继续观察现场证据", "模型改写后的另一组选项"}) ||
		projection.DecisionReason != "模型改写后的理由" {
		t.Fatalf("server binding overwrote time-correct POV options/reason: %+v", projection)
	}
	if !slices.Equal(projection.ObservableEffects, []string{"额度与货物都能现场核验"}) ||
		!slices.Equal(projection.CausalChain, []string{"额度出现", "小额试验", "现场核验"}) {
		t.Fatalf("server binding overwrote model-authored POV evidence: %+v", projection)
	}
}

func TestProjectAllGroundedAuthorityReceiptBindsHumanDecisionAndResume(t *testing.T) {
	st := newChapterSimulationTestStore(t)
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{
		Chapter:   1,
		Title:     "额度到账",
		CoreEvent: "林澈在青山县河畔夜市先核验专项额度，再完成一笔可撤回的小额县内试验；首笔真实县内支出完成并首次结算。",
		Hook:      "林澈约定次日继续核验。",
	}}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"林澈", "沈知遥"} {
		if err := st.SaveCharacterDossier(domain.CharacterDossier{
			Version: 1, Character: name, Role: map[string]string{"林澈": "主角", "沈知遥": "女主"}[name], Tier: "core",
			Profile: domain.CharacterDossierProfile{Description: name + "的角色卡"},
			CurrentAtStoryStart: domain.CharacterStartState{
				Location:      "故事开始前未进入主角视角；正式出场前按角色卡和交通规则补足位置",
				Status:        "存活/状态待正文确认",
				CurrentAction: "按自身开局目标行动或被自身场景压力困住。",
				Pressure:      "第一章现场压力正在逼近",
			},
			KnowledgeBoundary: "只知道本人经历和合法通信获得的信息",
			DecisionModel:     "按自身目标、恐惧、资源、关系和现场证据选择，不为主角工具化。",
		}); err != nil {
			t.Fatal(err)
		}
	}
	dynamics := map[string]any{
		"version": 1, "scope": "zero_chapter", "chapter": 1,
		"characters": []domain.CharacterSimulationState{{
			Character:      "林澈",
			CurrentGoal:    "顶住饭桌上的难堪，先确认异常资金不会连累家人。",
			Pressure:       "亲戚追问失业与下一份工作，异常额度同时出现。",
			Resources:      []string{"角色卡既有经验", "第一章可见事实", "可复核的县内交易票据"},
			ActionTendency: "林澈先缩小风险，再用现场可核验动作换取新证据。",
			DecisionFrame: domain.CharacterDecisionFrame{
				DecisionRule: "先核验证据，再决定是否交易、承诺或升级投入。",
			},
			KnowledgeLedger: domain.CharacterKnowledgeLedger{
				KnownFacts:         []string{"林澈只知道本人界面与现场证据"},
				ForbiddenKnowledge: []string{"其他角色未传回的时间线"},
			},
		}},
	}
	raw, err := json.Marshal(dynamics)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(st.Dir(), "meta", "initial_character_dynamics.json"),
		raw,
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	planningContext, contextToken := installPlanningContextAccessProjectAll(
		t,
		st,
		1,
		"grounded-receipt-first-lease",
	)
	manifest, err := json.Marshal(projectAllAuthorityWorkspaceManifest{
		Version:                "project-all-workspace.v3",
		GenerationID:           planningContext.GenerationID,
		SourceOutput:           st.Dir(),
		BaseChapter:            0,
		Workspace:              st.Dir(),
		IsolatedWrites:         true,
		FoundationSnapshotRoot: sealedRAGGuardDigest(t, "receipt-foundation"),
		RAGSnapshotRoot:        sealedRAGGuardDigest(t, "receipt-rag"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(st.Dir(), "meta", "project_all_workspace_manifest.json"),
		manifest,
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	accessToken := readPlanningContextAccessToken(t, st, 1, "world_simulation")
	decisionText := "先核验专项额度，再完成一笔可撤回的小额县内试验"
	grounded := simulatedDecision("林澈", decisionText, true)
	grounded.Location = "青山县河畔夜市"
	grounded.CurrentGoal = dynamics["characters"].([]domain.CharacterSimulationState)[0].CurrentGoal
	grounded.Pressure = "异常额度同时出现"
	grounded.Resources = []string{"可复核的县内交易票据"}
	grounded.KnowledgeBoundary = "只知道本人经历和合法通信获得的信息"
	grounded.AvailableOptions = []string{"暂缓夜市试验", decisionText}
	grounded.Action = "先核验专项额度，再完成一笔可撤回的小额县内试验"
	grounded.DecisionReason = "先核验专项额度能把风险压到一笔可撤回的小额县内试验"
	grounded.ImmediateResult = "首笔真实县内支出完成并首次结算"
	grounded.StateAfter = "林澈约定次日继续核验"
	grounded.ButterflyEffects[0].Effect = "首笔真实县内支出完成并首次结算"
	grounded.ButterflyEffects[0].ProtagonistImpact = "林澈约定次日继续核验"
	var groundedEntry simulationCharacterAuthority
	for _, entry := range buildSimulationCharacterAuthority(st, 1) {
		if entry.Character == "林澈" {
			groundedEntry = entry
			break
		}
	}
	for name, mutate := range map[string]func(*domain.CharacterWorldDecision){
		"scene synopsis used as location": func(decision *domain.CharacterWorldDecision) {
			decision.Location = "林澈在青山县河畔夜市先核验专项额度，再完成一笔可撤回的小额县内试验"
		},
		"current goal copied as action": func(decision *domain.CharacterWorldDecision) {
			decision.Action = decision.CurrentGoal
		},
		"invented location suffix": func(decision *domain.CharacterWorldDecision) {
			decision.Location = "青山县河畔夜市旁地下实验室"
		},
		"invented pressure suffix": func(decision *domain.CharacterWorldDecision) {
			decision.Pressure = "异常额度同时出现，因此地下实验室正在倒计时"
		},
		"invented knowledge suffix": func(decision *domain.CharacterWorldDecision) {
			decision.KnowledgeBoundary += "；并知道地下实验室的密码"
		},
		"invented action suffix": func(decision *domain.CharacterWorldDecision) {
			decision.Action = "先核验专项额度，再完成一笔可撤回的小额县内试验并秘密扩张"
		},
		"invented decision reason": func(decision *domain.CharacterWorldDecision) {
			decision.DecisionReason = "地下实验室已经开启，所以必须立即交易"
		},
		"invented effect suffix": func(decision *domain.CharacterWorldDecision) {
			decision.ButterflyEffects[0].Effect = "首笔真实县内支出完成并首次结算，地下实验室随即开启"
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := grounded
			candidate.ButterflyEffects = append(
				[]domain.DecisionButterflyEffect(nil),
				grounded.ButterflyEffects...,
			)
			mutate(&candidate)
			if err := validateProjectAllGroundedDecision(
				st,
				1,
				groundedEntry,
				candidate,
			); err == nil {
				t.Fatal("grounded validator accepted an outline-prefix invention")
			}
		})
	}
	projection := domain.ProtagonistDecisionProjection{
		Protagonist:       "林澈",
		ObservableEffects: []string{"专项额度已在本人界面出现"},
		HiddenPressures:   []string{"沈知遥尚未进入现场"},
		AvailableOptions:  []string{"先暂缓交易，继续核验专项额度", decisionText},
		ChosenDecision:    decisionText,
		DecisionReason:    grounded.DecisionReason,
		PlanConstraints:   []string{"只能写本人可见的额度与交易结果"},
		CausalChain:       []string{"专项额度出现", "先核验专项额度", "完成小额县内试验"},
	}
	args, err := json.Marshal(map[string]any{
		"chapter":                       1,
		"time_window":                   "返乡当日晚饭至夜市收摊前",
		"character_decisions":           []domain.CharacterWorldDecision{grounded},
		"authority_contract_characters": []string{"沈知遥"},
		"protagonist_projection":        projection,
		"sources":                       []string{contextToken, accessToken, "current_chapter_outline"},
		"finalize":                      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewSimulateChapterWorldTool(st).Execute(t.Context(), args); err != nil {
		t.Fatalf("grounded project-all simulation did not finalize: %v", err)
	}
	simulation, err := st.LoadChapterWorldSimulation(1)
	if err != nil || simulation == nil {
		t.Fatalf("load grounded simulation: sim=%+v err=%v", simulation, err)
	}
	if simulation.GenerationID != planningContext.GenerationID ||
		simulation.AuthorityReceipt == nil ||
		simulation.AuthorityReceipt.GenerationID != planningContext.GenerationID ||
		simulation.ProtagonistProjection.ChosenDecision != decisionText ||
		!validSimulationAuthorityDigest(simulation.AuthorityReceipt.ReceiptDigest) {
		t.Fatalf("grounded receipt identity incomplete: %+v", simulation)
	}
	if !containsExactString(
		simulation.ProtagonistProjection.AvailableOptions,
		"先暂缓交易，继续核验专项额度",
	) {
		t.Fatalf("server overwrote a grounded, time-correct POV alternative: %+v", simulation.ProtagonistProjection)
	}
	tamperedProjection := *simulation
	tamperedProjection.ProtagonistProjection = simulation.ProtagonistProjection
	tamperedProjection.ProtagonistProjection.HiddenPressures = []string{
		"专项额度出现，地下实验室的密码同时解锁",
	}
	if gaps := projectAllGroundedProjectionGaps(st, tamperedProjection); len(gaps) == 0 {
		t.Fatal("grounded protagonist projection accepted a legal-prefix secret")
	}
	if gaps := chapterWorldSimulationGaps(st, *simulation); len(gaps) != 0 {
		t.Fatalf("fresh grounded receipt did not revalidate under issuing lease: %#v", gaps)
	}

	if err := st.Runtime.ReleasePipelineExecution("grounded-receipt-first-lease"); err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionProjectAll,
		TargetChapter: 1,
		Owner:         "grounded-receipt-resume-lease",
	}); err != nil {
		t.Fatal(err)
	}
	if gaps := chapterWorldSimulationGaps(st, *simulation); len(gaps) != 0 {
		t.Fatalf("same input root could not resume under a new lease: %#v", gaps)
	}
	tampered := *simulation
	tampered.CharacterDecisions = append(
		[]domain.CharacterWorldDecision(nil),
		simulation.CharacterDecisions...,
	)
	for i := range tampered.CharacterDecisions {
		if tampered.CharacterDecisions[i].Character == "林澈" {
			tampered.CharacterDecisions[i].Decision = "跳过核验直接扩大投入"
		}
	}
	if err := validateStoredSimulationAuthorityReceipt(st, tampered); err == nil {
		t.Fatal("grounded decision tamper passed receipt validation")
	}
}

func TestProjectAllGroundingUsesPriorContinuityAndLateEntrySeed(t *testing.T) {
	st := newChapterSimulationTestStore(t)
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{
		Chapter:   2,
		Title:     "次日复核",
		CoreEvent: "林澈与沈知遥在次日河畔夜市复核第一笔交易和走线安全。",
		Hook:      "沈知遥留下下一次现场核验要求。",
	}}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"林澈", "沈知遥"} {
		if err := st.SaveCharacterDossier(domain.CharacterDossier{
			Version: 1, Character: name, Role: "核心角色", Tier: "core",
			Profile:           domain.CharacterDossierProfile{Description: name + "的角色卡"},
			KnowledgeBoundary: "只知道本人经历和合法通信获得的信息",
		}); err != nil {
			t.Fatal(err)
		}
	}
	seed := map[string]any{
		"version": 1, "scope": "zero_chapter", "chapter": 1,
		"characters": []domain.CharacterSimulationState{
			{
				Character:      "林澈",
				CurrentGoal:    "第一章种子目标，不得覆盖后续连续态",
				Pressure:       "第一章种子压力，不得覆盖后续连续态",
				ActionTendency: "林澈按角色卡谨慎核验",
				DecisionFrame:  domain.CharacterDecisionFrame{DecisionRule: "林澈稳定决策规则"},
				KnowledgeLedger: domain.CharacterKnowledgeLedger{
					KnownFacts: []string{"林澈知道自己的经历"}, ForbiddenKnowledge: []string{"未通信事实"},
				},
			},
			{
				Character:      "沈知遥",
				CurrentGoal:    "首次正式入场时守住现场安全边界",
				Pressure:       "首次入场必须同时面对商户利益和走线风险",
				ActionTendency: "沈知遥先看现场证据再给出合规边界",
				DecisionFrame:  domain.CharacterDecisionFrame{DecisionRule: "沈知遥稳定决策规则"},
				KnowledgeLedger: domain.CharacterKnowledgeLedger{
					KnownFacts: []string{"沈知遥知道自己的监管职责"}, ForbiddenKnowledge: []string{"林澈未披露的秘密"},
				},
			},
		},
	}
	seedRaw, err := json.Marshal(seed)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(st.Dir(), "meta", "initial_character_dynamics.json"),
		seedRaw,
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	const generationID = "pg2_continuity_test"
	if err := st.SaveChapterWorldDelta(domain.ChapterWorldDelta{
		Version: 1, Chapter: 1, GenerationID: generationID,
		CharacterDeltas: []domain.CharacterChapterDelta{{
			Character:         "林澈",
			Location:          "河畔夜市入口",
			Status:            "第一笔小额试验已完成，等待次日复核",
			CurrentAction:     "保留票据并约定次日复核",
			Decision:          "完成小额试验后暂不扩大",
			DecisionReason:    "这是上一章一次性理由，不是稳定决策模型",
			KnowledgeBoundary: "只知道本人经历和合法通信获得的信息",
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.World.AppendStateChanges([]domain.StateChange{
		{Chapter: 1, Entity: "林澈", Field: "goal", NewValue: "复核第一笔交易并确认安全边界", FactKey: "林澈:goal"},
		{Chapter: 1, Entity: "林澈", Field: "pressure", NewValue: "次日复核必须面对票据与走线问题", FactKey: "林澈:pressure"},
		{Chapter: 1, Entity: "林澈", Field: "decision_frame", NewValue: "先复核票据和现场，再决定是否扩大", FactKey: "林澈:decision_frame"},
	}); err != nil {
		t.Fatal(err)
	}
	context := domain.ProjectedPlanningContextV2{
		Version:        domain.ProjectedPlanningContextV2Version,
		GenerationID:   generationID,
		NextChapter:    2,
		ThroughChapter: 1,
		StateRoot:      sealedRAGGuardDigest(t, "continuity-state-root"),
		CumulativeState: []domain.ProjectedPlanningStateFactV2{
			{Category: "character_state", StableID: "lin-state", Subject: "林澈", Field: "state", Value: "等待次日现场复核", ThroughChapter: 1},
			{Category: "knowledge", StableID: "lin-knowledge", Subject: "林澈", Field: "knowledge_boundary", Value: "只知道本人经历和合法通信获得的信息", ThroughChapter: 1},
			{Category: "location", StableID: "lin-location", Subject: "林澈", Field: "location", Value: "次日河畔夜市", ThroughChapter: 1},
		},
	}
	context.ContextDigest, err = domain.ComputeProjectedPlanningContextV2Digest(context)
	if err != nil {
		t.Fatal(err)
	}
	if err := domain.ValidateProjectedPlanningContextV2(context); err != nil {
		t.Fatal(err)
	}
	contextRaw, err := json.Marshal(context)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(st.Dir(), filepath.FromSlash(projectAllStateContextPath)),
		contextRaw,
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(projectAllAuthorityWorkspaceManifest{
		Version:                "project-all-workspace.v3",
		GenerationID:           generationID,
		SourceOutput:           st.Dir(),
		BaseChapter:            0,
		Workspace:              st.Dir(),
		IsolatedWrites:         true,
		FoundationSnapshotRoot: sealedRAGGuardDigest(t, "continuity-foundation"),
		RAGSnapshotRoot:        sealedRAGGuardDigest(t, "continuity-rag"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(st.Dir(), "meta", "project_all_workspace_manifest.json"),
		manifest,
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionProjectAll,
		TargetChapter: 2,
		Owner:         "continuity-grounding-test",
	}); err != nil {
		t.Fatal(err)
	}
	authority := buildSimulationCharacterAuthority(st, 2)
	byName := map[string]simulationCharacterAuthority{}
	for _, entry := range authority {
		byName[entry.Character] = entry
	}
	protagonist := byName["林澈"]
	if protagonist.AuthorityMode != "project_all_grounded" ||
		protagonist.CurrentGoal != "复核第一笔交易并确认安全边界" ||
		protagonist.CurrentPressure != "次日复核必须面对票据与走线问题" ||
		protagonist.CurrentAction != "保留票据并约定次日复核" ||
		protagonist.CurrentLocation != "次日河畔夜市" ||
		protagonist.DecisionModel != "先复核票据和现场，再决定是否扩大" {
		t.Fatalf("chapter-two continuity did not override chapter-one seed: %+v", protagonist)
	}
	lateEntry := byName["沈知遥"]
	if lateEntry.AuthorityMode != "project_all_grounded" ||
		lateEntry.CurrentGoal != "首次正式入场时守住现场安全边界" ||
		lateEntry.CurrentAction != "沈知遥先看现场证据再给出合规边界" ||
		lateEntry.DecisionModel != "沈知遥稳定决策规则" {
		t.Fatalf("first late entry could not use its untouched actor seed: %+v", lateEntry)
	}
}

func TestProjectAllGroundedCoarseOutlineAllowsDerivedOutputsWithoutNovelty(t *testing.T) {
	st := newChapterSimulationTestStore(t)
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{
		Chapter:   9,
		Title:     "承载边界",
		CoreEvent: "推进河畔夜市试点，林澈逐摊核对真实反馈，验证商户承载边界并留下可复核后果。",
		Hook:      "林澈进入下一轮复核。",
	}}); err != nil {
		t.Fatal(err)
	}
	entry := simulationCharacterAuthority{
		Character:             "林澈",
		CurrentLocation:       simulationAuthorityUnknown,
		CurrentStatus:         simulationAuthorityUnknown,
		CurrentGoal:           "推进河畔夜市试点",
		CurrentAction:         "逐摊核对真实反馈",
		CurrentPressure:       "验证商户承载边界",
		CurrentPressurePolicy: "outline_authorized_concise",
		KnowledgeBoundary:     "只知道本人经历和合法通信获得的信息",
		DecisionModel:         "先验证承载边界，再决定是否扩大",
		Resources:             []string{},
	}
	decision := domain.CharacterWorldDecision{
		Character:         "林澈",
		Location:          "河畔夜市",
		CurrentGoal:       entry.CurrentGoal,
		Pressure:          entry.CurrentPressure,
		Resources:         []string{},
		KnowledgeBoundary: entry.KnowledgeBoundary,
		AvailableOptions:  []string{"暂缓河畔夜市试点", "逐摊核对真实反馈"},
		Decision:          "逐摊核对真实反馈",
		DecisionReason:    "逐摊取得一轮真实反馈后才能验证商户承载边界",
		Action:            "逐摊核对真实反馈",
		ActionDuration:    "一个营业时段",
		CompletionState:   "in_progress",
		ImmediateResult:   "河畔夜市试点形成一项待复核的摊位反馈",
		StateAfter:        "河畔夜市试点转入次轮承载复核",
		VisibleToPOV:      true,
		ButterflyEffects: []domain.DecisionButterflyEffect{{
			Effect:            "夜市试点的摊位反馈进入下一轮复核",
			Targets:           []string{"林澈"},
			TransmissionPath:  "现场记录",
			ArrivalChapter:    10,
			Visibility:        "visible",
			ProtagonistImpact: "林澈下一轮仍需复核夜市试点",
		}},
	}
	if strings.Contains(
		"推进河畔夜市试点，林澈逐摊核对真实反馈，验证商户承载边界并留下可复核后果。",
		decision.ImmediateResult,
	) {
		t.Fatal("coarse regression did not exercise genuinely projected output")
	}
	if err := validateProjectAllGroundedDecision(st, 9, entry, decision); err != nil {
		t.Fatalf("coarse outline could not produce a content-addressed new result: %v", err)
	}

	t.Run("legal prefix cannot smuggle secret", func(t *testing.T) {
		tampered := decision
		tampered.ImmediateResult += "，地下实验室的密码同时解锁"
		if err := validateProjectAllGroundedDecision(st, 9, entry, tampered); err == nil {
			t.Fatal("grounded projected output accepted an invented secret suffix")
		}
	})
	t.Run("legal prefix cannot smuggle resource", func(t *testing.T) {
		tampered := decision
		tampered.StateAfter += "，两辆直升机降落"
		if err := validateProjectAllGroundedDecision(st, 9, entry, tampered); err == nil {
			t.Fatal("grounded projected output accepted an invented resource suffix")
		}
	})
	t.Run("effect target remains authorized", func(t *testing.T) {
		tampered := decision
		tampered.ButterflyEffects = append(
			[]domain.DecisionButterflyEffect(nil),
			decision.ButterflyEffects...,
		)
		tampered.ButterflyEffects[0].Targets = []string{"王总"}
		if err := validateProjectAllGroundedDecision(st, 9, entry, tampered); err == nil {
			t.Fatal("grounded projected output accepted an unknown effect target")
		}
	})
}

func TestSimulationAuthorityBindingAllowsLeaseRotationOnlyByContinuity(t *testing.T) {
	acquired := time.Unix(1_700_000_000, 0).UTC()
	first := &domain.SimulationAuthorityReceipt{
		Version:                domain.SimulationAuthorityReceiptVersion,
		Mode:                   domain.SimulationAuthorityModeGrounded,
		GenerationID:           "pg2_lease_rotation",
		Chapter:                2,
		ThroughChapter:         1,
		PlanningContextDigest:  sealedRAGGuardDigest(t, "lease-context"),
		ProjectedStateRoot:     sealedRAGGuardDigest(t, "lease-state"),
		FoundationSnapshotRoot: sealedRAGGuardDigest(t, "lease-foundation"),
		AuthorityInputRoot:     sealedRAGGuardDigest(t, "lease-input"),
		InitialDynamicsSHA256:  sealedRAGGuardDigest(t, "lease-dynamics"),
		PriorWorldDeltaSHA256:  sealedRAGGuardDigest(t, "lease-delta"),
		StateChangesSHA256:     sealedRAGGuardDigest(t, "lease-changes"),
		GroundedCharacters:     []string{"林澈"},
		HoldBaselineCharacters: []string{"沈知遥"},
		RewriteSourceAbsent:    true,
		LockOwner:              "first-owner",
		LockProcessID:          101,
		LockAcquiredAt:         acquired,
	}
	secondValue := *first
	secondValue.LockOwner = "resume-owner"
	secondValue.LockProcessID = 202
	secondValue.LockAcquiredAt = acquired.Add(time.Minute)
	second := &secondValue
	if sameSimulationAuthorityBinding(first, second) {
		t.Fatal("issuance binding ignored lease identity")
	}
	if !sameSimulationAuthorityContinuityBinding(first, second) {
		t.Fatal("same authoritative inputs could not rotate a crashed partial lease")
	}
	second.AuthorityInputRoot = sealedRAGGuardDigest(t, "different-input")
	if sameSimulationAuthorityContinuityBinding(first, second) {
		t.Fatal("lease rotation hid authoritative input drift")
	}
}

func TestFinalizedWorldSimulationRejectsStaleSimulationID(t *testing.T) {
	st := newChapterSimulationTestStore(t)
	simulation := domain.ChapterWorldSimulation{
		Version:            1,
		Chapter:            1,
		TimeWindow:         "当天夜里",
		CharacterDecisions: []domain.CharacterWorldDecision{},
		AuthorityReceipt:   &domain.SimulationAuthorityReceipt{},
		ProtagonistProjection: domain.ProtagonistDecisionProjection{
			Protagonist:      "林澈",
			ChosenDecision:   "先核验再行动",
			DecisionReason:   "风险仍未排除",
			AvailableOptions: []string{"先核验再行动", "暂时等待"},
		},
	}
	simulation.SimulationID = chapterWorldSimulationID(simulation)
	if gaps := chapterWorldSimulationGaps(st, simulation); slices.ContainsFunc(gaps, func(gap string) bool {
		return strings.Contains(gap, "simulation_id payload digest mismatch")
	}) {
		t.Fatalf("fresh simulation ID was rejected: %#v", gaps)
	}
	simulation.ProtagonistProjection.DecisionReason += "（被篡改）"
	gaps := chapterWorldSimulationGaps(st, simulation)
	if !slices.ContainsFunc(gaps, func(gap string) bool {
		return strings.Contains(gap, "simulation_id payload digest mismatch")
	}) {
		t.Fatalf("payload changed without invalidating simulation_id: %#v", gaps)
	}
}

func TestSimulationCharacterAuthorityKeepsOnlyConcreteCurrentFacts(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Characters.Save([]domain.Character{{Name: "林澈", Role: "主角", Tier: "core"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveCharacterDossier(domain.CharacterDossier{
		Version: 1, Character: "林澈", Role: "主角", Tier: "core",
		Profile:               domain.CharacterDossierProfile{Description: "返乡青年", Traits: []string{"谨慎", "行动派"}, Desires: []string{"先保住饭桌上的体面"}},
		Resources:             []domain.CharacterResource{{Name: "手机", Status: "available"}},
		KnowledgeBoundary:     "不知道场外角色未传回的信息",
		DecisionModel:         "先确认现场证据，再选择可逆行动",
		CommunicationBoundary: domain.CommunicationBoundary{CanContactProtagonist: true, Channels: []string{"自我行动"}},
		CurrentAtStoryStart: domain.CharacterStartState{
			Location: "林家饭桌", Status: "清醒", CurrentAction: "应付亲戚追问",
			Pressure: "亲戚追问工作去向", NextIndependentMove: "把话题转回饭桌账单",
		},
	}); err != nil {
		t.Fatal(err)
	}

	authority := buildSimulationCharacterAuthority(st, 1)
	if len(authority) != 1 {
		t.Fatalf("unexpected authority: %+v", authority)
	}
	got := authority[0]
	if got.Blocking || got.AuthorityMode != "authoritative" || got.CurrentLocation != "林家饭桌" || got.CurrentAction != "应付亲戚追问" || !slices.Contains(got.Resources, "手机（available）") {
		t.Fatalf("concrete dossier facts were not preserved: %+v", got)
	}
	if !strings.Contains(got.DecisionPolicy, "不得把 arc") {
		t.Fatalf("future arc boundary missing: %+v", got)
	}
}

func TestSimulationAuthorityPinsPreserveFactKnowledgeBoundaryIndependentlyOfModel(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("knowledge lock", 2); err != nil {
		t.Fatal(err)
	}
	if err := st.Characters.Save([]domain.Character{{Name: "林澈", Role: "主角", Tier: "core"}, {Name: "贺骁", Role: "配角", Tier: "core"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "章末电话", CoreEvent: "林澈拨通贺骁"}}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"林澈", "贺骁"} {
		if err := st.SaveCharacterDossier(domain.CharacterDossier{
			Version: 1, Character: name, Role: "配角", Tier: "core",
			Profile: domain.CharacterDossierProfile{Desires: []string{"守住当前边界"}},
			CurrentAtStoryStart: domain.CharacterStartState{
				Location: "青山县", Status: "清醒", CurrentAction: "处理手头事务", Pressure: "时间有限", NextIndependentMove: "只按已知信息行动",
			},
			Resources:         []domain.CharacterResource{{Name: "手机", Status: "available"}},
			KnowledgeBoundary: "只知道亲历与明确通信", DecisionModel: "先核验证据",
		}); err != nil {
			t.Fatal(err)
		}
	}
	const locked = "贺骁不知道林澈已回青山县、位于夜市附近或正在做夜市项目，也不得凭单向背景音推断这些位置与行动信息"
	prepareRewriteSourceTest(t, st, "第一章\n\n电话只传来贺骁一侧的扳手声。",
		"# 返工\n\n## 保留事实\n\n- "+locked+"。\n")
	var he, lin simulationCharacterAuthority
	for _, entry := range buildSimulationCharacterAuthority(st, 1) {
		if entry.Character == "贺骁" {
			he = entry
		}
		if entry.Character == "林澈" {
			lin = entry
		}
	}
	if len(he.RequiredKnowledgeBoundary) != 1 || he.RequiredKnowledgeBoundary[0] != locked {
		t.Fatalf("preserve knowledge lock missing from authority packet: %+v", he)
	}
	if len(lin.RequiredKnowledgeBoundary) != 0 {
		t.Fatalf("knowledge object was mistaken for the epistemic subject: %+v", lin)
	}
	decision := simulatedDecision("贺骁", "接起电话", true)
	decision.KnowledgeBoundary = "只知道亲历与明确通信"
	if err := validateIncomingSimulationCharacterAuthority(st, 1, []domain.CharacterWorldDecision{decision}); err == nil || !strings.Contains(err.Error(), "required_preserve_clause[0]") {
		t.Fatalf("model-deleted preserve knowledge boundary passed: %v", err)
	}
	decision.KnowledgeBoundary += "；" + locked
	if err := validateIncomingSimulationCharacterAuthority(st, 1, []domain.CharacterWorldDecision{decision}); err != nil {
		t.Fatalf("exact preserve knowledge boundary was rejected: %v", err)
	}
}

func TestBlockingAuthorityContractAndValidatorShareMergedKnowledgeLock(t *testing.T) {
	const locked = "贺骁不知道林澈已回青山县，也不得凭背景音推断位置"
	evidence := []string{"电话那头响了一声。"}
	decision := rewriteSourceOnlySentinelDecision("贺骁", 1, evidence)
	decision.KnowledgeBoundary = mergedAuthorityKnowledgeBoundary(decision.KnowledgeBoundary, []string{locked})
	if err := validateRequiredKnowledgeBoundaries(decision, []string{locked}); err != nil {
		t.Fatalf("merged contract lost the independent knowledge lock: %v", err)
	}
	if err := validateRewriteSourceOnlyDecision(1, decision, evidence, []string{locked}); err != nil {
		t.Fatalf("exact merged rewrite contract was rejected: %v", err)
	}
	payload := rewriteSourceOnlyContractPayload("贺骁", 1, evidence, []string{locked})
	if got, _ := payload["knowledge_boundary"].(string); got != decision.KnowledgeBoundary {
		t.Fatalf("published contract and validator disagree: payload=%q decision=%q", got, decision.KnowledgeBoundary)
	}

	hold := holdBaselineSentinelDecision("贺骁", 1)
	hold.KnowledgeBoundary = mergedAuthorityKnowledgeBoundary(hold.KnowledgeBoundary, []string{locked})
	if err := validateHoldBaselineDecision(1, hold, []string{locked}); err != nil {
		t.Fatalf("exact merged hold contract was rejected: %v", err)
	}
}

func TestKnowledgeBoundarySubjectIndexDoesNotBindNamedObject(t *testing.T) {
	clause := "贺骁不知道林澈已回青山县、位于夜市附近，也不得凭单向背景音推断这些信息"
	if got := knowledgeBoundarySubjectIndex(clause, "贺骁"); got != 0 {
		t.Fatalf("explicit epistemic subject was not recognized: %d", got)
	}
	if got := knowledgeBoundarySubjectIndex(clause, "林澈"); got != -1 {
		t.Fatalf("named knowledge object was bound as subject: %d", got)
	}
	if got := knowledgeBoundarySubjectIndex("沈知遥此时仍不知道系统存在", "沈知遥"); got != 0 {
		t.Fatalf("subject with epistemic modifiers was not recognized: %d", got)
	}
}

func TestSimulationCharacterAuthorityPolicyMatchesRoster(t *testing.T) {
	authority := []simulationCharacterAuthority{
		{Character: "甲", Blocking: false},
		{Character: "乙", Blocking: true},
	}
	policy := simulationCharacterAuthorityPolicy(authority)
	if policy["required_count"] != 2 || policy["blocking_count"] != 1 || !strings.Contains(policy["policy"].(string), "unknown") {
		t.Fatalf("unexpected policy: %+v", policy)
	}
}

func TestStagedPlanRepairKeepsSimulationCharacterAuthority(t *testing.T) {
	st := newChapterSimulationTestStore(t)
	if err := st.SaveCharacterDossier(domain.CharacterDossier{
		Version: 1, Character: "林澈", Role: "主角", Tier: "core",
		Profile: domain.CharacterDossierProfile{Description: "返乡青年"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveChapterPlanPartial(1, map[string]any{
		"structure":         map[string]any{"chapter": 1, "title": "待修骨架"},
		"causal_simulation": map[string]any{},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveChapterWorldSimulationPartial(domain.ChapterWorldSimulation{
		Version: 1, Chapter: 1,
		CharacterDecisions: []domain.CharacterWorldDecision{{Character: "林澈"}},
	}); err != nil {
		t.Fatal(err)
	}

	result, ok, err := NewContextTool(st, References{}, "default").stagedPlanRepairContext(1, 1, true)
	if err != nil || !ok {
		t.Fatalf("stagedPlanRepairContext: ok=%v err=%v", ok, err)
	}
	authority, ok := result["simulation_character_authority"].([]simulationCharacterAuthority)
	if !ok || len(authority) != 2 {
		t.Fatalf("staged repair lost the authoritative roster: %#v", result["simulation_character_authority"])
	}
	if authority[0].Character != "林澈" || authority[0].SimulationStatus != "already_present" || authority[1].Character != "沈知遥" {
		t.Fatalf("staged authority did not preserve present/missing identities: %+v", authority)
	}
	policy, ok := result["simulation_character_authority_policy"].(map[string]any)
	if !ok || policy["required_count"] != 2 {
		t.Fatalf("staged authority policy missing: %#v", result["simulation_character_authority_policy"])
	}
}

func TestHoldBaselineAuthorityGuardRejectsNarrativeGuessing(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Characters.Save([]domain.Character{
		{Name: "林澈", Role: "主角", Tier: "core"},
		{Name: "许牧", Role: "主角团配角", Tier: "core", Description: "主角旧友"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveCharacterDossier(domain.CharacterDossier{
		Version: 1, Character: "许牧", Role: "主角团配角", Tier: "core",
		Profile: domain.CharacterDossierProfile{Description: "主角旧友"},
		CurrentAtStoryStart: domain.CharacterStartState{
			Location:      "故事开始前未进入主角视角；正式出场前按角色卡和交通规则补足位置",
			CurrentAction: "按自身开局目标行动或被自身场景压力困住。",
		},
	}); err != nil {
		t.Fatal(err)
	}
	guessed := holdBaselineDecisionForTest("许牧", 1)
	guessed.DecisionReason = "人在外地，尚未收到主角消息"
	if err := validateIncomingSimulationCharacterAuthority(st, 1, []domain.CharacterWorldDecision{guessed}); err == nil || !strings.Contains(err.Error(), "decision_reason") || !strings.Contains(err.Error(), "hold_baseline_contract") {
		t.Fatalf("narrative guess passed hold-baseline guard: %v", err)
	}
	exact := holdBaselineDecisionForTest("许牧", 1)
	if err := validateIncomingSimulationCharacterAuthority(st, 1, []domain.CharacterWorldDecision{exact}); err != nil {
		t.Fatalf("exact sentinel contract rejected: %v", err)
	}
}

func TestHoldBaselineAuthorityPacketPublishesEveryValidatedSentinelField(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Characters.Save([]domain.Character{
		{Name: "林澈", Role: "主角", Tier: "core"},
		{Name: "许牧", Role: "配角", Tier: "important"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveCharacterDossier(domain.CharacterDossier{
		Version: 1, Character: "林澈", Role: "主角", Tier: "core",
		CurrentAtStoryStart: domain.CharacterStartState{Location: "林家饭桌", CurrentAction: "应付亲戚追问"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveCharacterDossier(domain.CharacterDossier{
		Version: 1, Character: "许牧", Role: "配角", Tier: "important",
		CurrentAtStoryStart: domain.CharacterStartState{
			Location: "故事开始前未进入主角视角；正式出场前按角色卡和交通规则补足位置",
		},
	}); err != nil {
		t.Fatal(err)
	}
	authority := buildSimulationCharacterAuthority(st, 1)
	if len(authority) != 2 || authority[1].Character != "许牧" || authority[1].AuthorityMode != simulationAuthorityHoldBaseline {
		t.Fatalf("unexpected authority packet: %+v", authority)
	}
	want := holdBaselineContractPayload("许牧", 1)
	if !reflect.DeepEqual(authority[1].HoldBaselineContract, want) {
		t.Fatalf("authority packet contract drifted from validator canonical value:\n got=%#v\nwant=%#v", authority[1].HoldBaselineContract, want)
	}
	if !strings.Contains(authority[1].DecisionPolicy, "authority_contract_characters") || !strings.Contains(authority[1].DecisionPolicy, "服务端") || !strings.Contains(authority[1].DecisionPolicy, "hold_baseline_contract") {
		t.Fatalf("authority policy does not route the model through server-side contract materialization: %s", authority[1].DecisionPolicy)
	}
}

func holdBaselineDecisionForTest(name string, chapter int) domain.CharacterWorldDecision {
	return domain.CharacterWorldDecision{
		Character: name, Time: simulationAuthorityUnknown, Location: simulationAuthorityUnknown,
		CurrentGoal: simulationAuthorityHoldBaseline, Pressure: simulationAuthorityMissing,
		KnowledgeBoundary: simulationAuthorityMissing,
		AvailableOptions:  []string{simulationAuthorityHoldBaseline, simulationAuthorityWait},
		Decision:          simulationAuthorityHoldBaseline,
		DecisionReason:    simulationAuthorityMissing,
		Action:            simulationAuthorityHoldBaseline,
		ActionDuration:    simulationAuthorityNotApplicable,
		CompletionState:   "blocked",
		ImmediateResult:   simulationAuthorityNoEffect,
		StateAfter:        simulationAuthorityUnchanged,
		ButterflyEffects: []domain.DecisionButterflyEffect{{
			Effect: simulationAuthorityBlockedEffect, TransmissionPath: simulationAuthorityMissing,
			ArrivalChapter: chapter, Visibility: "hidden", ProtagonistImpact: simulationAuthorityNoImpact,
		}},
	}
}
