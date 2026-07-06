package domain

import "fmt"

// sceneConflictEngines 冲突引擎枚举。
var sceneConflictEngines = map[string]bool{
	"value":    true, // 价值观冲突
	"interest": true, // 利益冲突
	"emotion":  true, // 情感冲突
	"survival": true, // 生存冲突
}

// SceneDynamics 每章 4 个量化叙事参数，Writer 在 commit 时可选自报。
// 落盘 meta/scene_dynamics/{NN}.json，供跨章确定性趋势 lint：
// 连续低压（节奏塌）、信息过载、熵单调上升（缺喘息）、引擎失衡（张力单一）。
type SceneDynamics struct {
	Chapter          int     `json:"chapter"`
	ConflictEngine   string  `json:"conflict_engine"`    // value|interest|emotion|survival
	PressureIndex    int     `json:"pressure_index"`     // 1-10
	InfoReleaseRatio float64 `json:"info_release_ratio"` // [0,1] 新信息占比
	EntropyDelta     float64 `json:"entropy_delta"`      // [-1,1] 混乱度增减
}

// Validate 校验枚举与取值范围。
func (s SceneDynamics) Validate() error {
	if !sceneConflictEngines[s.ConflictEngine] {
		return fmt.Errorf("scene_dynamics.conflict_engine 非法: %q（应为 value/interest/emotion/survival）", s.ConflictEngine)
	}
	if s.PressureIndex < 1 || s.PressureIndex > 10 {
		return fmt.Errorf("scene_dynamics.pressure_index 必须在 [1,10]，实际 %d", s.PressureIndex)
	}
	if s.InfoReleaseRatio < 0 || s.InfoReleaseRatio > 1 {
		return fmt.Errorf("scene_dynamics.info_release_ratio 必须在 [0,1]，实际 %v", s.InfoReleaseRatio)
	}
	if s.EntropyDelta < -1 || s.EntropyDelta > 1 {
		return fmt.Errorf("scene_dynamics.entropy_delta 必须在 [-1,1]，实际 %v", s.EntropyDelta)
	}
	return nil
}

// LintSceneDynamicsTrend 对"本章 + 最近几章"的动力序列做确定性趋势检测，
// 返回违规描述（warning 语义）。history 按章号升序、不含 current。
func LintSceneDynamicsTrend(current SceneDynamics, history []SceneDynamics) []string {
	var issues []string
	seq := append(append([]SceneDynamics{}, history...), current)

	// 连续 3 章压力 < 4 → 节奏塌了
	if n := len(seq); n >= 3 {
		low := true
		for _, d := range seq[n-3:] {
			if d.PressureIndex >= 4 {
				low = false
				break
			}
		}
		if low {
			issues = append(issues, "连续 3 章压力指数 < 4：节奏疲软，建议下一章提升冲突烈度")
		}
	}

	// 本章信息释放 > 0.8 → 读者过载
	if current.InfoReleaseRatio > 0.8 {
		issues = append(issues, fmt.Sprintf("本章信息释放量 %.2f > 0.8：大量新设定涌入，读者会累", current.InfoReleaseRatio))
	}

	// 连续 5 章熵值单调上升 → 缺喘息
	if n := len(seq); n >= 5 {
		rising := true
		for i := n - 4; i < n; i++ {
			if seq[i].EntropyDelta <= seq[i-1].EntropyDelta {
				rising = false
				break
			}
		}
		if rising {
			issues = append(issues, "连续 5 章熵值单调上升：缺少喘息场景，建议安排日常/过渡块")
		}
	}

	// 近 5 章单一引擎占比 > 70% → 张力单一
	if n := len(seq); n >= 5 {
		counts := map[string]int{}
		window := seq[n-5:]
		for _, d := range window {
			counts[d.ConflictEngine]++
		}
		for engine, c := range counts {
			if float64(c)/float64(len(window)) > 0.7 {
				issues = append(issues, fmt.Sprintf("近 5 章冲突引擎 %q 占比超 70%%：张力类型单一", engine))
			}
		}
	}
	return issues
}
