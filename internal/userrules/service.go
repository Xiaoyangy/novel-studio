package userrules

import (
	"context"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
)

// Service 编排用户规则快照的生成与更新：归一化各来源 → 确定性合并 → 落盘。
//
// 两个调用方共用同一套逻辑：
//   - 开书/刷新（启动侧，确定性）：Build / GetOrBuild，由 Host 直接调用，不经 Coordinator。
//   - 运行中更新（Coordinator 工具）：AddRuntimeRule，save_user_rules 工具壳复用。
type Service struct {
	store     *store.Store
	norm      *Normalizer
	rulesOpts rules.LoadOptions
}

// NewService 构造服务。model 用于归一化（应为能力较强的模型）；model 为 nil 时
// 所有来源降级为 raw preferences（仍可产出快照，机械检查由 system_defaults 兜底）。
func NewService(st *store.Store, model agentcore.ChatModel, opts rules.LoadOptions) *Service {
	return &Service{store: st, norm: NewNormalizer(model), rulesOpts: opts}
}

// Build 从静态来源（system_defaults + rules 文件 + 启动 prompt）归一化生成快照并落盘。
// 开书/刷新时调用。startupPrompt 可空。
func (s *Service) Build(ctx context.Context, startupPrompt string) (*rules.Snapshot, error) {
	cands := []rules.Candidate{rules.SystemDefaults()}
	for _, rs := range rules.RawFileSources(s.rulesOpts) {
		cands = append(cands, s.norm.Normalize(ctx, rs.Label, rs.Text))
	}
	if strings.TrimSpace(startupPrompt) != "" {
		cands = append(cands, s.norm.Normalize(ctx, "startup_prompt", startupPrompt))
	}
	snap := rules.BuildSnapshot(cands)
	if err := s.store.UserRules.Save(&snap); err != nil {
		return nil, err
	}
	return &snap, nil
}

// GetOrBuild 返回当前快照；老书无快照时惰性生成（无启动 prompt 原文，故只含
// system_defaults + rules 文件）。运行时读取路径统一走这里。
func (s *Service) GetOrBuild(ctx context.Context) (*rules.Snapshot, error) {
	cur, err := s.store.UserRules.Load()
	if err != nil {
		return nil, err
	}
	if cur != nil {
		return cur, nil
	}
	return s.Build(ctx, "")
}

// AddRuntimeRule 归一化一条运行中长期规则，以最高优先级叠加到当前快照并落盘。
// 永不因归一化失败而报错——失败时该条降级为 raw preferences。
// 返回叠加后的快照与本次的归一化候选（后者供 save_user_rules 回显"理解成了什么"给用户确认）。
func (s *Service) AddRuntimeRule(ctx context.Context, text string) (*rules.Snapshot, rules.Candidate, error) {
	cur, err := s.GetOrBuild(ctx)
	if err != nil {
		return nil, rules.Candidate{}, err
	}
	cand := s.norm.Normalize(ctx, "runtime_update", text)
	merged := rules.OverlaySnapshot(*cur, cand)
	if err := s.store.UserRules.Save(&merged); err != nil {
		return nil, cand, err
	}
	return &merged, cand, nil
}
