package store

import (
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestMethodologyStoreOptionalArtifacts(t *testing.T) {
	s := NewStore(t.TempDir())

	// 全部工件缺失时 Load 返回 (nil, nil)，消费方据此跳过。
	if m, err := s.Methodology.LoadMoralCeiling(); err != nil || m != nil {
		t.Fatalf("缺失道德天花板应返回 nil,nil: %v %v", m, err)
	}
	if p, err := s.Methodology.LoadPhysicsAxioms(); err != nil || p != nil {
		t.Fatalf("缺失物理公理应返回 nil,nil: %v %v", p, err)
	}
	if c, err := s.Methodology.LoadPacingContract(); err != nil || c != nil {
		t.Fatalf("缺失节奏契约应返回 nil,nil: %v %v", c, err)
	}

	// 往返
	ceiling := domain.MoralCeiling{KillsAllowedPerArc: 2, TabooZones: []string{"儿童", "折磨戏"}}
	if err := s.Methodology.SaveMoralCeiling(ceiling); err != nil {
		t.Fatalf("保存道德天花板失败: %v", err)
	}
	got, err := s.Methodology.LoadMoralCeiling()
	if err != nil || got == nil || got.KillsAllowedPerArc != 2 || len(got.TabooZones) != 2 {
		t.Fatalf("道德天花板往返失败: %+v %v", got, err)
	}

	axioms := domain.PhysicsAxioms{
		DistanceSpeed:   map[string]domain.SpeedRule{"驿马": {Unit: "里/天", Speed: 80}},
		InfoPropagation: map[string]float64{"飞鸽": 3},
		Era:             "古代",
	}
	if err := s.Methodology.SavePhysicsAxioms(axioms); err != nil {
		t.Fatalf("保存物理公理失败: %v", err)
	}
	pa, err := s.Methodology.LoadPhysicsAxioms()
	if err != nil || pa == nil || pa.DistanceSpeed["驿马"].Speed != 80 {
		t.Fatalf("物理公理往返失败: %+v %v", pa, err)
	}

	contract, _ := domain.PacingPreset("qidian_xuanhuan")
	if err := s.Methodology.SavePacingContract(contract); err != nil {
		t.Fatalf("保存节奏契约失败: %v", err)
	}
	pc, err := s.Methodology.LoadPacingContract()
	if err != nil || pc == nil || pc.Name != "qidian_xuanhuan" {
		t.Fatalf("节奏契约往返失败: %+v %v", pc, err)
	}

	// 非法契约保存应被 Validate 拒绝
	if err := s.Methodology.SavePacingContract(domain.PacingContract{}); err == nil {
		t.Fatal("空名契约应被拒绝")
	}
}

func TestMethodologyStoreSceneDynamics(t *testing.T) {
	s := NewStore(t.TempDir())
	for ch := 1; ch <= 4; ch++ {
		d := domain.SceneDynamics{Chapter: ch, ConflictEngine: "interest", PressureIndex: ch + 2, InfoReleaseRatio: 0.3, EntropyDelta: 0.1}
		if err := s.Methodology.SaveSceneDynamics(d); err != nil {
			t.Fatalf("保存第 %d 章动力失败: %v", ch, err)
		}
	}
	got, err := s.Methodology.LoadSceneDynamics(2)
	if err != nil || got == nil || got.PressureIndex != 4 {
		t.Fatalf("单章动力读取失败: %+v %v", got, err)
	}
	recent := s.Methodology.LoadRecentSceneDynamics(4, 5)
	if len(recent) != 3 {
		t.Fatalf("recent 应为 1-3 章共 3 条，实际 %d", len(recent))
	}
	if recent[0].Chapter != 1 || recent[2].Chapter != 3 {
		t.Fatalf("recent 应按章升序: %+v", recent)
	}
	// 非法动力保存被拒
	if err := s.Methodology.SaveSceneDynamics(domain.SceneDynamics{Chapter: 9, ConflictEngine: "chaos", PressureIndex: 5}); err == nil {
		t.Fatal("非法动力应被拒绝")
	}
}

func TestMethodologyStoreConfidence(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.Methodology.SaveConfidence(7, domain.ConfidenceReport{Overall: 0.55, Doubts: []string{"结尾钩子偏弱"}}); err != nil {
		t.Fatalf("保存置信度失败: %v", err)
	}
	got, err := s.Methodology.LoadConfidence(7)
	if err != nil || got == nil || got.Overall != 0.55 {
		t.Fatalf("置信度往返失败: %+v %v", got, err)
	}
}

func TestProgressSetChapterPOV(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.Progress.Init("测试书", 10); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := s.Progress.SetChapterPOV(2, "林昭"); err != nil {
		t.Fatalf("记录 POV 失败: %v", err)
	}
	if err := s.Progress.SetChapterPOV(3, "沈青"); err != nil {
		t.Fatalf("记录 POV 失败: %v", err)
	}
	p, err := s.Progress.Load()
	if err != nil || p == nil || p.POV == nil {
		t.Fatalf("progress 读取失败: %v", err)
	}
	if p.POV.CurrentPOV != "沈青" {
		t.Fatalf("CurrentPOV 期望 沈青，实际 %s", p.POV.CurrentPOV)
	}
	if len(p.POV.History) != 3 || p.POV.History[0] != "" || p.POV.History[1] != "林昭" || p.POV.History[2] != "沈青" {
		t.Fatalf("POV 历史补位错误: %v", p.POV.History)
	}
	// 空 POV 是 noop
	if err := s.Progress.SetChapterPOV(4, ""); err != nil {
		t.Fatalf("空 POV 应为 noop: %v", err)
	}
}
