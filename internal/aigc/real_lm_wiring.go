package aigc

import "sync"

// Task 072：RealLM 曲率维度接线。配置经 SetRealLMRuntimeConfig 注入（agents 装配期），
// ai_gate 报告在配置存在时显式出现该维度（权重保持 0——曲率采样的启用与权重调整
// 必须等校准报告，走 proposed 语义）；未配置时报告无痕。
type RealLMResult struct {
	Enabled bool    `json:"enabled"`
	Score   float64 `json:"score,omitempty"`
	Weight  float64 `json:"weight"`
	Note    string  `json:"note,omitempty"`
}

var (
	realLMMu  sync.RWMutex
	realLMCfg RealLMConfig
)

// SetRealLMRuntimeConfig 装配期注入 real_lm 配置（endpoint/model/weight）。
func SetRealLMRuntimeConfig(endpoint, model string, weight float64) {
	realLMMu.Lock()
	defer realLMMu.Unlock()
	realLMCfg = RealLMConfig{Endpoint: endpoint, Model: model, Weight: weight}
}

func realLMResult(_ string) *RealLMResult {
	realLMMu.RLock()
	cfg := realLMCfg
	realLMMu.RUnlock()
	if cfg.Endpoint == "" || cfg.Model == "" {
		return nil // 未配置：报告无痕
	}
	return &RealLMResult{
		Enabled: true,
		Weight:  cfg.Weight,
		Note:    "端点已配置；曲率采样与权重启用待 docs/aigc-calibration-report.md 校准结论（proposed）",
	}
}
