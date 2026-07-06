package store

import (
	"fmt"
	"os"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

const (
	simulationRestartPolicyJSON = "meta/simulation_restart_policy.json"
	simulationRestartPolicyMD   = "meta/simulation_restart_policy.md"
)

// SaveSimulationRestartPolicy stores the active restart boundary. It is used
// when regenerating a book from chapter 1 while keeping old material as seeds.
func (s *Store) SaveSimulationRestartPolicy(policy domain.SimulationRestartPolicy) error {
	if err := s.Progress.io.WriteJSON(simulationRestartPolicyJSON, policy); err != nil {
		return err
	}
	return s.Progress.io.WriteMarkdown(simulationRestartPolicyMD, renderSimulationRestartPolicy(policy))
}

// LoadSimulationRestartPolicy returns nil when no restart boundary exists.
func (s *Store) LoadSimulationRestartPolicy() (*domain.SimulationRestartPolicy, error) {
	var policy domain.SimulationRestartPolicy
	if err := s.Progress.io.ReadJSON(simulationRestartPolicyJSON, &policy); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &policy, nil
}

func renderSimulationRestartPolicy(p domain.SimulationRestartPolicy) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# 推演重启策略\n\n")
	fmt.Fprintf(&b, "- 项目：%s\n", policyDash(p.Project))
	fmt.Fprintf(&b, "- 模式：%s\n", policyDash(p.Mode))
	fmt.Fprintf(&b, "- 活动：%t\n", p.Active)
	fmt.Fprintf(&b, "- generation_id：%s\n", policyDash(p.GenerationID))
	fmt.Fprintf(&b, "- canonical_start：%s\n", policyDash(p.CanonicalStart))
	if p.GeneratedAt != "" {
		fmt.Fprintf(&b, "- generated_at：%s\n", p.GeneratedAt)
	}
	writePolicyLine(&b, "旧数据用途", p.LegacyUse)
	writePolicyLine(&b, "正文状态口径", p.StoryStatePolicy)
	writePolicyLine(&b, "人物状态口径", p.CharacterStatePolicy)
	writePolicyLine(&b, "资源口径", p.ResourcePolicy)
	writePolicyLine(&b, "信息边界", p.KnowledgePolicy)
	writePolicyLine(&b, "RAG 口径", p.RAGPolicy)
	writePolicyStringList(&b, "允许作为种子的来源", p.AllowedSeedSources)
	writePolicyStringList(&b, "禁止作为新事实的来源", p.ForbiddenFactSources)
	writePolicyStringList(&b, "新推演 canonical 状态根", p.CanonicalStateRoots)
	writePolicyStringList(&b, "重启时需要归零的活动状态", p.ResetTargets)
	writePolicyStringList(&b, "来源", p.Sources)
	return b.String()
}

func writePolicyLine(b *strings.Builder, label, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	fmt.Fprintf(b, "- %s：%s\n", label, value)
}

func writePolicyStringList(b *strings.Builder, label string, values []string) {
	if len(values) == 0 {
		return
	}
	fmt.Fprintf(b, "\n## %s\n\n", label)
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			fmt.Fprintf(b, "- %s\n", value)
		}
	}
}

func policyDash(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	return value
}
