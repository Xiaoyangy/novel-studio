package main

// --pipeline：把各功能串成一条可恢复的流水线，按阶段顺序执行。
// 阶段：cocreate → write → review → rewrite → export（默认不含 cocreate）。
// 状态持久化到 meta/pipeline.json：已完成的阶段在重跑时自动跳过，从断点继续。
//
// 设计：流水线只做"阶段编排 + 断点续跑"，每个阶段复用已有子命令逻辑（headless.Run /
// reviewExistingPipeline / ...）。阶段内部各自还有更细的恢复（write 走 checkpoint、
// review/rewrite 按章号），两层恢复叠加。

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
)

// defaultPipelineStages 不含 cocreate：默认假设已有创作指令（--prompt）。
// 想要先共创澄清，显式 --stages cocreate,write,review,rewrite,export。
var defaultPipelineStages = []string{"write", "review", "rewrite", "export"}

var knownPipelineStages = map[string]bool{
	"cocreate": true, "write": true, "review": true, "rewrite": true, "export": true,
}

type pipelineFlags struct {
	Stages           string
	Prompt           string
	PromptFile       string
	ExportOut        string
	Overwrite        bool
	Restart          bool
	Start            int
	End              int
	WriteTo          int
	Budget           time.Duration
	Role             string
	MaxRewriteRounds int
	PolishWarnings   bool
	RewriteBriefOnly bool
}

func parsePipelineFlags(argv []string) (pipelineFlags, []string, error) {
	fs := flag.NewFlagSet("pipeline", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "用法: novel-studio --pipeline [--prompt <text> | --prompt-file <path>] [--stages a,b,c] [--restart] [--export-out <path>]\n\n")
		fmt.Fprintf(os.Stderr, "按阶段顺序跑完整流程，状态存 meta/pipeline.json，可断点续跑。\n")
		fmt.Fprintf(os.Stderr, "阶段：cocreate / write / review / rewrite / export（默认 %s）\n\n选项：\n", strings.Join(defaultPipelineStages, ","))
		fs.PrintDefaults()
	}
	var f pipelineFlags
	fs.StringVar(&f.Stages, "stages", "", "逗号分隔的阶段列表，缺省 "+strings.Join(defaultPipelineStages, ","))
	fs.StringVar(&f.Prompt, "prompt", "", "创作指令（write 阶段用）")
	fs.StringVar(&f.PromptFile, "prompt-file", "", "从文件读创作指令，'-' 表示 stdin")
	fs.StringVar(&f.ExportOut, "export-out", "", "export 阶段输出路径（后缀决定 TXT/EPUB）")
	fs.BoolVar(&f.Overwrite, "overwrite", false, "export 阶段目标文件已存在时覆盖")
	fs.BoolVar(&f.Restart, "restart", false, "清空已保存的流水线状态，从头重跑")
	fs.IntVar(&f.Start, "from", 0, "review/rewrite 阶段起始章号（含），0 = 自动")
	fs.IntVar(&f.End, "to", 0, "review/rewrite 阶段结束章号（含），0 = 自动")
	fs.IntVar(&f.WriteTo, "write-to", 0, "write 阶段写到指定章节后暂停；0 = 写到全书完结")
	fs.DurationVar(&f.Budget, "budget", 0, "review/rewrite 阶段每章 LLM 调用硬时间预算，0 = 使用阶段默认")
	fs.StringVar(&f.Role, "role", "", "rewrite 阶段调用的模型角色，空 = writer")
	fs.IntVar(&f.MaxRewriteRounds, "max-rewrite-rounds", 0, "rewrite 阶段红旗重写-复审闭环最多轮数，0 = 使用默认 3")
	fs.BoolVar(&f.PolishWarnings, "polish-warnings", false, "rewrite 阶段无红旗但存在黄旗时，也按质量优先原则择优打磨")
	fs.BoolVar(&f.RewriteBriefOnly, "brief-only", false, "rewrite 阶段只刷新 rewrite brief，不调用 Writer、不改正文")
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

	return runPipelineWithStages(opts, flags, stages, prompt, nil)
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
	cfg, _, err := loadCfgBundle(opts)
	if err != nil {
		return err
	}
	if enabled, err := bootstrap.EnsureRAGQdrant(context.Background(), cfg); err != nil {
		return fmt.Errorf("pipeline 启动 Qdrant 失败: %w", err)
	} else if enabled {
		fmt.Fprintf(os.Stderr, "[pipeline] Qdrant 已就绪\n")
	}
	ensureDashboardServiceForRun(cfg.OutputDir)
	statePath := filepath.Join(cfg.OutputDir, "meta", "pipeline.json")
	if stageArgs == nil {
		stageArgs = pipelineStageArgs(flags)
	}

	state, err := loadOrInitPipelineState(statePath, stages, prompt, flags.Restart)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "[pipeline] 阶段：%s\n", strings.Join(state.Stages, " → "))
	if len(state.Completed) > 0 {
		fmt.Fprintf(os.Stderr, "[pipeline] 已完成（将跳过）：%s\n", strings.Join(state.Completed, ", "))
	}

	for _, stage := range state.Stages {
		if state.Done(stage) {
			evidence, err := verifyPipelineStage(stage, cfg.OutputDir, flags, state)
			if err == nil {
				state.MarkDone(stage, evidence)
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
		state.MarkDone(stage, evidence)
		if err := savePipelineState(statePath, state); err != nil {
			return fmt.Errorf("保存流水线状态失败: %w", err)
		}
		fmt.Fprintf(os.Stderr, "[pipeline] 阶段完成：%s\n", stage)
	}

	fmt.Fprintln(os.Stderr, "\n[pipeline] 全部阶段完成 ✓")
	return nil
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
	case "write":
		return pipelineWrite(opts, flags, state)
	case "review":
		return reviewExistingPipeline(opts, stageArgs["review"])
	case "rewrite":
		return rewriteExistingPipeline(opts, stageArgs["rewrite"])
	case "export":
		if err := exportPipeline(opts, pipelineExportArgs(flags)); err != nil {
			return err
		}
		cfg, _, err := loadCfgBundle(opts)
		if err != nil {
			return err
		}
		return settlePipelineDelivery(cfg.OutputDir, flags)
	default:
		return fmt.Errorf("未知阶段：%s", stage)
	}
}

func pipelineExportArgs(flags pipelineFlags) []string {
	var exportArgs []string
	if strings.TrimSpace(flags.ExportOut) != "" {
		exportArgs = []string{"--out", flags.ExportOut}
	}
	if flags.Overwrite {
		exportArgs = append(exportArgs, "--overwrite")
	}
	return exportArgs
}

// warnStaleZeroInitReadiness 写前检查零章就绪指纹（Task 052）：readiness 缺 schema_version
// 或低于当前版本时一律视为 not ready 并告警——防止旧版生成器的 ready:true 被误信。
// 只告警不阻塞：readiness 是零章推演资产的门禁，不是 pipeline 的硬前置。
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
