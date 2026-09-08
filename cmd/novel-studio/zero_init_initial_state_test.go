package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func explicitOpeningStateFixture() zeroInitProject {
	return zeroInitProject{
		FirstChapter:  domain.OutlineEntry{Chapter: 1, Title: "未来标题", CoreEvent: "FUTURE_OUTLINE_SECRET：林澄已经知道所有角色的秘密并选择拆封。", Scenes: []string{"机修棚"}},
		FirstCast:     map[string]bool{"林澄": true, "周砚": true, "许岚": true},
		FirstMentions: map[string]int{"林澄": 1, "周砚": 1, "许岚": 1},
		WorldCodex:    &domain.WorldCodex{CharacterViewVersion: 1},
		BookWorld:     &domain.BookWorld{Places: []domain.WorldPlace{{ID: "office", Name: "值班室"}, {ID: "shed", Name: "机修棚"}, {ID: "store", Name: "仓库"}}},
		Characters: []domain.Character{
			{Name: "林澄", Role: "主角", Tier: "core", Arc: "FUTURE_ARC_SECRET", InitialState: &domain.CharacterInitialState{
				Time: "T+0", Location: "值班室", CurrentGoal: "保护原始记录", Pressure: "末班移交即将截止", KnownFacts: []string{"林澄知道自己的父亲受伤"}, Resources: []string{"柜钥匙一把"}, Commitments: []string{"如实交接自己保管的文件"},
			}},
			{Name: "周砚", Role: "机修员", Tier: "important", Arc: "FUTURE_ARC_SECRET", InitialState: &domain.CharacterInitialState{
				Time: "T+0", Location: "机修棚", CurrentGoal: "完成安全检查", CurrentAction: "核对工具", Pressure: "检查需要实际工时", KnownFacts: []string{"周砚亲自测量的原领数量是六十升"}, Resources: []string{"自己的接收原件"},
			}},
			{Name: "许岚", Role: "仓库经营者", Tier: "important", Arc: "FUTURE_ARC_SECRET", InitialState: &domain.CharacterInitialState{
				Time: "T+0", Location: "仓库", CurrentGoal: "保住经营", Pressure: "积水威胁存货", KnownFacts: []string{"许岚知道自己改过账"}, Resources: []string{"仓库钥匙一把"},
			}},
		},
	}
}

func TestZeroInitUsesPrivateOpeningStateInsteadOfFutureOutline(t *testing.T) {
	project := explicitOpeningStateFixture()
	dynamics := zeroInitDynamics(project)
	dossiers := zeroInitCharacterDossiers(project)
	for _, c := range project.Characters {
		var dossier *domain.CharacterDossier
		for i := range dossiers {
			if dossiers[i].Character == c.Name {
				dossier = &dossiers[i]
			}
		}
		if dossier == nil || dossier.CurrentAtStoryStart.Location != c.InitialState.Location || dossier.CurrentAtStoryStart.Pressure != c.InitialState.Pressure || dossier.CurrentAtStoryStart.NextIndependentMove != c.InitialState.CurrentGoal {
			t.Fatalf("wrong opening dossier for %s: %+v", c.Name, dossier)
		}
		if len(dossier.PreStoryTimeline) != 0 || len(dossier.KnownFactsAtStoryStart) != len(c.InitialState.KnownFacts) {
			t.Fatalf("opening knowledge was lost or misrepresented as past events: %+v", dossier)
		}
		for _, state := range dynamics.Characters {
			if state.Character != c.Name {
				continue
			}
			if state.CurrentGoal != c.InitialState.CurrentGoal || state.Pressure != c.InitialState.Pressure || state.KnowledgeLedger.Confidence != "authored_initial_state" {
				t.Fatalf("wrong initial dynamics for %s: %+v", c.Name, state)
			}
			if len(state.KnowledgeLedger.FalseBeliefs) != 0 || len(state.KnowledgeLedger.Suspicions) != 0 ||
				len(state.Misbeliefs) != 0 || state.DecisionFrame.CostPaid != "" || state.DecisionFrame.RiskAccepted != "" ||
				state.ArcAxis.CoreLie != "" || state.ArcAxis.GrowthSignal != "" {
				t.Fatalf("opening state invents a belief, completed decision or future growth: %+v", state)
			}
			b, err := json.Marshal(struct {
				Dossier any
				State   any
			}{dossier, state})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(b), "FUTURE_") {
				t.Fatalf("future plot leaked into %s's opening state", c.Name)
			}
			for _, other := range project.Characters {
				if other.Name != c.Name && strings.Contains(string(b), other.InitialState.KnownFacts[0]) {
					t.Fatalf("%s received %s's private opening knowledge", c.Name, other.Name)
				}
			}
			state.KnowledgeLedger.KnownFacts[0] = "changed derived memory"
			if c.InitialState.KnownFacts[0] == "changed derived memory" {
				t.Fatal("derived state aliases author source")
			}
		}
	}
	ledger := zeroInitResourceLedger(project)
	if len(ledger.Claims) != 3 {
		t.Fatalf("author resources missing: %+v", ledger)
	}
	for _, claim := range ledger.Claims {
		if claim.Owner == "" || claim.Status != "booked" || claim.Evidence != "characters.json:initial_state.resources" || strings.Contains(claim.Name, "第一章现场证据") {
			t.Fatalf("invented opening resource instead of authored possession: %+v", claim)
		}
	}
}

func TestZeroInitKnowledgeSnapshotDoesNotAssignHistoricalEventTime(t *testing.T) {
	project := explicitOpeningStateFixture()
	c := project.Characters[2]
	c.InitialState.KnownFacts = []string{"T-600 分钟亲自发出六十升燃油", "T+0 看到仓库地面仍有积水"}
	var d domain.CharacterDossier
	zeroApplyExplicitInitialDossier(&d, c)
	if len(d.PreStoryTimeline) != 0 || len(d.KnownFactsAtStoryStart) != 2 ||
		d.KnownFactsAtStoryStart[0] != c.InitialState.KnownFacts[0] ||
		d.KnownFactsAtStoryStart[1] != c.InitialState.KnownFacts[1] || d.CurrentAtStoryStart.Time != "T+0" {
		t.Fatalf("event times were inferred from observation time: %+v", d)
	}
	d.KnownFactsAtStoryStart[0] = "changed projection"
	if c.InitialState.KnownFacts[0] == "changed projection" {
		t.Fatal("dossier knowledge aliases source")
	}
}

// Exercise the actual writer -> readiness seam, not just private projection.
// The old explicit-state branch passed privacy tests but could never pass the
// production zero-init gate because five required dynamic sections were empty.
func TestZeroInitExplicitOpeningStatesPassProductionReadiness(t *testing.T) {
	dir := seedZeroInitProject(t)
	st := store.NewStore(dir)
	characters, err := st.Characters.Load()
	if err != nil {
		t.Fatal(err)
	}
	for n := range characters {
		characters[n].InitialState = &domain.CharacterInitialState{
			Time: "T+0", Location: "老小区楼道", CurrentGoal: "核实自己眼前的账单",
			Pressure: "自己保管的凭据需要核验", KnownFacts: []string{characters[n].Name + "持有自己的凭据"},
		}
	}
	if err := st.Characters.Save(characters); err != nil {
		t.Fatal(err)
	}
	if err := writeArchitectReadiness(dir, assessArchitectReadiness(dir)); err != nil {
		t.Fatal(err)
	}
	if err := zeroInitPipeline(cliOptions{}, []string{"--dir", dir, "--rebuild-rag=false"}); err != nil {
		t.Fatalf("explicit initial state cannot run the production zero-init pipeline: %v", err)
	}
	readiness := assessZeroInitReadiness(dir, zeroInitRAGStats{})
	if !readiness.Ready {
		t.Fatalf("explicit opening state failed full readiness: %+v", readiness)
	}
	data, err := os.ReadFile(filepath.Join(dir, "drafts", "01.zero_init.plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	var plan domain.ChapterPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		t.Fatal(err)
	}
	for _, state := range plan.CausalSimulation.InitialState {
		if state.KnowledgeLedger.Confidence != "authored_initial_state" || state.RelationshipContract == nil {
			t.Fatalf("explicit source or empty relationship contract lost on disk: %+v", state)
		}
	}
	for _, tc := range []struct {
		name  string
		clear func(*domain.CharacterSimulationState)
	}{
		{"knowledge_ledger", func(s *domain.CharacterSimulationState) { s.KnowledgeLedger.UnknownFacts = nil }},
		{"decision_frame", func(s *domain.CharacterSimulationState) { s.DecisionFrame.AvailableOptions = nil }},
		{"emotion_appraisal", func(s *domain.CharacterSimulationState) { s.EmotionAppraisal.ActionPressure = "" }},
		{"arc_axis", func(s *domain.CharacterSimulationState) { s.ArcAxis.PressureTest = "" }},
		{"competence/mistake/correction", func(s *domain.CharacterSimulationState) { s.CorrectionTriggers = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var broken domain.ChapterPlan
			if err := json.Unmarshal(data, &broken); err != nil {
				t.Fatal(err)
			}
			tc.clear(&broken.CausalSimulation.InitialState[0])
			if issues := zeroValidateChapterPlan(broken); !strings.Contains(strings.Join(issues, ";"), tc.name) {
				t.Fatalf("required quality section %s silently accepted: %v", tc.name, issues)
			}
		})
	}
}

func TestZeroInitLegacyFallbackDoesNotBroadcastPlannedScene(t *testing.T) {
	project := explicitOpeningStateFixture()
	project.WorldCodex.CharacterViewVersion = 0
	for i := range project.Characters {
		project.Characters[i].InitialState = nil
	}
	for _, dossier := range zeroInitCharacterDossiers(project) {
		if !strings.Contains(dossier.CurrentAtStoryStart.Location, "离屏/未定") || strings.Contains(dossier.CurrentAtStoryStart.Pressure, "FUTURE_OUTLINE_SECRET") {
			t.Fatalf("legacy fallback invented co-location or perceived plot: %+v", dossier.CurrentAtStoryStart)
		}
	}
	for _, state := range zeroInitDynamics(project).Characters {
		b, _ := json.Marshal(state)
		if strings.Contains(string(b), "FUTURE_OUTLINE_SECRET") {
			t.Fatal("legacy opening state broadcasts the future outline")
		}
	}
}

func TestZeroInitRejectsMissingExplicitStateBeforeWritingDerivedArtifacts(t *testing.T) {
	project := explicitOpeningStateFixture()
	project.Characters[1].InitialState = nil
	dir := t.TempDir()
	if err := writeZeroInitArtifacts(dir, &project, true); err == nil || !strings.Contains(err.Error(), "周砚") {
		t.Fatalf("expected missing private initial state error, got %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("preflight failure wrote derived artifacts: %v, %v", entries, err)
	}
}

func TestExplicitRebaseCanLoadLegacyBaselineForAuthorRepair(t *testing.T) {
	dir := seedZeroInitProject(t)
	st := store.NewStore(dir)
	codex, err := st.LoadWorldCodex()
	if err != nil || codex == nil {
		t.Fatalf("load: %v", err)
	}
	codex.CharacterViewVersion = 1
	if err := st.SaveWorldCodex(*codex); err != nil {
		t.Fatal(err)
	}
	project, err := loadZeroInitProjectForExplicitRebaseCandidate(st, dir)
	if err != nil {
		t.Fatalf("explicit rebase cannot reach subsequent author repair: %v", err)
	}
	derived := filepath.Join(t.TempDir(), "derived")
	if err := writeZeroInitArtifacts(derived, &project, true); err == nil {
		t.Fatal("rebase load exception bypassed actual zero-init preflight")
	}
}
