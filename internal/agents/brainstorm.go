package agents

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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
	kickoffKey := brainstormKickoffKey(cfg, bundle, idea)
	if projectDir, ok := loadBrainstormKickoff(runsRoot, kickoffKey); ok {
		return projectDir, nil
	}
	models, err := bootstrap.NewModelSet(cfg)
	if err != nil {
		return "", fmt.Errorf("create models: %w", err)
	}
	// 暂存 store：craft_recall/web_research/novel_context 需要一个 store；新书还没项目目录，
	// Staging is isolated by immutable kickoff input. Concurrent new-book jobs do
	// not share RAG traces or temporary state, and a failed run cannot contaminate
	// another idea.
	stagingDir := filepath.Join(runsRoot, ".brainstorm-staging", kickoffKey[:16])
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
		agentcore.WithToolsAreIdempotent(false),
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
	projectDir, err := filepath.Abs(saved)
	if err != nil {
		projectDir = saved
	}
	if err := saveBrainstormKickoff(runsRoot, kickoffKey, projectDir); err != nil {
		return "", fmt.Errorf("save brainstorm kickoff journal: %w", err)
	}
	return projectDir, nil
}

type brainstormKickoffJournal struct {
	InputSHA256   string `json:"input_sha256"`
	ProjectDir    string `json:"project_dir"`
	BrainstormSHA string `json:"brainstorm_sha256"`
	CompletedAt   string `json:"completed_at"`
}

func brainstormKickoffKey(cfg bootstrap.Config, bundle assets.Bundle, idea string) string {
	role := cfg.Roles["architect"]
	payload, _ := json.Marshal(struct {
		Idea             string
		Style            string
		DefaultProvider  string
		DefaultModel     string
		ArchitectRole    bootstrap.RoleConfig
		BrainstormPrompt string
	}{strings.TrimSpace(idea), cfg.Style, cfg.Provider, cfg.ModelName, role, bundle.Prompts.Brainstorm})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func brainstormKickoffPath(runsRoot, key string) string {
	return filepath.Join(runsRoot, ".pipeline-kickoffs", key+".json")
}

func loadBrainstormKickoff(runsRoot, key string) (string, bool) {
	raw, err := os.ReadFile(brainstormKickoffPath(runsRoot, key))
	if err != nil {
		return "", false
	}
	var journal brainstormKickoffJournal
	if json.Unmarshal(raw, &journal) != nil || journal.InputSHA256 != key || strings.TrimSpace(journal.ProjectDir) == "" {
		return "", false
	}
	body, err := os.ReadFile(filepath.Join(journal.ProjectDir, "brainstorm.md"))
	if err != nil {
		return "", false
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != journal.BrainstormSHA {
		return "", false
	}
	return journal.ProjectDir, true
}

func saveBrainstormKickoff(runsRoot, key, projectDir string) error {
	body, err := os.ReadFile(filepath.Join(projectDir, "brainstorm.md"))
	if err != nil {
		return err
	}
	sum := sha256.Sum256(body)
	journal := brainstormKickoffJournal{
		InputSHA256:   key,
		ProjectDir:    projectDir,
		BrainstormSHA: hex.EncodeToString(sum[:]),
		CompletedAt:   time.Now().Format(time.RFC3339),
	}
	raw, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return err
	}
	path := brainstormKickoffPath(runsRoot, key)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".kickoff-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func brainstormPrompt(idea string) string {
	return "请对下面这个小说想法做头脑风暴：先确认逻辑、按需 web_research/craft_recall 调研，" +
		"再 save_brainstorm 落盘完整思路（书名、预期字数、题材、类型、主角与 CP、世界观、关键角色、核心爽点、给 Architect 的交接等）。\n\n[想法]\n" + idea
}
