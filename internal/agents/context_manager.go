package agents

import (
	"log/slog"

	"github.com/voocel/agentcore"
	corecontext "github.com/voocel/agentcore/context"
)

// contextManagerConfig 聚合 ContextManager 的全部配置参数。
type contextManagerConfig struct {
	Model            agentcore.ChatModel
	ContextWindow    int
	ReserveTokens    int
	KeepRecentTokens int
	Agent            string
	CommitOnProject  bool
	Summary          *corecontext.FullSummaryConfig
	ToolMicrocompact *corecontext.ToolResultMicrocompactConfig
	LightTrim        *corecontext.LightTrimConfig
	ExtraStrategies  []corecontext.Strategy
}

// protectedToolResults 是"承载本书事实、不该被 microcompact 硬删"的工具结果。
// novel_context 把世界/角色/大纲/计划/资源/伏笔全量注入 agent——硬删它会让 agent
// 失去事实基础、只能重新召回或凭空脑补（正是"压缩掉 novel_context 后 planner 失忆"
// 的根因）。需要缩容时这类结果留给 store_summary / full_summary 做"摘要而非丢弃"，
// 保住信息本体不伤质量；其余可再取的结果（read_chapter/check_consistency/craft_recall
// /web_research 等）不在此列，允许激进重写。
var protectedToolResults = map[string]bool{
	"novel_context": true,
}

// loadBearingToolClassifier 返回 true 表示该工具结果"可被激进重写（compactable）"。
// 承载性结果受保护返回 false；其余可再取的结果返回 true 允许压缩。
func loadBearingToolClassifier(toolName string) bool {
	return !protectedToolResults[toolName]
}

func newContextManager(cfg contextManagerConfig) *corecontext.ContextEngine {
	var sc corecontext.FullSummaryConfig
	if cfg.Summary != nil {
		sc = *cfg.Summary
	}
	sc.Model = cfg.Model
	if sc.KeepRecentTokens <= 0 {
		sc.KeepRecentTokens = cfg.KeepRecentTokens
	}

	var tc corecontext.ToolResultMicrocompactConfig
	if cfg.ToolMicrocompact != nil {
		tc = *cfg.ToolMicrocompact
	}
	var lc corecontext.LightTrimConfig
	if cfg.LightTrim != nil {
		lc = *cfg.LightTrim
	}

	strategies := []corecontext.Strategy{
		corecontext.NewToolResultMicrocompact(tc),
		corecontext.NewLightTrim(lc),
	}
	strategies = append(strategies, cfg.ExtraStrategies...)
	strategies = append(strategies, corecontext.NewFullSummary(sc))

	engine := corecontext.NewEngine(corecontext.EngineConfig{
		ContextWindow:   cfg.ContextWindow,
		ReserveTokens:   cfg.ReserveTokens,
		CommitOnProject: cfg.CommitOnProject,
		Strategies:      strategies,
	})

	callback := contextRewriteCallback(cfg.Agent)
	engine.SetProjectHook(callback)
	engine.SetRecoverHook(callback)
	return engine
}

// contextRewriteCallback 创建上下文重写的日志回调。
// 新架构简化为只写 slog,不再写 runtime queue 和 UIEvent。
func contextRewriteCallback(agent string) func(corecontext.RewriteEvent) {
	return func(ev corecontext.RewriteEvent) {
		attrs := []any{
			"module", "context",
			"agent", agent,
			"reason", ev.Reason,
			"strategy", ev.Strategy,
			"committed", ev.Committed,
			"tokens_before", ev.TokensBefore,
			"tokens_after", ev.TokensAfter,
		}
		if info := ev.Info; info != nil {
			attrs = append(attrs,
				"msgs_before", info.MessagesBefore,
				"msgs_after", info.MessagesAfter,
				"compacted", info.CompactedCount,
				"kept", info.KeptCount,
				"duration_ms", info.Duration.Milliseconds(),
			)
		}
		slog.Warn("上下文重写", attrs...)
	}
}
