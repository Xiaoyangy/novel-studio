package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestValidateHoldBaselineDecisionReportsEachMismatchedField(t *testing.T) {
	tests := []struct {
		name      string
		fieldPath string
		mutate    func(*domain.CharacterWorldDecision)
	}{
		{name: "time", fieldPath: "time", mutate: func(d *domain.CharacterWorldDecision) { d.Time = "今晚" }},
		{name: "location", fieldPath: "location", mutate: func(d *domain.CharacterWorldDecision) { d.Location = "外地办公室" }},
		{name: "current goal", fieldPath: "current_goal", mutate: func(d *domain.CharacterWorldDecision) { d.CurrentGoal = "继续上班" }},
		{name: "pressure", fieldPath: "pressure", mutate: func(d *domain.CharacterWorldDecision) { d.Pressure = "加班" }},
		{name: "resources", fieldPath: "resources", mutate: func(d *domain.CharacterWorldDecision) { d.Resources = []string{"一辆车"} }},
		{name: "knowledge boundary", fieldPath: "knowledge_boundary", mutate: func(d *domain.CharacterWorldDecision) { d.KnowledgeBoundary = "知道主角已经返乡" }},
		{name: "available options", fieldPath: "available_options", mutate: func(d *domain.CharacterWorldDecision) {
			d.AvailableOptions = []string{simulationAuthorityWait, simulationAuthorityHoldBaseline}
		}},
		{name: "decision", fieldPath: "decision", mutate: func(d *domain.CharacterWorldDecision) { d.Decision = "继续工作" }},
		{name: "decision reason", fieldPath: "decision_reason", mutate: func(d *domain.CharacterWorldDecision) { d.DecisionReason = "尚未收到消息" }},
		{name: "action", fieldPath: "action", mutate: func(d *domain.CharacterWorldDecision) { d.Action = "留在办公室" }},
		{name: "action duration", fieldPath: "action_duration", mutate: func(d *domain.CharacterWorldDecision) { d.ActionDuration = "一晚" }},
		{name: "completion state", fieldPath: "completion_state", mutate: func(d *domain.CharacterWorldDecision) { d.CompletionState = "completed" }},
		{name: "immediate result", fieldPath: "immediate_result", mutate: func(d *domain.CharacterWorldDecision) { d.ImmediateResult = "完成工作" }},
		{name: "state after", fieldPath: "state_after", mutate: func(d *domain.CharacterWorldDecision) { d.StateAfter = "准备返乡" }},
		{name: "visible to pov", fieldPath: "visible_to_pov", mutate: func(d *domain.CharacterWorldDecision) { d.VisibleToPOV = true }},
		{name: "effect count", fieldPath: "butterfly_effects", mutate: func(d *domain.CharacterWorldDecision) { d.ButterflyEffects = nil }},
		{name: "effect", fieldPath: "butterfly_effects[0].effect", mutate: func(d *domain.CharacterWorldDecision) { d.ButterflyEffects[0].Effect = "消息延迟" }},
		{name: "effect targets", fieldPath: "butterfly_effects[0].targets", mutate: func(d *domain.CharacterWorldDecision) { d.ButterflyEffects[0].Targets = []string{"林澈"} }},
		{name: "transmission path", fieldPath: "butterfly_effects[0].transmission_path", mutate: func(d *domain.CharacterWorldDecision) { d.ButterflyEffects[0].TransmissionPath = "电话" }},
		{name: "arrival chapter", fieldPath: "butterfly_effects[0].arrival_chapter", mutate: func(d *domain.CharacterWorldDecision) { d.ButterflyEffects[0].ArrivalChapter = 2 }},
		{name: "visibility", fieldPath: "butterfly_effects[0].visibility", mutate: func(d *domain.CharacterWorldDecision) { d.ButterflyEffects[0].Visibility = "delayed" }},
		{name: "protagonist impact", fieldPath: "butterfly_effects[0].protagonist_impact", mutate: func(d *domain.CharacterWorldDecision) { d.ButterflyEffects[0].ProtagonistImpact = "改变运力选项" }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			decision := cloneAuthorityDecisionForTest(holdBaselineDecisionForTest("许牧", 1))
			tc.mutate(&decision)
			err := validateHoldBaselineDecision(1, decision)
			if err == nil {
				t.Fatalf("mutated %s unexpectedly passed", tc.fieldPath)
			}
			if !strings.Contains(err.Error(), tc.fieldPath) {
				t.Fatalf("error must identify field path %q, got: %v", tc.fieldPath, err)
			}
		})
	}
}

func TestSimulationAuthorityRejectsResubmissionOfAlreadyPresentCharacter(t *testing.T) {
	st := newAuthorityGuardTestStore(t)
	existing := holdBaselineDecisionForTest("许牧", 1)
	if err := st.SaveChapterWorldSimulationPartial(domain.ChapterWorldSimulation{
		Version: 1, Chapter: 1, CharacterDecisions: []domain.CharacterWorldDecision{existing},
	}); err != nil {
		t.Fatal(err)
	}

	replacement := cloneAuthorityDecisionForTest(existing)
	replacement.Decision = "接受主角邀请"
	err := validateIncomingSimulationCharacterAuthority(st, 1, []domain.CharacterWorldDecision{replacement})
	if err == nil {
		t.Fatal("already-present character was silently overwritten")
	}
	for _, want := range []string{"许牧", "already_present"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("resubmission error must contain %q, got: %v", want, err)
		}
	}
}

func TestSimulationAuthorityRemainsFailClosedWhenAnotherDossierIsCorrupt(t *testing.T) {
	st := newAuthorityGuardTestStore(t)
	badDir := filepath.Join(st.Dir(), "meta", "characters", "损坏角色")
	if err := os.MkdirAll(badDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "dossier.json"), []byte("{invalid"), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, loadErr := st.LoadAllCharacterDossiers()
	if loadErr == nil || len(loaded) == 0 {
		t.Fatalf("fixture must produce partial dossiers plus an error: count=%d err=%v", len(loaded), loadErr)
	}

	guessed := holdBaselineDecisionForTest("许牧", 1)
	guessed.Location = "上海办公室"
	err := validateIncomingSimulationCharacterAuthority(st, 1, []domain.CharacterWorldDecision{guessed})
	if err == nil {
		t.Fatal("one corrupt dossier disabled the authority guard for every loaded character")
	}
	if !strings.Contains(err.Error(), "location") {
		t.Fatalf("fail-closed error must retain the concrete mismatch path, got: %v", err)
	}
}

func newAuthorityGuardTestStore(t *testing.T) *store.Store {
	t.Helper()
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
		CurrentAtStoryStart: domain.CharacterStartState{Location: "林家饭桌", Status: "清醒", CurrentAction: "应付亲戚追问"},
		Resources:           []domain.CharacterResource{{Name: "手机", Status: "available"}},
		KnowledgeBoundary:   "只知道亲历信息",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveCharacterDossier(domain.CharacterDossier{
		Version: 1, Character: "许牧", Role: "配角", Tier: "important",
		CurrentAtStoryStart: domain.CharacterStartState{
			Location:      "故事开始前未进入主角视角；正式出场前按角色卡和交通规则补足位置",
			CurrentAction: "按自身开局目标行动或被自身场景压力困住。",
		},
	}); err != nil {
		t.Fatal(err)
	}
	return st
}

func cloneAuthorityDecisionForTest(in domain.CharacterWorldDecision) domain.CharacterWorldDecision {
	out := in
	out.Resources = append([]string(nil), in.Resources...)
	out.AvailableOptions = append([]string(nil), in.AvailableOptions...)
	out.ButterflyEffects = append([]domain.DecisionButterflyEffect(nil), in.ButterflyEffects...)
	for i := range out.ButterflyEffects {
		out.ButterflyEffects[i].Targets = append([]string(nil), in.ButterflyEffects[i].Targets...)
	}
	return out
}
