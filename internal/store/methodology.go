package store

import (
	"fmt"
	"os"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// MethodologyStore 管理"小说塑造方法论"批次引入的可选 meta 工件：
// 道德天花板、物理公理、节奏契约、社会情绪、信息差图、仪式日历、NPC 生态、
// 生态图、宇宙观、文化脚注、场景物件清单、场景动力序列。
// 全部工件可选：Load 在文件缺失时返回 (nil, nil)，消费方据此跳过。
type MethodologyStore struct{ io *IO }

// NewMethodologyStore 创建方法论工件存储。
func NewMethodologyStore(io *IO) *MethodologyStore { return &MethodologyStore{io: io} }

// loadOptional 读取可选 JSON 工件；文件不存在返回 (false, nil)。
func (s *MethodologyStore) loadOptional(rel string, v any) (bool, error) {
	if err := s.io.ReadJSON(rel, v); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// --- 道德天花板 ---

func (s *MethodologyStore) SaveMoralCeiling(m domain.MoralCeiling) error {
	return s.io.WriteJSON("meta/moral_ceiling.json", m)
}

func (s *MethodologyStore) LoadMoralCeiling() (*domain.MoralCeiling, error) {
	var m domain.MoralCeiling
	ok, err := s.loadOptional("meta/moral_ceiling.json", &m)
	if err != nil || !ok {
		return nil, err
	}
	return &m, nil
}

// --- 物理公理 ---

func (s *MethodologyStore) SavePhysicsAxioms(p domain.PhysicsAxioms) error {
	return s.io.WriteJSON("meta/physics_axioms.json", p)
}

func (s *MethodologyStore) LoadPhysicsAxioms() (*domain.PhysicsAxioms, error) {
	var p domain.PhysicsAxioms
	ok, err := s.loadOptional("meta/physics_axioms.json", &p)
	if err != nil || !ok {
		return nil, err
	}
	return &p, nil
}

// --- 节奏契约 ---

func (s *MethodologyStore) SavePacingContract(p domain.PacingContract) error {
	if err := p.Validate(); err != nil {
		return err
	}
	return s.io.WriteJSON("meta/pacing_contract.json", p)
}

func (s *MethodologyStore) LoadPacingContract() (*domain.PacingContract, error) {
	var p domain.PacingContract
	ok, err := s.loadOptional("meta/pacing_contract.json", &p)
	if err != nil || !ok {
		return nil, err
	}
	return &p, nil
}

// --- 社会情绪 ---

func (s *MethodologyStore) SaveSocialMood(m domain.SocialMood) error {
	if err := m.Validate(); err != nil {
		return err
	}
	return s.io.WriteJSON("meta/social_mood.json", m)
}

func (s *MethodologyStore) LoadSocialMood() (*domain.SocialMood, error) {
	var m domain.SocialMood
	ok, err := s.loadOptional("meta/social_mood.json", &m)
	if err != nil || !ok {
		return nil, err
	}
	return &m, nil
}

// --- 信息差图 ---

func (s *MethodologyStore) SaveInfoGraph(g domain.InfoGraph) error {
	if err := g.Validate(); err != nil {
		return err
	}
	return s.io.WriteJSON("meta/info_graph.json", g)
}

func (s *MethodologyStore) LoadInfoGraph() (*domain.InfoGraph, error) {
	var g domain.InfoGraph
	ok, err := s.loadOptional("meta/info_graph.json", &g)
	if err != nil || !ok {
		return nil, err
	}
	return &g, nil
}

// --- 仪式日历 ---

func (s *MethodologyStore) SaveRitualCalendar(c domain.RitualCalendar) error {
	if err := c.Validate(); err != nil {
		return err
	}
	return s.io.WriteJSON("meta/ritual_calendar.json", c)
}

func (s *MethodologyStore) LoadRitualCalendar() (*domain.RitualCalendar, error) {
	var c domain.RitualCalendar
	ok, err := s.loadOptional("meta/ritual_calendar.json", &c)
	if err != nil || !ok {
		return nil, err
	}
	return &c, nil
}

// --- NPC 生态 ---

func (s *MethodologyStore) SaveCrowdLife(c domain.CrowdLifeEcosystem) error {
	if err := c.Validate(); err != nil {
		return err
	}
	return s.io.WriteJSON("meta/crowd_life.json", c)
}

func (s *MethodologyStore) LoadCrowdLife() (*domain.CrowdLifeEcosystem, error) {
	var c domain.CrowdLifeEcosystem
	ok, err := s.loadOptional("meta/crowd_life.json", &c)
	if err != nil || !ok {
		return nil, err
	}
	return &c, nil
}

// --- 生态图 ---

func (s *MethodologyStore) SaveEcologicalMap(m domain.EcologicalMap) error {
	if err := m.Validate(); err != nil {
		return err
	}
	return s.io.WriteJSON("meta/ecological_map.json", m)
}

func (s *MethodologyStore) LoadEcologicalMap() (*domain.EcologicalMap, error) {
	var m domain.EcologicalMap
	ok, err := s.loadOptional("meta/ecological_map.json", &m)
	if err != nil || !ok {
		return nil, err
	}
	return &m, nil
}

// --- 宇宙观 ---

func (s *MethodologyStore) SaveCosmology(c domain.Cosmology) error {
	if err := c.Validate(); err != nil {
		return err
	}
	return s.io.WriteJSON("meta/cosmology.json", c)
}

func (s *MethodologyStore) LoadCosmology() (*domain.Cosmology, error) {
	var c domain.Cosmology
	ok, err := s.loadOptional("meta/cosmology.json", &c)
	if err != nil || !ok {
		return nil, err
	}
	return &c, nil
}

// --- 文化脚注 ---

func (s *MethodologyStore) SaveCulturalFootnotes(c domain.CulturalFootnotes) error {
	if err := c.Validate(); err != nil {
		return err
	}
	return s.io.WriteJSON("meta/cultural_footnotes.json", c)
}

func (s *MethodologyStore) LoadCulturalFootnotes() (*domain.CulturalFootnotes, error) {
	var c domain.CulturalFootnotes
	ok, err := s.loadOptional("meta/cultural_footnotes.json", &c)
	if err != nil || !ok {
		return nil, err
	}
	return &c, nil
}

// --- 场景物件清单（按章） ---

func (s *MethodologyStore) SaveSceneInventory(inv domain.SceneInventory) error {
	if err := inv.Validate(); err != nil {
		return err
	}
	return s.io.WriteJSON(fmt.Sprintf("meta/scene_inventory/%02d.json", inv.Chapter), inv)
}

func (s *MethodologyStore) LoadSceneInventory(chapter int) (*domain.SceneInventory, error) {
	var inv domain.SceneInventory
	ok, err := s.loadOptional(fmt.Sprintf("meta/scene_inventory/%02d.json", chapter), &inv)
	if err != nil || !ok {
		return nil, err
	}
	return &inv, nil
}

// --- 置信度报告（按章，纯观测信号） ---

func (s *MethodologyStore) SaveConfidence(chapter int, c domain.ConfidenceReport) error {
	return s.io.WriteJSON(fmt.Sprintf("reviews/%02d_confidence.json", chapter), c)
}

func (s *MethodologyStore) LoadConfidence(chapter int) (*domain.ConfidenceReport, error) {
	var c domain.ConfidenceReport
	ok, err := s.loadOptional(fmt.Sprintf("reviews/%02d_confidence.json", chapter), &c)
	if err != nil || !ok {
		return nil, err
	}
	return &c, nil
}

// --- 场景动力（按章） ---

func (s *MethodologyStore) SaveSceneDynamics(d domain.SceneDynamics) error {
	if err := d.Validate(); err != nil {
		return err
	}
	return s.io.WriteJSON(fmt.Sprintf("meta/scene_dynamics/%02d.json", d.Chapter), d)
}

func (s *MethodologyStore) LoadSceneDynamics(chapter int) (*domain.SceneDynamics, error) {
	var d domain.SceneDynamics
	ok, err := s.loadOptional(fmt.Sprintf("meta/scene_dynamics/%02d.json", chapter), &d)
	if err != nil || !ok {
		return nil, err
	}
	return &d, nil
}

// LoadRecentSceneDynamics 返回 before 章之前（不含 before）最近 n 章的动力记录，
// 按章号升序；缺章跳过。
func (s *MethodologyStore) LoadRecentSceneDynamics(before, n int) []domain.SceneDynamics {
	var out []domain.SceneDynamics
	for ch := before - n; ch < before; ch++ {
		if ch <= 0 {
			continue
		}
		if d, err := s.LoadSceneDynamics(ch); err == nil && d != nil {
			out = append(out, *d)
		}
	}
	return out
}

// --- 书级 AI 味统计（Task 082 接线；数据由 stylestat.BookReport 产出） ---

func (s *MethodologyStore) SaveBookStylestat(v any) error {
	return s.io.WriteJSON("meta/book_stylestat.json", v)
}

func (s *MethodologyStore) LoadBookStylestatRaw() (map[string]any, error) {
	var m map[string]any
	ok, err := s.loadOptional("meta/book_stylestat.json", &m)
	if err != nil || !ok {
		return nil, err
	}
	return m, nil
}
