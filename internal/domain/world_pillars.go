package domain

// VisionPillars 视觉核心："这个世界看起来是什么样"（颜色/标志元素/光线/标志性场景）。
// 与 WorldPillars（如何运作）分层建模（育碧方法论）。
type VisionPillars struct {
	ColorPalette      []string `json:"color_palette,omitempty"`      // ["深红", "铁灰", "象牙白"]
	SignatureElements []string `json:"signature_elements,omitempty"` // ["青铜兽首", "竹简", "古琴"]
	Lighting          string   `json:"lighting,omitempty"`           // "昏暗室内+户外明亮对比"
	SignatureScenes   []string `json:"signature_scenes,omitempty"`   // ["朝堂辩论", "雨夜追逃"]
}

// PillarDetail 一根世界支柱的运作机制。
type PillarDetail struct {
	Base         string `json:"base"`                    // "盐铁专营"
	ControlledBy string `json:"controlled_by,omitempty"` // "朝廷"
	Tension      string `json:"tension,omitempty"`       // "中央 vs 地方"
}

// WorldPillars 世界核心："这个世界如何运作"（经济/文化/政治/历史四根支柱）。
// 指针 + omitempty：缺失的支柱不序列化空对象。
type WorldPillars struct {
	Economic   *PillarDetail `json:"economic,omitempty"`
	Cultural   *PillarDetail `json:"cultural,omitempty"`
	Political  *PillarDetail `json:"political,omitempty"`
	Historical *PillarDetail `json:"historical,omitempty"`
}
