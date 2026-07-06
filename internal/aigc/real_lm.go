package aigc

// Task 061：真实 LM 困惑度维度（可选启用）。SOTA zero-shot 检测是 Fast-DetectGPT
// （条件概率曲率）与 Binoculars（双模型困惑度比）；本地 aigc v3 的 curvature/perplexity
// 是字符统计代理。本文件只定义接口与融合位：config 给出本地 LM 端点（ollama/llama.cpp）
// 时按曲率思路取样打分；给两个端点时按 Binoculars 算困惑度比。
// 未配置时 Weight=0 完全跳过（老项目零影响）；不引网络依赖、不下载模型；
// 融合权重默认 0，待 Task 060 校准报告（proposed 语义）后由用户调整。

// RealLMConfig 真实 LM 打分配置（后续挂到 bootstrap.Config，当前由调用方显式传入）。
type RealLMConfig struct {
	Endpoint          string  `json:"endpoint,omitempty"`           // 主 LM（如 http://127.0.0.1:11434）
	Model             string  `json:"model,omitempty"`              // 中文小模型名
	SecondaryEndpoint string  `json:"secondary_endpoint,omitempty"` // 提供时启用 Binoculars 比值
	SecondaryModel    string  `json:"secondary_model,omitempty"`
	Weight            float64 `json:"weight,omitempty"` // 融合权重，默认 0（跳过）
}

// LogprobFn 由调用方注入的 logprob 采样函数（避免本包引 HTTP 依赖）：
// 返回文本在该 LM 下的平均 token logprob；err 时该维度整体跳过。
type LogprobFn func(endpoint, model, text string) (float64, error)

// RealLMScore 计算 real_lm_curvature 维度分 [0,100]（越高越像 AI）。
// cfg 未配置或 weight<=0 → (0,false)。单端点：条件概率曲率代理（原文 logprob 与
// 扰动样本均值之差归一）；双端点：Binoculars 困惑度比。
func RealLMScore(cfg RealLMConfig, text string, perturbations []string, logprob LogprobFn) (float64, bool) {
	if cfg.Endpoint == "" || cfg.Model == "" || cfg.Weight <= 0 || logprob == nil {
		return 0, false
	}
	orig, err := logprob(cfg.Endpoint, cfg.Model, text)
	if err != nil {
		return 0, false
	}
	if cfg.SecondaryEndpoint != "" && cfg.SecondaryModel != "" {
		sec, err := logprob(cfg.SecondaryEndpoint, cfg.SecondaryModel, text)
		if err != nil || sec == 0 {
			return 0, false
		}
		ratio := orig / sec // Binoculars: log-ppl 比；AI 文本比值更接近 1
		return clampScore((1.5 - absF(ratio-1)*5) * 66), true
	}
	if len(perturbations) == 0 {
		return 0, false
	}
	var sum float64
	var n int
	for _, p := range perturbations {
		if lp, err := logprob(cfg.Endpoint, cfg.Model, p); err == nil {
			sum += lp
			n++
		}
	}
	if n == 0 {
		return 0, false
	}
	curvature := orig - sum/float64(n) // Fast-DetectGPT：AI 文本曲率显著为正
	return clampScore(curvature * 50), true
}

func clampScore(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func absF(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
