package main

// --pipeline：把各功能串成一条可恢复的流水线，按阶段顺序执行。
// 阶段：cocreate → architect → zero-init → write → review → rewrite → deliver（默认不含 cocreate）。
// 可选的拆分写作路径为 preplan → plan → render；它不会改变上述默认阶段序列。
// 状态持久化到 meta/pipeline.json：已完成的阶段在重跑时自动跳过，从断点继续。
//
// 设计：流水线只做"阶段编排 + 断点续跑"，每个阶段复用已有子命令逻辑（headless.Run /
// reviewExistingPipeline / ...）。阶段内部各自还有更细的恢复（write 走 checkpoint、
// review/rewrite 按章号），两层恢复叠加。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/internal/agents"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
)

// defaultPipelineStages 不含 cocreate：默认假设已有创作指令（--prompt/brainstorm.md）。
// 新书必须先显式完成 Architect foundation，再执行 zero-init，最后才允许正文写作。
var defaultPipelineStages = []string{"architect", "zero-init", "write", "review", "rewrite", "deliver"}

var knownPipelineStages = map[string]bool{
	"cocreate": true, "architect": true, "zero-init": true,
	"preplan": true, "plan": true, "render": true,
	"write": true, "review": true, "rewrite": true, "deliver": true,
}

type pipelineFlags struct {
	Stages           string
	Prompt           string
	PromptFile       string
	Restart          bool
	Start            int
	End              int
	WriteTo          int
	StopAfterCommit  int
	Budget           time.Duration
	Role             string
	MaxRewriteRounds int
	PolishWarnings   bool
	RewriteBriefOnly bool
	ForceRerender    bool
	NewNovel         bool
	RefreshArchitect bool
	RefreshZeroInit  bool
	RenderOnly       bool
}

func parsePipelineFlags(argv []string) (pipelineFlags, []string, error) {
	fs := flag.NewFlagSet("pipeline", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "用法: novel-studio --pipeline [--prompt <text> | --prompt-file <path>] [--stages a,b,c] [--restart]\n\n")
		fmt.Fprintf(os.Stderr, "按阶段顺序跑完整流程，状态存 meta/pipeline.json，可断点续跑。\n")
		fmt.Fprintf(os.Stderr, "阶段：cocreate / architect / zero-init / preplan / plan / render / write / review / rewrite / deliver（默认 %s）\n", strings.Join(defaultPipelineStages, ","))
		fmt.Fprintf(os.Stderr, "拆分写作：preplan 生成非正史全书投影，plan 只冻结当前章正式计划，render 只按冻结计划写正文。\n\n选项：\n")
		fs.PrintDefaults()
	}
	var f pipelineFlags
	fs.StringVar(&f.Stages, "stages", "", "逗号分隔的阶段列表，缺省 "+strings.Join(defaultPipelineStages, ","))
	fs.StringVar(&f.Prompt, "prompt", "", "创作指令（write 阶段用）")
	fs.StringVar(&f.PromptFile, "prompt-file", "", "从文件读创作指令，'-' 表示 stdin")
	fs.BoolVar(&f.Restart, "restart", false, "清空已保存的流水线状态，从头重跑")
	fs.IntVar(&f.Start, "from", 0, "preplan/plan/render/review/rewrite 阶段起始章号（含），0 = 自动")
	fs.IntVar(&f.End, "to", 0, "preplan/plan/render/review/rewrite 阶段结束章号（含），0 = 自动")
	fs.IntVar(&f.WriteTo, "write-to", 0, "write 阶段写到指定章节后暂停；0 = 写到全书完结")
	fs.DurationVar(&f.Budget, "budget", 0, "review/rewrite 阶段每章 LLM 调用硬时间预算，0 = 使用阶段默认")
	fs.StringVar(&f.Role, "role", "", "rewrite 阶段调用的模型角色，空 = writer")
	fs.IntVar(&f.MaxRewriteRounds, "max-rewrite-rounds", 0, "rewrite 阶段红旗重写-复审闭环最多轮数，0 = 使用默认 3")
	fs.BoolVar(&f.PolishWarnings, "polish-warnings", false, "rewrite 阶段无红旗但存在黄旗时，也按质量优先原则择优打磨")
	fs.BoolVar(&f.RewriteBriefOnly, "brief-only", false, "rewrite 阶段只刷新 rewrite brief，不调用 Writer、不改正文")
	fs.BoolVar(&f.ForceRerender, "force-rerender", false, "rewrite 阶段显式让现有 plan 重新渲染整章；保留世界推演，不伪造外判阻断")
	fs.BoolVar(&f.NewNovel, "new-novel", false, "新建小说：先跑头脑风暴（web/RAG 调研 + 落盘 brainstorm.md），再据此初始化并写作")
	fs.BoolVar(&f.RefreshArchitect, "refresh-architect", false, "已有 foundation 仍强制 Architect 按本次 prompt 重规划开篇/大纲；只在 architect 阶段生效")
	fs.BoolVar(&f.RefreshZeroInit, "refresh-zero-init", false, "已有正文时安全刷新 zero-init 开篇计划和 RAG，不覆盖活动资源/关系台账")
	if err := fs.Parse(argv); err != nil {
		return f, nil, err
	}
	return f, fs.Args(), nil
}

// hasPipelineFlag 判断 argv 中是否含 --pipeline 入口。
func hasPipelineFlag(argv []string) bool {
	for _, a := range argv {
		if a == "--pipeline" {
			return true
		}
	}
	return false
}

func pipelinePipeline(opts cliOptions, args []string) error {
	if hasHelpToken(args) {
		_, _, _ = parsePipelineFlags([]string{"--help"})
		return nil
	}
	flags, extra, err := parsePipelineFlags(args)
	if err != nil {
		return err
	}
	if len(extra) > 0 {
		return fmt.Errorf("--pipeline 不接受额外参数：%v", extra)
	}

	stages, err := resolveStages(flags.Stages)
	if err != nil {
		return err
	}

	prompt, err := resolvePipelinePrompt(flags, opts)
	if err != nil {
		return err
	}

	// 新建小说：先跑头脑风暴，落盘 data/runs/<书名>/brainstorm.md，再把项目目录设为它，
	// 让后续 Architect 基于 brainstorm.md 初始化世界 → zero-init → 写作。
	if flags.NewNovel {
		projectDir, berr := runPipelineBrainstorm(opts, prompt)
		if berr != nil {
			return fmt.Errorf("头脑风暴阶段失败: %w", berr)
		}
		opts.Dir = projectDir  // 后续 OutputDir = projectDir/output/novel
		prompt = ""            // 创作指令改由 brainstorm.md 承载，不再透传原始 idea
		flags.NewNovel = false // 已完成
	}

	return runPipelineWithStages(opts, flags, stages, prompt, nil)
}

// runPipelineBrainstorm 跑头脑风暴子代理，产出 brainstorm.md，返回项目根目录。
func runPipelineBrainstorm(opts cliOptions, idea string) (string, error) {
	if strings.TrimSpace(idea) == "" {
		return "", fmt.Errorf("新建小说需要一个想法：用 --prompt \"<你的小说想法>\"")
	}
	cfg, bundle, err := loadCfgBundle(opts)
	if err != nil {
		return "", err
	}
	runsRoot := filepath.Join("data", "runs")
	fmt.Fprintln(os.Stderr, "[pipeline:brainstorm] 头脑风暴中：调研题材、推敲逻辑、落盘 brainstorm.md…")
	projectDir, err := agents.RunBrainstorm(cfg, bundle, runsRoot, idea)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(os.Stderr, "[pipeline:brainstorm] 已落盘：%s\n", filepath.Join(projectDir, "brainstorm.md"))
	return projectDir, nil
}

func runPipelineAlias(opts cliOptions, stages []string, prompt string, stageArgs map[string][]string) error {
	flags := pipelineFlags{Stages: strings.Join(stages, ",")}
	if reviewArgs, ok := stageArgs["review"]; ok {
		reviewFlags, extra, err := parseReviewFlags(reviewArgs)
		if err == nil && len(extra) == 0 {
			flags.Start = reviewFlags.Start
			flags.End = reviewFlags.End
			flags.Budget = reviewFlags.Budget
		}
	}
	if rewriteArgs, ok := stageArgs["rewrite"]; ok {
		rewriteFlags, extra, err := parseRewriteFlags(rewriteArgs)
		if err == nil && len(extra) == 0 {
			flags.Start = rewriteFlags.Start
			flags.End = rewriteFlags.End
			flags.Budget = rewriteFlags.Budget
			flags.Role = rewriteFlags.Role
			flags.PolishWarnings = rewriteFlags.PolishWarnings
			flags.RewriteBriefOnly = rewriteFlags.BriefOnly
		}
	}
	return runPipelineWithStages(opts, flags, stages, prompt, stageArgs)
}

func runPipelineWithStages(opts cliOptions, flags pipelineFlags, stages []string, prompt string, stageArgs map[string][]string) error {
	cfg, bundle, err := loadCfgBundle(opts)
	if err != nil {
		return err
	}
	if pipelineStagesNeedQdrant(stages) {
		if enabled, err := bootstrap.EnsureRAGQdrant(context.Background(), cfg); err != nil {
			return fmt.Errorf("pipeline 启动 Qdrant 失败: %w", err)
		} else if enabled {
			fmt.Fprintf(os.Stderr, "[pipeline] Qdrant 已就绪\n")
		}
	}
	ensureDashboardServiceForRun(cfg.OutputDir)
	statePath := filepath.Join(cfg.OutputDir, "meta", "pipeline.json")
	if stageArgs == nil {
		stageArgs = pipelineStageArgs(flags)
	}

	inputDigest := pipelineRunInputDigest(cfg, bundle)
	state, err := loadOrInitPipelineState(statePath, stages, prompt, inputDigest, flags.Restart)
	if err != nil {
		return err
	}
	// Persist restart/input invalidation before the first expensive stage. A
	// crash at stage startup must not resurrect the previous completed graph.
	if err := savePipelineState(statePath, state); err != nil {
		return fmt.Errorf("保存流水线启动状态失败: %w", err)
	}

	fmt.Fprintf(os.Stderr, "[pipeline] 阶段：%s\n", strings.Join(state.Stages, " → "))
	if len(state.Completed) > 0 {
		fmt.Fprintf(os.Stderr, "[pipeline] 已完成（将跳过）：%s\n", strings.Join(state.Completed, ", "))
	}

	for _, stage := range state.Stages {
		if state.Done(stage) {
			storedEvidence := state.Evidence[stage]
			evidence := storedEvidence
			err := verifyStoredPipelineArtifactDigests(cfg.OutputDir, storedEvidence)
			if err == nil {
				evidence, err = verifyPipelineStage(stage, cfg.OutputDir, flags, state)
			}
			if err == nil {
				evidence = stampPipelineArtifactDigests(cfg.OutputDir, evidence)
				state.MarkDone(stage, evidence)
				if err := savePipelineState(statePath, state); err != nil {
					return fmt.Errorf("保存流水线状态失败: %w", err)
				}
				fmt.Fprintf(os.Stderr, "[pipeline] 跳过已完成阶段：%s\n", stage)
				continue
			}
			evidence.Status = "stale"
			evidence.Message = err.Error()
			state.ClearDone(stage, evidence)
			fmt.Fprintf(os.Stderr, "[pipeline] 已完成阶段证据失效，重新执行 %s：%v\n", stage, err)
		}
		fmt.Fprintf(os.Stderr, "\n========== 阶段：%s ==========\n", stage)
		if stage == "write" {
			warnStaleZeroInitReadiness(cfg.OutputDir)
		}
		if err := runPipelineStage(stage, opts, flags, state, stageArgs); err != nil {
			// 不标记完成 → 下次重跑从这里继续。先落盘（可能 cocreate 已写入 prompt）。
			_ = savePipelineState(statePath, state)
			return fmt.Errorf("阶段 %s 失败（修复后重跑 --pipeline 即从此阶段继续）: %w", stage, err)
		}
		evidence, err := verifyPipelineStage(stage, cfg.OutputDir, flags, state)
		if err != nil {
			evidence.Status = "invalid"
			evidence.Message = err.Error()
			state.ClearDone(stage, evidence)
			_ = savePipelineState(statePath, state)
			return fmt.Errorf("阶段 %s 完成证据不足（未标记完成）: %w", stage, err)
		}
		evidence = stampPipelineArtifactDigests(cfg.OutputDir, evidence)
		state.MarkDone(stage, evidence)
		if err := savePipelineState(statePath, state); err != nil {
			return fmt.Errorf("保存流水线状态失败: %w", err)
		}
		fmt.Fprintf(os.Stderr, "[pipeline] 阶段完成：%s\n", stage)
	}
	// Later stages intentionally mutate earlier-stage artifacts (rewrite updates
	// chapters and re-review replaces reports). Reconcile every completed stage
	// against the final state so persisted digests describe the delivered graph,
	// not a transient midpoint that is stale by design.
	for _, stage := range state.Stages {
		if !state.Done(stage) {
			continue
		}
		evidence, verifyErr := verifyPipelineStage(stage, cfg.OutputDir, flags, state)
		if verifyErr != nil {
			evidence.Status = "stale"
			evidence.Message = verifyErr.Error()
			state.ClearDone(stage, evidence)
			_ = savePipelineState(statePath, state)
			return fmt.Errorf("pipeline 最终证据对账失败（阶段 %s）: %w", stage, verifyErr)
		}
		state.MarkDone(stage, stampPipelineArtifactDigests(cfg.OutputDir, evidence))
	}
	if err := savePipelineState(statePath, state); err != nil {
		return fmt.Errorf("保存流水线最终证据失败: %w", err)
	}

	fmt.Fprintln(os.Stderr, "\n[pipeline] 全部阶段完成 ✓")
	return nil
}

// pipelineStagesNeedQdrant keeps the phase boundary honest. A standalone
// preplan pass only derives prose-free causal projections from versioned local
// artifacts, so it must remain runnable without an embedding service or vector
// database. Every other stage preserves the existing Qdrant startup behavior;
// in particular, formal plan and render still fail closed when their RAG
// dependencies are unavailable.
func pipelineStagesNeedQdrant(stages []string) bool {
	for _, stage := range stages {
		if normalizePipelineStageName(stage) != "preplan" {
			return true
		}
	}
	return false
}

func verifyStoredPipelineArtifactDigests(outputDir string, evidence domain.PipelineStageEvidence) error {
	for artifact, expected := range evidence.ArtifactDigests {
		if strings.TrimSpace(expected) == "" {
			continue
		}
		path := artifact
		if !filepath.IsAbs(path) {
			path = filepath.Join(outputDir, filepath.FromSlash(artifact))
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("阶段产物 %s 无法读取: %w", artifact, err)
		}
		sum := sha256.Sum256(raw)
		actual := "sha256:" + hex.EncodeToString(sum[:])
		if actual != expected {
			return fmt.Errorf("阶段产物 %s 指纹漂移（expected=%s actual=%s）", artifact, expected, actual)
		}
	}
	return nil
}

func stampPipelineArtifactDigests(outputDir string, evidence domain.PipelineStageEvidence) domain.PipelineStageEvidence {
	if len(evidence.Artifacts) == 0 {
		return evidence
	}
	evidence.ArtifactDigests = make(map[string]string, len(evidence.Artifacts))
	for _, artifact := range evidence.Artifacts {
		path := artifact
		if !filepath.IsAbs(path) {
			path = filepath.Join(outputDir, filepath.FromSlash(artifact))
		}
		raw, err := os.ReadFile(path)
		if err != nil || len(raw) == 0 {
			continue
		}
		sum := sha256.Sum256(raw)
		evidence.ArtifactDigests[artifact] = "sha256:" + hex.EncodeToString(sum[:])
	}
	if len(evidence.ArtifactDigests) == 0 {
		evidence.ArtifactDigests = nil
	}
	return evidence
}

func pipelineStageArgs(flags pipelineFlags) map[string][]string {
	args := make(map[string][]string)
	var review []string
	var rewrite []string
	if flags.Start > 0 {
		value := strconv.Itoa(flags.Start)
		review = append(review, "--from", value)
		rewrite = append(rewrite, "--from", value)
	}
	if flags.End > 0 {
		value := strconv.Itoa(flags.End)
		review = append(review, "--to", value)
		rewrite = append(rewrite, "--to", value)
	}
	if flags.Budget > 0 {
		value := flags.Budget.String()
		review = append(review, "--budget", value)
		rewrite = append(rewrite, "--budget", value)
	}
	if strings.TrimSpace(flags.Role) != "" {
		rewrite = append(rewrite, "--role", flags.Role)
	}
	if flags.MaxRewriteRounds > 0 {
		rewrite = append(rewrite, "--max-rounds", strconv.Itoa(flags.MaxRewriteRounds))
	}
	if flags.PolishWarnings {
		rewrite = append(rewrite, "--polish-warnings")
	}
	if flags.RewriteBriefOnly {
		rewrite = append(rewrite, "--brief-only")
	}
	if len(review) > 0 {
		args["review"] = review
	}
	if len(rewrite) > 0 {
		args["rewrite"] = rewrite
	}
	return args
}

func runPipelineStage(stage string, opts cliOptions, flags pipelineFlags, state *domain.PipelineState, stageArgs map[string][]string) error {
	switch stage {
	case "cocreate":
		draft, err := runCoCreate(opts, state.Prompt)
		if err != nil {
			return err
		}
		state.Prompt = draft
		return nil
	case "architect":
		return pipelineArchitect(opts, flags, state)
	case "zero-init":
		return pipelineZeroInit(opts, flags, state)
	case "preplan":
		return pipelinePreplan(opts, flags)
	case "plan":
		return pipelinePlan(opts, flags)
	case "render":
		return pipelineRender(opts, flags, state)
	case "write":
		return pipelineWrite(opts, flags, state)
	case "review":
		return reviewExistingPipeline(opts, stageArgs["review"])
	case "rewrite":
		return pipelineCausalRewrite(opts, flags, state, stageArgs["review"], stageArgs["rewrite"])
	case "deliver":
		cfg, _, err := loadCfgBundle(opts)
		if err != nil {
			return err
		}
		if err := ensurePipelineRAGReady(cfg); err != nil {
			return fmt.Errorf("deliver 阶段 RAG 就绪检查失败: %w", err)
		}
		return settlePipelineDelivery(cfg.OutputDir, flags)
	default:
		return fmt.Errorf("未知阶段：%s", stage)
	}
}

// warnStaleZeroInitReadiness 写前检查零章就绪指纹（Task 052）：readiness 缺 schema_version
// 或低于当前版本时一律视为 not ready 并告警——防止旧版生成器的 ready:true 被误信。
// 这里只负责提前告警；第 1 章前的实际阻断由共享的
// tools.ZeroInitReadinessState 执行。已完成第 1 章的历史项目不会被倒逼重跑。
func warnStaleZeroInitReadiness(outputDir string) {
	data, err := os.ReadFile(filepath.Join(outputDir, "meta", "first_chapter_generation_readiness.json"))
	if err != nil {
		return // 无 readiness（未走 zero-init 的项目）不告警
	}
	var r zeroInitReadiness
	if err := json.Unmarshal(data, &r); err != nil {
		fmt.Fprintln(os.Stderr, "[pipeline] ⚠ first_chapter_generation_readiness.json 无法解析，视为 not ready，建议重跑 --zero-init")
		return
	}
	if r.SchemaVersion < zeroReadinessSchemaVersion {
		fmt.Fprintf(os.Stderr, "[pipeline] ⚠ readiness 由旧版生成器产出（schema_version=%d < %d），其 ready=%v 不可信，视为 not ready：请先重跑 --zero-init --overwrite\n", r.SchemaVersion, zeroReadinessSchemaVersion, r.Ready)
	}
}
