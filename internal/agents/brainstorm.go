package agents

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
	"github.com/voocel/agentcore"
)

// RunBrainstorm 跑头脑风暴阶段：一个专注单代理，用 web_research/craft_recall/novel_context
// 调研并推敲小说逻辑，最终 save_brainstorm 落盘 data/runs/<书名>/brainstorm.md。
// 返回落盘的项目目录（供 pipeline 设定 OutputDir 后续跑 Architect→zero-init→写作）。
//
// 新建小说时项目目录在头脑风暴确定书名后才生成，故这里用 runsRoot（data/runs）作根，
// 由 save_brainstorm 按书名派生目录。
func RunBrainstorm(cfg bootstrap.Config, bundle assets.Bundle, runsRoot, idea string) (string, error) {
	models, err := bootstrap.NewModelSet(cfg)
	if err != nil {
		return "", fmt.Errorf("create models: %w", err)
	}
	// 暂存 store：craft_recall/web_research/novel_context 需要一个 store；新书还没项目目录，
	// 先用 runsRoot/.brainstorm-staging，落盘后真正的思路进 data/runs/<书名>/brainstorm.md。
	stagingDir := filepath.Join(runsRoot, ".brainstorm-staging")
	st := store.NewStore(stagingDir)
	_ = st.Init()

	saveBrainstorm := tools.NewSaveBrainstormTool(runsRoot)
	contextTool := tools.NewContextTool(st, bundle.References, cfg.Style)
	// craft 手法库/对标库检索（全局库；staging 无索引时优雅降级为 no_material）。
	craftRecall := tools.NewCraftRecallTool(st)
	if qc, enabled, qerr := bootstrap.NewRAGQdrantClient(cfg, false); qerr == nil && enabled {
		contextTool.WithRAGVectorSearcher(qc)
	}
	if embedder, enabled, eerr := bootstrap.NewRAGEmbedder(cfg); eerr == nil && enabled {
		contextTool.WithRAGEmbedder(embedder)
	}
	webResearch := tools.NewWebResearchTool(st)

	// 推演/策划用 architect 角色的模型（capable）；失败走其兜底。
	model := models.ForRoleWithFailover("architect", func(bootstrap.FailoverEvent) {})

	agent := agentcore.NewAgent(
		agentcore.WithModel(model),
		agentcore.WithSystemPrompt(bundle.Prompts.Brainstorm),
		agentcore.WithTools(saveBrainstorm, webResearch, craftRecall, contextTool),
		agentcore.WithMaxTurns(cfg.ResolveMaxTurns("architect", 30)),
		agentcore.WithToolsAreIdempotent(true),
		agentcore.WithMaxToolErrors(0),
		agentcore.WithMaxRetries(subagentMaxRetries),
	)
	thinking, _ := ResolveThinkingForModel(models.ForRole("architect"), roleThinking(cfg, "architect"))
	agent.SetThinkingLevel(thinking)

	if err := agent.Prompt(context.Background(), brainstormPrompt(idea)); err != nil {
		return "", fmt.Errorf("brainstorm run: %w", err)
	}
	agent.WaitForIdle()

	saved := saveBrainstorm.SavedDir()
	if saved == "" {
		return "", fmt.Errorf("头脑风暴结束但未落盘 brainstorm.md：请检查模型是否调用了 save_brainstorm")
	}
	return saved, nil
}

func brainstormPrompt(idea string) string {
	return "请对下面这个小说想法做头脑风暴：先确认逻辑、按需 web_research/craft_recall 调研，" +
		"再 save_brainstorm 落盘完整思路（书名、预期字数、题材、类型、主角与 CP、世界观、关键角色、核心爽点、给 Architect 的交接等）。\n\n[想法]\n" + idea
}
