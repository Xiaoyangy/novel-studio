package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func newLintTestTool(t *testing.T) *CommitChapterTool {
	t.Helper()
	s := store.NewStore(t.TempDir())
	if err := s.Progress.Init("测试书", 10); err != nil {
		t.Fatalf("init progress: %v", err)
	}
	return NewCommitChapterTool(s)
}

func countRule(violations []rules.Violation, rule string) int {
	n := 0
	for _, v := range violations {
		if v.Rule == rule {
			n++
		}
	}
	return n
}

func TestMethodologyViolationsAllSkippedWhenAbsent(t *testing.T) {
	tool := newLintTestTool(t)
	got := tool.methodologyViolations(1, 3000, "crisis", methodologyCommitExtras{})
	if len(got) != 0 {
		t.Fatalf("无自报且无契约时应零违规: %v", got)
	}
}

func TestMethodologyViolationsSceneDynamics(t *testing.T) {
	tool := newLintTestTool(t)
	// 非法自报 → 一条 invalid warning，不落盘
	got := tool.methodologyViolations(1, 3000, "", methodologyCommitExtras{
		SceneDynamics: &domain.SceneDynamics{ConflictEngine: "chaos", PressureIndex: 5},
	})
	if countRule(got, "scene_dynamics_invalid") != 1 {
		t.Fatalf("非法动力应产出 invalid 违规: %v", got)
	}
	// 合法自报 → 落盘 + 信息过载 warning
	got = tool.methodologyViolations(2, 3000, "", methodologyCommitExtras{
		SceneDynamics: &domain.SceneDynamics{ConflictEngine: "value", PressureIndex: 6, InfoReleaseRatio: 0.9},
	})
	if countRule(got, "scene_dynamics_trend") != 1 {
		t.Fatalf("信息过载应产出 trend 违规: %v", got)
	}
	if d, err := tool.store.Methodology.LoadSceneDynamics(2); err != nil || d == nil {
		t.Fatalf("动力应已落盘: %v", err)
	}
}

func TestMethodologyViolationsPacing(t *testing.T) {
	tool := newLintTestTool(t)
	contract, _ := domain.PacingPreset("qidian_xuanhuan")
	if err := tool.store.Methodology.SavePacingContract(contract); err != nil {
		t.Fatalf("保存契约: %v", err)
	}
	// 5000 字（超上限 3000）+ 无钩子 → 至少字数与钩子两条
	got := tool.methodologyViolations(1, 5000, "", methodologyCommitExtras{})
	if countRule(got, "pacing_contract") < 2 {
		t.Fatalf("超字数+缺钩子应至少两条 pacing 违规: %v", got)
	}
	// 合规章零违规
	got = tool.methodologyViolations(1, 2500, "crisis", methodologyCommitExtras{})
	if countRule(got, "pacing_contract") != 0 {
		t.Fatalf("合规章不应有 pacing 违规: %v", got)
	}
}

func TestMethodologyViolationsPOV(t *testing.T) {
	tool := newLintTestTool(t)
	// 写入契约：scope 限林昭/沈青
	if err := tool.store.Progress.SetChapterPOV(1, "林昭"); err != nil {
		t.Fatalf("set pov: %v", err)
	}
	p, _ := tool.store.Progress.Load()
	p.POV.Contract = &domain.POVContract{Scope: []string{"林昭", "沈青"}}
	if err := tool.store.Progress.Save(p); err != nil {
		t.Fatalf("save progress: %v", err)
	}

	got := tool.methodologyViolations(2, 3000, "", methodologyCommitExtras{POV: "路人甲"})
	if countRule(got, "pov_contract") != 1 {
		t.Fatalf("scope 外 POV 应告警: %v", got)
	}
	// POV 历史应已追加
	p, _ = tool.store.Progress.Load()
	if p.POV.CurrentPOV != "路人甲" || len(p.POV.History) != 2 {
		t.Fatalf("POV 历史未追加: %+v", p.POV)
	}
}

func TestMethodologyViolationsConfidence(t *testing.T) {
	tool := newLintTestTool(t)
	got := tool.methodologyViolations(3, 3000, "", methodologyCommitExtras{
		Confidence: &domain.ConfidenceReport{Overall: 0.5, Doubts: []string{"中段节奏可能拖"}},
	})
	if countRule(got, "low_confidence") != 1 {
		t.Fatalf("低置信应产出一条 warning: %v", got)
	}
	for _, v := range got {
		if v.Severity != rules.SeverityWarning {
			t.Fatalf("置信度违规必须是 warning（绝不阻塞）: %+v", v)
		}
	}
	if c, err := tool.store.Methodology.LoadConfidence(3); err != nil || c == nil {
		t.Fatalf("置信度报告应已落盘: %v", err)
	}
	// 高置信不告警
	got = tool.methodologyViolations(4, 3000, "", methodologyCommitExtras{
		Confidence: &domain.ConfidenceReport{Overall: 0.9},
	})
	if countRule(got, "low_confidence") != 0 {
		t.Fatalf("高置信不应告警: %v", got)
	}
}

func TestMethodologyViolationsPersonality(t *testing.T) {
	tool := newLintTestTool(t)
	chars := []domain.Character{
		{Name: "林昭", Role: "主角", Psych: &domain.CharacterPsychProfile{BigFive: &domain.BigFive{Neuroticism: 1.0}}},
		{Name: "无画像", Role: "配角"},
	}
	if err := tool.store.Characters.Save(chars); err != nil {
		t.Fatalf("save characters: %v", err)
	}
	// N=1 期望区间 [0.5,1]+容差；自报 0.1 明显偏离 → 告警
	got := tool.methodologyViolations(1, 3000, "", methodologyCommitExtras{
		ExpressionChecks: []domain.CharacterExpressionCheck{
			{Name: "林昭", EmotionIntensity: 0.1},
			{Name: "无画像", EmotionIntensity: 0.1}, // 缺画像跳过
			{Name: "不存在", EmotionIntensity: 0.1}, // 不在册跳过
		},
	})
	if countRule(got, "personality_consistency") != 1 {
		t.Fatalf("仅有画像且偏离的角色应告警一条: %v", got)
	}
	// 区间内不告警
	got = tool.methodologyViolations(2, 3000, "", methodologyCommitExtras{
		ExpressionChecks: []domain.CharacterExpressionCheck{{Name: "林昭", EmotionIntensity: 0.8}},
	})
	if countRule(got, "personality_consistency") != 0 {
		t.Fatalf("区间内表现不应告警: %v", got)
	}
}

func TestMarshalOrderedContextOrdering(t *testing.T) {
	result := map[string]any{
		"_warnings":       []string{"w"},
		"world_rules":     []string{"r"},
		"progress_status": map[string]any{"phase": "writing"},
		"previous_tail":   "……",
		"_reading_guide":  contextReadingGuide,
		"unknown_key":     1,
	}
	data, err := marshalOrderedContext(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	text := string(data)
	idx := func(key string) int { return strings.Index(text, "\""+key+"\"") }
	if !(idx("_reading_guide") < idx("progress_status")) {
		t.Fatalf("critical-first 顺序错误: %s", text)
	}
	if !(idx("progress_status") < idx("unknown_key") && idx("unknown_key") < idx("previous_tail")) {
		t.Fatalf("未知 key 应落中段: %s", text)
	}
	if !(idx("previous_tail") < idx("_warnings")) {
		t.Fatalf("critical-last 顺序错误: %s", text)
	}
	// 内容等价（顺序无损）
	var back map[string]any
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("回读失败: %v", err)
	}
	if len(back) != len(result) {
		t.Fatalf("键数不一致: %d vs %d", len(back), len(result))
	}
}

func TestUnmarshalToolArgsRepairs(t *testing.T) {
	type args struct {
		Chapter int    `json:"chapter"`
		Title   string `json:"title"`
	}
	var a args

	// 合法输入直通
	if err := unmarshalToolArgs(json.RawMessage(`{"chapter":3,"title":"雪夜"}`), &a); err != nil || a.Chapter != 3 {
		t.Fatalf("合法输入应直通: %v", err)
	}
	// markdown 围栏
	a = args{}
	fenced := "```json\n{\"chapter\":4,\"title\":\"围栏\"}\n```"
	if err := unmarshalToolArgs(json.RawMessage(fenced), &a); err != nil || a.Chapter != 4 {
		t.Fatalf("围栏应被剥离: %v", err)
	}
	// 尾逗号
	a = args{}
	if err := unmarshalToolArgs(json.RawMessage(`{"chapter":5,"title":"尾逗号",}`), &a); err != nil || a.Chapter != 5 {
		t.Fatalf("尾逗号应被修复: %v", err)
	}
	// 双重编码
	a = args{}
	double, _ := json.Marshal(`{"chapter":6,"title":"字符串化"}`)
	if err := unmarshalToolArgs(double, &a); err != nil || a.Chapter != 6 {
		t.Fatalf("双重编码应被解包: %v", err)
	}
	// 彻底畸形返回错误
	if err := unmarshalToolArgs(json.RawMessage(`完全不是 JSON`), &a); err == nil {
		t.Fatal("畸形输入应返回错误")
	}
}
