package domain

import (
	"fmt"
	"slices"
)

// POVContract 视角契约：200+ 章多 POV 长篇没有显式契约会塌。
type POVContract struct {
	RotationInterval int      `json:"rotation_interval,omitempty"` // 每 N 章应切换 POV；0 = 不约束
	Reliability      string   `json:"reliability,omitempty"`       // reliable / unreliable
	Scope            []string `json:"scope,omitempty"`             // 允许的 POV 角色；空 = 不约束
	UnreliableIn     []string `json:"unreliable_in,omitempty"`     // 不可靠的领域
}

// POVConvergence 多视角汇合点：在该章各线索汇合并揭示信息。
type POVConvergence struct {
	Chapter int      `json:"chapter"`
	POVs    []string `json:"povs"`
	Reveal  string   `json:"reveal,omitempty"`
}

// POVState 挂在 Progress 上的视角状态（History 按章追加，对齐 HookHistory 模式）。
type POVState struct {
	CurrentPOV        string           `json:"current_pov,omitempty"`
	History           []string         `json:"history,omitempty"`
	Contract          *POVContract     `json:"contract,omitempty"`
	ConvergencePoints []POVConvergence `json:"convergence_points,omitempty"`
}

// CheckPOV 对"本章使用 pov"做确定性契约检查，返回违规描述列表（warning 语义，不阻塞）。
// chapter 为本章号；无契约或未自报 pov 时返回 nil。
func (s *POVState) CheckPOV(chapter int, pov string) []string {
	if s == nil || s.Contract == nil || pov == "" {
		return nil
	}
	var issues []string
	c := s.Contract
	if len(c.Scope) > 0 && !slices.Contains(c.Scope, pov) {
		issues = append(issues, fmt.Sprintf("第%d章 POV %q 不在契约 scope %v 内", chapter, pov, c.Scope))
	}
	if c.RotationInterval > 0 && len(s.History) >= c.RotationInterval {
		tail := s.History[len(s.History)-c.RotationInterval:]
		same := true
		for _, p := range tail {
			if p != pov {
				same = false
				break
			}
		}
		if same {
			issues = append(issues, fmt.Sprintf("POV %q 已连续 %d 章未轮换（契约每 %d 章应切换）", pov, c.RotationInterval+1, c.RotationInterval))
		}
	}
	for _, conv := range s.ConvergencePoints {
		if conv.Chapter == chapter && len(conv.POVs) > 0 && !slices.Contains(conv.POVs, pov) {
			issues = append(issues, fmt.Sprintf("第%d章是汇合点（应含 %v），实际 POV %q", chapter, conv.POVs, pov))
		}
	}
	return issues
}
