package domain

import "fmt"

// Ecosystem 一个生态区：主角跨越 N 个地区时生态必须不同
// （海岛/丛林/草原/丘陵/山脉/雪原/沙漠 7 大主生态 + 嵌套子生态）。
type Ecosystem struct {
	ID               string      `json:"id"` // "qingzhou_plains"
	Name             string      `json:"name"`
	Elevation        int         `json:"elevation,omitempty"` // 米
	Climate          string      `json:"climate,omitempty"`   // "temperate_humid"
	DominantSpecies  []string    `json:"dominant_species,omitempty"`
	PredatorSpecies  []string    `json:"predator_species,omitempty"`
	PrimaryPressure  string      `json:"primary_pressure,omitempty"` // "周期性水患"
	HumanInhabitants []string    `json:"human_inhabitants,omitempty"`
	SubEcosystems    []Ecosystem `json:"sub_ecosystems,omitempty"`
}

// EcologicalMap 全书生态图。落盘 meta/ecological_map.json（可选工件）。
type EcologicalMap struct {
	Ecosystems []Ecosystem `json:"ecosystems"`
}

// Validate 递归校验 id/name 非空。
func (m EcologicalMap) Validate() error {
	var check func(list []Ecosystem, path string) error
	check = func(list []Ecosystem, path string) error {
		for i, e := range list {
			if e.ID == "" || e.Name == "" {
				return fmt.Errorf("ecological_map%s[%d] 缺少 id/name", path, i)
			}
			if err := check(e.SubEcosystems, fmt.Sprintf("%s[%d].sub", path, i)); err != nil {
				return err
			}
		}
		return nil
	}
	return check(m.Ecosystems, "")
}
